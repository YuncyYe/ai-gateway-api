# mod-api-key 接口

## 1. 接口信息

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 导出 API-Key 及配额配置 | 供 BFE 进行 Token 鉴权和配额检查 |
| 端点 | `/configs/mod-api-key` | - |
| Method | GET | - |
| 鉴权 | `FeatureAPIKey + ActionExport` | - |

## 2. 请求参数

**Query 参数**

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| version | string | 否 | 上次返回的版本号，用于增量同步 | 可选；无强制格式/长度校验；为空或未传时按首次拉取处理 |

**请求示例**

```shell
curl -X GET "http://api-server:port/inner-api/v1/configs/mod-api-key?version=00010101000000" \
  -H "Authorization:Token TOKEN_STRING"
```

## 3. 返回数据结构

### 3.1 顶层结构

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "version": "00010101000000",
        "config": {
            "product_name": [/* API-Key 路由规则 */]
        },
        "QuotaPlans": {
            "product_name": [/* 配额计划定义 */]
        },
        "tokens": {
            "product_name": {
                "api_key_value": {/* Token 配置 */}
            }
        }
    },
    "WorkMode": "ModeNormal"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| version | string | 配置版本号 |
| config | object | 按产品线分组的 API-Key 路由规则 |
| QuotaPlans | object | 按产品线分组的配额计划定义，Token 通过 `quota_plans` 数组引用 |
| tokens | object | 按产品线分组的 Token 配置；**`[Security].EncryptExports=true` 时外层键为 `enc$v1$` 密文，见 §3.5** |

### 3.2 config 结构（路由规则）

