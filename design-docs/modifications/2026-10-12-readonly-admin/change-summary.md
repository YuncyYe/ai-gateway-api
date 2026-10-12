# 增加只读管理员（role 取代 is_admin + 密钥脱敏）：变更摘要

## 1. 背景

管理面用户角色仅"系统管理员"一档：任何能登录控制台的人都能创建/修改/删除全部资源，且密钥类字段明文返回——无法向"只需要看"的人员（审计、只读巡检、交付演示）开放控制台。

| 问题 | 证据 |
|------|------|
| 角色仅 admin：`is_admin` 固定 `true`，`false` 不支持 | `design-docs/api-define/OpenAPI接口定义/auth.md:19,75,90,192,230,290,508` |
| API 层三处收紧：`IsAdmin` 拒 false、`Scope` 仅 System/Support、创建用户硬编码 `ScopeSystem` | `lib/validate/validate.go:379-392`、`endpoints/openapi_v1/auth/user_create.go:54-68`、`user_update_is_admin.go:58-61` |
| `User.GetScopes()` 按 `Admin` bool 二值硬编码，不读 scopes 字段——无法表达"非 admin 的系统级只读用户" | `model/iauth/authentication.go:112-118`；存储层折叠 `storage/rdb/auth/authentication.go:116-124` |
| api-keys `key` / providers `keys[].key` / tokens `token` 创建、列表、详情均明文返回 | `api-keys.md:8,71`、`providers.md:17,102-107`、`auth.md:371,434,479` |
| 前端非 `is_admin` 直接 `illegalAccess`，无角色细分 | `ai-gateway-web/src/utils/authorize.js:61-63` |

