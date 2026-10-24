# API 变更说明：下发配置文件敏感字段加密（InnerAPI 导出内容契约）

> 变更摘要与总体设计见同目录《change-summary.md》《design-changes.md》。
> 本文只描述 API 契约变更：**无新增/删除端点、无响应包装变化、无新错误码**；
> 变更形态为——2 个 InnerAPI 导出端点的**内容契约**变化 + 1 个 monitor 端点
> 行为扩展 + `ai_gateway_api.toml [Security]` 配置项新增。
> 数据面消费契约（BFE 解密语义）已落地：
> `bfe/docs/zh_cn/modifications/2026-10-05-export-config-field-encryption/design-changes.md`。

---

## 1. 变更概览

| 变更 | Method | Path | 说明 |
|------|--------|------|------|
| 内容契约变化 | GET | `/inner-api/v1/configs/mod-api-key` | `EncryptExports=true` 时 Tokens 外层键为 `enc$v1$` 密文、内层 `key` 置空 |
| 内容契约变化 | GET | `/inner-api/v1/configs/tls_conf/server_data_conf` | `EncryptExports=true` 时 `AIConf.Keys[].Key` 为 `enc$v1$` 密文 |
| 行为扩展 | POST | monitor `/reload/security` | 除既有 MasterKeyFile 外，一并重读 `ExportKeyFile`（两 keyring 独立校验、失败各自保留旧值） |
| 配置新增 | - | `ai_gateway_api.toml [Security]` | `EncryptExports` / `ExportKeyFile` / `ActiveExportKeyID`（§6） |

开关默认关：`EncryptExports=false` 时两个端点输出与现状**字节级一致**（回归基线）。

## 2. 公共约定：enc$v1$ 信封（消费方必须遵守）

| 项 | 约定 |
|----|------|
| 格式 | `enc$v1$<base64(keyID(1B) \| nonce(12B) \| AES-256-GCM(plaintext)+tag(16B))>` |
| marker 直通 | **无 `enc$v1$` 前缀的值一律按明文直通**（兼容/灰度/回滚/新旧并存的基本语义）；消费方不得拒绝无前缀值 |
| 确定性 | 同一 (keyID, plaintext) 密文字节恒定（`nonce = HMAC-SHA256(key, plaintext)[0:12]`，`data_sign` 因此稳定）；消费方不得假设密文随机 |
| 加密钥 | keyring 文件中的 **32B 原始密钥**（无 HKDF 派生），选钥按密文首字节 keyID 查 `ExportKeyFile` 的 `[Keys]` |
| 门控 | 仅 `EncryptExports=true` 且仅上述两 topic；其余导出 topic 永不出现密文 |
| 非目标 | 文件保持合法 JSON；非敏感字段（`key_id`、`enabled`、配额、`Name`/`Weight`/`KeyPolicy`/模型映射/路由）原样可读 |

## 3. GET /inner-api/v1/configs/mod-api-key

### 3.1 变化字段

| JSON 路径 | `EncryptExports=false`（现状） | `EncryptExports=true` |
|-----------|-------------------------------|------------------------|
| `tokens.<product>.<外层键>` | 外层键 = api-key 明文值 | 外层键 = api-key 的 `enc$v1$` 密文 |
| `tokens.<product>.*.key` | api-key 明文（与外层键一致） | `""`（空串；消费方解密外层键后回填，不得因空串拒绝加载） |
| `tokens.<product>.*.key_id` / `enabled` / `expired_time` / `unlimited_quota` / `allow_models` / `block_models` / `subnet` / `tags` / `quota_plans` | 原样 | **原样（不加密）** |
| `config` / `QuotaPlans` / `version` | 原样 | **原样（不加密）** |

### 3.2 示例对照

```jsonc
// EncryptExports=false（现状）
{"tokens": {"ai_product": {"ak-123": {"key": "ak-123", "key_id": "id-1", "enabled": true, …}}}}

// EncryptExports=true
{"tokens": {"ai_product": {"enc$v1$9mJz…": {"key": "", "key_id": "id-1", "enabled": true, …}}}}
```

### 3.3 错误语义

- 加密失败（`ExportKeyFile` 缺失/未含 `ActiveExportKeyID`）→ **该次导出整体失败**，
  走既有导出错误包装；错误信息只含 `key_id`/长度元信息，**不含 key 明文或密文**。
