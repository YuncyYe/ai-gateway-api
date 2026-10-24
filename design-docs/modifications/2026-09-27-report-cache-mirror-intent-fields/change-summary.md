# Report 体现 ai-cache / 流量镜像 / ai-intent 访问日志字段 变更摘要

> 本文档覆盖 report 子系统消费三组访问日志新字段的 ai-gateway-api 仓库改动方案。
> 总体分期与跨仓（log-reader / ai-gateway-observability）链路设计见设计稿
> 《report 体现缓存镜像意图字段设计》（v0.8/report，权威来源）。
> 前置依赖：bfe-access-pb v0.3.7/3.8/3.9（proto 字段已发布）、bfe 已回填
> （mileszhang2016/bfe `36171a54`）。

## 1. 背景

BFE 已就三个模块落访问日志字段，但 report 全链路（`model/ireport`、
`storage/{mysql,doris}report`、`db_ddl_report_mysql.sql`、api-define `report.md`）
零消费：

| 模块 | 字段（proto） | 现状 |
|------|---------------|------|
| ai-cache | `ai_cache_status`(789/790)；cache token 计量 781/782/788 已在明细+聚合表就位但查询层从未消费 | 未进报表 |
| 流量镜像 | `mirror_hit`~`mirror_error`(842–850) | 未进报表 |
| ai-intent | `ai_intent_*`(803–809) | 未进报表 |

缺口贯穿 5 环节：log-reader 字段注册 → MySQL/Doris 明细 DDL → 聚合 JOB →
report 查询层（本仓负责后两者中的 MySQL 部分与全部查询层）。

## 2. 目标

- **一期（本方案）**：Backend=mysql 下交付完整能力——明细 10 列 + 聚合表
  3 个维度列（`ai_cache_status`/`mirror_hit`/`ai_intent_answer`）+ 查询层全量
  （overview 新指标、logs 新列与过滤、timeseries `cache_tokens`、
  distribution/rankings/timeseries 新维度）；Backend=doris 下一期只保证明细
  能力（列由跨仓链路同步加入 Doris 明细表），新维度请求按能力门控返回 422；
- **二期**：Doris 聚合表维度补齐后两后端拉平（另行立项）。

分期理由：MySQL 分钟聚合是进程内 JOB + 普通分区表，加维度 = ALTER + 改
GROUP BY；Doris `bfe_ai_metrics_1m` 是 AGGREGATE KEY 模型，加维度 = 整表
重建。一期在 MySQL 上验证口径与产品形态，二期只做 Doris 对齐。

## 3. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api`（本仓库，DDL + 查询层 + 契约）；log-reader、ai-gateway-observability 的入库链路改动见设计稿 §3.1（跨仓，单独排期） |
| 修改模块 | `db_ddl_report_mysql.sql`（明细 +10 列、聚合表 +3 维度列）；`model/ireport/`（维度/指标常量、OverviewResult/LogRow/LogFilter、backend 能力门控 `Capabilities()`）；`storage/mysqlreport/`（job.go 聚合列与 GROUP BY + report.go 全量查询）；`storage/dorisreport/report.go`（一期仅 overview/logs/cache_tokens 部分）；`endpoints/openapi_v1/report/params.go` |
| 测试适配 | 两侧 storager 单测 + endpoints 单测；集成测试组 B（`test/integration/tests/report/query/`，新过滤/新维度/新指标断言）；schema 契约守卫（`test/integration/tests/schema/report/`） |
| 涉及接口 | `GET /open-api/v1/report/overview`、`/timeseries`、`/rankings`、`/distribution`、`/logs`（详见 api-changes.md） |
| 数据迁移 | 无回填；聚合维度从上线时刻起积累，分钟表不回溯历史 |
| 数据面影响 | BFE / log-reader 语义不变（log-reader 扩列属数据面配套，跨仓排期） |

## 4. 关键决策

| 决策 | 说明 |
|------|------|
| MySQL 先行全量、Doris 二期对齐 | 两后端聚合机制成本不对称（ALTER vs AGGREGATE KEY 重建）；一期交付后 Doris 只做对齐，口径已被验证 |
| 聚合维度只选 3 个低基数字段 | `ai_cache_status`（≤4 值）、`mirror_hit`（2 值）、`ai_intent_answer`（≤问题选项数 ~10）；`ai_intent_question` 不进聚合（一期 BFE 只记路由消费的单条，question 恒为同一个）；行数膨胀估 10–30 倍相关子集，上线后监控 JOB 耗时与表行数 |
| backend 能力门控显式化 | `ReportStorager` 增 `Capabilities()`；Doris 后端请求新维度返回 422（指明"mysql backend only"），杜绝"空报表"误读为"没有数据" |
| confidence/latency 只进明细不进聚合 | `MinConfidence` 标定与决策服务 SLO 分析用 logs 端点导出即可，一期不做分位数聚合 |
| `ai_cache_key`、mirror 异步字段（844–850）不进报表 | 前者 debug 专用防膨胀；后者 bfe 设计即走 Prometheus 不回写日志 |
| 意图消费口径显式声明 | `ai_intent_*` 是"路由实际消费的意图"，overview"意图分类数"非全量分类量，契约文档注明 |

## 5. 兼容性影响

- 新增字段/参数/枚举值均为**增量**，无 breaking change；
- Backend=doris 的部署新维度返回 422——对存量 Doris 用户属行为变化（此前无此维度，请求同样以 422 参数非法拒绝但不区分具体原因；一期仍返回 422 并指明维度不支持的原因）；
- 新列数据从各链路（log-reader 扩列、DDL、JOB）上线后积累，此前为空。

## 6. 关联文档

- `design-docs/modifications/2026-09-27-report-cache-mirror-intent-fields/design-changes.md`
- `design-docs/modifications/2026-09-27-report-cache-mirror-intent-fields/api-changes.md`
- 历史先例：`2026-09-15-report-query-api/`（报表子系统首次引入）、`2026-09-24-issue-207-report-cost-amount-conversion/`（纯查询层改造模式）
