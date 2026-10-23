# Issue #280：operation-logs 过滤列无可用索引（存量库缺索引迁移）修复方案

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/280](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/280)

> QA 环境（MySQL 8.4，`operation_logs` 252,118 行）上，`GET /open-api/v1/operation-logs` 带 `operator_name` / `status` / `resource_name` / `resource_id` 过滤时首页耗时 3.6–20 s（无过滤首页 43.9 ms）。接口返回 200、结果子集与 `pagination.total` 均正确。
>
> 根因（issue 已实锤，本次源码直读全部复核一致）：
> 1. `db_ddl.sql:632-634` 已有 `idx_operator_name_created` / `idx_status_created` / `idx_resource_name_created` 三个组合索引（#275 引入，commit `75d0c51`），但仓库唯一的增量脚本 `database/db_ddl_upgrade_to_0.0.11.sql` **不含**这三条索引 → 全量建库生效、**存量库未执行 ALTER**，过滤仍全表扫描；
> 2. `resource_id` 单独过滤无可用索引：`idx_resource(resource_type, resource_id)` 带前导列，`resource_id=?` 无法使用（实测 17.4 s vs 带 `resource_type` 时 56 ms），且不存在 `(resource_id, created_at)` 索引；
> 3. 每个过滤请求执行两遍全表扫描（`TOperationLogCount` + 延迟关联内层子查询），且 `pagination.total` 无缓存，同一过滤条件连翻 3 页执行 3 次全表计数（实测合计 27,243 ms）；
> 4. `operation-logs.md#2.1` 写明 `operator_name` / `resource_name` “支持模糊匹配”，而实现为等值过滤（`A-05`，待产品裁定，直接决定索引形态）。

本卡是 #275 的**部署缺口残余项**：#275 的延迟关联修复了深分页回表，索引进了全量 DDL，但缺“存量库如何获得这些索引”的交付物。全新建库环境不复现，按增量脚本升级的存量环境稳定复现。

## 2. 目标

在**不变更公开契约**（`operation-logs.md#2.1` 与 `00-common.md` 通用分页约定；`pagination.total` 为完整过滤集合计数 `A-02`，不降级为近似值）的前提下：

1. 存量库执行一次增量脚本后获得全部过滤列索引（`operator_name` / `status` / `resource_name` / `resource_id`）；
2. 过滤首页与无过滤首页同量级（issue 参照门限：≤3× 基线 43.9 ms）；
3. `resource_id` 单独过滤可用（不再依赖 `resource_type` 前导列）；
4. 同一过滤条件连续翻页不重复全表 `count(*)`（issue 验收 #6）；
5. 不静默变更契约：不设 `page` 上限、不强制时间窗、不在产品裁定前改模糊匹配语义（与 #275 的裁定一致）。

## 3. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api` |
| 主要文件 | `database/db_ddl_upgrade_to_0.0.11.sql`（追加索引迁移）、`db_ddl.sql`（新增第 4 个索引）；`storage/rdb/ioperlog/operation_log.go`（计数缓存）；`stateful/container/rdb/components.go`（装配）；`CHANGELOG.md`、`docs/zh_cn/upgrade.md` |
| 接口契约 | OpenAPI 请求/响应字段、分页语义、`pagination.total` 定义**均不变** |
| 数据库 | MySQL：存量 `ALTER TABLE ... ADD INDEX`（INPLACE/NONE 锁）；SQLite 本期不做（沿用 #275 决策，见 `2026-10-23-issue-275-operation-logs-deep-page/design-changes.md#7`） |
| 数据迁移 | 仅在线加索引（MySQL 8.4 二级索引默认 `ALGORITHM=INPLACE, LOCK=NONE`，25 万行实测秒级，#275 已在 QA 验证） |
| 部署动作 | QA 存量库手动执行追加后的增量脚本（验收前提，见 §7） |

## 4. 最终方案

**核心：存量索引迁移脚本（4 条 ALTER）+ `pagination.total` 计数缓存（Redis + 世代失效）。**

### 4.1 存量索引迁移（核心修复）

向 `database/db_ddl_upgrade_to_0.0.11.sql` 追加 §5（沿用该文件 §3.3 / §4 的“后补章节”先例；0.0.11 正式版尚未发布，preview 期间补齐升级路径，不新开 0.0.12 脚本）：

