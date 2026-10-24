# 报表查询新增 ClickHouse 后端 变更摘要

> 本文档覆盖《数据报表-ClickHouse 与 StarRocks 对接设计方案》（v0.8 迭代系统设计稿，
> 2026-10-01，下称"对接设计稿"）的 **ai-gateway-api 侧 ClickHouse 改动**（对接设计稿 §3.3 配置契约、
> §6.3 查询层改动、§7 发布顺序、§8 测试计划中 api 相关条目）。
> 对接设计稿 §5 的 ClickHouse 数仓侧资产由 ai-gateway-observability 仓落地，
> **已交付**（`clickhouse/sqls/` 六份 SQL + `clickhouse/tests/integration/` clickhouse-it），
> 本变更为其配套的 api 查询后端。
> 对接设计稿中的 StarRocks 后端不在本期范围。

## 1. 背景

v0.7《数据报表-多存储与API化设计方案》确立"一套报表 API、多存储后端、配置切换"架构，
查询层以 `model/ireport/types.go` 的 `ReportStorager` 六方法接口（五查询 + `Capabilities()`）
+ `BackendCaps` 能力门控为扩展点，`stateful/container/rdb/components.go` 的 `initReport()`
按 `[Report].Backend` 装配，当前已交付 `"mysql"`（mysqlreport，进程内 JOB）与
`"doris"`（dorisreport，纯查询）两个后端。

对接设计稿将后端扩展为四种取值，其中 ClickHouse 侧的三项前置条件均已就绪：

1. **数仓资产已交付**：ai-gateway-observability `clickhouse/` 全套声明式资产
   （Kafka 引擎表 + 消费 MV 打平、明细表 `bfe_ai_request_log`、聚合表
   `bfe_ai_metrics_1m`（SummingMergeTree）+ 聚合 MV、TTL 7 天），
   与 Doris 表契约同构（同名、同列、同口径）；
2. **字段契约稳定**：`api/depends_api/req_log.md`，三引擎共用；
3. **测试环境就绪**：`environment/clickhouse-installation.md`
   （ClickHouse 26.10.1.1149，HTTP 8123 / Native TCP 9000，WSL2 低配）。

本变更对 api 侧是纯增量平行扩展：不动 mysqlreport / dorisreport 任何已交付内容，
不动既有部署的任何行为。

## 2. 目标

| # | 目标 | 验证标准 |
|---|------|----------|
| 1 | 新增 `storage/clickhousereport` 查询后端，实现 `ireport.ReportStorager`，结构对齐 dorisreport | 五类端点（overview/timeseries/rankings/distribution/logs）全部可用，含缓存/镜像/意图三维度下钻与延迟分位数 |
| 2 | `[Report].Backend` 扩展为 `"mysql" \| "doris" \| "clickhouse"`，`initReport()` 新增装配 case | 仅改配置即可切换到 ClickHouse 报表；对 `Backend = "mysql" \| "doris"` 及缺省部署零行为变化 |
| 3 | 新驱动接入：`DbConfig.FormatDSN()` 支持 `Driver = "clickhouse"`（clickhouse-go/v2 stdlib，native TCP） | `[Databases.clickhouse_db]` 配置可建立连接池；其他 driver 行为不变 |
| 4 | 三引擎口径一致 | 同一份 demo 消息集灌 Doris / ClickHouse 两消费组，同窗口五端点数值一致（分位数按近似算法给容差） |

## 3. 非目标

- 不改动 mysqlreport / dorisreport 任何已交付内容，不抽象公共 SQL 基座
  （与对接设计稿 §6.1 / §9.8 既定结论一致：先各自成包 + 快照测试锁定，
  第三处以上重复再评估抽取 `reportsql` 公共方言层）；
- **StarRocks 后端不在本期**（对接设计稿 §6.2 另案交付）；
- 不交付 ClickHouse 的 Grafana 数据源与 dashboard（对接设计稿 §2 非目标，
  可视化统一走 ai-gateway-web 报表页与 `/open-api/v1/report/*`）；
- 不改 log-reader、BFE、ai-gateway-web（前端按字段有无降级，响应结构不变即自然兼容）；
- 不新增报表端点、不改动五端点的参数/响应结构/鉴权；
- 不做历史数据迁移工具与三引擎并存部署的口径仲裁（维持"单集群单落库"约束，
  并存仅用于灰度比对期）；
- 不做 MySQL 后端分位数补齐（v0.7 开放问题维持）。

## 4. 范围

| 范围 | 说明 |
|------|----------|
| 涉及仓库 | `ai-gateway-api`（本仓库，查询层 + 驱动接入 + 契约文档）；ClickHouse 数仓侧已交付于 ai-gateway-observability |
| 新增模块 | `storage/clickhousereport/`（report.go + report_test.go，快照测试） |
| 修改模块 | `stateful/config_database.go`（`FormatDSN()` 增加 `case "clickhouse"`）；`stateful/container/rdb/components.go`（`initReport()` 增加 case）；`stateful/config.go` 与 `model/ireport/types.go` 的 Backend 枚举注释；`conf/ai_gateway_api.toml` 新增 ClickHouse 形态注释样例；`design-docs/api-define/OpenAPI接口定义/report.md` 后端支持标注更新 |
| 依赖变更 | go.mod 新增 `github.com/ClickHouse/clickhouse-go/v2`（版本对齐 observability clickhouse-it 已验证的 v2.40.0；单独评审，见 design-changes.md §8） |
| 测试适配 | clickhousereport SQL builder 快照单测；`Capabilities()` 声明断言；`testutil.StartReportServerWithBackend` 新增 `"clickhouse"` 取值（真实实例不可达自动 Skip，仿 doris 组）；三引擎口径比对；`make test-model-cover-gate` 回归 |
| 涉及接口 | `GET /open-api/v1/report/overview`、`/timeseries`、`/rankings`、`/distribution`、`/logs`（仅新增 Backend=clickhouse 形态，详见 api-changes.md） |
| 数据迁移 | 无（查询层改造，无 DDL/JOB 变更；聚合与保留由 CH 侧 MV + TTL 承担） |
| 数据面影响 | 无（纯查询层 + 文档） |

