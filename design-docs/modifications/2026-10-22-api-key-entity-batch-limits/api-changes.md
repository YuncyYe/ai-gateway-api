# API-Key / Entity 内联限流策略补齐 batch_limits——接口变更

## 1. 变更范围

| 文档 | 位置 | 变更 |
|------|------|------|
| `api-keys.md` | §2.3 创建请求示例（`POST /api-keys`） | `rate_limit_policy.rules` 增加 `batch_limits` 段 |
| `api-keys.md` | 约束条款（创建/PUT/PATCH 各一处） | 增加 `batch_limits` 批量限流口径说明 |
| `entities.md` | §2.3 创建请求示例（`POST /entities`） | 同上 |
| `entities.md` | 约束条款（创建/PUT/PATCH 各一处） | 同上（按发起请求的 API-Key 计数口径） |
| `00-common.md` | §9 `rules` 必填口径 | "三者至少配置其一"更正为"四者"（补计 `batch_limits`） |

`batch_limits` 字段本身的定义（四个维度、合法性条件、导出映射）已在
`00-common.md` §9 与 `rate-limit-policy.md` 中定义，本次不重复展开。

## 2. `api-keys.md` 变更

### 2.1 创建请求示例（§2.3）

变更前：

```json
"rate_limit_policy": {
    "enabled": true,
    "rules": {
        "tpm": [
            {"name": "tpm_1min", "model": "*", "window_minutes": 1, "max_tokens": 10000, "step_minutes": 1}
        ],
        "rpm": [
            {"name": "rpm_1min", "model": "*", "window_minutes": 1, "max_requests": 100}
        ],
        "max_concurrency": 50
    }
}
```

变更后：

```json
"rate_limit_policy": {
    "enabled": true,
    "rules": {
        "tpm": [
            {"name": "tpm_1min", "model": "*", "window_minutes": 1, "max_tokens": 10000, "step_minutes": 1}
        ],
        "rpm": [
            {"name": "rpm_1min", "model": "*", "window_minutes": 1, "max_requests": 100}
        ],
        "max_concurrency": 50,
        "batch_limits": {
            "max_create_rpm": 60,
            "max_active_batches": 10,
            "max_file_bytes": 104857600,
            "max_file_lines": 1000000
        }
    }
}
```

### 2.2 约束条款（创建/PUT/PATCH 三处约束列表各追加一条）

新增：

