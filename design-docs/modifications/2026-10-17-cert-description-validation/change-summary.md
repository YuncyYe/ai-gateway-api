# `/certificates` `description` 校验缺口修复方案（契约必填 2-256 / 禁控制字符，实现仅有 `required,min=2`）

本卡为 issue #244 根因排查中识别出的**同族第 3 处缺口**，经 owner 指定单独出方案：

- **已修复（A）** `/entity-types`：上限常量取错（256 vs 契约 255）；
- **已修复（B）** `/clusters`：`description` 完全无校验；
- **本卡（C）** `/certificates`：`description` 只有 `required,min=2`，**缺长度上限与控制字符校验**。

A/B 的方案与实施记录见 `design-docs/modifications/2026-10-17-issue-244-description-length-validation/`；本卡最初登记在该文档 §8 遗留第 3 项。

## 1. 问题来源

本卡**无对应外部 issue 或 TC**，来源是对 `description` 字段族的横向排查（A 的根因分析顺带发现的同族缺口）。

产品条款依据（Oracle）：`design-docs/api-define/OpenAPI接口定义/certificates.md`

- §1 数据模型字段表（第 21 行）：`description` —— 必填；**长度 2-256**；不能包含控制字符；
- §2.1 创建证书 Body 参数表（第 54 行）与"约束"（第 61 行）：同一字段、同一合法性条件；
- §2.4 更新证书为默认证书（第 183 行起）：Body 只取 URI 的 `cert_name`，**不接受 `description`**；§2.5 删除证书（第 219 行起）同理。

⇒ `description` 在 OpenAPI 上的**唯一写入路径是 `POST /open-api/v1/certificates`**（`endpoints/openapi_v1/certificate/endpoints.go` 的 5 个端点中，只有 create 携带该字段；`update`/`one`/`all`/`delete` 均只按 `cert_name` 定位）。

## 2. 根因（源码直读核实，2026-10-17）

1. `endpoints/openapi_v1/certificate/create.go:41`：`Description *string \`json:"description" validate:"required,min=2"\`` ——
   只有 tag 级 `required,min=2`，**没有 max 上限、没有控制字符检查**；`CreateParam` 也**未实现** `Validate()`（`xreq.Validator`，grep 该包零命中）；
2. `CreateAction`（同文件 80-90 行）在 bind 之后只做 `validate.CertName(*param.Name)`（86-88 行），
   `description` 原样进入 `createActionProcess` → `container.CertificateManager.CreateCertificate`（56-62 行 `Description: param.Description`）；
3. `model/iprotocol/certificate.go`（`CertificateParam.Description`，第 54 行；`CreateCertificate` 第 201-264 行）与
   `storage/rdb/protocol/certificate.go:49-57`（`certificateParamI2D(pp)` 直插）**均无 description 校验或清洗**；
4. `lib/validate` 中不存在证书描述校验函数或常量：全仓 `MaxCertificateDescriptionLength` / `CertDescription` **零命中** ——
   本仓唯一的 description 校验器 `validate.Description(s, maxLen, name)`（`lib/validate/validate.go:344-348`）未被 cert 域使用；
5. 列宽不构成兜底：`certificates.description varchar(1024) NOT NULL DEFAULT 'no desc'`（`db_ddl.sql:228`；SQLite 为 `TEXT`）⇒ 任意长度都能落库；
6. 因果结果：`POST /open-api/v1/certificates` 提交 257 字节、或含控制字符（如 `\x01`）的 `description`，
   按当前实现会被接受（HTTP 200 / `ErrNum=200`）并落库。
   **证据等级：源码直读（链路 1~5）+ 运行时实测（第 6 条，2026-10-17 红用例实测升级）**：
   在未修代码 + 当前二进制的真实环境下，`CERT-1-015`（257 字节）与 `CERT-1-016`（含 `\x01`）
   均返回 `ErrNum=200` **且证书已创建**（紧接着的 `GET /certificates/{cert_name}` 返回 200）；见 §8.2。

同族对照（说明本卡确实是孤立缺口，不是设计选择）：

| 域 | 契约上限 | 实现现状 | 结论 |
|----|----------|----------|------|
| `/entities` | 0-255 | `MaxEntityDescriptionLength = 255` + `validate.Description` | 正确 |
| `/entity-types` | 0-255 | 原 `MaxDescriptionLength = 256`（取错） | 已修（A） |
| `/clusters` | 0-256 | 原无任何校验 | 已修（B） |
| `/api-keys` | ≤512（必填） | `MaxAPIDescriptionLength = 512` + `validate.Description` | 正确 |
| `/providers` | 0-256 | `MaxProviderDescriptionLength = 256`（本地实现） | 正确 |
| `/certificates` | 必填 2-256 | 仅 tag `required,min=2` | **本卡** |

