# EPP 运行时参数：API 接口变更说明

> 本文档描述 `ai-gateway-api` 为接入 EPP 运行时参数（`epp_runtime`）所做的接口契约变更。设计见本目录 `design-changes.md`；摘要见本目录 `change-summary.md`。

## 1. 变更范围

| 接口 | 类型 | 变更 |
|------|------|------|
| `POST /open-api/v1/clusters` | OpenAPI | 请求体新增可选对象 `epp_runtime`（`check` / `timeout`）；响应体新增 `epp_runtime` |
| `PATCH /open-api/v1/clusters/{cluster_name}` | OpenAPI | 请求体新增可选对象 `epp_runtime`（更新语义见 §2.4） |
| `GET /open-api/v1/clusters`、`GET /open-api/v1/clusters/{cluster_name}` | OpenAPI | 响应体新增 `epp_runtime`（原样回读存储值） |
| `GET /inner-api/v1/configs/tls_conf/server_data_conf` | InnerAPI | `ClusterConf.Config.<cluster>.GslbBasic` 新增 `EPPCheck` / `EPPTimeout`，并在 EPP 模式下固定输出 `EPPTLS` / `EPPBreaker` |

无新增端点、无删除端点、无字段重命名。变更向后兼容（新增可选字段）。

---

## 2. OpenAPI 变更：`/clusters` 的 `epp_runtime`

### 2.1 字段变更清单

新增请求/响应对象 `epp_runtime`：

| 字段 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `epp_runtime.check` | object | N | — | EPP 后台健康检查与失败滞回配置 |
| `epp_runtime.check.disabled` | bool | N | `false` | 是否关闭后台健康检查（不建议生产关闭） |
| `epp_runtime.check.check_interval` | string(duration) | N | `"2s"` | 健康检查探活间隔 |
| `epp_runtime.check.fail_threshold` | int | N | `3` | 主地址连续失败次数阈值，达到后触发切换 |
| `epp_runtime.check.cooldown` | string(duration) | N | `"45s"` | 冷却期内不切回已失败地址 |
| `epp_runtime.check.success_threshold` | int | N | `2` | 连续成功次数阈值，达到后切回高优先级地址 |
| `epp_runtime.timeout` | object | N | — | EPP 调用超时 |
| `epp_runtime.timeout.connect` | string(duration) | N | `"500ms"` | 建立 gRPC 连接/流超时 |
| `epp_runtime.timeout.call` | string(duration) | N | `"3s"` | 首条消息（RequestHeaders 收发）超时 |

> 默认值均在与 BFE 加载期默认对齐（`bfe/.../cluster_conf_load.go:434-445`），语义与单位同 BFE。

**不对外开放（出现在请求中即 422）**：

- `epp_runtime.tls`（`EPPTLS` 固定明文，见 §3.2）。
- `epp_runtime.breaker`（`EPPBreaker` 固定 `disabled=true`，见 §3.2）。

### 2.2 合法性条件与约束（api 侧强制，复刻 BFE）

| 字段 | 合法性条件 |
|------|------------|
| `check.check_interval` / `check.cooldown` | 合法 Go duration 字符串，且 `> 0` |
| `check.fail_threshold` | `>= 1` |
| `check.success_threshold` | `>= 1` |
| `timeout.connect` / `timeout.call` | 合法 Go duration 字符串，且 `> 0` |
| 未知字段（含 `tls` / `breaker`） | 拒绝，返回 `422`（`DisallowUnknownFields`） |

- 校验规则与 BFE `EPPCheckConfCheck` / `EPPTimeoutConfCheck`（`cluster_conf_load.go:969-1047`）逐条对齐，防止"控制面放行、BFE 加载失败"的配置穿透。
- `epp_runtime` 为整体对象：**非空即做完整校验**，与 `balance_mode` 无关（与 `epp_config` 的休眠保留语义一致）。
- 条件必填：无（`epp_runtime` 整体可选，缺省时 EPP cluster 仍可导出固定 TLS/Breaker，`check`/`timeout` 由 BFE 补默认）。

### 2.3 数据模型示例

创建 / 详情请求体示例：

```json
{
    "name": "my-epp-cluster",
    "balance_mode": "EPP",
    "epp_config": { "...既有 EPP 调度配置，保持不变..." },
    "epp_runtime": {
        "check": {
            "disabled": false,
            "check_interval": "2s",
            "fail_threshold": 3,
            "cooldown": "45s",
            "success_threshold": 2
        },
        "timeout": {
            "connect": "500ms",
            "call": "3s"
        }
    }
}
```

> 上述示例取值即控制面默认值；省略任一子字段时按 §2.1 默认补齐后落盘。

响应体新增字段（`GET /clusters/{name}`，`epp_runtime` 原样回读）示例：

```json
{
    "name": "my-epp-cluster",
    "balance_mode": "EPP",
    "epp_config": { "...": "..." },
    "epp_runtime": {
        "check": {
            "disabled": false,
            "check_interval": "2s",
            "fail_threshold": 3,
            "cooldown": "45s",
            "success_threshold": 2
        },
        "timeout": { "connect": "500ms", "call": "3s" }
    }
}
```

- 从未配置过 `epp_runtime` 时，响应中 `epp_runtime` 为 `null`。

### 2.4 更新语义（`PATCH /clusters/{cluster_name}`）

- `epp_runtime` 为**整体替换**语义：请求中提供 `epp_runtime`（非 `null` / 非空对象）时，以请求值（补默认后）整体替换存储值；**不提供**时保持存储值不变（DAO nil-skip，见 `sys-design/details/部分更新语义与DAO-nil-skip约定.md`）。
- `epp_runtime` 与 `balance_mode` 相互独立：可在一次 PATCH 中同时变更，也可单独变更。
- `epp_runtime` 与 `epp_config` 相互独立：`epp_runtime` 在 `balance_mode=WRR` 时**允许保留（休眠）**，非空即校验，但仅 `balance_mode=EPP` 时导出。
- 变更 `epp_runtime` 会 bump `server_data_conf` version（`data_sign` 覆盖整份导出），conf-agent 自动感知下发。

