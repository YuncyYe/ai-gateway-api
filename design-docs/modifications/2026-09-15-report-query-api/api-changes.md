# 报表查询 API（Report Query API）接口变更

> 指标口径对齐现有 Grafana Dashboard 面板（QPS、错误率、Token 吞吐、平均/分位延迟、成本等口径与面板 SQL 同源）。

## 1. 通用约定

- **前缀**：统一挂 `/open-api/v1`，即 `/open-api/v1/report/*`。
- **响应信封**：`{"ErrNum":0,"ErrMsg":"","Data":...}`（`lib/xreq` 惯例）。
- **时间参数**：Unix 秒（与 operation-logs 一致），`start`/`end` 必填，`end > start`，窗口上限 7 天（与明细保留期对齐，超限报参数错误）。
- **过滤项**（各端点共有，query 参数，可组合）：

| 参数 | 类型 | 说明 |
|------|------|------|
| `models` | string | 路由模型（`ai_target_model`）列表，逗号分隔 |
| `requested_models` | string | 请求模型（`ai_requested_model`）列表，逗号分隔（明细端点支持） |
| `apikey_ids` | string | API Key ID 列表，逗号分隔 |
| `providers` | string | 上游提供商（`ai_provider`）列表，逗号分隔 |
| `hosts` | string | 主机标识（`hostid`）列表，逗号分隔 |
| `stream` | bool | 是否流式（`ai_stream`） |
| `status_codes` | string | 响应状态码列表，逗号分隔，如 `200,500` |

- **分页**（仅 `/report/logs`）：`page`（从 1 起）、`page_size`（默认 20，上限 100）。
- **鉴权**：所有端点 `Authorizer: iauth.FA(iauth.FeatureReport, iauth.ActionReadAll)`；无权限返回 402。
- **后端降级**：MySQL 后端的分位数字段（`latency_p50/p90/p99`）恒不存在，前端按字段有无降级；Doris 后端返回。两后端响应**结构一致**，差异仅在可选字段的有无。

## 2. 端点清单

### 2.1 GET /report/overview — 总览指标卡

时序/排行/分布的数据源为聚合表合计；明细总量 `COUNT(*)` 走明细表（与 Grafana「日志明细」面板口径一致）。

请求参数：共有过滤项 + `start`、`end`。

响应 `Data`：

```json
{
  "request_total": 152300,
  "error_total": 1200,
  "error_rate": 0.00788,
  "input_tokens": 88341233,
  "output_tokens": 12093441,
  "total_tokens": 100434674,
  "latency_avg_ms": 1234.5,
  "latency_max_ms": 9876,
  "latency_p50_ms": 1100,
  "latency_p90_ms": 2100,
  "latency_p99_ms": 4500,
  "ttft_avg_ms": 320.4,
  "tpot_avg_ms": 25.1,
  "cost": [{"currency": "USD", "value": 15230000}, {"currency": "RMB", "value": 98000}],
  "rate_limit_hits": 320,
  "auth_rejects": 45,
  "logs_total": 152300
}
```

口径：错误率 = `error_count/request_count`；平均延迟 = `all_time_sum/request_count`；TTFT/TPOT 平均 = `ttft_us_sum/stream 请求数`（微秒→毫秒）；成本按币种分组返回定点整数原值（前端按 currency 格式化）。

### 2.2 GET /report/timeseries — 时序数据

请求参数：共有过滤项 + `start`、`end` + `metric`（必填，见下）。bucket 由服务端按窗口自动计算（≤6h→1min，≤3d→5min，≤7d→30min），客户端不传。

| metric | 含义 | 值字段 |
|--------|------|--------|
| `qps` | 请求 QPS | `value` |
| `tokens` | Token 吞吐（个/秒） | `input`、`output`、`total` |
| `latency` | 延迟（毫秒） | `avg`、`max`（+ Doris 后端 `p50`、`p90`、`p99`） |
| `ttft` / `tpot` | 首 Token / 每 Token 延迟（毫秒，聚合于 stream 请求） | `avg` |
| `cost` | 成本增速（定点整数/秒） | 按币种多条序列，`currency` 字段区分 |