## 3. 影响面

| 项目 | 结论 |
|------|------|
| 接口范围 | 仅 `POST /open-api/v1/certificates`（`description` 的唯一写入路径） |
| 契约上限 | 必填、长度 2-256（单位按 §5.3 统一为字节） |
| 修复前错误形态 | >256 字节被接受（200）并落库；含控制字符被接受（200）并落库 |
| 修复后错误形态 | >256 字节 → 422；含控制字符 → 422；长度 1 / 空串 / 未携带 → 422（现状不变，tag 拦截） |
| 调用方 | 控制台证书页、自动化建证书脚本、集成测试夹具（`testutil.CreateCertificate`） |
| 数据/配置 | 无 DDL 变更、无配置变更、无数据迁移（理由见 §5.4） |
| 数据面 | 无 BFE / conf-agent 联动：已核 `model/iversion_control/` 的证书导出不含 `description`（下发的是证书/密钥文件路径与内容） |

风险性质与 A/B 一致：文档契约与实现边界不一致；按文档做客户端校验（≤256）的调用方与实现（无上限）不匹配；
超出文档上限的描述能落库，使按文档假设预留展示宽度的下游出现排版异常。无数据损坏、无越权。

## 4. 目标

1. `description` 长度 **≥257 字节** 在创建路径被拒绝（422），且**不落库、不产生副作用**（不建证书记录、不切换默认证书）；
2. `description` **含控制字符** 被拒绝（422），同样零副作用；
3. `description` **恰好 256 字节**与普通描述仍创建成功（200），并可通过 `GET` 回读一致（合法边界回归锁）；
4. 长度 1、空串、未携带三条既有拒绝路径**行为不变**（422，由 tag `min=2` / `required` 承担）；
5. 错误码沿用 422（参数错误），不新增错误码、不新增包装层；
6. 契约与实现边界一致，且长度单位措辞在两份文档中与实现口径一致。

## 5. 最终方案

契约是已发布的对外合同，故**修实现向契约收敛**，不放宽 "2-256"、不改"必填"。

### 5.1 新增上限常量（`lib/validate/validate.go`）

```go
// MaxCertificateDescriptionLength 是 /certificates 的 description 上限，
// 依据 design-docs/api-define/OpenAPI接口定义/certificates.md §1/§2.1：2-256。
MaxCertificateDescriptionLength = 256
```

命名与注释沿用 A/B 确定的约定（`sys-design/接口层设计文档.md` §4.4.4：**上限常量按域命名并写明文档出处**），
不引入通用名，避免再次出现"常量取错"。

### 5.2 接入校验：与既有 `validate.CertName` 并列（推荐）

`endpoints/openapi_v1/certificate/create.go` 的 `CreateAction`，在 `validate.CertName(*param.Name)`（86-88 行）之后追加：

```go
if err := validate.Description(*param.Description, validate.MaxCertificateDescriptionLength, "description"); err != nil {
    return nil, err
}
```

要点：

- **责任划分**：`required`（必填）与 `min=2` 继续由 tag 承担；`validate.Description` 补齐 **max 上限 + 禁控制字符**（复用既有实现，不新写校验逻辑）；
- **零副作用**：该校验位于 `createActionProcess`（55 行起）之前，被拒时不会触达 `CreateCertificate`，
  既不落库、也不会触发"新证书设为默认 → 旧默认降级"的连带写；
- **错误码**：`validate.Description` 返回 `WrapParamErrorWithMsg` ⇒ `ErrNum=422`，与 tag 违规同一形态。

**备选（未采纳）**：让 `CreateParam` 实现 `Validate() error`（`xreq.Validator`），由 `xreq.BindJSON` 在 tag 校验后统一调用。
两者都在写路径之前、错误码相同；不采纳的原因是本包 as-built 风格是 handler 内集中校验（`validate.CertName` 就在此处），
采用并列写法只加 1 条语句、且该端点的集中校验集中在同一处，便于阅读。

**随附文档校准（必须同批做）**：`sys-design/接口层设计文档.md` §4.4.4 第 4 条当前只写了"结构体实现 `Validate()` 由 `xreq.Bind*` 调用"这一接入位置；
需补一句"也允许在 handler 内与其它集中校验并列调用（如 `/certificates` 的 `validate.CertName`/`validate.Description`），
**前提是位于写路径之前**"，否则文档与该节将与本卡实现不一致。

