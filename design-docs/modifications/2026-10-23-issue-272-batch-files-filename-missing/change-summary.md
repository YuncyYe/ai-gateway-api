# Issue #272：batch_files[].filename 缺键——控制面修复说明

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/272](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/272)

> `GET /open-api/v1/batches/{id}` 返回的 `batch_files[]` 元素缺 `filename` 键。触发条件是该文件已被同步/登记成
> 控制面 DB 行：此后即使 provider 在 File 对象里明确给了文件名、数据面 readback 一致、Redis `BATCH_FILE` hash
> 仍有值，控制面响应也不再带 `filename`。§2.2 只允许在"老键、provider 未给"两种情形下省略该键，本场景两者都不成立。

产品条款依据（Oracle）：`design-docs/api-define/OpenAPI接口定义/batches.md`

- §2.2（第 121 行）：`filename` 为"provider File 对象给出的文件名（数据面同步落库）；缺失时（老键、provider 未给）
  为空，响应中省略该键"；
- §2.2 示例（第 106-114 行）显式写 `"filename": "input.jsonl"`。

即条款要求是"provider 已给则必现"，省略豁免仅限"老键 / provider 未给"。`omitempty` 只解释实际行为，
不是 Expected 来源。

系统级验证用例：`test-cases/SC1401/sc1401_tc026_test.go:1038`（`A-state-accounting`，由 `:480` 的
`WaitTerminal` 在 `state-output-accounting` 阶段调用）。

## 2. 根因（当前 HEAD 逐环核实，2026-10-23）

### 2.1 写入侧参数层有值

`model/ibatch/redis_view.go:435-436` 的 `mergeFileForSync` 保留 filename；`storage/rdb/batch/batch_file.go:92`
的 `batchFileDataToParam` 把 `Filename` 写入 `TBatchFileParam`（`Filename: lib.PString(file.Filename)`）。
input 文件的登记（`model/ibatch/sync.go:170` `syncOneFile` → `UpsertBatchFile`）与 output 文件的登记
（`model/ibatch/advance.go:300` 输出文件走同一条 upsert）在参数层都带 filename。**链路在此是好的。**

### 2.2 根因：DAO upsert 的列清单与参数都不含 filename，值被静默丢弃

`storage/rdb/internal/dao/table_batch_files.go:120` `TBatchFileUpsertByFileProvider` 是 `batch_files` 表
唯一生产写入路径（`TBatchFileCreate` `:96` 能写全字段，但仓库内无生产调用方，仅 `_test.go` 命中）。其三处
都缺 `filename`：

- `args`（`:131-135`）：13 个值，无 `data.Filename`；
- MySQL INSERT 列清单（`:139-141`）与 SQLite INSERT 列清单（`:147-149`）：13 列，无 `filename`；
- `ON DUPLICATE KEY UPDATE`（`:142-145`）/ `ON CONFLICT DO UPDATE`（`:150-153`）的刷新列表同样不含。

→ 参数里的文件名被静默丢弃，DB 行 `filename` 恒为 NULL（DDL 中列存在：`db_ddl.sql:740` /
`db_ddl_sqlite.sql:751`，`VARCHAR(256)` / `TEXT`，可空）。

### 2.3 读出侧：DB 优先且不与 Redis 合并

`model/ibatch/manager.go:214` `FetchFile`：DB 命中即返回（`:223-225`），`applyFileView` 仅在 DB miss 的
Redis 回源分支生效（`:226-247`）→ 空 filename 原样返回，不与 Redis `BATCH_FILE` 里仍在的值合并。

### 2.4 序列化：omitempty 把空串省略成缺键

`endpoints/openapi_v1/batches/one.go:55-59` 取到文件后经 `ToMeta()` 输出，`model/ibatch/types.go:176`
`Filename string \`json:"filename,omitempty"\`` 把空串省略 → 响应缺键。

### 2.5 同日行为反转的解释

文件上传后约 3 秒由对账 job 落库（`first_seen_at` 远早于当日 22:02 的检查）。**该行自建立起 `filename`
即为 NULL**；22:02 那次检查通过只能解释为当时该行尚未建立、走了 DB miss 的 Redis 回源分支；23:26 检查
时已落库，DB 命中即返回 NULL。替代解释均已排除（Redis 值仍在、同一响应其他字段正常、单文件接口返回
DB-only 字段 `id` / `first_seen_at` 证明确实命中 DB 行）。

