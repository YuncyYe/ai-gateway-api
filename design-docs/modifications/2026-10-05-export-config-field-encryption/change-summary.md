# 下发配置文件敏感字段加密（导出口加密）：变更摘要

## 1. 背景

控制面导出的两类密钥配置经 conf-agent **字节透传**写盘（conf-agent 不解析内容），
在 BFE 数据面磁盘上**明文落盘**：

| 导出 Topic | InnerAPI | 密钥材质 | 现状（代码事实） |
|------------|----------|----------|------------------|
| `mod_api_key_rule` | `/configs/mod-api-key` | 下游虚拟密钥全量（api-key 值是 Tokens map 的外层键） | `model/imods/exporter.go:374,381` 以 `*one.Key` 明文作 map 键组装 `Tokens` |
| `server_data_conf`（`tls_conf`） | `/configs/tls_conf/server_data_conf` | 上游 provider 密钥（`AIConf.Keys[].Key`） | `model/icluster_conf/cluster.go:1390-1397` 以 `keyMap[name]` 明文组装 `AIConf.Keys` |

**数据面侧已落地**：BFE 加载点解密（`enc$v1$` 信封 + `bfe.conf [Security] KeyFile`
keyring，含失败语义/热加载轮换/SC26 集成测试 9 用例），见
`bfe/docs/zh_cn/modifications/2026-10-05-export-config-field-encryption/design-changes.md`。
本变更覆盖**控制面导出口加密**，是两场景中的加密端；DB 落盘加密（密钥静态加密）
已于本仓 `2026-10-05-db-encryption-at-rest` 落地，两场景**密钥独立**、信封规范一致。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-10-05 |
| 涉及仓库 | `ai-gateway-api`（本仓；BFE 侧已先行落地，conf-agent 零改动） |
| 变更类型 | 复用安全基础设施：`lib/xcrypto` 小幅增强 + `[Security]` 新增导出密钥配置 + 两个导出口字段级加密 + 集成测试 |
| 产出 | xcrypto 增强（原始密钥加密/确定性 nonce）+ ExportKeyFile keyring 加载与热加载 + 两个导出 topic 敏感字段密文化 + 单测/集成测试 + 设计文档 |
| 预估工作量 | 约 0.8 人日（见 design-changes.md WBS） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 加密点 | **导出生成点**（`ExportData` 组装处），conf-agent 零改动、BFE 侧只解密 | 密文字节流自然落到 BFE 磁盘；字节透传性质不变 |
| 2 | 加密粒度 | **字段级**：仅 `Tokens` 外层键与 `AIConf.Keys[].Key`；文件保持合法 JSON、`cat` 可排障；QuotaPlans/KeyPolicy/路由等非敏感不动 | 整文件加密失去排障能力（评审决议，与 DB 场景一致） |
| 3 | 开关 | `[Security].EncryptExports = false`（默认关）；开启后仅上述两 topic 加密 | 灰度可回滚；版本错配保护依赖"先全量升级 BFE 再开"（BFE 侧文档 §4） |
| 4 | 文件密钥 | **独立 keyring 文件** `ExportKeyFile`（conf-file-key，独立根），与 `MasterKeyFile`（DB 主密钥）**完全分离** | 分发域不同：文件钥须下发全部 BFE 实例，DB 主密钥不出控制面主机；BFE 失陷仅波及导出配置文件 |
| 5 | **不做 HKDF 派生** | 导出加密**直接使用 keyring 中的 32B 原始密钥**（实现校准：取代方案文本中 `HKDF(conf-file-key, "file-enc-key")` 的派生设想） | 数据面已落地实现（`bfe_util/crypto`）直接使用原始密钥解密；导出钥本就单用途，派生无收益却造成跨仓不一致 |
| 6 | 确定性密文 | **确定性 nonce**：`nonce = HMAC-SHA256(key, plaintext)[0:12]`，同一 (keyID, plaintext) 密文恒定 | GCM 随机 nonce 会使每次导出的 `data_sign`（`model/iversion_control/version_control.go:38` 内容 MD5）都不同 → conf-agent 把等价配置当新版本反复推送。确定性密文使签名稳定，对 iversion_control/conf-agent **零侵入**；代价是泄露"明文相同"的相等性——api-key/上游 key 均为高熵随机串，可接受（记录风险） |
| 7 | Tokens 内层 `key` 字段 | 加密导出时**置空**（map 键为密文、内层 `"key": ""`） | 文件中不重复出现密钥材质；BFE 解密后回填明文键（BFE 侧已支持该导出形态并对 marker 键豁免内层 key 必填校验） |
| 8 | 签名语义 | `data_sign` 在 `ExportData` 组装**之后**计算（现状即如此），加密完成后签名自然反映密文 | 落实方案 Q5 意图：明文/密文切换产生一次正常版本推进；配合决策 6，稳态下不产生虚假差异 |
| 9 | 加密失败语义 | 加密动作失败（keyring 未配置/未含 ActiveExportKeyID）→ **该次导出失败返回错误**，不产出半成品 | 与 BFE 侧"半解密状态不允许"对称 |
| 10 | keyring 热加载 | `/reload/security` handler 一并重读 `ExportKeyFile`（失败保留旧值，两 keyring 独立校验） | 轮换要求免重启（与 DB 场景 runbook 同一纪律） |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `lib/xcrypto`：`Keyring.EncryptWithKey(plaintext, keyID)`（原始密钥，确定性 nonce）或等价 API + 单测；`stateful/security.go`：`SecurityConfig` 新增 `ExportKeyFile`/`ActiveExportKeyID`/`EncryptExports`、`ExportSecretRing` 加载（atomic）与 `/reload/security` 重读；`test/integration/tests/` 新增加密导出场景 |
| 主要修改 | `model/imods/exporter.go`（Tokens 组装处加密外层键、清空内层 key）；`model/icluster_conf/cluster.go`（AIConf.Keys 遍历加密 `Key`）；两个文件的导出单测补充密文断言 |
| 明确不动 | InnerAPI/OpenAPI 端点契约（无新端点、无响应结构变化）；`iversion_control` 签名机制（决策 6 使其无需改动）；conf-agent（零改动）；BFE（已落地）；`MasterKeyFile`/DB 加密链路（本变更只加并列的导出钥） |
| 数据迁移 | 无（开关默认关，开启后下一导出周期自然产出密文；回滚=关开关，下一周期回到明文） |

