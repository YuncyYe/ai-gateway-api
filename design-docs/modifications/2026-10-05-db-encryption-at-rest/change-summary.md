# DB 落盘加密（密钥静态加密）：变更摘要

## 1. 背景

控制面 `ai-gateway-api` 的两类密钥当前**明文落库**：

| 对象 | 位置 | 现状 |
|------|------|------|
| 上游厂商密钥 | `providers.api_keys`（JSON 列，`db_ddl.sql:465`） | `storage/rdb/provider/provider.go:358,369` `json.Marshal` 明文入库 |
| 下游虚拟密钥 | `api_keys.api_key`（`db_ddl.sql:302`，`uk_api_key` 唯一键） | 明文存储；按值唯一性校验（`model/api_key/api_key.go:683-705`）走明文查找 |

客户《大模型网关必备功能点 v2》#11（核心必备）要求"数据库不存明文"。本变更覆盖**控制面 DB 落盘加密**；下发给 BFE 的配置文件加密（数据面磁盘落盘）为独立场景、**另行立项**，两场景密钥独立、信封规范一致。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-10-05 |
| 涉及仓库 | `ai-gateway-api`（本仓；BFE 下发加密涉及 `bfe/`，另行立项） |
| 变更类型 | 新增安全基础设施：`lib/xcrypto`（信封/keyring）+ `[Security]` 配置 + DAO 透明加解密 + `api_key_hash` 哈希索引列（双 DDL）+ 收敛任务（任务表/锁表双 DDL + OpenAPI 2 端点） |
| 产出 | 3 张新表（0 业务新表：任务表/锁表/哈希列为既有表加列）+ xcrypto 包 + keyring/热加载 + sweep 管理器 + 2 个 OpenAPI 端点 + 单测/集成测试 + 设计文档 |
| 预估工作量 | 约 5.5 人日（见 design-changes.md WBS） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 算法 | **AES-256-GCM**（Go 标准库 `crypto/aes`+`crypto/cipher`，零新依赖） | 认证加密防磁盘位翻转；随机 nonce；否决 CBC（padding oracle）与 SM4（二期经 Provider 接口替换） |
| 2 | 信封格式 | `enc$v1$<base64(keyID(1B)\|nonce(12B)\|ct+tag)>`，marker 无前缀=明文直通 | 自描述：解密按密文自带 keyID 查 keyring 选钥，**无需按数据类别维护密钥映射**；读兼容存量明文实现不停机迁移；keyID 支持轮换多版本并存，且前瞻兼容按类别分钥（1 字节 255 空间，解密路径零改动） |
| 3 | 定位 | **防泄漏，不加 AAD** | 防库文件/备份外泄，不防篡改；库内密文换位粘贴的前提是已获库写权限（更高权限场景），不在威胁模型 |
| 4 | 主密钥注入 | **仅 keyring 文件一种方式**（`[Security].MasterKeyFile`，0600），**不提供 env**（评审决议） | env 泄漏面（/proc、core dump、CI 日志）；单一代码路径——任何部署天然具备多钥+热加载，消除"env 模式轮换不生效"类事故；测试用临时文件即可 |
| 5 | 密钥派生 | HKDF-SHA256：db-enc-key（列加密）/ hash-pepper（哈希 HMAC），避免一钥多用 | 标准实践 |
| 6 | 透明层位置 | DAO 边界（storager 出入参映射），model/endpoints 零感知 | 所有读写路径（OpenAPI/报表/InnerAPI 导出）自动获得解密后数据 |
| 7 | api_key 按值查询 | 新增 `api_key_hash char(64)`（HMAC-SHA256(pepper, key) hex，不可逆）承接唯一键与按值查找；`uk_api_key`→`uk_api_key_hash`；`APIKeyFilter.Key` 在 storager 层转哈希下发 | AES-GCM 随机 nonce 密文不可查；model 层零改动 |
| 8 | 轮换生效 | **`/reload/security` 热加载为硬性要求**（keyring 原子替换，免重启） | 重启仅作兜底；runbook 全程免重启 |
| 9 | 收敛（存量重加密） | **强制 sweep 任务，不存在"自然收敛"**（评审指正）；复用同一机制支持 `mode=reencrypt`（默认，收敛到 active 钥）/`mode=decrypt`（仅回滚预案） | 冷数据行不触发写就永不收敛；decrypt 模式使回滚=原地解密（数据不动、无需导入恢复） |
| 10 | sweep 任务状态 | **DB 持久化**（评审决议）：`keyrotate_sweep_tasks` + 单行锁表，多实例共享；心跳失联接管 | 低频操作 + 审计留存 → DB 优于 Redis；多实例下触发/查询可能跨实例，进程内表不可行 |
| 11 | sweep 接口 | `POST /open-api/v1/security/reencrypt-sweeps`（202 异步）+ `GET .../{task_id}`；响应必带 `mode` 与 `active_key_id` | 复数集合符合仓内惯例；active_key_id 触发即可校验"热加载遗漏"型静默失败 |
| 12 | 启动语义 | fail-fast：库有密文但 keyring 缺失/错钥 → 拒启动；无密文无密钥 → 允许启动（渐进启用）；解密失败密文不传播 | 与 `access_control` 同一纪律 |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `lib/xcrypto/`（envelope/keyring/marker）；`stateful` `[Security]` 配置 + `/reload/security`；`model/keyrotate/`（sweep 管理器）；`endpoints/openapi_v1/security/`（2 端点）；`stateful/metrics.go` 计数器；DDL：`db_ddl.sql`/`db_ddl_sqlite.sql` 加 `api_keys.api_key_hash` 列、`keyrotate_sweep_tasks`、`keyrotate_sweep_lock` |
| 主要修改 | `storage/rdb/provider`（出入参加解密）；`storage/rdb/api_key`（双列写入 + filter 改道）；`model/iauth/features.go`（FeatureSecurity）；两个 `endpoints.go` 注册 |
| 明确不动 | OpenAPI/InnerAPI 既有端点契约（除新增 2 端点）；导出配置文件格式（下发链路加密另行立项，不在本变更）；`users.password`（口令散列化单独立项，已于 2026-10-06 实现：`modifications/2026-10-06-password-hash`）；conf-agent、bfe 数据面（本变更零改动）；KMS/Vault（商业版，`MasterKeyProvider` 接口预留） |
| 接口契约 | 新增 2 端点；无既有端点变更；响应包装遵循 `00-common.md`（ErrNum=403 不在本变更，本变更无新错误码进 OpenAPI 通用表，422/409 语义照既有） |
| 数据迁移 | `api_keys` 加列 + 唯一键切换（在线变更模板随 DDL 给出）；存量明文/哈希由 sweep 任务收敛，无需离线脚本 |

