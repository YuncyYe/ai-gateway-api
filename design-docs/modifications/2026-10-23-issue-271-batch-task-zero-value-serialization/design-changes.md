# Issue #271 控制面：批量任务对外响应形状变更（零值/空值显式）

## 1. 设计目标

把 `GET /batches`、`GET /batches/{batch_id}`、`POST /batches/{batch_id}/cancel` 三个接口的 Data 形状
从"由内部模型 tag 隐式决定"改为"由 endpoints 层显式视图决定"，满足 `batches.md` §2.1/§3.2/§3.3 的
"字段必现、零值/空值显式"要求。

**变更边界**：不增删任何键，不引入新字段，不涉及存储、DDL、数据面。

## 2. 对外视图（新增 `endpoints/openapi_v1/batches/view.go`）

### 2.1 结构体

```go
type batchTaskView struct {
    ID                int64                   `json:"id"`            // 契约超集，暂留（#249 遗留）
    BatchID           string                  `json:"batch_id"`
    APIKeyID          string                  `json:"api_key_id"`
    ProductName       string                  `json:"product_name"`
    EntityID          string                  `json:"entity_id,omitempty"`
    Provider          string                  `json:"provider"`
    KeyName           string                  `json:"key_name,omitempty"` // 契约超集，暂留
    Endpoint          string                  `json:"endpoint,omitempty"`
    InputFileID       string                  `json:"input_file_id,omitempty"`
    OutputFileID      string                  `json:"output_file_id"`   // 空串必现
    Status            string                  `json:"status"`
    RequestCounts     map[string]int64        `json:"request_counts,omitempty"`
    EstLines          int64                   `json:"est_lines"`        // 0 必现
    UsageInputTokens  int64                   `json:"usage_input_tokens"`
    UsageOutputTokens int64                   `json:"usage_output_tokens"`
    UsageSource       string                  `json:"usage_source,omitempty"`
    ReserveUnits      int64                   `json:"reserve_units"`    // 0 必现
    SettleUnits       int64                   `json:"settle_units"`     // 0 必现
    SettleStatus      string                  `json:"settle_status"`
    OverReserved      bool                    `json:"over_reserved"`
    SyncSource        string                  `json:"sync_source"`
    IdemKey           string                  `json:"idem_key"`         // 契约超集，暂留
    CreatedAt         time.Time               `json:"created_at"`
    UpdatedAt         time.Time               `json:"updated_at"`
    TerminalAt        string                  `json:"terminal_at"`      // RFC3339 或 ""
    BatchFiles        []*ibatch.BatchFileMeta `json:"batch_files,omitempty"`
}
```

### 2.2 映射规则

| 契约字段 | 视图来源（`*ibatch.BatchTask`） | 序列化规则 |
|----------|--------------------------------|-----------|
| `output_file_id` | `OutputFileID` | 空串照常输出 |
| `est_lines` | `EstLines` | 0 照常输出 |
| `usage_input_tokens` | `UsageInputTokens` | 0 照常输出 |
| `usage_output_tokens` | `UsageOutputTokens` | 0 照常输出 |
| `reserve_units` | `ReserveUnits` | 0 照常输出 |
| `settle_units` | `SettleUnits` | 0 照常输出 |
| `terminal_at` | `TerminalAt *time.Time` | `nil` → `""`；非 nil → `Format(time.RFC3339)` |
| `usage_source` | `UsageSource` | 维持 `omitempty`（契约未定义空值，见 change-summary §9） |
| `request_counts` | `RequestCounts` | 维持 `omitempty`（无合法空对象） |
| `entity_id` / `key_name` / `endpoint` / `input_file_id` | 同名 | 维持 `omitempty`（标识类，契约未定义空值语义） |
| `batch_files` | `BatchFiles` | `omitempty`；仅详情填充（§3.2），列表不填充（§3.1 示例无该键），取消返回不做关联装配 |
| 其余 | 同名 | 原样平移，保持现状 |

