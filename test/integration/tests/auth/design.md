# Auth 测试用例设计文档

## 1. 模块概述

Auth 模块负责管理用户（User）、Session Key、Token 及系统导航配置。v0.3.0 起用户 `role` 必填，枚举 system_admin / system_admin_readonly，Token `scope` 仅支持 `System`/`Support`，并删除 Product Scope 及产品线的绑定/查询接口。

## 2. 接口列表

| 编号 | 接口名称 | 方法 | 路径 | 说明 |
|------|----------|------|------|------|
| AUTH-1 | 创建用户 | POST | `/open-api/v1/auth/users` | `password`/`role` 必填，`role` 枚举 system_admin / system_admin_readonly |
| AUTH-2 | 删除用户 | DELETE | `/open-api/v1/auth/users/{user_name}` | - |
| AUTH-3 | 重置密码 | PATCH | `/open-api/v1/auth/users/{user_name}/passwd` | 管理员重置无需旧密码 |
| AUTH-4 | 用户列表 | GET | `/open-api/v1/auth/users` | 返回 user_name、role（不含 session_key） |
| AUTH-5 | 变更用户角色 | PATCH | `/open-api/v1/auth/users/{user_name}/role` | `role` 枚举 system_admin / system_admin_readonly；内置 admin 固定 system_admin（原 `PATCH .../is_admin` 已废弃） |
| AUTH-6 | 查询单个用户 | GET | `/open-api/v1/auth/users/{user_name}` | - |
| AUTH-7 | 创建 Session Key | POST | `/open-api/v1/auth/session-keys` | 用户名密码登录 |
| AUTH-8 | 删除 Session Key | DELETE | `/open-api/v1/auth/session-keys/{session_key}` | - |
| AUTH-9 | 创建 Token | POST | `/open-api/v1/auth/tokens` | `scope` 枚举 System / SystemReadonly / Support |
| AUTH-10 | 删除 Token | DELETE | `/open-api/v1/auth/tokens/{token_name}` | - |
| AUTH-11 | Token 详情 | GET | `/open-api/v1/auth/tokens/{token_name}` | 返回 name/token/scope |
| AUTH-12 | Token 列表 | GET | `/open-api/v1/auth/tokens` | 数组，元素同详情 |
| AUTH-13 | 获取系统导航配置 | GET | `/open-api/v1/meta` | 无需鉴权 |
| AUTH-14 | 密码哈希化（跨接口安全场景） | POST/PATCH/DB | `/open-api/v1/auth/users`、`/open-api/v1/auth/users/{user_name}/passwd`、`/open-api/v1/auth/session-keys` + `users` 表直读 | 口令 bcrypt 哈希落库、懒迁移、SKIP 后门移除、审计脱敏（2026-10-06 安全整改，见 `design-docs/modifications/2026-10-06-password-hash`） |
| AUTH-16 | 只读管理员（跨接口安全场景） | GET/POST/PATCH/DELETE | 用户登录 + api-keys / providers / auth/tokens / auth/users 等读接口与写接口 | 只读角色读放行、密钥字段掩码、写接口 403、用户读接口无 session_key（2026-10-12 变更，见 `design-docs/modifications/2026-10-12-readonly-admin`） |

## 3. 测试用例统计

| 接口 | 测试用例数 |
|------|-----------|
| 创建用户 | 13 |
| 删除用户 | 2 |
| 重置密码 | 4 |
| 用户列表 | 1 |
| 变更用户角色（原设置管理员） | 8 |
| 查询单个用户 | 2 |
| 创建 Session Key | 3 |
| 删除 Session Key | 2 |
| 创建 Token | 8 |
| 删除 Token | 2 |
| Token 详情 | 1 |
| Token 列表 | 1 |
| 获取系统导航配置 | 1 |
| 密码哈希化 | 6 |
| 只读管理员（跨接口场景） | 15 |
| **合计** | **69** |

## 4. 认证方式

测试环境配置 `SkipTokenValidate=true`：**无 `Authorization` 头的请求被视作完整管理员**（fakeVisitor(System)）；带 `Authorization: Session <session_key>`（或 `Token <token>`）头的请求会真实认证（`UserProbeAction` 优先解析请求头），因此只读管理员场景经 `testutil.LoginSessionKey` 登录后，用 `testutil.SetHeader("Authorization", "Session "+key)` 精确扮演。

注意：同一用户重复登录会覆盖服务端 session key（旧 key 立即失效），共享夹具的用例需使用最后一次登录返回的 key。

## 5. 目录结构

```
auth/
├── design.md
├── create_user/
│   └── create_user_test.go
├── delete_user/
│   └── delete_user_test.go
├── reset_password/
│   └── reset_password_test.go
├── list_users/
│   └── list_users_test.go
├── update_role/
│   └── update_role_test.go
├── user_detail/
│   └── user_detail_test.go
├── create_session_key/
│   └── create_session_key_test.go
├── delete_session_key/
│   └── delete_session_key_test.go
├── create_token/
│   └── create_token_test.go
├── delete_token/
│   └── delete_token_test.go
├── token_detail/
│   └── token_detail_test.go
├── token_list/
│   └── token_list_test.go
├── password_hash/
│   └── password_hash_test.go
├── readonly_admin/
│   └── readonly_admin_test.go
└── meta/
    └── meta_test.go
```

## 6. 创建用户

### 6.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 创建用户 |
| 方法 | POST |
| 路径 | `/open-api/v1/auth/users` |
| 说明 | 创建用户，`role` 必填（system_admin / system_admin_readonly） |

### 6.2 接口参数说明

#### 6.2.1 请求参数

##### Body 参数

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| user_name | string | Y | 用户名 | 长度 1-64；仅允许字母、数字、`_`、`-`、`.`；不能以 `.`、`-`、`_` 开头或结尾；全局唯一（大小写不敏感）；不能为 `admin`/`root`/`system` 等保留用户名 |
| password | string | Y | 用户密码 | 长度 8-72 字节（bcrypt 上限）；不能包含空白字符；不能等于 `user_name` 或其逆序；库内 bcrypt 哈希存储 |
| role | string | Y | 用户角色 | 必填；枚举值：`system_admin`、`system_admin_readonly` |

