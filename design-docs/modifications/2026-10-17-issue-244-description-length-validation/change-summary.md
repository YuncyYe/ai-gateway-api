# Issue #244 + SC2101-TC024：`description` 长度校验缺口修复方案（Entity-Type 越界 256 / Cluster 缺校验）

本期一并修复两个同族缺陷：

- **A. Issue #244**（本仓 issue）：Entity-Type `description` 文档要求 0-255，实现上限写成 256，恰好 256 被接受并落库；
- **B. SC2101-TC024**（本期并入，按 owner 裁定）：Cluster `description` 文档要求 0-256，实现**完全没有校验**，257 及以上被接受。

## 1. 问题来源

### 1.1 A：Entity-Type `description` 上限越界（Issue #244）

[rainway-ai-gateway/ai-gateway-api/issues/244](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/244)

> `PATCH /open-api/v1/entity-types/{type_name}` 提交长度恰好 256 的 `description`，服务返回 HTTP 200 / `ErrNum=200` 并把它写入该 Entity-Type（读回即该 256 字符串），而不是按设计拒绝。承载上限的同一常量也被 `POST /open-api/v1/entity-types` 使用，创建路径存在同一越界。

产品条款依据（Oracle）：`design-docs/api-define/OpenAPI接口定义/entity-types.md`

- §1 数据模型（第 19 行）：`description` —— 非必填；若传入，**长度 0-255**；不能包含控制字符（原文措辞为"0-255 字符"，2026-10-17 按 §5.4 结论明确为"0-255 字节（按 UTF-8 字节计）"，见 §7）；
- §2.1 创建 Entity-Type 输入参数（第 43 行）：同一字段、同一合法性条件；
- §2.4 更新 Entity-Type（第 122-147 行）：更新的是同一 `description` 字段（该节 Body 参数表原未重述长度条件，规范性约束由 §1 收口；2026-10-17 已在表内补引用 §1，见 §7）。

受影响 TC：`SC1203-TC011`（A-04 `description-patch-validation`）、`SC1203-TC015`、`SC1203-TC017`（含 invalid-description-atomicity）。

### 1.2 B：Cluster `description` 长度/控制字符未校验（SC2101-TC024）

观察记录（`SC2101-TC024` 失败摘要）：`Cluster description length 257 code=200` —— 257 字符的集群描述被接受。

产品条款依据（Oracle）：`design-docs/api-define/OpenAPI接口定义/clusters.md`

- §1 数据模型字段表（第 135 行）：`description` —— 非必填；若传入，**长度 0-256**；不能包含控制字符；
- §2.1 创建集群"约束"（第 301 行）与 Body 参数表（第 365 行）：同一字段、同一合法性条件；
- §2.4 更新集群基本配置（第 596 行起）：Body 说明为"可修改字段含义同创建接口"，`description` 的合法性条件经 §1/§2.1 继承（该节无独立条件，与 A 的 §2.4 情形不同，**文档侧无缺口**）。

## 2. 根因（均已对当前检出逐环直读核实，2026-10-17）

### 2.1 A：上限常量取错（256 与 255 两个同族常量并存）

调用链（`source/ai-gateway-api`）：

1. `endpoints/openapi_v1/entity_type/update.go:67-71`（PATCH）与 `endpoints/openapi_v1/entity_type/create.go:55-59`（POST）：
   `param.Description != nil` 时调用 `validate.Description(*param.Description, validate.MaxDescriptionLength, "description")`；
2. `lib/validate/validate.go:55`：`MaxDescriptionLength = 256` —— 上限常量取 256，与条款"0-255"差一；
3. `lib/validate/validate.go:335-339`：`func Description` 以 `if len(s) > maxLen` 判定超限 ⇒ 恰好 256 通过校验、落库、返回 200；
4. 姊妹常量取值冲突：同文件 `lib/validate/validate.go:56` 已有 `MaxEntityDescriptionLength = 255`，被 `endpoints/openapi_v1/entity/validator.go:42` 正确用于 `/entities`（`entities.md` 同为 0-255）。**两个近乎同名的常量 256 / 255 并存，Entity-Type 路径取到了 256 那个**（常量选择/命名混淆）；
5. 历史来源：`MaxDescriptionLength = 256` 由提交 `b8a8a93`（2026-07-30，"feat: enforce OpenAPI param validation…"）引入并接入 Entity-Type 两个端点；`MaxEntityDescriptionLength = 255` 由 `980e493`（/entities 新增 description）引入。全仓 `MaxDescriptionLength` **仅这 2 处引用**（已 grep 全仓，含非 Go 文件）。

