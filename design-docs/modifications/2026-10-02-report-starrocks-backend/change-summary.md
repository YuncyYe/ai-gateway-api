# 报表查询新增 StarRocks 后端 变更摘要

> 本文档覆盖《数据报表-ClickHouse 与 StarRocks 对接设计方案》（v0.8 迭代系统设计稿，
> 2026-10-01，下称"对接设计稿"）的 **ai-gateway-api 侧 StarRocks 改动**（对接设计稿 §3.3 配置契约、
> §6.2 查询层改动、§7 发布顺序、§8 测试计划中 api 相关条目）。
> 对接设计稿 §4 的 StarRocks 数仓侧资产由 ai-gateway-observability 仓落地，
> **已交付**（`starrocks/sqls/` 四份 SQL + `starrocks/tests/`），本变更为其配套的 api 查询后端。
> 对接设计稿中的 ClickHouse 已由 `2026-10-02-report-clickhouse-backend/` 交付，本文为平行变更。

## 1. 背景

v0.7《数据报表-多存储与API化设计方案》确立"一套报表 API、多存储后端、配置切换"架构，
查询层扩展点（`model/ireport/types.go` `ReportStorager` 六方法接口 + `BackendCaps` 能力门控、
`stateful/container/rdb/components.go` `initReport()` 装配）当前已交付三种取值：
`"mysql"`（mysqlreport，进程内 JOB）、`"doris"`（dorisreport，纯查询）、
`"clickhouse"`（clickhousereport，纯查询，2026-10-02 交付）。

StarRocks 侧的三项前置条件均已就绪：

1. **数仓资产已交付**：ai-gateway-observability `starrocks/` 全套声明式资产
   （明细表 `bfe_ai_request_log`（DUPLICATE KEY，RANGE 动态分区）、Routine Load
   打平导入、异步物化视图 `bfe_ai_metrics_1m`（`REFRESH ASYNC EVERY 1 MINUTE` +
   `partition_ttl = "7 DAY"`）），与 Doris/ClickHouse 表契约同构（同名、同列、同口径）；
2. **零驱动成本**：StarRocks FE 的 MySQL 协议端口（9030）复用现有
   `go-sql-driver/mysql`，`DbConfig` 与 go.mod **零改动**（对接设计稿 §6.2 既定结论）；
3. **测试环境就绪**：`environment/starrocks-installation.md`（StarRocks 3.5.21 单机
   FE+BE，MySQL 协议 9030；与 Doris 端口完全相同的互斥环境，不可同时运行）。

本变更对 api 侧是纯增量平行扩展：不动 mysqlreport / dorisreport / clickhousereport
任何已交付内容，不动既有部署的任何行为。

## 2. 目标

| # | 目标 | 验证标准 |
|---|------|----------|
| 1 | 新增 `storage/starrocksreport` 查询后端，实现 `ireport.ReportStorager`，结构对齐 dorisreport | 五类端点（overview/timeseries/rankings/distribution/logs）全部可用，含缓存/镜像/意图三维度下钻与延迟分位数 |
| 2 | `[Report].Backend` 扩展为 `"mysql" \| "doris" \| "clickhouse" \| "starrocks"`，`initReport()` 新增装配 case | 仅改配置即可切换到 StarRocks 报表；对其他取值及缺省部署零行为变化 |
| 3 | **零 go.mod 变更**：`Driver = "mysql"` 直连 FE 9030 走现有驱动与 DSN 路径 | `conf` 样例配置即可建立连接池；`FormatDSN()` 无改动 |
| 4 | 四引擎口径一致 | 同一份种子数据在 MySQL/Doris（借实例）/StarRocks/ClickHouse（真实实例）同窗口五端点数值一致（分位数按各后端近似算法给容差） |

## 3. 非目标

- 不改动 mysqlreport / dorisreport / clickhousereport 任何已交付内容，不抽象公共 SQL 基座
  （与 ClickHouse 变更既定结论一致：各自成包 + 快照测试锁定，第三处以上重复再评估抽取）；
- 不交付 StarRocks 的 Grafana 数据源与 dashboard（对接设计稿 §2 非目标，
  可视化统一走 ai-gateway-web 报表页与 `/open-api/v1/report/*`）；
- 不改 log-reader、BFE、ai-gateway-web（响应结构不变即自然兼容）；
- 不新增报表端点、不改动五端点的参数/响应结构/鉴权；
- 不做历史数据迁移工具与多引擎并存部署的口径仲裁（维持"单集群单落库"约束）；
- 不处理 StarRocks 数仓侧的资产演进（异步 MV 重建等由 observability 仓修改说明记录）。

## 4. 范围

| 范围 | 说明 |
|------|----------|
| 涉及仓库 | `ai-gateway-api`（本仓库，查询层 + 契约文档）；StarRocks 数仓侧已交付于 ai-gateway-observability |
| 新增模块 | `storage/starrocksreport/`（report.go + report_test.go，快照测试） |
| 修改模块 | `stateful/container/rdb/components.go`（`initReport()` 增加 case）；`stateful/config.go` 与 `model/ireport/types.go` 的 Backend 枚举注释；`conf/ai_gateway_api.toml` 新增 StarRocks 形态注释样例；`design-docs/api-define/OpenAPI接口定义/report.md` 与 `sys-design/details/报表查询模块.md` 后端支持标注更新 |
| 依赖变更 | **无**（复用 `go-sql-driver/mysql` 与 gendry） |
| 测试适配 | starrocksreport SQL builder 快照单测（克隆 dorisreport 快照模式）；`Capabilities()` 声明断言；`testutil` 新增 StarRocks 数据源装配 helper（仿 ClickHouse 的 `StartClickHouseReportServer` 模式，DDL 占位符为 `${STARROCKS_DATABASE}`/`${INIT_PARTITION_DATE}`）；集成测试两组（借 MySQL + 真实 SR 环境门）；`make test-model-cover-gate` 回归 |
| 涉及接口 | `GET /open-api/v1/report/overview`、`/timeseries`、`/rankings`、`/distribution`、`/logs`（仅新增 Backend=starrocks 形态，详见 api-changes.md） |
| 数据迁移 | 无（查询层改造，无 DDL/JOB 变更；聚合由异步物化视图维护、保留期由动态分区 + partition_ttl 承担） |
| 数据面影响 | 无（纯查询层 + 文档） |