### 5.3 判定单位：字节（沿用 A/B 已确认口径）

- 契约措辞"长度 2-256 字符"明确为"**长度 2-256 字节（按 UTF-8 字节计）**"，共 2 处：
  `certificates.md` §1 字段表（第 21 行）、§2.1 Body 参数表（第 54 行）；
- 与 tag `min=2` 的关系：go-playground 的 `min` 对字符串按 **rune** 计数，集中校验的 max 按**字节**计数。
  两者单位不同但**互不矛盾**（`bytes ≥ runes`，不存在"满足 min 必然违反 max"的不可满足区间）；
  是否把 min 一并改为集中校验（做到单位完全统一）属独立小改，**不在本卡范围**；
- 影响示例：86 个汉字 = 258 字节 > 256 → 被拒（按"字符"字面本应通过）；偏差方向与 A/B 一致，口径一致。

### 5.4 DDL：不改

`certificates.description varchar(1024) NOT NULL DEFAULT 'no desc'`（`db_ddl.sql:228`）保持不动。理由同 A/B：

1. 校验收紧后，列宽宽于契约不构成缺陷（写入路径已封住）；
2. 仓库既有惯例就是"列宽宽于契约"（`providers`/`clusters`/`certificates` 均 `varchar(1024)`，契约 0-256）；
3. SQLite 部署该列本是 `TEXT`，**参数校验是唯一收口点**。

### 5.5 行为变化表

| 场景 | 修复前 | 修复后 |
|------|--------|--------|
| `description` ≥257 字节 | HTTP 200 / `ErrNum=200`，且落库 | **422 参数非法，不落库、无副作用** |
| `description` 恰好 256 字节 | 200 | 200（**合法边界，不变**） |
| `description` 含控制字符（`\x01`、`\n`、`\x7f`） | **200，落库** | **422**（新增拦截，按 §1"不能包含控制字符"） |
| `description` 长度 1 / 空串 / 未携带 | 422 | 422（不变，tag: `min=2` / `required`） |
| 报错文案 | — | `description length must be <= 256` / `string contains control characters` |
| `cert_name` 校验、PATCH default、GET、DELETE | — | 全部不变 |

### 5.6 不改的东西

- `validate.CertName`（2-64 字符、字符集、首尾约束，Issue #133）不变；
- `PATCH /certificates/{cert_name}/default`、`GET` 列表/详情、`DELETE` 不变（均不携带 `description`）；
- 响应视图 `OneData`（定义在 `endpoints/openapi_v1/certificate/delete.go:35-52`，create/one/update 共用）不变；
- `model/iprotocol` 与 `storage/rdb` 不改（校验在接口层收口，与 A/B 同一分层判断）；
- 无 DDL / 配置 / 数据面变更。

## 6. 测试

### 6.1 单元测试（`lib/validate/validate_test.go`）

在既有 `TestDescription` 中增补：

- 子用例"certificate boundary is 256"：`Description(strings.Repeat("a", 256), MaxCertificateDescriptionLength, "description")` 无错；
  `strings.Repeat("a", 257)` 报错；
- "limit constants are pinned" 子用例增补 `assert.Equal(t, 256, MaxCertificateDescriptionLength, "certificates.md §1/§2.1: 必填 2-256")`。

### 6.2 集成测试（`test/integration/tests/certificate/create/create_test.go`）

沿用该文件既有的 `t.Run` 风格（现有 CERT-1-001~013）：

- **CERT-1-014** `description` 恰好 **256 字节** → 200，且 `GET /certificates/{cert_name}` 回读一致；
- **CERT-1-015** `description` **257 字节** → 422；
- **CERT-1-016** `description` 含 `\x01` → 422；
- **CERT-1-017** `description` 长度 1 → 422（pin 住 tag `min=2`；当前无用例覆盖该边界，属顺手补齐）；
- 拒绝臂（015/016）追加**零副作用断言**：`GET /certificates/{cert_name}` → 404（证书未被创建）。

`test/integration/tests/certificate/design.md` 同步：

- §3 统计：创建 13 → 17、合计 21 → 25；
- §6.3 创建用例表增补 4 行；
- 第 74 行参数表 `description` 合法性条件补单位：`必填；长度 2-256 字节（按 UTF-8 字节计）；不能包含控制字符`。

### 6.3 文档与契约