**链路到此结束**：数据面、Redis、读出通路、序列化均无缺陷，唯一断点是 2.2 的 upsert。

## 3. 方案

### 3.1 修复 upsert：把 filename 补进列清单、参数与冲突更新

`storage/rdb/internal/dao/table_batch_files.go` `TBatchFileUpsertByFileProvider`：

1. `args` 增加 `data.Filename`；
2. MySQL / SQLite 的 INSERT 列清单各增加 `filename`（14 列 14 值）；
3. 冲突更新列表增加 filename，**带 NULL 保护**——
   - MySQL：`filename=COALESCE(VALUES(filename), filename)`
   - SQLite：`filename=COALESCE(excluded.filename, batch_files.filename)`

   理由：provider 未给文件名的场景（如 output 文件首次登记、`advance.go:280` `registerOutputFile` 只构造
   `file_id`/`provider`/`api_key_id`/`key_name`/`product_name`/`direction`，且其 Redis `BATCH_FILE` hash 可能
   只有 `batch_id`/`dir`/`api_key_id`/`key_name`）合并出的 `Filename` 为空；若用裸 `filename=VALUES(filename)`
   会把已有非空文件名刷回 NULL。`COALESCE` 语义即条款语义："provider 已给则落库，未给不覆盖已有值"。
4. 函数头注释的"可变字段"清单同步补 `filename`。

### 3.2 存量数据回填

- **自愈路径**：修复后，Redis `BATCH_FILE` hash 仍存在的行，下一次对账 sync 走 `syncOneFile` upsert 时
  会带上 filename 并刷新 DB 行，无需人工干预；
- **一次性回填**：对 Redis hash 已过期但 DB 行仍在的存量数据，提供回填 SQL/脚本从数据面补 `filename`
  （按 `file_id` 反查数据面 File 对象；若数据面也已清理，则维持 NULL 并享受条款豁免，属合法省略）。

### 3.3 不改的部分（及理由）

- **读出侧 `FetchFile` 不做 DB+Redis 合并**：DB 是同步落库后的权威源，为它再叠一层 Redis 回合并引入
  两个源的优先级语义；修好写入侧后 DB 值即正确。
- **`types.go` 的 `omitempty`**：保留。空 filename（provider 未给）省略该键正是 §2.2 豁免语义；
  #271 已论证"必现"原则适用于带 `≥0` 约束/零值示例的字段，filename 是"缺失时省略"条款字段，不适用。
- **`TBatchFileCreate`**：保持现状（无生产调用方），不扩大改动面。

## 4. 影响面

- **接口**：`GET /open-api/v1/batches/{id}` 的 `batch_files[]`，以及 `GET /open-api/v1/batch-files/{file_id}`
  （同一读出链路）。响应从"缺 `filename` 键"变为"provider 已给时出现该键"——这正是条款本来就要求的行为，
  属契约对齐而非新增键。
- **消费方**：SC1401-TC026 `:1038` 断言（`A-state-accounting`）、对账/排障脚本。
- **DB/DDL**：无改动（列已存在）。
- **数据面（BFE）**：无改动。
- **版本**：不涉及接口版本升级。

## 5. 行为变化表

| 场景 | 现状 | 修复后 |
|------|------|--------|
| input 文件已同步落库，provider 已给 filename | `filename` 键缺席 | 键出现，值为 provider 文件名 |
| provider 未给 filename（老键 / output 首次登记） | 键缺席 | 不变（§2.2 豁免） |
| 冲突 upsert 且新值 filename 为空 | DB 行非空 filename 保持不变（因为根本没写） | `COALESCE` 保证不刷回 NULL |
| 其余 `batch_files[]` 字段（file_id/bytes/purpose/created_at 等） | — | 不变 |

## 6. 测试

### 6.1 DAO / storage 单测

- `storage/rdb/internal/dao`：为 `TBatchFileUpsertByFileProvider` 增加用例——upsert 带 filename 后
  `TBatchFileOne` 读出非空；同键二次 upsert 传空 filename 时 DB 值不回退（验证 `COALESCE` 分支）；