既有测试为何没拦住：

- `test/integration/tests/entity_type/update/update_test.go:82-91`（ET-4-004"更新 description 超过最大长度"）用的是 `strings.Repeat("a", 300)`——300 在 256 与 255 下都被拒，**没有钉边界**；
- `test/integration/tests/entity_type/create/create_test.go` 的 9 个用例**无 description 长度臂**；
- `lib/validate/validate_test.go` 无 `Description()` 的直接单测（只有 `TestAPIKeyDescription` 用 513）。

### 2.2 B：Cluster `description` 无任何校验

1. 校验入口是 `UpsertParam.Validate()`（`endpoints/openapi_v1/product_cluster/create.go:116-136`），它只校验 `name`（`validate.ClusterName`）、`basic` 数值范围、`sticky_sessions`、`passive_health_check`、`llm_config`；
2. `UpsertParam.Description`（同文件第 57 行，`json:"description"`，**无 validate tag、无集中校验**）在 `Validate()` 中完全没有出现；
3. 两个写入路径直接透传：`clusterParamControlModel`（`create.go:248`）与 `clusterParamControlModel4Update`（`update_basic.go:131`）都写 `Description: param.Description`；
4. `model/icluster_conf/`（`cluster.go:136` / `:304`）与存储层同样**没有** description 校验（已 grep：描述相关的校验命中为零）；
5. 列宽不构成兜底：`clusters.description` 为 `varchar(1024)`（`db_ddl.sql:60`），256/257 都装得下 ⇒ 任何长度的描述都能落库；
6. 因果结果：`POST /clusters` 与 `PATCH /clusters/{cluster_name}` 提交 257 及以上长度、或含控制字符的 `description`，均返回 HTTP 200 / `ErrNum=200` 并落库。

与 A 的区别：A 是"常量取值取错（差一）"，B 是"校验整体缺失"。两者同属 `description` 字段族，但根因与改动点不同，故分开叙述、合并本期交付。

### 2.3 Claim 校验（共同）

A 的调用点、常量取值、比较方向、姊妹常量及其正确用法均已逐环直读核实；"256 被接受"由 issue 中三条 TC 的历史失败摘要（均为"256 被接受，HTTP 200 / ErrNum=200"）与 2026-10-14 真实环境 replay 佐证。B 的"无校验"由 `UpsertParam.Validate()` 全文、两个写入路径与 `model/icluster_conf` 的全量 grep 佐证；"257 被接受"来自 `SC2101-TC024` 的失败摘要（该 TC 的测试设计文档不在本仓，仅取失败摘要作为现象证据）。

## 3. 影响面

| 项目 | A（Entity-Type） | B（Cluster） |
|------|------------------|--------------|
| 接口范围 | `POST /open-api/v1/entity-types`、`PATCH /open-api/v1/entity-types/{type_name}` 的 `description` | `POST /open-api/v1/clusters`、`PATCH /open-api/v1/clusters/{cluster_name}` 的 `description` |
| 契约上限 | 0-255（字节） | 0-256（字节） |
| 修复前错误形态 | 恰好 256 及以下被接受（200）并落库；控制字符已拒绝（422） | **任意长度**被接受（200）并落库；**控制字符也被接受**（200） |
| 修复后错误形态 | 256 及以上 → 422 参数非法 | 257 及以上 → 422；含控制字符 → 422 |
| 调用方 | 控制台（ai-gateway-web）、治理/同步脚本、上层编排与对账工具 | 同上（集群创建/编辑页、自动化建集群脚本） |
| 数据/配置 | 无 DDL 变更、无配置变更、无数据迁移（见 §5.5） | 同上（`clusters.description varchar(1024)` 宽于契约，无需改列） |
| 数据面 | 无 BFE / conf-agent 联动（该字段不参与配置导出，仅管控面存储） | 同左（已核：`model/imods/` 导出逻辑不含 cluster description） |

## 4. 目标

1. **A**：Entity-Type `description` 恰好 256 字节在 **POST 与 PATCH 两条路径**均被拒绝（422），且不落库；
2. **A**：255 字节、空串 `""`、未携带（PATCH）三条合法路径行为不变；
3. **B**：Cluster `description` 恰好 256 字节**通过**、257 及以上被拒绝（422），在 **POST 与 PATCH 两条路径**一致；
4. **B**：Cluster `description` 含控制字符被拒绝（422）—— 这是当前行为的**新增拦截**（此前 200 放行）；
5. 两域的 PATCH 被拒时**不得改写**库中既存 description（原子性）；
6. 控制字符规则（`NoControlChars`）在两条路径上均**被单独钉住**（A 的该臂此前 E2E 未触达）；
7. 常量取值与命名消除"同族两常量 256/255 并存"的误用空间，避免同类回归；Cluster 常量按域显式命名，避免再出现"无归属常量"；
8. 契约与实现边界一致：`SC1203-TC011` / `TC015` / `TC017` 与 `SC2101-TC024` 的对应断言具备转 PASS 的条件。

