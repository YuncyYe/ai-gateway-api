# Issue #226：内置 admin 无法经 API 修改密码（保留名校验误伤按名更新端点）修复方案

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/226](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/226)

> 对内置管理员账号修改密码：
>
> ```
> PATCH /auth/users/admin/passwd {"old_password": "...", "password": "..."}
> → {"ErrNum":422,"ErrMsg":"参数非法: user_name \"admin\" is reserved"}
> ```
>
> 无论 `old_password` 是否正确、是否管理员代改，请求均被参数校验拦截在进入业务逻辑之前，100% 可复现。同族端点 `PATCH /auth/users/{user_name}/is_admin` 存在同一缺陷。

影响：

- 内置 admin 密码无法经管理面 API rotation；旧密码泄露后的标准处置路径不可用，只能直连 DB 改 bcrypt 字段。
- admin 的 `is_admin` 属性无法经 API 调整（次要，同根因）。
- 与 OpenAPI 契约冲突（详见 §5.3：类型定义经端点章节引用，间接把保留名约束套到了更新/查询端点上）。

复核补充的两个关联问题（一并纳入本期修复，依据见 §5.2 / §5.3）：

- `UserUpdatePasswordParam.Password` 的 tag `validate:"required,min=6"`、`UserCreateParam.Password` 的 tag `validate:"required,min=1"` 均宽松于公共类型文档 `00-common.md` §13 对 Password"长度 8-72 字节"的要求（集中校验 `validate.Password()` 兜底，实际行为未越出文档，但 tag 与文档/集中校验双层口径不一致）。
- `00-common.md` §12 UserName 类型的保留名约束未区分"创建"与"引用"场景，经 `auth.md` 各端点章节的类型引用蔓延到更新/查询端点的文档契约上，与代码误接同源。

## 2. 根因

提交 `b8a8a93`（2026-07-30, "feat: enforce OpenAPI param validation and add unit/integration tests"）把 `validate.UserName()` 从仅创建场景一并接入了**按名字定位已有用户**的更新端点：

1. `lib/validate/validate.go:62-66`：保留名表 `reservedUserNames = {"admin", "root", "system"}`；`validate.go:204-206`：`UserName()` 命中即返回 `user_name %q is reserved`（422）。
2. `endpoints/openapi_v1/auth/user_update_password.go:45-50`：本次提交**新增** `Validate()` 并调用 `validate.UserName(*p.UserName)` —— 提交前该端点仅有 tag 级 `required,min=1` 校验，可以正常修改 admin 密码（`git show b8a8a93` 与父提交对比可证）。
3. `endpoints/openapi_v1/auth/user_update_is_admin.go:44-49`：同样问题，`is_admin` 亦无法 PATCH。
4. `endpoints/openapi_v1/auth/user_create.go:36` 使用完整 `UserName()` 校验是**正确**的（防止新建账号冒用系统名），应保留。

关键矛盾：`admin` 是 DB 种子内置账号（`db_ddl.sql:635` / `db_ddl_sqlite.sql:673` 以 id=1 插入 bcrypt 哈希），**无法**经 create API 创建（create 端点拒绝 reserved 正是为此，集成用例 AUTH-1-007 固化了该行为）。因此 reserved 名中唯一真实存在的账号，恰恰是唯一必须可操作该端点的账号 —— 校验语义与端点用途直接冲突。

旁证：`user_delete.go` / `user_one.go` 等同为"按名定位已有用户"的端点**没有**接入 `validate.UserName()`（至今仍只有 tag 级校验），说明 reserved 检查泄漏进更新端点并非有意设计。

Password tag 问题的根因：tag 为集中校验（`Validate()` 接口）接入之前的历史遗留，b8a8a93 引入集中校验后 tag 未同步对齐，形成"tag 宽松先行、集中校验兜底"的双层口径。

## 3. 目标

