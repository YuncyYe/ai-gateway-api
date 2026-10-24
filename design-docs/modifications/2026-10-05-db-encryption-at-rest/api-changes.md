# API 变更说明：DB 落盘加密收敛任务（security）

> 变更摘要与总体设计见同目录《change-summary.md》《design-changes.md》。
> 本文只描述 API 契约变更：**新增 2 个端点，无修改/删除既有端点**。
> 响应包装遵循 `api-define/OpenAPI接口定义/00-common.md`（顶层 `ErrNum`/`ErrMsg`/`Data`，成功 `ErrNum=200`）。
> 实现阶段按本文落 `api-define/OpenAPI接口定义/security.md`（六步法 Step 3）。

---

## 1. 变更概览

| 变更 | Method | Path | 说明 |
|------|--------|------|------|
| 新增 | POST | `/open-api/v1/security/reencrypt-sweeps` | 触发收敛任务（异步，202 返回） |
| 新增 | GET | `/open-api/v1/security/reencrypt-sweeps/{task_id}` | 查询任务状态与进度 |

命名取复数集合：POST 在集合上创建任务、GET 读取集合中单个任务，与 `/api-keys`、`/entities`、`/operation-logs` 惯例一致。

## 2. 公共约定

| 项 | 约定 |
|----|------|
| 鉴权 | 管理面 Session/Token（既有体系）；授权新增 Feature `FeatureSecurity`（scope=System）：POST 需 `update` 动作，GET 需 `read` 动作；不足返回 402 |
| 审计 | 触发与任务完成均写 `operation_logs`（参数含 `mode`/`dry_run`/`scope` 与结果计数；**不含任何密钥或密文材料**） |
| 幂等 | 任务整体幂等：完成后可重触发（空跑 `rewritten=0` 即收敛确认）；运行中重复触发返回 409 不排队 |
| 多实例 | 任务状态持久化于 DB（`keyrotate_sweep_tasks`），任意实例均可查询任意任务 |

## 3. POST /open-api/v1/security/reencrypt-sweeps

触发收敛任务。任务异步执行，响应 202 后立即返回，进度经查询端点轮询。

### 请求体（JSON，全可选）

| 字段 | 类型 | 必填 | 默认 | 合法性条件 | 说明 |
|------|------|------|------|------------|------|
| `mode` | string | N | `reencrypt` | 仅 `reencrypt` \| `decrypt` | 行级变换方向：`reencrypt`=非目标形态行（明文/旧 keyID 密文）重写为 active 钥密文（轮换收敛/初始启用）；`decrypt`=所有密文行解密为明文（**仅用于回滚预案**） |
| `dry_run` | bool | N | `false` | - | `true` 只扫描统计不写库；正式执行前必须先 dry-run 核对 |
| `scope` | string | N | `all` | 仅 `all` \| `providers` \| `api_keys` | 限定扫描表 |

### 成功响应（HTTP 202，`ErrNum=200`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `task_id` | string | 任务标识（查询端点入参） |
| `status` | string | 恒为 `running` |
| `mode` | string | 回显请求 mode |
| `dry_run` | bool | 回显请求 dry_run |
| `scope` | string | 回显请求 scope |
| `active_key_id` | int | **触发瞬间内存 keyring 的 active 钥**；触发即可核对"重写目标钥"，防"改 keyring 文件但漏 `/reload/security`"型静默失败；`decrypt` 模式返回 0 |

```json
{
  "ErrNum": 200, "ErrMsg": "success",
  "Data": {
    "task_id": "rsp-01J8XQ…", "status": "running",
    "mode": "reencrypt", "dry_run": false, "scope": "all", "active_key_id": 2
  }
}
```

### 错误码

| HTTP/ErrNum | 场景 | ErrMsg 归因 |
|-------------|------|-------------|
| 401 | 未认证 | 既有鉴权语义 |
| 402 | 无 FeatureSecurity 权限 | "Authorizate Fail: Feature Access Deny" |
| 409 | 已有任务在运行（集群级单例互斥） | 含持有者 `task_id`，调用方直接轮询该任务 |
| 422 | `mode`/`scope` 非法 | 字段级归因 |

## 4. GET /open-api/v1/security/reencrypt-sweeps/{task_id}

查询任务状态与实时进度。

### 路径参数

| 参数 | 类型 | 合法性条件 |
|------|------|------------|
| `task_id` | string | 非空；POST 响应获得 |

### 成功响应（HTTP 200）

| 字段 | 类型 | 说明 |
|------|------|------|
| `task_id` | string | 任务标识 |
| `status` | string | `running` \| `succeeded` \| `failed` |
| `mode` | string | 任务创建时的 mode |
| `dry_run` | bool | 是否 dry-run |
| `scope` | string | 扫描范围 |
| `active_key_id` | int | 任务启动时的 active 钥（重放判定依据） |
| `started_at` / `finished_at` | string(datetime) | 起止时间；running 时 finished_at 为空 |
| `duration_ms` | int | 任务耗时（running 时为当前已耗时） |
| `summary` | object | 按表分组：`{ providers: {scanned, rewritten, skipped}, api_keys: {…} }`；`skipped`=已是目标形态，`rewritten` 在 dry_run 下为"将要重写"数 |
| `error` | string | `failed` 时的可归因错误（**不含密文/密钥**）；其余状态为空串 |

```json
{
  "ErrNum": 200, "ErrMsg": "success",
  "Data": {
    "task_id": "rsp-01J8XQ…", "status": "succeeded",
    "mode": "reencrypt", "dry_run": false, "scope": "all", "active_key_id": 2,
    "started_at": "2026-10-05T10:00:00Z", "finished_at": "2026-10-05T10:00:08Z",
    "duration_ms": 8300,
    "summary": {
      "providers": { "scanned": 120,  "rewritten": 118,  "skipped": 2 },
      "api_keys":  { "scanned": 2400, "rewritten": 2390, "skipped": 10 }
    },
    "error": ""
  }
}
```

### 错误码

| HTTP/ErrNum | 场景 |
|-------------|------|
| 401 / 402 | 同 POST |
| 404 | `task_id` 不存在 |

## 5. 枚举值定义

| 枚举 | 取值 | 语义 |
|------|------|------|
| `mode` | `reencrypt`（默认） | 收敛到 active 钥密文：覆盖初始启用（明文→密文）与轮换收敛（旧 keyID→active keyID） |
| `mode` | `decrypt` | 全部密文行解密为明文、剥离 marker；**仅回滚预案使用**，审计重点标注 |
| `scope` | `all` / `providers` / `api_keys` | 扫描表范围 |
| `status` | `running` / `succeeded` / `failed` | 任务状态；`failed` 含 `executor lost`（执行实例失联被接管） |

## 6. 兼容性

- 无既有端点路径、字段、返回值变更；`00-common.md` 错误码表无需新增（409/422/404 为既有语义）。
- 任务记录持久化于 `keyrotate_sweep_tasks` 表（DDL 见 design-changes.md §5），历史保留不 TTL。
