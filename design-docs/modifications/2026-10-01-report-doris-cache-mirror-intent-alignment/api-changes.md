# Doris 报表后端对齐缓存/镜像/意图字段（二期）：API 变更说明

> 对应 `design-docs/api-define/OpenAPI接口定义/report.md` 的计划修改内容
> （实现阶段按本文落地并核对行号）。本期**无新增/删除参数或字段**，全部为
> 既有枚举值的可用后端标注变化与 Doris 后端行为修复（422/列不存在 → 正常返回），
> 响应结构保持不变，无 breaking change。

## 1. 变更总览

| 端点 | 变更（仅 Backend=doris 的行为变化） |
|------|------|
| GET /report/overview | 缓存/镜像/意图新指标由"触碰新列报错"转为正常返回（依赖 Doris 侧先加列） |
| GET /report/timeseries | `dimension = ai_intent_answer / ai_cache_status / mirror_hit` 由 422 转为正常返回数据 |
| GET /report/rankings | 同上三维度由 422 转为正常返回数据 |
| GET /report/distribution | 同上三维度由 422 转为正常返回数据 |
| GET /report/logs | 明细新 10 列与 5 个新过滤参数在 Doris 后端正常可用（一期已交付，依赖 Doris 侧先加列后不再报错） |

Backend=mysql 与缺省（未装配）部署的所有端点行为**零变化**。

## 2. 枚举可用后端标注修订

### 2.1 dimension（rankings / distribution / timeseries 维度参数）

| 值 | 聚合表列 | 可用后端（一期） | 可用后端（二期交付后） |
|----|----------|------------------|------------------------|
| `ai_intent_answer` | `ai_intent_answer` | mysql；doris 422 | **mysql / doris** |
| `ai_cache_status` | `ai_cache_status` | mysql；doris 422 | **mysql / doris** |
| `mirror_hit` | `mirror_hit` | mysql；doris 422 | **mysql / doris** |

### 2.2 metric（timeseries 指标参数）

`cache_tokens` 一期已在两后端交付（Doris 走聚合表既有 SUM 列），本期无变化，
仅随 Doris 明细/聚合表加列后链路完整可用。

## 3. 错误码表修订

移除以下条目（Doris 后端三新维度不再触发 422）：

```
422  dimension <name> not supported by doris backend (mysql only until doris support lands)
```

保留的 4xx 语义不变：未装配 `/report/*` 返回 404；无报表读权限返回 402；
非法 dimension/metric 参数返回 422（`xerror` PARAM → ErrNum=422、HTTP 422；
参数级校验先于后端能力校验）。

## 4. 不变项与既有差异声明

- 请求参数、响应 JSON 结构、字段命名、鉴权（`FeatureReport` × `ReadAll`）
  全部不变；
- 两后端既有固有差异保留声明：响应延迟分位数（`latency_p50/p90/p99`）
  仅 Doris 后端返回（明细表 `PERCENTILE_APPROX`），MySQL 后端恒无——
  v0.7 开放问题（MySQL 分位数是否做明细抽样近似）本期不推进；
- 时区口径两后端一致（`2026-09-15-report-fixes` 方案），本期不触碰；
- 空值归一口径（distribution 的 `"unknown"` 桶、`mirror_hit` 的
  `"0"/"1"` 桶、rankings 空值不进排行）与 MySQL 后端一致，前端无感。

## 5. 配置契约（随本期文档化，非接口变更）

`[Report].Backend = "doris"` 的标准形态配置样例（`Databases` 中 Doris 数据源 +
`Backend/Datasource` 两项生效、`EnableAggregateJob`/`RetentionDays`/
`EnablePartitionMgmt` 三项不生效）见 change-summary.md §3 与设计稿 §3.2；
仅改配置即可在 MySQL/Doris 两种报表形态间切换，API 与前端零改动。
