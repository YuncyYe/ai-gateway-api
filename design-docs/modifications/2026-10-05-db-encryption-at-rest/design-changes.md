# DB 落盘加密（密钥静态加密）：设计变更说明

> 配套：《change-summary.md》（背景、目标、关键决策、范围）、《api-changes.md》（接口契约）。
> 本文给出可落地的详细设计；评审决议全文见《change-summary.md》决策表。

---

## 1. 现状代码事实（设计约束来源）

| 事实 | 位置 | 约束 |
|------|------|------|
| provider 密钥明文入库 | `storage/rdb/provider/provider.go:358,369`（json.Marshal） | 加密点：两处 Marshal 后 Encrypt；Scan 后 Decrypt |
| api_key 明文 + 按值唯一性校验 | `storage/rdb/api_key/api_key.go`；`model/api_key/api_key.go:683-705`（`APIKeyFilter{Key}`） | 密文不可查 → 需 hash 索引，filter 在 storager 层改道 |
| 唯一键 | `db_ddl.sql:317` `uk_api_key(api_key)` | 迁移为 `uk_api_key_hash(api_key_hash)` |
| 双 DDL 纪律 | `db_ddl.sql` / `db_ddl_sqlite.sql` | 本次 3 处 DDL（加列 + 2 新表）双写 |
| 配置/校验/热加载范式 | `stateful/access_control.go`（2026-10-04 落地） | `[Security]`、`/reload/*`、fail-fast、atomic.Value 同款模式 |
| 多实例共享态范式 | `model/quota`（Redis 分布式锁）、本次评审决议 | sweep 状态走 DB（低频+审计），非 Redis |
| 错误包装 | `lib/xerror` → `xreq.Result`（ErrNum/ErrMsg/Data） | 新端点遵循 |

## 2. 配置模型（`stateful/config.go` 新增）

```go
type SecurityConfig struct {
    MasterKeyFile string `validate:"omitempty"` // keyring 文件（唯一注入方式，无 env）
    ActiveKeyID   int    `validate:"min=0,max=255"`
    // 运行时：keyring 编译产物，atomic.Value 承载（见 §3）
}
```

```toml
[Security]
# MasterKeyFile = "/etc/ai-gateway/master.keys"   # 0600、与数据目录异机
# ActiveKeyID = 1
```

启动校验（fail-fast）：配置了 MasterKeyFile 但读不到/解析失败 → 拒绝启动；库中**存在密文**（任一敏感列 LIKE 'enc$v1$%' 抽样）而无 keyring → 拒绝启动；无密文无 keyring → 允许启动（渐进启用）。

## 3. `lib/xcrypto/`（零外部依赖）

```text
lib/xcrypto/
├── envelope.go   # Encrypt(plaintext, keyID) / Decrypt(value)（marker 识别、明文直通、keyID 查钥）
├── keyring.go    # keyring{keys map[uint8][32]byte; active uint8}；LoadKeyringFile(path)；
│                 # HKDF 派生 db-enc-key / hash-pepper；atomic.Value 热替换
└── marker.go     # IsEncrypted(value) / KeyIDOf(value)
```

- 信封：`enc$v1$<base64(keyID(1B)|nonce(12B)|AES-256-GCM(ct))>`；**解密选钥自描述**（密文首字节 keyID → keyring 查表），无 AAD（防泄漏定位）。
- keyring 文件（TOML）：

  ```toml
  ActiveKeyID = 2
  [Keys]
    1 = "b64_old…"   # 收敛后删除
    2 = "b64_new…"
  ```

- 全局访问：`stateful` 内 `LoadKeyring() *xcrypto.Keyring`（atomic 加载）；`/reload/security` handler 重读文件 → 校验（ActiveKeyID 必须在 [Keys] 中）→ 原子替换，失败保留旧 keyring。

## 4. DAO 透明加解密层

| storager | 出库（DB→model） | 入库（model→DB） |
|----------|------------------|------------------|
| `storage/rdb/provider` | Scan 后 `Decrypt(api_keys)`（marker 判定，明文直通） | Marshal 后 `Encrypt(json, activeKeyID)` |
| `storage/rdb/api_key` | 同上对 `api_key` 列 | 同上；**同事务**写 `api_key_hash = HMAC-SHA256(pepper, plaintext)` |

- model/endpoints 全程明文，无感知。
- `APIKeyFilter.Key` → storager 层转 `KeyHash` 下发 SQL（`model` 零改动）；按值唯一性校验改走 hash。

> **修订注记（2026-10-21，issue #264，见 `modifications/2026-10-21-issue-264-provider-api-keys-column-type/`）**：
> 本节「Marshal 后 Encrypt」使 `providers.api_keys` 整列成为**一条信封**，而该列在建库 SQL 中声明为 `JSON`；
> MySQL 严格模式对 `JSON` 列做语法校验 → `Error 3140`，Provider 写接口整条失败。结论：「信封列必须是文本类」
> 是 §4 的**隐含前置条件**，不是可选优化——`providers.api_keys` 改为 `MEDIUMTEXT`，并新增启动期列类型自检
> `stateful.CheckEncryptedColumnTypes`（见 §5 下方补注）。