```sql
-- ---------------------------------------------------------------------------
-- 5. 新增索引（operation-logs 过滤列，issue #280）
-- ---------------------------------------------------------------------------
-- 背景：#275（commit 75d0c51）把过滤列组合索引写进了 db_ddl.sql（全量建库），
-- 但本脚本此前未包含对应 ALTER，存量库过滤查询仍全表扫描（首页 3.6-20 s）。
-- 已在 2026-10-23 之前执行过本脚本的存量库：无需重跑全文，只需补执行本节
-- （脚本非幂等，重复执行会报 "Duplicate key"，跳过即可）。
ALTER TABLE `operation_logs`
  ADD INDEX `idx_operator_name_created` (`operator_name`, `created_at`),
  ADD INDEX `idx_status_created` (`status`, `created_at`),
  ADD INDEX `idx_resource_name_created` (`resource_name`, `created_at`),
  ADD INDEX `idx_resource_id_created` (`resource_id`, `created_at`),
  ALGORITHM=INPLACE, LOCK=NONE;

-- 回滚（仅供应急）：
-- ALTER TABLE `operation_logs`
--   DROP INDEX `idx_operator_name_created`,
--   DROP INDEX `idx_status_created`,
--   DROP INDEX `idx_resource_name_created`,
--   DROP INDEX `idx_resource_id_created`;
```

同步修改 `db_ddl.sql`：在 `operation_logs` 建表语句（`db_ddl.sql:632-634`）中补第 4 条 `KEY idx_resource_id_created (resource_id, created_at)`，保持全量 DDL 为存量迁移后的超集。

设计要点：

- **为什么补 `(resource_id, created_at)`**：现有 `idx_resource(resource_type, resource_id)` 前导列是 `resource_type`，`resource_id` 单独过滤走不上（issue 实测 17.4 s vs 56 ms）；组合索引 `(resource_id, created_at)` 同时支撑等值过滤 + `ORDER BY created_at DESC, id DESC` 覆盖索引反向扫描（InnoDB 索引隐含主键，等价 `(resource_id, created_at, id)`），与 #275 的三个索引同一形态；
- **为什么显式 `ALGORITHM=INPLACE, LOCK=NONE`**：加二级索引本就走 INPLACE；显式声明让无法就地执行时**报错而非退化为锁表**，对生产库更安全。`resource_name` varchar(512) utf8mb4 = 2048 B < 3072 B 上限，可全列索引（#275 已实测）；
- 至此**全部文档化过滤列**均有可用索引：`operator_name`/`status`/`resource_name`/`resource_id`（本节新增）、`action`（`idx_action`）、`resource_type`（`idx_resource` 前导列）、`resource_parent_id`（`idx_resource_parent`）、时间窗（`idx_created_at`）。任意合法过滤组合至少可命中一个索引，根除全表扫描路径。

### 4.2 `pagination.total` 计数缓存（Redis + 世代失效）

`storage/rdb/ioperlog` 的 `List` 中，`TOperationLogCount` 目前每请求必执行。引入按过滤条件缓存的精确计数，满足 issue 验收 #6（同一过滤条件连翻 3 页，`count(*)` 调用数 ≤1），且不违反 `A-02`（不是近似值，见下）：