- `storage/rdb/batch`：`batchFileDataToParam` 已带 filename（现状即通过），补一条 upsert 往返断言。

```bash
go build ./... && go vet ./...
go test ./storage/rdb/... ./model/ibatch/...
```

### 6.2 集成测试（`test/integration/tests/batch/`）

新增详情用例：种子 `batch_tasks` + `batch_files` 行（filename 非空），断言详情响应
`batch_files[]` input 元素含 `filename` 键且等于种子值，output 元素按种子值出现/省略。断言方法沿用
#271 确立的 map 存在性 + RawBody token 双锁。**判据属性**：修复前用例必须失败（filename 缺键），修复后
通过——本地回归闸门，与 SC1401-TC026 形成双锁。

### 6.3 系统级判据（issue 给定）

`GET /open-api/v1/batches/{id}` 的 `batch_files[]` input 元素键集合含 `filename`；Redis
`BATCH_FILE` hash 有值而响应缺键的三方共存场景不再出现；SC1401-TC026 `A-state-accounting` 通过。

整体回归：

```bash
cd ai-gateway-api
make test
```

## 7. 文档与契约同步

- `design-docs/api-define/OpenAPI接口定义/batches.md` §2.2：**不改条款**（条款已是 Oracle，缺陷是
  落库行为偏离）。§2.2 语义明确，无需追加总则。
- `design-docs/sys-design/details/批量任务与对账.md`：在对账落库小节补一句"`batch_files` upsert 列清单
  必须覆盖 `filename`，与 `TBatchFileParam` 字段保持同步；新增字段时两处同改"，防止同类静默丢弃复发。

## 8. 遗留与范围边界

1. **output 文件元素额外缺 `bytes`/`purpose`**（issue 已注明）：成因与 filename 不同——
   `registerOutputFile` 首次登记只构造标识字段，其 Redis `BATCH_FILE` hash 亦无 bytes/purpose，
   `mergeFileForSync` 无从合并。是否应由 provider File 对象补齐属另一个待产品确认点，**不在本 issue
   修复范围**（本 issue 的 filename 判据只用 input 元素）。
2. **回填脚本**：§3.2 的一次性回填可作为运维动作执行，不随代码发布；需要时另起小卡。
3. 本卡与 #271（零值金额省略）、整组输出/结算字段缺失卡片**成因不同、互不替代**——修其中一处
   不会连带修好其余。
4. **实施状态：已实施**（2026-10-23 落地，实施记录见 §9；§6 的回归闸门用例 BT-2-008
   已完成"回退修复 FAIL / 恢复修复 PASS"双向验证）。

## 9. 实施记录（2026-10-23，六步法 Step 5/6）

代码、单测、集成测试与文档已按 §3 落地。

### 9.1 改动文件

| 文件 | 动作 | 说明 |
|------|------|------|
| `storage/rdb/internal/dao/table_batch_files.go` | 修改 | `TBatchFileUpsertByFileProvider`：args/双方言 INSERT 列清单补 `filename`（14 列 14 值）；冲突更新 `filename=COALESCE(NULLIF(..., ''), 旧值)`；函数头注释同步 |
| `storage/rdb/internal/dao/table_batch_tasks_test.go` | 修改 | 内存库 `batch_files` DDL 补 `filename` 列；`TestTBatchFileUpsertByFileProvider` 增首次落库 + 空串二次 upsert 不回退断言 |
| `storage/rdb/batch/batch_test.go` | 修改 | `TestBatchFileConversions` 补 filename upsert 往返断言（现状即通过） |
| `test/integration/tests/batch/detail/detail_test.go` | 修改 | +BT-2-008 `TestBatchDetail_FilenameFromUpsertPath`（种子走生产 upsert 通路） |
| `test/integration/tests/batch/design.md` | 修改 | §4.2 用例表 +BT-2-008 行 |
| `test/integration/go.mod` / `go.sum` | 修改 | 集成模块新增对主模块 `lib` / `model/ibatch` / `storage/rdb/batch` 的导入，`go mod tidy` 补传递依赖 |
| `design-docs/sys-design/details/批量任务与对账.md` | 修改 | 职责①小节补维护性注记（INSERT 列清单/冲突更新与 `TBatchFileParam` 同步，Step 4） |
| `model/ibatch` 读出逻辑 / `types.go` omitempty / BFE 数据面 | 不改 | 按 §3.3 范围边界 |

