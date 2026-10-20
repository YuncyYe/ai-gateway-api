# Issue #254：批量任务终态字段缺失与下载结算不触发——控制面修复方案

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/254](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/254)

> 批量链路（`POST /v1/files` → `POST /v1/batches` → 轮询）经 BFE 透传到受控后端后正常进入 `completed`，数据面 `GET /v1/batches/{id}` 返回了 `output_file_id` 与 `request_counts`，但控制面 `GET /open-api/v1/batches/{id}` 对同一批次返回的 `entity_id`/`endpoint`/`output_file_id`/`request_counts`/`usage_input_tokens`/`usage_output_tokens`/`usage_source`/`reserve_units`/`settle_units` 九个字段全部缺失，`settle_status` 停留在 `reserved`，`batch_files` 只登记输入文件；下载输出文件后 `SETTLE_USAGE_PARSE`、`BATCH_SETTLED` 均无变化。

产品条款依据（Oracle）：`design-docs/api-define/OpenAPI接口定义/batches.md` §2.1（BatchObject 全字段）/ §2.2（`batch_files` 元素形状，`purpose ∈ batch_input|batch_output`）；`design-docs/sys-design/details/批量任务与对账.md` §2（`BATCH_*` 键结构）/ §4（三态对账）/ §5（对账 job 五职责）。

**责任划分**：本缺陷的**主要根因在 BFE 数据面**（`BATCH_TASK` 根本没有写入这些字段、下载拦截的 jsonl usage 解析结构与输出契约不符），BFE 侧修复方案见
`bfe/docs/zh_cn/modifications/2026-10-19-issue-254-ai-batch-task-state-and-settle-fix/design-changes.md`。
**控制面即使拿到完整 Redis，也读不出/补不齐这些字段**，因此本节文档覆盖控制面必须同批落的 5 项修复；两者缺一不可。

## 2. 根因（当前 HEAD 逐环直读核实，2026-10-19）

### 2.1 Redis 同步视图不解析终态字段

`model/ibatch/redis_view.go:49-91`（`ParseRedisTaskView`）只解析
`status` / `est_lines` / `reserved_units` / `reserved` / `usage_in` / `usage_out` / `settle_units` / `settle_status`，
**不解析** `output_file_id` / `request_counts` / `endpoint` / `usage_source` / `created_at`；
`applyTaskView`（`:141-190`）随之无法赋值。而 `batch_tasks` 表的这些列早已存在
（`db_ddl.sql:751-770`、`database/db_ddl_upgrade_to_0.0.11.sql:191-215`），DAO 也支持
（`storage/rdb/batch/batch_task.go:173` 写 `endpoint`、`:196-198` 写 `request_counts` JSON）——
纯属同步链路漏映射，`Endpoint` 更是全仓无任何赋值点。

### 2.2 状态推进只处理非终态，终态任务的产物字段永不补齐

`model/ibatch/advance.go:56-59` 的候选查询是
`&BatchTaskFilter{StatusNotIn: TerminalStatuses, Limit: ...}`；
而 `model/ibatch/job.go:142-160` 的职责顺序是 **sync → advance → reconcile**，
职责① `SyncFromRedis` 会把 Redis 里的 `status=completed` 直接落库（`sync.go:115-142`），
于是职责② 的 `AdvanceStatus` 从下一轮起就再也选不到该行——
`output_file_id` / `request_counts`（以及 `batchResp.Endpoint`，`advance.go:35-40` 的
`providerBatchResponse` 压根没这个字段）**永远不会被补**。设计 §5 职责② 的意图因此被打破。

### 2.3 `batch_files` 关联只认 `OutputFileID`，`purpose` 未映射，`filename` 无列

- `endpoints/openapi_v1/batches/one.go:50-60` 用
  `[]string{task.InputFileID, task.OutputFileID}` 拉关联文件；`OutputFileID` 为空
  （§2.1/§2.2）时输出文件条目不出现——issue 里人工复测的
  "`BATCH_FILE:...` 存在且 `dir=output`，但控制面未列出"正是此因。
- `model/ibatch/types.go:132-144`（`ToMeta`）把 `file.Purpose` 原样输出；数据面存的是
  provider 原值 `batch`，契约要求 `batch_input` / `batch_output`（`batch_files.direction`
  已能唯一区分方向，`types.go:78-81`）。
- `batch_files` 表**无 `filename` 列**（`db_ddl.sql:731-749`、`db_ddl_sqlite.sql:740-756`），
  `ToMeta` 里硬编码空串（`types.go:140`）。

