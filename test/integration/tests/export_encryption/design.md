# 下发配置文件敏感字段加密（导出口加密）集成测试设计

> 功能实现：`lib/xcrypto`（`EncryptDeterministic`/`EncryptWithRawKey`）、
> `stateful/security.go`（`[Security]` 导出钥三字段 + `ExportSecretRing` +
> `/reload/security` 扩展）、`model/imods/exporter.go`（Tokens 外层键加密）、
> `model/icluster_conf/cluster.go`（`NewBfeClusterConf` 导出边界加密）。
> （变更说明 `design-docs/modifications/2026-10-05-export-config-field-encryption/`，
> 契约 `api-changes.md`；数据面契约 `bfe/docs/zh_cn/modifications/2026-10-05-export-config-field-encryption/`）。
> 模块前缀：**EFE**（Export Field Encryption）。

---

## 1. 被测对象与特殊性

导出加密是**导出生成点的字段级加密**（非业务域资源），被测面与特殊性：

| 被测面 | 说明 |
|--------|------|
| 开关门控 | `[Security].EncryptExports`（默认 false）仅作用于两个 topic：`/configs/mod-api-key`（Tokens 外层键，内层 `key` 置空）与 `/configs/tls_conf/server_data_conf`（`AIConf.Keys[].Key`）；其余导出 topic 永不出现密文 |
| 信封契约 | `enc$v1$<base64(keyID(1B)\|nonce(12B)\|AES-256-GCM(pt)+tag(16B))>`；**确定性密文**（同一 keyID+明文字节恒定，`data_sign` 稳定）；加密钥 = `ExportKeyFile` 中 **32B 原始密钥**（无 HKDF 派生，与数据面 `bfe_util/crypto` 严格一致） |
| marker 直通 | 无 `enc$v1$` 前缀按明文直通（灰度/回滚兼容语义） |
| 启动 fail-fast | 开关开而 `ExportKeyFile` 未配置/损坏/`ActiveExportKeyID` 不在 `[Keys]` → 拒绝启动；开关关时 keyring 配置错误仅告警（渐进启用） |
| 热加载 | monitor `POST /reload/security` 一并重读 `ExportKeyFile`，原子替换、失败保留旧值 |

**与 SAR 模块的差异**：断言的核心证据在 **InnerAPI 导出报文**而非 DB 字节——按
`api-changes.md` 合同断言（检查项 #8 导出产物断言、#4 防泄漏：响应 body 全文不含
密钥明文；#12 以合同为唯一断言依据）。解密验证采用**数据面视角**（原始密钥 +
信封布局手工 GCM 解包，与 `bfe_util/crypto.Decrypt` 同算法），证明 BFE 可解开控制面
产出。

**实例模型**：各用例独立 `testutil.StartServerWithMonitor(extraTOML)`，keyring 文件写在
`t.TempDir()`（0600，TOML：`ActiveKeyID` + `[Keys]` map），extraTOML 注入
`[Security]` 段（路径经 `filepath.ToSlash` 转义，同 SAR）。

## 2. 关键实现技巧

| 技巧 | 落点 |
|------|------|
| 跨仓解密断言 | integration go.mod `replace ../../` 引主模块；手工解包（base64 → keyID\|nonce\|ct → GCM open with raw key）复用 SAR 的 `decryptEnvelope` 形态，**不解为主模块 `xcrypto.Keyring.Decrypt`**（那会走 HKDF 派生钥，恰好打不开导出密文——正是合同锁定的差异点） |
| keyID 判定 | base64 解码首字节 == 预期 keyID（写死断言 1/2，不只断"是密文"，同 SAR 纪律） |
| 版本单调/增量 | 导出接口 `version` 语义：无变更返回 `Data: null`（合同）——EFE-1-003 用它锁确定性密文 |
| 配置变更触发新版本 | 轮换用例中 PATCH api-key `allow_models` 触发版本推进（确定性密文下单纯换钥不产生新 sign） |
| 启动失败断言 | `StartServerWithMonitor` 返回 err（同 SAR-1-003，进程 fail-fast 后 `waitForReady` 超时，固定接受 10s 耗时） |

## 3. 场景总览（用例编号登记）