- **缓存位置**：storager 层（`OperationLogStorager` 增加 count 缓存依赖，构造时注入），manager / endpoint 零改动；遵循 AGENTS.md 的依赖注入约定，不直接引用 `stateful.DefaultClientSet`；
- **缓存键**：过滤条件（`LogID`/`OperatorType`/`OperatorID`/`OperatorName`/`Action`/`ResourceType`/`ResourceID`/`ResourceName`/`ResourceParentID`/`Status`/`StartTime`/`EndTime`，**不含** `Page`/`PageSize`）做确定性序列化后取 SHA-256，key 为 `OPERLOG_COUNT:<hash>`（沿用 `model/ibatch` 的大写冒号前缀风格）；
- **值与失效**：value 为 `<gen>:<total>`。世代键 `OPERLOG_COUNT_GEN` 在每次 `BatchCreate` **成功**后 `Incr`。读取时比对世代：一致直接返回，不一致视为 miss 重新计数并覆写。旧世代条目无需删除，靠 TTL 自然过期；
- **TTL**：10 s（覆盖一个异步刷盘周期 5 s 并留余量）；
- **有界 staleness 的语义论证**：操作日志为异步批量刷盘——`Record()` 进 buffer 后 ≤5 s 才落库，**尚未落库的日志本来就不会出现在实时 `count(*)` 里**；而已落库的写入会立即顶起世代键使缓存失效。因此缓存值与实际即时计数的一致性，等价于实时 count 自身的一致性（差别仅多实例无共享 Redis 时跨实例刷盘的 TTL 边界，≤10 s）。这不是把 `total` 改成近似值，不构成对 `A-02` 的静默降级；
- **降级**：Redis 任何错误（未配置/超时/键缺失）一律落回实时 `count(*)`，缓存永不阻断请求。`stateful` 在未配置 Redis 时用 `MockRedisClient`（进程内）兜底，单机部署语义不变；多实例且无共享 Redis 时各实例独立缓存，staleness 上界仍为 TTL；
- **只缓存 count，不缓存 list**：列表查询在索引补齐后已是毫秒级，无缓存必要；
- Redis 客户端能力复用 `redis_client.Client` 的 `Get`/`Setex`/`Incr`/`GetInt64`（`MockRedisClient` 已全量实现，测试可直接用 mock）。

### 4.3 A-05 模糊匹配语义（产品裁定项，本期不动实现）

`operation-logs.md#2.1` 的“支持模糊匹配”与实现（等值 `WHERE (operator_name=?)`，全仓 `ioperlog`/`dao` 无 LIKE 使用）不一致，issue 列为**待产品裁定**。裁定前不加模糊实现（避免白做/做错），但各分支的索引预案已核实可行：

| 裁定结果 | 实现路径 | 索引形态 |
|------|------|------|
| 等值匹配（**本期默认**，现状行为） | 不动 | 本期 4 个组合索引直接覆盖 |
| 前缀匹配 `kw%` | `filterToParam` 改为 `LIKE 'kw%'`（转义 `%`/`_`）；DAO builder 已支持 `like` 操作符（`storage/rdb/internal/dao/internal/builder_test.go:104` 有先例） | **同一批 B-tree 索引可用**，零 DDL 变更 |
| 包含匹配 `%kw%` | 需 `MATCH ... AGAINST` + ngram 解析器 FULLTEXT 索引 | 另行出 DDL 与探针验证，不在本期 |

本期方案对等值/前缀两个分支都成立，因此索引迁移先行不构成返工风险。

### 4.4 不采纳 / 二期项（附理由）

| 备选 | 结论 | 理由 |
|------|------|------|
| `page` 上限 / 强制时间窗 | 不采纳 | 违反已批准契约（任意 `page>0` 合法），#275 已裁定；如产品裁定加上限，走文档化 4xx + api-define 更新流程 |
| statement timeout（`MAX_EXECUTION_TIME`） | 二期可选 | 索引补齐后全部文档化过滤列均有索引，无已知全表扫描路径；超时会把慢查询变成 500，响应语义劣于“慢但正确”。随 `A-04`（时延门限）产品裁定一并决定 |
| ngram FULLTEXT | 仅当 A-05 裁定 `%kw%` | 见 §4.3 |
| 表分区 / 日志 TTL 清理 | 运维长期项 | 数据生命周期策略属产品/运维裁定，与本期解耦（#275 已列） |
| SQLite DDL 同步 | 本期不做 | 沿用 #275 决策，只考虑 MySQL |

## 5. 实施步骤

1. **SQL（可独立交付，先行）**
   - `database/db_ddl_upgrade_to_0.0.11.sql`：追加 §5（§4.1 全文，含回滚注释与“已升级存量库只需补执行本节”说明）；
   - `db_ddl.sql`：`operation_logs` 建表语句补 `idx_resource_id_created`；
   - `docs/zh_cn/upgrade.md`：第 19 行脚本覆盖范围的枚举补上“operation-logs 过滤索引”；