---

## 3. InnerAPI 变更：`server_data_conf` 的 `GslbBasic` EPP 字段

导出载体：`ClusterConf.Config.<cluster>.GslbBasic`（见 `design-docs/api-define/InnerAPI接口定义/server-data-conf.md`）。仅 `BalanceMode=EPP`（分配有效）时输出 EPP 运行时字段；分配缺失降级为 `WRR` 时输出 `BalanceMode=WRR` 且不输出 EPP 运行时字段。

### 3.1 新增字段

| BFE 字段 | 类型 | 来源 | 说明 |
|----------|------|------|------|
| `GslbBasic.EPPCheck` | object | OpenAPI `epp_runtime.check` | 未配置 `epp_runtime` 时不下发，BFE 补默认 |
| `GslbBasic.EPPCheck.Disabled` | bool | `check.disabled` | |
| `GslbBasic.EPPCheck.CheckInterval` | string | `check.check_interval` | |
| `GslbBasic.EPPCheck.FailThreshold` | int | `check.fail_threshold` | |
| `GslbBasic.EPPCheck.Cooldown` | string | `check.cooldown` | |
| `GslbBasic.EPPCheck.SuccessThreshold` | int | `check.success_threshold` | |
| `GslbBasic.EPPTimeout` | object | OpenAPI `epp_runtime.timeout` | 未配置 `epp_runtime` 时不下发，BFE 补默认 |
| `GslbBasic.EPPTimeout.Connect` | string | `timeout.connect` | |
| `GslbBasic.EPPTimeout.Call` | string | `timeout.call` | |

### 3.2 固定值语义（不对用户暴露）

| BFE 字段 | 固定值 | 说明 |
|----------|--------|------|
| `GslbBasic.EPPTLS` | `{"Plaintext": true}` | 统一明文接入 EPP；`Insecure`/`CAFile` 恒为 false/空。取代 BFE legacy `{Insecure:true}` 默认，消除 warning 并使 EPP 明文可达。 |
| `GslbBasic.EPPBreaker` | `{"Disabled": true}` | 关闭熔断；`WindowSize`/`MinVolume`/`ErrorRatePercent`/`OpenTimeout` 不下发（BFE 补默认但不生效）。 |

> BFE 中 `EPPTLSConf.Plaintext`、`EPPBreakerConf.Disabled` 为 `bool` **值类型**（非指针），导出时直接赋值。

### 3.3 变更后示例（EPP cluster）

```json
{
    "ClusterConf": {
        "Version": "00010101000000",
        "Config": {
            "my-epp-cluster": {
                "BackendConf": { "...": "..." },
                "CheckConf": { "...": "..." },
                "GslbBasic": {
                    "CrossRetry": 0,
                    "RetryMax": 2,
                    "HashConf": { "...": "..." },
                    "BalanceMode": "EPP",
                    "EPPAddr": ["10.0.0.1:8443", "10.0.0.2:8443"],
                    "EPPTLS": { "Plaintext": true },
                    "EPPCheck": {
                        "Disabled": false,
                        "CheckInterval": "2s",
                        "FailThreshold": 3,
                        "Cooldown": "45s",
                        "SuccessThreshold": 2
                    },
                    "EPPTimeout": { "Connect": "500ms", "Call": "3s" },
                    "EPPBreaker": { "Disabled": true }
                },
                "ClusterBasic": { "...": "..." },
                "AIConf": { "...": "..." }
            }
        }
    }
}
```

---

## 4. 文档变更

| 文档 | 变更 |
|------|------|
| `design-docs/api-define/OpenAPI接口定义/clusters.md` | §1 数据模型增 `epp_runtime` 示例与「表：EPP运行时配置」；创建字段表新增 `epp_runtime` 行；§2.4 更新规则新增 `epp_runtime` 行；约束段新增校验与默认值 |
| `design-docs/api-define/InnerAPI接口定义/server-data-conf.md` | §3.2 增 `GslbBasic` EPP 字段说明；§3.3 补充固定明文 / 固定熔断语义 |
| `design-docs/sys-design/数据库设计文档.md` | `clusters` 表增 `epp_runtime` 列 |
| `design-docs/sys-design/details/EPP调度对接.md` | 增「EPP 运行时参数」小节 |
| `CHANGELOG.md` | 版本发布时补充 |

---

## 5. 边界语义

| 场景 | 行为 |
|------|------|
| 未配置 `epp_runtime` 的 WRR cluster | 导出与现状完全一致（不输出 EPP 运行时字段） |
| 未配置 `epp_runtime` 的 EPP cluster（分配有效） | 输出固定 `EPPTLS{Plaintext:true}` 与 `EPPBreaker{Disabled:true}`；`EPPCheck`/`EPPTimeout` 不下发（BFE 补默认）。**首次生效会产生一次 version bump / reload** |
| EPP cluster 分配缺失（导出降级） | 输出 `BalanceMode=WRR` 且不输出任何 EPP 运行时字段（与既有降级语义一致） |
| `epp_runtime` 携带 `tls` 或 `breaker` | `422`（未知字段） |
| `epp_runtime` 非法 duration / 阈值越界 | `422`，错误信息指明字段路径 |
| `balance_mode=WRR` 且携带 `epp_runtime` | 接受并落盘（休眠），非空即校验；不导出 |
| `EPP → WRR` 切换 | 保留 `epp_runtime`（休眠）；切回 EPP 继续生效 |