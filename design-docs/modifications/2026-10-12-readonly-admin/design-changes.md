# 增加只读管理员：设计变更说明

> 配套：《change-summary.md》（背景、目标、关键决策、范围）。本文给出可落地的详细设计，两份文档自包含。

---

## 1. 现状代码事实（设计约束来源）

| 事实 | 位置 | 约束 |
|------|------|------|
| Scope 常量 `Allowed/System/Product/Support` | `model/iauth/authorization.go:26-31` | 新增 `ScopeSystemReadonly` |
| `Authorizate` 先查 `IsAdmin()` 短路放行 | `model/iauth/authorization.go:96-98` | 只读角色 `Admin` 必须为 false（红线）；短路机制本身不动 |
| `UpdateUserIsAdmin` 重写 scopes（true→System / false→Product） | `model/iauth/authorization.go:68-88` | 由 `UpdateUserRole` 取代后删除 |
| `scope2permission` 三档；未列出的 Feature 默认拒绝；`ScopeSystem` 档漏列 9 个 Feature | `model/iauth/features.go:174-259` | 只读档必须显式列举全部 35 个 Feature；顺带补全 System 档 |
| Action 位掩码；`ActionRead` 与 `ActionReadAll` 同值 | `model/iauth/features.go:67-76` | 端点挂 Read 或 ReadAll 都能过只读档的 `IsAllowed` 位与 |
| `User.GetScopes()` 按 `Admin` bool 硬编码，不读 scopes | `model/iauth/authentication.go:112-118` | `User` 需贯通 `Scopes []string`，空值回退旧逻辑 |
| `Token.GetScopes()` 返回 `[t.Scope]`；`Token.IsAdmin()` 为 `Scope==System` | `model/iauth/authentication.go:141-151` | Token 侧零改动天然兼容新 scope 值 |
| 存储层 scopes 折叠成 `Admin` bool | `storage/rdb/auth/authentication.go:116-124`（:120） | userD2M 增加 `Scopes` 解析；`Admin` 赋值不变 |
| 校验收紧：`IsAdmin` 拒 false、`Scope` 仅 System/Support | `lib/validate/validate.go:379-392` | 新增 `Role` 校验；`Scope` 增枚举；`IsAdmin` 删除 |
| 创建用户硬编码 `ScopeSystem` | `endpoints/openapi_v1/auth/user_create.go:62-67` | 改 `roleToScopes(param.Role)` |
| api-key 列表/详情共用 `newResponse` | `endpoints/openapi_v1/api_key/list.go:139-151`、`one.go:62` | 脱敏挂一处覆盖两个端点 |
| provider 返回 `*iprovider.Provider`（含 `Keys []ProviderKey`） | `endpoints/openapi_v1/provider/list.go:41-76`、`model/iprovider/provider.go:66-69,95-106` | list/one 组装处新增掩码 helper（先 copy Keys 再改） |
| tokens 列表/详情共用 `newTokenData` | `endpoints/openapi_v1/auth/token_one.go:63-76`、`token_list.go:34-46` | 两处组装后掩码 `TokenData.Token`；创建（POST）写操作天然免疫 |
| `UserData.SessionKey` 默认不填，仅登录赋值 | `endpoints/openapi_v1/auth/user_one.go:64-76`、`session_key_by_password.go:58-59` | 固化为契约：用户读接口不得返回 session key |
| `users.scopes` 列已存在 | `db_ddl.sql:271-293` | 零 DDL |
| inner-api 导出走 Support scope 的 `ActionExport` | `model/iauth/features.go:246-258` | 导出链路零改动；Support token 对读端点 403，到不了脱敏点 |
| 导航按角色派生（`allowed_roles`） | `stateful/config_depends.go:82-137`、`conf/nav_tree.toml` | 配置追加只读角色 key 即可，后端零代码 |

## 2. 角色模型选型

