# 下发配置文件敏感字段加密（导出口加密）：设计变更说明

> 配套：《change-summary.md》（背景、目标、关键决策、范围）。
> 数据面侧（BFE 加载点解密）已落地：
> `bfe/docs/zh_cn/modifications/2026-10-05-export-config-field-encryption/design-changes.md`、
> `bfe/docs/zh_cn/sys_design/config_file_field_encryption.md`——本文所有跨仓约定以其实现为基准。

---

## 1. 现状代码事实（设计约束来源）

| 事实 | 位置 | 约束 |
|------|------|------|
| Tokens 外层键 = api-key 明文值 | `model/imods/exporter.go:374`（`items[*one.Key] = tokenFile`）、`:381`（`Tokens: apiKey2Config`）、`:44`（`Tokens map[string]map[string]*TokenFile json:"tokens"`） | 加密点：组装 items 时对键加密；内层 `TokenFile.Key`（`:73` `json:"key"`）置空 |
| AIConf.Keys 明文组装 | `model/icluster_conf/cluster.go:1390-1397`（`keyMap[name]` → `cluster_conf.AIKey.Key`） | 加密点：`aiConf.Keys` 构建完成后遍历加密 `Key` 字段 |
| 导出内容与签名 | `model/iversion_control/version_control.go:38`（`Sign` = 内容 MD5）、`:97`（`DataSignWithoutVersion` 于组装后计算） | 加密在 `ExportData` 组装内完成 → 签名自然反映密文；**必须确定性密文**（§5） |
| 信封/keyring 基础设施 | `lib/xcrypto/`（`envelope.go`：`enc$v1$` 信封、`Encrypt`/`IsEncrypted`；`keyring.go`：TOML keyring、HKDF 派生 `encKeys`、"db-enc-key"） | 复用 Marker/解析/keyring 加载；**导出加密须绕过 `encKeys` 派生**（决策 5） |
| Security 配置与热加载范式 | `stateful/security.go`：`SecurityConfig{MasterKeyFile, ActiveKeyID}`、`loadSecretRing`（fail-fast）、`ReloadSecurity`（atomic 替换） | 新增导出钥字段 + ExportSecretRing，同范式扩展；`/reload/security` 一并重读 |
| 数据面解密基准 | `bfe/bfe_util/crypto/crypto.go`（`Marker="enc$v1$"`、`Decrypt` 用 **keyring 原始 32B 密钥**、无 HKDF 派生）；`bfe/bfe_util/crypto/keyring.go`（同款 TOML 解析） | 加密端必须与之一字节兼容：原始密钥 + 同信封布局（keyID(1B)\|nonce(12B)\|ct+tag） |

## 2. 配置模型（`stateful/security.go` 扩展）

```go
type SecurityConfig struct {
    MasterKeyFile   string `toml:"MasterKeyFile"`   // 既有：DB 主密钥 keyring
    ActiveKeyID     int    `toml:"ActiveKeyID"`     // 既有
    // 新增（导出文件加密）：
    EncryptExports  bool   `toml:"EncryptExports"`    // 默认 false；开启后两 topic 导出加密
    ExportKeyFile   string `toml:"ExportKeyFile"`     // 文件钥 keyring（独立根，与 MasterKeyFile 分离）
    ActiveExportKeyID int  `toml:"ActiveExportKeyID"` // 新密文使用的 keyID；默认取 keyring ActiveKeyID
}
```

```toml
[Security]
# MasterKeyFile = "/etc/ai-gateway/master.keys"      # 既有（DB 落盘加密）
# ActiveKeyID = 1
# 新增：
# EncryptExports = false
# ExportKeyFile = "/etc/ai-gateway/keys/export.keys" # 0600；分发到全部 BFE 实例（与 BFE [Security].KeyFile 同一份文件）
# ActiveExportKeyID = 1
```

启动校验（fail-fast，同 DB 场景纪律）：
- `EncryptExports=true` 但 `ExportKeyFile` 未配置/读不到/解析失败 → 拒绝启动；
- `EncryptExports=true` 且 `ActiveExportKeyID` 不在 `[Keys]` 中 → 拒绝启动；
- `EncryptExports=false` 时 `ExportKeyFile` 配置错误仅告警（渐进启用，允许先配钥后开开关）。

运行时：`stateful` 内新增 `ExportSecretRing *xcrypto.Keyring`（atomic.Value 承载，
与既有 SecretRing 并列）；`/reload/security` 重读两个文件、各自校验、各自原子替换，
单文件失败不影响另一个（保留旧值并返回错误）。

## 3. 导出加密实现