## 5. 最终方案

两处契约均为已批准且已发布的对外合同（A 的条款 Release `0.0.9`、`released` / `normative`），故一律**修实现向契约收敛**，不放宽断言、不改 §1 的 0-255 / 0-256 数值。

### 5.1 A：校验常量按域命名并收敛为 255（`lib/validate/validate.go:55-56`）

```go
const (
    ...
    // MaxEntityTypeDescriptionLength 是 /entity-types 的 description 上限，
    // 依据 design-docs/api-define/OpenAPI接口定义/entity-types.md §1/§2.1：0-255。
    MaxEntityTypeDescriptionLength = 255
    // MaxEntityDescriptionLength 是 /entities 的 description 上限，
    // 依据 design-docs/api-define/OpenAPI接口定义/entities.md §1：0-255。
    MaxEntityDescriptionLength = 255
    ...
)
```

即：删除含义模糊、无归属的 `MaxDescriptionLength`，改为按域命名，让"哪个文档约束对应哪个常量"在名字与注释上显式可见 —— 本缺陷的根因正是常量归属不清导致的取错。

**备选（更极简，不推荐）**：保留单一 `MaxDescriptionLength = 255` 供 `/entities` 与 `/entity-types` 共用，删除 `MaxEntityDescriptionLength`。差异是少一个常量，但把两个资源（Entity / Entity-Type）的长度约束耦合在一起，日后任一文档调整都会牵动另一方；仅在希望最小 diff 时采用。

### 5.2 A：调用点改动（各一行）

- `endpoints/openapi_v1/entity_type/create.go:56`：`validate.MaxDescriptionLength` → `validate.MaxEntityTypeDescriptionLength`；
- `endpoints/openapi_v1/entity_type/update.go:68`：同上；
- `endpoints/openapi_v1/entity/validator.go:42`：保持 `validate.MaxEntityDescriptionLength` 不变（本来就是 255）。

改名的波及面已核：`MaxDescriptionLength` 全仓仅上述 2 处引用，`lib/validate` 在本仓以外无导入方（工作区内 grep 无外部 importer），删除该符号不会破坏其他仓库/模块。

### 5.3 B：Cluster 补齐 `description` 校验（新增常量 + 接入集中校验）

**新增常量**（`lib/validate/validate.go` const 块，与 §5.1 同一批）：

```go
// MaxClusterDescriptionLength 是 /clusters 的 description 上限，
// 依据 design-docs/api-define/OpenAPI接口定义/clusters.md §1/§2.1：0-256。
MaxClusterDescriptionLength = 256
```

**接入集中校验**（`endpoints/openapi_v1/product_cluster/create.go` 的 `UpsertParam.Validate()`，紧随 `ClusterName` 校验之后）：

```go
if p.Description != nil {
    if err := validate.Description(*p.Description, validate.MaxClusterDescriptionLength, "description"); err != nil {
        return err
    }
}
```

复用 `validate.Description`（长度 + `NoControlChars`），不新写校验逻辑，语义与 `/entities`、`/entity-types`、`/api-keys` 一致。

**为什么一处改动即覆盖两个端点**（已核）：

- `POST /clusters`：`newCreateParam4Create` → `xreq.BindJSON` → `validateData` → `Validator.Validate()`；
- `PATCH /clusters/{cluster_name}`：`newUpdateParam4Update` → `xreq.Bind` → `validateData` → `Validator.Validate()`；

`lib/xreq/validate.go:33-45` 的 `validateData` 会在结构体 tag 校验通过后调用实现了 `Validator` 接口的参数的 `Validate()`，并把错误包成参数错误 → **`ErrNum=422`**，无需新增错误码或包装器。

**原子性**：校验发生在 `Bind`/`BindJSON` 阶段，早于 `updateActionProcess`（`update_basic.go:97`）与任何 `UpdateCluster` 调用 ⇒ 被拒时不会触达写路径，库值不变。

**范围说明**：`/clusters` 只有上述两条写入路径携带 `description`（`create.go:248`、`update_basic.go:131`，已 grep 全包）；子集群、provider 实例池等不写 cluster description，不在本期范围。

### 5.4 判定单位：维持字节（`len`），口径已确认并落入文档（两域共用）