- 端点响应结构、`ErrNum` 语义、版本参数行为均无变化。

## 4. GET /inner-api/v1/configs/tls_conf/server_data_conf

### 4.1 变化字段

| JSON 路径 | `EncryptExports=false`（现状） | `EncryptExports=true` |
|-----------|-------------------------------|------------------------|
| `Config.<cluster>.AIConf.Keys[].Key` | 上游 provider key 明文 | `enc$v1$` 密文 |
| `Config.<cluster>.AIConf.Keys[].Name` / `Weight` | 原样 | **原样（不加密）** |
| `Config.<cluster>.AIConf.KeyPolicy` / `ModelProtocols` / `ModelMapping` / `ModelTable` / `Provider` | 原样 | **原样（不加密）** |
| `Config.<cluster>` 其余段（BackendConf/CheckConf/GslbBasic/ClusterBasic/HTTPSConf） | 原样 | **原样（不加密）** |
| HostTable / RouteTable / VipTable / version | 原样 | **原样（不加密）** |

### 4.2 示例对照

```jsonc
// EncryptExports=false（现状）
"AIConf": {"Keys": [{"Name": "k1", "Key": "sk-example", "Weight": 100}], …}

// EncryptExports=true
"AIConf": {"Keys": [{"Name": "k1", "Key": "enc$v1$Ab3x…", "Weight": 100}], …}
```

### 4.3 错误语义

同 §3.3：加密失败 → 该次导出整体失败，错误含 cluster 名与 key 序号，不含密钥材质。

## 5. Monitor 端点 `/reload/security` 行为扩展

| 项 | 约定 |
|----|------|
| 请求 | 不变（POST，无参数） |
| 行为 | 在既有 MasterKeyFile 重读之外，**追加** `ExportKeyFile` 重读；两个 keyring 各自校验（`ActiveKeyID` 必须在 `[Keys]` 中）、各自原子替换 |
| 失败语义 | 任一文件失败 → 返回错误；**失败的 keyring 保留旧值，成功的不受影响**（部分失败是可能的，调用方须按响应判断） |
| 用途 | 文件钥轮换免重启（硬性要求，见 design-changes.md §4） |

## 6. 配置项变更（`ai_gateway_api.toml [Security]`）

| 配置项 | 类型 | 必填 | 默认 | 合法性条件 | 说明 |
|--------|------|------|------|------------|------|
| `EncryptExports` | bool | N | `false` | - | 导出加密总开关；仅控制 §1 两 topic |
| `ExportKeyFile` | string | 条件 | 空 | `EncryptExports=true` 时必填 | 文件钥 keyring 路径（0600；与 BFE `[Security].KeyFile` 同一份文件，分发到全部 BFE 实例） |
| `ActiveExportKeyID` | int | N | 取 keyring 文件 `ActiveKeyID` | 须在 `[Keys]` 中 | 新密文使用的 keyID；与 keyring 文件内 `ActiveKeyID` 一致性启动时校验 |

启动校验（fail-fast）：`EncryptExports=true` 而 `ExportKeyFile` 未配置/不可读/解析失败、
或 `ActiveExportKeyID` 不在 `[Keys]` 中 → 拒绝启动；开关关闭时 keyring 配置错误仅告警。

## 7. 兼容性与消费方要求

- **消费方义务**：凡解析上述两导出内容的组件，必须实现"marker 识别 → 有 `enc$v1$` 用 keyring 解密 / 无则明文直通"。数据面 BFE 已落地（含失败语义：reload 失败旧配置保留、首启 fail-fast、密文不落日志）。
- **版本错配**：`EncryptExports=true` + 未升级 BFE = reload 失败、旧配置滞留（危险态）。上线顺序硬约束：**先全量升级 BFE，再开开关**；回滚 = 关开关，下一导出周期回到明文（双向安全）。
- **签名语义**：`data_sign` 为导出内容 MD5，加密在组装后计算，自然反映密文；稳态下确定性密文保证签名稳定，conf-agent 不会把等价配置当差异反复推送。
- **无破坏性变更**：`EncryptExports=false`（默认）时全部端点输出与现状字节级一致；无既有字段删除/改名；OpenAPI 零变化；`00-common.md` 错误码表无需新增。