### 2.4 `usage_source=download` 无写入点

`UsageSourceDownload` 只在 `types.go:67` 定义，全仓无引用；
落库路径只有兜底结算的 `UsageSourceReconcile`（`settle.go:222`），
且 `ParseRedisTaskView` 也不解析 `usage_source`。
即：**正常（下载拦截）结算成功的批次，该字段恒为空**。

### 2.5 兜底结算的 jsonl usage 解析与数据面同款缺陷（静默 0 结算）

`model/ibatch/settle.go:387-410`（`handleUsageLine`）按扁平 `response.usage` / `response.model`
解析，而 OpenAI Batch 输出契约是 `response.body.usage` / `response.body.model`
（issue 证据与受控后端实测一致）。后果比"不结算"更糟：
`usageTotals` 全 0 且 `model` 为空 → `calcSettleUnits` 命中 `priceMiss`（`settle.go:312-318`）
→ `settle_units=0`，但 `settle_status` 仍被写进 Redis 并条件更新为 `settled`（`settle.go:205-238`）
——**静默漏结算**，只能靠人工金额核对发现。

### 2.6 回源窗口时间戳为零（次要，不作判据）

`FetchTask`（`manager.go:175-210`）DB miss 时回源 Redis，`applyTaskView` 不设置
`CreatedAt`/`UpdatedAt`，序列化即 `0001-01-01T00:00:00Z`。issue 已注明该现象只在
"尚未落库"窗口存在、不作为判据；数据面补写 `created_at` 后（BFE 方案步骤 5）可一并收敛。

## 3. 影响面

| 项目 | 结论 |
|------|------|
| 接口范围 | `GET /open-api/v1/batches`（列表）、`GET /open-api/v1/batches/{batch_id}`（详情）、`GET /open-api/v1/batch-files/{file_id}`（文件元数据）字段取值变化；`POST /{id}/cancel` 无变化 |
| 对账链路 | 职责①②③ 的行为与候选集变化（新增终态补漏、兜底结算解析修正） |
| DDL | `batch_files` 新增 `filename` 列（3 个 DDL 文件 + DAO/存储映射） |
| 数据面依赖 | 需 BFE 同批上线：`BATCH_TASK` 写 `output_file_id`/`request_counts`/`endpoint`/`created_at`/`reserved_units`/`usage_*`/`settle_units`/`settle_status`/`usage_source`，`BATCH_FILE` 写 `filename` |
| 配置 | 无变更（复用现有对账 job 周期与 `advanceLimit`） |
| 兼容性 | 全部为读路径扩映射 + 新增列；存量行新列 NULL，视图按 `omitempty` 省略，前端无感 |

## 4. 目标

同一 `completed` 批次在控制面详情返回（`batches.md` §3.2 / §2.1 / §2.2）：

1. `Data` 顶层含 `endpoint`、`output_file_id`、`request_counts`、`reserve_units`、
   `settle_units`、`usage_input_tokens`、`usage_output_tokens`、`usage_source`；
   `settle_status` 在下载结算或兜底结算后为 `settled`；
2. `batch_files` 同时含输入与输出两条，元素 `purpose` 分别为 `batch_input` / `batch_output`，
   输入条目带 `filename`（provider File 对象给出）；
3. 对账 job 兜底结算不再把"解析不到 usage"当成"用量为 0 已结算"。

## 5. 最终方案

设计原则：**控制面不重复实现数据面簿记**——能由 Redis 同步取得的字段就只扩映射；只有数据面取不到的
（终态产物的 provider 侧回读、输出文件 `filename`）才由控制面出网。不改计费口径、不改 `BATCH_SETTLED`
去重语义、不改 `WHERE settle_status='reserved'` 条件更新防线。

### 5.1 `model/ibatch/redis_view.go`：视图扩映射（必改）

`redisTaskView` 增加指针字段（保留"hash 未携带 → 不覆盖 DB"语义，`sync.go:139` / `mergeTaskForSync`
的既有约定）：

```go
outputFileID  *string
endpoint      *string
usageSource   string
requestCounts map[string]int64 // nil = 未携带
createdAt     *time.Time
```

`ParseRedisTaskView` 解析 `output_file_id` / `endpoint` / `usage_source` / `created_at`
（unix 秒；同时接受 RFC3339 以兼容控制面自身写入形态）；`request_counts` 按 JSON 解析，
非法/空则视为未携带。`applyTaskView` 赋值，`mergeTaskForSync` 按"是否携带"刷新，
不携带时保留 existing（与 `est_lines`/`usage_in` 现有分支同构）。

