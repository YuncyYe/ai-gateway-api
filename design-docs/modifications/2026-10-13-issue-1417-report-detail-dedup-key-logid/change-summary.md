# 报表库明细表幂等键补 `logid` 列（issue #1417）：变更摘要

> 日期：2026-10-13　　issue：[bfenetworks/bfe#1417](https://github.com/bfenetworks/bfe/issues/1417)
> 上游方案：`document-ai-gateway/迭代系统设计/v0.8/bfe_ai_request_log秒级覆盖问题/bfe_ai_request_log秒级覆盖问题修复方案.md`（下称"总方案"）
> 本目录产出：`change-summary.md`（本文）、`design-changes.md`（DDL diff / 迁移 runbook / 文档同步清单 / 测试计划）
> 配套仓库方案：`bfe/docs/zh_cn/modifications/2026-10-13-issue-1417-logid-dedup-fix/design-changes.md`（已实施并推送）、`log-reader`（待办）、`ai-gateway-observability` Doris 侧（另立任务，见 §5）

## 1. 背景

报表库明细表 `bfe_ai_request_log` 的唯一键 `uk_dedup` 只有四列，其中 `log_time` 是由 pb `timestamp`（Unix **秒**）转换来的秒级 `DATETIME`；写入侧 log-reader 以 `INSERT ... ON DUPLICATE KEY UPDATE <全列>` 覆盖写。于是**同一秒内同一 API Key、同一模型的并发请求互相覆盖，n 个请求只留 1 行，且无任何信号**。设计上本该区分请求的 `logid` 列恒为 0（BFE 侧缺陷，已在 `bfe` 仓库修复，见 §5）。

| 问题 | 证据 |
|------|------|
| 唯一键无请求级判别列 | `db_ddl_report_mysql.sql:173` `UNIQUE KEY uk_dedup (hostid, log_time, ai_apikey_id, ai_requested_model)` |
| `log_time` 为秒级 | `db_ddl_report_mysql.sql:60` `log_time DATETIME NOT NULL`；`log-reader/reader_modules/mod_log_mysql/field_mapper.go:52,276-283` 由 pb `timestamp`（Unix 秒）构造 |
| 全列覆盖 → 整行消失 | `log-reader/reader_modules/mod_log_mysql/record_writer.go:105-111,396-423` |
| 判别列 `logid` 恒 0 | 生产 CSV 1827 行恒 `"0"`；代码链见 bfe 方案 §三 |
| 生产实测丢率恰为 (n−1)/n | 2026-10-12 13:00–13:59 CST：pb3 2113 行 vs MySQL 1827 行，丢 286 行 = 13.5%；同秒 1/2/3-4 并发丢率 1%/50%/67% |
| 设计文档承诺与实现不符 | `log-reader/doc/modules/mod_log_mysql/output-columns.md:186` 承诺"同一**分钟**内同 Key 同模型只保留一条"，实现是同一**秒**（差 60 倍） |

**影响**：明细检索、逐行计费审计，以及所有**求和类报表**——被覆盖行的 `ai_total_tokens` / `ai_cost_value` 从求和中整体消失，聚合同样失真。以该表为唯一明细源的模块是分钟聚合 JOB（`storage/mysqlreport/job.go:534,547`），故本仓的成本/token 报表口径受害。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-10-13 |
| 涉及仓库 | `ai-gateway-api`（本仓：权威 DDL + 设计文档）；配套 `bfe`（已实施：pb `logid` 由恒 0 变为逐请求唯一非 0）、`log-reader`（文档 + 集成测试，核心零代码改动）；**需另立任务**：`ai-gateway-observability` Doris 侧同缺陷 |
| 变更类型 | **报表库 DDL 变更**：`uk_dedup` 增列 `logid`；`logid` 由 `BIGINT DEFAULT NULL` 收紧为 `BIGINT NOT NULL DEFAULT 0`。**零 Go 代码改动、零新增依赖、零 API 变更** |
| 产出 | `db_ddl_report_mysql.sql` 明细表定义与存量迁移语句；`design-docs/sys-design/报表库设计文档.md`、`details/报表查询模块.md`、`数据库设计文档.md` 同步 |
| 预估工作量 | DDL + 文档约 0.5 人日；生产迁移窗口与验证约 0.5 人日；端到端对账验证约 0.5 人日 |
| 前置依赖 | `bfe` 侧 logid 修复（**已实施**：commit `89defa14`，已推送 `origin/v1.8.9-dev`）。**顺序不敏感**：任一单独上线都不劣于现状，只改本仓 DDL 不会使任何情况变差（见 §3、design-changes §4） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 判别列选谁 | **加 `logid` 进 `uk_dedup`**，不引入新列 | `logid` 已存在于表、pb 与写入列清单（`field_mapper.go:55`），是设计指定的逃逸字段；新增"行序号/request_uid"需改 pb、改 log-reader 列清单、改 Doris 同步，面大且无必要 |
| 2 | 为什么不用"提高 `log_time` 精度" | **否决** | pb `timestamp` 是 Unix **秒**（`bfe/bfe_modules/mod_access_pb3/request_log.go:56`），`DATETIME(3)` 拿不到亚秒信息；且改 `log_time` 类型会连带改分区列定义、需要分区重建 |
| 3 | `logid` 是否允许 NULL | **收紧为 `NOT NULL DEFAULT 0`**（关键） | MySQL 唯一键视 `NULL` 互不相等。若保持可空，任何 `logid IS NULL` 的行会**逃出全部去重** → 同一行重放插入重复行，把"覆盖丢行"换成"重放重复行"，是语义回退。`NOT NULL DEFAULT 0` 下无 logid 的行恒为 0，与现状四元组覆盖行为**逐行等价** |
| 4 | 键长是否安全 | 安全，无需前缀索引 | `(256+256+128)×4 + 长度字节 + 5(log_time) + 8(BIGINT) ≈ 2579` 字节 < 3072（InnoDB DYNAMIC 行格式上限） |
| 5 | 与分区是否冲突 | 不冲突 | uk 仍含分区列 `log_time`，满足 MySQL"分区表唯一键必须包含全部分区列"；分区管理 JOB（`storage/mysqlreport/job.go:272-301`）只读写 `information_schema.PARTITIONS`，不感知唯一键 |
| 6 | 上线顺序 | **与 bfe 顺序不敏感，推荐 bfe 先行** | 只改 DDL 时新行 `logid` 仍全 0 → 冲突行为与现状完全相同；只改 bfe 时 `logid` 不进键 → 仍覆盖。两个中间态都**不劣于现状**，无需联合割接 |
| 7 | 历史丢失能否回收 | **不能靠 `-b` 补读回收** | 旧 pb3 原始日志里的 `logid` 本身也是 0，重放时仍互相覆盖。历史补偿需另立数据修复任务（导入到不带该唯一键/带合成行号的表） |
| 8 | 与 Doris 的关系 | Doris 必须同改，但**另立任务** | Doris 明细表为同一四元组 `UNIQUE KEY`（`doris/sqls/bfe_ai_request_log.sql:144`），同样丢行；只改 MySQL 会破坏"幂等键与 Doris UNIQUE KEY 完全一致"的既有承诺。该仓库不在本卡范围（详见 §5） |
| 9 | 严重级别 | 按 issue 口径维持 **P2** | 本仓没有任何以 `SUM(ai_cost_value)` 直接出账单的端点：明细表的唯一求和消费方是分钟聚合 JOB，批量对账明确"不读报表库"（`design-docs/sys-design/details/批量任务与对账.md:87`）。若仓外存在以该表 SUM 出账的下游，需升 P1 并另行裁定 |

## 4. 范围

**本仓改动**

- `db_ddl_report_mysql.sql`：明细表 `logid` 列与 `uk_dedup` 定义 + 头部注释块新增存量迁移语句（沿用 `:19-34` 的既有范式）。
- `design-docs/sys-design/报表库设计文档.md`：§2.2 的 DDL 片段、§3.2 设计要点 2/3（NULL 语义与"唯一键与 Doris 完全一致"表述）、§3.3 生命周期说明。
- `design-docs/sys-design/details/报表查询模块.md:145`：唯一键表述同步。
- `design-docs/sys-design/数据库设计文档.md:48`：顺带修正既存漂移（"89 列"应为 99 列、"37 维"应为 40 维）。
- `test/integration/testutil/report_server.go`、`test/integration/tests/report/partition/partition_test.go`：各补 1 行 `Net = "tcp"`，修复"`[Databases.report_db]` 缺 `Net` → `FormatDSN` 丢弃 `Addr` → 驱动回退 `127.0.0.1:3306`"的 harness 缺陷（使报告库位于非 3306 端口时也能跑通；根因、先例与验证见 `design-changes.md` 附录）。**属独立缺陷修复**，如需可拆分为单独提交。
- `test/integration/tests/report/dedup_key/dedup_key_test.go`（新增）：**报表侧回归锁**——同秒 + 同 `hostid`/`ai_apikey_id`/`ai_requested_model` 的 3 条并发明细必须全部保留（`/report/overview.logs_total` 与 `/report/logs.total` 均为 3）；另含"旧四列键对照臂"（同批数据写入旧键镜像表 → 只留 1 行，幸存最后写入行）与"重放仍幂等臂"（同 `logid` 再写 → 仍 3 行且值被覆盖）。修复前该用例**红**（并发行只剩 1 行）、修复后**绿**；测试设计见 `test/integration/tests/report/design.md` §9。

**明确不在本仓范围**

- Doris / StarRocks / ClickHouse 明细表 DDL（`ai-gateway-observability` 仓库；结论见 §5）。
- BFE 侧 `logid` 填充（`bfe` 仓库，**已完成**）。
- log-reader 文档与集成测试（`log-reader` 仓库）。
- 历史丢失行的数据修复。

**不受影响（已核实）**

- 分钟聚合 JOB `RunAggregateOnce`（`storage/mysqlreport/job.go:229-266`）与 `buildAggregateInsertSQL`（`:486-556`）：不引用唯一键；其幂等性来自"`DELETE` 窗口（`:448-450`）+ `INSERT SELECT`"事务与 `GET_LOCK('report_agg_job')`（`:33`），与明细表唯一键无关。
- 报表查询层 `storage/mysqlreport/report.go`：明细表只被 `COUNT(*)` 与日志列表读取（`:262-263,276-277,466-467,527-537`），不引用唯一键。
- 分区管理 JOB、账号权限模型（写入账号仍只需 INSERT/UPDATE）。
- 本仓现有测试：`test/integration/tests/report/partition/cases_test.go:166,185` 只插 `(hostid, log_time)`，`logid` 落 `DEFAULT 0`，行为不变。

## 5. 非本仓登记点（链路协同）

| 仓库 | 需做的事 | 状态/阻塞关系 |
|------|---------|--------------|
| `bfe` | pb `logid`：`genLogId()` 改产十进制串（≤19 位、掩 63 位），三处取值改为"解析 → 为 0/失败随机重生 + 计数" | **已完成**：commit `89defa14`（`origin/v1.8.9-dev`）。缺它则本仓 DDL 变更零收益 |
| `log-reader` | 文档修正（"不含 logid / 分钟级"表述）+ 两处内嵌测试 DDL 同步（`scenario-LR03`/`LR04`）+ 新增"同秒同 Key 同模型、logid 不同 → 两行各自落库"回归用例 | 非阻塞；测试 DDL 不随本仓更新会与新权威 DDL 分叉 |
| `ai-gateway-observability`（**另立任务**） | Doris 明细表 `UNIQUE KEY(hostid, log_time, ai_apikey_id, ai_requested_model)` 前缀扩展加 `logid`（`logid` 恰为第 5 列，无需调列序），`logid` 收 `NOT NULL`；Doris 不支持在线改 UNIQUE KEY → 建新表 + `INSERT INTO ... SELECT` + 换名（动态分区 `start=-7`、保留 7 天，代价可控）；Routine Load 零改动（`bfe_ai_log_load_routine.sql:5` COLUMNS 已含 `logid`）；遵守 63 位值域约束 | 非阻塞，但两侧口径分叉期间 Doris 链路仍 (n−1)/n 丢行。**注意 Doris 聚合 JOB 无 DELETE 窗口**（`doris/sqls/bfe_ai_metrics_1m_job.sql` 仅 `INSERT ... SELECT SUM(...)`），若该侧改 DUPLICATE KEY 会让明细重复穿透到聚合——本仓结论是**维持 UNIQUE KEY**（详见总方案 §7） |
| `ai-gateway-observability`（无需修改，已核实） | StarRocks `DUPLICATE KEY`、ClickHouse MergeTree `ORDER BY`、三后端聚合表 | append-only 不去重，覆盖丢失在引擎层不可能发生；改键需全表重建且零去重收益。StarRocks 文档中"append-only 天然唯一、`logid` 天然唯一"的论证依赖了不成立的假设，但建模结论不受影响，可顺手修正措辞 |

## 6. 文档配套

- 本目录 `design-changes.md`：DDL diff、`NOT NULL DEFAULT 0` 论证、迁移 runbook（含分区表在线 DDL 注意事项）、兼容矩阵与升级顺序、测试计划、风险与开放问题。
- **无 `api-changes.md`**：本次不涉及任何 Open API / Inner API 的请求或响应契约变更（`/open-api/v1/report/*` 的字段与过滤参数均不变）。
- 不新增 `sys-design/details/` 长文档：本次是在既有 `报表库设计文档.md` 与 `details/报表查询模块.md` 上修订，不构成新的模块设计。

## 7. 六步法核对

| 步骤 | 状态 |
|------|------|
| 1. 创建 `design-docs/modifications/2026-10-13-issue-1417-report-detail-dedup-key-logid/` | ✅ 本目录 |
| 2. 编写变更文档（`change-summary.md` / `design-changes.md`） | ✅ |
| 3. 更新并评审 `api-define/` | N/A（无 API 变更） |
| 4. 更新并评审 `sys-design/` + `summary.md` 索引 | ✅ 已实施（2026-10-13）：修订 `报表库设计文档.md`（§2.2 DDL 片段、§3.2 要点 2/3：`logid NOT NULL DEFAULT 0` 与"唯一键 = 请求级判别键"）、`details/报表查询模块.md`（唯一键表述）、`数据库设计文档.md`（顺带修正 89→99 列、37→40 维漂移）；`summary.md` 无需新增条目（均为既有文档修订） |
| 5. 实现并测试（DDL + 存量迁移 + 端到端对账） | ✅ 已实施并验证（2026-10-13）：`db_ddl_report_mysql.sql` 明细表 `logid` 列与 `uk_dedup` 修改 + 头部存量迁移语句；以临时 MySQL 8.4.9 实例验证"新 DDL 可应用 / 旧键缺陷复现 (n−1)/n / 迁移语句（含 `Error 1138` 实测发现，须先回填 NULL）/ 迁移后并发 3 行 + 重放仍 3 行 + `DEFAULT 0` 等价性"，并据此**更正**了"NULL 行被静默补 0"的错误描述——详见 `design-changes.md` 附录。仓库 report 集成测试（4 套件 / 43 用例）已在非默认端口的临时实例上**全部跑通**，过程中修复了一处 harness 缺陷（`[Databases.report_db]` 缺 `Net = "tcp"` 导致 `Addr` 被 `FormatDSN` 丢弃、驱动回退 3306）；并新增**报表侧回归锁** `tests/report/dedup_key`（3 个子用例：明细保留 / 旧四列键对照 / 重放幂等，修复前红、修复后绿） |
| 6. 总结并决定是否新增 `details/` 长文档 | 结论：不需要（见 §6）；变更记录与实测结果已在本目录归档 |