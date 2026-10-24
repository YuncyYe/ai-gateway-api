# 集成测试默认后端 SQLite → MySQL（消除 JSON 列类等仅 MySQL 可见的回归盲区）

> 2026-10-21：**已实施**。本卡把 `test/integration` 的默认测试后端从 SQLite 切换为 MySQL，
> 并顺带修复全量 MySQL 首跑暴露的一个产品排序缺陷（§5）。
> 起因：issue #264 的根因是 `providers.api_keys` 声明为 `JSON` 列——SQLite 的 `TEXT` 列
> 不校验 JSON，信封写路径在 SQLite 上"天然可用"，全部集成回归绿灯，而真实 MySQL 部署
> 一开加密就 500。**凡依赖后端行为差异的缺陷，SQLite 后端都测不出来**，继续以 SQLite
> 为默认后端等于保留盲区。

## 1. 目标

1. `StartServer` / `StartServerWithExtraConfig` / `StartServerWithMonitor` /
   `StartServerWithSharedInfra` 默认走 MySQL（每次调用独立建库 `ai_gateway_it_*`、
   执行 `db_ddl.sql` + 种子、`Shutdown` 时 DROP）；
2. 无 MySQL 的环境/CI 自动回退 SQLite（行为与改造前完全一致），显式
   `AIAPI_FORCE_SQLITE=1` 可强制旧行为；
3. 直读数据库的断言统一走后端无关的 `testutil.OpenDB(t, sm)`，不再散落
   `sql.Open("sqlite-strip", sm.DBPath)`；
4. 全量集成在 MySQL 后端跑通（首跑暴露的缺陷修复后 0 失败）。

## 2. 后端选择规则（`testutil.resolveMySQLAdminDSN`）

| 优先级 | 条件 | 后端 |
|--------|------|------|
| 1 | `AIAPI_FORCE_SQLITE=1` | SQLite（显式回退） |
| 2 | `AIAPI_MYSQL_DSN`（管理员 DSN，不带库名） | MySQL（带口令实例用这个） |
| 3 | 探测 `root@tcp(127.0.0.1:3306)/`（root 无口令）可达 | MySQL |
| — | 均不满足 | SQLite |

`StartServerAuto` / `StartServerWithMonitorAuto` 与上述规则一致（此前它们是唯一双后端入口，
现在全部入口统一）。

## 3. testutil 改动

| 改动 | 说明 |
|------|------|
| `startServerAuto` 分发器 | `StartServer*` 系列入口统一入口；`sharedDBPath != ""` 时保持"共享 SQLite 文件"旧语义 |
| MySQL 共享库池 | `StartServerWithSharedInfra` 多实例场景：首个实例建库并注册进程内池（引用计数），后续实例复用同一 `[Databases.bfe_db]` 配置；最后一个 `Shutdown` 才 DROP。SQLite 行为不变 |
| `provisionMySQLDatabase` / `mysqlDBSection` / `dropMySQLDatabase` | 从 `startServerWithMySQL` 抽出的公共件（显式 DSN 的并发用例 `StartServerWithMySQL` 行为不变） |
| `ServerManager.openDB()` | MySQL DSN 优先、`sm.DBPath` 兜底；`GetQuotaPlanLastResetAt(Nullable)`、`UpdateQuotaPlanLastResetAt`、`NullQuotaPlanLastResetAt` 四个配额辅助方法改用它（此前固定 sqlite-strip，MySQL 下打开空库报 `no such table`） |
| `testutil.OpenDB(t, sm)` | 新增公开助手：MySQL 用 mysql 驱动、SQLite 用 sqlite-strip（含 busy_timeout）。`batch/helper.OpenDB`、`secret_at_rest.openRawDB` 改为委托 |
| `GetUserPassword` / `SetUserPassword` | 签名从 `(dbPath, ...)` 改为 `(t, sm, ...)`，内部走 `OpenDB` |
| `ServerManager.mysqlShared` | 标记共享池成员，`Shutdown` 只减引用不 DROP |

## 4. 套件迁移

| 套件 | 改动 |
|------|------|
| `tests/batch/helper` | `OpenDB` 委托 `testutil.OpenDB`（种子 SQL 本就方言中立） |
| `tests/secret_at_rest` | `openRawDB` 委托；SAR-1-003（共享 SQLite 文件）在 MySQL 下跳过 |
| `tests/auth/password_hash` | 适配 `GetUserPassword` / `SetUserPassword` 新签名 |
| `tests/quota_period_reset` | 无需改动：`StartServerWithSharedInfra(smA.Redis, smA.DBPath)` 在 MySQL 下 `DBPath==""` 自动加入共享池 |
| `tests/operation_log/*` | 无改动；`operation_logs` 直读断言本就走 `OpenDB` |

测试自身的 SQL 全部方言中立（无 `INSERT OR IGNORE` / `datetime('now')` 等——此前
`testutil/db.go`、`mysql.go` 之外的 grep 为 0），因此除上表外 114 处 `StartServer()`
调用点零改动即切到 MySQL。

## 5. 顺带修复的产品缺陷：操作日志列表同秒排序不确定

**现象**：全量 MySQL 首跑 `TestOperationLog_UpdateGlobalRouteRulesHasLog` 失败
（`diff_keys` 断言取到了错误日志条目：同秒批量落盘的 reset/set 两条日志次序随机）。
SQLite 下 filesort 恰好稳定为 id 倒序，从未暴露。

**根因**：`storage/rdb/ioperlog/operation_log.go` 列表排序仅 `ORDER BY created_at DESC`，
而 `created_at` 是**秒级** Unix 时间戳；异步刷盘（5s 批量）使同一秒内的多条日志次序
由存储层自行决定，MySQL 与 SQLite 行为不同。

**修复**：排序改为 `created_at DESC, id DESC`（id 为自增主键，作决胜键）。语义与
"最新在前"一致，只是把同秒的次序确定下来；对既有调用方是稳定性修复而非契约变更。

## 6. 验证

- 首跑（MySQL）：`./tests/...` 共 129 包，125 ok / 4 失败——
  `operation_log/list`（§5 排序缺陷）、`quota_period_reset`（3 例，testutil 配额
  辅助方法固定 sqlite-strip）、`quota_reset_null_guard`（同包同因，共 5 个子用例失败）；
- 修复后复跑（MySQL 8.4.9，`AIAPI_MYSQL_DSN` 走本机 3306 无口令实例）：**全部通过**（回填最终计数）；
- SQLite 回归：`AIAPI_FORCE_SQLITE=1` 下 `secret_at_rest`、`operation_log/list`、
  `quota_period_reset`、`provider/...`、`innerapi/...` 全 PASS（后端回退路径无损）；
- 单测：`go test ./...` 全绿；`go build`/`go vet` 通过。

## 7. 影响与运维

- 无接口契约变化；唯一的运行行为变化是操作日志列表同秒条目的次序由"不确定"变为
  "id 大在前"（契约本就是 created_at 倒序）；
- 开发机需本地 MySQL（或设 `AIAPI_MYSQL_DSN`）；无任何 MySQL 时自动回退 SQLite，
  CI 无需改动；
- 每次测试调用独立建/删库，测试间无状态泄漏；共享池仅服务于多实例用例（引用计数，
  串行测试安全）。