`redisFileView` 增加 `filename string`（`ParseRedisFileView` 取 `filename`），
`applyFileView`/`mergeFileForSync` 同规则处理。

### 5.2 `model/ibatch/advance.go` + `job.go`：终态产物补漏（必改）

保留现有"非终态推进"路径不变，新增第二轮候选（职责②b，仍需出网）：

- 候选：`status='completed'` 且 `created_at >= now-24h`（复用已有 `CreatedAtGTE` 过滤，
  `storage/rdb/batch/batch_task.go:136-163`），内存过滤 `OutputFileID == "" || RequestCounts == nil`，
  每轮上限复用 `advanceLimit`（避免一次扫全表）；
- 处理：复用 `advanceOneTask`，并给 `providerBatchResponse` 补 `Endpoint string`（`advance.go:35-40`），
  使 `endpoint` 也能由 provider 回读补齐；
- 404 语义沿用 `handleProviderAbsent`（宽限内跳过，不误判 expired）；
- **代价与约束（明确记录）**：Redis 侧已补字段后，本路径只对"Redis 键已过期但 DB 行仍在"的老批次生效；
  永久缺失的行会按周期重复出网，因此需要限额 + 失败计数告警
  （建议 state/日志计数 `advance_artifact_miss`），并接受"每分钟最多 N 条"的成本。
  带尝试次数的收敛（如 `artifact_attempts` 列）列入遗留，不在本期。

> 说明：不通过新增 SQL `IS NULL` 过滤来实现，是因为现有 DAO where 构造只支持等值/IN
> （`batchTaskFilterToParam`，`batch_task.go:136-163`），改造成本高于内存过滤；本轮以
> "时间窗 + 限量"控制扫描面。

### 5.3 `model/ibatch/types.go` + `endpoints/openapi_v1/batches/one.go`：文件视图（必改）

- `ToMeta()` 按 `direction` 映射契约 `purpose`：`input → batch_input`、`output → batch_output`，
  `direction` 为空时回退 provider 原值（保持向后兼容；`direction` 由数据面 `BATCH_FILE.dir`
  与控制面 `registerOutputFile` 保证存在）；
- `BatchFile` 增加 `Filename string`，`ToMeta` 输出（取代 `types.go:140` 的硬编码空串）；
- `one.go` 的关联文件组装逻辑不变（仍按 `InputFileID`/`OutputFileID` 拉取），但
  `OutputFileID` 在 §5.1/§5.2 落地后不再为空；输出条目在数据面未带 `filename` 时由
  §5.3-补充项兜底。

**补充项（可选，需产品确认）**：输出文件 `filename` 的权威来源只有 provider File 对象。
控制面 `advance` 已出网，可在补登记输出文件时追加 `GET {base}/files/{id}` 取 `filename`/`bytes`；
若判定"输出文件名为 `{batch_id}.jsonl`"这类派生值可接受，则免一次出网。二者择一后再实现。

### 5.4 `model/ibatch/settle.go`：jsonl usage 解析修正（必改）

`handleUsageLine`（`:387-410`）改双形态解析：`response.body.usage` / `response.body.model` 优先，
回退扁平 `response.usage` / `response.model`（与 BFE 修复保持完全一致，避免两侧口径漂移）。

同时补一道**安全阀**：`parseJSONLUsage` 结束时若 `parsedLines == 0` 且 `totals.inputTokens+outputTokens == 0`，
不得推进 `settled`——按"本轮不结算、记 warn + 计数、下轮重试"处理（输出文件可能仍在生成）。
这条与 issue 的"静默漏结算"直接相关，避免将来任何解析口径问题再次变成 0 元结算。

### 5.5 DDL：`batch_files.filename`（必改）

三处同步（0.0.11 尚未发版，`version/version.go:30` 仍为 `0.0.10`，升级脚本可直接改）：

| 文件 | 改动 |
| --- | --- |
| `db_ddl.sql:731-749` | `batch_files` 新增 `filename VARCHAR(256) COMMENT '文件名（provider File 对象，可空）'`，位置在 `purpose` 之后 |
| `db_ddl_sqlite.sql:740-756` | 同列 `filename TEXT` |
| `database/db_ddl_upgrade_to_0.0.11.sql:170-188` | 同列（与基线保持一致；若该脚本已对外发布则改为追加 `ALTER TABLE`，二选一由发布状态决定） |

