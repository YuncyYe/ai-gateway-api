# 批量与异步任务支持（一期）——接口变更

## 1. 变更范围

**OpenAPI 变更**：

- `POST /model-prices`、`PATCH /model-prices/{id}`、`GET /model-prices[/{id}]`：
  数据模型扩展（`mode` 新增枚举值 `batch`、新增 `batch_discount`）。
- `POST /rate-limit-policies`、`PATCH /rate-limit-policies/{id}`、
  `GET /rate-limit-policies[/{id}]`：数据模型扩展（新增可选 `batch_limits`）。
- 新增 batches 域：`GET /open-api/v1/batches`、`GET /open-api/v1/batches/{batch_id}`、
  `POST /open-api/v1/batches/{batch_id}/cancel`、`GET /open-api/v1/batch-files/{file_id}`。

**InnerAPI 变更**：

- `GET /inner-api/v1/configs/rate-limit-policy`：导出响应中每条策略 `rules`
  新增 `batch` 段（源为 `batch_limits`；为空则省略）。
- 集群 conf 导出（`ModelTable` 所在链路）：价格表新增 `mode=batch` 展开行
  （对消费方透明，接口形态不变）。

**向后兼容**：

- `batch_discount` / `batch_limits` 均为可选字段：存量数据（NULL）行为不变；
  旧 BFE 收到含 `rules.batch` 的导出文件忽略未知 JSON 字段，安全。
- 新增 `/batches`、`/batch-files` 端点为纯新增路径，不影响既有端点。
- 注意：`/rate-limit-policies` 更新为整体替换语义（同现有 tpm/rpm_configs），
  省略 `batch_limits` 等价于清空。

## 2. `/model-prices` 变更

### 2.1 请求/响应数据模型扩展

```json
{
    "provider": "openai",
    "model": "gpt-4o",
    "base_model": "gpt-4o",
    "mode": "batch",                 // 新增枚举值：batch
    "prices": {"input_cost_per_token": 0.00000125},
    "tier_prices": {...},
    "batch_discount": 0.5            // 新增（可选）：仅 mode=chat 等基础行可配，
                                     // 导出时展开为 mode=batch 行 = 各价格键 × 系数
}
```

### 2.2 字段说明

**表：`model_prices.batch_discount`**

| 参数名 | 类型 | 参数含义 | 必填 | 补充描述 | 合法性条件 |
| - | - | - | - | - | - |
| `batch_discount` | float | 批量价折扣系数 | N | 默认 0.5；导出时把本行各 token 价格键 × 系数展开生成 `(provider, model, mode=batch)` 价格行随 `ModelTable` 下发；tier 价同样展开 | (0, 1]；与手工 batch 行并存时手工行优先（导出告警） |

`mode` 白名单新增 `batch`（校验 `validate.go:28-42`）；`file` 不进价格体系
（文件操作不计费）。

## 3. `/rate-limit-policies` 变更

### 3.1 请求/响应数据模型扩展

```json
{
    "enabled": true,
    "max_concurrency": 50,
    "tpm_configs": [ ... ],
    "rpm_configs": [ ... ],
    "batch_limits": {                // 新增（可选）
        "max_create_rpm": 10,
        "max_active_batches": 5,
        "max_file_bytes": 104857600,
        "max_file_lines": 50000
    }
}
```

### 3.2 字段说明

**表：`rate_limit_policies.batch_limits`**

| 参数名 | 类型 | 参数含义 | 必填 | 补充描述 | 合法性条件 |
| - | - | - | - | - | - |
| `batch_limits` | object | 批量限流维度 | N | 省略/null = 该策略不参与批量限流；四个维度均按 apikey 计、与 model 无关 | 各维度 ≥ 0；0 = 不配此维度 |
| `max_create_rpm` | int | 单 apikey 批量创建 RPM | N | 计数器按策略独立（导出生成 `RL_BATCH_<policyId>_rpm`） | ≥ 0 |
| `max_active_batches` | int | 单 apikey 在途 batch 上限 | N | 数据面复用 `BATCH_ACTIVE` ZSET 基数校验 | ≥ 0 |
| `max_file_bytes` | int64 | 单文件字节上限 | N | 与数据面全局硬顶取 min；纯请求内校验 | ≥ 0 |
| `max_file_lines` | int | 单文件行数上限 | N | 同上 | ≥ 0 |