现状：`Description()` 用 `len(s)`，即 **UTF-8 字节数**；文档措辞是"0-255 / 0-256 **字符**"。两者在纯 ASCII 下等价，在多字节文本下不等价。

**结论（2026-10-17 owner 已确认）：维持字节判定（`utf8` 字节数，`len(s)`），只做边界收敛**，并把文档措辞明确到单位。理由：

1. 与仓库既有口径一致：所有长度校验都用 `len()`（Entity-Type/Entity name、API-Key description 512、Provider description 256），单独把 description 换成 `utf8.RuneCountInString` 会造成同族字段口径分裂；
2. 本期涉及的 TC 失败臂都是 ASCII（A：`invalid description "ddddd…" (len=256)`；B：`length 257`），字节/字符等价，收敛到各自上限后必然通过；
3. 改判 rune 计数是**放宽已上线行为**，且需同步 `/entities`、`/api-keys`（512）、`/providers` 与文档，属独立决策，超出本期范围。

**已知的反向偏差（已按"字节"口径确认，记录备查）**：字节判定会拒掉"≤上限字符数但 >上限字节数"的多字节描述（例：86 个汉字 = 258 字节，在 A 的 255 上限下被拒；集群侧同理，85 个汉字 = 255 字节可过、86 个即 258 字节被拒）。这是"过严"方向的偏差，已由文档措辞明确为字节计而消解（见 §7）。

#### 5.4.1 替代路径（仅当 owner 改判"字符"= 字符数时启用）

- `lib/validate/validate.go` `Description()`：`if len(s) > maxLen` → `if utf8.RuneCountInString(s) > maxLen`；
- 语义影响：`/entities`、`/entity-types`（255）、`/clusters`（256）、`/api-keys`（512）四处放宽（`/providers` 为独立实现，按其文档 0-256 亦需一并处理）；
- 存储可行性：相关列为 utf8mb4 `VARCHAR`（MySQL 按字符计），按字符判定仍可完整落库；
- 回归风险：需重跑四域的边界集成用例，并同步文档措辞。

### 5.5 DDL：本期不改

| 域 | 列定义 | 结论 |
|----|--------|------|
| Entity-Type | `entity_types.description VARCHAR(256)`（`db_ddl.sql:380`；SQLite 为 `TEXT`，`db_ddl_sqlite.sql:391`） | 保持不动 |
| Cluster | `clusters.description varchar(1024)`（`db_ddl.sql:60`） | 保持不动 |

理由：

1. 校验一旦收紧到各自上限，列宽宽于契约**不构成缺陷**（写入路径已封住），仅是 as-built 痕迹；
2. 仓库既有先例就是"列宽宽于契约"：`providers.description VARCHAR(1024)`（文档上限 0-256）、`clusters.description varchar(1024)`（文档上限 0-256）；
3. 把 `entity_types` 列收窄为 `VARCHAR(255)` 需先处理存量 256 字符数据 —— MySQL 严格模式下 `ALTER TABLE … MODIFY` 遇超长行会直接失败，牵出迁移脚本与人工确认步骤，收益低、风险高（可选路径见 §8）；
4. SQLite 部署 `entity_types.description` 本是 `TEXT`，**参数校验是唯一的收口点**，故修复必须落在校验层，列宽调整无法替代它。

### 5.6 行为变化表（两域合并）

| 域 | 场景 | 修复前 | 修复后 |
|----|------|--------|--------|
| A | POST/PATCH `description` 长度 256 | HTTP 200 / `ErrNum=200`，且落库 | **422 参数非法，不落库** |
| A | `description` 长度 255 | 200 | 200（不变） |
| A | `description` 为空串 `""` | 200 | 200（不变） |
| A | `description` 未携带（PATCH） | 不改动该字段 | 不改动（不变） |
| A | `description` 含控制字符（`\x01`、`\n`、`\x7f`） | 422 | 422（不变） |
| A | 报错文案 | `description length must be <= 256` | `description length must be <= 255` |
| B | POST/PATCH `description` 长度 ≥ 257 | 200，且落库 | **422 参数非法，不落库** |
| B | `description` 长度 256 | 200 | 200（**合法边界，不变**） |
| B | `description` 含控制字符（`\x01`、`\n`、`\x7f`） | **200，落库** | **422**（新增拦截，按 §1"不能包含控制字符"） |
| B | `description` 为空串 `""` | 200 | 200（不变） |
| B | `description` 未携带（PATCH） | 不改动该字段 | 不改动（不变） |
| B | 报错文案 | 无（不拦截） | `description length must be <= 256` / `string contains control characters` |
| 两域 | 其他接口/字段 | — | 全部不变 |

