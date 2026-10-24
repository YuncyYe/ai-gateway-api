# 密码哈希化与「SKIP」后门移除：设计变更说明

> 配套：《change-summary.md》（背景、目标、关键决策、范围）。本文给出可落地的详细设计，两份文档自包含。

---

## 1. 现状代码事实（设计约束来源）

| 事实 | 位置 | 约束 |
|------|------|------|
| 登录比对明文 `!=` + `SKIP` 后门 | `model/iauth/authentication.go:259` | 改造点①：删旁路、改恒时、支持哈希/明文双格式、懒迁移 |
| 登录事务边界 | `authentication.go:244-294`（`AtomExecute`，:246） | 懒迁移重哈希与 session 更新**同事务** |
| 改密旧密码明文比对、新密码明文落库 | `authentication.go:604-644`（:613、:619） | 改造点②：旧密码恒时校验、新密码哈希后写入 |
| 建号明文落库 | `authentication.go:525-552`（storager :544） | 改造点③：HashPassword 后再交 storager |
| model 层密码策略仅 ≥6 | `authentication.go:590-596` | 改造点④：对齐 API 层 8–72 字节 |
| API 层密码策略 8–128 字符 | `lib/validate/validate.go:211-224`（user_create.go:39、user_update_password.go:49 调用） | 上限 128 字符→72 字节（按 UTF-8 字节计） |
| type=1 Token 用户不走密码认证 | `storage/rdb/auth/authentication.go:47`（FetchUserList 强制 Type=normal） | demo/conf-agent 空密码无需处理 |
| 存储层出入参即字符串 | `storage/rdb/auth/authentication.go:87-96,111-127,143-161` | DAO 零改动 |
| 日志已脱敏 password | `model/ioperlog/mask.go:41-74`；`operation_log.go:112-114,139-141` | 哈希化后日志安全性只增不减 |
| x/crypto 依赖已存在 | `go.mod:72`（v0.45.0） | bcrypt 零新增依赖 |
| SecurityConfig 已有 | `stateful/security.go:60-78` | PasswordHashCost 并入同一配置段 |
| 初始化 admin/admin 明文 ×3 | `db_ddl.sql:632-633`、`db_ddl_sqlite.sql:670-671`、`ai-gateway/api_db_ddl.sql:532` | 改预计算哈希 |

## 2. 算法选型

| 方案 | 结论 | 理由 |
|------|------|------|
| **bcrypt** | **选用** | 零新依赖；自包含盐+成本因子；`CompareHashAndPassword` 内部按口令恒时；生态成熟、审计多 |
| scrypt / argon2id（同 x/crypto 模块） | 备选演进 | 内存硬/现代首选，但参数敏感、与既有 §10 建议不一致；可经 xcrypto 接口化后续替换 |
| PBKDF2 / SHA-256 / MD5 | 否决 | 抗 GPU 弱或无盐无成本因子 |

- 存储格式：标准 bcrypt 串（60 字符），`$2a$10$<22字符盐><31字符摘要>`。
- 识别规则：`$2a$`/`$2b$`/`$2y$` 前缀=已哈希；无前缀=存量明文（迁移期兼容判定）。
- 成本因子：默认 10（现代 CPU 单次校验约 60–80ms）；登录为低频管理面操作，无性能压力。

## 3. `lib/xcrypto/password.go`（新增）

```go
package xcrypto

// HashPassword 生成 bcrypt 哈希（成本因子取包级配置，默认 10）。
func HashPassword(plaintext string) (string, error)

// CheckPassword 恒时校验。返回 ok 与 needMigrate：
//   - hashed 为 bcrypt 串：bcrypt.CompareHashAndPassword（恒时），needMigrate=false；
//   - hashed 为存量明文：subtle.ConstantTimeCompare（恒时），
//     ok=true 时 needMigrate=true（调用方应重哈希落库）。
func CheckPassword(hashed, plaintext string) (ok bool, needMigrate bool)

// IsHashedPassword 判断 s 是否为 bcrypt 串（$2a$/$2b$/$2y$ 前缀）。
func IsHashedPassword(s string) bool

// SetPasswordHashCost 启动时由 stateful 注入；非法值（<4 或 >16）返回错误。
func SetPasswordHashCost(cost int) error

// dummyHash 包级预生成固定 bcrypt 哈希，用于用户不存在时的恒时拉平比对。
var dummyHash = func() string { h, _ := bcrypt.GenerateFromPassword([]byte("dummy"), DefaultCost); return string(h) }()
```

恒时三原则：

1. 已哈希用户 → `bcrypt.CompareHashAndPassword`（库内部恒时）。
2. 存量明文用户 → `subtle.ConstantTimeCompare`（替代 `!=`，消除按位泄露时序）。
3. **用户不存在也执行一次针对 dummyHash 的 bcrypt 比对**，拉平"用户不存在 / 口令错误"两条路径的响应时序，缓解用户名枚举。

明文不出此包边界：调用方仅拿到 ok/needMigrate，哈希在入口完成，不进日志、不进错误信息。

## 4. `model/iauth/authentication.go` 改造