1. `PATCH /auth/users/{user_name}/passwd` 与 `PATCH /auth/users/{user_name}/is_admin` 恢复对内置 `admin`（及一切已存在用户）的可操作性，与 `b8a8a93` 之前的行为一致；
2. 保留名校验在**创建用户**场景继续生效（行为不变，AUTH-1-007 依旧 422）；
3. 更新端点仍保留格式校验（长度/字符集/首尾字符），不退化为纯 tag 级校验；
4. 通过命名与注释让"创建名校验"与"引用名校验"的语义边界对未来开发者显式可见，避免同类误接；
5. Password 相关 tag 与公共类型文档（`00-common.md` §13，8-72 字节）对齐，消除双层口径（ErrCode 422 行为不变，仅拦截层与报错文案统一）；
6. 修正接口文档：UserName 类型的保留名约束明确限定为创建场景，更新/查询端点文档契约与修复后行为一致，并修复 `auth.md` 的悬空锚点。

## 4. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api` |
| 主要文件 | `lib/validate/validate.go`（拆分校验函数）；`endpoints/openapi_v1/auth/user_update_password.go`、`user_update_is_admin.go`（换用引用名校验）；`endpoints/openapi_v1/auth/user_update_password.go`、`user_create.go`（Password tag 对齐）；`design-docs/api-define/OpenAPI接口定义/00-common.md`、`auth.md`（文档修正） |
| 接口契约 | 请求/响应结构不变；"更新端点拒绝 reserved 用户"这一 `b8a8a93` 引入的错误行为被移除；Password 长度口径统一为文档定义的 8-72 字节（行为不变，仅拦截层上移至 tag）；UserName 类型的保留名约束明确限定创建场景 |
| 不在范围 | `user_delete` / `user_one` 维持现状（本就不拒绝 reserved，模型层的删除保护逻辑不变）；不引入"禁止删除/降级内置 admin"的新产品语义 |
| 数据迁移 | 无 |

## 5. 最终方案

### 5.1 校验函数拆分与端点换用

核心思路：把 `UserName()` 拆成"格式校验"与"保留名检查"两层，新增仅做格式校验的 `UserNameRef()` 供"按名引用已有用户"的端点使用；创建端点继续用完整 `UserName()`。

`lib/validate/validate.go`：

```go
// userNameFormat 校验 user_name 的通用格式：长度 1..64、字符集 [a-zA-Z0-9_.-]、
// 首尾不得为 '.', '-', '_'。不检查保留名。
func userNameFormat(s string) error {
    if err := validateName(s, 1, MaxUserNameLength, "user_name"); err != nil {
        return err
    }
    if err := validateNamePattern(s, nameToken, "user_name"); err != nil {
        return err
    }
    return validateNameEdges(s, "user_name")
}

// UserName 校验新建用户的 user_name：格式 + 保留名（admin/root/system）。
// 仅用于创建类端点，防止新建账号冒用系统内置身份。
func UserName(s string) error {
    if err := userNameFormat(s); err != nil {
        return err
    }
    if reservedUserNames[strings.ToLower(s)] {
        return xerror.WrapParamErrorWithMsg("user_name %q is reserved", s)
    }
    return nil
}

// UserNameRef 校验引用已有用户的 user_name（按名定位的更新/查询端点）：
// 仅格式校验，不拒绝保留名 —— 内置账号 admin 恰是必须通过此类端点
// 修改密码的账号。禁止在本函数中追加保留名检查。
func UserNameRef(s string) error {
    return userNameFormat(s)
}
```

调用点改动（各一行）：

- `endpoints/openapi_v1/auth/user_update_password.go:46`：`validate.UserName(...)` → `validate.UserNameRef(...)`；
- `endpoints/openapi_v1/auth/user_update_is_admin.go:45`：`validate.UserName(...)` → `validate.UserNameRef(...)`；
- `endpoints/openapi_v1/auth/user_create.go:36`：保持 `validate.UserName(...)` 不变。

安全面分析：

- 保留名检查的目的（防冒用系统身份创建账号）由 create 端点完整保住，更新端点换用 `UserNameRef` 不会产生新的创建路径；
- 更新端点按名定位后走原有业务逻辑：不存在用户返回 404（`user_update_is_admin.go` 显式查库、`UpdateUserPassword` 内部校验），权限由 endpoint Authorizer（`FeatureUser/ActionUpdate`，需管理员）与"自助改密必须提供 old_password"逻辑（`user_update_password.go:65-69`）控制，均不受本次改动影响；
- `is_admin` 端点仅支持置 `true`（`validate.IsAdmin` + action 内双重校验），修复后 admin 被 PATCH `is_admin=true` 为幂等无害操作，与 `b8a8a93` 之前行为一致。