## 5. 非本仓登记点（链路协同）

| 位置 | 改动 | 状态 |
|------|------|------|
| `integration-test` 仓 | `secret_at_rest` 场景（多实例、轮换全链路、回滚预案、错误密钥 fail-fast） | 待落地 |
| 运维手册 | keyring 文件管理/SOP/回滚预案 runbook | 随发布交付 |
| `bfe/` | 下发配置文件字段级加密（`mod_ai_token_auth`/`server_data_conf` 加载点；独立变更，不在本次范围） | 另行立项 |

## 6. 文档配套

- 本文（change-summary）+ `design-changes.md`（配置模型/xcrypto/keyring/DAO/DDL/sweep/多实例流程/测试/WBS）+ `api-changes.md`（2 个新端点契约：字段表/错误码/枚举/示例）。三份文档自包含，全部评审决议已融入 change-summary 决策表。

## 7. 六步法核对

- [x] Step 1：变更目录 `2026-10-05-db-encryption-at-rest`
- [x] Step 2：本摘要 + design-changes.md + api-changes.md
- [x] Step 3：api-changes.md 即接口变更说明；实现时同步落 `api-define/OpenAPI接口定义/security.md` 并 review
- [ ] Step 4：sys-design——实现后沉淀 `details/`（密钥静态加密机制：信封规范/keyring/sweep/轮换 runbook），并更新 summary.md
- [ ] Step 5：代码实现（按 design-changes.md WBS）
- [ ] Step 6：落地后总结沉淀