配套代码：`storage/rdb/internal/dao/table_batch_files.go`（`TBatchFile` 加 `Filename string`、
`TBatchFileParam` 加 `Filename *string`）、`storage/rdb/batch/batch_file.go:83-120`（`batchFileDataToParam` /
`batchFileParamToData` 映射）、`model/ibatch/types.go`（`BatchFile.Filename`）。

### 5.6 与数据面的契约对齐

本节所有字段名与 BFE 方案 §三-2.4 的 `BATCH_TASK` / `BATCH_FILE` 最终布局一一对应，
需在两侧评审时**对同一张表逐字段核对**（`output_file_id`、`request_counts`（JSON 串）、`endpoint`、
`created_at`（unix 秒）、`reserved_units`、`usage_in`、`usage_out`、`settle_units`、`settle_status`、
`usage_source`、`lines`、`bytes`、`purpose`、`dir`、`filename`、`batch_id`）。

## 6. 行为变化表

| 场景 | 修复前 | 修复后 |
|------|--------|--------|
| 详情：completed 批次 | `output_file_id`/`request_counts`/`endpoint`/`reserve_units`/`settle_units`/`usage_*` 缺失 | 由 Redis 同步补齐；Redis 缺则终态补漏出网补齐 |
| 详情：`batch_files` | 仅输入条目，`purpose=batch`，无 `filename` | 输入 + 输出两条，`purpose=batch_input|batch_output`，输入条目带 `filename` |
| 下载后 `settle_status` | 恒 `reserved` | `settled`（`usage_source=download`）；job 兜底为 `reconcile` |
| 兜底结算：输出文件无 usage | 记为 `settled`、`settle_units=0` | 不推进，warn + 计数，下轮重试 |
| 回源窗口 `created_at` | `0001-01-01T00:00:00Z` | 取 Redis `created_at`（旧键仍为零，issue 已注明不作判据） |
| 未完成批次 | 不变 | 不变 |
| 取消 / 列表分页 / 404 / 409 语义 | 不变 | 不变 |

## 7. 测试

单元测试（`model/ibatch`，手写 fake 按 `TESTING.md` 约定）：

1. `redis_view_test.go`：`ParseRedisTaskView` 新字段解析（含 `request_counts` 非法 JSON → 视为未携带）、
   `mergeTaskForSync` 在"未携带"时保留 DB 既有值、终态不回退仍成立；
   `ParseRedisFileView` / `mergeFileForSync` 的 `filename` 同规则；
2. `advance_test.go`：新增"终态产物补漏"用例——`completed` 且缺 `output_file_id`/`request_counts`
   → 出网补齐并 `registerOutputFile`；provider 404 → 走宽限跳过、不改状态；
   已有产物 → 不出网（候选过滤生效）；
3. `settle_test.go`：`response.body.usage` 解析用例 + 扁平旧格式兼容用例 +
   "无任何 usage → 不推进 settled"安全阀用例（断言 `UpdateBatchTaskSettleStatus` 未被调用）；
4. `types_test.go`：`ToMeta()` 的 `purpose` 按 direction 映射、`direction` 空时回退原值、
   `filename` 透出；
5. `batches_test.go`：详情组装输出两条 `batch_files` 且键集合符合 §2.2。

集成测试（`test/integration`，按 `.agents/skills/ai-gateway-api-integration-test-design` 方法论）：

6. `tests/batch/detail/`：completed 批次详情字段完整性锁（九个字段 + `batch_files` 两条）+
   `purpose` 取值锁 + `filename` 存在性；原始报文禁止出现内部字段（沿用 #249 的防泄漏断言）；
7. `tests/batch/file/`：§3.4 文件元数据 `purpose`/`filename` 断言；
8. 对账 job：以数据面真实 Redis 键（或另一侧 e2e 场景 SC1401-TC026 的运行结果）验证
   sync/advance/settle 三条路径。

回归验证：

```bash
cd ai-gateway-api
go build ./... && go vet ./...
go test ./model/ibatch/... ./endpoints/openapi_v1/batches/...
make test-model-cover-gate   # model ≥70%
go test ./...
```

## 8. 文档与契约

- `design-docs/sys-design/details/批量任务与对账.md` §2：`BATCH_TASK` / `BATCH_FILE` 键结构表补
  `output_file_id` / `endpoint` / `request_counts` / `created_at` / `filename`（与 BFE 方案 §三-2.4 对齐），
  §5 职责② 补一句"终态产物补漏"；