### 5.2 Password tag 与公共类型文档对齐

依据：`00-common.md` §13 用户密码（Password）要求**长度 8-72 字节**（bcrypt 上限，按 UTF-8 字节计）；`lib/validate/validate.go` 的 `Password()`（8-72 字节）与 `model/iauth/authentication.go:620-624` 的存储层 `passwordCheck` 均与文档一致；宽松的是 tag。

改动（两行 tag）：

- `endpoints/openapi_v1/auth/user_update_password.go:41`：`validate:"required,min=6"` → `validate:"required,min=8"`；
- `endpoints/openapi_v1/auth/user_create.go:30`：`validate:"required,min=1"` → `validate:"required,min=8"`。

行为变化说明（对调用方透明）：

- 短密码仍在 xreq.Bind 的 tag 校验阶段被拦截，ErrNum 恒为 422，仅报错文案变化：原 6-7 字节密码漏过 tag 后由 `Password()` 报 `password length must be between 8 and 72 bytes`；对齐后由 tag 直接拦截，文案为 go-playground/validator 的中文翻译（形如"password 长度必须至少为 8 个字符"，经 `xerror.WrapParamErrorWithMsg` 包装为"参数非法: …"）。
- 8-72 字节的合法密码两阶段均放行，无变化。

### 5.3 接口文档修正

`00-common.md` §12 用户名（UserName）现行合法性条件为：长度 1-64 字符；字符集 `[a-zA-Z0-9_.-]`；首尾不得为 `.`/`-`/`_`；全局唯一（大小写不敏感）；**不能为 `admin`、`root`、`system` 等保留用户名**。`auth.md` §2.3（重置密码）、§2.5（设置管理员）、§2.12（查询单个用户）的 `user_name` 均声明"类型为 [UserName]"——即文档经由类型引用把保留名约束间接套到了更新/查询端点上，与代码 `b8a8a93` 的误接是同源语义错误。

修正（两处）：

1. `00-common.md` §12：保留名约束改写为"**创建用户**时不能为保留用户名（`admin`/`root`/`system`）；按名引用已有用户的端点（重置密码、设置管理员、查询用户等）不受此限制，允许操作系统内置账号"——类型定义作为唯一事实源，端点章节（`auth.md` §2.3/§2.5/§2.12）经由"类型为 [UserName]"引用自然继承，不再逐端点重复标注；
2. `auth.md` 中 `[UserName](#12-用户名username)`（§2.1/§2.2/§2.3）、`[Password](#13-用户密码password)`（§2.1/§2.3）、`[TokenName](#14-token-名称tokenname)`（§2.8）的锚点指向本文档内不存在的 §1.2/§1.3/§1.4 小节（实际定义在同目录 `00-common.md` §12/§13/§14，跨文件锚点无法跳转），改为相对路径链接 `00-common.md#12-用户名username` 等形式。

## 6. 测试

### 6.1 单元测试

`lib/validate/validate_test.go`：

- 新增 `TestUserNameRef`：`admin` / `root` / `system`（含大小写变体如 `Admin`）返回 **nil**；`-user`、`user.`、`user name`、空串、超长串仍返回 error；
- `TestUserName` 保持现状（reserved 拒绝），可补 `root`/`system` 用例。

`endpoints/openapi_v1/auth/validator_test.go`：

- `TestUserUpdatePasswordParamValidate`：
  - 原 case `"invalid user name"` 使用的 `admin` 是**合法引用**，改为期望无错，并更名如 `"reserved user name is allowed"`；
  - 新增真正的格式非法 case（如 `-user`）期望报错；
  - 密码类 case（过短、等于用户名）不变。
- `TestUserUpdateIsAdminParamValidate`：同上调整（`-user` 报错；`admin` 放行；`is_admin=false` 仍报错）。

