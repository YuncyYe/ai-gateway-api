# rate-limit-policy 接口

## 1. 接口信息

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 导出限流策略配置 | 供 BFE 执行 TPM/RPM/并发限制检查 |
| 端点 | `/configs/rate-limit-policy` | - |
| Method | GET | - |
| 鉴权 | `FeatureRateLimitPolicy + ActionExport` | - |

## 2. 请求参数

**Query 参数**

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| version | string | 否 | 上次返回的版本号，用于增量同步 | 可选；无强制格式/长度校验；为空或未传时按首次拉取处理 |

**请求示例**

```shell
curl -X GET "http://api-server:port/inner-api/v1/configs/rate-limit-policy?version=00010101000000" \
  -H "Authorization:Token TOKEN_STRING"
```

## 3. 返回数据结构

### 3.1 顶层结构

与 BFE 动态配置文件 `ai_rate_limit.data` 格式保持一致：

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Config": {
            "AI_product": [/* 路由规则 */]
        },
        "RateLimitPolicies": {
            "rlp-0001": {/* 限流策略 */}
        },
        "ApikeyRateLimitPolicyBindings": {
            "ak-2v8x9k3m7p": ["rlp-0001", "rlp-0002"]
        },
        "Version": "00010101000000"
    },
    "WorkMode": "ModeNormal"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| Config | object | 按产品线组织的路由规则，key 为产品线名称 |
| RateLimitPolicies | object | 限流策略定义，key 为策略 ID（如 `rlp-0001`） |
| ApikeyRateLimitPolicyBindings | object | API-Key 到策略 ID 列表的绑定关系 |
| Version | string | 配置版本号 |

### 3.2 Config 结构（路由规则）