### 5.7 不改的东西

- 校验入口与错误码机制不变：两域均为 `xerror.WrapParamErrorWithMsg` / `validateData` 包装 → `ErrNum=422`（A 与既有 ET-4-004 的 422 断言一致；B 与既有 `CL-4-015`~`CL-4-023` 的 422 断言同一形态），无需新码；
- PATCH 原子性无需新增代码：A 的校验在 `UpdateEntityType`（`update.go:73`）之前；B 的校验在 bind 阶段、`UpdateCluster` 之前；
- 控制字符规则 `NoControlChars`（`validate.go:140-147`）不改；
- `/entities`（255）、`/api-keys`（512）、`/providers`（256）行为不变；
- Cluster 除 `description` 外的字段校验与默认值归一逻辑（`normalize*`）不变；`instance_pool`/子集群/EPP 相关逻辑不触碰。

## 6. 测试

### 6.1 单元测试（`lib/validate/validate_test.go`）

新增 `TestDescription`（表格驱动，maxLen 参数化，覆盖两域）：

| 用例 | 输入 | 期望 |
|------|------|------|
| A 边界 255 通过 | `strings.Repeat("a", 255)`，maxLen=255 | `nil` |
| A 边界 256 拒绝 | `strings.Repeat("a", 256)`，maxLen=255 | error |
| B 边界 256 通过 | `strings.Repeat("a", 256)`，maxLen=256 | `nil` |
| B 边界 257 拒绝 | `strings.Repeat("a", 257)`，maxLen=256 | error |
| 空串通过 | `""`（两种 maxLen） | `nil` |
| 控制字符拒绝 | `"abc\x01def"`、`"abc\ndef"`、`"abc\x7fdef"` | error |
| 常量钉住 | `MaxEntityTypeDescriptionLength == 255`、`MaxEntityDescriptionLength == 255`、`MaxClusterDescriptionLength == 256` | 断言相等 |

最后一条是**防回退锁**：常量若被再次改错，单测立即失败，避免同类缺陷无声复现。

### 6.2 集成测试 A（`test/integration/tests/entity_type/`，真实二进制 + SQLite 种子库）

按接口合同（`entity-types.md` §1/§2.1）断言，**不得**按实现现状把"256 通过"固化为通行证。

`update/update_test.go`：

- ET-4-004 收紧到边界：`description` 恰好 **256 → 422**（原 300 字符用例可保留为"远超限"臂）；
- 新增 ET-4-005 `description` 恰好 **255 → 200** 并回读一致（合法边界回归锁）；
- 新增 ET-4-006 控制字符：含 `\x01` 的 description → 422，GET 回读仍为原值；
- 新增 ET-4-007 **拒绝原子性**：先 PATCH 一个正常描述并 GET 回读确认，再 PATCH 256 字符 → 422，GET 回读**仍为上一次的正常值**（对应 SC1203-TC017 的 `invalid-description-atomicity`）。

`create/create_test.go`：

- 新增 ET-1-010 description 恰好 **256 → 422**；
- 新增 ET-1-011 description 恰好 **255 → 200**，并 GET 回读断言写入值。

`design.md` 同步登记：创建 9 → 11、更新 4 → 7、合计 18 → 23，并写明边界值与合同出处。

### 6.3 集成测试 B（`test/integration/tests/clusters/`，本期新增）

按接口合同（`clusters.md` §1/§2.1）断言；用例编号延续既有分段。实测现状：update 已用 `CL-4-001`~`CL-4-023` 与 `CL-4-101`~`CL-4-104`，**create 已用到 `CL-1-044`**（规划阶段的 `CL-1-031`~`CL-1-033` 已被占用，实际落地改为 `CL-1-045`~`CL-1-047`）。

`update/update_test.go`（`TestClusters_Update` 内）：

- 新增 **CL-4-024 `description` 恰好 256 字节 → 200**，GET 回读一致（合法边界回归锁，对应"257 才拒"的上界）；
- 新增 **CL-4-025 `description` 257 字节 → 422**（SC2101-TC024 的失败臂，修复后应转 PASS）；
- 新增 **CL-4-026 `description` 含 `\x01` → 422**，GET 回读仍为原值；
- 新增 **CL-4-027 拒绝原子性**：先 PATCH 正常描述并回读确认，再 PATCH 257 字节 → 422，GET 回读**仍为上一次的正常值**（与既有 `CL-4-104`"非法值拒绝 + 回读零变更"同模式）。

`create/create_test.go`（表格驱动）：