| 方案 | 机制 | 结论 |
|------|------|------|
| A. `is_admin` + `is_readonly_admin` 双 bool | 两个 bool 组合表达三态 | **否决**：组合态易冲突（双 true），且 `IsAdmin()` 短路语义必须改，波及所有鉴权路径 |
| B. 放宽 `is_admin` 语义为"系统级（含只读）" | 只读管理员 `is_admin=true`，另靠 scope 限制写 | **否决**：与 `authorization.go:96-98` 短路直接冲突——短路放行即穿透全部写校验，红线 |
| C. **`role` 枚举统一表达、`is_admin` 废弃 + scopes 落新值（选用）** | API 层 `role`（`system_admin`/`system_admin_readonly`），内核 `scopes`（`System`/`SystemReadonly`），单一映射 `roleToScopes` | **选用**：角色语义单点表达，消除歧义；内部 `Admin` 与短路不变（API 与内核解耦）；未来加角色只加枚举值 |

## 3. 角色与 scope 模型

**常量**（`model/iauth/authorization.go`）：

```go
ScopeSystemReadonly = "SystemReadonly"
```

**role ↔ scopes 映射**（`model/iauth/authentication.go`）：

```go
func roleToScopes(role string) ([]string, error) {
    switch role {
    case RoleSystemAdmin:          // "system_admin"
        return []string{ScopeSystem}, nil
    case RoleSystemAdminReadonly:  // "system_admin_readonly"
        return []string{ScopeSystemReadonly}, nil
    default:
        return nil, xerror.WrapParamErrorWithMsg("role must be system_admin or system_admin_readonly")
    }
}
```

**`iauth.User` 贯通 scopes**：

| 位置 | 现状 | 改为 |
|------|------|------|
| `User` 结构（`authentication.go:98-106`） | 无 Scopes 字段 | 增加 `Scopes []string` |
| `User.GetScopes()`（`:112-118`） | 按 `Admin` 硬编码 `[System]`/`[Product]` | 返回 `u.Scopes`；**为空时回退旧逻辑**（旧路径/旧测试行为不变） |
| `User.IsAdmin()`（`:124-126`） | 返回 `u.Admin` | **不变**（红线） |
| `Visitor`（`:90-96` 附近） | 无 | 新增 `HasScope(scope string) bool`、`IsSystemReadonly() bool`（`!IsAdmin() && HasScope(ScopeSystemReadonly)`） |
| storage `userD2M`（`storage/rdb/auth/authentication.go:116-124`） | scopes 折叠为 `Admin` | 增加 `Scopes` 解析（逗号分隔、空串过滤）；`Admin` 赋值不变 |

**角色变更**（`model/iauth/authorization.go`）：

- 新增 `AuthorizeManager.UpdateUserRole(ctx, user, role string)`：复用 `AtomExecute + storager.UpdateUserScopes`（`:74-76`）与 `recordUserOperation`（`:78-87`），与 `UpdateUserIsAdmin` 同构；**内置账号保护**：`strings.EqualFold(user.Name, "admin")` 时仅接受 `system_admin`，降级请求返回参数错误（`user "admin" role is fixed to system_admin`）——保证系统始终存在完整管理入口，防误操作/账号被盗后降级失联；
- **移除 `UpdateUserIsAdmin` 与 `validate.IsAdmin`**：`is_admin` 端点废弃后成为死代码；原 `is_admin=false` 写 `ScopeProduct` 路径（`:69`）随之消亡（Product 放开属多租户另项）。

Token 侧零改动：`Token.GetScopes()` 返回 `[t.Scope]`、`Token.IsAdmin()` 为 `Scope==System`（`authentication.go:141-151`），`SystemReadonly` 天然落入同一机制。

## 4. 只读权限档

`scope2permission` 新增（`features.go`），**显式列举 `features.go:90-155` 全部 35 个 Feature**，统一 `ActionRead`：