### 4.1 `authTypePassword`（:244-294）——登录 + 懒迁移 + 后门移除

```go
userName := param.Identify
user, err := manager.storager.FetchUser(ctx, &UserFilter{Name: &userName})
if err != nil { return err }

if user == nil {
    xcrypto.CheckPassword(xcrypto.DummyHash(), param.Extend) // 恒时拉平，防用户名枚举
    return xerror.WrapAuthenticateFailErrorWithMsg("User %s Not Exist", userName)
}

// 改造前：if param.Extend != "SKIP" && user.Password != param.Extend { ... }
// 改造后：旁路删除，双格式恒时比对
ok, needMigrate := xcrypto.CheckPassword(user.Password, param.Extend)
if !ok {
    return xerror.WrapAuthenticateFailErrorWithMsg("Password Wrong")
}

// update session key（原 :264-290 循环逻辑不变），newParam 扩展：
newParam := &UserParam{ /* SessionKey / SessionKeyCreateAt 原样 */ }
if needMigrate { // 懒迁移：与 session 更新同一 AtomExecute 事务
    reHashed, err := xcrypto.HashPassword(param.Extend)
    if err != nil { return err }
    user.Password = reHashed
    newParam.Password = &reHashed
}
return manager.storager.UpdateUser(ctx, user, newParam)
```

- `SKIP` 移除后，字面量口令 `"SKIP"` 行为与任意错误口令一致（补回归用例）。
- `Extend` 为空串时 `CheckPassword` 必然失败，与现状行为一致。
- 并发登录：password 更新为幂等赋值（bcrypt 随机盐，两次哈希串不同但均有效，最后落库者胜），无冲突风险。

### 4.2 `UpdateUserPassword`（:604-644）——改密

```go
// userChecker 内（:613）：
if pcd.OldPassword != "" {
    ok, _ := xcrypto.CheckPassword(user.Password, pcd.OldPassword) // 兼容存量明文旧密码
    if !ok { return xerror.WrapParamErrorWithMsg("Invalid Password") }
}
// :619 newData.Password 写入前：
hashed, err := xcrypto.HashPassword(pcd.Password)
if err != nil { return err }
newData.Password = &hashed
```

### 4.3 `CreateUser`（:525-552）——建号

`passwordCheck` 通过后、`storager.CreateUser` 之前：`param.Password = &hashed`（HashPassword 结果）。storager 与 DAO 无改动。

### 4.4 `passwordCheck`（:590-596）——策略对齐

```go
func passwordCheck(password string) error {
    n := len(password) // 按 UTF-8 字节计
    if n < 8 || n > 72 {
        return xerror.WrapParamErrorWithMsg("password length must be between 8 and 72 bytes")
    }
    return nil
}
```

仅约束新设/改密；登录比对路径不校验长度，存量短口令（如 admin）登录不受影响。

### 4.5 操作日志

`userToMap`/`userParamToMap` 结构不变；落库值变为哈希后，`MaskSensitiveFields` 对 `password` 键脱敏（`mask.go:41-74`）保持有效，日志不引入新泄露面。

## 5. 配置模型（`stateful/security.go` 并入 SecurityConfig）

```go
type SecurityConfig struct {
    // ... 既有字段（MasterKeyFile/ActiveKeyID/EncryptExports/ExportKeyFile/ActiveExportKeyID）不变
    // PasswordHashCost 是 users.password bcrypt 成本因子。Default 10。
    // Valid range 4-16; raise for higher security at login latency cost.
    PasswordHashCost int `toml:"PasswordHashCost" validate:"min=0,max=16"` // 0=默认 10
}
```

```toml
[Security]
  # ... 既有项不变
  # PasswordHashCost = 10   # bcrypt cost for users.password，默认 10，合法 4-16
```

- 加载时：`cost := config.Security.PasswordHashCost; if cost == 0 { cost = 10 }` → `xcrypto.SetPasswordHashCost(cost)`，失败则启动报错（fail-fast，与 access_control 同纪律）。
- model 层不读 `stateful.DefaultConfig`（遵循 AGENTS.md 依赖注入约定）。

## 6. DDL（本仓 2 处 + 另仓 1 处）

```sql
-- ai-gateway-api/db_ddl.sql:632-633
-- ai-gateway-api/db_ddl_sqlite.sql:670-671
-- ai-gateway/api_db_ddl.sql:532（另仓 ai-gateway，随本变更同步）
insert into users (id, name, password, scopes, created_at)
values(1, 'admin', '$2a$10$w2oNyh4MO7SB.NHLPSq6kOj1GMiX1fApPYcWJmL8toZXGCQs3AJ0K', 'System', now());
```

- 哈希为口令 `admin`、cost=10 的实测值（`bcrypt.GenerateFromPassword([]byte("admin"), 10)`）；初始口令仍为 `admin`，README/BUILD_GUIDE 快速开始文档不变。
- bcrypt 字母表不含单引号，SQL 字符串安全；`varchar(255)` 无需改表。
- 若调整默认 cost，重新生成：`h, _ := bcrypt.GenerateFromPassword([]byte("admin"), <cost>)`。

## 7. 存量迁移与升级顺序

