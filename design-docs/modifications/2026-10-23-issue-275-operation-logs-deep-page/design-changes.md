# Issue #275 修复：存储层设计变更说明

本文档是 `change-summary.md` 的配套设计文档，描述存储层的机制变化、实现要点与兼容性结论。

## 1. 现状调用链（问题确认）

```
OperationLogListAction (endpoints/openapi_v1/operation_log/list.go:78)
  └─ OperationLogManager.QueryLogs (model/ioperlog/manager.go:141)
       └─ OperationLogStorager.List (storage/rdb/ioperlog/operation_log.go:61)
            ├─ dao.TOperationLogCount  → SELECT count(*) ...（绕过 SQLRecord 埋点）
            └─ dao.TOperationLogList   → internal.QueryList
                 └─ internal.queryList (storage/rdb/internal/dao/internal/curd.go:39)
                      └─ NewSelectBuilder(table, where, nil)   // fields=nil → SELECT *
                           └─ gendry builder.BuildSelect → SELECT * FROM operation_logs
                              WHERE ... ORDER BY created_at DESC, id DESC LIMIT offset,n
```

`filterToParam`（`storage/rdb/ioperlog/operation_log.go:194-205`）在有分页参数时写入：

```go
param.Limit   = []uint{uint(offset), uint(*filter.PageSize)}
param.OrderBy = &"created_at DESC, id DESC"
```

offset 由 `Struct2Where` 落入 where map 的 `_limit` 键，gendry 编译为 `LIMIT offset,n`。`SELECT *` + offset 语义导致每跳回表物化整行（含 mediumtext），是 14–20 s 的直接来源（issue 根因 1–4 环，已逐环复核一致）。

## 2. 变更一：DAO 新增延迟关联分页查询

### 2.1 新增函数

`storage/rdb/internal/dao/table_operation_logs.go` 新增（命名以最终实现为准）：

```go
// TOperationLogListDeferred 延迟关联分页查询：
// 内层覆盖索引只取 id，外层主键回表取整行，消除深页 offset 回表成本。
func TOperationLogListDeferred(dbCtx lib.DBContexter, where *TOperationLogParam) ([]*TOperationLog, error)
```

### 2.2 SQL 组装

延迟关联 SQL 无法由 gendry `BuildSelect` 整体表达（派生表 JOIN），采用"内层 gendry 编译 + 外层模板拼接"：

```go
innerWhere := internal.Struct2Where(where)          // 含全部过滤条件 + _orderby + _limit
build := internal.NewSelectBuilder(tOperationLogTableName, innerWhere, []string{"id"})
innerSQL, args, err := build.Compile()               // SELECT id FROM operation_logs WHERE ... ORDER BY ... LIMIT ?,?

sql := "SELECT t.* FROM operation_logs t JOIN (" + innerSQL + ") AS x ON t.id = x.id " +
       "ORDER BY t.created_at DESC, t.id DESC"
```

- **参数化不变**：所有过滤值仍由 gendry 编译为 `?` 占位参数，注入面与现状一致；
- 外层固定串无用户输入；
- 扫描复用 `scanner.Scan` 到 `[]*TOperationLog`；
- 埋点与 `curd.go:51-59` 一致：构造 `stateful.SQLRecord{SQL, Args, Err, Cost}`，`defer record.Print(dbCtx)`，保证 metrics 可见。

### 2.3 外层为什么必须再 `ORDER BY`

派生表 JOIN 不保证输出行序（`<derived>` 物化 + `eq_ref` 驱动顺序）。契约要求"最新在前"且相邻页单调，因此外层显式 `ORDER BY t.created_at DESC, t.id DESC`——仅本页 `page_size`（≤100）行参与排序，代价可忽略。`created_at DESC, id DESC` 决胜键语义与 `filterToParam` 既有注释（同秒批量刷盘的稳定性）保持一致。

### 2.4 为什么不能用 `WHERE id IN (子查询 LIMIT)`

MySQL 语法限制（本地 8.4.9 实测，与 QA 同版本）：

```
ERROR 1235 (42000): This version of MySQL doesn't yet support
'LIMIT & IN/ALL/ANY/SOME subquery'
```

`IN` 子查询带 `LIMIT` 在解析层被拒绝，**必须**使用 `JOIN 派生表`形态。SQLite 对两种形态均支持，但形态统一按 MySQL 要求取 JOIN。

## 3. 变更二：storager 层接线与越界短路

`storage/rdb/ioperlog/operation_log.go` 的 `List`：

```go
total, err := dao.TOperationLogCount(dbCtx, where)   // 现有逻辑不变
offset := (*filter.Page - 1) * (*filter.PageSize)    // page/page_size 恒非nil（endpoint 归一化）
if int64(offset) >= total {
    return []*ioperlog.OperationLogEntry{}, total, nil   // 越界短路：跳过列表 SQL
}
list, err := dao.TOperationLogListDeferred(dbCtx, where)
```

- `page` / `page_size` 在 endpoint（`list.go:84-94`）已被归一化为 >0，且契约语义不变，存储层沿用该前提；
- 有分页参数 → 延迟关联；无分页参数 → 保留旧 `TOperationLogList` 路径（其他复用方行为不变）；
- endpoint 层与 model 层**零改动**。