### 9.2 与方案的偏差（实施中确认）

1. **冲突更新的空值保护由裸 `COALESCE` 加强为 `COALESCE(NULLIF(新值,''), 旧值)`**：
   实施中发现参数层 `batchFileDataToParam` 经 `lib.PString` 把空 filename 转成**空串**
   （非 nil 指针），生产二次 upsert 实际传 `''`——裸 `COALESCE(VALUES(filename), filename)`
   会拿空串覆盖已有值，集成用例首轮跑挂即暴露此问题（DB 行被刷成 '' → 键缺席）。
   `NULLIF` 把空串归一为 NULL 后 COALESCE 兜底旧值，对 nil/'' 两种入参形态都成立，
   语义仍即条款语义"provider 已给则落库，未给不覆盖已有值"。
2. **BT-2-008 种子走生产 upsert 通路而非 helper 直写 SQL**：§6.2 原文写"种子 batch_tasks
   + batch_files 行（filename 非空）"，但直写 SQL 会绕过缺陷路径（写入侧无丢列），
   修复前后用例都通过，不满足 §6.2 自定的判据属性（修复前必须失败）。实施改为经
   `batch.NewBatchStorager` + `UpsertBatchFile` 落库（即对账 sync 的生产写入路径），
   使用例真实复现"参数有值、SQL 丢列"的缺陷。
3. 其余按方案执行，无偏离。

### 9.3 验证结果

| 验证项 | 结果 |
|--------|------|
| `go build ./... && go vet ./...`（主模块） | 通过 |
| `go test -count=1 ./storage/rdb/... ./model/ibatch/...` | 全部 ok（EXIT 0，含新增 DAO/storage 用例） |
| `go vet ./tests/batch/...`（集成模块） | 通过 |
| `bash test/scripts/run_module_tests.sh batch`（SQLite 集成全量，含 BT-2-008） | detail/list/cancel/file 全部 ok（EXIT 0） |
| **复现实验①**：`git checkout` 回退 DAO 改动（回到 HEAD 缺陷态）→ 跑 BT-2-008 | **FAIL**（`input 元素必须含 filename 键`，detail_test.go:451——filename 缺键，缺陷复现） |
| **复现实验②**：`git apply` 恢复修复 → 再跑 BT-2-008 | **PASS**（0.18s） |
| MySQL 后端集成（`AIAPI_MYSQL_DSN`，本机 MySQL 8.4.9，环境说明见 `environment/mysql-installation.md`）：`bash test/scripts/run_module_tests.sh batch` | detail（含 BT-2-008 PASS）/ list / cancel / file 全部 ok（EXIT 0） |
| MySQL upsert 语句定点验证：在 MySQL 8.4.9 按 `db_ddl.sql` 建 `batch_files` 表，直接执行生产 upsert SQL 四场景 | ① 带 filename upsert → 落库 `input.jsonl`；② 空串二次 upsert → 值不回退；③ 真实新文件名 upsert → 正确更新为 `new.jsonl`；④ NULL filename 新行 → 允许插入 NULL 且不影响既有行。四场景全部符合预期 |

注：单测与集成测试串行运行（#271 §10.3 注的 Windows 日志互锁问题），本次全部串行执行。

### 9.4 六步法核对

- [x] modifications 目录已创建（Step 1）
- [x] 变更说明已编写（Step 2，本文档 + design-changes.md）
- [x] api-define 无需变更（Step 3，§7：条款已是 Oracle，不改 batches.md）
- [x] sys-design 已更新并 review（Step 4，details/批量任务与对账.md 职责①维护性注记）
- [x] 代码按设计实现并通过测试（Step 5，§9.3；含 §9.2 两处经判据属性验证确认的偏差修正）
- [x] 已评估 details 沉淀（Step 6）：根因模式（"参数有值、SQL 丢列"的静默丢弃）已落入
      details 维护性注记与 BT-2-008 用例注释，回归闸门与 SC1401-TC026 形成双锁，
      无需新建 details 文档
