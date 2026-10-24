# API 变更说明：增加只读管理员（role 取代 is_admin + 密钥脱敏）

> 变更摘要与决策依据见同目录《change-summary.md》。本文只描述 API 契约变更，实现阶段按本文落
> `api-define/OpenAPI接口定义/`（六步法 Step 3）。
> 响应包装遵循 `00-common.md`（顶层 `ErrNum`/`ErrMsg`/`Data`，成功 `ErrNum=200`）。

---

## 1. 变更概览

| 变更 | Method | Path | 说明 |
|------|--------|------|------|
| 新增 | PATCH | `/open-api/v1/auth/users/{user_name}/role` | 变更用户角色（系统管理员 / 系统管理员（只读）） |
| 废弃 | PATCH | `/open-api/v1/auth/users/{user_name}/is_admin` | 移除（404）；迁移指引见 §5 |
| 修改 | POST | `/open-api/v1/auth/users` | 请求体 `is_admin` → `role`（必填） |
| 修改 | GET | `/open-api/v1/auth/users`、`/open-api/v1/auth/users/{user_name}`、`POST /open-api/v1/auth/session-keys` | 响应 `is_admin` → `role`；不含 `session_key`（契约见 §8） |
| 修改 | POST | `/open-api/v1/auth/tokens` | 请求 `scope` 枚举增加 `SystemReadonly`（P1，随排期） |
| 修改 | GET | `/open-api/v1/api-keys`、`/open-api/v1/api-keys/{id}` | 只读角色视角 `key` 掩码（§6） |
| 修改 | GET | `/open-api/v1/providers`、`/open-api/v1/providers/{name}` | 只读角色视角 `keys[].key` 掩码（§6） |
| 修改 | GET | `/open-api/v1/auth/tokens`、`/open-api/v1/auth/tokens/{token_name}` | 只读角色视角 `token` 掩码（§6） |

## 2. 公共约定

| 项 | 约定 |
|----|------|
| 角色值 | `role` 枚举：`system_admin`（系统管理员）/ `system_admin_readonly`（系统管理员（只读）） |
| 判定 | 响应是否脱敏由**服务端按登录身份**决定（scopes 含 `SystemReadonly` 且非 admin），调用方不可选择；admin 与现状一致（明文） |
| 掩码算法 | 长度 >8：前 4 + `********` + 后 4；4<长度≤8：前 2 + `****` + 后 2；长度≤4：`********`。纯展示用途，不可用于鉴权 |
| 错误码 | 无新增；写接口被拒沿用 `Authorizate Fail: Feature Access Deny`（既有语义） |

## 3. 用户模型变更：`is_admin` 废弃 → `role`

用户对象（登录响应、用户列表、用户详情）中 **`is_admin` 字段废弃，不再出现**；新增：

| 字段 | 类型 | 说明 |
|------|------|------|
| `role` | string | 角色，枚举 `system_admin` / `system_admin_readonly`；系统记录，创建时指定 |

- 存量数据（`users.scopes='System'`）经 `scopesToRole` 反推显示为 `system_admin`；
- 内部模型 `Admin`/`IsAdmin()` 保留（鉴权短路机制载体），但不再暴露于任何 API 响应；
- 只读管理员登录返回 `role=system_admin_readonly`（**无 `is_admin` 字段**）。

## 4. 角色管理端点

### 4.1 新增：PATCH `/auth/users/{user_name}/role`

| 项 | 值 |
|----|----|
| 鉴权 | `FeatureUser` 的 `update` 动作（仅系统管理员） |
| 请求体 | `{ "role": "system_admin" \| "system_admin_readonly" }` |
| 响应 | Data 为 null；角色变更写操作日志（recordUserOperation） |
| 错误 | 用户不存在 404；`role` 非法 422；无权限 402/403；**降级内置 admin 422**（见下） |

**内置账号保护**：`user_name` 为 `admin`（DB 种子内置账号，大小写不敏感）时，role 固定为 `system_admin`——请求将其变更为 `system_admin_readonly` 返回 422（`user "admin" role is fixed to system_admin`）。保证系统始终存在至少一个完整管理入口，防止误操作或账号被盗后降级 admin 导致管理面失联。

