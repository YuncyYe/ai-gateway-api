# 报表查询新增 ClickHouse 后端：API 变更说明

> 对应 `design-docs/api-define/OpenAPI接口定义/report.md` 的计划修改内容
> （实现阶段按本文落地并核对行号）。本期**无新增/删除参数或字段**，
> 五端点路由、请求参数、响应 JSON 结构、字段命名、鉴权
> （`FeatureReport` × `ReadAll`）全部不变；唯一的变化是新增
> `Backend = "clickhouse"` 形态下五端点全部可用（含延迟分位数），
> 对既有 mysql / doris / 缺省（未装配）形态**零行为变化**。

## 1. 变更总览

| 端点 | Backend=clickhouse 形态（本期新增） |
|------|------|
| GET /report/overview | 全量指标返回；延迟分位数 `latency_p50_ms/p90/p99` 由明细表 `quantile(0.5/0.9/0.99)(all_time)` 三独立列（t-digest）计算返回 |
| GET /report/timeseries | 全量 metric 可用；`dimension = ai_cache_status / mirror_hit / ai_intent_answer` 三维下钻可用（`Capabilities()` 声明全量，manager 门控自动放行） |
| GET /report/rankings | 全量 dimension 可用，空值不进排行、`mirror_hit` 为 `"0"/"1"` 桶的归一口径与 doris 一致 |
| GET /report/distribution | 全量 dimension 可用，空值归一 `"unknown"` 桶口径与 doris 一致 |
| GET /report/logs | 明细 47 列投影与全部过滤参数（含 `cache_status` / `mirror_hit` / `intent_answer` 等一期新过滤）在 CH 明细表正常可用 |

Backend=mysql / doris / 缺省部署的所有端点行为**零变化**。

## 2. 能力矩阵修订（report.md 表格标注）

| 能力 | MySQL | Doris | ClickHouse（本期交付） |
|------|-------|-------|------------------------|
| overview 基础指标 / logs / timeseries / rankings / distribution | ✅ | ✅ | ✅ |
| overview 延迟分位数 P50/P90/P99 | ❌ | ✅ `PERCENTILE_APPROX` | ✅ `quantiles`（t-digest 近似） |
| 缓存/镜像/意图三维度下钻 | ✅ | ✅ | ✅ |

说明：ClickHouse 分位数与 Doris 同为近似算法族但实现不同（t-digest vs
PERCENTILE_APPROX），响应结构（`latency_p50_ms/p90/p99` 字段）与 Doris 完全一致，
前端无感；跨引擎数值为"近似一致"（比对容差 ≤5%，见 design-changes.md §6）。

## 3. report.md 标注更新点

1. 开头引用块"查询后端通过 `[Report].Backend` 配置切换 MySQL（轻量形态）或
   Doris（标准形态）"——扩展为三种形态（新增 ClickHouse 标准形态说明）；
2. 延迟分位数说明处"仅 Doris 返回，MySQL 前端降级 avg/max"——修订为
   "MySQL 恒不返回；Doris / ClickHouse 返回（均为近似算法，数值不承诺跨引擎相等）"；
3. 配置契约处新增 ClickHouse 形态样例引用（指向
   `design-docs/modifications/2026-10-02-report-clickhouse-backend/`）。

## 4. 错误码表

无新增、无删除、无语义变化：

- 未装配（`Backend = ""`）`/report/*` 返回 404；
- 无报表读权限返回 402；
- 非法 metric/dimension/参数级校验返回 422（`xerror` PARAM → ErrNum=422，
  先于后端能力校验；与既有五端点行为一致）；
- dimension 不被后端 `Capabilities().SupportedDimensions` 声明时返回 422
  ——clickhouse 后端声明全量维度，该 422 仅在枚举非法时不会出现
  （与 doris 对齐后的行为一致）。

## 5. 不变项与既有差异声明

- 请求参数、响应 JSON 结构、字段命名、鉴权（`FeatureReport` × `ReadAll`）
  全部不变；
- `BucketSeconds()` 桶宽规则（≤6h→60s、≤3d→300s、≤7d→1800s）、7 天窗口上限、
  明细投影列清单与过滤参数集，全部不变；
- 两后端既有固有差异保留：MySQL 无分位数（v0.7 开放问题本期不推进）；
- 时区口径（`2026-09-15-report-fixes` 方案）本期延续：入参/出参皆 unix 秒，
  CH 按 epoch 比较天然免疫会话时区；
- 空值归一口径（distribution `"unknown"` 桶、`mirror_hit` `"0"/"1"` 桶、
  rankings 空值不进排行）与 doris 后端一致，前端无感。

## 6. 配置契约（随本期新增）

`[Report].Backend = "clickhouse"` 形态配置样例（`Databases` 中
`clickhouse_db` 数据源 + `Driver = "clickhouse"` + `Backend/Datasource` 生效、
三个 JOB 选项不生效）见 design-changes.md §3；仅改配置即可在三种报表形态间
切换，API 与前端零改动。
