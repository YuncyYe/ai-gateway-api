# Issue #275：操作日志深分页 14–20 s 修复方案

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/275](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/275)

> QA 环境（MySQL 8.4，`operation_logs` 250,709 行）下 `GET /open-api/v1/operation-logs?page=12535&page_size=20`（末页，offset=250,680）返回 200 但耗时 14–20 s（首屏 0.116 s）；`operator_name=admin` 等无索引过滤条件连第 1 页也要 15 s。
>
> 根因（issue 已实锤，本次源码直读全部复核一致）：
> 1. `endpoints/openapi_v1/operation_log/list.go:84-94`：`page` 无上限、无深页分支，任意页码透传；
> 2. `storage/rdb/ioperlog/operation_log.go:194-205`：`offset=(page-1)*page_size`，`LIMIT offset,page_size`；
> 3. `storage/rdb/internal/dao/internal/curd.go:39-70`：`NewSelectBuilder(table, where, nil)` 字段为 `nil` → 编译为 `SELECT *`；
> 4. MySQL 执行：`idx_created_at` 反向扫描虽满足排序，但 `LIMIT offset` 语义要求产出并丢弃前 offset 行，`SELECT *` 使每一跳回表物化整行（`change_summary` 为 mediumtext）→ offset 线性成本；
> 5. `TOperationLogCount`（`storage/rdb/internal/dao/table_operation_logs.go:101-134`）每请求额外全表 `count(*)` 且绕过 `stateful.SQLRecord` 埋点。

## 2. 本地复现与修复验证（MySQL 8.4.9 探针）

本机 MySQL 8.4.9（与 QA 同版本）按 `db_ddl.sql` 建表，灌入 250,000 行合成数据（`change_summary` 平均 1,361 B，`created_at` 覆盖 180 天并按 10 s 量化制造同秒批量）。实测：

| 场景 | 旧 SQL（`SELECT * … LIMIT offset,n`） | 新 SQL（延迟关联 JOIN 派生表） |
|------|------|------|
| 无过滤末页 offset=250,680,20 | 冷 79.9 s / 热 56.0 s | 0.196 s / 0.121 s |
| 无过滤首屏 offset=0,20 | 0.080 s | 0.078 s |
| `operator_name='admin'`（占 90%）末页，`SELECT *` | 33.3 s（有索引时；无索引时优化器可能选全表扫描 + filesort，QA 实测 15.08 s） | 0.144 s / 0.137 s |
| 过滤首屏（有索引） | 0.066 s | 0.066 s |
| 越界页 offset=9,999,999 | 需全量扫描（与末页同量级，可被低成本放大为 DoS） | 0.132 s，返回 0 行 |
| `count(*)`（热） | 0.156 s（QA ≈40 ms） | 不变 |

关键结论：

- **加速比 ≈430×**：末页耗时与首屏处于同一量级（深页/首屏 ≈1.6×，满足 issue 验收"比值 ≤3×"）。
- **正确性**：新旧两种形态返回的 id 序列完全一致（`diff` 校验通过）；越界页返回空集，语义不变。
- **两个实现红线（探针踩出）**：
  - `WHERE id IN (SELECT id … LIMIT ?)` 被 MySQL 8.4 直接拒绝（ERROR 1235，`LIMIT & IN/ALL/ANY/SOME subquery`）——IN 子查询不能带 LIMIT，**必须**用 `JOIN 派生表`形式；
  - 派生表内层 `SELECT id … ORDER BY created_at DESC, id DESC LIMIT …` 在 InnoDB 上是覆盖索引扫描（EXPLAIN：`Backward index scan; Using index`，走 `idx_created_at` 或过滤组合索引），这是性能来源。

## 3. 目标

在**不变更公开契约**（`operation-logs.md#2.1` 与 `00-common.md` 通用分页约定：任意 `page>0` 均为合法请求，必须返回该页数据与完整集合计数 `pagination.total`）的前提下：

1. 深页耗时不再随 offset 线性增长（深页/首屏 ≤3×）；
2. `operator_name` / `status` / `resource_name` 过滤首屏走索引、与无过滤首屏同量级；
3. `pagination.total` 语义不回归（跨页稳定、越末页不塌缩为 0）；
4. 分页顺序稳定（`created_at DESC, id DESC`，跨页无重复无遗漏）；
5. `count(*)` 纳入 `/monitor/metrics` SQL 埋点；
6. 不静默变更契约：不设 `page` 上限、不改游标分页（若产品后续裁定，按 issue 验收 #7 走文档化 4xx + api-define 更新流程）。