```json
{
    "config": {
        "ai_product": [
            {
                "Cond": "req_host_in(\"api.example.com\")",
                "Action": {
                    "Cmd": "CHECK_TOKEN"
                }
            }
        ]
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| Cond | string | 路由匹配条件表达式 |
| Action | object | 匹配后执行的动作 |
| Action.Cmd | string | 动作命令，固定为 `CHECK_TOKEN` |

### 3.3 tokens 结构（Token 配置）

**实际导出的 TokenFile 结构**

```json
{
    "tokens": {
        "ai_product": {
            "AI_product-abcdef123456": {
                "key": "AI_product-abcdef123456",
                "key_id": "ak-test-key-001",
                "enabled": true,
                "expired_time": -1,
                "unlimited_quota": false,
                "allow_models": "gpt-4,gpt-3.5-turbo",
                "block_models": "gpt-4-32k",
                "subnet": "192.168.0.0/16,10.0.0.0/8",
                "quota_plans": ["ak-test-key-001", "dept-engineering"],
                "Tags": [
                    {"TagName": "department", "TagValue": "dept-engineering", "TagLevel": 3}
                ]
            }
        }
    }
}
```

**Token 字段说明**

| 字段 | 类型 | 说明 | 可能取值 |
|------|------|------|----------|
| key | string | API-Key 值；**`EncryptExports=true` 时固定为空串**（明文见外层键密文，消费方解密后回填），见 §3.5 | 系统生成的 Key 字符串 |
| key_id | string | API-Key 标识 ID | 用于唯一标识该 API-Key，对应 API-Key 的唯一标识 |
| enabled | bool | 是否启用 | `true`: 启用，`false`: 禁用 |
| expired_time | int64 | 过期时间 | `-1`: 永不过期；其他为 Unix 时间戳（秒） |
| unlimited_quota | bool | 是否无限配额 | `true`: 不检查配额；`false`: 检查配额 |
| allow_models | string | 允许访问的模型 | 逗号分隔，空或 `""` 表示不限制 |
| block_models | string | 禁止访问的模型 | 逗号分隔，空或 `""` 表示无不允许模型 |
| subnet | string | 允许的客户端子网 | 逗号分隔的 CIDR 列表，空或 `""` 表示不限制 |
| quota_plans | []string | 关联的配额计划 ID 列表 | 引用顶层 `QuotaPlans` 中的定义，包含 API-Key 自身和 Entity 层级的所有配额计划 |
| Tags | []ApikeyTag | Entity 层级标签列表 | 包含 `TagName`、`TagValue` 和 `TagLevel` 字段 |

**ApikeyTag 结构（Entity 层级标签，按字段名导出）**

| 字段 | 类型 | 说明 | 示例 |
|------|------|------|------|
| TagName | string | Entity 类型 | `department`, `team`, `project` |
| TagValue | string | Entity 名称 | `dept-engineering`, `team-core` |
| TagLevel | int | 标签级别，取值为 1~5 的整数 | `3` |

### 3.5 tokens 敏感字段加密形态（`[Security].EncryptExports=true` 时）

开关默认关闭，关闭时本节不适用、输出与历史版本一致。开启后**仅** tokens 外层键加密，
其余字段（含内层 `key_id`/`enabled`/配额/Tags、`config`、`QuotaPlans`）原样可读。

- **信封**：外层键 = `enc$v1$<base64(keyID(1B) | nonce(12B) | AES-256-GCM(api_key)+tag(16B))>`；
  加密钥为文件钥 keyring（`[Security].ExportKeyFile`）中的 32B 原始密钥，选钥按密文首字节
  keyID 查 `[Keys]`；密文确定性（同一 keyID+明文 字节恒定，`data_sign` 稳定）。
- **marker 直通**：无 `enc$v1$` 前缀的值一律按明文处理（灰度/回滚/新旧并存兼容语义），
  消费方不得拒绝无前缀值。
- **内层 `key`**：固定空串 `""`。消费方解密外层键后回填明文键；不得因内层 `key` 为空
  拒绝加载。

```json
{
    "tokens": {
        "ai_product": {
            "enc$v1$9mJz…": {
                "key": "",
                "key_id": "ak-test-key-001",
                "enabled": true,
                "expired_time": -1,
                "unlimited_quota": false,
                "allow_models": "gpt-4,gpt-3.5-turbo",
                "block_models": "gpt-4-32k",
                "subnet": "192.168.0.0/16,10.0.0.0/8",
                "quota_plans": ["ak-test-key-001", "dept-engineering"],
                "Tags": [
                    {"TagName": "department", "TagValue": "dept-engineering", "TagLevel": 3}
                ]
            }
        }
    }
}
```

上线顺序：先全量升级支持解密的 BFE，再开启 `EncryptExports`（错配将导致 BFE reload
失败、旧配置滞留）。回滚 = 关闭开关，下一导出周期回到明文。详见
`design-docs/modifications/2026-10-05-export-config-field-encryption/api-changes.md`。

### 3.4 QuotaPlans 结构（配额计划定义）

配额计划定义在顶层 `QuotaPlans` 中按产品线分组，Token 通过 `quota_plans` 数组引用这些计划的 ID。

```json
{
    "QuotaPlans": {
        "ai_product": [
            {
                "Id": "ak-test-key-001",
                "Unlimited": false,
                "PassNoQuota": false,
                "RedisKey": "QUOTA_ak-test-key-001",
                "ExpiredTime": -1,
                "Quota": 100000000,
                "Unit": "RMB"
            }
        ]
    }
}
```

**QuotaPlan 字段说明**

| 字段 | 类型 | 说明 | 可能取值 |
|------|------|------|----------|
| Id | string | 配额计划 ID | 通常为 API-Key ID 或 Entity ID |
| Unlimited | bool | 是否无限配额 | `true`/`false` |
| PassNoQuota | bool | 配额不足时是否放行 | `true`: 放行；`false`: 拒绝 |
| RedisKey | string | Redis 中存储配额余额的 Key | 格式 `QUOTA_{id}` |
| ExpiredTime | int64 | 过期时间 | `-1`: 永不过期 |
| Quota | int64 | 配额总量 | 初始配额值；`Unit=RMB` 时为定点整数，精度 `1e-8` 元 |
| Unit | string | 配额单位，`RMB` 同时隐含货币类型 | `total_token` / `RMB` |

## 4. 成功返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "version": "00010101000000",
        "config": {
            "ai_product": [
                {
                    "Cond": "req_host_in(\"api.example.com\")",
                    "Action": {
                        "Cmd": "CHECK_TOKEN"
                    }
                },
                {
                    "Cond": "default_t()",
                    "Action": {
                        "Cmd": "CHECK_TOKEN"
                    }
                }
            ]
        },
        "QuotaPlans": {
            "ai_product": [
                {
                    "Id": "ak-test-key-001",
                    "Unlimited": false,
                    "PassNoQuota": false,
                    "RedisKey": "QUOTA_ak-test-key-001",
                    "ExpiredTime": -1,
                    "Quota": 100000000
                },
                {
                    "Id": "dept-engineering",
                    "Unlimited": false,
                    "PassNoQuota": false,
                    "RedisKey": "QUOTA_dept-engineering",
                    "ExpiredTime": -1,
                    "Quota": 500000000
                }
            ]
        },
        "tokens": {
            "ai_product": {
                "AI_product-abcdef123456": {
                    "key": "AI_product-abcdef123456",
                    "key_id": "ak-test-key-001",
                    "enabled": true,
                    "expired_time": -1,
                    "unlimited_quota": false,
                    "allow_models": "gpt-4,gpt-3.5-turbo",
                    "block_models": "gpt-4-32k",
                    "subnet": "192.168.0.0/16,10.0.0.0/8",
                    "quota_plans": ["ak-test-key-001", "dept-engineering"],
                    "Tags": [
                        {"TagName": "department", "TagValue": "dept-engineering", "TagLevel": 3}
                    ]
                },
                "AI_product-ghijkl789012": {
                    "key": "AI_product-ghijkl789012",
                    "key_id": "ak-prod-key-002",
                    "enabled": true,
                    "expired_time": -1,
                    "unlimited_quota": true,
                    "allow_models": "",
                    "block_models": "",
                    "subnet": "",
                    "quota_plans": [],
                    "Tags": []
                }
            }
        }
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

---