### 3.1 `lib/xcrypto` 增强（增量小改）

- 新增按原始密钥加密：`func (k *Keyring) EncryptWithRawKey(plaintext string, keyID uint8) (string, error)`
  —— 取 `k.keys[keyID]`（**非** `encKeys` 派生钥），调用包级 `Encrypt(plaintext, rawKey, keyID)`。
- 新增确定性 nonce 变体：`func EncryptDeterministic(plaintext string, key []byte, keyID uint8) (string, error)`，
  `nonce = HMAC-SHA256(key, plaintext)[0:12]`（替代随机 `rand.Read`）；信封布局不变。
  `EncryptWithRawKey` 内部走确定性 nonce。
- 单测：与 `bfe_util/crypto.Decrypt` 交叉断言（同一 keyring 文件 + 同一明文 → 密文可被
  BFE 侧算法解密、两次加密结果字节一致）。

### 3.2 mod-api-key 导出（`model/imods/exporter.go`）

组装 `items` 时（`:374` 附近）：

```go
// EncryptExports 开启：Tokens 外层键（api-key 值）字段级加密，内层 key 置空
tokenKey := *one.Key
if stateful.ExportCryptoEnabled() {
    tokenKey, err = stateful.ExportEncrypt(tokenKey) // EncryptWithRawKey(plaintext, ActiveExportKeyID)
    if err != nil {
        return nil, fmt.Errorf("encrypt token key %q failed: %s", one.KeyID, err) // 不含 key 明文
    }
    tokenFile.Key = "" // 文件不重复出现密钥材质；BFE 解密后回填
}
items[tokenKey] = tokenFile
apiKey2Config[*one.ProductName] = items
```

- 错误信息只带 `KeyID`/长度元信息，**绝不带 key 明文或密文**。
- `Config`/`QuotaPlans` 等非敏感段不动；文件保持合法 JSON。

### 3.3 server_data_conf 导出（`model/icluster_conf/cluster.go`）

`aiConf.Keys` 组装完成后（`:1397` 之后、`return aiConf` 之前）：

```go
if stateful.ExportCryptoEnabled() {
    for i := range aiConf.Keys {
        enc, err := stateful.ExportEncrypt(aiConf.Keys[i].Key)
        if err != nil {
            return nil, fmt.Errorf("cluster %s: encrypt AIConf.Keys[%d] failed: %s", clusterName, i, err)
        }
        aiConf.Keys[i].Key = enc
    }
}
```

- `Name`/`Weight`/`KeyPolicy`/模型映射等非敏感字段不动。

### 3.4 辅助函数（`stateful`）

```go
func ExportCryptoEnabled() bool            // EncryptExports && ExportSecretRing 已加载
func ExportEncrypt(plaintext string) (string, error) // 取 active ExportKeyID 的原始密钥做确定性加密
```

## 4. keyring 文件（与 BFE 共用同一份）

```toml
# ExportKeyFile（与 BFE [Security].KeyFile 同文件，分发到全部 BFE 实例）
ActiveKeyID = 2
[Keys]
  1 = "b64_old…"   # 旧钥：解密存量密文，收敛后删除
  2 = "b64_new…"
```