## 4. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api` |
| 主要文件 | `storage/rdb/internal/dao/table_operation_logs.go`、`storage/rdb/ioperlog/operation_log.go`、`db_ddl.sql`；`endpoints/openapi_v1/operation_log/list.go` **不改**（分页语义不变） |
| 数据库 | `db_ddl.sql`（MySQL）新增 3 个组合索引；存量环境执行对应 `ALTER TABLE`（本变更不考虑 SQLite，见 `design-changes.md#7`） |
| 接口契约 | OpenAPI 请求/响应字段、分页语义、`pagination.total` 定义**均不变** |
| 数据迁移 | 仅需在线加索引（MySQL 8.4 加二级索引默认 ALGORITHM=INPLACE、LOCK=NONE，25 万行约秒级） |

## 5. 最终方案

**核心：延迟关联（deferred join）+ 过滤列组合索引 + `count(*)` 埋点补齐 + 越界短路。**

### 5.1 列表查询改延迟关联（核心修复）

把"排序翻页"与"取整行"拆成两步，内层只扫覆盖索引、外层按主键回表只取本页 20 行：

```sql
SELECT t.*
FROM operation_logs t
JOIN (
    SELECT id
    FROM operation_logs
    WHERE <全部过滤条件>
    ORDER BY created_at DESC, id DESC
    LIMIT ?, ?          -- offset, page_size
) x ON t.id = x.id
ORDER BY t.created_at DESC, t.id DESC;   -- 外层重排序，保持契约顺序
```

- 内层 `WHERE <filters> ORDER BY created_at DESC, id DESC` 对 `idx_created_at(created_at)`（InnoDB 隐含主键，等价 `(created_at,id)`）或过滤组合索引构成覆盖索引扫描，**不回表**；
- 外层 `t.id = x.id` 为 `eq_ref` 主键查找，仅本页 `page_size` 次回表；
- 必须采用 `JOIN 派生表`形态（MySQL 不支持带 `LIMIT` 的 IN 子查询，见 §2）；
- 该 SQL 无法由 gendry `BuildSelect` 表达，DAO 新增专用函数：内层仍用 gendry 编译（复用 `Struct2Where` 条件与占位参数，无注入面），外层模板拼接，执行后走 `stateful.SQLRecord` 埋点（实现细节见 `design-changes.md#2`）；
- 有分页参数时统一走延迟关联（首屏实测与旧路径同为 ≈0.08 s，无需保留双路径）；无分页参数的调用保持旧路径不变。

### 5.2 过滤列组合索引

`db_ddl.sql` 新增（MySQL）：

```sql
ALTER TABLE `operation_logs`
  ADD INDEX `idx_operator_name_created` (`operator_name`, `created_at`),
  ADD INDEX `idx_status_created` (`status`, `created_at`),
  ADD INDEX `idx_resource_name_created` (`resource_name`, `created_at`);
```

- 等值过滤 + `created_at` 有序：过滤后 `ORDER BY created_at DESC, id DESC` 仍走覆盖索引反向扫描；
- `resource_name` 为 varchar(512) utf8mb4（2050 B < 3072 B InnoDB 上限），全列索引可建（已实测建索引并走 `ref` 扫描）；
- 存量部署随版本发布附带 ALTER 脚本执行；新部署直接进 `db_ddl.sql`；
- 注意：当前代码对 `operator_name` / `resource_name` 做的是**等值匹配**（`struct2map` 生成 `=`），与 api-define 中"支持模糊匹配"的描述存在偏差。本方案的索引对等值语义有效；模糊语义需产品裁定后另行处理（不在 #275 范围）。

### 5.3 `count(*)` 埋点补齐

`TOperationLogCount` 现有实现直接 `dbCtx.Execer().QueryContext`（`table_operation_logs.go:117`），绕过 `stateful.SQLRecord`，metrics 中查不到。补齐与 `curd.go` `queryList` 相同的 `SQLRecord{SQL, Args, Err, Cost}` + `record.Print(dbCtx)` 包裹，使 `count(*)` 出现在 `/monitor/metrics` SQL 指标中（issue 验收 #6）。

### 5.4 越界短路（防御性，零成本）

`storage/rdb/ioperlog` 的 `List` 已先取 `total`。当 `offset >= total` 时直接返回空列表、跳过列表 SQL——越界页（如 `page=9999999`）从"扫完整张表"降为一次 `count(*)`，消除深分页 DoS 放大面，且响应语义（空 `list` + 完整 `total`）完全不变。

### 5.5 不采纳的备选（附理由）

| 备选 | 结论 | 理由 |
|------|------|------|
| 给 `page` 加上限 / 深页拒绝 | 不采纳 | 违反已批准契约（任意 `page>0` 合法）；如产品裁定加上限，按 issue 验收 #7 走文档化 4xx + api-define 更新 |
| keyset / 游标分页 | 本期不采纳 | 需变更公开契约与前端交互模式，属产品裁定项；延迟关联已把任意深页压到亚秒级，游标失去了必要前提 |
| `pagination.total` TTL 缓存 | 二期可选 | 修复后 `count(*)` 仅 ≈40–160 ms，不是主要矛盾；如后续要做，按"过滤条件哈希为 key、TTL 5–10 s、`BatchCreate` 时递增世代计数失效"设计，并满足 issue 验收 #6 的连续翻页不重复计数 |
| 表分区 / 日志 TTL 清理 | 运维长期项 | 数据生命周期策略属产品/运维裁定，与本期代码修复解耦 |
| SQLite DDL 同步 | 本变更不做 | 按当前决策只考虑 MySQL（见 `design-changes.md#7`） |