需求与方案全文见 `document-ai-gateway/迭代系统设计/v0.8/增加只读管理员/`（需求分析 + 技术方案两份）。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-10-12 |
| 涉及仓库 | `ai-gateway-api`（主改造）、`ai-gateway-web`（前端，另仓）、`ai-gateway`（仅 nav_tree.toml 部署配置说明同步，无代码） |
| 变更类型 | 权限体系扩展：新增 `SystemReadonly` scope 档 + `role` 角色字段（**废弃 `is_admin`**）+ `scope2permission` 只读档 + 响应层密钥脱敏（`lib/xmask`）；**零 DDL、零新增依赖** |
| 产出 | 新角色"系统管理员（只读）"：全部读接口放行、全部写接口 403；api-keys `key`、providers `keys[].key`、tokens `token` 对只读角色掩码返回；用户接口 `is_admin` → `role`；session key 防泄漏契约；前端角色守卫/导航/用户管理适配 |
| 预估工作量 | 后端约 2-3 人日；前端约 1 人日；集成回归与文档约 1 人日（WBS 见 design-changes.md §9） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 角色模型 | **`role` 枚举字段统一表达（`system_admin`/`system_admin_readonly`），`is_admin` 废弃**；数据库存储复用 `users.scopes` 新值 `SystemReadonly`，单一映射 `roleToScopes` | 角色语义单点表达，消除"is_admin 宽泛/精确"长期歧义；内部 `Admin` bool 与 `IsAdmin()` 短路机制不变（API 表达与内核实现解耦，存量鉴权零影响）；**零 DDL**（`scopes varchar(2048)` 已存在，`db_ddl.sql:271-293`） |
| 2 | 判定红线 | 只读管理员不得拥有 `System` scope（内部 `Admin=false`），`Authorizate` 的 `IsAdmin()` 短路（`authorization.go:96-98`）对其不生效，只读性完全由 `scope2permission[SystemReadonly]` 表达 | 短路一旦放行即穿透全部写校验；方案 B"放宽 is_admin 语义"因此否决 |
| 3 | 权限档 | `scope2permission` 新增 `ScopeSystemReadonly` 档，**显式列举全部 35 个 Feature** 统一 `ActionRead`；不给 Update/Create/Delete/Export/Cancel | `scope2permission` 未列出的 Feature 默认拒绝；`ScopeSystem` 档当前漏列 9 个 Feature（靠短路兜底），只读档漏一个就是权限洞（反向）或不可用（正向）。**防御性补全 `ScopeSystem` 档**（与短路等价、消除隐式依赖） |
| 4 | 写接口全拒 | 不加任何写端点特判，纯靠既有 `Authorizer: iauth.FA(Feature, Action)` 声明 + `Authorizate` 位与判定 | 现有机制天然拒绝；403 语义统一（`"Feature Access Deny"`），前端已有统一处理 |
| 5 | 脱敏算法 | 掩码：长度 >8 → 前 4 + `********` + 后 4；4<长度≤8 → 前 2 + `****` + 后 2；长度≤4 → 全掩码。新轻量包 `lib/xmask`（零依赖） | 保留 `ak-`/`sk-` 类型前缀与尾部特征便于运维定位；O(1) 字符串拼接；否决哈希指纹（识别性差）、字段不回传（破坏结构稳定）、AES 可逆（违背防泄漏目标） |
| 6 | 脱敏挂载点 | 仅 openapi_v1 响应组装层：api-keys `newResponse`（一处覆盖列表+详情）、provider list/one、tokens list/one；`/inner-api/v1` 导出链路**零改动** | inner-api 导出（Support token 的 Export 动作）是 BFE 数据面鉴权转发的明文消费方，脱敏即破坏数据面；Support token 对读端点本就 403，到不了脱敏点 |
| 7 | tokens `token` | 列表/详情对只读角色掩码；**创建接口（POST）为写操作，只读角色鉴权层即被 403，天然免疫** | 写操作不可达，无需处理 |
| 8 | session key | 登录响应返回**本人** session key（不脱敏，功能必需）；用户列表/详情**不得返回** session key（现状已满足，`user_one.go:64-76` 默认不填）——固化为契约 + 测试断言，只读/admin 视角一致 | 掩码即破坏登录功能；契约化防止未来改动误填该字段 |
| 9 | 角色管理 API | 新增 `PATCH /auth/users/{user_name}/role`；`PATCH .../is_admin` 废弃移除（404）；`UpdateUserRole` 成为唯一角色变更入口，`UpdateUserIsAdmin`/`validate.IsAdmin` 作为死代码删除；变更写操作日志 | 单入口、无兼容包袱；迁移指引随变更记录发布 |
| 10 | Token scope | `validate.Scope` 枚举增加 `SystemReadonly`（P1，token 创建接口同步） | 自动化只读巡检脚本的凭证形态 |
| 11 | 升级约束 | **前后端必须同发**；禁止单独升级后端 | 新后端响应不再含 `is_admin`，旧前端守卫 `!userData.is_admin` 对所有人（含 admin）恒为真 → 控制台整体不可用（安全失败方向但影响可用性） |
| 12 | 前端 | 守卫改判 `role`（不做 `is_admin` 回退）；`nav_tree.toml` 查询类节点 `allowed_roles` 追加只读角色 key（导航按角色派生已就绪，`stateful/config_depends.go:82-137`）；用户管理页角色单选；核心列表页写按钮示范隐藏 | 导航按角色下发已就绪，零代码；后端 403 是唯一可信安全边界，前端隐藏仅为体验 |
| 13 | 内置账号保护 | **`admin` 账号的 role 固定为 `system_admin`**：`PATCH .../role` 对 admin 降级返回 422；校验落地 model 层 `UpdateUserRole`（大小写不敏感匹配） | 保证系统始终存在至少一个完整管理入口，防误操作/账号被盗后降级导致管理面失联；与创建场景保留名校验语义独立（reserved 校验仍仅创建场景，见 issue-226 修复） |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `lib/xmask/xmask.go`（掩码纯函数 + 单测）；`endpoints/openapi_v1/auth/user_update_role.go`（新端点）；`scope2permission` 只读档（`model/iauth/features.go`）；`Visitor.HasScope`/`IsSystemReadonly`（`model/iauth/authentication.go`） |
| 主要修改 | `model/iauth/authorization.go`（`ScopeSystemReadonly` 常量、`UpdateUserRole`、补全 `ScopeSystem` 档）；`model/iauth/authentication.go`（`User.Scopes` 贯通、`GetScopes()` 空值回退、`roleToScopes`/`scopesToRole`）；`storage/rdb/auth/authentication.go`（userD2M 解析 scopes）；`lib/validate/validate.go`（`Role` 新校验、`Scope` 增枚举、删 `IsAdmin`）；`endpoints/openapi_v1/auth/user_create.go`、`user_one.go`、`user_list.go`、`session_key_by_password.go`（`role` 字段）；`endpoints/openapi_v1/api_key/list.go`、`provider/list.go`、`provider/one.go`、`auth/token_list.go`、`auth/token_one.go`（脱敏）；`conf/nav_tree.toml`（allowed_roles） |
| 明确不动 | `/inner-api/v1` 全部端点与 `model/imods/` 导出链路（明文 key 通道）；`storage` 层 `Admin` 赋值与 `IsAdmin()` 短路机制；BFE 数据面；`users` 表结构（**零 DDL**）；`ai-gateway` 仓代码；`Product` scope 档（多租户另项）；前端 v-permission 指令体系（二期） |
| 接口契约 | 有变更，详见 `api-changes.md`：`is_admin` 废弃（breaking）、新端点、6 个读端点脱敏说明、用户读接口不含 session key 契约 |
| 数据迁移 | 无表结构变更；存量 `scopes='System'` 用户经 `scopesToRole` 显示为 `system_admin`；回滚后 `SystemReadonly` 账号在旧代码下退化为 `Product`（读部分放行、写拒绝，无越权写能力） |