#### 6.2.2 返回数据字段

Data 为 null。

### 6.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-1-001 | 创建用户（完整参数） | 正常参数 | role=system_admin |
| AUTH-1-002 | 创建用户（缺少 role） | 正常参数 | 默认填充 true |
| AUTH-1-003 | 创建用户缺少 user_name | 必填校验 | 验证 ErrNum=422 |
| AUTH-1-004 | 创建用户缺少 password | 必填校验 | 验证 ErrNum=422 |
| AUTH-1-005 | 重复创建用户 | 业务规则 | 验证 ErrNum=555/556 |
| AUTH-1-006 | 非法 user_name（以 `-` 开头） | 合法性条件 | 验证 ErrNum=422 |
| AUTH-1-007 | 保留 user_name（`admin`） | 合法性条件 | 验证 ErrNum=422 |
| AUTH-1-008 | 密码过短 | 合法性条件 | 验证 ErrNum=422 |
| AUTH-1-009 | 密码包含空白字符 | 合法性条件 | 验证 ErrNum=422 |
| AUTH-1-010 | 密码与用户名相同 | 合法性条件 | 验证 ErrNum=422 |
| AUTH-1-011 | role 非法 | 合法性条件 | 验证 ErrNum=422 |

### 6.4 测试场景详细设计

#### 6.4.1 AUTH-1-001：创建用户（完整参数）

##### 设计思路

验证创建用户接口基本功能，`role=system_admin` 时创建成功。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求到 `/open-api/v1/auth/users`。
2. 验证返回成功，后续查询 `role=system_admin`。

##### 请求参数

```json
{
    "user_name": "test_user_001",
    "password": "password@123",
    "role": "system_admin"
}
```

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | null | IsNull |

---

#### 6.4.2 AUTH-1-002：创建用户（缺少 role）

##### 设计思路

验证缺少 `role` 时返回 422。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，Body 中不包含 `role`。
2. 验证返回成功，后续查询 `role=system_admin`。

##### 请求参数

```json
{
    "user_name": "test_user_002",
    "password": "password@123"
}
```

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | null | IsNull |

---

#### 6.4.3 AUTH-1-003：创建用户缺少 user_name（必填校验）

##### 设计思路

验证 `user_name` 为必填字段。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，Body 中缺少 `user_name`。
2. 验证返回错误码。

##### 请求参数

```json
{
    "password": "password@123"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 "user_name" 的错误信息  
**Data**：null

---

#### 6.4.4 AUTH-1-004：创建用户缺少 password（必填校验）

##### 设计思路

验证 `password` 为必填字段。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，Body 中缺少 `password`。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "test_user_003"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 "password" 的错误信息  
**Data**：null

---

#### 6.4.5 AUTH-1-005：重复创建用户（业务规则）

##### 设计思路

验证用户名全局唯一，重复创建时返回错误。

##### 前提数据准备

已创建 `test_user_dup`。

##### 执行步骤

1. 发送 POST 请求，使用重复的用户名。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "test_user_dup",
    "password": "password@123"
}
```

##### 预期返回结果

**ErrNum**：555 或 556  
**ErrMsg**：用户名已存在的错误信息  
**Data**：null

---

#### 6.4.6 AUTH-1-006：非法 user_name（以 `-` 开头）

##### 设计思路

验证 `user_name` 不能以 `-`、`.`、`_` 开头，否则返回参数校验错误。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`user_name` 以 `-` 开头。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "-baduser",
    "password": "password@123",
    "role": "system_admin"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 user_name 非法的错误信息  
**Data**：null

---

#### 6.4.7 AUTH-1-007：保留 user_name（`admin`）

##### 设计思路

验证保留用户名 `admin`/`root`/`system` 不允许创建。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`user_name=admin`。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "admin",
    "password": "password@123",
    "role": "system_admin"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 user_name 保留的错误信息  
**Data**：null

---

#### 6.4.8 AUTH-1-008：密码过短

##### 设计思路

验证 `password` 长度必须 ≥8 字符。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`password` 为 6 位字符。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "test_user_short",
    "password": "short1",
    "role": "system_admin"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 password 长度非法的错误信息  
**Data**：null

---

#### 6.4.9 AUTH-1-009：密码包含空白字符

##### 设计思路

验证 `password` 不能包含空白字符。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`password` 包含空格。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "test_user_space",
    "password": "pass word",
    "role": "system_admin"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 password 不能包含空白的错误信息  
**Data**：null

---

#### 6.4.10 AUTH-1-010：密码与用户名相同

##### 设计思路

验证 `password` 不能等于 `user_name` 或其逆序。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`password` 与 `user_name` 相同。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "myuser_2024",
    "password": "myuser_2024",
    "role": "system_admin"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 password 不能等于用户名的错误信息  
**Data**：null

---

#### 6.4.11 AUTH-1-011：role 非法

##### 设计思路

验证 `role` 必填，枚举 system_admin / system_admin_readonly，传入 `false` 时返回参数校验错误。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`role` 为非法值。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "test_user_admin_false",
    "password": "password@123",
    "role": "other"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 role 非法的错误信息  
**Data**：null

---

## 7. 删除用户

### 7.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 删除用户 |
| 方法 | DELETE |
| 路径 | `/open-api/v1/auth/users/{user_name}` |
| 说明 | 删除用户记录 |

### 7.2 接口参数说明

#### 7.2.1 请求参数

##### URI 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| user_name | string | Y | 待删除的用户名 |

#### 7.2.2 返回数据字段

Data 为 null。

### 7.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-2-001 | 删除用户 | 正常参数 | 删除成功，再次查询返回 404 |
| AUTH-2-002 | 删除不存在的用户 | 异常参数 | 验证 ErrNum=404 |

### 7.4 测试场景详细设计