```go
ScopeSystemReadonly: {
    // global resource
    FeatureProxyPool:  ActionRead,
    FeatureBFECluster: ActionRead,
    FeatureBFEPool:    ActionRead,
    FeatureArea:       ActionRead,
    FeatureDomain:     ActionRead,
    FeatureProduct:    ActionRead,
    FeatureExtraFile:  ActionRead, // ScopeSystem 档当前漏列，只读档必须显式列
    // product resource
    FeatureProductPool:       ActionRead,
    FeatureRoute:             ActionRead,
    FeatureSubCluster:        ActionRead,
    FeatureProductCluster:    ActionRead,
    FeatureGeneral:           ActionRead,
    FeatureTraffic:           ActionRead,
    FeatureCert:              ActionRead,
    FeatureActiveHealthCheck: ActionRead,
    // auth
    FeatureProductUser: ActionRead,
    FeatureUser:        ActionRead,
    FeatureToken:       ActionRead,
    // nlb / ai
    FeatureNLBPool:    ActionRead,
    FeatureNLBCluster: ActionRead,
    FeatureAIRoute:    ActionRead,
    FeatureAPIKey:     ActionRead,
    // quota
    FeatureEntityType:      ActionRead,
    FeatureEntity:          ActionRead,
    FeatureQuotaPlan:       ActionRead, // 漏列补齐
    FeatureRateLimitPolicy: ActionRead,
    // ai cache / context / mirror / intent（均为漏列补齐）
    FeatureAICache:       ActionRead,
    FeatureAIContext:     ActionRead,
    FeatureTrafficMirror: ActionRead,
    FeatureAIIntent:      ActionRead,
    // pricing / provider
    FeatureModelPrice: ActionRead,
    FeatureProvider:   ActionRead,
    // k8s / ops / security / report / batch
    FeatureK8sPool:      ActionRead,
    FeatureOperationLog: ActionRead,
    FeatureSecurity:     ActionRead,
    FeatureReport:       ActionRead,
    FeatureBatch:        ActionRead, // 仅读；Cancel 不给
},
```

要点：

- `ActionRead`/`ActionReadAll` 同值（`features.go:69-70`），端点无论挂 `ActionRead`（如 `provider/list.go:32`）还是 `ActionReadAll`（如 `api_key/list.go:34`），`IsAllowed` 位与判定（`features.go:86-88`）均通过；
- **不给 `ActionExport`**：报表 CSV 导出与 inner-api 导出同挂 Export 动作，给了会同时放开两类——本期只读档不含 Export（开放问题见 change-summary §3-10 关联）；
- **防御性补全 `ScopeSystem` 档**：漏列的 9 个 Feature（`ExtraFile/EntityType/Entity/QuotaPlan/RateLimitPolicy/AICache/AIContext/TrafficMirror/AIIntent`）补为 `actionAll`——行为与现状等价（admin 走 `IsAdmin()` 短路不读该档），消除"档表不完整"的隐式依赖；
- 校验链路：只读管理员 `IsAdmin()=false` → 不短路 → 查 `scope2permission[SystemReadonly]` → 读动作命中放行；写动作位与全 0 → `xerror.WrapAuthorizateFailErrorWithMsg("Feature Access Deny")`（`authorization.go:118-120`），即现有 403 语义。

## 5. 敏感字段脱敏

**判定**（`model/iauth`，复用 §3 的 `Visitor` 辅助）：

```go
v, err := iauth.MustGetVisitor(ctx)
if err != nil { return nil, err }
readonly := v.IsSystemReadonly()
```

- `System` admin：`IsAdmin()=true` → 不掩码（现状不变）；
- `SystemReadonly`：`IsSystemReadonly()=true` → 掩码；
- `Support` token：对 APIKey/Provider/Token 等 Feature 只有 `ActionExport`（`features.go:246-258`），挂 Read 的端点本就 403，**到不了脱敏点**；
- inner-api 导出：不经过 openapi_v1 组装点，天然零改动。