## 5. 非本仓登记点（链路协同）

| 位置 | 改动 | 状态 |
|------|------|------|
| `ai-gateway-web` 仓 | `authorize.js`（role 守卫）、`store.js`（role 登录态/nav key 映射）、`User.vue`/`CreateUser.vue`（角色列/单选）、`zh.js`（i18n）、核心列表页写按钮示范隐藏 | **推迟到后续版本**（用户决策：本期不动前端）；发布本后端版本时必须同步发布前端适配版本，否则旧前端守卫对全部用户拒绝（决策 11） |
| `ai-gateway` 仓 | 无代码改动；发布物随附 `nav_tree.toml` 说明（如该仓分发配置文件需同步 allowed_roles） | 待确认 |
| 集成测试（本仓 `test/integration/tests/auth/`） | auth 域用例：`is_admin` → `role` 断言迁移；新增 `update_role`（AUTH-5，含内置 admin 降级保护）与 `readonly_admin`（AUTH-16，读/写矩阵、6 端点脱敏三视角、session key 契约）；45→67 例全绿 | 已落地 |
| `ai-gateway-api/conf/nav_tree.toml` | 查询类节点 `allowed_roles` 追加只读角色 key | 随本变更 |

## 6. 文档配套

- 本文（change-summary）+ `design-changes.md`（现状代码事实/选型/角色与权限档/脱敏/前端/兼容性/测试/WBS/风险）+ `api-changes.md`（接口契约变更）。三份文档自包含，评审决议全部融入 change-summary 决策表。
- 需求/方案全文：`document-ai-gateway/迭代系统设计/v0.8/增加只读管理员/增加只读管理员需求分析.md`、`增加只读管理员技术方案.md`（FR-1~FR-14、验收标准）。
- 实现时同步更新：`design-docs/api-define/OpenAPI接口定义/auth.md`、`api-keys.md`、`providers.md`（见 api-changes.md §11）；`design-docs/sys-design/details/认证授权机制.md`（补只读角色档）。

## 7. 六步法核对