#### 7.4.1 AUTH-2-001：删除用户（正常参数）

##### 设计思路

验证删除用户成功，并级联清理相关记录。

##### 前提数据准备

已创建用户 `test_user_del`。

##### 执行步骤

1. 发送 DELETE 请求到 `/open-api/v1/auth/users/test_user_del`。
2. 验证返回成功。
3. 再次查询该用户，验证返回 404。

##### 请求参数

URI：`test_user_del`

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | null | IsNull |

---

#### 7.4.2 AUTH-2-002：删除不存在的用户（异常参数）

##### 设计思路

验证删除不存在的用户时返回 404。

##### 前提数据准备

无

##### 执行步骤

1. 发送 DELETE 请求到 `/open-api/v1/auth/users/non_existent_user`。
2. 验证返回错误码。

##### 请求参数

URI：`non_existent_user`

##### 预期返回结果

**ErrNum**：404  
**ErrMsg**：用户不存在的错误信息  
**Data**：null

---

## 8. 重置密码

### 8.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 重置密码 |
| 方法 | PATCH |
| 路径 | `/open-api/v1/auth/users/{user_name}/passwd` |
| 说明 | 重置用户密码，管理员重置无需旧密码 |

### 8.2 接口参数说明

#### 8.2.1 请求参数

##### URI 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| user_name | string | Y | 待修改密码的用户名 |

##### Body 参数

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| old_password | string | N | 旧密码，修改当前登录用户时需要 | - |
| password | string | Y | 新密码 | 长度 8-72 字节（bcrypt 上限）；不能包含空白字符；不能等于 `user_name` 或其逆序；库内 bcrypt 哈希存储 |

#### 8.2.2 返回数据字段

Data 为 null。

### 8.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-3-001 | 管理员重置他人密码 | 正常参数 | 无需 old_password |
| AUTH-3-002 | 缺少 password | 必填校验 | 验证 ErrNum=422 |
| AUTH-3-003 | 密码过短 | 合法性条件 | 验证 ErrNum=422 |
| AUTH-3-004 | 修改不存在用户的密码 | 异常参数 | 验证 ErrNum=404 |

### 8.4 测试场景详细设计

#### 8.4.1 AUTH-3-001：管理员重置他人密码（正常参数）

##### 设计思路

验证管理员重置他人密码时无需旧密码。

##### 前提数据准备

已创建用户。

##### 执行步骤

1. 发送 PATCH 请求到 `/open-api/v1/auth/users/{user_name}/passwd`，传入新密码。
2. 验证返回成功。

##### 请求参数

```json
{
    "password": "newpassword@456"
}
```

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | null | IsNull |

---

#### 8.4.2 AUTH-3-002：缺少 password（必填校验）

##### 设计思路

验证 `password` 为必填字段。

##### 前提数据准备

已创建用户。

##### 执行步骤

1. 发送 PATCH 请求，Body 为空对象。
2. 验证返回错误码。

##### 请求参数

