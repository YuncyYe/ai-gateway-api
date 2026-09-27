# Report 体现 ai-cache / 流量镜像 / ai-intent 字段：设计变更（ai-gateway-api）

> 权威分期与跨仓链路见设计稿《report 体现缓存镜像意图字段设计》（v0.8/report）。
> 本文只描述 ai-gateway-api 仓库内的具体设计变更。

## 1. 报表库 schema 变更（`db_ddl_report_mysql.sql`）

### 1.1 明细表 `bfe_ai_request_log` +10 列

| 列 | 类型 | 来源 proto | 说明 |
|----|------|-----------|------|
| `ai_cache_status` | VARCHAR(16) NOT NULL DEFAULT '' | 789 | hit/miss/skip；空=未启用 |
| `mirror_hit` | TINYINT(1) NOT NULL DEFAULT 0 | 842 | 是否被镜像 |
| `mirror_cluster` | VARCHAR(128) NOT NULL DEFAULT '' | 843 | 镜像目标集群名 |
| `ai_intent_question` | VARCHAR(64) NOT NULL DEFAULT '' | 803 | 消费的问题名 |
| `ai_intent_answer` | VARCHAR(64) NOT NULL DEFAULT '' | 804 | 答案，含 unknown |
| `ai_intent_confidence` | DOUBLE | 805 | 门控后置信度 |
| `ai_intent_source` | VARCHAR(32) NOT NULL DEFAULT '' | 806 | explicit_header/classifier/cache |
| `ai_intent_latency_us` | BIGINT | 807 | 决策耗时（μs） |
| `ai_intent_cache_hit` | TINYINT(1) | 808 | 意图缓存命中 |
| `ai_intent_questions_version` | VARCHAR(32) NOT NULL DEFAULT '' | 809 | questions 配置版本 |

风格与既有列保持一致（NOT NULL DEFAULT 填充、空值语义）；列序追加在既有
AI 列段末尾，与 log-reader `mod_log_mysql/field_mapper.go` 扩列后的列序
一致（三处同 PR 评审：log-reader mapper、MySQL DDL、Doris DDL）。

### 1.2 聚合表 `bfe_ai_metrics_1m` +3 维度列

| 列 | 类型 | 维度常量 |
|----|------|----------|
| `ai_cache_status` | VARCHAR(16) NOT NULL DEFAULT '' | `DimensionCacheStatus` |
| `mirror_hit` | TINYINT(1) NOT NULL DEFAULT 0 | `DimensionMirrorHit` |
| `ai_intent_answer` | VARCHAR(64) NOT NULL DEFAULT '' | `DimensionIntentAnswer` |

普通 ALTER（非 KEY 变更，MySQL 无 AGGREGATE KEY 概念）；既有维度列风格同上。

## 2. 聚合 JOB 变更（`storage/mysqlreport/job.go`）

- `aggregateInsertColumns` 增加 3 列；
- 聚合 INSERT SELECT 的 GROUP BY 增加 3 列（来源为明细表同名列）；
- 分钟 JOB 的 DELETE+INSERT SELECT 事务语义不变，新维度从上线后分钟起积累。

## 3. 查询模型变更（`model/ireport/types.go`）

1. `DimensionColumns` 增加 3 个映射（两后端共用）；
2. 新增 `BackendCaps` 结构与 `ReportStorager.Capabilities()` 方法：
   `SupportedDimensions []string`。manager 在维度白名单校验后追加后端能力校验，
   不支持的维度返回 422，错误信息 `"dimension <name> not supported by <backend>
   backend (mysql only until doris support lands)"`；
3. `OverviewResult` 新增：
   - `CacheHitCount` / `CacheSkipCount` / `CacheMissCount` / `CacheHitRate`
     （hit/(hit+miss)，skip 不计入分母）；
   - `CacheReadTokens` / `CacheWriteTokens`（聚合表既有 SUM 列）；
   - `MirrorHitCount`；
   - `IntentClassifiedCount` / `IntentUnknownCount` / `IntentUnknownRate`
     （口径：路由实际消费的意图，非全量分类）；
4. `LogRow` 新增明细 10 列（指针类型可空列沿用既有风格）；
5. `LogFilter` 新增：`CacheStatus *string`、`MirrorHit *bool`、
   `IntentQuestion *string`、`IntentAnswer *string`、`IntentSource *string`；
6. `TimeSeriesMetrics` 新增 `MetricCacheTokens`（cache_read_tokens /
   cache_write_tokens 两条序列）。

## 4. storager 变更

### 4.1 `storage/mysqlreport/report.go`（一期全量）

- overview：聚合表 SUM 取 cache_read/write_tokens；明细 count 取
  cache_status 分布、mirror_hit、intent classified/unknown（沿用
  `logs_total` 的明细 count 模式，7 天窗口上限内）；
- logs：`logRowFields` 投影 +10 列、`scanLogRow` 扫描、`detailWhere` 增加
  5 个过滤条件；
- timeseries：`cache_tokens` 指标分支；新维度 × 既有指标（qps/tokens/cost/
  latency）取数分支（聚合表 GROUP BY 新维度列）；
- rankings / distribution：新维度分支（GROUP BY 新维度列，COUNT/SUM 口径
  与既有维度一致）。

### 4.2 `storage/dorisreport/report.go`（一期部分）

- overview / logs / timeseries `cache_tokens` 同 4.1（明细列已由跨仓链路
  加入 Doris 明细表；cache_tokens 用聚合表既有列）；
- 新维度分支不实现，`Capabilities()` 不声明，由 manager 门控 422；
- 二期 Doris 聚合表重建后补实现并移除门控。

## 5. 端点与装配

- `endpoints/openapi_v1/report/params.go`：绑定 5 个新过滤参数（复用现有
  query 绑定风格）；
- `stateful/container/rdb/components.go` `initReport()`：装配时向 storager
  注入 backend 标识（`Capabilities()` 的实现依赖）。

## 6. 测试设计

| 层 | 内容 |
|----|------|
| 单测 | `types_test.go`（新维度映射/门控校验）、`manager_test.go`（422 路径）、`mysqlreport`（overview/logs/timeseries/rankings/distribution 新分支，含 GROUP BY 断言）、`dorisreport`（门控 + 已实现分支）、`job_test.go`（新维度聚合口径）、`endpoints` 参数绑定 |
| 集成测试组 B | `test/integration/tests/report/query/`：种子灌含新字段日志 → overview 新指标断言、logs 过滤断言（intent_answer/cache_status/mirror_hit）、timeseries cache_tokens、rankings 新维度；**Doris 后端用例**断言新维度 422 |
| schema 守卫 | `test/integration/tests/schema/report/`：report.md 契约形状守卫补新字段 |
| 回归 | 既有组 B 用例全绿（新列为空不影响旧断言） |

## 7. 不做的事

- `ai_intent_confidence`/`ai_intent_latency_us` 不做分位数聚合（标定走
  logs 导出）；
- mirror 异步字段（844–850）、`ai_cache_key` 不进报表；
- Doris 聚合表维度（二期，另行立项）。
