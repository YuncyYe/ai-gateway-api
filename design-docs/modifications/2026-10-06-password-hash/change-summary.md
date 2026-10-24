# 密码哈希化与「SKIP」后门移除：变更摘要

## 1. 背景

控制面管理面用户口令当前**全链路明文**：

| 问题 | 证据 |
|------|------|
| 明文等值比对 + `"SKIP"` 后门（提交密码字面量等于 `SKIP` 即跳过校验，可对任意账号登录） | `model/iauth/authentication.go:259` |
| 改密旧密码同样明文比对 | `model/iauth/authentication.go:613` |
| 密码原样落库 | `storage/rdb/auth/authentication.go:87-96` |
| 默认 admin/admin 明文写死初始化 SQL（3 处） | `db_ddl.sql:632-633`、`db_ddl_sqlite.sql:670-671`、`ai-gateway/api_db_ddl.sql:532` |
| 密码策略两层不一致 | API 层 8–128 字符（`lib/validate/validate.go:211-224`）；model 层仅 ≥6（`authentication.go:590-596`） |

安全底线要求：管理面用户口令库内**不可逆散列**、比对**恒时**、无任何旁路。需求来源：需求池《管理面强认证与密码加密存储现状分析》P0-1（密码 bcrypt 哈希存储）/ P0-2（移除 SKIP 后门）。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-10-06 |
| 涉及仓库 | `ai-gateway-api`（主改造）；`ai-gateway`（仅 `api_db_ddl.sql` 初始化数据同步，另仓登记） |
| 变更类型 | 安全整改：新增 `lib/xcrypto` 口令哈希原语 + `[Security].PasswordHashCost` 配置 + `model/iauth` 登录/改密/建号改造（bcrypt + 懒迁移 + 后门移除）+ 3 处 DDL 初始化数据哈希化 |
| 产出 | xcrypto 口令模块 + 配置项 + model 层 4 处改造 + 3 处 DDL + 单测/手工验证；**无新表、无表结构变更、无接口契约变更** |
| 预估工作量 | 约 2 人日（见 design-changes.md WBS） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 算法 | **bcrypt（`golang.org/x/crypto/bcrypt`）** | x/crypto v0.45.0 已在 `go.mod:72`（DB 落盘加密引入），零新增依赖；自包含盐+成本因子，单字符串入库；`CompareHashAndPassword` 内部恒时；生态最成熟。scrypt/argon2id 列备选（同模块可演进），PBKDF2/MD5/SHA 否决 |
| 2 | 72 字节上限 | **密码策略统一收紧为 8–72 字节（UTF-8 字节计），不引入 SHA-256 预哈希** | bcrypt 超 72 字节静默截断；现状 API 层 128 字符上限存在截断风险。保持存储格式标准、单代码路径；口令管理器普遍 ≤64 字符，影响面可忽略。登录比对路径不校验长度 |
| 3 | 后门移除 | `authentication.go:259` 删除 `param.Extend != "SKIP"` 旁路，比对路径唯一化 | 杜绝任意账号旁路登录；字面量 `SKIP` 退化为普通错误口令 |
| 4 | 恒时比对 | 三路径时序拉平：已哈希走 bcrypt 内部恒时；存量明文走 `subtle.ConstantTimeCompare`；**用户不存在时走预生成 dummy 哈希比对** | 缓解用户名枚举时序侧信道（现状 `!=` 比较与即时返回均泄露信息） |
| 5 | 存量迁移 | **懒迁移（首次登录成功即重哈希）**，复用 `AtomExecute` 同事务落库；无离线脚本、不锁表不停机 | 老库升级后随登录自然收敛；冷账号由 P1「首次登录强制改密」处理 |
| 6 | 升级顺序 | **先升代码、禁止先刷数据**（旧代码遇哈希口令登录失败） | 新代码双格式兼容，是唯一安全顺序；回滚需重置口令（见 design-changes.md §7） |
| 7 | DDL 初始口令 | 三处 `'admin'` 改为**预计算 bcrypt 哈希**（口令仍为 `admin`，cost=10，已实测生成）；README/BUILD_GUIDE 快速开始文档不变 | 库里不再出现明文；初始口令强制改密属 P1 另项 |
| 8 | 配置 | `[Security].PasswordHashCost`，默认 10，合法 4–16，越界启动报错；经 `xcrypto.SetPasswordHashCost` 注入，model 层不读 `stateful.DefaultConfig` | 与 DB 落盘加密共享 `SecurityConfig`（`stateful/security.go:60-78`）；遵循 AGENTS.md 依赖注入约定 |
| 9 | 改造层 | 哈希落在 **model manager 层入口**（所有调用路径统一）；`storage/rdb/auth` **零改动**（哈希只是字符串）；`ai-gateway-web` **零改动**（接口契约不变） | 最小爆炸半径；前端/DAO 无感知 |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `lib/xcrypto/password.go`（HashPassword / CheckPassword / IsHashedPassword / SetPasswordHashCost + dummy 哈希） |
| 主要修改 | `model/iauth/authentication.go`（authTypePassword 登录+懒迁移+后门移除、UpdateUserPassword、CreateUser、passwordCheck 策略对齐）；`stateful/security.go` + 配置加载（PasswordHashCost）；`lib/validate/validate.go`（Password 上限 128 字符→72 字节）；DDL：`db_ddl.sql:632-633`、`db_ddl_sqlite.sql:670-671`（本仓 2 处） |
| 另仓登记 | `ai-gateway/api_db_ddl.sql:532`（初始化数据哈希化，随本变更同步合入 ai-gateway 仓） |
| 明确不动 | OpenAPI/InnerAPI 端点契约（无 request/response 变化，**本变更不需要 api-changes.md**）；`storage/rdb/auth` DAO；`ai-gateway-web`；`SkipTokenValidate` 调试开关治理（P1 另项）；TLS（P0-3 另项）；MFA/登录失败锁定/首次登录强制改密（P1/P2 另项）；type=1 Token 用户（demo/conf-agent 空密码不走密码认证，`storage/rdb/auth/authentication.go:47` 强制 Type=normal） |
| 数据迁移 | 无表结构变更（bcrypt 串 60 字符 < varchar(255)）；存量明文由懒迁移收敛 |
| 接口契约 | 无变更；登录/改密/建号的请求响应与错误码语义不变 |