响应 `Data`：

```json
{"bucket_sec": 60, "series": [{"time": 1782345600, "value": 12.3}]}
```

`tokens`/`latency`/`cost` 等多值 metric 的每条 series 元素含多个值字段（如 `{"time":..,"input":..,"output":..,"total":..}`）；`cost` 按币种拆多条序列时增加 `currency` 字段。

### 2.3 GET /report/rankings — TopN 维度排行

请求参数：共有过滤项 + `start`、`end` + `dimension`（必填）+ `limit`（默认 10，上限 50）。

| dimension | 维度列 | 排序指标 |
|-----------|--------|----------|
| `model` | `ai_target_model` | request_count |
| `requested_model` | `ai_requested_model` | request_count |
| `provider` | `ai_provider` | request_count |
| `apikey` | `ai_apikey_id` | request_count |
| `host` | `hostid` | request_count |
| `status` | `res_status_code` | request_count |
| `protocol` | `ai_protocol` | request_count |
| `mode` | `ai_mode` | request_count |

响应 `Data`：

```json
{"items": [{"name": "gpt-4o", "request_count": 90000, "error_count": 500, "input_tokens": 55000000, "output_tokens": 7000000}]}
```

### 2.4 GET /report/distribution — 占比分布（饼图）

请求参数：共有过滤项 + `start`、`end` + `dimension`（必填）。dimension ∈ `status` | `protocol` | `mode` | `stream`。

响应 `Data`：

```json
{"items": [{"name": "200", "request_count": 150000, "ratio": 0.985}, {"name": "500", "request_count": 2300, "ratio": 0.015}]}
```

`ratio` 为该维度值请求数 / 窗口请求总数；空值维度（NULL/''）归一为 `"unknown"` 桶返回。

### 2.5 GET /report/logs — 日志明细分页

读明细表 `bfe_ai_request_log`（与 Grafana「日志明细」面板对齐），JSON 列原样返回字符串由前端展开。

请求参数：共有过滤项（含 `requested_models`）+ `start`、`end` + `err_only`（bool，只看 `err_code` 非空）+ `keyword`（`err_msg` LIKE 模糊匹配，长度上限 128）+ `page`/`page_size`。按 `log_time` 倒序。

响应 `Data`：

```json
{
  "total": 152300,
  "page": 1,
  "page_size": 20,
  "items": [{
    "logid": 12345, "log_time": 1782345600, "hostid": "gw-01", "product": "BFE",
    "ai_apikey_id": "key-001", "ai_requested_model": "gpt-4", "ai_target_model": "gpt-4o",
    "ai_provider": "openai", "ai_protocol": "openai", "ai_mode": "chat", "ai_stream": 1,
    "res_status_code": 200, "err_code": null, "err_msg": "ok",
    "ai_input_tokens": 1000, "ai_output_tokens": 200, "ai_total_tokens": 1200,
    "all_time": 1200, "ai_ttft_us": 500000, "ai_tpot_us": 25000,
    "ai_cost_value": 5000, "ai_cost_currency": "USD",
    "ai_rate_limit_hits": null, "ai_auth_reject_quota_plans": null,
    "level1Name": "dep", "level1": "ops",
    "client_ip": "10.0.0.1", "header_host": "api.example.org", "origin_uri": "/v1/chat",
    "req_headers": null, "res_headers": null
  }]
}
```

`items` 为明细行的投影（89 列中面向展示的子集 + JSON 列原文），字段名与表列名一致。

## 3. 错误码

| 场景 | ErrNum | HTTP |
|------|--------|------|
| 参数缺失/非法（start≥end、窗口超 7 天、metric/dimension 非法值、page_size 超上限） | 422（Param Illegal） | 422 |
| 未认证 / 无 FeatureReport 权限 | 现有鉴权错误码 | 401 / 402 |
| Report 模块未装配（`[Report]` 缺省） | 路由不存在 | 404 |
| 后端查询失败 | 内部错误码 | 500 |