**实现**：`lib/xmask/xmask.go`（新增，零依赖）：

```go
// Secret 对密钥类字符串做展示掩码，保留首尾少量字符以便运维定位归属。
func Secret(s string) string {
    n := len(s)
    switch {
    case n <= 4:
        return "********"
    case n <= 8:
        return s[:2] + "****" + s[n-2:]
    default:
        return s[:4] + "********" + s[n-4:]
    }
}
```

示例：`ak-2v8x9k3m7p` → `ak-2********k3m7p`；`sk-aaaaaaaaaaaa` → `sk-a********aaaa`；`Px2szn6R1HQo-WRSIJyt` → `Px2s********IJyt`；`abcd` → `********`；`abcdefg` → `ab****fg`。

**三个挂载点**：

- api-keys（`endpoints/openapi_v1/api_key/list.go:139-151`）：`newResponse` 已集中组装且详情 `one.go:62` 复用——补完 `RemainingQuota` 后追加：

```go
v, err := iauth.MustGetVisitor(ctx)
if err != nil { return nil, err }
if v.IsSystemReadonly() {
    for _, one := range list {
        if one.Key != nil { *one.Key = xmask.Secret(*one.Key) }
    }
}
```

  就地修改安全：`APIKeyParam` 为每次请求新构造对象，无跨请求共享。

- providers（`endpoints/openapi_v1/provider/list.go`、`one.go`）：新增包内 helper，list/one 返回前调用：

```go
func maskKeysIfReadonly(ctx context.Context, providers ...*iprovider.Provider) error {
    v, err := iauth.MustGetVisitor(ctx)
    if err != nil { return err }
    if !v.IsSystemReadonly() { return nil }
    for _, p := range providers {
        masked := make([]iprovider.ProviderKey, len(p.Keys)) // 浅拷贝 Keys 切片，避免写回共享底层数组
        copy(masked, p.Keys)
        for i := range masked { masked[i].Key = xmask.Secret(masked[i].Key) }
        p.Keys = masked
    }
    return nil
}
```

- tokens（`endpoints/openapi_v1/auth/token_list.go:34-46`、`token_one.go`）：两处 `newTokenData` 组装完成后：

```go
if v.IsSystemReadonly() {
    for _, t := range tokens { t.Token = xmask.Secret(t.Token) }
}
```

**session key 设计决策**（固化为契约，防未来改动引入泄露）：

- **登录响应**（`POST /auth/session-keys`）返回**本人** session key——**不脱敏**。理由：session key 是登录行为的直接结果，与"登录者持有自己的会话"等价，掩码即破坏登录功能本身；其强度由口令认证保障（bcrypt 已落地，见 `2026-10-06-password-hash`）。
- **用户列表/详情不得返回 session key**：`UserData.SessionKey`（`user_one.go:68`）现状即默认不填（`omitempty` 不出现），本期固化为契约并加测试断言——防未来改动误填；只读与 admin 视角一致（admin 同样看不到其他用户的 session key）。

**覆盖核对（防绕过）**：返回密钥类明文的 openapi 读端点共 6 个——`GET /api-keys`、`GET /api-keys/{id}`（共用 `newResponse`）、`GET /providers`、`GET /providers/{name}`、`GET /auth/tokens`、`GET /auth/tokens/{token_name}`。providers 的模型发现（`discover.go`）、api-keys 的 quota-plan 子资源读接口不返回密钥明文，无需处理；用户读接口不得返回 session key。清单随测试用例固化。

## 6. 前端适配（`ai-gateway-web`，另仓同发）