- 新增 **CL-1-045 `description` 恰好 256 字节 → 200**（含回读断言）；
- 新增 **CL-1-046 `description` 257 字节 → 422**（断言报错含 `description length must be <= 256`）；
- 新增 **CL-1-047 `description` 含 `\x01` → 422**（断言报错含 `control characters`）。

`test/integration/tests/clusters/design.md` 同步：

- 新增上述 7 个用例的登记（编号、期望、合同出处）与 §6.4/§9.4 详细设计；
- 第 89 行 `description` 合法性条件由"长度 ≤256；不能包含控制字符"改为"长度 ≤256 字节（按 UTF-8 字节计）；不能包含控制字符"，与 §7 的契约措辞一致；
- §3 统计：创建 28 → 31、更新 11 → 15、合计 49 → 56。

### 6.4 回归验证

1. 静态与单测：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；
2. 覆盖率门禁：`make test-model-cover-gate`（本次仅动 `lib/validate`、entity_type 调用点、product_cluster 校验，model 层无改动，仍须确认 `model/` ≥ 70%）；
3. 集成：重建 `ai-gateway-api.exe` 后跑 `test/integration/tests/entity_type/...` 与 `test/integration/tests/clusters/...`，全量 PASS；
4. 靶向复现：
   - A：PATCH/POST 提交 256 字节 description → 422 且 GET 回读未被改写；255 字节 → 200；
   - B：PATCH/POST 提交 257 字节 description → 422 且 GET 回读未被改写；256 字节 → 200；含 `\x01` → 422；
5. TC 侧收口：`SC1203-TC011` / `TC015` / `TC017` 与 `SC2101-TC024` 的对应断言应转 PASS —— 由 TC 测试方在其环境复跑确认，不在本仓验证范围。

## 7. 文档与契约

- **数值契约全部不变**：A 的 0-255、B 的 0-256 均不改；本次是运行行为向契约收敛；
- **已采纳并落地（2026-10-17，owner 接受）**：
  1. `entity-types.md` §2.4 更新路径 Body 参数表的"合法性条件"列原为 `-`，现补为
     `非必填；若传入，长度 0-255 字节（按 UTF-8 字节计）；不能包含控制字符；字段规则同 §1 数据模型`（引用性说明，不改变规范性内容）；
  2. 判定单位措辞明确为字节（按 §5.4 结论），共 11 处：
     - `entity-types.md`：§1 字段说明（第 19 行）、§2.1 Body 参数（第 43 行）—— `长度 0-255 字符` → `长度 0-255 字节（按 UTF-8 字节计）`；§2.4 Body 参数表（第 143 行）同措辞；
     - `entities.md`：§1 字段说明（第 48 行）、§2.1 Body 参数（第 81 行）与"约束"（第 95 行）、§2.4 约束（第 403 行）、§2.5 约束（第 456 行）—— 同一替换，共 5 处；
     - `clusters.md`：§1 字段说明（第 135 行）、§2.1"约束"（第 301 行）、§2.1 Body 参数（第 365 行）—— `长度 0-256 字符` → `长度 0-256 字节（按 UTF-8 字节计）`，共 3 处；
     - 措辞与 `00-common.md` §13 Password 既有的"…字节（…按 UTF-8 字节计）"表述保持一致；
- `design-docs/sys-design/数据库设计文档.md`：§6.4 `entities.description` 说明由"0-255 字符"改为"0-255 字节"；§6.3 `entity_types.description` 说明补注"接口写入上限 0-255 字节（列宽比契约多 1 位，无实际影响）"；§3.3 `clusters.description` 说明补注"接口写入上限 0-256 字节（列宽宽于契约，无实际影响）"（列宽均不动，见 §5.5）；
- **不改历史记录**：`design-docs/modifications/2026-09-21-add-entity-description/` 等既有变更存档保留原措辞，不回溯改写；
- 变更记录即本文档。

## 8. 遗留 / 待确认

1. ~~**判定单位（字节 vs 字符）**~~ → **已关闭（2026-10-17）**：owner 确认按字节（UTF-8 字节数）计，两域文档措辞已同步（见 §7）；替代路径 §5.4.1 仅在日后改判时启用；
2. **列宽可选收口（本期不做）**：若要把 `entity_types.description` 与契约对齐为 `VARCHAR(255)`，建议路径为
   （a）审计存量：`SELECT id, type_name, CHAR_LENGTH(description) FROM entity_types WHERE CHAR_LENGTH(description) > 255;`
   （b）人工确认后截断/改写超长行（不做静默截断，避免丢数据）；
   （c）`ALTER TABLE entity_types MODIFY COLUMN description VARCHAR(255) DEFAULT '' COMMENT '类型描述';`
   （d）按既有约定追加到 `database/db_ddl_upgrade_to_0.0.11.sql`（该脚本要求不含 DROP、破坏性步骤写入手动步骤章节），并同步更新 `db_ddl.sql` / `design-docs/sys-design/数据库设计文档.md` §6.3；
   Cluster 侧列宽 1024 宽于契约 0-256，收窄收益更低，不建议纳入；