注：tag 级校验不在 `validator_test.go` 覆盖范围（该文件直接调用 `Validate()`，不经 `xreq.Bind`），§5.2 的 tag 对齐由集成测试经真实 HTTP 路径覆盖。

### 6.2 集成测试（`test/integration/tests/auth/`，真实二进制子进程，SQLite 种子库自带 admin）

- `reset_password/reset_password_test.go`：
  - 新增 `AUTH-3-005 管理员代改内置 admin 密码`：`PATCH /open-api/v1/auth/users/admin/passwd` 仅带 `password` → 断言 200。集成环境 `SkipTokenValidate=true`（`test/integration/conf/ai_gateway_api.toml:51`），visitor 为 `SkipUser`，走"代改"路径无需 `old_password`；
  - 现有 `AUTH-3-003 密码过短`（`short1`，6 字节）保持 422 断言不变 —— 对齐后拦截层由 `Password()` 上移至 tag，ErrCode 不变；
  - 可新增 7 字节边界用例（如 `short12`）pin 住 tag 层拦截。
- `set_admin/set_admin_test.go` 新增 `AUTH-4-00x 为内置 admin 设置 is_admin`：`PATCH /auth/users/admin/is_admin {"is_admin": true}` → 断言 200，并 GET `/auth/users/admin` 复核 `is_admin=true`。
- `create_user/create_user_test.go`：
  - `AUTH-1-007 保留 user_name（admin）` 保持 422 断言不变（创建路径行为回归）；
  - `AUTH-1-008 密码过短`（`short1`，6 字节）保持 422 断言不变（拦截层同上移至 tag）。

### 6.3 回归验证

1. 本地：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；
2. `make test-model-cover-gate`（model 覆盖率 ≥70%）通过 —— 本次改动集中在 endpoints、lib 与文档，model 层无改动，预期无覆盖率波动；
3. 集成测试 `test/integration/tests/auth/{reset_password,set_admin,create_user}` 全部 PASS 后，按 issue #226 复现脚本对部署环境重验：admin 自助改密（带 `old_password`）与管理员代改均返回 200，bcrypt 字段更新生效；
4. 文档人工核对：`auth.md` 修正后的链接可跳转至 `00-common.md` §12/§13；§12 保留名约束措辞与端点行为一致。

## 7. 实施记录

实施内容与 §5 方案一致，已完成并验证（2026-10-09）：

- `lib/validate/validate.go`：新增私有 `userNameFormat()` 与导出 `UserNameRef()`；`UserName()` 改为 `userNameFormat()` + 保留名检查，行为不变；
- `endpoints/openapi_v1/auth/user_update_password.go`：`Validate()` 换用 `validate.UserNameRef()`；`Password` tag `min=6` → `min=8`；
- `endpoints/openapi_v1/auth/user_update_is_admin.go`：`Validate()` 换用 `validate.UserNameRef()`；
- `endpoints/openapi_v1/auth/user_create.go`：`Password` tag `min=1` → `min=8`；
- `design-docs/api-define/OpenAPI接口定义/00-common.md` §12：保留名约束限定为创建场景，引用端点显式不受限；
- `design-docs/api-define/OpenAPI接口定义/auth.md`：`[UserName]`/`[Password]`/`[TokenName]` 共 8 处锚点改为 `00-common.md` 相对路径链接。端点章节不逐条重复保留名说明（类型定义为唯一事实源，评审后撤销了 §2.3/§2.5 的逐端点标注）。

验证结果：

- 单元测试：`go test ./lib/validate/... ./endpoints/openapi_v1/auth/...` 通过；`go test ./...` 全量通过；
- model 覆盖率门禁：84.8% ≥ 70%，通过（环境无 make，按 Makefile target 等价命令执行）；
- 集成测试（重新构建 `ai-gateway-api.exe` 后运行 `test/integration/tests/auth/{reset_password,set_admin,create_user}`）全部 PASS，关键用例：`AUTH-3-005 管理员代改内置 admin 密码` ✅、`AUTH-3-006 密码 7 字节边界（tag min=8 拦截）` ✅、`AUTH-5-002 为内置 admin 设置 is_admin` ✅、创建路径 `AUTH-1-007 保留 user_name（admin）` 仍为 422 ✅。
