# Issue #249：批量任务详情返回结构与 batches.md §3.2 契约不符——修复方案

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/249](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/249)

> 批量任务详情接口返回 Data = {task: {...}, files: [...]}，而 batches.md 规定 Data 就是 BatchTask 对象本身（内含 batch_files）。控制面 UI 与 web 侧变更记录均按文档实现，导致点开任意批量任务详情都看不到任何数据（5 个面板字段全部渲染 -，批量文件表格不渲染）。
>
> 复现：批量任务列表 → 任意行点「详情」。

产品条款依据（Oracle）：`design-docs/api-define/OpenAPI接口定义/batches.md` §3.2「返回数据（Data内容）：**字段同 §2.1（含 `batch_files`）**」，成功示例中 `Data` 顶层即任务字段（`batch_id` / `status` / … / `batch_files` 数组）。

## 2. 根因（已对当前检出逐环直读核实，2026-10-16）

`GET /open-api/v1/batches/{batch_id}` 的实现未按文档契约组装响应，共三处偏差：

1. **多包一层**：`endpoints/openapi_v1/batches/one.go:27-30` 定义私有包装结构
   `batchTaskDetailResponse{Task *ibatch.BatchTask; Files []*ibatch.BatchFile}`，
   `Data` 顶层是 `task` / `files` 两个键，与文档"Data 即任务对象本身"不符；
2. **任务字段缺 `batch_files`**：`model/ibatch/types.go:85-111` 的 `BatchTask`
   没有 `batch_files` 字段，详情响应无法携带关联文件元数据；
3. **文件形状不符**：`files` 直接序列化内部实体 `ibatch.BatchFile`
   （`types.go:114-129`），外泄内部字段（`id` / `direction` / `key_name` /
   `api_key_id` / `product_name` / `lines` / `first_seen_at` / `last_seen_at` /
   `updated_at`），且字段形状与文档 §2.2（`file_id` / `provider` / `purpose` /
   `filename` / `bytes` / `created_at`）不一致。

另有一处文档与 as-built 的既有偏差（非本卡引入）：`batch_files` 表
（`db_ddl.sql:732-750`）**无 `filename` 列**，文件元数据一期不落库文件名。

## 3. 影响面

| 项目 | 结论 |
|------|------|
| 接口范围 | 仅 `GET /open-api/v1/batches/{batch_id}`（详情）；列表 §3.1、取消 §3.3、文件查询 §3.4 不在本卡范围 |
| 调用方 | ai-gateway-web 批量任务详情页（已按 batches.md 实现）——修复服务端后前端**零改动**恢复 |
| 数据/配置 | 无 DDL 变更、无配置变更、无数据迁移 |
| 数据面 | 无 BFE / conf-agent 联动 |

观察记录（不在本期裁剪范围）：列表与取消接口同样直接序列化 `BatchTask`，
响应中含文档 §2.1 未列出的内部字段（`id` / `key_name` / `idem_key`）；
详情修复后这些字段仍在。前端按文档取字段、对多余键无感，属"契约超集"，记入遗留。

## 4. 目标

详情接口 `Data` 顶层 = §2.1 任务字段 + `batch_files` 数组，与
batches.md §3.2 成功示例逐字段一致：

```json
{"ErrNum":200,"ErrMsg":"success","Data":{
  "batch_id":"batch-seed-01","api_key_id":"api-key-6","status":"queued","...":"...",
  "terminal_at":"",
  "batch_files":[{"file_id":"file-seed-in-01","provider":"deepseek","bytes":4224,"..."}]}}
```

## 5. 最终方案

文档是已批准的对外契约且 UI 已按其实现，故**修服务端向契约收敛**，
不改 batches.md §3.2 的返回约定。

### 5.1 `model/ibatch/types.go`：新增文件元数据对外视图 + 任务附加字段