`terminal_at` 用 `string` 而不是 `*time.Time`：§3.2 第 263 行、§3.3 第 347 行的示例都是 `""`，
Go 的时间指针在 JSON 里只能给出 `null` 或省略，两种都不符合条款。格式化统一用 `RFC3339`，
与 `created_at` / `updated_at` 的 `time.Time` 默认编码一致（`time.Time` 默认即 `RFC3339Nano`，
秒级精度下无差异；若担心纳秒差，可直接 `t.Format(time.RFC3339)` 显式固定）。

### 2.3 构造入口

```go
func newBatchTaskView(task *ibatch.BatchTask) *batchTaskView   // nil 安全，返回 nil
func newBatchTaskViews(tasks []*ibatch.BatchTask) []*batchTaskView // 空入参 → 非 nil 空切片
```

`newBatchTaskViews` 必须把 `nil` 归一为 `[]*batchTaskView{}`，保持 `list.go:85-88` 现有的
"空列表输出 `[]` 而不是 `null`" 行为。

## 3. 为什么不在 `model/ibatch/types.go` 上改 tag

`ibatch.BatchTask` 同时承担三个角色，混在一处会互相牵制：

1. **对外的 OpenAPI 响应体**（当前实现）；
2. **Redis 回源 / 合并的读模型**：`redisTaskView` 用指针字段区分"hash 未携带"与零值
   （`redis_view.go:25-26`），`mergeTaskForSync` / `mergeFileForSync` 依赖"未携带"语义；
3. **DAO 映射目标**：`storage/rdb/batch/batch_task.go:203-240`。

去掉 `omitempty` 只解决第 1 个角色的部分字段，且 `terminal_at` 无论如何都达不到 `""`；
若未来在内部模型上新增字段，仍会自动泄漏进 OpenAPI（#249 观察到的 `id` / `key_name` / `idem_key`
超集就是这样产生的）。视图层显式声明对外字段集后，内部模型可自由演进。

已评估并否决的备选：

- **给 `BatchTask` 加自定义 `MarshalJSON`**：影响所有 `json.Marshal` 调用点（含 `types_test.go` 的形状断言），
  隐式魔法、调试成本高；
- **把 `TerminalAt` 改为 `string`**：牵连 `settle.go` / `sync.go` / `advance.go` 的时间计算与 DAO 映射，
  为一个序列化问题付出模型语义代价，不划算。

## 4. 三个接口的接入与差异

| 接口 | 接入方式 | 差异 |
|------|----------|------|
| 详情 `one.go:62` | `return newBatchTaskView(task), nil` | 填充 `batch_files`（§3.2） |
| 取消 `cancel.go:55` | 同上 | 契约 §3.3 示例不含 `batch_files`；沿用现有代码——`Cancel` 返回的任务不带关联文件，故自然省略 |
| 列表 `list.go:38-41` | `List []*batchTaskView`，`newBatchTaskViews(result.Tasks)` | 不填 `batch_files`（§3.1 示例） |

一致性要求：三个接口必须共用同一视图类型，禁止其中一个继续返回 `*ibatch.BatchTask`
（否则零值缺失会在列表或取消接口残留）。

## 5. 兼容性与退化

- **键集合**：只增不减（`id` / `key_name` / `idem_key` 保持不变），存量消费者解析不受影响；
- **空值语义**：`terminal_at` 从"键缺失"变为 `""`。对严格 schema 消费方是修复；对把 `undefined` 判为
  "未终结"的前端，`""` 同样是假值，`ai-gateway-web` 的 `text()` 渲染无差异；
- **数值类型**：int64 仍以 JSON 数字输出（1e-8 定点整数，不换算），不引入字符串化；
- **回源窗口任务**（`sync_source=redis`）：结算字段可能尚未就绪，此时输出 `0` 是契约允许的
  "本次为零 / 未就绪"，就绪与否由 `settle_status` 判定——这正是 issue 要求的可区分性；
- **不涉及**：DB 列、Redis 键结构、数据面导出、接口版本。

## 6. 测试设计