```json
{
    "Config": {
        "AI_product": [
            {
                "cond": "default_t()",
                "hit_action": {
                    "cmd": "FINISH",
                    "params": []
                }
            }
        ]
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| cond | string | 路由匹配条件表达式，通常填 `default_t()` |
| hit_action | object | 命中条件后的动作 |
| hit_action.cmd | string | 动作命令，支持 `PASS`、`RETURN`、`REDIRECT`、`FINISH`、`CLOSE` |
| hit_action.params | []string | 动作参数 |

**说明**：命中 cond 后，模块自动执行限流检查流程（提取 API-Key → 查找策略 → 执行限流），`hit_action.cmd` 用于指定被限流规则拒绝后的行为。

### 3.3 RateLimitPolicies 结构（限流策略）

```json
{
    "RateLimitPolicies": {
        "rlp-0001": {
            "name": "rlp-0001",
            "enabled": true,
            "rules": {
                "tpm": [
                    {
                        "name": "win2min",
                        "models": ["*"],
                        "window_minutes": 1,
                        "max_tokens": 10000,
                        "step_minutes": 1,
                        "redis_key": "RL_TPM_rlp-0001_0"
                    },
                    {
                        "name": "win10min",
                        "models": ["gpt-4"],
                        "window_minutes": 10,
                        "max_tokens": 50000,
                        "step_minutes": 1,
                        "redis_key": "RL_TPM_rlp-0001_1"
                    }
                ],
                "rpm": [
                    {
                        "name": "win2min",
                        "models": ["gpt-4"],
                        "window_minutes": 1,
                        "max_requests": 100,
                        "burst": 1,
                        "redis_key": "RL_RPM_rlp-0001_0"
                    }
                ],
                "max_concurrency": 50,
                "batch": {
                    "max_create_rpm": 10,
                    "max_active_batches": 5,
                    "max_file_bytes": 104857600,
                    "max_file_lines": 50000,
                    "redis_key": "RL_BATCH_rlp-0001_rpm"
                }
            }
        }
    }
}
```

**RateLimitPolicy 字段说明**

| 字段 | 类型 | 说明 |
|------|------|------|
| name | string | 策略名称 |
| enabled | bool | 是否启用限流，false 时跳过该策略 |
| rules | object | 限流规则集合 |

**rules 结构**

| 字段 | 类型 | 说明 |
|------|------|------|
| tpm | []TPMConfig | Token Per Minute 限制配置，最多 3 个；为空则不做 TPM 限制 |
| rpm | []RPMConfig | Request Per Minute 限制配置，最多 3 个；为空则不做 RPM 限制 |
| max_concurrency | int | 最大并发数，>=1 表示限制；0 表示封禁；<0 表示不限制 |
| batch | object | 批量限流配置；源为控制面 `rate_limit_policies.batch_limits`，为空或省略时不输出本段；结构见下表 |

**TPMConfig 结构**

| 字段 | 类型 | 说明 | 约束 |
|------|------|------|------|
| name | string | 规则名称 | 同一策略内不能重复 |
| models | []string | 目标模型列表 | 为空或 `["*"]` 表示不限制模型；非空时仅对匹配模型执行限流，多个 model 共用同一限流器；models 按**转发后的目标模型名**匹配（与 API-Key 允许/禁止模型判定一致），导出格式不变 |
| window_minutes | int | 统计时间窗口（分钟） | 取值范围 1-360 |
| max_tokens | int | 最大 Token 数 | >0: 有限制；0: 封禁；<0: 不限制 |
| step_minutes | int | 滑动步长（分钟） | 取值范围 1-360，必须 <= window_minutes |
| redis_key | string | Redis 计数器 key | 由控制面按 `(policy_id, 规则下标)` 稳定生成，例如 `RL_TPM_rlp-0001_0`；BFE 直接使用该值构建 Redis key，不依赖规则名或 model |

**RPMConfig 结构**

| 字段 | 类型 | 说明 | 约束 |
|------|------|------|------|
| name | string | 规则名称 | 同一策略内不能重复 |
| models | []string | 目标模型列表 | 为空或 `["*"]` 表示不限制模型；非空时仅对匹配模型执行限流，多个 model 共用同一限流器；models 按**转发后的目标模型名**匹配（与 API-Key 允许/禁止模型判定一致），导出格式不变 |
| window_minutes | int | 统计时间窗口（分钟） | 取值范围 1-360，默认 1 |
| max_requests | int | 最大请求数 | >=1: 有限制；0: 封禁；<0: 不限制 |
| burst | int | 突发请求数 | 最小值 1，默认 1 |
| redis_key | string | Redis 计数器 key | 由控制面按 `(policy_id, 规则下标)` 稳定生成，例如 `RL_RPM_rlp-0001_0`；BFE 直接使用该值构建 Redis key，不依赖规则名或 model |

**batch 结构（批量限流）**

| 字段 | 类型 | 说明 | 约束 |
|------|------|------|------|
| max_create_rpm | int | 单 API-Key 每分钟允许创建的批量任务数 | 四个维度均可单独省略或配 0（= 不配此维度）；>=1 表示限制 |
| max_active_batches | int | 单 API-Key 最大进行中（非终态）批量任务数 | 同上 |
| max_file_bytes | int64 | 批量输入文件大小上限（字节） | 同上 |
| max_file_lines | int64 | 批量输入文件行数上限 | 同上 |
| redis_key | string | 批量创建计数 Redis key | 仅 `max_create_rpm` 需要计数 key；由控制面按策略 ID 稳定生成，生成惯例同 `RL_TPM_<policyId>_<idx>`（见 `model/shared/rate_limit_redis_key.go`），例如 `RL_BATCH_rlp-0001_rpm`；BFE 直接使用该值构建 Redis key |

**batch 说明**：

- `batch` 段四维度均**按 API-Key 计、与 model 无关**（区别于 tpm/rpm 可按 model 细分）。
- 仅 `max_create_rpm` 需要 Redis 计数 key；文件上限（`max_file_bytes` / `max_file_lines`）为请求内本地校验，**无计数 key**；`max_active_batches` 复用数据面共享 `BATCH_ACTIVE` ZSET，**不重复配置 key**。
- 源数据为控制面 `rate_limit_policies.batch_limits`：可空列，省略 / null 表示该策略不参与批量限流，导出时 `rules` 不携带 `batch` 段。
- 批量任务的模型价格（`mode=batch`）由集群 conf 导出 `ModelTable` 的批量价格行提供（`batch_discount` 系数展开或手工 `mode=batch` 价格行），属价格表链路，对本接口消费方透明。

### 3.4 ApikeyRateLimitPolicyBindings 结构（绑定关系）

```json
{
    "ApikeyRateLimitPolicyBindings": {
        "ak-2v8x9k3m7p": ["rlp-0001", "rlp-0002"],
        "ak-3w9y0k4n8q": ["rlp-0002"]
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| key | string | API-Key 字符串 |
| value | []string | 绑定的策略 ID 列表，绑定多个策略时必须全部满足才通过 |

## 4. 成功返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Config": {
            "AI_product": [
                {
                    "cond": "default_t()",
                    "hit_action": {
                        "cmd": "FINISH",
                        "params": []
                    }
                }
            ]
        },
        "RateLimitPolicies": {
            "rlp-0001": {
                "name": "rlp-0001",
                "enabled": true,
                "rules": {
                    "tpm": [
                        {
                            "name": "global-tpm",
                            "models": ["*"],
                            "window_minutes": 1,
                            "max_tokens": 1000000,
                            "step_minutes": 1,
                            "redis_key": "RL_TPM_rlp-0001_0"
                        },
                        {
                            "name": "gpt4-tpm",
                            "models": ["gpt-4"],
                            "window_minutes": 1,
                            "max_tokens": 500000,
                            "step_minutes": 1,
                            "redis_key": "RL_TPM_rlp-0001_1"
                        }
                    ],
                    "rpm": [
                        {
                            "name": "global-rpm",
                            "models": ["*"],
                            "window_minutes": 1,
                            "max_requests": 60,
                            "burst": 1,
                            "redis_key": "RL_RPM_rlp-0001_0"
                        }
                    ],
                    "max_concurrency": 50,
                    "batch": {
                        "max_create_rpm": 10,
                        "max_active_batches": 5,
                        "max_file_bytes": 104857600,
                        "max_file_lines": 50000,
                        "redis_key": "RL_BATCH_rlp-0001_rpm"
                    }
                }
            },
            "rlp-0002": {
                "name": "rlp-0002",
                "enabled": true,
                "rules": {
                    "tpm": [
                        {
                            "name": "gpt4-tpm-limit",
                            "models": ["gpt-4"],
                            "window_minutes": 1,
                            "max_tokens": 5000,
                            "step_minutes": 1,
                            "redis_key": "RL_TPM_rlp-0002_0"
                        }
                    ],
                    "max_concurrency": 10
                }
            },
            "rlp-0003": {
                "name": "rlp-0003",
                "enabled": false,
                "rules": {
                    "tpm": [],
                    "rpm": [],
                    "max_concurrency": -1
                }
            }
        },
        "ApikeyRateLimitPolicyBindings": {
            "ak-2v8x9k3m7p": ["rlp-0001", "rlp-0002"],
            "ak-3w9y0k4n8q": ["rlp-0002"],
            "ak-9z8y7x6w5v": ["rlp-0001"]
        },
        "Version": "00010101000000"
    },
    "WorkMode": "ModeNormal"
}
```

## 5. 配置未变化返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": null,
    "WorkMode": "ModeNormal"
}
```

## 6. 批量限流（batch）向后兼容

- `rate_limit_policies.batch_limits` 为**可选可空列**，存量 NULL 行为不变（策略不参与批量限流，`rules` 不携带 `batch` 段）；存量数据**零迁移**。
- 旧版本 BFE 收到含 `batch` 段的 `rules` 时，**忽略未知 JSON 字段安全**，不报错。
- 策略更新为**整体替换语义**（同 `tpm` / `rpm_configs`）：省略 `batch_limits` 等价清空该策略的批量限流配置。

---