## 5. DDL（双写）

```sql
-- db_ddl.sql / db_ddl_sqlite.sql
ALTER TABLE api_keys ADD COLUMN api_key_hash char(64) NOT NULL DEFAULT '' COMMENT 'HMAC-SHA256(pepper, api_key) hex';
ALTER TABLE api_keys DROP KEY uk_api_key;
ALTER TABLE api_keys ADD UNIQUE KEY uk_api_key_hash (api_key_hash);
-- 存量回填：UPDATE api_keys SET api_key_hash = hmac(.....  由一次性迁移脚本/触发器完成（随 DDL 交付模板）

CREATE TABLE keyrotate_sweep_tasks (
  id bigint AUTO_INCREMENT PRIMARY KEY,
  task_id varchar(64) NOT NULL UNIQUE,
  status varchar(16) NOT NULL,          -- running | succeeded | failed
  mode varchar(16) NOT NULL,            -- reencrypt | decrypt
  dry_run tinyint(1) NOT NULL DEFAULT 0,
  scope varchar(16) NOT NULL DEFAULT 'all',
  active_key_id int NOT NULL DEFAULT 0,
  scanned bigint NOT NULL DEFAULT 0,
  rewritten bigint NOT NULL DEFAULT 0,
  summary text,
  error varchar(1024) NOT NULL DEFAULT '',
  heartbeat_at datetime NOT NULL,
  started_at datetime NOT NULL,
  finished_at datetime NULL,
  duration_ms bigint NOT NULL DEFAULT 0,
  created_by varchar(255) NOT NULL DEFAULT ''
);
CREATE TABLE keyrotate_sweep_lock (
  id bigint PRIMARY KEY,                -- 恒为 1
  holder_task_id varchar(64) NOT NULL DEFAULT ''
);
INSERT INTO keyrotate_sweep_lock (id) VALUES (1);   -- sqlite 同
```

> **补注（2026-10-21，issue #264）**：本文 §5 的变更清单漏了信封列的**列类型**。实际落地为：
> `providers.api_keys` 由 `JSON` 改为 `MEDIUMTEXT`（新建库 `db_ddl.sql`，存量库
> `database/db_ddl_upgrade_to_0.0.11.sql` §1.6，`ALGORITHM=COPY, LOCK=SHARED`）；SQLite 侧 `TEXT` 不变。
> 另新增启动期自检 `stateful.CheckEncryptedColumnTypes`（keyring 已加载时校验
> `providers.api_keys` / `api_keys.api_key` 非 `JSON`，命中即拒启动），防止「换二进制未改表结构」的
> 半升级部署以运行期 500 暴露。

## 6. sweep 管理器（`model/keyrotate/`）

参数：`BatchSize=100`、`HeartbeatTimeoutSec=300`、主键游标分页。

**触发（单例互斥，多实例安全）**：

```text
BEGIN
  SELECT holder_task_id FROM keyrotate_sweep_lock WHERE id=1 FOR UPDATE   -- 串行化触发
  读 holder 任务行：
    无 holder / 已终态            → INSERT 新任务行(running) + 更新锁行
    running 且心跳新鲜(<300s)     → ROLLBACK → 409(携带 holder_task_id)
    running 且心跳超时            → UPDATE 旧任务 failed('executor lost')（条件 status='running'）
                                     → INSERT 新任务行 + 更新锁行
COMMIT → 202 {task_id, status, mode, dry_run, scope, active_key_id}
```

**执行（后台 goroutine，按表循环）**：

```text
每批：BEGIN
  ① SELECT pk, <敏感列> WHERE pk > cursor ORDER BY pk LIMIT 100      -- 快照读
  ② 逐行 Go 侧判定（解 base64 首字节得 keyID）：
       reencrypt 模式：无 marker 或 keyID≠active → Decrypt(按行内 keyID 选钥) → Encrypt(active) → UPDATE
       decrypt  模式：有 marker → Decrypt → 明文写回（剥 marker）；无 marker → skip
       ※ api_key_hash 列不动（明文密钥的 HMAC 与密文无关）
  ③ UPDATE keyrotate_sweep_tasks SET scanned=+N, rewritten=+M, heartbeat_at=NOW() WHERE task_id=?
COMMIT；游标推进；批级死锁回退重试（业务优先）
完成：UPDATE tasks SET status='succeeded', finished_at, duration_ms, summary；锁行 holder 置空
```

- 任务表历史保留不 TTL（审计）；指标 `crypto_sweep_total{table,action}`、`crypto_sweep_running`。
- 全程**不触碰 Redis**；交互对象仅任务表/锁表/业务表。

## 7. OpenAPI 契约

> **契约权威来源为同目录《api-changes.md》**（字段级定义、错误码、枚举、示例）；实现时落 `api-define/OpenAPI接口定义/security.md`。遵循 `00-common.md` 包装（ErrNum=200 成功）。本节仅列设计要点。

**`POST /open-api/v1/security/reencrypt-sweeps`**（权限：FeatureSecurity/update）