- `design-docs/api-define/OpenAPI接口定义/batches.md` §2.2：`filename` 说明从"一期留空"改为
  "provider File 对象给出，缺失时省略"（原注记为 #249 的裁剪项，本次收敛）；
- 契约字段集本身不变（九个字段与 §2.2 元素形状都是已批准条款，本次是**运行行为向契约收敛**）。

## 9. 实施状态与排期

- 代码：**未实施**（本文档为方案与修改说明，供评审后落地）。
- 排期约束：必须与 BFE
  `bfe/docs/zh_cn/modifications/2026-10-19-issue-254-ai-batch-task-state-and-settle-fix/design-changes.md`
  **同批发布**。仅上控制面：老 `BATCH_TASK` 缺字段，视图扩映射拿不到值（仍需终态补漏出网，成本高）；
  仅上数据面：控制面读不出（§2.1/§2.4）。
- 灰度顺序建议：先 BFE（写端扩字段，纯新增、向后兼容）→ 再控制面（读端扩映射 + 终态补漏）→
  最后修 `settle.go` 解析与安全阀（此项独立可回滚，优先级最高，避免继续产生 0 元结算）。

## 10. 遗留

- `entity_id`：控制面已从 `api_key` 解析（`redis_view.go:162-164`），issue 复现为空须先确认对应
  api_key 在库里是否真有 `entity_id`——属数据核查，不是本次代码修复项。
- 回源窗口 `updated_at` 为零：`BATCH_TASK` 无该语义字段；如需严格 RFC3339 可改 `BatchTask`
  时间字段为指针 + `omitempty`，属对外形状变更，需产品确认后另起小卡。
- 终态补漏的重复出网成本：如实测告警量偏高，再引入"尝试次数"列（DDL 增量）做退避。
- 输出文件 `filename` 的派生值方案（§5.3 补充项）待产品确认。
## 11. 实施记录（2026-10-19，六步法 Step 5）

代码与测试已按 `design-changes.md` 落地。

### 11.1 改动文件

| 文件 | 改动 |
| --- | --- |
| `model/ibatch/redis_view.go` | `redisTaskView` 扩 `outputFileID`/`endpoint`/`requestCounts`/`usageSource`/`createdAt`（含 `parseRequestCounts`、`parseUnixOrRFC3339` 两个辅助）；`redisFileView` 扩 `filename`/`cluster`/`batchID`/`linesSrc`/`bytesSrc`；`applyTaskView`/`applyFileView` 赋值；`mergeTaskForSync` 补 `created_at` 的"未携带不抹零"分支、`mergeFileForSync` 补 `filename` |
| `model/ibatch/advance.go` | `providerBatchResponse` 补 `endpoint`；推进路径 patch `endpoint`；新增职责②b `BackfillTerminalArtifacts` + `backfillOneTask`（404/非 2xx/脏 JSON 一律告警跳过、不改状态） |
| `model/ibatch/job.go` | `ReconcileManager` 扩 `BackfillTerminalArtifacts`；`RunOnce` 在 advance 与 reconcile 之间插入职责②b |
| `model/ibatch/settle.go` | `batchResultRow`/`responseBody`/`usageTokens` + `usageAndModel()`：`response.body.usage` 优先、扁平回退；`usageTotals` 扩 `noUsageLines`/`badLines`；`settleOneTask` 增加安全阀（`parsedLines==0 && noUsageLines>0` → 不推进 settled、释放 claim、下轮重试） |
| `model/ibatch/types.go` | `BatchFile.Filename`；`BatchTaskProgressPatch.Endpoint`；`ToMeta` 改用 `contractPurpose()` 按 `direction` 映射契约枚举；新增 `PurposeBatchInput`/`PurposeBatchOutput` |
| `db_ddl.sql` / `db_ddl_sqlite.sql` / `database/db_ddl_upgrade_to_0.0.11.sql` | `batch_files.filename` |
| `storage/rdb/internal/dao/table_batch_files.go` / `storage/rdb/batch/batch_file.go` | `filename` 结构字段与映射 |
| `storage/rdb/batch/batch_task.go` | `UpdateBatchTaskProgress` 透传 `Endpoint` |
| `model/ibatch/{redis_view,advance,settle,types,job}_test.go` | 见 §11.3 |
| `test/integration/tests/batch/{helper,detail,file}`、`tests/batch/design.md`、`tests/schema/openapi/*` | 见 §11.3 |