### 6.1 单测（`endpoints/openapi_v1/batches`）

新增 `view_test.go`：

1. 表驱动三态：`reserved`（未结算）、`settled`（金额 0）、`released`（全额释放），
   `json.Marshal(newBatchTaskView(task))` 后按字符串精确断言：
   `"reserve_units":0`、`"settle_units":0`、`"est_lines":0`、`"usage_input_tokens":0`、
   `"usage_output_tokens":0`、`"output_file_id":""`、`"terminal_at":""`；
2. 非零回归：`reserve_units=250000000`、`settle_units=180000000`、`terminal_at` 非零值 → 输出 RFC3339 字符串；
3. 键集合锁定：断言序列化结果包含 `id` / `key_name` / `idem_key`（防"修缺字段变成丢字段"），
   且不含任何未声明字段；
4. `newBatchTaskViews(nil)` 返回非 nil 空切片，`json.Marshal` 后为 `[]`；
5. 未终结任务序列化结果不含 `"terminal_at":null`。

### 6.2 已有用例调整

`batches_test.go` 中 `data.(*ibatch.BatchTask)` 的类型断言（`:251`、`:279`、`:306`）与列表用例的
`list[0].BatchID` 访问改为视图类型；`model/ibatch/types_test.go` 不动（内部模型未变）。

### 6.3 集成测试（`test/integration/tests/batch/`，新增 4 条用例）

真实二进制子进程 + SQLite / MySQL 双后端（`testutil.StartServerAuto`），种子直写
`batch_tasks`。选择直写而非走数据面，是因为本缺陷在控制面序列化层——数据面链路（BFE）与
issue 复现无关，集成环境也没有 BFE。

零值种子现成：`helper.InsertBatchTask` 的 INSERT 固定写 `est_lines/usage_in/usage_out=0`、
`settle_units=0`、`usage_source=''`、`terminal_at=NULL`，与 issue 复现行（`reserved_units=0`、
`settle_units=0`）同形，零值用例无需新增种子函数。仅需给 `helper.BatchTaskRow` 增加
`SettleUnits int64` 与 `TerminalAt *time.Time` 两个可选字段（默认零值 = 现状行为，
BT-1-001~004 / BT-2-000~005 / BT-3-001~007 不受影响），供 BT-2-007 的非零种子使用。

| 编号 | 文件 | 场景 | 断言要点 |
|------|------|------|----------|
| BT-2-006 | `detail/detail_test.go` | 零值终态任务（`settled` / `released` 各一行） | map 存在性 + `resp.RawBody` token 双锁：五个 int64 键存在且为整数 0（沿用 BT-2-002 的整数 token 断言法）；`output_file_id` / `terminal_at` 存在且为 `""`；不含 `"terminal_at":null`；`usage_source` 键缺席（锁契约缺口的现行为） |
| BT-2-007 | `detail/detail_test.go` | 非零回归（`settled`、金额非零、`terminal_at` 非空） | `settle_units` 为整数 token 180000000、`terminal_at` 为 RFC3339 字符串——防"无条件输出 0/空串"回归 |
| BT-1-005 | `list/list_test.go` | 列表元素零值键必现 | `Data.list[0]` 七键同详情规则——三接口同源改造的守门 |
| BT-3-008 | `cancel/cancel_test.go` | 取消响应零值键必现 | 复用 BT-3-006 的 httptest 假 provider；200 Data 七键必现 |

断言方法约束（沿用 batch 模块"以合同为唯一断言依据"的反面模式 #5 教训）：

- **键存在性必须走 map 的 `v, ok := data[k]` + `require.True(ok)`，不允许只断言值**——
  结构体解码会给缺席键填零值，"值正确"断言无法发现键缺席（本 issue 的缺陷形态正是键缺席）；
- 整数字段必须对 `resp.RawBody` 做 token 精确匹配（防浮点/字符串化回归）；
- BT-2-006 / BT-1-005 / BT-3-008 在当前 HEAD 上必须失败（复现 issue）、修复后通过，
  作为本地回归闸门；BT-2-007 为防"无条件输出 0/空串"的回归闸，修复前后都应通过；
