# Issue #271：批量任务详情零值金额字段被省略——控制面修复说明

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/271](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/271)

> 批量任务进入终态后，`GET /open-api/v1/batches/{id}` 在 `reserve_units` / `settle_units` 为 0 时不返回这两个键。Redis
> `BATCH_TASK` 明确存有 `reserved_units=0` / `settle_units=0`，数据已写入，丢失发生在控制面 JSON 序列化。`settled`
> 与 `released` 两种终态均可复现；调用方无法区分"平台不支持该字段"与"本次金额为零"。

产品条款依据（Oracle）：`design-docs/api-define/OpenAPI接口定义/batches.md`

- §2.1 字段表（第 93-94 行）：`reserve_units` / `settle_units` 为 int64，合法性条件 `≥0`——`0` 是合法值；
- §3.2 详情示例（第 257 行）显式写 `"settle_units": 0`；同示例还显式写 `usage_input_tokens: 0`、`usage_output_tokens: 0`、`"terminal_at": ""`；
- §3.3 取消示例（第 333-347 行）显式写 `usage_input_tokens: 0`、`usage_output_tokens: 0`、`settle_units: 0`、`"output_file_id": ""`、`"terminal_at": ""`。

即条款要求是"字段必现、零值/空值显式"，不存在省略豁免。`omitempty` 只解释实际行为，不是 Expected 来源。

系统级验证用例：`test-cases/SC1401/sc1401_tc026_test.go:758` 的必现字段循环（`:768` 起）。

## 2. 根因（当前 HEAD 逐环核实，2026-10-23）

### 2.1 数据面写入正确：0 确实被写进 Redis

`bfe/bfe_modules/mod_ai_batch/mod_ai_batch_handlers.go:513-528`（`buildCreateTaskFields`）：

```go
reservedUnits := int64(0)
if ctx.reserved {
    reservedUnits = ctx.rmbUnits
}
fields := map[string]string{
    ...
    "reserved":       boolStr(ctx.reserved),
    "reserved_units": strconv.FormatInt(reservedUnits, 10),
}
```

`reserved_units` 是**无条件写入**的字段，预留失败时就是字符串 `"0"`；结算侧
`buildSettleTaskFields`（`:606-624`）在 `BatchSettleSettle` 分支写入 `settle_units`（0 亦写入）。
因此数据库/Redis 侧数据完好，与 issue 的 `HMGET BATCH_TASK:... reserved reserved_units settle_units` 回读
`0 0 0` 一致。

> 附带观察（非本 issue 缺陷）：`BatchSettleRelease` 分支（`:623-624`）只写 `settle_status=released`，
> 不写 `settle_units`；释放即零结算，控制面按 0 输出符合 §2.1，无需数据面改动。

### 2.2 控制面存储层原样映射 0

`storage/rdb/batch/batch_task.go:222-223`：

```go
ReserveUnits:      one.ReserveUnits,
SettleUnits:       one.SettleUnits,
```

目标类型 `int64`，0 值被完整保留，无丢失。`model/ibatch/redis_view.go` 的 `redisTaskView` 用
`*int64` 区分"hash 未携带"与零值（`:25-26` 注释），回源链路同样能拿到 0。

### 2.3 根因：响应直接序列化模型结构体，`omitempty` 吞掉零值

`endpoints/openapi_v1/batches/one.go:62` 直接 `return task`（`*ibatch.BatchTask`），
`cancel.go:55` 同样；`list.go:38-41` 的列表元素也是 `[]*ibatch.BatchTask`。
而 `model/ibatch/types.go` 上这些字段带 `omitempty`：

| 行号 | 字段 | 现有 tag |
|------|------|----------|
| 95 | `OutputFileID` | `json:"output_file_id,omitempty"` |
| 98 | `EstLines` | `json:"est_lines,omitempty"` |
| 99 | `UsageInputTokens` | `json:"usage_input_tokens,omitempty"` |
| 100 | `UsageOutputTokens` | `json:"usage_output_tokens,omitempty"` |
| 101 | `UsageSource` | `json:"usage_source,omitempty"` |
| 102 | `ReserveUnits` | `json:"reserve_units,omitempty"` |
| 103 | `SettleUnits` | `json:"settle_units,omitempty"` |
| 110 | `TerminalAt` | `json:"terminal_at,omitempty"` |