- `certificates.md` §1（第 21 行）/ §2.1（第 54 行）：`长度 2-256 字符` → `长度 2-256 字节（按 UTF-8 字节计）`（数值与"必填""禁控制字符"不变）；
- `sys-design/接口层设计文档.md` §4.4.4：常量表增补一行
  `| /certificates | validate.MaxCertificateDescriptionLength | 256（必填、≥2） | certificates.md §1/§2.1 |`，
  并按 §5.2 补"handler 内集中校验"这一合法接入位置；
- `sys-design/数据库设计文档.md` §5.1（第 810 行字段表）`certificates.description` 说明补注
  `接口写入上限 2-256 字节（列宽宽于契约，无实际影响）`，与 §6.3/§6.4/§3.3 的同类标注口径一致；
- `CHANGELOG.md` `[Unreleased] > Fixed` 增一条（与 A/B 同批风格）；
- 变更记录即本文档。

### 6.4 回归验证

1. **先跑红用例**：在未修改代码（仅含 A/B 修复）的二进制上执行 CERT-1-014~016，确认
   257 字节与控制字符当前返回 **200**（把 §2 第 6 条的"源码直读推断"升级为运行时实锤）；
2. `go build ./...`、`go vet ./...`、`go test ./...` 全绿；
3. 覆盖率门禁：`go test -coverprofile=… ./model/...` ≥ 70%（本卡不动 `model/`，预期与 84.9% 持平）；
4. 重建 `ai-gateway-api.exe` 后跑 `test/integration/tests/certificate/...` 与 `tests/schema/...`，全量 PASS；
5. 靶向复现：257 字节 → 422、控制字符 → 422、256 字节 → 200 且回读一致；
   被拒请求后 `GET` 该 `cert_name` → 404（未创建，零副作用）。

## 7. 遗留 / 待确认

1. **`min=2` 与 max 的单位不完全统一**（tag 按 rune、集中校验按字节）：逻辑上互不矛盾，本卡不做统一；
   若 owner 决定统一为字节口径，需把 `min` 也改为集中校验（`lib/validate` 提供 `Description` 之外的 min 逻辑），并同步 `certificates.md` 措辞；
2. **列宽收窄仍不做**：`certificates.description varchar(1024)` 与契约 2-256 的差异保持（同 A/B §8 第 2 项判断）；
3. **同族字段的横向清查已覆盖**：`/entities`、`/entity-types`、`/clusters`、`/api-keys`、`/providers`、`/certificates` 六个 description 域已逐一核对；
   其余含 `description` 的接口（`/products`、`/route-tables`、`/subclusters` 等，见 `endpoints/openapi_v1/*/`）在 `api-define` 中**未声明该字段的长度/控制字符约束**，
   故不构成本族缺口；若后续为其补充契约约束，应同步补校验并登记；
4. **邻仓副本**：`ai-gateway-web/design-docs/api-define/OpenAPI接口定义/certificates.md` 若与本仓同源，需其 owner 同步本卡 §6.3 的措辞（跨仓边界，本仓不改动其他仓库文件）。

## 8. 实施状态

**已完成（2026-10-17）** —— 代码、测试、文档（Step 3/4）全部落地，采用"先红后绿"流程。

### 8.1 改动文件

代码（2 个）：

- `lib/validate/validate.go`：常量块新增
  `MaxCertificateDescriptionLength = 256`（注释写明文档出处，并注明下限由 tag 承担）；
- `endpoints/openapi_v1/certificate/create.go`：`CreateAction` 在 `validate.CertName` 之后新增
  `validate.Description(*param.Description, validate.MaxCertificateDescriptionLength, "description")`
  （§5.2 推荐的 handler 内集中校验方式；位于 `createActionProcess` 之前 ⇒ 拒绝即零副作用）。

测试（3 个）：

- `lib/validate/validate_test.go`：`TestDescription` 新增"certificate boundary is 256"子用例，
  空串/控制字符子用例与"常量钉住"子用例补入证书常量；
- `test/integration/tests/certificate/create/create_test.go`：新增 `createWithDescription` /
  `assertCertNotCreated` 两个局部 helper 与 **CERT-1-014 ~ CERT-1-017** 四个用例，并登记 cleanup；
- `test/integration/tests/certificate/design.md`：§3 统计 `21 → 25`（创建 `13 → 17`）、§6.3 用例表 +4 行、
  §6.4 新增 6.4.14 详细设计、§6.2 参数表 `description` 合法性条件补单位。

文档（3 个）：

- `design-docs/api-define/OpenAPI接口定义/certificates.md`：§1（第 21 行）/ §2.1（第 54 行）
  `长度 2-256 字符` → `长度 2-256 字节（按 UTF-8 字节计）`（数值、"必填"、"禁控制字符"均不变）；