- 新增 `BatchFileMeta`（文档 §2.2 形状）：

  ```go
  // BatchFileMeta 批量文件元数据对外视图（batches.md §2.2）。
  type BatchFileMeta struct {
      FileID    string    `json:"file_id"`
      Provider  string    `json:"provider"`
      Purpose   string    `json:"purpose,omitempty"`
      Filename  string    `json:"filename,omitempty"`
      Bytes     int64     `json:"bytes,omitempty"`
      CreatedAt time.Time `json:"created_at"`
  }
  ```

  附转换函数 `BatchFile.ToMeta()`（或包级 `NewBatchFileMeta`），只映射对外
  字段，内部字段（`id` / `key_name` / `api_key_id` / `product_name` /
  `direction` / `lines` / `first_seen_at` / `last_seen_at` / `updated_at`）不外泄。
- `BatchTask` 增加**非持久化**视图字段：

  ```go
  BatchFiles []*BatchFileMeta `json:"batch_files,omitempty"`
  ```

  DAO / 存储层不读写该字段，仅详情 handler 组装，与 §3.1 列表行为
  （示例不含 `batch_files`）保持区分。

### 5.2 `endpoints/openapi_v1/batches/one.go`：去包装、按契约返回

- 删除 `batchTaskDetailResponse`，handler 返回填充好 `BatchFiles` 的
  `*ibatch.BatchTask`；
- 关联文件拉取逻辑不变：`input_file_id` / `output_file_id` best-effort
  逐个 `FetchFile`（缺失不阻塞详情），逐个转 `BatchFileMeta` 填入；
- Redis 回源快照（`sync_source=redis`）路径同样填 `batch_files`
  （BFE 的 `BATCH_TASK` hash 含 `input_file_id` / `output_file_id`，
  回源后同样可按文件 ID 查 `batch_files` 表 / Redis）。

### 5.3 `filename` 字段校准

`batch_files` 表无 `filename` 列，一期无法返回真实文件名：
`BatchFileMeta.Filename` 固定为空（`omitempty` 序列化时省略），并在
`batches.md` §2.2 字段表补注「`filename`：一期留空，文件元数据不落库文件名，
后续接入」。这是文档与 as-built 的校准，不改变 §2.2 字段集。

### 5.4 行为变化表

| 场景 | 修复前 | 修复后 |
|------|--------|--------|
| 详情成功响应结构 | `Data:{task:{...},files:[...]}` | `Data:{...任务字段,batch_files:[...]}` |
| 文件字段形状 | 内部实体（含 `id`/`direction`/`key_name` 等） | §2.2 视图（`file_id`/`provider`/`purpose`/`bytes`/`created_at`） |
| 无关联文件 | `files:[]` | `batch_files` 省略（`omitempty`） |
| 404 / 401 / 402 / 422 | 不变 | 不变 |
| 列表 / 取消 / 文件查询 | 不变 | 不变 |

## 6. 测试

### 6.1 单元测试（`endpoints/openapi_v1/batches/batches_test.go`）

- 改造 `TestBatchTaskOneAction`：断言返回 `*ibatch.BatchTask`，
  `BatchFiles` 长度 2（input + output）且元素为 `BatchFileMeta` 形状；
- 新增用例：output 文件缺失时 `BatchFiles` 仅含 input，不报错；
- 新增用例：`BatchFile.ToMeta()` 转换不携带内部字段。

### 6.2 回归验证

1. `go build ./...`、`go vet ./...`、`go test ./...` 全绿；
2. `make test-model-cover-gate`（model ≥70%）；
3. 集成：详情端点断言 `ErrNum=200`、`Data.batch_id` / `Data.status` 顶层可取、
   `Data.batch_files[*]` 含 `file_id` / `provider` / `bytes`（修复前
   这些路径取不到值，即 issue 复现点）。

## 7. 文档与契约

- `batches.md` §3.2 契约不变（本次是运行行为向契约收敛）；
- `batches.md` §2.2 补注 `filename` 一期留空（见 §5.3）；
- 变更记录即本文档；无 DDL / 配置 / 数据面变更。

## 8. 实施记录

实施已完成并验证（2026-10-16），与 §5 方案一致。

**改动文件**：