## 5. 关键决策

| 决策 | 说明 |
|------|------|
| native TCP 协议 + clickhouse-go/v2 stdlib | 对接设计稿 §6.3 既定结论。ClickHouse 的 MySQL 兼容端口（9004）官方定位为"尽力而为"，类型系统与函数支持有限，且函数方言（quantile/intDiv/toString）仍需独立 builder，省不下查询层工作量，不为此妥协驱动质量 |
| 复用 `DbConfig` 嵌入的 `mysql.Config` 字段组装 CH DSN | `DbConfig` 内嵌 `mysql.Config`（config_database.go），clickhouse case 仅读取 `Addr`/`User`/`Passwd`/`DBName` 四字段拼 `clickhouse://<user>:<passwd>@<addr:9000>/<DBName>?dial_timeout=10s&compress=lz4`；mysql 专有字段（TLS、timeout 等）不映射，文档注明。零 `DbConfig` 结构改动 |
| 不用 gendry，fmt 模板 + 白名单校验 | gendry 是 MySQL 方言 builder，不可用于 ClickHouse（对接设计稿 §6.3）。沿用 dorisreport 的字面量内联纪律：仅时间桶宽、LIMIT/OFFSET、UNION ALL kind 三类字面量内联（manager 校验/clamp 后），字符串值一律绑定参数 |
| 聚合表按"GROUP BY 40 维 + SUM(指标)"读取 | CH 聚合表为 SummingMergeTree、排序键全 40 维（observability 已交付资产），merge 只折叠真正同维组合的行；api 五类查询本就按维度聚合，天然兼容，**禁止 SELECT * 直读聚合表**（对接设计稿 §5.4 既定纪律） |
| 无本地 JOB | `EnableAggregateJob`/`RetentionDays`/`EnablePartitionMgmt` 三项仅 backend=mysql 时生效的既有语义不变；CH 侧分钟聚合由消费明细的物化视图同步维护、保留期由两表 TTL 自管（对接设计稿 §3.3） |
| 时区纪律：全部比较走 epoch | api 入参/出参皆 unix 秒，CH 按 epoch 比较天然免疫会话时区；任何调试 SQL 渲染时间字符串必须显式 `toDateTime(x, 'UTC')`（对接设计稿 §6.3） |
| 不抽象公共基座 | clickhousereport 与 dorisreport 预计高度同源，但两后端 SQL 差异点分散且各自演进；先各自成包 + 快照测试锁定（对接设计稿 §6.1 / §9.8） |

## 6. 兼容性影响

- **Backend = "clickhouse" 新形态**：随本版本启用，五端点全量可用；
- **Backend = "mysql" / "doris" 及缺省部署**：零行为变化（纯增量 case，
  `Backend = ""` 不装配 404、非法值启动报错的既有行为不变）；
- **回滚耦合点（唯一）**：配置置为 `"clickhouse"` 后回滚 api 到老版本会因
  `unsupported [Report].Backend` 启动失败——**配置回滚必须与 api 回滚同步**
  （先把 Backend 改回 mysql/doris 再降级 api）。存储资产与消费位点保留在 CH 侧，
  重新升级时 Kafka 消费组从断点续传无重放；
- 错误码无变化：未装配 404、无权限 402、参数级 422（`xerror` PARAM）、
  后端能力 422 语义全部不变
  （clickhouse 后端声明全量维度，422 仅在枚举非法时出现，与 doris 一致）。

## 7. 关联文档

- 权威设计稿：《数据报表-ClickHouse 与 StarRocks 对接设计方案》（v0.8 迭代系统设计稿，2026-10-01）
- 架构源头：《数据报表-多存储与API化设计方案》（v0.7 迭代系统设计稿，多后端架构与 [Report] 配置的首次定义）
- 平行模板：《数据报表-Doris 后端设计方案》（v0.8 迭代系统设计稿，本方案平行扩展的既定模式来源）
- 数仓侧（另一仓，已交付）：ai-gateway-observability
  `clickhouse/docs/modifications/2026-10-01-clickhouse-dock/design-changes.md`、
  `clickhouse/docs/design/TABLE_DESIGN.md`、`clickhouse/docs/user/HOWTO.md`
- 历史先例：`design-docs/modifications/2026-09-15-report-query-api/`（报表子系统首次引入）、
  `2026-09-15-report-fixes/`（时区/分区边界修复）、
  `2026-10-01-report-doris-cache-mirror-intent-alignment/`（最近一期后端对齐）
- 同目录：`design-changes.md`、`api-changes.md`
