# 报表库明细表幂等键补 `logid` 列（issue #1417）：设计变更说明

> 日期：2026-10-13　　issue：[bfenetworks/bfe#1417](https://github.com/bfenetworks/bfe/issues/1417)
> 上游方案：`document-ai-gateway/迭代系统设计/v0.8/bfe_ai_request_log秒级覆盖问题/bfe_ai_request_log秒级覆盖问题修复方案.md`
> 本文件展开 `change-summary.md` 的决策 1/3/4/5/6，给出可直接执行的 DDL、迁移 runbook 与验证步骤。

## 1. 现状代码事实（设计约束来源）

| 项 | 事实 | 位置 |
|----|------|------|
| 明细表 DDL | `log_time DATETIME NOT NULL`、`logid BIGINT DEFAULT NULL`、`UNIQUE KEY uk_dedup (hostid, log_time, ai_apikey_id, ai_requested_model)`、按天 RANGE 分区 `PARTITION BY RANGE (TO_DAYS(log_time))` | `db_ddl_report_mysql.sql:58-183` |
| 写入侧 | log-reader 单事务多值 `INSERT ... ON DUPLICATE KEY UPDATE col=VALUES(col),...`（全列覆盖），幂等键由 DDL 决定（写入列清单取自 `field_mapper.go` 的 `Columns()`，**已含 `logid`**） | `log-reader/reader_modules/mod_log_mysql/record_writer.go:100-112,396-423`、`field_mapper.go:55` |
| `logid` 写入值 | log-reader 对 `logid` 走 `kindScalar`：**恒写整数，不写 NULL**（pb 字段为 `uint64`）；本仓测试与手工插入会落 `DEFAULT 0` | `field_mapper.go:324-333`、`test/integration/tests/report/partition/cases_test.go:166,185` |
| 明细表消费方 | 分钟聚合 JOB（`DELETE` 窗口 + `INSERT SELECT SUM(...)`，`GET_LOCK('report_agg_job')`）；报表查询的日志列表与 `COUNT(*)`；overview 的明细条件计数 | `storage/mysqlreport/job.go:229-266,446-556`、`report.go:262-263,276-277,466-467,527-537` |
| 分区管理 | 每 6h 巡检 `information_schema.PARTITIONS`，`ADD PARTITION` / `DROP PARTITION`，保留期默认 7 天（`RetentionDays <= 0 → 7`）；不感知唯一键 | `storage/mysqlreport/job.go:272-301,558-570`、`stateful/config.go:75,94-95` |
| 存量迁移范式 | 仓库内只有**普通 `ALTER TABLE ... ADD COLUMN`** 的注释块示例；无 `ALGORITHM=`/`LOCK=NONE`/pt-osc/gh-ost 先例 | `db_ddl_report_mysql.sql:19-34` |
| 既有文档承诺 | "幂等键与 Doris UNIQUE KEY 完全一致"；"NULL 不参与唯一键去重…可接受" | `design-docs/sys-design/报表库设计文档.md:181-182`、`details/报表查询模块.md:145` |
| 测试落点 | 本仓集成测试应用本文件建表（MySQL 侧）；ClickHouse/StarRocks 用 `ai-gateway-observability` 侧 DDL 资产 | `test/integration/tests/report/design.md:95,196`、`test/integration/testutil/report_server.go:163-167,370` |

## 2. 变更内容（DDL diff）

**变更前**（`db_ddl_report_mysql.sql:63,170-173`）

```sql
    logid                   BIGINT        DEFAULT NULL,
    ...
    -- 幂等键：与 Doris UNIQUE KEY 一致，log-reader 重发/-b 补读安全；
    -- 唯一键长度 (256+256+128)*4+5 ≈ 2565 字节 < 3072 InnoDB 上限，无需前缀索引；
    -- 唯一键包含分区列 log_time（MySQL 分区表约束）
    UNIQUE KEY uk_dedup (hostid, log_time, ai_apikey_id, ai_requested_model),
```

**变更后**

```sql
    -- logid：BFE 逐请求唯一标识（bfe#1417 起为非 0 十进制值，映射为 BIGINT）。
    -- NOT NULL DEFAULT 0 而非可空：MySQL 唯一键视 NULL 互不相等，可空会让
    -- "无 logid 的行"逃出全部去重（同一行重放插入重复行）——那是语义回退。
    logid                   BIGINT        NOT NULL DEFAULT 0,
    ...
    -- 幂等键：请求级判别列 logid 入键后，同秒同 Key 同模型的并发请求各自成行；
    -- 只有同一请求重放（同 logid）才命中覆盖，保持重发/-b 补读幂等。
    -- 键长 (256+256+128)*4 + 长度字节 + 5(log_time) + 8(BIGINT) ≈ 2579 < 3072；
    -- 唯一键包含分区列 log_time（MySQL 分区表约束）
    UNIQUE KEY uk_dedup (hostid, log_time, ai_apikey_id, ai_requested_model, logid),
```

同时更新文件头部注释块（`db_ddl_report_mysql.sql:19-34` 的"存量表加列"范式之后）新增本次存量迁移语句，见 §3。

## 3. 为什么必须 `NOT NULL DEFAULT 0`（而非可空）

| `logid` 可空性 | `logid` 缺省行在唯一键下的行为 | 同一行重放的结果 | 与现状比较 |
|---------------|------------------------------|----------------|-----------|
| `DEFAULT NULL`（不变） | `logid IS NULL` 不参与任何比较 → 该行**逃出全部去重** | 插入**重复行** | **语义回退**：把"覆盖丢行"换成"重放重复行"，且重复行会穿透到分钟聚合的 `SUM` |
| **`NOT NULL DEFAULT 0`（选定）** | 缺省值为 `0`，`0` 参与比较 | 命中覆盖率 → 仍是 1 行 | **逐行等价现状**：无 logid 的行与今天的四元组覆盖行为完全一致 |

补充事实：log-reader 的 `logid` 走 `kindScalar` 且 pb 字段是 `uint64`，**实际写入恒为整数**（`field_mapper.go:324-333`），所以生产表中本来就不存在 `NULL` 行；收紧为 `NOT NULL` 是**防御性**的（挡住本仓测试种子、手工插入、未来新写入方引入 NULL 的路径）。

## 4. 约束核算

| 约束 | 核算 | 结论 |
|------|------|------|
| InnoDB 唯一键长度上限（3072 字节，DYNAMIC 行格式） | `hostid` VARCHAR(256)→1026B、`ai_apikey_id` VARCHAR(256)→1026B、`ai_requested_model` VARCHAR(128)→514B、`log_time` DATETIME→5B、`logid` BIGINT→8B，合计 ≈ 2579B | ✅ 无需前缀索引 |
| 分区表唯一键必须包含全部分区列 | uk 仍含分区列 `log_time` | ✅ |
| 分区管理 JOB | 只读写 `information_schema.PARTITIONS` 并 `ADD/DROP PARTITION`，不解析唯一键 | ✅ 不受影响 |
| 分钟聚合 JOB | `DELETE FROM bfe_ai_metrics_1m WHERE ts_min = ?` + `INSERT ... SELECT ... SUM(...) FROM bfe_ai_request_log WHERE log_time >= ? AND log_time < ?`，明细表只读 | ✅ 不受影响；明细不再丢行后，聚合口径随之修正 |
| 报表查询层 | 明细表只做 `COUNT(*)`、日志列表与条件计数，均按过滤条件读取 | ✅ 不受影响 |
| 账号权限（`db_ddl_report_mysql.sql:36-39`） | 写入账号只需明细表 INSERT/UPDATE；DDL 由维护账号执行 | ✅ 权限模型不变 |

## 5. 生产迁移 runbook

### 5.1 迁移前检查与 NULL 回填（**必做**）

```sql
-- 1) NULL 行统计（生产预期为 0：log-reader 对 logid 恒写整数）
SELECT COUNT(*) AS null_logid_rows FROM bfe_ai_request_log WHERE logid IS NULL;

-- 2) 若 >0，必须先回填为 0：MySQL 默认严格模式下，含 NULL 行的
--    MODIFY COLUMN ... NOT NULL 会直接报 Error 1138（Invalid use of NULL value；
--    实测于 MySQL 8.4.9），并不会静默补 0。失败是原子的——整条 ALTER 不生效、
--    键与列属性保持原样（fail-fast，不留半成品状态）。
UPDATE bfe_ai_request_log SET logid = 0 WHERE logid IS NULL;

-- 3) 当前键定义与唯一键长度
SHOW CREATE TABLE bfe_ai_request_log\G

-- 4) 表规模（决定迁移窗口与方式）与保留期
SELECT table_rows, ROUND(data_length/1024/1024,1) AS data_mb, ROUND(index_length/1024/1024,1) AS idx_mb
  FROM information_schema.TABLES
 WHERE table_schema = DATABASE() AND table_name = 'bfe_ai_request_log';
SHOW CREATE TABLE bfe_ai_request_log\G          -- 核对分区与动态保留期（向 DROP 超 RetentionDays 的分区）
```

保留期默认 7 天（`stateful/config.go:94-95`），故表规模有界；上线前以实际 `RetentionDays` 与行数确认窗口是否足够。

### 5.2 迁移语句（单条 ALTER，原子生效）

```sql
ALTER TABLE bfe_ai_request_log
  MODIFY COLUMN logid BIGINT NOT NULL DEFAULT 0,
  DROP KEY uk_dedup,
  ADD UNIQUE KEY uk_dedup (hostid, log_time, ai_apikey_id, ai_requested_model, logid);
```

- **前置条件**：`logid` 无 NULL 行（见 §5.1 第 1/2 条）；否则本语句报 `Error 1138` 并整体失败。

- **执行窗口**：建议低峰窗口。仓库内无在线 DDL 先例（`db_ddl_report_mysql.sql:19-34` 的范式为普通 `ALTER`），且本表为**分区表**，唯一键增列的在线/锁行为依赖 MySQL 版本与分区结构——先在实际版本的预发库上量测耗时再定策略；如必须在线，可用 `pt-online-schema-change --alter "..."` 承接（其表拷贝 + `RENAME` 流程与分区表共存需单独验证）。
- **写入中断影响**：`ALTER` 期间 log-reader 的写入会失败并按其既有策略退避重试（`MaxRetries` 后 `markDown` → 重连 → 持有批优先补写），**不会丢批**；但仍应尽量缩短窗口。
- 生产库与开发库均执行（本文件即建表 DDL，新环境建表时自动带上新键）。

### 5.3 迁移后验证

```sql
-- 1) 键定义生效
SHOW INDEX FROM bfe_ai_request_log WHERE Key_name = 'uk_dedup';
-- 预期 5 行（Seq_in_index 1..5：hostid, log_time, ai_apikey_id, ai_requested_model, logid）

-- 2) 列属性生效
SELECT COLUMN_NAME, IS_NULLABLE, COLUMN_DEFAULT
  FROM information_schema.COLUMNS
 WHERE table_schema = DATABASE() AND table_name = 'bfe_ai_request_log' AND COLUMN_NAME = 'logid';
-- 预期：IS_NULLABLE=NO, COLUMN_DEFAULT=0

-- 3) 新行 logid 非 0（迁移后窗口）
SELECT COUNT(*) AS rows_total,
       SUM(logid = 0)  AS rows_logid_zero,
       SUM(logid IS NULL) AS rows_logid_null
  FROM bfe_ai_request_log
 WHERE log_time >= NOW() - INTERVAL 10 MINUTE;
-- 预期：rows_logid_zero = 0（bfe 侧修复已上线），rows_logid_null = 0

-- 4) 端到端对账（issue 复现步骤）
--    同秒以同一 API Key、同一模型并发 n（2/3/4）个可区分请求后：
SELECT log_time, ai_apikey_id, ai_requested_model, COUNT(*) AS rows_here
  FROM bfe_ai_request_log
 WHERE log_time >= '<触发时刻的秒>'
 GROUP BY log_time, ai_apikey_id, ai_requested_model, hostid;
-- 预期：同秒同 Key 同模型的行数 = 并发请求数 n（修复前恒为 1）
```

### 5.4 回滚

```sql
ALTER TABLE bfe_ai_request_log
  DROP KEY uk_dedup,
  ADD UNIQUE KEY uk_dedup (hostid, log_time, ai_apikey_id, ai_requested_model);
```

`logid` 列的 `NOT NULL DEFAULT 0` 可保留（不影响旧逻辑）。回滚后无数据损坏：`logid` 只是变回"不参与键"，新行仍携带真实值可供后续再次迁移。

## 6. 兼容矩阵与升级顺序

| 中间态 | 明细表行为 | 与现状比较 |
|--------|-----------|-----------|
| 只上本仓 DDL（bfe 未修，`logid` 恒 0） | uk 五列，`logid` 恒 0 → 与现状四元组冲突行为完全相同 | 不劣于现状 |
| 只上 bfe 修复（`logid` 不进键） | uk 四列，同秒并发仍覆盖 | 不劣于现状 |
| 两者齐备（**已具备**：bfe `89defa14` 已推送） | `logid` 逐请求唯一且入键 → 同秒并发各自成行；同 logid 重放命中覆盖 | 缺陷消除 |

**推荐顺序：bfe 先行（已完成）→ 本仓 DDL**。本仓 DDL 的迁移效果可用 §5.3 的第 3/4 条立即验证；迁移窗口内到达的行已带真实 `logid`，不存在"零值穿越"。

## 7. 测试计划

| # | 层级 | 用例 | 判据 |
|---|------|------|------|
| 1 | DDL 静态 | 本文件建表后 `SHOW INDEX`/`SHOW CREATE TABLE` | 5 列唯一键、`logid NOT NULL DEFAULT 0`、分区仍在 |
| 2 | 本仓集成 | `test/integration/tests/report/` 既有用例（应用本 DDL 建表） | 全绿；`logid` 缺省插入落 0 不报错 |
| 3 | 本仓集成 | `test/integration/tests/report/partition/cases_test.go` 等仅插 `(hostid, log_time)` 的用例 | 全绿（`DEFAULT 0` 生效，行为不变；该文件的另一臂期望 Error 1526"无分区"，与 `logid` 无关） |
| 4 | 跨仓端到端 | 同秒同 Key 同模型并发 n 个可区分请求，比对 pb3 解码行数与明细表行数 | 丢率 **0**（现状 (n−1)/n） |
| 5 | 跨仓重放幂等 | log-reader `-b` 补读 / 重发同一 pb 行 | 明细仍 1 行（同 logid 命中覆盖） |
| 6 | 跨仓回归 | `log-reader` LR03 既有 `IdempotentReplay` + 新增"不同 logid 同四元组 → 2 行"用例 | 全绿（该用例由 log-reader 方案交付，需同步两处内嵌测试 DDL） |
| 7 | 聚合口径 | 迁移前后同一分钟窗口的 `bfe_ai_metrics_1m.total_tokens` / `ai_cost_value_sum` | 迁移后不再低估（并发窗口的求和恢复完整） |

## 8. 文档同步清单（六步法 Step 4）

| 文件 | 需改内容 |
|------|---------|
| `design-docs/sys-design/报表库设计文档.md` | §2.2 DDL 片段（`logid` 属性 + uk 五列）；§3.2 设计要点 2（NULL 语义段落补 logid 的特例与理由）、要点 3（"唯一键与 Doris UNIQUE KEY 完全一致"改为"列契约一致；Doris 侧同缺陷待同改，见 §5 登记点"）；§3.3 生命周期补一句"分区列未变，分区管理与保留期不受影响" |
| `design-docs/sys-design/details/报表查询模块.md:145` | 唯一键表述同步为五列 |
| `design-docs/sys-design/数据库设计文档.md:48` | 顺带修正既存漂移："89 列"→99 列、"37 维 + 24 指标"→40 维 + 24 指标 |
| `sys-design/summary.md` | 无新增条目（均为既有文档修订） |

## 9. WBS 与工作量

| # | 任务 | 预估 |
|---|------|------|
| 1 | DDL 修改 + 头部迁移语句 + 文档同步（§8） | 0.5 人日 |
| 2 | 预发库迁移演练（量测 `ALTER` 耗时、验证 §5.3） | 0.5 人日 |
| 3 | 生产迁移窗口执行 + 观察 | 0.5 人日 |
| 4 | 跨仓端到端对账（§7 第 4/5 项，依赖 bfe 已上线 + log-reader 用例） | 0.5 人日 |

## 10. 风险与对策

| # | 风险 | 对策 |
|---|------|------|
| 1 | 分区表 `ALTER` 的锁/重建耗时不可控 | 先预发量测；低峰窗口；必要时用 pt-osc 并单独验证其与分区表的兼容性；保留期 7 天使表规模有界 |
| 2 | 存在 `logid IS NULL` 的历史行 | 迁移前按 §5.1 第 1 条统计，**>0 时先回填** `UPDATE ... SET logid = 0 WHERE logid IS NULL`。MySQL 默认严格模式下，含 NULL 行的 `MODIFY ... NOT NULL` 会直接报 `Error 1138` 并整体失败（实测于 MySQL 8.4.9），**不会静默补 0**——失败是原子的、fail-fast，但会挡住迁移。生产预期为 0（log-reader 对 `logid` 恒写整数），该步骤是稳健性保障 |
| 3 | 迁移期间写入失败 | log-reader 既不退出也不丢批（退避重试 → markDown → 重连后持有批优先补写）；尽量缩短窗口并在迁移后核对 §5.3 第 3 条 |
| 4 | 只改本仓不改 Doris → 两侧对账口径分叉 | §5 已登记另立任务；分叉期间对账统一以 pb3 原始日志为准 |
| 5 | 下游存在"同秒只 1 行"的隐式依赖 | 已核查本仓无此依赖；仓外若有（如按 `(hostid, log_time)` 去重的历史脚本）需通知，属口径修正而非破坏 |
| 6 | 历史已丢行不可回收 | 旧 pb3 行本身 `logid=0`，重放仍互相覆盖；历史补偿需另立任务（导入到不带该唯一键/带合成行号的表） |

## 11. 开放问题

| # | 问题 | 影响 | 需要谁裁定 |
|---|------|------|-----------|
| 1 | 是否存在仓外以 `SUM(ai_cost_value)` 出账单的下游 | 决定 P2 / P1 | owner |
| 2 | 生产库实际 `RetentionDays` 与表规模 | 决定迁移方式（普通 ALTER / pt-osc）与窗口 | 运维 |
| 3 | Doris 侧同改的排期 | 决定两链路口径分叉持续多久 | owner |

## 12. 参考

- 缺陷：[bfenetworks/bfe#1417](https://github.com/bfenetworks/bfe/issues/1417)
- 总方案与三份仓库方案：见 `change-summary.md` 头部
- 权威 DDL：`db_ddl_report_mysql.sql:58-183`
- 写入实现：`log-reader/reader_modules/mod_log_mysql/record_writer.go:100-112,396-423`、`field_mapper.go:52,55,324-333`
- 聚合与分区：`storage/mysqlreport/job.go:229-266,272-301,446-570`、`stateful/config.go:75,94-95`
- Doris 同缺陷：`ai-gateway-observability/doris/sqls/bfe_ai_request_log.sql:144`、`doris/sqls/bfe_ai_log_load_routine.sql:5`

## 附：实测验证记录（2026-10-13）

**环境**：临时 MySQL 8.4.9 实例（`--initialize-insecure` 新建 datadir 于系统临时目录、端口 13306；验证后关闭并删除目录）。**未触碰本机既有 MySQL 的数据目录与凭据**；本机既有实例未运行、`REPORT_MYSQL_DSN` 未设置，故仓库集成测试（`test/integration/tests/report/*`）本次未执行（见文末"未覆盖"）。

| # | 验证项 | 结果 |
|---|--------|------|
| 1 | **修改后的 `db_ddl_report_mysql.sql` 可应用**（按测试 harness 方式：`${INIT_DATE}` 替换、剔除 `--` 注释行、分号切分执行） | 两张表建成；`uk_dedup` 列序 = `hostid, log_time, ai_apikey_id, ai_requested_model, **logid**`（5 列）；`logid` `IS_NULLABLE=NO` / `COLUMN_DEFAULT=0` |
| 2 | **旧四列键缺陷复现**：同秒同 Key 同模型 3 行（`logid` 111/222/333）走 log-reader 同款 `ON DUPLICATE KEY UPDATE` 批插 | **仅 1 行**幸存（`logid=333`、`SUM(ai_total_tokens)=30` 而非 60）——丢率 2/3 = (n−1)/n，且幸存的是**最后写入行**，与 issue 的现场观察一致 |
| 3 | **迁移语句（未回填直接执行）**：`logid` 存在 NULL 行时执行 §5.2 的 `ALTER` | **`Error 1138 (22004): Invalid use of NULL value`**，整条 ALTER 失败、键与列属性保持原样（原子、fail-fast）——**推翻**了本文早期"会按 `DEFAULT 0` 静默补 0"的描述，已更正 |
| 4 | **迁移语句（按修正后的 §5.1 先回填再迁移）** | `UPDATE ... SET logid = 0 WHERE logid IS NULL` 后 NULL 行归零 → `ALTER` 成功；键 5 列、`logid NOT NULL DEFAULT 0`、原 NULL 行 `logid=0` |
| 5 | **迁移后并发行为**：同秒同 Key 同模型 3 个不同 `logid` 批插 | **3 行**（`SUM(ai_total_tokens)=66`）——缺陷消除 |
| 6 | **迁移后重放幂等**：同 3 个 `logid` 再批插一次（token 改为 99） | **仍 3 行**、token 被覆盖为 297——同请求重放仍收敛，未引入重复行 |
| 7 | **迁移后等价性**：不带 `logid` 重复插入同一 4 元组（走 `DEFAULT 0`） | **1 行**（`logid=0`、token 被覆盖）——与旧四元组覆盖行为逐行等价，**无"重放重复行"回退** |

**顺带发现（非本次引入）**：把 `db_ddl_report_mysql.sql` **原样**管道给 Windows 下的 `mysql` 客户端会因中文注释行报 `Error 1064`；用 **HEAD（未修改）版本**做同样操作**报同一个错**（同在注释行），属既存的"客户端字符集 + 原样管道"问题——测试 harness 正是靠剔除 `--` 注释行规避（`test/integration/tests/report/design.md:97`）。

**本仓 Go 侧回归**：`go build ./...` 通过；`go test ./storage/mysqlreport/... ./model/ireport/...` 全绿（DDL 变更不涉及 Go 代码，此二包为明细表相关的最直接消费者）。

**仓库集成测试（已跑通，2026-10-13）**：`tests/report/{not_assembled,query,partition}` 与 `tests/schema/report` 已在本机用**非默认端口**的临时 MySQL 8.4.9 实例全部跑通（`REPORT_MYSQL_DSN="root:@tcp(127.0.0.1:13306)/"`，共 **43 个用例 PASS、0 失败**）；harness 在跑通前成功建库、**应用本次修改后的 DDL**、灌入种子。

跑通过程中发现并修复了一处**测试基础设施缺陷**（与本次 DDL 改动无关，但会挡住任何"报告库不在 127.0.0.1:3306"的验证）：

- **现象**：API 子进程启动阶段 stdout 报 `config.InitDB - dial tcp 127.0.0.1:3306: connectex: ... actively refused it` → 服务端未监听 → harness 报 `server not ready at 127.0.0.1:<port> in 10s`（服务端日志停在 `stateful.CheckSecretAtRest` 之后）。
- **根因**：harness 生成的 `[Databases.report_db]` 配置段缺 `Net = "tcp"`。`DbConfig` 内嵌 `mysql.Config`，`FormatDSN()`（`stateful/config_database.go:45`）在 `Net` 为零值时会把 **DSN 的地址段一并丢弃**，驱动回退默认 `127.0.0.1:3306`——配置里的 `Addr` 被静默忽略。该坑在本仓已有先例记录：`testutil/report_server.go:194` 的字段注释（"mysql 形态必须显式 tcp"）与 `design-docs/modifications/2026-10-02-report-mysql-driver-upgrade/change-summary.md`（当时只补了 conf 的 doris/starrocks 样例段）。原开发环境恰好把报告库放在 `127.0.0.1:3306`，回退地址与真实地址重合，故长期未暴露。
- **修复（2 处，各 1 行）**：
  1. `test/integration/testutil/report_server.go` 简版装配（`StartReportServer`，被 `tests/schema/report`、`tests/report/query` 使用）；
  2. `test/integration/tests/report/partition/partition_test.go` 自拼的 `extraTOML`（`StartServerWithExtraConfig` 路径）。
- **验证**：修复后四套件全绿（`not_assembled` 2 / `query` 37 / `partition` 3 / `schema/report` 1）；`Net` 缺失时 `query`/`schema` 必失败、补上即通过，构成完整因果证据链。

复现命令（任意 `host:port` 均可，不再要求 3306；harness 需仓库根目录存在预构建 `ai-gateway-api.exe`，即先 `make build`）：

```bash
cd ai-gateway-api/test/integration
REPORT_MYSQL_DSN="root:<pass>@tcp(<host>:<port>)/" go test -v -count=1 ./tests/report/... ./tests/schema/report/...
```

**新增回归用例（报表出口侧，2026-10-13）**：`test/integration/tests/report/dedup_key/dedup_key_test.go`（测试设计见 `tests/report/design.md` §9）。它补上了此前缺失的一环——bfe 侧 TC-22 只断言 pb 日志、log-reader LR03 只断言"写入 MySQL"，**报表 API 出口没有载体**：

| 子用例 | 断言 | 修复前（旧四列键） | 修复后 |
|--------|------|------------------|--------|
| `detail_key_keeps_concurrent_rows` | 同秒 + 同 `hostid`/`ai_apikey_id`/`ai_requested_model` 的 3 行全部保留：`/report/overview.logs_total=3`、`/report/logs.total=3` 且返回 3 个不同 `logid`；聚合口径 `request_total=3`、`total_tokens=75`、cost=`0.000075` | **FAIL**：`logs_total` 与明细列表均为 **1**（报 `logs_total must count every concurrent row`） | **PASS** |
| `legacy_four_column_key_collapses` | 同批数据写入"旧四列键"镜像表（生产同款多值 `INSERT ... ON DUPLICATE KEY UPDATE`）→ 只留 1 行，幸存**最后写入行**（`logid=3003`） | PASS（对照臂：两态均通过，它证明这些行"唯一差别就是 `logid`"） | PASS |
| `replay_still_idempotent` | 同 `logid` 再写一次（值变更）→ 仍 3 行、token 被覆盖为 100+200+300 | **FAIL**（仍只有 1 行） | **PASS** |

红/绿复现方式：`git stash push -- db_ddl_report_mysql.sql` 后运行该包即为**红**——seed 走生产同款 `ON DUPLICATE KEY UPDATE`，因此红色以"行数断言失败"呈现（而非 SQL 报重复键），与线上事故形态一致；`git stash pop` 后为**绿**。