2. **代码**
   - `model/ioperlog/types.go`：定义 count 缓存依赖的小接口（`Get`/`Setex`/`Incr`/`GetInt64` 子集）；
   - `storage/rdb/ioperlog/operation_log.go`：`List` 中 `TOperationLogCount` 走缓存（命中返回 / miss 计数后写入 / Redis 错误落回实时）；`BatchCreate` 成功后 `Incr` 世代键；过滤条件确定性序列化 + SHA-256 的 key 生成；
   - `stateful/container/rdb/components.go`：装配时把 `stateful.DefaultClientSet.RedisClient` 注入 storager；
3. **测试**
   - 缓存单测（MockRedisClient）：命中 / miss / 世代失效 / page 与 page_size 不参与 key / 各过滤字段参与 key / Redis 错误降级实时计数；
   - `filterToParam` 与既有 DAO 测试不回归（sqlmock）；`make test-model-cover-gate` 保持 ≥70%；
4. **文档**
   - `CHANGELOG.md` Unreleased/Fixed：存量库 operation-logs 过滤索引迁移 + total 计数缓存；
5. **QA 部署与验收**
   - QA 存量库执行 §5 → `SHOW INDEX` 核对 4 条索引 → 跑 issue 最小复现 6 步 → `EXPLAIN` 核对无 `type=ALL` → `/monitor/metrics` 核对 `sql_cost`（§7 验收判据）。

## 6. 预期收益与风险

| 项目 | 说明 |
|------|------|
| 收益 | `operator_name`/`status`/`resource_name` 过滤首页 3.6–20 s → 亚秒级（#275 本机同版本探针：有过滤组合索引时首屏 0.066 s，为无过滤首屏 0.078 s 同量级）；`resource_id` 单独过滤 17.4 s → 毫秒级；同一过滤条件翻页计数从“每页一次 count”降为“TTL 窗口内一次”（27,243 ms / 3 页 → ≈单次 count）；DB CPU/I/O 占用消除 |
| 契约兼容 | 请求/响应字段、分页参数、`pagination.total` 语义、排序键全部不变；前端无需改动 |
| 索引成本 | 4 个二级索引增加写放大与磁盘（25 万行秒级构建，#275 已实测）；审计写入为异步批量低频刷盘，可接受 |
| 缓存风险 | 世代键为单点 Redis 计数器：Redis 故障时读取必然 miss → 自动降级实时 count，无正确性风险；多实例无共享 Redis 时 staleness ≤ TTL 10 s（§4.2 语义论证） |
| 脚本风险 | 脚本非幂等与现有升级脚本一致（MySQL 无 `ADD INDEX IF NOT EXISTS`），§5 注释明确说明重复执行报错可跳过，与 §3.3 先例同风格 |

## 7. 验收判据

对照 issue #280 验收 7 条（QA `172.19.1.222:8183`，存量库执行 §5 后复测）：

1. `SHOW INDEX FROM operation_logs` 含 `(operator_name, created_at)`、`(status, created_at)`、`(resource_name, created_at)`、`(resource_id, created_at)` 四组索引；
2. `EXPLAIN` 下 `count(*)` 与列表子查询对四个过滤列均不再出现 `type=ALL`（预期 `ref` + `Backward index scan; Using index`）；
3. `page=1&page_size=20` 上加 `operator_name` / `status` / `resource_name` / `resource_id` 任一过滤，耗时 ≤ 无过滤首页（43.9 ms 基线）的 3 倍；
4. #275 行为不回归：无过滤 `page=1` 毫秒级、深分页（`page=12535`）亚秒级；
5. `pagination.total` 语义不回归：`operator_name=admin`→73、`status=2`→5,731、`resource_name=batch-controlled-001082`→1、`resource_id=1162`→4，与加索引前一致且为完整过滤集合计数；
6. 同一过滤条件连续翻 3 页：`sql_cost` 中该 `count(*)` 调用数 ≤1（缓存命中），或每页耗时均为毫秒级；
7. 过滤结果正确性不回归：`Data.list` 每行满足传入过滤条件；`resource_type` 与 `resource_id` 组合时不得跨类型命中。

## 8. 参考文档