| 位置 | 现状 | 改为 |
|------|------|------|
| `authorize.js:61-63` | `!userData.is_admin → illegalAccess` | 改判 `role`：`system_admin`/`system_admin_readonly` 放行；**不做 `is_admin` 回退**（后端已废弃，回退无意义且重新引入歧义） |
| `store.js` | 登录态存 `is_admin`、`ADMIN_NAV_KEY='admin'` | 登录态改存 `role`；按 `role` 映射 nav key（`system_admin→admin`、`system_admin_readonly→admin_readonly`）；提供 `isReadonly()` getter |
| `nav_tree.toml`（api 仓配置） | 节点 `allowed_roles=["admin"]` | 查询类节点追加只读角色 key——只读角色菜单与 admin 一致（页面可读），写能力由后端 403 强制 |
| `User.vue`/`CreateUser.vue`/`zh.js` | 角色固定勾选/展示"系统管理员" | 角色单选（系统管理员 / 系统管理员（只读））；`com.sysAdminReadonly: '系统管理员（只读）'` |
| 核心列表页（APIKey、Provider） | 写入口常显 | `v-if="!isReadonly()"` 隐藏主写按钮（示范）；其余页面靠后端 403 + 统一错误提示，全量隐藏二期 |

## 7. 兼容矩阵与升级顺序

| 后端 | 前端 | 结果 |
|------|------|------|
| 新 | 新 | ✅ 全部目标达成 |
| 新 | 旧 | **控制台整体不可用**：`is_admin` 从响应消失，旧守卫 `!userData.is_admin` 对所有人恒为真 → 全部 `illegalAccess`。**禁止单独升级后端** |
| 旧 | 新 | 响应无 `role` 字段，新守卫不识别 → 同样不可用（不做回退） |

**本期前后端必须同发**：同一发布窗口升级 `ai-gateway-api`、`ai-gateway-web`，并更新 `nav_tree.toml`。

**回滚**：前后端一起回滚。回滚后若库中已存在 `SystemReadonly` 账号：旧代码下 `Admin=false`、`GetScopes()` 回退 `[Product]`——读接口被 `scope2permission[Product]` 部分放行、写接口拒绝，**不会获得越权写能力**；回滚窗口内建议直接删除只读账号或改回 `System`。

## 8. 测试计划

- `lib/xmask/xmask_test.go`（新增）：长度 0/1/4/5/8/9/100 边界；多字节字符不 panic（key 约定 ASCII，仍补防御用例）。
- `model/iauth` 增补：
  - `roleToScopes`/`scopesToRole` 双向映射与非法值报错；
  - `User.GetScopes()`：Scopes 非空按其返回；为空回退旧二值逻辑；
  - `HasScope`/`IsSystemReadonly`：admin（false）/只读（true）/Support token（false）；
  - **`scope2permission` 全 Feature 覆盖断言（防漏列，关键用例）**：遍历全部 Feature 常量，断言只读档均存在且 `IsAllowed(ActionRead)=true`、`IsAllowed(ActionUpdate|ActionCreate|ActionDelete|ActionExport|ActionCancel)=false`；
  - `UpdateUserRole`：storager 收到预期 scopes、操作日志成功/失败两路径；
  - `Authorizate` 端到端：只读 visitor 读放行、写返回 `Feature Access Deny`；admin 仍短路。