- [x] Step 1：变更目录 `2026-10-12-readonly-admin`
- [x] Step 2：本摘要 + design-changes.md + api-changes.md
- [x] Step 3：api-define —— `auth.md`（`is_admin` 全量废弃改 `role`、§2.5 整章替换为变更用户角色、Token scope 枚举、tokens 脱敏说明、session key 契约）；`api-keys.md`/`providers.md`（密钥字段角色可见性说明）；`00-common.md` 无需改动
- [x] Step 4：sys-design —— `details/认证授权机制.md`（Scope 表增 SystemReadonly、role↔scopes 映射、§4.3 只读档、§4.4 短路注释、接口清单表、边界情况、安全建议、Action 位掩码注释修正）；`summary.md` 索引同步
- [x] Step 5：代码实现 —— `lib/xmask`（新）、`model/iauth`（角色映射/Scopes 贯通/只读档/System 档补全/UpdateUserRole+admin 保护）、`storage/rdb/auth`（parseScopes 精确解析）、`lib/validate`（Role/Scope/删 IsAdmin）、auth 端点（user_update_role 新增/user_update_is_admin 删除/UserData.role）、脱敏三挂载点、`nav_tree.toml`；验证：`go build ./...` 通过，全仓 `go test ./...` 零回归，model 覆盖率 84.9% ≥ 70%
- [x] Step 6：落地总结（见 §8 实现校准记录）；集成测试 `test/integration/tests/auth/` 已同步落地（`update_role`/`readonly_admin` 新增，存量 is_admin 用例迁移，45→67 例全绿）

**当前状态：已实现并完成回归验证（2026-10-12），待发布。前端适配（ai-gateway-web）按计划推迟，发布时必须前后端同发（决策 11）。**

## 8. 实现校准记录（Step 5 实际落地 vs 设计）

| 项 | 设计 | 实际实现 | 说明 |
|----|------|----------|------|
| `ActionRead`/`ActionReadAll` 位掩码 | 文档（含 `features.go` 注释）声称两者同值，只读档授 `ActionRead` 即可 | **注释有误**：`1<<iota` 下 `ActionRead=2`、`ActionReadAll=4`，为不同位。挂 `ActionReadAll` 的读端点（api-keys 列表、tokens、用户列表/详情）会被只读档误拒——单测 `TestAuthorizeManager_Authorizate_SystemReadonly` 当场抓住 | 只读档改为 `actionReadonly = ActionRead.Grant(ActionReadAll)`；代码注释、`认证授权机制.md` 错误描述同步修正 |
| 存储层 admin 判定 | 沿用 `strings.Contains(param.Scopes, ScopeSystem)` | **子串匹配隐患**：`"SystemReadonly"` 含子串 `"System"`，只读管理员会被误判为完整 admin（短路穿透全部写校验，正中设计红线） | 改为 `parseScopes` 逗号分隔精确解析 + 精确匹配；`Admin` 赋值逻辑语义不变 |
| 掩码示例 | 文档示例 `Px2szn6R1HQo-WRSIJyt` → `Px2s********WRSIJyt`（后 4 误写为 8 字符） | 正确为 `Px2s********IJyt`（20 字符 token：前 4 + 后 4） | `api-changes.md`/`design-changes.md`/需求分析文档示例已修正 |
| `Validate()` nil 防护 | 未预见 | `Role` 为指针，直接 `Validate()`（绕过 tag 校验的路径）对 nil 解引用 panic | `UserCreateParam`/`UserUpdateRoleParam` 的 `Validate()` 增加 nil 判空 |
| 只读档 Feature 数 | 设计文稿写"约 35 个" | 实际 37 个（Feature 常量全量） | `allFeatures` 清单 + 全 Feature 覆盖断言（`TestScope2Permission_SystemReadonlyCoversAllFeatures`）固化，新增 Feature 漏授权即测试报警 |
| 集成测试身份扮演 | 设计登记为"待落地" | 已实现：`UserProbeAction` 优先解析 Authorization 头，Skip 模式可经 `Authorization: Session <key>` 真实扮演只读用户 | testutil 新增 `SetHeader`/`LoginSessionKey`/`CreateUserWithRole`；踩坑两则：共享夹具清理须挂 TestMain（`t.Cleanup` 会在首个用例函数结束时误删用户）、同一用户重复登录覆盖 session key |
| 集成测试用例质量 | 初版按现有测试风格编写 | 经 `ai-gateway-api-integration-test-design` 技能清单复核，修正反面模式 #2（负向用例只断言 4xx 不回读）：写拒绝矩阵补「精确 402 + 拒绝后回读零变更」；补角色变更审计断言（检查项 7）与 inner-api 导出明文回归（检查项 8 + 本期 FR-6）；用例 67→69 | design.md 已同步登记 |