`encoding/json` 对带 `omitempty` 的 int64 零值直接跳过该键 → §2.1 要求"必现"的金额键在 0 值时消失。
**链路到此结束**：不存在同步延迟、读通路或权限问题（issue 已用同 run 的 `settle_status` / `over_reserved` /
`request_counts` 可读排除）。

### 2.4 同族偏差：同一响应里还有 4 个零值/空值字段被省略

按同一裁定规则（§2.1 `≥0` 或 §3.2/§3.3 示例显式给出 `0` / `""`）扫描，当前还会缺失：

- `usage_input_tokens` / `usage_output_tokens`（§3.2 第 253-254 行、§3.3 第 337-338 行示例为 0）；
- `est_lines`（§2.1 约束 `≥0`，行数未知时合法值为 0）；
- `output_file_id`（§3.3 示例 `""`，字段表注明"未完成时为空"）；
- `terminal_at`（§3.2/§3.3 示例 `""`，字段表注明"未终结为空"）。

这些与金额键是同一个根因、同一个响应、同一类消费方断言，若只修两个金额键，下一轮 SC1401 会以同样方式失败。

## 3. 契约裁定（逐字段）

裁定规则：**契约给出零值/空值示例，或给出 `≥0` 约束 → 字段必现；契约未定义空值语义的标识类字段 → 维持省略**。

| 字段 | 条款 | 零/空是否合法 | 结论 |
|------|------|---------------|------|
| `reserve_units` | §2.1 第 93 行 `≥0`；§3.2 示例 | 0 合法 | **必现**（本 issue 判定项） |
| `settle_units` | §2.1 第 94 行 `≥0`；§3.2/§3.3 示例为 0 | 0 合法 | **必现**（本 issue 判定项） |
| `usage_input_tokens` | §2.1 第 90 行 `≥0`；§3.2/§3.3 示例为 0 | 0 合法 | **必现**（同批收敛） |
| `usage_output_tokens` | §2.1 第 91 行 `≥0`；§3.2/§3.3 示例为 0 | 0 合法 | **必现**（同批收敛） |
| `est_lines` | §2.1 第 89 行 `≥0` | 0 合法 | **必现**（同批收敛） |
| `output_file_id` | §2.1 第 86 行"未完成时为空"；§3.3 第 333 行示例 `""` | `""` 合法 | **必现**（同批收敛） |
| `terminal_at` | §2.1 第 100 行"未终结为空"；§3.2/§3.3 第 263/347 行示例 `""` | `""` 合法 | **必现**（同批收敛） |
| `usage_source` | §2.1 第 92 行枚举 `download`/`reconcile`，未定义空值 | 空串不是合法枚举 | 维持省略；**契约缺口**待产品确认（见 §9） |
| `request_counts` | §2.1 object，未定义空对象 | 无 | 维持省略；契约缺口 |
| `entity_id` / `key_name` / `endpoint` / `input_file_id` | §2.1 标识类，未定义空值语义 | 无 | 维持省略 |
| `batch_files` | §3.1 列表示例不含；§3.2 详情 best-effort | 无 | 维持省略（列表不填充） |
| `batch_files[].filename` | §2.2 "provider File 对象提供，缺失时省略" | 省略即契约 | 维持省略；SC1401 `:1038` 的 filename 为空属数据面写入问题，另案 |

> 兼容性红线：本次**不增删任何键**，只改变"零值/空值键是否出现"。`id` / `key_name` / `idem_key` 这三个
> §2.1 未列出但对存量消费者可见的"契约超集"字段**保持原样输出**（#249 已登记为遗留，裁剪属独立小卡），
> 避免把一个"字段缺失"缺陷修成"字段消失"的破坏性变更。

## 4. 影响面

- **接口**：`GET /open-api/v1/batches`（列表）、`GET /open-api/v1/batches/{batch_id}`（详情）、
  `POST /open-api/v1/batches/{batch_id}/cancel`（取消）三个接口的 Data 形状。三处同源，必须一起改。