## 5. 关键决策

| 决策 | 说明 |
|------|------|
| 复用 `Driver = "mysql"`，零 go.mod 变更 | 对接设计稿 §6.2 既定结论；SR FE 9030 即 MySQL 协议，`DbConfig`/`FormatDSN()` 现有路径直通；conf 中 `AllowNativePasswords = true` 注意事项与 Doris 段同款（SR FE 认证同为 mysql_native_password） |
| starrocksreport = dorisreport 当前版本同源克隆 + 方言修正 | 两后端 SQL 差异点仅参考对接设计稿 §6.2 对照表（实质为同一 MySQL 函数集；percentile_approx 小写、其余同构）；**克隆基线取已含 `CAST AS SIGNED` 修复的 dorisreport**（见下条） |
| 时间桶 `CAST(... AS SIGNED)`（继承 2026-10-02 dorisreport 修复） | dorisreport 的 `CAST AS BIGINT` 在真实 MySQL 8.4 非法（Error 1064，已修为 SIGNED）；starrocksreport 沿用 SIGNED 以支持"借 MySQL 实例"的集成验证组。**实施门**：真实 StarRocks 3.5.21 上验证 `CAST AS SIGNED` 被接受（SR 支持 CAST AS BIGINT 无疑问，SIGNED 别名需实机确认；若不接受则回退 BIGINT 并放弃借-MySQL 数据断言、仅保留门控/参数校验组） |
| 复杂列（ARRAY<STRUCT>）直接投影 | SR 明细表原生 `ARRAY<STRUCT<key,value>>` 等复杂列（flatten 陷阱为 ClickHouse 特有，SR 无此问题）；经 MySQL 协议 SELECT 预期返回 JSON 文本（同 Doris JSON 列行为），扫描层与 dorisreport 一致用 NullString。**实施门**：集成测试断言 req_headers 内容形态；若驱动返回非字符串则改 SR `to_json()` 函数包装（§9 风险 2） |
| 聚合表读法不变（GROUP BY 维度 + SUM(指标)） | SR 侧分钟聚合为异步物化视图 `bfe_ai_metrics_1m`（物化粒度 = Doris 聚合表 40 维全键）；api 查询按维度聚合 + SUM 折叠天然安全，与 Doris 直读纪律同构；MV 重建演进约束由 observability 仓记录 |
| 无本地 JOB | 三个 JOB 配置项仅 backend=mysql 生效的既有语义不变；SR 侧聚合由异步 MV、保留期由动态分区 + partition_ttl 承担 |
| 时区纪律：全部比较走 epoch | api 入参/出参皆 unix 秒，借 `FROM_UNIXTIME(?)` 与 Doris 同款，天然免疫会话时区 |
| 环境互斥声明 | SR 与 Doris 端口完全相同（9030/8030 等），测试环境不可同时运行（`environment/starrocks-installation.md` 已声明）；集成验证借 MySQL 组与真实 SR 组设计为与 Doris 组错峰 |

## 6. 兼容性影响

- **Backend = "starrocks" 新形态**：随本版本启用，五端点全量可用；
- **Backend = "mysql" / "doris" / "clickhouse" 及缺省部署**：零行为变化（纯增量 case）；
- **回滚耦合点（与 ClickHouse 同款）**：配置置为 `"starrocks"` 后回滚 api 到老版本会因
  `unsupported [Report].Backend` 启动失败——**配置回滚必须与 api 回滚同步**；
  SR 侧资产与 Kafka 消费位点保留，重新升级时 Routine Load 从断点续传无重放；
- 错误码无变化：未装配 404、无权限 402、参数级 422（`xerror` PARAM）语义全部不变
  （starrocksreport 声明全量 12 维，能力 422 不触发）。

## 7. 关联文档

- 权威设计稿：《数据报表-ClickHouse 与 StarRocks 对接设计方案》（§6.2 StarRocks 查询层）
- 架构源头：《数据报表-多存储与API化设计方案》（v0.7 迭代系统设计稿，多后端架构与 [Report] 配置的首次定义）
- 平行变更：`design-docs/modifications/2026-10-02-report-clickhouse-backend/`（本文的直接模板：
  结构、测试两组模式、真实实例验证出的方言陷阱清单均沿用）
- Doris 对齐先例：`2026-10-01-report-doris-cache-mirror-intent-alignment/`
- 数仓侧（另一仓，已交付）：ai-gateway-observability `starrocks/docs/design/TABLE_DESIGN.md`、
  `starrocks/docs/user/HOWTO.md`、`api/depends_api/req_log.md`
- 环境依据：`environment/starrocks-installation.md`（StarRocks 3.5.21 单机 FE+BE，MySQL 协议 9030；
  **与 Doris 端口互斥**）
- 同目录：`design-changes.md`、`api-changes.md`