| 编号 | 用例名 | 前置配置（extraTOML） | 步骤与断言要点 |
|------|--------|------------------------|----------------|
| EFE-1-001 | 开关关闭明文基线 | 无 `[Security]` 段 | 建 provider（key `sk-efe-a1`）+ cluster（llm_config 引用 provider key）+ api-key（`ak-efe-a1`）→ `GET /configs/mod-api-key`：tokens 外层键 == `ak-efe-a1` 明文、内层 `key` 同值、body **全文不含 `enc$v1$`** → `GET /configs/tls_conf/server_data_conf`：`AIConf.Keys[].Key` == `sk-efe-a1` 明文 |
| EFE-1-002 | 开关开启加密导出 | `EncryptExports=true` + `ExportKeyFile`（key 1，active=1） | 同前置资源 → mod-api-key：外层键 `enc$v1$` 且 keyID=1、内层 `key==""`、`key_id`/`enabled`/`quota_plans` 原样、body 不含 `ak-efe-a1` → server_data_conf：`Keys[].Key` `enc$v1$` 且 keyID=1、`Name`/`Weight`/`KeyPolicy` 原样、body 不含 `sk-efe-a1` → 两处密文均能被**原始密钥**解回明文（数据面视角） |
| EFE-1-003 | 确定性密文 → data_sign 稳定 | 同 EFE-1-002 | 首次导出记 `version=V1` → 无任何变更再次 `GET ?version=V1` → 响应 `Data: null`（合同：配置未变化）→ 直接 `GET`（version 空）→ 敏感字段密文与首次**字节一致**（确定性 nonce 锁定，防"每次导出都成新版本"的签名风暴） |
| EFE-1-004 | 门控：无关 topic 无密文 | 同 EFE-1-002 | `GET /configs/gslb_data/cluster_table`、`/configs/ai-route`、`/configs/gslb_data/gslb?bfe_cluster=...` → body 全文不含 `enc$v1$`（门控只作用两 topic） |
| EFE-2-001 | 开关开无 ExportKeyFile 拒启动 | `EncryptExports=true`（无 ExportKeyFile） | `StartServerWithMonitor` 必须返回 err（fail-fast） |
| EFE-2-002 | 开关开 keyring 损坏拒启动 | `EncryptExports=true` + `ExportKeyFile` 指向垃圾文件 | 启动必须失败 |
| EFE-2-003 | 开关开 ActiveExportKeyID 未知拒启动 | `EncryptExports=true` + 有效 keyring + `ActiveExportKeyID=99` | 启动必须失败 |
| EFE-2-004 | 开关关 keyring 损坏仅告警 | `EncryptExports=false` + `ExportKeyFile` 指向垃圾文件 | 启动成功；导出为明文（同 EFE-1-001 断言要点） |
| EFE-3-001 | 热加载轮换全链路 | keyring v1（key 1，active=1）启动 | 建资源 → 首次导出外层键 keyID=1 → 覆写 keyring v2（追加 key 2、active=2）→ `POST {monitor}/reload/security` 200 且响应含 export keys 1→2 → **PATCH api-key `allow_models` 触发版本推进**（确定性密文下单纯换钥不产生新 sign）→ 新导出：全部敏感字段 keyID=2 且解回同一明文 → 旧 keyID=1 密文样本仍能被 key 1 解开（双钥并存期兼容） |

> 用例计数：9。编号在实现 `_test.go` 中以注释标注（`// EFE-x-xxx`）。

## 4. 合同对齐声明

- 断言依据 `api-changes.md`：字段对照表（外层键/内层 key/AIConf.Keys[].Key）、
  信封公共约定（§2）、失败语义（§3.3/4.3）、`/reload/security` 扩展（§5）、
  配置项与 fail-fast（§6）。当前实现与合同无已知偏差；若实现期发现偏差，
  按检查项 #12 先确认"修代码还是修合同"再写断言，并回填本节。

## 5. 运行方式

```bash
cd ai-gateway-api && go build -o ai-gateway-api.exe .   # 先重建二进制
cd ai-gateway-api/test/integration
go vet ./tests/export_encryption/
go test -v -count=1 -timeout 600s ./tests/export_encryption/...
```