- **消费方**：强 schema 断言（SC1401-TC026）、对账/报表脚本、Web 详情页（`ai-gateway-web/src/modules/BatchTasks/`，
  用 `text()` 渲染，对新增键无感）。
- **数据面**：无改动（§2.1 已证明 0 已写入）。
- **DB/DDL**：无改动。
- **版本**：不涉及接口版本升级；§6 向后兼容——只是补出已批准条款要求的键。

## 5. 方案

### 5.1 方案 A（推荐）：endpoints 层显式响应投影

新增 `endpoints/openapi_v1/batches/view.go`，定义对外视图并集中映射：

```go
// batchTaskView 是 batches.md §2.1 的对外形状：int64 零值与空串必须显式
// 出现，因此不复用 ibatch.BatchTask 的 omitempty 规则。
type batchTaskView struct {
    ID                int64                  `json:"id"`
    BatchID           string                 `json:"batch_id"`
    APIKeyID          string                 `json:"api_key_id"`
    ProductName       string                 `json:"product_name"`
    EntityID          string                 `json:"entity_id,omitempty"`
    Provider          string                 `json:"provider"`
    KeyName           string                 `json:"key_name,omitempty"`
    Endpoint          string                 `json:"endpoint,omitempty"`
    InputFileID       string                 `json:"input_file_id,omitempty"`
    OutputFileID      string                 `json:"output_file_id"`   // "" 必现
    Status            string                 `json:"status"`
    RequestCounts     map[string]int64       `json:"request_counts,omitempty"`
    EstLines          int64                  `json:"est_lines"`        // 0 必现
    UsageInputTokens  int64                  `json:"usage_input_tokens"`
    UsageOutputTokens int64                  `json:"usage_output_tokens"`
    UsageSource       string                 `json:"usage_source,omitempty"`
    ReserveUnits      int64                  `json:"reserve_units"`
    SettleUnits       int64                  `json:"settle_units"`
    SettleStatus      string                 `json:"settle_status"`
    OverReserved      bool                   `json:"over_reserved"`
    SyncSource        string                 `json:"sync_source"`
    IdemKey           string                 `json:"idem_key"`
    CreatedAt         time.Time              `json:"created_at"`
    UpdatedAt         time.Time              `json:"updated_at"`
    TerminalAt        string                 `json:"terminal_at"`      // RFC3339 或 ""
    BatchFiles        []*ibatch.BatchFileMeta `json:"batch_files,omitempty"`
}
```

映射规则：

1. `TerminalAt`：`nil` → `""`；非 nil → `RFC3339`（与 `created_at` / `updated_at` 同格式）。**不输出 `null`**，
   严格对齐 §3.2/§3.3 示例。
2. 其余字段一一平移，字段集合与当前响应**完全一致**（含 `id` / `key_name` / `idem_key` 超集字段）。
3. `batch_files` 仅详情填充；取消接口返回的任务不带关联文件，自然省略（§3.3 示例亦无该键）；列表不填充（§3.1 示例无此键）。

接入点：

- `one.go:62`：`return newBatchTaskView(task), nil`；
- `cancel.go:55`：同上；
- `list.go:38-41`：`List []*batchTaskView`，遍历 `result.Tasks` 转换（`nil` 仍归一为 `[]`）。

为什么选它而不是直接改 `types.go` 的 tag：

- `terminal_at` 契约是空串，`*time.Time` 无论是否 `omitempty` 都只能给 `null` 或省略，tag 改法到不了契约；
- `ibatch.BatchTask` 同时是内部读模型（Redis 回源合并、对账 job、DAO 映射），对外 `omitempty` 与对内"未携带 = 未知"
  的语义不该混在一个结构体上；
- 投影把"对外字段集"显式化：后续裁剪 `id` / `key_name` / `idem_key` 超集（#249 遗留）只需删视图字段，不动内部模型。

### 5.2 方案 B（备选，最小改动）

只改 `model/ibatch/types.go`：去掉 `est_lines` / `usage_*` / `reserve_units` / `settle_units` / `output_file_id`
的 `omitempty`；`terminal_at` 保持省略（或改 `null`）。代价：`terminal_at` 仍偏离 §3.2/§3.3 示例，需先改条款（§9）；
且对外形状继续由内部模型 tag 隐式决定，未来新增内部字段会再次误入 OpenAPI。