- [Issue #280](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/280)（QA 探针数据、根因链、证据索引、验收判据）
- `design-docs/modifications/2026-10-23-issue-275-operation-logs-deep-page/`（延迟关联 + 前 3 个索引 + count 埋点；本卡为其存量迁移残余项，TTL 缓存设计沿其 §5.5 二期 sketch）
- `ai-gateway-api/db_ddl.sql`（`operation_logs` 表，行 604-635）
- `ai-gateway-api/database/db_ddl_upgrade_to_0.0.11.sql`（唯一存量增量脚本，将追加 §5）
- `ai-gateway-api/storage/rdb/internal/dao/table_operation_logs.go`（`TOperationLogListDeferred` / `TOperationLogCount`）
- `ai-gateway-api/storage/rdb/ioperlog/operation_log.go`（`filterToParam` 等值过滤、`List` 计数调用点）
- `ai-gateway-api/design-docs/api-define/OpenAPI接口定义/operation-logs.md#2.1`、`00-common.md`（契约，本次不变更；A-04/A-05 为待确认条款）
- `ai-gateway-api/stateful/config_redis.go`、`mock_redis.go`（Redis 客户端与 mock，`Get`/`Setex`/`Incr`/`GetInt64` 能力）

## 9. 实施记录（六步法执行结果）

已按 `design-docs/README.md` 六步法完成（2026-10-23）：

1. **变更记录**：本目录 `change-summary.md`；
2. **api-define**：无需变更——接口契约（请求/响应字段、分页语义、`pagination.total` 定义）未变；`operator_name`/`resource_name` 的“模糊匹配”文案与实现对齐（`A-05`）仍为产品裁定项，裁定前不动文案与实现；
3. **sys-design**：`details/操作日志模块.md` 新增 §8.2（存量索引迁移 + 计数缓存设计），§8.1/§10 同步更新；`summary.md` 操作日志模块条目补记 issue #280；未新增长期 details 文档（§8.2 已足够，变更记录即完整设计载体）；
4. **实现**：
   - `database/db_ddl_upgrade_to_0.0.11.sql`：追加 §5（4 条 `ADD INDEX`，`ALGORITHM=INPLACE, LOCK=NONE`，含回滚注释与“已升级存量库只需补执行本节”说明）；
   - `db_ddl.sql`：`operation_logs` 新增 `idx_resource_id_created (resource_id, created_at)`；
   - `storage/rdb/ioperlog/count_cache.go`（新）：`countCache`——过滤条件确定性序列化（`Page`/`PageSize` 除外，值经 `url.QueryEscape` 防分隔符串键）→ SHA-256 为 key，key 内嵌世代号 `OPERLOG_COUNT:G<gen>:<hash>`，TTL 10 s；值以 +1 偏移存储（0 保留为 miss 哨兵，空结果集可正确缓存）；真实 redigo 缺失键的 `redigo: nil returned` 归一化为世代 0；`storage/rdb/ioperlog/operation_log.go`：`List` 计数走缓存（命中返回 / miss 实时计数后回写 / Redis 故障自动降级），`BatchCreate` 成功后 `Incr` 世代键；构造器注入 `redis_client.Client`，nil 禁用缓存；
   - `stateful/container/rdb/components.go`：装配注入 `stateful.DefaultClientSet.RedisClient`（与 `quotacache` 同惯例；Redis 未配置时 `config_redis.go` 已兜底为 `MockRedisClient`，进程内缓存语义不变）；
5. **测试**：`count_cache_test.go`（key 稳定性/区分度/分页参数不参与/分隔符注入不串键、0 值往返、世代失效、redigo-nil 兼容、故障降级）；`operation_log_test.go` 新增 3 个集成测试（同过滤翻页不重复计数、BatchCreate 后世代失效重计、Redis 故障降级实时计数）。验证：`go build ./...`、`go vet`、`go test ./...` 全量通过；
6. **汇总**：CHANGELOG Unreleased/Fixed、`docs/zh_cn/upgrade.md` 脚本覆盖说明已同步；无长期 details 文档新增。

遗留（需环境/产品侧推进，不在代码交付内）：QA 存量库（`172.19.1.222:8183`）需手动执行升级脚本 §5 并按本文档 §7 验收判据复测（issue 验收 7 条）；`A-04`（时延门限）与 `A-05`（模糊匹配语义）待产品裁定，裁定结论可能影响后续迭代但不阻塞本修复。