> - `rate_limit_policy.rules` 中的 `batch_limits` 段（批量任务限流维度）与独立策略资源一致生效：四维度均按发起请求的 API-Key 计数、与 `model` 无关，省略/`null` 表示该策略不参与批量限流，不配置不代表批量不限流（仍受数据面 `mod_ai_batch.data` 全局硬顶约束）；字段定义见 [RateLimitPolicy](./00-common.md#公共参数类型)。

## 3. `entities.md` 变更

### 3.1 创建请求示例（§2.3）

变更前：

```json
"rate_limit_policy": {
    "enabled": true,
    "rules": {
        "tpm": [
            {"name": "tpm_1min", "model": "*", "window_minutes": 1, "max_tokens": 10000, "step_minutes": 1}
        ],
        "rpm": [
            {"name": "rpm_1min", "model": "*", "window_minutes": 1, "max_requests": 100}
        ],
        "max_concurrency": 50
    }
}
```

变更后：

```json
"rate_limit_policy": {
    "enabled": true,
    "rules": {
        "tpm": [
            {"name": "tpm_1min", "model": "*", "window_minutes": 1, "max_tokens": 10000, "step_minutes": 1}
        ],
        "rpm": [
            {"name": "rpm_1min", "model": "*", "window_minutes": 1, "max_requests": 100}
        ],
        "max_concurrency": 50,
        "batch_limits": {
            "max_create_rpm": 60,
            "max_active_batches": 10,
            "max_file_bytes": 104857600,
            "max_file_lines": 1000000
        }
    }
}
```

### 3.2 约束条款（创建/PUT/PATCH 三处约束列表各追加一条）

新增（Entity 口径，强调挂载其下的 API-Key 各自计数）：

> - `rate_limit_policy.rules` 中的 `batch_limits` 段（批量任务限流维度）与独立策略资源一致生效：四维度均按发起请求的 API-Key 计数（与本 Entity 挂载的 API-Key 各自的活跃批量任务数比对）、与 `model` 无关，省略/`null` 表示该策略不参与批量限流，不配置不代表批量不限流（仍受数据面 `mod_ai_batch.data` 全局硬顶约束）；字段定义见 [RateLimitPolicy](./00-common.md#公共参数类型)。

## 4. `00-common.md` 口径更正（§9）

`rules` 必填口径与实现（`lib/validate/validate.go` 的 `RateLimitPolicy`）对齐：

| 项 | 变更前 | 变更后 |
|----|--------|--------|
| `rules` 必填条件 | `enabled=true` 时必填，且 `tpm`、`rpm`、`max_concurrency(>=0)` 三者至少配置其一 | `enabled=true` 时必填，且 `tpm`、`rpm`、`max_concurrency(>=0)`、`batch_limits` 四者至少配置其一 |

## 5. 无行为变更声明

- 校验：`validate.RateLimitPolicy` 早已覆盖 `batch_limits`（内联策略与独立策略
  资源同一入口，`endpoints/openapi_v1/api_key/checker.go`、
  `endpoints/openapi_v1/entity/validator.go`）。
- 存储：内联策略经 `storage/rdb/rate_limit_policy` 落库，序列化
  `batch_limits` 列，与独立策略资源同一条路径。
- 导出：随策略生成 BFE `ai_rate_limit.data` `rules.batch` 段，行为不变。
- 兼容性：旧客户端不传 `batch_limits` 语义不变；无 DDL、无数据面变更。

## 6. 测试

### 6.1 已有覆盖（本次复用，无需改动）

| 用例 | 文件 | 覆盖点 |
|------|------|--------|
| AK-BL-1-001 | `test/integration/tests/api_key/batch_limits/batch_limits_test.go` | API-Key 创建内嵌 `batch_limits` 四维度成功，`GET` 回读一致 |
| AK-BL-1-002 | 同上 | 负维度值（`max_create_rpm` 等 < 0）返回 422 |
| AK-BL-1-003 | 同上 | InnerAPI 导出策略含 `rules.batch` 段与 `redis_key` |
| AK-BL-1-004 | 同上 | 不带 `batch_limits` 的策略导出不生成 batch 段 |
| AK-BL-1-005 | 同上 | `max_create_rpm=0` 不生成 batch redis_key |
| 模型层 | `model/rate_limit_policy/rate_limit_policy_batch_test.go` | 模型层落库/导出字段 |
| 共享层 | `model/shared/rate_limit_redis_key_test.go` | 含 `BatchLimits` 的 Redis Key 构建与清理 |

### 6.2 已补充实现（随本次变更落地）

1. **Entity 内联路径用例族**：新建
   `test/integration/tests/entity/batch_limits/batch_limits_test.go`
   （编号 ET-BL-1-001 起，与 AK-BL 对称）：
   - ET-BL-1-001 创建内嵌 `batch_limits` 四维度成功，`GET` 回读一致；
   - ET-BL-1-002 负维度值返回 422（四个子用例）；
   - ET-BL-1-003 InnerAPI 导出 `rules.batch` 段与 `redis_key`
     （经挂载 API-Key 的层级绑定定位祖先 Entity 策略）；
   - ET-BL-1-004 不带 `batch_limits` 的策略导出不生成 batch 段；
   - ET-BL-1-005 `max_create_rpm=0` 不生成 redis_key；
   - ET-BL-1-006 PATCH 整对象替换语义（省略 `batch_limits` 即清空该段）。
2. **端点级单测**：`endpoints/openapi_v1/api_key/checker_test.go`
   （`TestCheckCreateAPIKey` / `TestCheckUpdateAPIKey` 各三例）与
   `endpoints/openapi_v1/entity/validator_test.go`
   （`TestValidateEntityParam` 三例）补充 `batch_limits` 校验用例
   （合法四维度合法、`enabled=true` 仅配 `batch_limits` 合法、负维度值 422）。
3. **验证**：`go build ./endpoints/... ./test/...` 通过；相关包
   `go test` 通过；SQLite 后端集成用例本地执行通过（MySQL 后端由 CI 覆盖）。
4. 尚未落地：ai-gateway-web 批量限流表单（另行安排，不影响本契约）。