请求体：`{ "dry_run": bool=false, "scope": "all"|"providers"|"api_keys", "mode": "reencrypt"|"decrypt" }`

202 响应 Data：`{ task_id, status:"running", mode, dry_run, scope, active_key_id }`
- `active_key_id` 必返：触发即可核对"重写目标钥"，防"改文件漏热加载"型静默失败；decrypt 模式返回 0。
错误码：401/402；409（互斥，带 holder task_id）；422（scope/mode 非法）。

**`GET /open-api/v1/security/reencrypt-sweeps/{task_id}`**（权限：FeatureSecurity/read）

200 Data：`{ task_id, status, mode, dry_run, scope, active_key_id, started_at, finished_at, duration_ms, summary{providers{scanned,rewritten,skipped}, api_keys{...}}, error }`
404 = task_id 不存在。审计：触发与完成均写 operation_logs（含 mode 与计数，无密钥材料）。

## 8. 失败语义

| 场景 | 语义 |
|------|------|
| 启动时库有密文但 keyring 缺失/错钥 | fail-fast 拒启动 |
| `/reload/security` 失败（格式错/ActiveKeyID 无对应条目） | 返回错误，旧 keyring 保持生效 |
| 库内解密失败（keyID 未知/数据损坏） | 读路径 DAO 错误 → 5xx；日志只记 keyID/长度，密文不落日志 |
| sweep 解密失败 | 该任务 failed（error 可归因），已提交批不回滚 |
| 指标 | `crypto_decrypt_fail_total{table,key_id}`（8284 端口） |

## 9. 测试计划

- 单测：xcrypto 往返/marker/多 keyID/HKDF/keyring 文件加载；provider/api_key storager 明文兼容、写后密文、读回一致；hash filter；sweep 判定与双模式；锁表互斥与接管（可注入时钟）。
- 集成 `test/integration/tests/secret_at_rest/`：**直读 SQLite 文件断言无明文与 `enc$v1$` 前缀**；API 读回/导出明文一致；错误密钥启动 fail-fast；导入唯一性回归；**轮换全链路：热加载 → dry-run（核对 active_key_id）→ 正式 sweep → 摘钥 → 旧钥读失败**；**回滚预案：mode=decrypt → 复核无残留 → 模拟旧版本明文直读**；**多实例（共享 DB）：A 触发 B 查询、B 重复触发 409、杀执行实例后接管**。
- **MySQL 严格模式（2026-10-21 补，issue #264）**：SAR 模块支持 MySQL 后端（`AIAPI_MYSQL_DSN`），新增用例断言
  Provider 建/改在真实 MySQL 下 2xx、落库为信封、列类型非 `JSON`，并覆盖 `scope=providers` 的 sweep 收敛。
- 回归：既有 api_key/provider 全部用例。

## 10. WBS

| # | 任务 | 改动 | 量 |
|---|------|------|----|
| 1 | lib/xcrypto | envelope/keyring/marker + HKDF | 1 d |
| 2 | [Security] 配置 + keyring 加载 + `/reload/security` + 启动校验 + 指标 | stateful | 0.5 d |
| 3 | provider storager 加解密 | storage/rdb/provider + 单测 | 0.5 d |
| 4 | api_key 哈希索引 | DDL 双写 + storager 双列/filter 改道 + 回填模板 + 唯一性回归 | 1.5 d |
| 5 | sweep 管理器 | model/keyrotate（端点/锁表/心跳/接管/游标/批量）+ 2 张 DDL | 0.5 d |
| 6 | 集成测试 secret_at_rest | test/integration | 1 d |
| 7 | 文档 | api-define/security.md + modifications + 运维手册 | 0.5 d |

合计约 5.5 人日。

## 11. 风险与对策

| 风险 | 对策 |
|------|------|
| keyring 文件丢失 | 双人持有/异地备份；上线 checklist"先备份 keyring 再启用加密"；KMS 商业版托管 |
| 回滚到无加密版本 | 回滚预案（见 change-summary 决策 #9 与本文 §6 sweep decrypt 模式）：新版本 `mode=decrypt` sweep 原地解密（数据不动、免导入）→ 复核无残留 → 回滚；发布单关联 keyring 版本与 task_id |
| `uk_api_key` 在线变更 | DDL 附在线变更模板（MySQL 8.0 能力依赖行内实例）；sqlite 重建 |
| 双 DDL 漂移 | 双写 + 既有纪律 |
| 密文进日志/慢日志 | 解密失败日志只记 keyID/长度；ioperlog 既有脱敏保持 |

## 12. 开放问题（实现前需评审确认）

1. `api_key_hash` 是否进报表（按值检索调用日志取舍）——倾向不进。
2. DB 主密钥"分段掌握"——一期不做，KMS 覆盖。
3. SM4 随信创包提前与否——Provider 接口已预留。
4. 按数据类别分密钥——一期单 active 钥；keyID 为 1 字节（255 空间）且解密按密文自带 keyID 选钥，分域演进时解密路径零改动（见 change-summary 决策 #2）。