## 6. 预期收益与风险

| 项目 | 说明 |
|------|------|
| 收益 | 末页 14–20 s → 亚秒级（本地同版本复现加速 ≈430×）；过滤首屏 15 s 级 → 亚秒级；越界页不再可放大为 DoS；`count(*)` 可观测 |
| 契约兼容 | 请求/响应字段、分页参数、`pagination.total` 语义、排序键全部不变；前端无需改动 |
| 主要风险 | 派生表形态为手写 SQL 模板，需 DAO 单测覆盖各过滤组合与占位参数（filters 仍走 gendry 编译，注入面与现状一致） |
| 并发语义 | 内层取 id 集与外层回表之间非同一事务，极端并发下若日志行被删除可能少 1 行；操作日志无删除入口，风险可接受（同现状的非可重复读级别） |
| 索引成本 | 3 个二级索引增加写放大与磁盘占用（25 万行实测秒级完成构建）；审计表写入为异步批量刷盘，频率低，可接受 |

## 7. 验收判据

对照 issue #275 验收 7 条（QA 环境 `operation_logs` ≥250,000 行复测）：

1. `page=12535&page_size=20` 返回 200、`list` ≤20、`pagination` 回显正确、`total` 为完整过滤集合计数；
2. **深页与首屏同量级（比值 ≤3×）**，且同页 `page_size=1` 与 `page_size=20` 耗时差 ≤2×（本地探针实测 ≈1.6× / ≈1×）；
3. `operator_name=admin&page=1` 及 `status` / `resource_name` 过滤首屏与无过滤首屏同量级，`EXPLAIN` 走组合索引（`ref`/`range` + `Backward index scan; Using index`），非全表扫描 + filesort；
4. `pagination.total` 无过滤跨页、过滤越末页均等于完整集合计数，不塌缩为 0；
5. 相邻页按 `created_at DESC, id DESC` 单调，跨页 id 无重复无遗漏（新旧实现对拍一致，已在本地探针验证）；
6. `/monitor/metrics` 可见 `SELECT count(*) FROM operation_logs …` 的 SQL 指标；
7. 契约未变：未引入 `page` 上限、未改游标，无需文档化 4xx。

## 8. 参考文档

- [Issue #275](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/275)（含 QA 探针数据与证据索引）
- `ai-gateway-api/endpoints/openapi_v1/operation_log/list.go`
- `ai-gateway-api/storage/rdb/ioperlog/operation_log.go`
- `ai-gateway-api/storage/rdb/internal/dao/table_operation_logs.go`、`…/internal/curd.go`、`…/internal/builder.go`
- `ai-gateway-api/stateful/sql_record.go`（SQL 埋点机制）
- `ai-gateway-api/db_ddl.sql`（`operation_logs` 表与现有索引）
- `ai-gateway-api/design-docs/api-define/OpenAPI接口定义/operation-logs.md#2.1`、`00-common.md`（分页契约，本次不变更）
- 本地验证：`operlog_probe` 库（MySQL 8.4.9，250,000 行），探针脚本与结果见本目录 `design-changes.md#8`

## 9. 实施记录

已按本文档完成实现（2026-10-23）：

- `storage/rdb/internal/dao/table_operation_logs.go`：新增 `TOperationLogListDeferred`（内层 gendry 编译取 id + 外层 JOIN 派生表模板拼接 + `SQLRecord` 埋点）；`TOperationLogCount` 补齐 `SQLRecord` 埋点；
- `storage/rdb/ioperlog/operation_log.go`：`List` 有分页走延迟关联、`offset >= total` 短路返回空页、无分页保持旧路径；endpoint / model 层零改动；
- `db_ddl.sql`：新增 `idx_operator_name_created` / `idx_status_created` / `idx_resource_name_created` 三个组合索引；
- 测试：`table_operation_logs_test.go`（sqlmock）、`table_operation_logs_mysql_test.go`（`//go:build mysql`，本机 MySQL 逐页对拍新旧路径 id 序列与排序单调性）、`storage/rdb/ioperlog/operation_log_test.go`（三条路径与越界短路）；
- 验证：`go test ./...` 全量通过；`go test -tags mysql ./storage/rdb/internal/dao/` 通过；`go vet` 干净；新文件许可证头与既有文件一致（本机未装 license-eye，已人工比对）。