- 该限制是**本端点的业务规则**，与创建场景的保留名校验（`00-common.md` §12，防冒用创建）语义独立：reserved 名校验维持"仅创建场景"不变（见 `2026-10-09-issue-226-reserved-user-name-update-endpoints` 修复）；
- 校验落地在 model 层 `UpdateUserRole`（所有调用路径统一），端点透传错误。

### 4.2 废弃：PATCH `/auth/users/{user_name}/is_admin`

移除（404）。既有依赖（自动化脚本等）迁移至 §4.1。变更记录附迁移指引。

## 5. 创建用户：POST `/auth/users` 请求体变更

| 变更前 | 变更后 |
|--------|--------|
| `is_admin` bool（固定/默认 true） | `role` string，**必填**，枚举 `system_admin` / `system_admin_readonly` |

携带 `is_admin` 的请求被拒绝（422，提示使用 `role`）。

## 6. 密钥脱敏（6 个读端点）

对**系统管理员（只读）**视角，以下字段掩码返回；系统管理员视角明文不变（与现状一致）：

| 端点 | 字段 | 示例（明文 → 掩码） |
|------|------|----------------------|
| `GET /api-keys`、`GET /api-keys/{id}` | `key` | `ak-2v8x9k3m7p` → `ak-2********k3m7p` |
| `GET /providers`、`GET /providers/{name}` | `keys[].key` | `sk-aaaaaaaaaaaa` → `sk-a********aaaa` |
| `GET /auth/tokens`、`GET /auth/tokens/{token_name}` | `token` | `Px2szn6R1HQo-WRSIJyt` → `Px2s********IJyt` |

- tokens 创建（`POST /auth/tokens`）为写操作，只读角色鉴权层即被 403，**天然不可达明文**；
- `/inner-api/v1` 导出链路不受影响（Support scope 的 Export 通道，数据面消费明文 key）；
- 其余字段（name/id/scope/quota/余额等）原样返回。

## 7. Token 创建：scope 枚举扩展

`POST /auth/tokens` 请求体 `scope` 枚举由 `System` / `Support` 扩展为：

```
System / SystemReadonly / Support
```

`SystemReadonly` 为 P1（随排期与本期同步或随后小版本放开）；只读 token 与只读用户同权限口径（读放行 + 脱敏，写拒绝）。

## 8. session key 契约

| 接口 | 约定 |
|------|------|
| `POST /auth/session-keys`（登录） | 仅返回**本人** `session_key`（不脱敏，功能必需） |
| `GET /auth/users`、`GET /auth/users/{user_name}` | **不得包含** `session_key`（任何角色视角，含 admin；现状已满足，本文固化为契约） |

## 9. 兼容性

- **Breaking**：用户相关请求/响应的 `is_admin` 字段移除；旧 `is_admin` 端点 404。
- **升级约束**：前后端必须同发——新后端响应不含 `is_admin`，旧前端守卫对所有人恒拒绝（控制台整体不可用）。
- 无 DDL 变更、无数据迁移；存量 `scopes='System'` 数据显示为 `system_admin`。
- 无错误码新增；`00-common.md` 公共类型无需改动（`role` 为用户对象字段，非公共类型）。

## 10. auth.md 以外的文档落点

- `api-keys.md` / `providers.md`：读接口响应说明补"系统管理员（只读）视角下密钥字段为掩码值"及 §2 掩码算法。
- `auth.md`：用户模型（§3）、创建用户（§5）、角色变更端点（§4.1/§4.2）、Token scope 枚举（§7）、token 读接口脱敏说明（§6）、session key 契约（§8）。

## 11. 六步法 Step 3 清单

- [ ] `auth.md`：按 §3/§4/§5/§7/§8 修订
- [ ] `api-keys.md`、`providers.md`：按 §6/§10 补脱敏说明
- [ ] `00-common.md`：无需改动