- 解析复用 `xcrypto.LoadKeyringFile`（TOML，`ActiveKeyID` 必须在 `[Keys] 中）。
- **文件钥不派生 pepper**（BFE 按内存明文索引鉴权，无需不可逆查找列）。
- **热加载（硬性要求，与 DB 场景同一纪律）**：轮换 = 编辑本文件（追加新钥 +
  改 `ActiveKeyID`）→ 控制面 `/reload/security` 原子生效（失败保留旧值）；BFE 侧
  每次加载 data 文件时重读该文件（已落地，K8s `subPath` 挂载例外需滚动重启，
  见 BFE 文档 §3.6）。两侧均免重启，重启仅作兜底。
- 生命周期 SOP（生成/双人分发/0600/与 conf 目录分离/禁入镜像与配置中心）沿用
  BFE 侧文档 §3.6 与 runbook §6；收敛 = 切 `ActiveExportKeyID` 后触发一次全量导出
  （导出文件每次全量重生成，下一周期自然收敛，无 sweep）。

## 5. 确定性密文（签名稳定性，本变更最关键的结构性约束）

`iversion_control.Sign` 是导出内容 MD5；conf-agent 以签名差异决定是否拉取新版本。
AES-GCM 随机 nonce 下同一明文每次密文不同 → **每次导出签名都变 → 等价配置被当作
新版本反复推送**（版本号膨胀、BFE 无意义 reload）。

决策：**确定性 nonce**（`HMAC-SHA256(key, plaintext)[0:12]`），同一 (keyID, plaintext)
密文字节恒定 → 稳态下签名稳定；明文/密文切换或密钥轮换产生一次正常版本推进。

代价与缓解（记录风险，评审确认）：
- 确定性加密泄露"明文相同"的相等性。api-key 与上游 provider key 均为高熵随机串，
  相等性不附加可利用信息；若未来引入低熵敏感字段，须重新评估。
- nonce 由 key+明文决定：同一 key 下不同明文 nonce 碰撞概率 ≈ 生日界（2^64 条），
  导出文件密钥条数为千级，余量充足。

备选方案（否决记录）：Sign 前对加密字段规范化置空再签名——侵入 `iversion_control`
公共链路，且签名不再反映真实下发内容，违反"签名反映密文"的审计语义。

## 6. 失败语义与灰度矩阵

| 场景 | 语义 |
|------|------|
| `EncryptExports=false` | 现状明文导出（基线，全链路兼容） |
| `EncryptExports=true`、keyring 正常 | 两 topic 敏感字段密文导出；BFE（新版）解密加载，磁盘零明文 |
| `EncryptExports=true`、keyring 缺失/错误 | 控制面**该次导出失败**（启动期 fail-fast）；BFE 侧按已落地语义：reload 失败旧配置保留 / 首启拒启动 |
| 回滚（关开关） | 下一导出周期回到明文 → 新旧版 BFE 均可加载（双向安全） |

上线顺序硬约束（BFE 侧文档 §4 矩阵）：**先全量升级 BFE（支持解密）→ 再开
`EncryptExports`**；反向错配（密文 + 旧版 BFE）导致 reload 失败、配置滞留。

## 7. 测试计划

| 层 | 用例 |
|----|------|
| xcrypto 单测 | `EncryptWithRawKey` 确定性（两次加密字节一致）；`EncryptDeterministic` 信封可被 BFE 算法描述解开（按 `bfe_util/crypto` 布局手工解）；ActiveExportKeyID 缺失报错 |
| 导出单测 | `model/imods` exporter：开关开/关对照（开：Tokens 外层键带 `enc$v1$`、内层 `key==""`、非敏感字段原样；关：字节级与现状一致）；`model/icluster_conf`：AIConf.Keys 密文 + Name/Weight 原样 |
| 集成（`test/integration/`，复用 `secret_at_rest` 模式） | 起服务 + `[Security] ExportKeyFile` → 拉取 `/configs/mod-api-key` 与 `/configs/tls_conf/server_data_conf` → 断言敏感字段 `enc$v1$`、无密钥明文、非敏感可读；密文可被 BFE 算法解开（交叉向量）；关开关回退明文 |
| 跨仓回归 | BFE 侧 SC26 场景（`bfe/tests/integration/implementation/scenario-SC26-encrypted-config-fields/`）已覆盖解密全语义，本变更交付后以其作为端到端联调入口 |

## 8. WBS（预估 0.8 人日）

| # | 工作 | 包 | 估 |
|---|------|----|----|
| 1 | xcrypto：`EncryptDeterministic`/`EncryptWithRawKey` + 单测（含与 BFE 算法交叉向量） | `lib/xcrypto` | 0.15 d |
| 2 | `SecurityConfig` 三字段 + ExportSecretRing 加载/fail-fast + `/reload/security` 重读 + 单测 | `stateful` | 0.2 d |
| 3 | 两个导出口加密插入 + 单测（开关对照） | `model/imods`、`model/icluster_conf` | 0.2 d |
| 4 | 集成测试（加密导出场景） | `test/integration` | 0.2 d |
| 5 | 文档（sys-design details + summary.md） | `design-docs` | 0.05 d |

## 9. 风险与应对

| 风险 | 影响 | 应对 |
|------|------|------|
| 确定性 nonce 的相等性泄露（低熵字段引入时） | 理论风险 | 本期限高熵密钥；doc 记录重评估触发条件 |
| 直接操作 `k.keys` 绕过派生层，误用 `encKeys` | 跨仓解不开 | `EncryptWithRawKey` 单测内置 BFE 布局解包断言；CR  checklist 标注 |
| 漏加密路径（未来新增导出敏感字段） | 明文落盘回退 | 新增导出字段评审 checklist 项；`xcrypto.IsEncrypted` 可在导出测试中做全文件扫描断言 |
| `ActiveExportKeyID` 切换早于 BFE 全量换钥 | BFE reload 失败、旧配置滞留（可用性） | runbook 纪律：BFE keyring 先行，与 BFE 侧 §6 同一顺序；控制面切 ID 前校验观测指标 |