**推荐 A**：一次把同一响应的 7 个零值/空值字段收敛干净，并给后续契约演进留出显式出口。

## 6. 行为变化表

| 场景 | 现状 | 修复后 |
|------|------|--------|
| `settled` 且金额 0 | 无 `reserve_units` / `settle_units` 键 | `"reserve_units": 0`、`"settle_units": 0` |
| `released`（全额释放） | 同上，无键 | 同上，显式 0 |
| 未结算 / 无 usage | 无 `usage_input_tokens` / `usage_output_tokens` 键 | 显式 0 |
| `est_lines` 未知 | 无键 | `"est_lines": 0` |
| 未完成 / 无输出文件 | 无 `output_file_id` 键 | `"output_file_id": ""` |
| 未终结 | 无 `terminal_at` 键 | `"terminal_at": ""` |
| 其余字段（含 `id` / `key_name` / `idem_key`、`usage_source` 空时省略） | — | 不变 |

## 7. 测试

### 7.1 单测（`endpoints/openapi_v1/batches`）

1. 新增 `view_test.go`：表驱动覆盖 `reserved`（未结算）、`settled`（金额 0）、`released`（全额释放）三种状态下零值批次，
   `json.Marshal(newBatchTaskView(task))` 后按字符串精确断言：
   `"reserve_units":0`、`"settle_units":0`、`"est_lines":0`、`"usage_input_tokens":0`、
   `"usage_output_tokens":0`、`"output_file_id":""`、`"terminal_at":""`；
2. 断言未终结时响应正文不含 `"terminal_at":null`；
3. 键集合锁定：断言序列化结果包含 `id` / `key_name` / `idem_key`（防"修缺字段变成丢字段"），
   且不含任何未声明字段；
4. 更新 `batches_test.go` 中 `data.(*ibatch.BatchTask)` 的类型断言（`:251`、`:279`、`:306` 及列表用例）为视图类型。

```bash
go build ./... && go vet ./...
go test ./endpoints/openapi_v1/batches/... ./model/ibatch/...
```

### 7.2 集成测试（`test/integration/tests/batch/`，新增 4 条用例）

框架沿用 batch 模块现状：真实二进制子进程 + `testutil.StartServerAuto`（默认 SQLite，设
`AIAPI_MYSQL_DSN` 后同一批用例跑 MySQL），种子经 `tests/batch/helper` 直写 `batch_tasks`。

零值场景无需新增种子函数：`helper.InsertBatchTask` 现有 INSERT 恰好构造零值任务
（`est_lines` / `usage_in` / `usage_out` = 0、`settle_units` = 0、`usage_source=''`、
`terminal_at=NULL`），与 issue 复现数据的形状一致。仅 BT-2-007 需要给
`helper.BatchTaskRow` 增加 `SettleUnits int64` 与 `TerminalAt *time.Time` 两个可选字段
（零值默认与现状行为一致，已有用例不受影响）。

| 编号 | 文件 | 场景 | 断言要点 |
|------|------|------|----------|
| BT-2-006 | `detail/detail_test.go` | 零值终态任务（`settled` 与 `released` 各直写一行） | map 存在性 + 原始报文 token 双锁：`reserve_units` / `settle_units` / `est_lines` / `usage_input_tokens` / `usage_output_tokens` 键存在且为 0（整数 token 精确匹配，沿用 BT-2-002 的 `resp.RawBody` 断言法，防止解码容忍造成假通过）；`output_file_id` / `terminal_at` 键存在且为 `""`；报文不含 `"terminal_at":null`；`usage_source` 键缺席（契约缺口，锁现行为，防未来擅自改成输出空串） |
| BT-2-007 | `detail/detail_test.go` | 非零回归：`settled` 且金额非零、`terminal_at` 非空 | `settle_units=180000000`（整数 token，不 ÷1e8）、`terminal_at` 为 RFC3339 字符串而非 `""`——防"无条件输出 0/空串"的修复回归 |
| BT-1-005 | `list/list_test.go` | 列表元素零值键必现 | `Data.list[0]` 与详情同规则七键必现——防"只修详情、列表残留"（`one.go` 与 `list.go` 必须同批改） |
| BT-3-008 | `cancel/cancel_test.go` | 取消响应零值键必现 | 复用 BT-3-006 的 httptest 假 provider 数据准备，种子 `reserve_units=0` 非终态行；200 响应 Data 七键必现——防"只修详情、取消残留" |