## 4. 变更三：`TOperationLogCount` 补埋点

`table_operation_logs.go:117` 现有裸调用：

```go
rows, err := dbCtx.Execer().QueryContext(dbCtx, sql, args...)
```

补 `SQLRecord` 包裹（与 `curd.go` 同模式），使 `count(*)` 出现在 `/monitor/metrics` 的 `MetricSQLAccessCounter` / `MetricSQLCostCounter`（SQL label 即为最终语句）。本次不改 count 的执行时机与语义（仍每请求一次；TTL 缓存见 `change-summary.md#5.5` 二期项）。

## 5. 变更四：`db_ddl.sql`（MySQL）新增索引

```sql
-- operation_logs 过滤列 + 排序列组合索引（Issue #275）
ALTER TABLE `operation_logs`
  ADD INDEX `idx_operator_name_created` (`operator_name`, `created_at`),
  ADD INDEX `idx_status_created` (`status`, `created_at`),
  ADD INDEX `idx_resource_name_created` (`resource_name`, `created_at`);
```

设计要点：

- 列序为**等值列在前、`created_at` 在后**：等值过滤后索引天然按 `created_at` 有序，`ORDER BY created_at DESC, id DESC` 走反向扫描（InnoDB 索引记录隐含主键，`id` 决胜键也被覆盖），EXPLAIN 表现为 `ref` + `Backward index scan; Using index`，不回表；
- 无过滤时仍走 `idx_created_at`（`index` 类型 + `Backward index scan; Using index`）；
- `resource_name` varchar(512) × utf8mb4 = 2048 B + 长度信息 < 3072 B InnoDB 索引项上限，可全列建索引（已实测）；
- 存量环境（QA/生产）随发布执行上述 `ALTER`；MySQL 8.4 加二级索引默认 `ALGORITHM=INPLACE, LOCK=NONE`，25 万行规模秒级完成、不锁表；
- `action`（`idx_action`）等其余过滤列已存在单列索引；本期只补 issue 实锤的三个无索引过滤列。

## 6. 接口与契约影响

| 项 | 结论 |
|----|------|
| 请求参数 | 不变（`page`/`page_size`/全部过滤参数） |
| 响应结构 | 不变（`list` + `pagination{page,page_size,total}`） |
| `pagination.total` | 仍每请求实时 `count(*)`，语义不变 |
| 排序 | 不变（`created_at DESC, id DESC`） |
| 错误码 | 不变（不新增 `page` 上限类 4xx） |
| `api-define` / `sys-design` | 无需变更（契约未变）；若未来产品裁定加上限/游标，再按六步法走 Step 3/4 |

## 7. 兼容性说明

- **MySQL**：本方案目标版本（QA/生产均为 MySQL 8.4）。DDL 与 SQL 形态均已实测。
- **SQLite**：按本次决策不处理。代码侧延迟关联 SQL（派生表 JOIN）SQLite 可执行，但组合索引不进 `db_ddl_sqlite.sql`，SQLite 下过滤 + 深分页仍走旧性能水平；SQLite 仅单机开发环境使用，列为已知限制与后续项。
- **回滚**：代码回滚即恢复旧 SQL；索引可保留（对旧代码无害，只是占用磁盘）或按需 `DROP INDEX`。

## 8. 本地探针与验证记录

验证环境：本机 MySQL 8.4.9（Windows，默认 InnoDB 配置，buffer pool 小于表体积，磁盘 I/O 弱于 QA，绝对耗时偏长但新旧对比结论一致）。探针库 `operlog_probe`：`operation_logs` 250,000 行，`change_summary` 平均 1,361 B，`created_at` 覆盖 180 天、按 10 s 量化制造同秒批量。

关键数据（SQL_NO_CACHE，冷/热两次）：

| 测量 | 耗时 | 说明 |
|------|------|------|
| 旧：无过滤末页 `SELECT * … LIMIT 250680,20` | 79.9 s / 56.0 s | 复现 issue 形态（QA 14–20 s） |
| 新：无过滤末页 JOIN 派生表 | 0.196 s / 0.121 s | **≈430×** |
| 新旧末页 id 序列 `diff` | 完全一致 | 正确性对拍 |
| 旧：过滤末页 `operator_name='admin'`（有索引）`SELECT *` | 33.3 s | 索引解决不了 `SELECT *` 回表 |
| 新：过滤末页延迟关联（有索引） | 0.144 s / 0.137 s | |
| 新：越界页 `LIMIT 9999999,20` | 0.132 s，0 行 | 内层覆盖索引快速扫完 |
| `count(*)` 热 | 0.156 s | QA ≈40 ms，维持现状可接受 |

EXPLAIN（新，无过滤末页）：derived2 `type=index, key=idx_created_at, Extra=Backward index scan; Using index`；外层 `t type=eq_ref, key=PRIMARY`。EXPLAIN（新，过滤末页）：derived2 `type=ref, key=idx_operator_name_created, Extra=Backward index scan; Using index`。

QA 修复后复测按 issue 验收判据的探针协议执行（页码曲线 + 同页 `page_size` 对照 + 时间窗对照 + `/monitor/metrics` 前后快照）。