### 7.1 兼容矩阵

| 代码版本 | 数据形态 | 结果 |
|----------|----------|------|
| 新代码 | 明文（存量库） | ✅ 登录成功并懒迁移重哈希 |
| 新代码 | 哈希（新装/已迁移） | ✅ 正常登录 |
| 旧代码 | 明文 | ✅ 现状（升级起点） |
| 旧代码 | 哈希 | ❌ 登录失败——**必须先升代码，禁止先刷数据** |

### 7.2 升级步骤

1. 升级 `ai-gateway-api` 至本变更版本（双格式兼容）。
2. 运行观察：存量账号随登录自然收敛为哈希，不停机、不锁表。
3. （可选）长期不登录账号由运维重置密码，或待 P1「首次登录强制改密/批量重散列工具」统一处理。
4. 新装环境直接使用新 DDL。

### 7.3 回滚

代码回滚后已哈希口令无法通过明文比对登录，需 DBA 重置密码或回滚数据快照。**发布单须注明升级顺序约束与回滚时限**。

## 8. 测试计划

- `lib/xcrypto/password_test.go`（新增）：
  - Hash/Check 往返：正确口令 ok、错误口令 !ok；
  - `IsHashedPassword`：`$2a$`/`$2b$`/`$2y$` 前缀识别与非哈希串判定；
  - 明文路径：`subtle` 比对且 needMigrate=true；
  - 71/72/73 字节边界行为；
  - dummy 哈希存在且校验失败（用于时序拉平，不断言耗时）；
  - `SetPasswordHashCost` 合法/非法值。
- `model/iauth/authentication_test.go`（增补，沿用 `mocks_test.go` 手写回调 mock 风格）：
  - bcrypt 用户登录成功（FetchUser 返回 `$2` 前缀口令）；
  - **legacy 明文登录成功 + 懒迁移**：断言 `UpdateUser` 收到的新 password 为 `$2` 前缀且 session key 同时更新；
  - **`"SKIP"` 后门移除回归**：口令 `SKIP` 登录失败；
  - 用户不存在与口令错误均返回认证失败（不泄露差异语义）；
  - `UpdateUserPassword`：旧密码分别为明文/哈希两种形态均改密成功；落库新值为哈希；
  - `CreateUser`：storager 收到的 password 为哈希串而非明文。
- 回归：既有 iauth 全部用例；`make test-model-cover-gate`（model 覆盖率 ≥70%）通过。
- 手工验证：旧库（明文）+ 新二进制 admin/admin 登录后查库为 `$2a$...`；新装 DDL 登录成功且库内无明文口令；改密后旧 session 失效、新口令可登录；`SKIP` 登录失败。

## 9. WBS

| # | 任务 | 改动 | 量 |
|---|------|------|----|
| 1 | xcrypto 口令模块 | `lib/xcrypto/password.go` + 单测 | 0.5 d |
| 2 | PasswordHashCost 配置 | `stateful/security.go` + 配置加载注入 + fail-fast 校验 | 0.25 d |
| 3 | model/iauth 改造 | 登录（懒迁移+后门移除）、改密、建号、passwordCheck + 单测 | 0.5 d |
| 4 | API 层密码策略 | `lib/validate/validate.go` 上限 128 字符→72 字节 | 0.1 d |
| 5 | DDL 三处 + 手工验证 | db_ddl.sql / db_ddl_sqlite.sql / ai-gateway api_db_ddl.sql | 0.5 d |
| 6 | 文档 | modifications（本文档）+ 认证授权机制.md 更新 + AES §10 状态标注 | 0.15 d |

合计约 2 人日。

## 10. 风险与对策

| 风险 | 对策 |
|------|------|
| 先刷数据后升代码导致全员无法登录 | 升级顺序写入发布单与运维手册（§7.1/§7.2）；DDL 变更与二进制发布绑定评审 |
| 代码回滚后哈希口令无法登录 | 回滚预案：DBA 重置密码或数据快照回滚；发布单注明回滚时限 |
| 超长存量口令（>72 字节）改密被拒 | 概率极低；报错信息明确（长度提示）；登录路径不受影响 |
| cost 配置过高（>12）导致登录变慢 | 配置区间上限 16 + 注释建议；默认 10 |
| 懒迁移窗口期库内残留明文行 | 属预期（收敛速度=登录频率）；高安全场景升级后强制批量改密 |
| `"SKIP"` 旁路删除影响既有依赖 | grep 全仓确认 `SKIP` 仅 :259 一处消费；集成环境回归登录链路 |

## 11. 开放问题（实现前需评审确认）

1. 长期不登录账号的收敛：本期不做工具，P1「首次登录强制改密/批量重散列」统一处理——倾向认可。
2. 初始口令保持 `admin`（快速开始体验）vs 随机初始口令——倾向保持 + P1 强制改密兜底。
3. 客户环境是否要求更高 cost（如 12）或 scrypt/argon2id 替换——保持 bcrypt + 配置化，留演进接口。
4. `SkipTokenValidate` 调试开关是否随本期一并治理——**不属本期**（P1 另项，避免变更混杂）。