- `model/ibatch/types.go`：新增 `BatchFileMeta` 对外视图（§2.2 形状，
  `filename` 一期固定为空）与 `BatchFile.ToMeta()` 转换；`BatchTask` 增加
  非持久化 `BatchFiles []*BatchFileMeta`（`json:"batch_files,omitempty"`，
  DAO 不读写）；
- `endpoints/openapi_v1/batches/one.go`：删除 `batchTaskDetailResponse`
  包装结构，详情 handler 返回填充好 `batch_files` 的 `*ibatch.BatchTask`
  （Data 顶层即任务字段，对齐 batches.md §3.2）；
- `endpoints/openapi_v1/batches/batches_test.go`：`TestBatchTaskOneAction`
  按新契约重写（断言返回 `*ibatch.BatchTask`、`BatchFiles` 为 meta 形状），
  新增"输出文件缺失不阻塞详情"用例；
- 新增 `model/ibatch/types_test.go`：`ToMeta()` 映射与序列化不泄漏内部
  字段断言、`batch_files` omitempty 行为用例。
- 集成测试（`test/integration`，按
  `.agents/skills/ai-gateway-api-integration-test-design` 方法论以合同为
  唯一断言依据）：
  - `tests/batch/detail/detail_test.go`：BT-2-002 按 batches.md §3.2 合同
    重写——顶层形状锁（`Data.task` / `Data.files` 包装层不得存在，家族11）、
    任务字段顶层取值、`batch_files` 元素键集合精确锁（§2.2 五键，
    家族4/#201 防幻影键）、原始报文无 `"direction"` 防泄漏断言；
    初版用例曾按实现现状断言 task/files 包装与 direction 字段（反面模式
    #5，把缺陷固化成通行证），一并修正；新增 BT-2-004（无关联文件时
    `batch_files` 键缺席，omitempty）；
  - `tests/batch/design.md`：BT-2-002 修订登记 + BT-2-004 登记 + 断言要点；
  - `tests/schema/openapi/batch.go`（新增）：`BatchTaskDetailSchema` /
    `BatchFileMetaSchema`（合同字段，内部超集键不列入），注册
    `testBatchSchema`（含包装层缺席与元素键集合精确锁）。

**验证结果**（环境无 make，按 Makefile target 等价命令执行）：

- 构建：`go build ./...`、`go vet`（ibatch / batches 两包）通过；
- 单元测试：`go test ./model/ibatch/... ./endpoints/openapi_v1/batches/...`
  全 PASS（初跑一处断言过严——`BatchFileMeta.CreatedAt` 零值序列化——
  属测试自身预期问题，已修正）；
- model 覆盖率门禁：85.0% ≥ 70%，通过；
- 全量回归：`go test ./...` exit 0 全绿。
- 集成测试（重建 `ai-gateway-api.exe` 后，`tests/batch/...` 全量 PASS，
  含重写后的 BT-2-002 与新增 BT-2-004；`tests/schema/...` 全量 PASS，
  含新增 `batch_task_detail` schema 子用例）。

**结论**：issue #249 修复落地——批量任务详情 `Data` 顶层即任务字段并携带
`batch_files`（§2.2 形状），ai-gateway-web 详情页按文档取值即可恢复，
前端零改动。列表 / 取消 / 文件查询端点行为未变。

**沉淀评估（六步法 Step 6）**：本次为响应结构向已批准契约的收敛修复，
契约本身在 `batches.md` §3.2 与《接口层设计文档.md》§4.2.13 已有承载，
无新机制、无复杂权衡，不新增 `sys-design/details/` 文档。

**遗留**（记入观察，不在本期范围）：

- 列表与取消接口响应仍含文档 §2.1 未列出的内部字段（`id` / `key_name` /
  `idem_key`），属"契约超集"，前端无感；如需裁剪可另起小卡统一处理；
- `GET /batch-files/{file_id}`（§3.4）目前直接序列化内部 `BatchFile`
  实体，与 §2.2 视图同样存在形状偏差（外泄内部字段、无 `filename`），
  可随下一批次管控接口打磨一并收敛。