3. **同类缺口的观察（未纳入本期）**：`certificates.md` §1/§2.1 要求 `description`"必填；长度 2-256 字符；不能包含控制字符"，而 `endpoints/openapi_v1/certificate/create.go:41` 只有 tag `validate:"required,min=2"`（**无 256 上限、无控制字符检查**），与 B 属同一形态的缺校验。本期按 owner 指定范围只并入 Cluster，证书域建议另开 issue 或在下一批一并处理；
4. **邻仓副本的同步情况（跨仓边界，本期未改）**：
   - `ai-gateway-web/design-docs/api-define/OpenAPI接口定义/{entity-types.md,entities.md}` 是本次改动文件的**逐字镜像**（改动前与本仓版本 byte-identical；`diff` 现有差异恰为本次替换的 3 处 / 5 处）⇒ 需要其 owner 用同措辞同步这两份，否则前端侧合同文本仍写"字符"；
   - `ai-gateway-web/.../clusters.md` **不是镜像**：与本仓版本已有 138 行历史差异（`epp_runtime`、`load_profile`/`affinity`、`normalize_upstream_error` 等前端侧尚未跟进的变更），本期不适用"逐字同步"，该文件的整体对齐属前端文档的独立任务；
   - 本仓不改动其他仓库的文件。

## 9. 实施状态

**已完成（2026-10-17）** —— 文档侧（§7）、代码侧（§5.1~§5.3）、测试（§6.1~§6.3）全部落地。

### 9.1 改动文件

代码（3 个）：

- `lib/validate/validate.go`：删除语义模糊的 `MaxDescriptionLength = 256`，改为按域命名 ——
  `MaxEntityTypeDescriptionLength = 255`（新）、`MaxClusterDescriptionLength = 256`（新），
  `MaxEntityDescriptionLength = 255` 保持；三个常量均注释文档出处；`Description()` 注释补充
  "长度按 UTF-8 字节计且与接口文档措辞一致"；
- `endpoints/openapi_v1/entity_type/create.go:56`、`update.go:68`：改用 `validate.MaxEntityTypeDescriptionLength`；
- `endpoints/openapi_v1/product_cluster/create.go`：`UpsertParam.Validate()` 在 `ClusterName` 之后新增
  `p.Description != nil` 分支，调用 `validate.Description(..., validate.MaxClusterDescriptionLength, "description")`
  —— 一处即覆盖 `POST /clusters` 与 `PATCH /clusters/{cluster_name}`（两者都经 `xreq.Bind*` → `validateData` → `Validate()`）。

测试（6 个）：

- `lib/validate/validate_test.go`：新增 `TestDescription`（255/256 边界、空串、控制字符、UTF-8 字节计数、常量钉住 6 组子用例）；
- `test/integration/tests/entity_type/update/update_test.go`：ET-4-004 收紧为 256 字节（保留 300 字节臂），新增 ET-4-005/006/007；
- `test/integration/tests/entity_type/create/create_test.go`：新增 ET-1-010/011（255 臂含 GET 回读）；
- `test/integration/tests/clusters/update/update_test.go`：新增 CL-4-024/025/026/027；
- `test/integration/tests/clusters/create/create_test.go`：新增 CL-1-045/046/047（含 GET 回读与报错信息断言）；
- `test/integration/tests/entity_type/design.md`（统计 18 → 23）、`test/integration/tests/clusters/design.md`（统计 49 → 56）：
  用例登记 + 详细设计 + 合法性条件补单位。

文档/其他：

- `design-docs/api-define/OpenAPI接口定义/entity-types.md`、`entities.md`、`clusters.md`（§7）；
- `design-docs/sys-design/接口层设计文档.md`：新增 §4.4.4「文本描述类字段（`description`）的校验约定」（Step 4 产出）；
- `design-docs/sys-design/数据库设计文档.md`：§6.3 / §6.4 / §3.3 的上限标注与单位措辞；
- `CHANGELOG.md`：`[Unreleased] > Fixed` 新增 3 条。

### 9.2 验证结果（环境无 `make`，按 Makefile target 等价命令执行）