## 5. 非本仓登记点（链路协同）

| 位置 | 改动 | 状态 |
|------|------|------|
| `bfe/` | 加载点解密（`bfe_util/crypto` + `[Security] KeyFile` + 两加载点 + SC26 集成测试） | **已落地**（2026-10-05 提交 `9c7c77df`） |
| `conf-agent` | 无（字节透传，天然兼容） | 无需改动 |
| 运维手册 | 文件钥生成/分发/轮换 runbook（BFE 侧先行，控制面轮换 = 切 `ActiveExportKeyID` + `/reload/security` + 触发全量导出） | 随发布交付 |

## 6. 文档配套

- 本文（change-summary）+ `design-changes.md`（配置模型/xcrypto 增强/导出口改动/确定性密文/失败语义/测试/WBS）+ `api-changes.md`（导出内容契约变化：信封公共约定、两 InnerAPI 端点字段对照与示例、`/reload/security` 行为扩展、`[Security]` 配置项、兼容性与消费方义务）。三份文档自包含，全部评审决议已融入 change-summary 决策表。

## 7. 六步法核对

- [x] Step 1：变更目录 `2026-10-05-export-config-field-encryption`
- [x] Step 2：本摘要 + design-changes.md + api-changes.md
- [x] Step 3：无 OpenAPI 契约变更；InnerAPI 导出内容契约已落 `api-define/InnerAPI接口定义/mod-api-key.md` §3.5 与 `server-data-conf.md`
- [x] Step 4：sys-design 已沉淀 `details/下发配置加密.md`（含实现校准记录）并更新 `summary.md`
- [x] Step 5：代码实现完成（xcrypto 增强 / stateful 导出钥 / 两个导出口加密）
- [x] Step 6：落地后总结沉淀（本核对表 + details 实现校准记录；集成测试 EFE 9 用例全绿）