## 5. 非本仓登记点（链路协同）

| 位置 | 改动 | 状态 |
|------|------|------|
| `ai-gateway` 仓 | `api_db_ddl.sql:532` admin 初始化数据改 bcrypt 哈希（值见 design-changes.md §6） | 随本变更同步 |
| `document-ai-gateway` 仓 | 需求池《管理面强认证与密码加密存储现状分析》P0-1/P0-2 需求输入 | 已完成 |
| 运维手册 | 升级顺序与回滚预案公告（先升代码、禁先刷数据） | 随发布交付 |

## 6. 文档配套

- 本文（change-summary）+ `design-changes.md`（现状代码事实/算法选型/xcrypto 口令模块/model 改造/配置/DDL/兼容矩阵/测试/WBS/风险）。两份文档自包含，全部评审决议已融入 change-summary 决策表。
- 实现时同步更新（已完成）：`design-docs/sys-design/details/认证授权机制.md` §3.2 流程、§7.1 `password` 字段说明与 §10 安全建议第 1 条已更新为 bcrypt 哈希方案；`2026-10-05-db-encryption-at-rest` change-summary「明确不动」行的 `users.password` 关联项已回链标注本变更。
- 集成测试（已交付）：`test/integration/tests/auth/password_hash/`（AUTH-14-001~006，6 例，含 DB 直读存储断言与审计脱敏断言），已在 `tests/auth/design.md` 登记；testutil 新增 `GetUserPassword`/`SetUserPassword` helper。全量回归：`go test ./tests/auth/...` 14 包全部通过。

## 7. 六步法核对

- [x] Step 1：变更目录 `2026-10-06-password-hash`
- [x] Step 2：本摘要 + design-changes.md（无接口契约变更，不需要 api-changes.md）
- [x] Step 3：接口路径与响应契约无变更；仅 `00-common.md` Password 类型合同条款随 8-72 字节策略修订（测试用例 AUTH-14-005 锁定新语义，防止合同漂移）
- [x] Step 4：sys-design——`details/认证授权机制.md` §3.2/§7.1/§10 已更新；summary.md 索引描述仍准确，无需改动
- [x] Step 5：代码实现完成（WBS 全部落地：`lib/xcrypto/password.go` + 单测、`[Security].PasswordHashCost` 配置 + fail-fast 注入、`model/iauth` 四处改造 + 单测、`validate.Password` 72 字节上限、3 处 DDL 哈希化、i18n 映射同步；验证：`go build ./...` 通过，`lib/xcrypto`/`lib/validate`/`model/iauth`/`stateful` 测试通过，model 覆盖率 84.0% ≥ 70% 门禁通过，全仓测试编译检查通过）
- [x] Step 6：落地总结——bcrypt 哈希化 + SKIP 后门移除 + 懒迁移已生效；存量明文随登录收敛；升级顺序约束（先升代码、禁先刷数据）与回滚预案见 design-changes.md §7，需随发布单公告