### 11.2 与方案的偏差（实施中确认）

1. **补漏不复用 `advanceOneTask`**：终态任务经 404 会被 `handleProviderAbsent` 判 `expired`（改写 `completed`），故独立实现 `backfillOneTask`，404 只告警。这是 `design-changes.md` §3.1 已写明的差异，实施时再次确认必要。
2. **安全阀拆两个计数**：`noUsageLines`（有 response 无 usage）触发拦截，`badLines`（非法 JSON）只记录不拦截——只有错误行/空文件（`noUsageLines==0`）照常按 0 元结算，避免"全部请求失败"的合法形态被永久卡住。
3. **`purpose` 映射放在视图层**（`ToMeta`→`contractPurpose`）：`batch_files.purpose` 列继续保留 provider 原值，便于与 provider 对账；DB 列不做值转换。
4. **`BatchTaskProgressPatch` 新增 `Endpoint`**（方案只写了"patch 缺失字段"）：存储层 `UpdateBatchTaskProgress` 同步透传，否则 endpoint 无法经补漏写入。

### 11.3 测试

| 类型 | 用例 |
| --- | --- |
| 单测（`model/ibatch`） | `TestParseRedisTaskView` 扩（新字段、RFC3339/unix 两形态、脏值不携带）；`TestParseRedisFileView` 扩（filename/cluster/src）；`TestMergeTaskForSync` 新增"终态字段按携带性刷新 + created_at 不抹零"；`TestBackfillTerminalArtifacts_{FillsMissingFields,CompleteTaskSkipped,Provider404KeepsStatus,ProviderErrorsSkipped,MismatchedID,NoProviderQuerier}`；`TestParseJSONLUsage` 扩（body 契约、混合形态、noUsage/badLines）；`TestReconcileSettle_NoUsageNotSettled`、`TestReconcileSettle_OnlyErrorRowsSettlesZero`；`TestBatchFileToMeta` 新增 purpose 映射与 filename 子用例；`job_test.go` fake 扩 backfill（含调用顺序锁 `sync→advance→backfill→settle`） |
| 集成（`test/integration`） | BT-2-002 键集合锁扩为 §2.2 六键（含 filename）；新增 **BT-2-005 purpose 按方向映射**（列内 `batch` → 视图 `batch_input`/`batch_output`，filename 缺失时键省略）；BT-4-001 增加 filename 断言；schema `BatchFileMetaSchema` 注释与 `testBatchSchema` 键锁同步 |

### 11.4 验证结果

| 范围 | 结果 |
| --- | --- |
| `go build ./...` / `go vet`（ibatch、storage/rdb/batch、dao、endpoints/batches） | 通过 |
| `go test ./model/... ./lib/... ./endpoints/...` | 全部通过；`model/ibatch` 语句覆盖率 **87.8%**（门禁 70%） |
| 集成 `tests/batch/{detail,file,cancel,list}` | 通过（`cancel`/`list` 因 Windows SQL 日志文件占用需单独包运行，属既有环境陷阱） |
| 集成 `tests/schema/{openapi,innerapi,report}` | 通过 |

### 11.5 六步法核对

| Step | 内容 | 状态 |
| --- | --- | --- |
| 1 | `design-docs/modifications/2026-10-19-issue-254-batch-task-state-and-settle-fix/` | ✅（change-summary + design-changes） |
| 2 | 变更说明编写 | ✅ 本文档 |
| 3 | api-define 更新 | ✅ `batches.md` §2.2 `filename`/`purpose` 说明（字段集不变） |
| 4 | sys-design 更新 | ✅ `批量任务与对账.md` §2 键结构 + §5 职责②b/③；`数据库设计文档.md` §6.14；`summary.md` 索引描述 |
| 5 | 代码 + 单测 + 集成测试 | ✅（含 DDL / DAO / 存储层同步） |
| 6 | 沉淀评估 | 无新增 `details/` 文档：本次是既有 `批量任务与对账.md` 机制的补齐（终态补漏与安全阀已内联进该文档 §5），不构成新核心机制 |

### 11.6 遗留

- 终态补漏的重复出网成本（永久缺失行按周期重试）：实测告警量偏高时再引入尝试次数字段做退避。
- 输出文件 `filename` 仍需 provider `GET /v1/files/{id}` 才能拿到；当前留空省略，待产品确认派生值方案。
- 回源窗口 `updated_at` 为零的行为未变（`BATCH_TASK` 无该字段，改动属对外形状变更）。
