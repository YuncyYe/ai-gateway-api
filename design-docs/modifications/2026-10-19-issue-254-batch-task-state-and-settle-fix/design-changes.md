# Issue #254 控制面：数据模型 / 存储 / 对账 job 设计变更

配套文档：`change-summary.md`（问题、根因、影响面、目标）；接口契约见 `design-docs/api-define/OpenAPI接口定义/batches.md` §2.1/§2.2（本次不改字段集，只补 `filename` 的来源说明）。

本文档给出**落地方案**：数据类型、Redis 契约映射、存储列、对账 job 行为与边界，供 Step 5 实现对照。

---

## 1. 数据模型变更（`model/ibatch`）

### 1.1 `BatchFile` 增加 `filename`

```go
type BatchFile struct {
    ...
    Filename string `json:"filename,omitempty"` // provider File 对象给出的文件名（可空）
    ...
}
```

- 优先来源：数据面 `BATCH_FILE:<cluster>:<file_id>` 的 `filename` 字段（BFE 从 `POST /v1/files` 的 File 对象取得）；
- 缺失（老键、输出文件）时留空，`ToMeta()` 用 `omitempty` 省略，不编造派生值。

### 1.2 `ToMeta()` 按方向映射契约 `purpose`

`batch_files.purpose` 列存的是 **provider 原值**（OpenAI 为 `batch`），契约 §2.2 要求 `batch_input` / `batch_output`。映射放在对外视图层：

| `direction` | `ToMeta().purpose` |
| --- | --- |
| `input` | `batch_input` |
| `output` | `batch_output` |
| 空（历史脏数据） | 透传列值（不猜） |

理由：`direction` 是控制面自己的列（数据面 `dir` + `registerOutputFile` 都保证写入），比 provider 的 `purpose` 更可靠；把映射放在视图层可保持列值原样，便于与 provider 对账。

### 1.3 `redisTaskView` / `redisFileView` 扩字段

`redisTaskView` 新增（指针/零值语义与既有字段一致——**hash 未携带则不覆盖 DB**）：

| 字段 | Redis 来源 | 类型 | 说明 |
| --- | --- | --- | --- |
| `outputFileID` | `output_file_id` | `*string` | completed 批次应有值 |
| `endpoint` | `endpoint` | `*string` | provider 透传端点 |
| `requestCounts` | `request_counts` | `map[string]int64` | JSON 对象；非法/缺失 = 未携带 |
| `usageSource` | `usage_source` | `string` | `download` / `reconcile` |
| `createdAt` | `created_at` | `*time.Time` | 接受 unix 秒（数据面写入）与 RFC3339（控制面自写） |

`redisFileView` 新增 `filename string`。

### 1.4 合并语义（`mergeTaskForSync` / `mergeFileForSync`）

保持既有规则：**hash 携带即刷新，未携带保留 DB 值**；终态不回退不变。新增字段遵循同一规则，另加：

- `usageSource`：非空即刷新（`download` 与 `reconcile` 都可能是终值，不设优先级）；
- `createdAt`：仅当 hash 携带时刷新，避免把 DB 的真实创建时间抹零；
- `filename`：非空即刷新。

---

## 2. 存储层变更

### 2.1 DDL：`batch_files` 新增 `filename`

三处同步（`version/version.go` 仍为 `0.0.10`，`db_ddl_upgrade_to_0.0.11.sql` 尚未发版，直接加列）：

| 文件 | 位置 |
| --- | --- |
| `db_ddl.sql` | `batch_files` 表，`purpose` 之后 |
| `db_ddl_sqlite.sql` | `batch_files` 表，`purpose` 之后 |
| `database/db_ddl_upgrade_to_0.0.11.sql` | `batch_files` 表，`purpose` 之后 |

列定义：`filename VARCHAR(256) COMMENT '文件名（provider File 对象给出，可空）'`（sqlite：`filename TEXT`）。

### 2.2 DAO 与映射

| 文件 | 改动 |
| --- | --- |
| `storage/rdb/internal/dao/table_batch_files.go` | `TBatchFile` 加 `Filename string`；`TBatchFileParam` 加 `Filename *string` |
| `storage/rdb/batch/batch_file.go` | `batchFileDataToParam` / `batchFileParamToData` 映射 `filename` |

`batch_tasks` 无 DDL 变更（`endpoint` / `output_file_id` / `request_counts` / `usage_source` / `created_at` 列均已存在）。

---

## 3. 对账 job 变更（`model/ibatch/advance.go` + `job.go`）

### 3.1 职责②b：终态产物补漏

新增 `BackfillTerminalArtifacts(ctx)`，在 `RunOnce` 的 advance 之后、reconcile 之前执行（`job.go`）：

```
sync from redis → advance status → backfill terminal artifacts → reconcile settle
```

**候选**：`status='completed'` 且 `created_at >= now-24h`（复用 `BatchTaskFilter.CreatedStart`），每轮上限 `advanceLimit`；内存过滤 `OutputFileID == "" || RequestCounts == nil`。

**行为**（不复用 `advanceOneTask`，避免终态被 404 路径改写）：

| provider 结果 | 处理 |
| --- | --- |
| 2xx | 只 patch 缺失的 `output_file_id` / `request_counts` / `endpoint`；首次发现 `output_file_id` 时补登记输出 `batch_files` |
| 404 | 告警后跳过（**不改状态**：completed 不会因为 provider 侧过期被降级为 expired） |
| 非 2xx / 网络错误 / JSON 非法 | 告警后跳过，下轮重试 |

**为什么需要**：Redis 是主事件源，数据面修好后字段会随同步落库；但当 `BATCH_TASK` 键先到 TTL（48h）或历史键缺字段时，这是唯一不依赖数据面的补齐路径。