编号遵循 `tests/batch/design.md` §6（1=列表、2=详情、3=取消，场景编号追加）；落地时同步在
design.md §4.1-§4.3 用例表登记上述四行及断言要点。

**判据属性**：BT-2-006 / BT-1-005 / BT-3-008 在当前 HEAD（`56249a0`）上**必须失败**（复现 issue），
按 §5.1 修复后通过——这是本 issue 的本地回归闸门，与系统级 SC1401-TC026 形成双锁；
BT-2-007 是防"无条件输出 0/空串"的回归闸，修复前后都应通过。

```bash
bash test/scripts/run_module_tests.sh batch                        # SQLite（默认后端）
AIAPI_MYSQL_DSN=<admin-dsn> bash test/scripts/run_module_tests.sh batch   # MySQL 双后端
```

### 7.3 系统级判据（issue 给定）

`GET /open-api/v1/batches/{batch_id}` 的**原始 JSON 键集合**含两个金额键，值为 0；
SC1401-TC026 `:768` 必现字段循环通过；`settled` 与 `released` 各一次。

整体回归：

```bash
cd ai-gateway-api
make test
```

## 8. 文档与契约同步

- `design-docs/api-define/OpenAPI接口定义/batches.md` §2.1：本次**不改条款**（条款已是 Oracle，缺陷是运行行为偏离）。
  建议追加一条总则，把"零值显式"写成文字而非只能从示例推断：
  > 本节字段除标注"为空/缺失时省略"外均必现；int64 字段的 `0`、字符串字段的 `""` 必须显式输出，
  > 不得用省略键表达"未知/为零"。
- `design-docs/sys-design/details/批量任务与对账.md`：在对外形状处补一句"OpenAPI 响应由
  `endpoints/openapi_v1/batches/view.go` 投影，零值/空值显式输出，内部模型 tag 不决定对外契约"。

## 9. 实施状态与遗留

**实施状态：已于 2026-10-23 按 §5.1 落地**（六步法 Step 5，记录见 §10）。修复前基线
`56249a0` 上 `model/ibatch/types.go:98-103` 带 `omitempty`，缺陷可稳定复现（§10.3
用旧行为二进制做了复现实验）。

遗留 / 待产品确认：

1. `usage_source` 空值语义：枚举只有 `download` / `reconcile`，任务未结算时 DB 为空——省略键与"必现"都各有违反面，
   需在 §2.1 明确定义（建议：允许省略，或补 `""` 语义并声明为"未就绪"，由 `settle_status` 判定）。
2. `request_counts` 空对象语义：未拿到 provider 计数时无合法空值表示。
3. `id` / `key_name` / `idem_key` 契约超集：本次保持输出，裁剪另起小卡（沿用 #249 结论）。
4. `batch_files[].filename` 为空（SC1401 `:1038`）：数据面 `BATCH_FILE` 未写 filename 的另一字段问题，不属本 issue。
5. 若产品最终裁定"允许省略 0"（issue 中提到的替代路径），则需改 `batches.md` 示例并过 `DESIGN_GATE`，代码不动；
   本次按现有条款实现。

## 10. 实施记录（2026-10-23，六步法 Step 5）

代码、单测、集成测试与文档已按 §5.1 落地。

### 10.1 改动文件