### 3.3 InnerAPI 导出（`/configs/rate-limit-policy`）

每条启用策略的 `rules` 内新增（为空省略）：

```jsonc
"rules": {
  "tpm": [ ... ],
  "rpm": [ ... ],
  "max_concurrency": 50,
  "batch": {                         // 源：batch_limits
    "max_create_rpm": 10,
    "max_active_batches": 5,
    "max_file_bytes": 104857600,
    "max_file_lines": 50000,
    "redis_key": "RL_BATCH_rlp-0001_rpm"
  }
}
```

## 4. 新增 batches 域

### 4.1 `GET /open-api/v1/batches`

批量任务列表。

**请求参数**（query）：

| 参数 | 类型 | 必填 | 说明 |
| - | - | - | - |
| `api_key_id` | string | N | 按 apikey 过滤 |
| `entity_id` | string | N | 按实体过滤 |
| `provider` | string | N | 按 provider 过滤 |
| `status` | string | N | 按状态过滤（枚举同 batch_tasks.status） |
| `start_time` / `end_time` | datetime | N | 创建时间窗 |
| `cursor` | int64 | N | 主键游标分页 |
| `limit` | int | N | 每页条数（默认 50，上限 200） |

`product_name` 由中间件强制注入，不接受外部传参。响应为 `batch_tasks` 行
列表 + next_cursor；金额字段为 1e-8 定点整数，单位 RMB。

> 2026-10-23 修订（issue #276）：上文"`product_name` 由中间件强制注入"的表述被取代，现行口径为服务端解析默认产品线 `AIRouteInnerProductName`（本组路由无产品路径变量、Token 不绑定产品线）；本条原表述保留作历史记录，见 `modifications/2026-10-23-issue-276-batch-product-name-contract-fix/`。

### 4.2 `GET /open-api/v1/batches/{batch_id}`

任务详情。查询顺序：DB → Redis `BATCH_TASK` 回源（job 落库前窗口）→ 404。
响应含：状态机、request_counts、用量（usage_input/output_tokens、
usage_source）、预留/结算/释放金额（reserve_units/settle_units）、
settle_status（reserved/settled/released）、over_reserved、关联
batch_files、terminal_at。

### 4.3 `POST /open-api/v1/batches/{batch_id}/cancel`

取消批量任务（控制面直调 provider）。

**行为**：DB 条件更新抢占（`WHERE status NOT IN (终态)`，affected_rows=0 →
409）→ 出网 POST provider cancel → 成功置 `cancelling` + Redis 幂等释放预留
→ operation_logs 审计。**错误码**：provider 不可达 502；provider 404；任务
已终态/他实例处理中 409。

**前置依赖**：控制面 → provider 出网（部署文档放通说明；降级部署退化为仅
展示）。

### 4.4 `GET /open-api/v1/batch-files/{file_id}`

文件元数据查询（file_id + provider 唯一定位；鉴权同 product 隔离）。

### 4.5 授权

新 feature/action（`iauth.FA`，如 `FeatureBatch: actionRead/actionCancel`），
挂到既有角色体系；全部写操作进 `operation_logs`。

## 5. 错误码

| 场景 | HTTP | 错误码 |
| - | - | - |
| 批量任务不存在（DB 与 Redis 均 miss） | 404 | `BATCH_NOT_FOUND` |
| cancel 时任务已终态 / 他实例处理中 | 409 | `BATCH_TERMINAL_OR_CONFLICT` |
| provider 不可达 | 502 | `UPSTREAM_UNREACHABLE` |
| provider 返回批量不存在 | 404 | `BATCH_NOT_FOUND`（透传 provider 语义） |