- 用例登记同步进 `tests/batch/design.md` §4.1-§4.3 表格与断言要点。

运行：`bash test/scripts/run_module_tests.sh batch`（SQLite）；设 `AIAPI_MYSQL_DSN` 后同命令跑 MySQL。

### 6.4 系统级判据

沿用 issue 给定口径：读取**原始 JSON 键集合**（不经结构体默认值），断言 §2.1 必现字段存在且
`settle_units` / `reserve_units` 为 0；`settled` 与 `released` 各一次；SC1401-TC026 `:768`
必现字段循环应整体通过。

## 7. 改动文件清单（Step 5 对照）

| 文件 | 动作 | 说明 |
|------|------|------|
| `endpoints/openapi_v1/batches/view.go` | 新增 | `batchTaskView` + `newBatchTaskView` / `newBatchTaskViews` |
| `endpoints/openapi_v1/batches/view_test.go` | 新增 | §6.1 用例 |
| `endpoints/openapi_v1/batches/one.go` | 修改 | 返回视图；`batch_files` 装配逻辑不变 |
| `endpoints/openapi_v1/batches/cancel.go` | 修改 | 返回视图 |
| `endpoints/openapi_v1/batches/list.go` | 修改 | `List` 改 `[]*batchTaskView` |
| `endpoints/openapi_v1/batches/batches_test.go` | 修改 | 类型断言对齐 |
| `test/integration/tests/batch/detail/detail_test.go` | 修改 | +BT-2-006 / BT-2-007（§6.3） |
| `test/integration/tests/batch/list/list_test.go` | 修改 | +BT-1-005 |
| `test/integration/tests/batch/cancel/cancel_test.go` | 修改 | +BT-3-008 |
| `test/integration/tests/batch/helper/helper.go` | 修改 | `BatchTaskRow` 增加 `SettleUnits` / `TerminalAt *time.Time` |
| `test/integration/tests/batch/design.md` | 修改 | §4.1-§4.3 登记新用例行 |
| `test/integration/tests/schema/openapi/batch.go` | 修改 | `BatchTaskDetailSchema` 七键 Optional → Required（实施中补入，见 §9） |
| `model/ibatch/types.go` | 不改 | 保留内部 `omitempty` 语义 |
| `storage/rdb/batch/batch_task.go` | 不改 | — |
| `design-docs/api-define/OpenAPI接口定义/batches.md` | 建议补总则 | 见 change-summary §8 |
| `design-docs/sys-design/details/批量任务与对账.md` | 建议补一句 | 对外形状由 endpoints 投影决定 |

## 8. 与 #254 的关系

#254（`2026-10-19-issue-254-batch-task-state-and-settle-fix`）解决的是"字段**有值但读不出/补不齐**"；
本 issue 解决的是"字段**值为零/空时被 `omitempty` 吞掉**"。两者互补：即使 #254 把
`reserve_units` / `settle_units` 正确写入 DB 与 Redis，零值场景仍会因序列化规则再次缺失，
因此本修复是 #254 回归用例（SC1401-TC026 金额断言）能稳定通过的必要条件。

## 9. 实施记录（2026-10-23）

已按本文档落地，与方案的偏差及全部验证结果见 change-summary.md §10。要点：

1. 视图与三接口接入与 §2/§4 一致；`list.go` 的 nil 归一移入 `newBatchTaskViews`（§2.3 已预期）；
2. 实施中补入 `tests/schema/openapi/batch.go`：`BatchTaskDetailSchema` 的七个零值/空值键由
   Optional 收紧为 Required——schema 是契约的机器可读形式，应随 §2.1"字段必现规则"同步；
3. 复现实验确认回归闸门成立：旧行为二进制上 BT-2-006 / BT-1-005 / BT-3-008 全部失败，
   修复后主模块单测、batch 集成模块、schema 模块全部通过。