| 文件 | 动作 | 说明 |
|------|------|------|
| `endpoints/openapi_v1/batches/view.go` | 新增 | `batchTaskView` + `newBatchTaskView` / `newBatchTaskViews`（terminal_at 空串或 RFC3339） |
| `endpoints/openapi_v1/batches/view_test.go` | 新增 | 零值显式 / 非零回归 / 键集合 / nil 归一四组用例 |
| `endpoints/openapi_v1/batches/one.go` | 修改 | 返回 `newBatchTaskView(task)`；注释同步 |
| `endpoints/openapi_v1/batches/cancel.go` | 修改 | 返回 `newBatchTaskView(task)` |
| `endpoints/openapi_v1/batches/list.go` | 修改 | `List []*batchTaskView`；nil 归一移入 `newBatchTaskViews` |
| `endpoints/openapi_v1/batches/batches_test.go` | 修改 | 三处 `data.(*ibatch.BatchTask)` → `*batchTaskView` |
| `test/integration/tests/batch/detail/detail_test.go` | 修改 | +BT-2-006 / BT-2-007 |
| `test/integration/tests/batch/list/list_test.go` | 修改 | +BT-1-005 |
| `test/integration/tests/batch/cancel/cancel_test.go` | 修改 | +BT-3-008 |
| `test/integration/tests/batch/helper/helper.go` | 修改 | `BatchTaskRow` + `SettleUnits` / `TerminalAt *time.Time` |
| `test/integration/tests/batch/design.md` | 修改 | §3 目录计数、§4.1-§4.3 用例行、§4.2 断言要点 |
| `test/integration/tests/schema/openapi/batch.go` | 修改 | `BatchTaskDetailSchema` 七键 Optional → Required（偏差见 §10.2） |
| `design-docs/api-define/OpenAPI接口定义/batches.md` | 修改 | §2.1 补"字段必现规则"总则（Step 3） |
| `design-docs/sys-design/details/批量任务与对账.md` | 修改 | §3.1 补对外形状投影注记（Step 4） |
| `model/ibatch/types.go` / `storage/rdb/batch/batch_task.go` | 不改 | 内部模型 `omitempty` 语义保留 |

### 10.2 与方案的偏差（实施中确认）

1. **`tests/schema/openapi/batch.go` 未列入原方案清单，实施中补入**：batch 详情 schema 原把
   `est_lines` / `usage_*` / `reserve_units` / `settle_units` / `output_file_id` / `terminal_at`
   七键列为 Optional（omitempty 语义），与 §2.1 必现规则冲突——按契约收紧为 Required。
   种子任务为零值形态，修复后七键恒输出，schema 模块双用例（含 / 不含关联文件）均通过。
2. `list.go` 的 `nil` 归一从 handler 移入 `newBatchTaskViews`（方案 §2.3 已预期，行为不变）。
3. `batches.md` §2.1 总则与 details 文档注记由"建议"落地为实际修改（Step 3 / Step 4）。
4. 其余按方案执行，无偏离。

### 10.3 验证结果

| 验证项 | 结果 |
|--------|------|
| `go build ./... && go vet ./...`（主模块） | 通过 |
| `go test ./...`（主模块全量单测） | 通过（EXIT 0） |
| `go test ./tests/batch/...`（SQLite 集成，含 BT-2-006/007、BT-1-005、BT-3-008） | 全部 ok（EXIT 0） |
| `go test ./tests/schema/...`（含收紧后的 `BatchTaskDetailSchema`） | innerapi / openapi / report 全部 ok |
| **复现实验**：回退端点改动、以旧行为二进制跑 `-run ZeroValuesExplicit` | BT-2-006 / BT-1-005 / BT-3-008 全部 FAIL（复现 issue），修复后全部通过——本地回归闸门成立 |

注：两个集成模块需串行运行——并行启动时多个测试进程共享
`test/integration/data/*.log`，Windows 下日志轮转会互锁（`server not ready`），
与本次改动无关。

### 10.4 六步法核对

- [x] modifications 目录已创建（Step 1）
- [x] 变更说明已编写（Step 2，本文档 + design-changes.md）
- [x] api-define 已更新并 review（Step 3，§2.1 总则）
- [x] sys-design 已更新并 review（Step 4，details/批量任务与对账.md §3.1 注记）
- [x] 代码按设计实现并通过测试（Step 5，§10.3）
- [x] 已评估 details 沉淀（Step 6）：经验已落入 details 注记与 batch design.md 断言要点，
      契约零值断言模式（map `ok` 存在性 + RawBody token 双锁）在 BT-2-006 注释与 design.md
      固化，无需新建 details 文档