| 项目 | 命令 | 结果 |
|------|------|------|
| 构建 | `go build ./...` / `go build -o ai-gateway-api.exe .` | 通过；二进制重建（41,293,824 字节） |
| 静态检查 | `go vet ./...` | 干净 |
| 主模块单测 | `go test ./... -count=1` | 全绿（无 FAIL 行） |
| 覆盖率门禁 | `go test -coverprofile=… ./model/...` | `total: (statements) 84.9%` ≥ 70%，通过 |
| 集成（本期两域） | `go test -count=1 ./tests/entity_type/... ./tests/clusters/...` | 全量 PASS |
| 集成（本期新用例） | `-v -run …` 逐条确认 | ET-4-004/005/006/007、ET-1-010/011、CL-4-024/025/026/027、CL-1-045/046/047 均 RUN 且 PASS |
| 集成（响应契约回归） | `go test -count=1 ./tests/schema/...` | openapi / innerapi / report 全量 PASS |

model 覆盖率 84.9% 与改动前口径一致（本次未触碰 `model/`）。

### 9.3 靶向复现（§6.4 第 4 项）

由新增集成用例经真实 HTTP 覆盖，即 issue 的复现步骤：

- A：PATCH/POST 256 字节 `description` → **422**（修复前 200 且落库）；255 字节 → 200 且 GET 回读一致；含 `\x01` → 422；
- B：PATCH/POST 257 字节 `description` → **422**（修复前 200 且落库）；256 字节 → 200 且 GET 回读一致；含 `\x01` → **422**（修复前 200）；
- 两域"被拒后 GET 回读值不变"（原子性）均已断言。

反向对照（说明新用例确实钉住了缺陷）：修复前 A 的两条路径分别返回 200（issue 三条 TC 的失败摘要与 2026-10-14 replay 逐字一致），B 返回 200（`SC2101-TC024` 失败摘要 `length 257 code=200`）；新用例在修复后断言 422 并通过。

TC 侧收口（不在本仓验证范围）：`SC1203-TC011` / `TC015` / `TC017` 与 `SC2101-TC024` 的对应断言需由 TC 测试方在其环境复跑确认。

### 9.4 Step 6 沉淀评估

**不新增 `sys-design/details/` 文档。** 本次属"校验边界取错 + 校验缺失"的收敛修复，无新机制、无复杂权衡需要长篇沉淀；可复用的约定已写入 `sys-design/接口层设计文档.md` §4.4.4（description 字段族的常量命名、字节口径、判定方向、接入位置与文档同步要求），并以 `lib/validate` 常量注释 + §8 遗留条目构成双向锚点。`sys-design/summary.md` 索引无需变更（未新增文档）。

### 9.5 全量集成套件：一处既有并发抖动（与本改动无关）

- 标准命令 `go test -count=1 ./tests/...` 默认**并行**执行各包（`-p = GOMAXPROCS`）。该模式下全量运行出现 **1 个与本改动无关的失败**：
  `tests/quota_period_reset` → `TestQuotaResetNullGuard_LegacyNullClaimAndIdempotency/QR-4-003`
  （`POST /inner-api/v1/quota/trigger-reset` 未重置 `last_reset_at IS NULL` 的计划：Redis 剩余量仍为 100000、`last_reset_at` 仍为 NULL，两条断言均失败）。
- 判定为**既有并发抖动、非本改动引入**，依据有三：
  1. 同一二进制（含本改动）**单包运行**该用例通过：`go test -count=1 -run TestQuotaResetNullGuard ./tests/quota_period_reset/` → `ok`（144s）；
  2. 本改动 diff 仅涉及 `description` 校验（`lib/validate` 常量、`entity_type` 两个调用点、`product_cluster.UpsertParam.Validate()`），与配额重置调度、`reset_period` 认领、`last_reset_at` 回填无任何调用关系；
  3. 失败点位于 `model/quota` 的触发重置路径，该路径的并行敏感性（定时器 + Redis 分布式锁）是既有特性。
- 为取得全量绿灯证据，另以 `-p 1`（串行，等价于 README 推荐的 `scripts/run_module_tests.sh` 逐模块运行方式）重跑全量套件 → **exit 0，`./tests/...` 全部 ok（含 `tests/quota_period_reset` → ok 139.981s）**。即本改动在串行全量集成套件下无任何回归。
- 建议（不在本卡范围）：把依赖定时器/分布式锁的用例（如 `quota_period_reset`）在并行模式下隔离（CI 加 `-p 1` 或对该包强制串行），避免出现与缺陷无关的 CI 红灯。