**代价（明确记录）**：provider 永久缺失的水平每轮会重复出网，成本上限是"每轮 `advanceLimit` 次 GET、24h 窗口内的缺失行"。告警日志带 `batch_id` 与缺失字段，便于运营发现永久缺失。带尝试次数的退避需要新增列，列入遗留。

### 3.2 `providerBatchResponse` 补 `endpoint`

复用既有结构补 `Endpoint string`，使非终态推进路径也能补齐 `endpoint`（作业一：数据面只在 create 时给一次）。

---

## 4. 兜底结算解析口径（`model/ibatch/settle.go`）

### 4.1 与数据面完全一致的双形态解析

`handleUsageLine` 改为：`response.body.usage` / `response.body.model` 优先，回退扁平 `response.usage` / `response.model`；`response` 为 `null`/缺省的错误行跳过。

### 4.2 安全阀：解析不到任何 usage 时不得判"0 元已结算"

`usageTotals` 增加 `noUsageLines`（有 `response` 对象但两处都取不到 usage）与 `badLines`（整行非法 JSON）。`settleOneTask` 在下载解析后：

```go
if usage.parsedLines == 0 && usage.noUsageLines > 0 {
    // 内容存在但一行 usage 都取不到：解析口径可疑，不推进 settled
    warn + counter; release claim; return false
}
```

- 只有错误行（`response:null` + `error`）或空文件时 `parsedLines == 0 && noUsageLines == 0` → **照常结算 0**（合法形态：全部请求失败）；
- 触发安全阀的批次下轮重试；永久不可解析的文件会持续占用结算候选，告警里带 `batch_id`，属可观测的已知代价。

**理由（issue 实测）**：修复前扁平解析使所有行落进"无 usage"，兜底结算因此算出 `settle_units=0` 并把 `settle_status` 置 `settled`——静默漏结算比不结算更糟。修复解析后仍需这道阀，防止以后任何口径漂移再次变成 0 元终态。

---

## 5. 兼容性与退化

| 场景 | 行为 |
| --- | --- |
| 数据面未升级（旧 `BATCH_TASK` 无新字段） | 视图字段未携带 → 不覆盖 DB；补齐由职责②b 出网完成 |
| 无 Redis 降级部署 | sync/backfill 仍可跑（候选来自 DB）；`redis == nil` 时 reconcile 直接返回（既有语义不变） |
| 存量 `batch_files` 行 | `filename` 为 NULL → 视图省略该键；`purpose` 按 `direction` 映射（历史行 `direction` 均有值） |
| 契约字段集 | 不变（§2.1/§2.2 字段集与语义均为已批准条款） |

---

## 6. 改动文件清单（Step 5 对照）

| 文件 | 改动 |
| --- | --- |
| `model/ibatch/redis_view.go` | 视图扩字段 + 解析 + 合并语义 |
| `model/ibatch/advance.go` | `providerBatchResponse.endpoint`；新增 `BackfillTerminalArtifacts` / `backfillOneTask` |
| `model/ibatch/job.go` | 插入职责②b（带日志与错误隔离） |
| `model/ibatch/settle.go` | 双形态行解析 + `noUsageLines`/`badLines` + 安全阀 |
| `model/ibatch/types.go` | `BatchFile.Filename`；`ToMeta` 的 `purpose` 映射与 `filename` |
| `db_ddl.sql` / `db_ddl_sqlite.sql` / `database/db_ddl_upgrade_to_0.0.11.sql` | `batch_files.filename` |
| `storage/rdb/internal/dao/table_batch_files.go` / `storage/rdb/batch/batch_file.go` | `filename` 映射 |
| 单测 `model/ibatch/*_test.go` | 见 §7 |
| 集成测试 `test/integration/tests/batch/...` + `schema/openapi/batch.go` | 见 §7 |

---

## 7. 测试设计（单测 + 集成）

### 7.1 单测（`model/ibatch`）

| 用例 | 断言要点 |
| --- | --- |
| `TestParseRedisTaskView`（扩） | `output_file_id`/`endpoint`/`request_counts`/`usage_source`/`created_at`（unix 与 RFC3339 两形态）；`request_counts` 非法 JSON → 未携带 |
| `TestMergeTaskForSync`（扩） | 未携带字段保留 DB 值；终态不回退仍成立；`created_at` 不被抹零 |
| `TestParseRedisFileView` / `TestMergeFileForSync`（扩） | `filename` 解析与"非空才刷新" |
| `TestBackfillTerminalArtifacts`（新） | completed 且缺产物 → 出网补齐 + 登记输出文件；404 → 不改状态；已有产物 → 不出网；已终态不回退 |
| `TestParseJSONLUsage`（扩） | `response.body.usage` 命中；扁平兼容；错误行不计；`noUsageLines`/`badLines` 计数 |
| `TestReconcileSettle_NoUsageNotSettled`（新） | 结果文件有 response 无 usage → 不调用 `UpdateBatchTaskSettleStatus`、释放 claim、下轮重试 |
| `TestBatchFileToMeta`（扩） | `purpose` 按 direction 映射、空 direction 透传、`filename` 透出 |

### 7.2 集成测试（`test/integration`,`ai-gateway-api-integration-test-design` 方法）

| 用例 | 断言要点 |
| --- | --- |
| `tests/batch/detail`（扩） | completed 批次详情九个字段齐备、`batch_files` 两条且 `purpose` 为 `batch_input`/`batch_output`、`filename` 存在；原始报文无内部字段（沿用 #249 的防泄漏断言） |
| `tests/batch/file`（扩） | §3.4 文件元数据 `purpose`/`filename` |
| `tests/schema/openapi/batch.go`（扩） | `BatchFileMetaSchema` 含 `filename`；元素键集合精确锁 |