```json
{}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 "password" 的错误信息  
**Data**：null

---

#### 8.4.3 AUTH-3-003：密码过短（合法性条件）

##### 设计思路

验证重置密码时 `password` 长度必须 ≥8 字符。

##### 前提数据准备

已创建用户。

##### 执行步骤

1. 发送 PATCH 请求到 `/open-api/v1/auth/users/{user_name}/passwd`，传入过短密码。
2. 验证返回错误码。

##### 请求参数

```json
{
    "password": "short1"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 password 长度非法的错误信息  
**Data**：null

---

#### 8.4.4 AUTH-3-004：修改不存在用户的密码（异常参数）

##### 设计思路

验证修改不存在的用户密码时返回 404。

##### 前提数据准备

无

##### 执行步骤

1. 发送 PATCH 请求到 `/open-api/v1/auth/users/non_existent_user/passwd`。
2. 验证返回错误码。

##### 请求参数

URI：`non_existent_user`  
Body：
```json
{
    "password": "newpassword@456"
}
```

##### 预期返回结果

**ErrNum**：404  
**ErrMsg**：用户不存在的错误信息  
**Data**：null

---

## 9. 用户列表

### 9.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 用户列表 |
| 方法 | GET |
| 路径 | `/open-api/v1/auth/users` |
| 说明 | 返回所有用户列表 |

### 9.2 接口参数说明

#### 9.2.1 请求参数

无

#### 9.2.2 返回数据字段

Data 为数组，每个元素包含：

| 参数名 | 类型 | 说明 |
|--------|------|------|
| user_name | string | 用户名 |
| role | string | 用户角色：system_admin / system_admin_readonly |

### 9.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-4-001 | 用户列表 | 正常参数 | 返回数组，元素含 user_name、role；不含 session_key |

### 9.4 测试场景详细设计

#### 9.4.1 AUTH-4-001：用户列表（正常参数）

##### 设计思路

验证用户列表接口返回字段完整，且不包含 `products` 等已移除字段。

##### 前提数据准备

已创建用户。

##### 执行步骤

1. 发送 GET 请求到 `/open-api/v1/auth/users`。
2. 验证返回数组元素含 `user_name`、`role`，且不含 `session_key`。

##### 请求参数

无

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | 数组 | IsArray |
| Data[*].user_name | 非空字符串 | NotEmpty |
| Data[*].role | system_admin | Equals |
| Data[*].products | 不存在 | NotExists |

---

## 10. 变更用户角色

> 原「设置管理员」（`PATCH .../is_admin`）已随 `is_admin` 字段废弃而移除（旧端点 404，见 AUTH-5-007 验证）。本章为替代接口。

### 10.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 变更用户角色 |
| 方法 | PATCH |
| 路径 | `/open-api/v1/auth/users/{user_name}/role` |
| 说明 | 仅系统管理员可调用；**内置账号 `admin` 的 role 固定为 `system_admin`，不可降级**（防管理面失联） |

### 10.2 接口参数说明

#### 10.2.1 请求参数

##### URI 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| user_name | string | Y | 待变更角色的用户名（引用名校验，允许 `admin`） |

##### Body 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| role | string | Y | 枚举：`system_admin` / `system_admin_readonly` |

#### 10.2.2 返回数据字段

Data 为 null。角色变更写操作日志。

### 10.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-5-001 | 变更为只读管理员并复核 | 正常参数 | PATCH role=system_admin_readonly，GET 复核 role |
| AUTH-5-002 | 变更回系统管理员并复核 | 正常参数 | PATCH role=system_admin，GET 复核 role |
| AUTH-5-003 | 内置 admin 不可降级 | 业务规则 | PATCH admin→readonly 拒绝，GET 复核 role 仍 system_admin |
| AUTH-5-004 | 大小写变体同样不可降级 | 业务规则 | PATCH `Admin`→readonly 拒绝（EqualFold 匹配） |
| AUTH-5-005 | role 非法 | 合法性条件 | 422 |
| AUTH-5-006 | 用户不存在 | 异常场景 | 非 200 |
| AUTH-5-007 | 旧 is_admin 端点已废弃 | 废弃断言 | `PATCH .../is_admin` 不再成功（404） |
| AUTH-5-008 | 角色变更写操作日志 | 审计断言 | `resource_type=user, action=update`，change_summary 含新角色 |

### 10.4 测试场景详细设计

#### 10.4.1 AUTH-5-001~002：角色变更往返（正常参数）

##### 设计思路

验证角色可变更、可回退，且 GET 用户详情返回的 `role` 与变更一致。

##### 执行步骤

1. PATCH `/auth/users/{user_name}/role`，Body `{"role": "system_admin_readonly"}`。
2. GET `/auth/users/{user_name}` 断言 `role=system_admin_readonly`。
3. PATCH 改回 `system_admin`，GET 复核。

#### 10.4.2 AUTH-5-003~004：内置 admin 保护（业务规则）

##### 设计思路

内置账号 `admin` 是系统最后管理入口：任何将其降级为只读的请求必须被拒绝（大小写不敏感），且库内角色不变。

##### 预期返回结果

**ErrNum**：非 200（422 参数错误，`user "admin" role is fixed to system_admin`）  
复核：GET `/auth/users/admin` 的 `role` 仍为 `system_admin`。

---

## 11. 查询单个用户

### 11.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 查询单个用户 |
| 方法 | GET |
| 路径 | `/open-api/v1/auth/users/{user_name}` |
| 说明 | 查询单个用户详情 |

### 11.2 接口参数说明

#### 11.2.1 请求参数

##### URI 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| user_name | string | Y | 用户名 |

#### 11.2.2 返回数据字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| user_name | string | 用户名 |
| role | string | 用户角色：system_admin / system_admin_readonly |

### 11.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-6-001 | 查询单个用户 | 正常参数 | role=system_admin |
| AUTH-6-002 | 查询不存在用户 | 异常参数 | 验证 ErrNum=404 |

### 11.4 测试场景详细设计

#### 11.4.1 AUTH-6-001：查询单个用户（正常参数）

##### 设计思路

验证查询单个用户接口返回字段完整。

##### 前提数据准备

已创建用户 `test_user_001`。

##### 执行步骤

1. 发送 GET 请求到 `/open-api/v1/auth/users/test_user_001`。
2. 验证返回 `role=system_admin`。

##### 请求参数

URI：`test_user_001`

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| user_name | "test_user_001" | Equals |
| role | system_admin | Equals |

---

#### 11.4.2 AUTH-6-002：查询不存在用户（异常参数）

##### 设计思路

验证查询不存在的用户时返回 404。

##### 前提数据准备

无

##### 执行步骤

1. 发送 GET 请求到 `/open-api/v1/auth/users/non_existent_user`。
2. 验证返回错误码。

##### 请求参数

URI：`non_existent_user`

##### 预期返回结果

**ErrNum**：404  
**ErrMsg**：用户不存在的错误信息  
**Data**：null

---

## 12. 创建 Session Key

### 12.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 创建 Session Key |
| 方法 | POST |
| 路径 | `/open-api/v1/auth/session-keys` |
| 说明 | 使用账号密码得到 session key |

### 12.2 接口参数说明

#### 12.2.1 请求参数

##### Body 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| user_name | string | Y | 用户名 |
| password | string | Y | 用户密码 |

#### 12.2.2 返回数据字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| session_key | string | 会话密钥 |
| user_name | string | 用户名 |
| role | string | 用户角色：system_admin / system_admin_readonly |

### 12.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-7-001 | 正确登录创建 Session Key | 正常参数 | 返回 session_key、role=system_admin |
| AUTH-7-002 | 密码错误 | 异常参数 | 验证 ErrNum=401 |
| AUTH-7-003 | 缺少 user_name | 必填校验 | 验证 ErrNum=422 |

### 12.4 测试场景详细设计

#### 12.4.1 AUTH-7-001：正确登录创建 Session Key（正常参数）

##### 设计思路

验证使用正确用户名密码创建 Session Key 成功。

##### 前提数据准备

已创建用户 `test_user_login`，密码为 `password@123`。

##### 执行步骤

1. 发送 POST 请求到 `/open-api/v1/auth/session-keys`。
2. 验证返回 `session_key` 非空，`role=system_admin`。

##### 请求参数

```json
{
    "user_name": "test_user_login",
    "password": "password@123"
}
```

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| session_key | 非空字符串 | NotEmpty |
| user_name | "test_user_login" | Equals |
| role | system_admin | Equals |

---

#### 12.4.2 AUTH-7-002：密码错误（异常参数）

##### 设计思路

验证密码错误时返回 401。

##### 前提数据准备

已创建用户 `test_user_login`。

##### 执行步骤

1. 发送 POST 请求，传入错误密码。
2. 验证返回错误码。

##### 请求参数

```json
{
    "user_name": "test_user_login",
    "password": "wrong"
}
```

##### 预期返回结果

**ErrNum**：401  
**ErrMsg**：密码错误或登录失败的错误信息  
**Data**：null

---

#### 12.4.3 AUTH-7-003：缺少 user_name（必填校验）

##### 设计思路

验证 `user_name` 为必填字段。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，Body 中缺少 `user_name`。
2. 验证返回错误码。

##### 请求参数

```json
{
    "password": "password@123"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 "user_name" 的错误信息  
**Data**：null

---

## 13. 删除 Session Key

### 13.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 删除 Session Key |
| 方法 | DELETE |
| 路径 | `/open-api/v1/auth/session-keys/{session_key}` |
| 说明 | 删除 session key |

### 13.2 接口参数说明

#### 13.2.1 请求参数

##### URI 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| session_key | string | Y | 待删除的 session key |

#### 13.2.2 返回数据字段

Data 为 null。

### 13.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-8-001 | 删除 Session Key | 正常参数 | 删除成功 |
| AUTH-8-002 | 删除不存在 Session Key | 异常参数 | 验证 ErrNum=404 |

### 13.4 测试场景详细设计

#### 13.4.1 AUTH-8-001：删除 Session Key（正常参数）

##### 设计思路

验证删除 Session Key 成功。

##### 前提数据准备

已创建 Session Key。

##### 执行步骤

1. 发送 DELETE 请求到 `/open-api/v1/auth/session-keys/{session_key}`。
2. 验证返回成功。

##### 请求参数

URI：`<session_key>`

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | null | IsNull |

---

#### 13.4.2 AUTH-8-002：删除不存在 Session Key（异常参数）

##### 设计思路

验证删除不存在的 Session Key 时返回 404。

##### 前提数据准备

无

##### 执行步骤

1. 发送 DELETE 请求到 `/open-api/v1/auth/session-keys/non_existent_key`。
2. 验证返回错误码。

##### 请求参数

URI：`non_existent_key`

##### 预期返回结果

**ErrNum**：404  
**ErrMsg**：Session Key 不存在的错误信息  
**Data**：null

---

## 14. 创建 Token

### 14.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 创建 Token |
| 方法 | POST |
| 路径 | `/open-api/v1/auth/tokens` |
| 说明 | 创建 Token，`scope` 仅支持 System/Support |

### 14.2 接口参数说明

#### 14.2.1 请求参数

##### Body 参数

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| name | string | Y | Token 名称，全局唯一 | 长度 1-64；仅允许字母、数字、`_`、`-`、`.`；不能以 `.`、`-`、`_` 开头或结尾；全局唯一；不能为 `admin`/`system`/`default` 等保留名称；不能包含空白字符 |
| scope | string | Y | 权限范围：System / Support | 仅允许 `System`/`Support` |

#### 14.2.2 返回数据字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| token | string | Token 值 |

### 14.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-9-001 | 创建 System Token | 正常参数 | 返回非空 token |
| AUTH-9-002 | 创建 Support Token | 正常参数 | 返回非空 token |
| AUTH-9-003 | 创建 Token 缺少 name | 必填校验 | 验证 ErrNum=422 |
| AUTH-9-004 | 创建 Token 非法 scope | 异常参数 | Product 已移除 |
| AUTH-9-005 | 重复创建 Token | 业务规则 | 验证 ErrNum=555/556 |
| AUTH-9-006 | 非法 token name（以 `.` 开头） | 合法性条件 | 验证 ErrNum=422 |
| AUTH-9-007 | 保留 token name（`default`） | 合法性条件 | 验证 ErrNum=422 |
| AUTH-9-008 | token name 包含空白 | 合法性条件 | 验证 ErrNum=422 |

### 14.4 测试场景详细设计

#### 14.4.1 AUTH-9-001：创建 System Token（正常参数）

##### 设计思路

验证创建 System scope Token 成功。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求到 `/open-api/v1/auth/tokens`。
2. 验证返回 `token` 非空。

##### 请求参数

```json
{
    "name": "token_system_001",
    "scope": "System"
}
```

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| token | 非空字符串 | NotEmpty |

---

#### 14.4.2 AUTH-9-002：创建 Support Token（正常参数）

##### 设计思路

验证创建 Support scope Token 成功。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`scope=Support`。
2. 验证返回 `token` 非空。

##### 请求参数

```json
{
    "name": "token_support_001",
    "scope": "Support"
}
```

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| token | 非空字符串 | NotEmpty |

---

#### 14.4.3 AUTH-9-003：创建 Token 缺少 name（必填校验）

##### 设计思路

验证 `name` 为必填字段。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，Body 中缺少 `name`。
2. 验证返回错误码。

##### 请求参数

```json
{
    "scope": "System"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 "name" 的错误信息  
**Data**：null

---

#### 14.4.4 AUTH-9-004：创建 Token 非法 scope（异常参数）

##### 设计思路

验证 `scope` 仅支持 `System`/`Support`，`Product` 已移除。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`scope=Product`。
2. 验证返回错误码。

##### 请求参数

```json
{
    "name": "token_bad",
    "scope": "Product"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 "scope" 非法的错误信息  
**Data**：null

---

#### 14.4.5 AUTH-9-005：重复创建 Token（业务规则）

##### 设计思路

验证 Token name 全局唯一。

##### 前提数据准备

已创建同名 Token `token_dup`。

##### 执行步骤

1. 发送 POST 请求，使用重复的 name。
2. 验证返回错误码。

##### 请求参数

```json
{
    "name": "token_dup",
    "scope": "System"
}
```

##### 预期返回结果

**ErrNum**：555 或 556  
**ErrMsg**：Token 名称已存在的错误信息  
**Data**：null

---

#### 14.4.6 AUTH-9-006：非法 token name（以 `.` 开头）

##### 设计思路

验证 Token `name` 不能以 `.`、`-`、`_` 开头，否则返回参数校验错误。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`name` 以 `.` 开头。
2. 验证返回错误码。

##### 请求参数

```json
{
    "name": ".badtoken",
    "scope": "System"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 name 非法的错误信息  
**Data**：null

---

#### 14.4.7 AUTH-9-007：保留 token name（`default`）

##### 设计思路

验证保留 Token 名称 `admin`/`system`/`default` 不允许创建。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`name=default`。
2. 验证返回错误码。

##### 请求参数

```json
{
    "name": "default",
    "scope": "System"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 name 保留的错误信息  
**Data**：null

---

#### 14.4.8 AUTH-9-008：token name 包含空白

##### 设计思路

验证 Token `name` 不能包含空白字符。

##### 前提数据准备

无

##### 执行步骤

1. 发送 POST 请求，`name` 包含空格。
2. 验证返回错误码。

##### 请求参数

```json
{
    "name": "bad token",
    "scope": "System"
}
```

##### 预期返回结果

**ErrNum**：422  
**ErrMsg**：包含 name 不能包含空白的错误信息  
**Data**：null

---

## 15. 删除 Token

### 15.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 删除 Token |
| 方法 | DELETE |
| 路径 | `/open-api/v1/auth/tokens/{token_name}` |
| 说明 | 删除 Token |

### 15.2 接口参数说明

#### 15.2.1 请求参数

##### URI 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| token_name | string | Y | 待删除的 token name |

#### 15.2.2 返回数据字段

Data 为 null。

### 15.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-10-001 | 删除 Token | 正常参数 | 删除成功 |
| AUTH-10-002 | 删除不存在 Token | 异常参数 | 验证 ErrNum=404 |

### 15.4 测试场景详细设计

#### 15.4.1 AUTH-10-001：删除 Token（正常参数）

##### 设计思路

验证删除 Token 成功。

##### 前提数据准备

已创建 Token `token_system_001`。

##### 执行步骤

1. 发送 DELETE 请求到 `/open-api/v1/auth/tokens/token_system_001`。
2. 验证返回成功。

##### 请求参数

URI：`token_system_001`

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | null | IsNull |

---

#### 15.4.2 AUTH-10-002：删除不存在 Token（异常参数）

##### 设计思路

验证删除不存在的 Token 时返回 404。

##### 前提数据准备

无

##### 执行步骤

1. 发送 DELETE 请求到 `/open-api/v1/auth/tokens/non_existent_token`。
2. 验证返回错误码。

##### 请求参数

URI：`non_existent_token`

##### 预期返回结果

**ErrNum**：404  
**ErrMsg**：Token 不存在的错误信息  
**Data**：null

---

## 16. Token 详情

### 16.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | Token 详情 |
| 方法 | GET |
| 路径 | `/open-api/v1/auth/tokens/{token_name}` |
| 说明 | 查询 Token 详情 |

### 16.2 接口参数说明

#### 16.2.1 请求参数

##### URI 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| token_name | string | Y | token name |

#### 16.2.2 返回数据字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| name | string | token 名字 |
| token | string | token 的值 |
| scope | string | scope |

### 16.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-11-001 | Token 详情 | 正常参数 | 返回 name/token/scope，无 product_name |

### 16.4 测试场景详细设计

#### 16.4.1 AUTH-11-001：Token 详情（正常参数）

##### 设计思路

验证 Token 详情接口返回字段完整，且不包含 `product_name`。

##### 前提数据准备

已创建 Token `token_system_001`。

##### 执行步骤

1. 发送 GET 请求到 `/open-api/v1/auth/tokens/token_system_001`。
2. 验证返回字段。

##### 请求参数

URI：`token_system_001`

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| name | "token_system_001" | Equals |
| token | 非空字符串 | NotEmpty |
| scope | "System" | Equals |
| product_name | 不存在 | NotExists |

---

## 17. Token 列表

### 17.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | Token 列表 |
| 方法 | GET |
| 路径 | `/open-api/v1/auth/tokens` |
| 说明 | 返回所有 Token 列表 |

### 17.2 接口参数说明

#### 17.2.1 请求参数

无

#### 17.2.2 返回数据字段

Data 为数组，元素同 Token 详情。

### 17.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-12-001 | Token 列表 | 正常参数 | 数组元素不含 product_name |

### 17.4 测试场景详细设计

#### 17.4.1 AUTH-12-001：Token 列表（正常参数）

##### 设计思路

验证 Token 列表接口返回字段完整，且不包含 `product_name`。

##### 前提数据准备

已创建 Token。

##### 执行步骤

1. 发送 GET 请求到 `/open-api/v1/auth/tokens`。
2. 验证返回数组元素字段。

##### 请求参数

无

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| Data | 数组 | IsArray |
| Data[*].name | 非空字符串 | NotEmpty |
| Data[*].token | 非空字符串 | NotEmpty |
| Data[*].scope | System 或 Support | InEnum |
| Data[*].product_name | 不存在 | NotExists |

---

## 18. 获取系统导航配置

### 18.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 获取系统导航配置 |
| 方法 | GET |
| 路径 | `/open-api/v1/meta` |
| 说明 | 获取系统导航/图标/Logo 配置，无需鉴权 |

### 18.2 接口参数说明

#### 18.2.1 请求参数

无

#### 18.2.2 返回数据字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| nav | object | 导航配置 |
| icon | object | 图标配置 |
| logo | object | Logo 配置 |

### 18.3 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-13-001 | 获取系统导航配置 | 正常参数 | 返回 nav/icon/logo |

### 18.4 测试场景详细设计

#### 18.4.1 AUTH-13-001：获取系统导航配置（正常参数）

##### 设计思路

验证获取系统导航配置接口基本功能，无需鉴权。

##### 前提数据准备

无

##### 执行步骤

1. 发送 GET 请求到 `/open-api/v1/meta`。
2. 验证返回结构。

##### 请求参数

无

##### 预期返回结果

**ErrNum**：200  
**ErrMsg**：success

**Data 字段校验**：

| 字段 | 预期值 | 校验方式 |
|------|--------|---------|
| nav | 对象 | IsObject |
| icon | 对象 | IsObject |
| logo | 对象 | IsObject |

---

## 19. 密码哈希化（AUTH-14）

### 19.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Auth |
| 接口名称 | 密码哈希化（跨接口安全场景组） |
| 方法 | POST / PATCH + DB 直读 |
| 路径 | `/open-api/v1/auth/users`、`/open-api/v1/auth/users/{user_name}/passwd`、`/open-api/v1/auth/session-keys` |
| 说明 | 2026-10-06 安全整改（`design-docs/modifications/2026-10-06-password-hash`）：口令 bcrypt 哈希存储、移除 `"SKIP"` 后门、存量明文懒迁移、审计脱敏。本组用例复用 AUTH-1/3/7 接口并以 testutil 直读/改写 SQLite `users` 表做存储断言。 |

### 19.2 测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AUTH-14-001 | DDL 种子 admin 口令哈希 | 存储断言（DB 直读） | `users.password` 为 `$2` 前缀 bcrypt 哈希；admin/admin 登录 200 |
| AUTH-14-002 | 创建用户落库哈希 | 存储断言（DB 直读） | DB password 非明文且 `$2` 前缀；错误密码登录 401 |
| AUTH-14-003 | 存量明文懒迁移 | 迁移行为 | DB 置回明文 → 登录 200 → DB 变 `$2` 前缀 → 同口令再次登录 200（哈希路径） |
| AUTH-14-004 | SKIP 字面量不再旁路 | 安全回归 | 密码 `"SKIP"` 登录 401 |
| AUTH-14-005 | 73 字节密码拒绝且回读零变更 | 边界值 | 创建 422 且 DB 无该用户；改密 422 且 DB password 与操作前一致 |
| AUTH-14-006 | 重置密码落库哈希且审计脱敏 | 存储断言 + 审计 | 改密后 DB 为 `$2` 前缀、新密码可登录旧密码 401；操作日志整段 JSON 不含新旧密码明文 |

### 19.3 测试场景详细设计

#### 19.3.1 AUTH-14-001：DDL 种子 admin 口令哈希（存储断言）

##### 设计思路

新装环境由 `db_ddl_sqlite.sql` 初始化，admin 初始口令 `admin` 必须以 bcrypt 哈希落库（库内无明文），且初始口令仍可登录（快速开始体验不变）。

##### 前提数据准备

无（依赖 DDL 种子数据）。

##### 执行步骤

1. testutil.GetUserPassword 直读 `users` 表 admin 记录：断言 `$2a$`/`$2b$`/`$2y$` 前缀。
2. POST `/open-api/v1/auth/session-keys`，`user_name=admin`、`password=admin`：断言 200。

##### 预期返回结果

**DB 断言**：`users.password` 为 bcrypt 哈希串（60 字符，非明文 `admin`）。
**ErrNum**：200

---

#### 19.3.2 AUTH-14-002：创建用户落库哈希（存储断言）

##### 设计思路

创建用户后，库中 password 必须为 bcrypt 哈希（不是请求明文），错误密码登录被拒。

##### 前提数据准备

无

##### 执行步骤

1. POST `/open-api/v1/auth/users` 创建用户（密码 `password@123`）：断言 200。
2. GetUserPassword 直读：断言为 `$2` 前缀且不等于明文。
3. POST `/open-api/v1/auth/session-keys` 用错误密码登录：断言 401。
4. 清理：删除该用户。

##### 请求参数

```json
{
    "user_name": "<run-scoped>",
    "password": "password@123",
    "role": "system_admin"
}
```

##### 预期返回结果

**ErrNum**：200（创建）/ 401（错误密码登录）
**DB 断言**：`users.password` 以 `$2` 前缀存储。

---

#### 19.3.3 AUTH-14-003：存量明文懒迁移（迁移行为）

##### 设计思路

模拟老版本升级后的存量明文库：将用户 password 直写为明文后，登录成功的同时必须在同一事务内重哈希落库；迁移后同一口令走哈希校验路径仍可登录。

##### 前提数据准备

已创建用户（密码 `password@123`）。

##### 执行步骤

1. testutil.SetUserPassword 将该用户 password 改写为明文 `password@123`（模拟存量库）。
2. POST `/open-api/v1/auth/session-keys` 登录：断言 200。
3. GetUserPassword 直读：断言已变为 `$2` 前缀（懒迁移生效）。
4. 再次以 `password@123` 登录：断言 200（哈希校验路径，口令本身不变）。
5. 清理：删除该用户。

##### 预期返回结果

**ErrNum**：200（两次登录）
**DB 断言**：步骤 1 后为明文，步骤 3 后为 bcrypt 哈希。

---

#### 19.3.4 AUTH-14-004：SKIP 字面量不再旁路（安全回归）

##### 设计思路

历史上 `authentication.go:259` 存在 `"SKIP"` 硬编码后门（密码字面量等于 SKIP 即跳过校验）。整改后 `"SKIP"` 必须是普通错误口令。

##### 前提数据准备

已创建用户。

##### 执行步骤

1. POST `/open-api/v1/auth/session-keys`，`password="SKIP"`：断言 401。
2. 清理：删除该用户。

##### 请求参数

```json
{
    "user_name": "<run-scoped>",
    "password": "SKIP"
}
```

##### 预期返回结果

**ErrNum**：401
**Data**：null

---

#### 19.3.5 AUTH-14-005：73 字节密码拒绝且回读零变更（边界值）

##### 设计思路

bcrypt 输入上限 72 字节，合同收紧为 8-72 字节。73 字节密码创建/改密必须 422，且拒绝路径不留任何写入（校验先于持久化）。

##### 前提数据准备

无（创建路径）；已创建用户（改密路径）。

##### 执行步骤

1. POST `/open-api/v1/auth/users`，`password=<73 个 'a'>`：断言 422；GetUserPassword 直读断言该用户不存在（回读零变更）。
2. 创建合法用户，记录其 password 存储值 H1。
3. PATCH `/open-api/v1/auth/users/{user_name}/passwd`，`password=<73 个 'a'>`：断言 422；GetUserPassword 直读断言仍为 H1（回读零变更）。
4. 清理：删除该用户。

##### 预期返回结果

**ErrNum**：422（两次）
**DB 断言**：创建拒绝后无记录；改密拒绝后存储值逐字节不变。

---

#### 19.3.6 AUTH-14-006：重置密码落库哈希且审计脱敏（存储断言 + 审计）

##### 设计思路

管理员重置密码后：库中为新口令的 bcrypt 哈希；新口令可登录、旧口令失效；操作日志整段 JSON 不得出现新旧口令明文（`MaskSensitiveFields` 脱敏为 `******`）。

##### 前提数据准备

已创建用户（旧密码 `oldpassword@123`）。

##### 执行步骤

1. PATCH `/open-api/v1/auth/users/{user_name}/passwd`，`password=newpassword@456`：断言 200。
2. GetUserPassword 直读：断言为 `$2` 前缀。
3. 旧密码登录：断言 401；新密码登录：断言 200。
4. testutil.WaitForOperationLog 查询该用户的 update 操作日志，将整条日志 JSON 序列化：断言不含 `newpassword@456` 与 `oldpassword@123` 明文。
5. 清理：删除该用户。

##### 预期返回结果

**ErrNum**：200（改密/新密码登录）/ 401（旧密码登录）
**DB 断言**：`users.password` 为 `$2` 前缀。
**审计断言**：日志 JSON 不含新旧口令明文。

---

## 20. 只读管理员（AUTH-16，跨接口场景）

> 对应实现：`design-docs/modifications/2026-10-12-readonly-admin`。验证"系统管理员（只读）"（`system_admin_readonly` / `SystemReadonly` scope）的端到端行为：读放行、密钥掩码、写拒绝、session key 契约。

### 20.1 测试场景总览

| 编号 | 场景 | 说明 |
|------|------|------|
| AUTH-16-001 | 登录返回只读角色 | 登录响应 `role=system_admin_readonly` |
| AUTH-16-002/003 | 用户列表、操作日志读放行 | 只读角色可读 |
| AUTH-16-010~013 | api-keys/providers/tokens 列表与详情掩码 | 只读视角密钥字段掩码（固定 key 夹具断言精确掩码值） |
| AUTH-16-014/015 | tokens 列表/详情掩码 | 随机 token 按掩码规则断言 |
| AUTH-16-016 | admin 视角明文回归 | 无头请求（Skip=完整管理员）必须看到明文 |
| AUTH-16-020~023 | 写接口拒绝矩阵 | 创建 api-key / 变更角色 / 删除 token / 删除 provider 均为受控 402 `Feature Access Deny`，且**拒绝后回读零变更**（技能清单检查项 3：负向用例必带回读断言） |
| AUTH-16-030~032 | session key 与废弃字段契约 | 用户读接口（只读/admin 视角）无 `session_key`、无 `is_admin` |
| AUTH-16-040 | inner-api 导出明文回归 | 只读用户读 api-key 触发脱敏后，`/inner-api/v1/configs/mod-api-key` 导出仍含明文（脱敏仅作用于 openapi 层） |
| AUTH-16-033 | 掩码值形态确认 | 掩码值≠明文且含掩码标记，不可用于鉴权 |

### 20.2 夹具与身份扮演

- 前置（`setupOnce`，包级共享）：创建只读用户 → 登录拿 session key；创建自定义 key 的 api-key、provider（默认 `sk-aaaaaaaaaaaa`）、token 作为被读对象。
- 身份扮演：`testutil.SetHeader("Authorization", "Session "+key)`；夹具清理挂在 `TestMain`（m.Run 之后），不可用 `t.Cleanup`（会在首个用例函数结束时误删共享用户）。
- 注意：同一用户重复登录会覆盖服务端 session key——AUTH-16-001 登录后须将包级 `readonlyKey` 更新为本次返回的 key。

---

## 21. 依赖与数据准备

1. 用户、Token、Session Key 均使用全局唯一名称，测试用例间需避免冲突。
2. Session Key 用例依赖预先创建的用户记录。
3. 测试环境 `SkipTokenValidate=true`：无认证头请求视作完整管理员；只读管理员用例（AUTH-16）经真实登录 + `Authorization: Session` 头扮演。
4. AUTH-14 组依赖 testutil 直读/改写 SQLite `users` 表（`GetUserPassword`/`SetUserPassword`），仅适用于集成测试的 SQLite 环境；SetUserPassword 用于模拟存量明文库，用例间互不影响（run-scoped 用户名）。
5. testutil 新增：`CreateUserWithRole`（指定角色建号）、`LoginSessionKey`（登录取 key）、`SetHeader`/`ClearHeaders`（请求头扮演）。

## 22. 注意事项

1. v0.3.0 已删除以下接口，测试方案不再覆盖：
   - `POST/DELETE /auth/users/{user_name}/products/{product_name}`
   - `GET /auth/users/actions/search-by-product/{product_name}`
   - `GET /auth/tokens/actions/search-by-product/{product_name}`
2. 用户 `role` 必填，枚举 `system_admin` / `system_admin_readonly`；`is_admin` 字段已废弃（请求携带按缺少 role 处理，响应不再返回该字段）。
3. Token `scope` 支持 `System` / `SystemReadonly` / `Support`，不再接受 `Product` 或 `product_name`。
4. 用户列表、Token 列表、Token 详情均不再返回 `products`/`product_name`；用户读接口不返回 `session_key`。
5. 测试环境 `SkipTokenValidate=true`，无认证头的请求视作完整管理员；带 `Authorization` 头的请求按头真实认证。
6. AUTH-14 组断言以合同（`design-docs/api-define/OpenAPI接口定义/00-common.md` Password 类型：8-72 字节、bcrypt 哈希存储）与整改方案（`design-docs/modifications/2026-10-06-password-hash`）为依据；口令长度合同已由 8-128 字符修订为 8-72 字节，AUTH-14-005 锁定该新语义。