- 端点级：api_key/provider/tokens 脱敏三视角（只读掩码 / admin 明文 / Support token 403）；`user_create` 仅接受 `role`；旧 `is_admin` 端点 404。
- 集成/手工（curl 矩阵）：
  1. admin 创建只读账号（`POST /auth/users role=system_admin_readonly`）→ 登录返回 `role`（不含 `is_admin`）；
  2. 只读账号 6 个密钥读端点 → 200 且掩码（含分页/非分页两态）；
  3. 只读账号写接口（POST/PATCH/DELETE、quota reset、batch cancel、模型发现触发）→ 全部 403；
  4. 只读账号 `GET /report/*`、`GET /operation-logs`、`GET /auth/users` → 200（角色列可见、不可变更）；
  5. admin 视角重放 2-3：明文不变（回归）；
  6. `PATCH /auth/users/{name}/role` 只读→admin → 写能力恢复；
  7. inner-api 回归：导出配置中 api key/provider key 明文，BFE 转发鉴权正常；
  8. Support token（conf-agent）回归：导出通道 200 且明文；
  9. 废弃断言：用户响应均不含 `is_admin`；旧 `is_admin` 端点 404；`POST /auth/users` 携带 `is_admin` 被拒（提示用 `role`）；
  10. session key 断言：只读/admin 视角用户读接口无 `session_key`；登录响应仅含本人 `session_key`；
  11. 内置账号保护：`PATCH /auth/users/admin/role {"role":"system_admin_readonly"}` → 422；`GET /auth/users/admin` 复核 `role` 仍为 `system_admin`；大小写变体（`Admin`）同拒；admin 将普通用户降级/升级 → 200（回归）。
- 回归：`make test-model-cover-gate`（model 覆盖率 ≥70%）。

## 9. WBS

| # | 任务 | 改动 | 量 |
|---|------|------|----|
| 1 | `lib/xmask` | `xmask.go` + 单测 | 0.25 d |
| 2 | model/iauth 角色与权限档 | 常量、`User.Scopes` 贯通、`GetScopes` 回退、`HasScope`/`IsSystemReadonly`、`roleToScopes`/`scopesToRole`、`UpdateUserRole`、只读档全列举 + System 档补全 + 单测 | 1 d |
| 3 | 存储层 | `userD2M` 解析 scopes | 0.1 d |
| 4 | 校验与 auth 端点 | `validate.Role`/`Scope`、删 `IsAdmin`；`user_create`/`user_update_role`（新增）/`user_update_is_admin`（删）/`user_one`/`user_list`/`session_key_by_password` 的 `role` 适配 | 0.5 d |
| 5 | 脱敏三挂载点 | api_key `newResponse`、provider list/one、tokens list/one + 端点级单测 | 0.5 d |
| 6 | 前端 | 守卫/store/用户管理/nav_tree.toml/核心页写按钮 + 联调 | 1 d |
| 7 | 文档与集成回归 | api-define 三文档同步、认证授权机制.md、integration-test 用例迁移与新增 | 1 d |

后端合计约 2-3 人日；含前端与回归总计约 4-5 人日。

## 10. 风险与对策

| 风险 | 对策 |
|------|------|
| `GetScopes()` 行为变化影响面 | 空值回退兼容 + `make test` 全量回归 + §8 专门用例 |
| 只读档漏列 Feature 导致"该看的看不了" | 全 Feature 覆盖断言从机制上防漏列 |
| `ScopeSystem` 档补列后有人误删 `IsAdmin()` 短路 | 补列后与短路等价（语义已验证），风险不增 |
| 新旧版本组合可用性（is_admin 废弃） | 同发约束写入发布单并显著标注；禁止单独升级后端 |
| 外部脚本仍调用旧 `is_admin` 端点 | 404 + 迁移指引随变更记录发布 |
| admin 被降级导致管理面失联 | 内置账号保护：admin 的 role 固定 `system_admin`，model 层强制（决策 13） |
| 前端 `role` 适配遗漏页面 | 后端 403 兜底（唯一可信边界），前端隐藏仅为体验 |

## 11. 开放问题（实现前需评审确认）

1. **Export 动作是否给只读档**：报表 CSV 导出若挂 `ActionExport`，只读管理员将不能导出报表。倾向：本期不给（纯接口只读），有诉求时再按 Feature 细放。
2. **Token 只读档排期**：`validate.Scope` 增加 `SystemReadonly` 为 P1，可与本期同步或随后小版本放开。
3. **掩码保留位数**：前 4 后 4 是否足够识别又不泄露？倾向维持，长度不足时降级规则见 §5。
4. **`ScopeSystem` 档补列**：本期顺带补全（倾向）还是保持现状只做只读档？