- `design-docs/sys-design/接口层设计文档.md` §4.4.4：常量表新增 `/certificates` 行；
  第 4 条改写为**两种**合法接入位置（`Validate()` 钩子 / handler 内集中校验），
  新增第 5 条"必填与下限由 tag 承担、上限与控制字符由集中校验承担"（含 rune/字节单位说明）；
- `design-docs/sys-design/数据库设计文档.md` §5.1：`certificates.description` 说明补注
  `接口写入上限 2-256 字节（必填，列宽宽于契约，无实际影响）`。

其他：

- `CHANGELOG.md` `[Unreleased] > Fixed` 新增 1 条（引用本变更目录）。

### 8.2 红 → 绿实测（本卡最强证据）

**红（未修代码 + 当前二进制，真实环境）**：先写用例再跑，

```
--- FAIL: TestCertificate_Create/CERT-1-015_description_257_字节被拒绝且未创建
    create_test.go:200: expected ErrNum=422, got ErrNum=200, ErrMsg=success
    create_test.go:201: expected ErrNum=404, got ErrNum=200, ErrMsg=success
--- FAIL: TestCertificate_Create/CERT-1-016_description_含控制字符被拒绝且未创建
    create_test.go:207: expected ErrNum=422, got ErrNum=200, ErrMsg=success
    create_test.go:208: expected ErrNum=404, got ErrNum=200, ErrMsg=success
--- PASS: CERT-1-014 (256 字节合法边界) / CERT-1-017 (长度 1，tag min=2)
```

⇒ §2 第 6 条由"源码直读推断"升级为**运行时实锤**：257 字节与含 `\x01` 的描述在修复前返回 200
**且证书已创建**（`GET` 该 `cert_name` 返回 200 而非 404）。

**绿（修复后重建二进制）**：CERT-1-014 ~ CERT-1-017 全部 PASS —— 256 字节创建成功且 POST/GET 回读一致；
257 字节、控制字符、长度 1 均 422 且 `GET` → 404（零副作用）。

### 8.3 验证结果（环境无 `make`，按 Makefile target 等价命令执行）

| 项目 | 命令 | 结果 |
|------|------|------|
| 构建 | `go build ./...` / `go build -o ai-gateway-api.exe .` | 通过；二进制重建后重跑集成 |
| 静态检查 | `go vet ./...` | 干净 |
| 单测（lib/validate） | `go test ./lib/validate/... -run TestDescription` | ok |
| 集成（证书域 + 响应契约） | `go test -count=1 ./tests/certificate/... ./tests/schema/...` | 全部 ok（certificate 6 个包 + schema 3 个包） |
| 主模块全量单测 | `go test ./... -count=1` | `main_exit=0`，FAIL 行 0（全绿，含 `endpoints/openapi_v1/certificate`） |
| 覆盖率门禁 | `go test -coverprofile=… ./model/...` | `total: (statements) 84.9%` ≥ 70%，通过（与 A/B 阶段一致，本卡未动 `model/`） |
| 全量集成（串行） | `go test -count=1 -p 1 ./tests/...` | exit 0，全部 ok（含 `tests/certificate/*` 6 包、`tests/schema/*` 3 包、`quota_period_reset` ok 136.8s） |

未跑默认并行全量集成（`-p = GOMAXPROCS`）：该方法在 A/B 阶段已观察到与业务无关的并发抖动
（`quota_period_reset`，见 A/B 文档 §9.5），故沿用串行口径取得全量绿灯。

靶向复现（§6.4 第 5 项）：257 字节 → 422；控制字符 → 422；256 字节 → 200 且回读一致；
被拒请求后 `GET /certificates/{cert_name}` → 404（未创建）。全部由 CERT-1-014 ~ 017 经真实 HTTP 覆盖。

### 8.4 Step 6 沉淀评估

**不新增 `sys-design/details/` 文档。** 本卡与 A/B 同属"校验边界/缺校验"的收敛修复，
可复用约定已由 A/B 沉淀到 `sys-design/接口层设计文档.md` §4.4.4；本卡对其做了两处增强
（新增 `/certificates` 行、把"接入位置"扩展为 `Validate()` 钩子与 handler 内集中校验两种，
并明确"tag 管必填/下限、集中校验管上限/控制字符"的分层），`summary.md` 索引无需变更。

至此 `description` 字段族（`/entities`、`/entity-types`、`/clusters`、`/api-keys`、`/providers`、`/certificates`）
六个域的"文档约束 ↔ 常量 ↔ 校验 ↔ 测试"链路已全部对齐并各自有用例钉住上限两侧。