# Issue #264：启用 DB 落盘加密后 `POST /providers` / `PATCH /providers/{name}` 必然 500（`providers.api_keys` 声明为 `JSON` 列，与「整列信封」互斥）修复方案

> 2026-10-21：**已实施完毕（同批）**。性质：**设计缺口**，不是实现偏离设计——`2026-10-05-db-encryption-at-rest/design-changes.md` §4 明文规定
> 「Marshal 后 `Encrypt(json, activeKeyID)`」即**整列一条信封**，而同批交付的 §5 DDL 只给了 `api_keys` 表的
> 变更，漏了 `providers.api_keys`；该列在建库 SQL 中仍是 `JSON`（`db_ddl.sql:467`）。MySQL 严格模式对
> `JSON` 列做语法校验，信封不是合法 JSON，写入必失败。
> 本卡在 §2 给出源码逐环 + 本机 MySQL 8.4.9 实测证据，§5 给出方案决策，§6/§7 给出改动与测试清单。
> **交付批次（2026-10-21 决定）**：§6.1 列类型修复 + §6.2 启动期列类型自检 + §6.3 读路径错误上抛**同批实现**，
> 不分阶段发布——自检单独上线会把「写接口 500」变成「服务拒绝启动」，而列降为文本后必须由读路径自己兜住坏值。

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/264](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/264)

> 在 `[Security]` keyring 已配置、DB 列加密生效的部署中，通过管理面 `POST /open-api/v1/providers` 创建契约
> 合法的 Provider 必然返回 HTTP 500：`Database Exception: Error 3140 (22032): Invalid JSON text: "Invalid
> value." at position 0 in value for column 'providers.api_keys'.`；`keys` 取 `[]` 时同样失败。
> 对照组 `api_keys.api_key` 的 13/13 行已是 `enc$v1$` 信封，说明加密写路径本身可用；失败集中在
> `providers.api_keys` 这一列。结果：加密一旦启用，Provider 配置只能读、不能增改。

产品条款（Oracle，均属 `approved` / `normative`）：

- `2026-10-05-db-encryption-at-rest/design-changes.md` §4「DAO 透明加解密层」：`storage/rdb/provider` 入库
  「Marshal 后 `Encrypt(json, activeKeyID)`」，出库「Scan 后 `Decrypt(api_keys)`（marker 判定，明文直通）」；
  model/endpoints 全程明文、零感知；
- 同目录 `change-summary.md` 决策 2：信封格式 `enc$v1$<...>`，**无前缀即明文直通**；决策 6：透明层位于 DAO 边界；
- 即：加密启用后 Provider 的管理面写入必须成功、落库值为信封、读回值恢复为业务明文。

`A-01`（入库路径 + DAO 边界）与 `A-02`（出库路径 + 信封格式）当前在真实 MySQL 部署下全部失败。

## 2. 现状与根因（2026-10-21 对当前检出逐环直读 + MySQL 8.4.9 实测）

### 2.1 调用链逐环

1. **接口层** `endpoints/openapi_v1/provider/create.go:43`（`POST /providers`）→ `model/iprovider` 的
   `ProviderManager.CreateProvider` → `storage/rdb/provider/provider.go:171`（`toDAOParam`）。
2. **序列化 + 加密** `storage/rdb/provider/provider.go:186-196`：`marshalJSON(param.Keys)` 先把整个 key 数组
   序列化成**一段 JSON 文本**，紧接着交给 `stateful.EncryptIfEnabled`（`stateful/security.go:121`）→
   `Keyring.Encrypt`（`lib/xcrypto/keyring.go:85`），产出**单条** `enc$v1$<base64(keyID|nonce|ct+tag)>`。
   即「整列一条信封」，与 §4 逐字一致，是**设计意图**而非缺陷。
3. **DAO 绑定** `storage/rdb/internal/dao/table_providers.go:202`（`TProviderCreate`）把该字符串绑定到
   `providers.api_keys`；`TProvider` 侧该列是 `string`（`table_providers.go:35`），**DAO 层不感知列类型**。
4. **列类型冲突（根因）** `db_ddl.sql:467`：`api_keys JSON COMMENT 'API key 列表'`。MySQL 严格模式
   （默认 `STRICT_TRANS_TABLES`）对 `JSON` 列做语法校验，`enc$v1$…` 不是合法 JSON → `Error 3140` →
   整条 `INSERT` 失败。
5. **错误包装** `lib/xerror` → `xreq.Result`：SQL 错误统一归为 `Database Exception` → HTTP 500；请求未落库，
   回读 404，无残留（与 issue 观测一致）。
6. **读路径** `provider.go:251-258`（`fromDAO`）对整列 `DecryptIfEnabled(one.Keys)`，与 §4 出库路径一致，
   所以「明文存量行可正常读、加密行写不进去」这一非对称现象完全由列类型解释。
7. **PATCH 同源** `provider.go:309-334`（`toDAOParamForUpdate`）在 `keys` 非 nil 时走相同的
   `EncryptIfEnabled`（`:327`）；`endpoints/openapi_v1/provider/update.go:34-35` 当前暴露的是
   `PATCH /providers/{provider_name}`（无 `PUT` 端点），带 `keys` 的 PATCH 同样 500，不带 `keys` 的 PATCH
   正常（DAO nil-skip 不写该列）。
8. **附带受损面：keyrotate sweep**。`model/keyrotate/tables.go:31` 把 `providers.api_keys` 列为扫描列，
   `storage/rdb/keyrotate/keyrotate.go:321-325` 用 `UPDATE providers SET api_keys=?` 写回：
   - `mode=reencrypt`：每行都要写入信封 → MySQL 上首个 provider 行即 3140 → 任务 `failed`；
   - `mode=decrypt`：库内不可能存在信封（写不进去），全部 skip → 任务成功但无实际收敛。
   **即：加密启用后不仅管理面不能增改 Provider，连「存量明文行收敛为密文」这条路在 MySQL 上也是断的**。

### 2.2 MySQL 8.4.9 本机实测（2026-10-21，临时实例，`sql_mode` 含 `STRICT_TRANS_TABLES`）

```sql
-- 1) 复现：JSON 列拒收信封（报文与 issue 逐字一致）
INSERT INTO providers (name, api_keys) VALUES ('enc', 'enc$v1$AAAAAAAB');
-- ERROR 3140 (22032): Invalid JSON text: "Invalid value." at position 0 in value for column 'providers.api_keys'.

-- 2) 修复语句：JSON -> MEDIUMTEXT
ALTER TABLE providers
  MODIFY COLUMN api_keys MEDIUMTEXT NULL COMMENT 'API key 列表（列加密整列信封 enc$v1$ 前缀；明文 JSON 兼容）';
-- OK；存量行原样保留（见下）；随后信封、RAW 70KB 值均可写入

-- 3) 迁移前的存量值（MySQL 输出的 JSON 规范化文本，语义等价）
[{"key": "sk-aaa", "weight": 1}]
[]

-- 4) 迁移算法：JSON<->文本 只能 COPY，且 COPY 必须带锁
ALTER TABLE t MODIFY COLUMN api_keys MEDIUMTEXT NULL, ALGORITHM=INPLACE, LOCK=NONE;
-- ERROR 1846: Cannot change column type INPLACE. Try ALGORITHM=COPY.
ALTER TABLE t MODIFY COLUMN api_keys MEDIUMTEXT NULL, ALGORITHM=COPY, LOCK=NONE;
-- ERROR 1846: LOCK=NONE is not supported. COPY algorithm requires a lock.
ALTER TABLE t MODIFY COLUMN api_keys MEDIUMTEXT NULL, ALGORITHM=COPY, LOCK=SHARED;  -- OK

-- 5) 反向回滚：存在信封行时 MEDIUMTEXT -> JSON 直接失败（同一 3140）
ALTER TABLE t_back MODIFY COLUMN api_keys JSON NULL;
-- ERROR 3140 (22032): Invalid JSON text ... in value for column '#sql-xxxx.api_keys'.
```

结论：**根因成立且可判据化**——修复后 `providers.api_keys` 能落信封、能读回明文；迁移语句在 8.4 可用；
迁移窗口会阻塞写（`COPY + LOCK=SHARED`：允许读、阻塞写）；回滚路径必须先解密再改类型。

### 2.3 为什么既有回归全绿却没拦住

| 漏点 | 事实 |
|------|------|
| SQLite DDL 是 `TEXT` | `db_ddl_sqlite.sql:485` `api_keys TEXT`，SQLite 不校验 JSON，加密写路径在 SQLite 上天然可用 |
| SAR 测试只在 SQLite 跑 | `test/integration/tests/secret_at_rest/secret_at_rest_test.go:93`（`StartServerWithMonitor`）+ `:106`（`sqlite-strip` 直读），断言信封合法但后端不是 MySQL |
| 设计文档 §5 DDL 只覆盖 `api_keys` | `design-changes.md:70-102` 的 DDL 变更清单没有 `providers.api_keys` 的类型调整 |
| 升级脚本同样漏了 | `database/db_ddl_upgrade_to_0.0.11.sql:26-31`（§1.2）把 `api_keys.api_key` 改成 `varchar(255)` 并注明「加密态存储，enc$v1$ 前缀」，但 §1.4 对 `providers` 只加 `instance_source`/`k8s_*`，全文无该列的 `MODIFY` → 按升级脚本对齐表结构后缺陷依旧（与 issue 的两次升级重测一致） |
| 部署模板另有两份建表副本 | `ai-gateway/api_db_ddl.sql:465`、`ai-gateway/kubernetes/deploy/mysql-deploy.yaml:596`（`ai-gateway` 仓）同样声明 `api_keys JSON`；属跨仓移交项，本卡不修改 |

## 3. 与 issue 报告的差异（逐条核实）

| issue 表述 | 核实结果 |
|-----------|---------|
| `PUT /providers/{name}` 预计同样失败 | 当前检出**没有** `PUT /providers/{name}`；实际端点为 `PATCH /providers/{provider_name}`（`update.go:34-35`）。带 `keys` 的 PATCH 同样 500（`:327`），不带 `keys` 不受影响 |
| 复用 `ProviderManager` 的 Cluster 收敛路径（`cluster.go:990`）"写入待确认" | 该处是 `ProviderInstancePoolSyncer` hook，本身不写 providers；`model/ik8s_pool/k8s_pool.go:192` 的写只带 `K8sInstancePool`（`Keys` 为 nil → 该列不进 UPDATE）→ **不受影响** |
| 「存量明文行随 sweep 收敛后也无法新增」 | 更严重：MySQL 上 `scope=providers` 的 reencrypt sweep **本身就会失败**（§2.1 第 8 环），存量行无法收敛 |
| 严重性 P0 | 认同：阻断 SC3201 A-01/A-02 全部断言，且使安全特性在真实 MySQL 部署不可用 |

## 4. 目标

1. 加密启用（keyring 已加载）时，`POST /providers`、`PATCH /providers/{name}`（含 `keys`）在 **MySQL 严格模式**下
   返回 2xx；`providers.api_keys` 落库为单条 `enc$v1$` 信封，读回为业务明文；
2. `POST /providers` 在 `keys` 缺省 / `[]` / 多 key 三种情形下均可成功；
3. `reencrypt-sweeps`（`scope=providers` 与 `all`）在 MySQL 上成功，存量明文行收敛为 `activeKeyID` 信封；
4. 未配置 keyring 的部署行为零变化（明文直通、JSON 语义不变）；
5. 新建库用 `db_ddl.sql`、存量库用升级脚本两条路径都修好；发布/部署仓（`ai-gateway`）的建表副本由该仓
   按自身流程同步，本卡只做移交提示、不跨仓修改；
6. 建立可判据化的回归：MySQL 严格模式下 Provider 写路径有用例覆盖，DDL 与「信封列」约定有静态一致性检查；
7. **同批**把两类「沉默失败」堵住（§6.2/§6.3）：漏执行 DDL 的部署在启动期就报错并给出修复语句，而不是运行期
   500；列降为文本后读路径遇到坏值必须报错，而不是静默当成「provider 没有 key」。

## 5. 方案决策

| 方案 | 做法 | 结论 |
|------|------|------|
| **A（采纳）列类型改为文本** | `providers.api_keys` → `MEDIUMTEXT`（保留「整列一条信封」的 §4 冻结契约，代码零改动） | 改动最小、可经升级脚本覆盖存量库、与 `api_keys.api_key`（`varchar`）同构；缺点见 §5.2/§5.3 |
| B 逐元素信封 | 保持 `JSON` 列，改成 `["enc$v1$…","enc$v1$…"]` 或每元素一个信封对象 | **否决**：违反 §4 冻结契约（出库是「整列 Decrypt」）；`stateful/security.go:279-299` 的启动探测 `LIKE 'enc$v1$%'` 永远匹配不到（值以 `[` 开头）→ fail-fast 纪律失效；`model/keyrotate` 的整列 transform、导出路径、既有 SAR 断言全部要改；回滚到 v0.0.10 后老代码读到密文元素直接当 key 使用 |
| C JSON 字符串包一封 | 保持 `JSON` 列，写成 `"enc$v1$…"`（带引号） | **否决**：读路径要额外剥引号；`LIKE 'enc$v1$%'` 探测同样失效；语义上仍在「合法 JSON」与「信封」之间做隐式约定，比 A 更脆 |

### 5.1 方案 A 的完整改动

1. **新建库** `db_ddl.sql:467`：

   ```sql
   `api_keys` MEDIUMTEXT NULL COMMENT 'API key 列表（列加密整列信封 enc$v1$ 前缀；无前缀为明文 JSON 兼容）',
   ```

   选 `MEDIUMTEXT`（16MB）而非 `TEXT`（64KB）：provider key 池是多 key 数组，`TEXT` 在严格模式下会以
   `1406 Data too long` 拒绝，构成新的容量陷阱。保持 `NULL` 可空（与存量行、`[]` 语义一致）。
2. **存量库** `database/db_ddl_upgrade_to_0.0.11.sql` 新增「§1.6 providers：api_keys 列扩容（加密态存储）」：

   ```sql
   -- 1.6 providers：api_keys 列扩容（加密态存储，issue #264）
   -- 整列信封 enc$v1$… 不是合法 JSON，而本列原声明为 JSON；不迁移则任何 provider 写入
   -- 在 MySQL 严格模式下报 Error 3140。JSON->文本只能 COPY 算法（阻塞写、允许读）。
   ALTER TABLE `providers`
     MODIFY COLUMN `api_keys` MEDIUMTEXT NULL COMMENT 'API key 列表（列加密整列信封 enc$v1$ 前缀；无前缀为明文 JSON 兼容）',
     ALGORITHM=COPY, LOCK=SHARED;
   ```

   同脚本「手动步骤」章节补一条：**已在 2026-10-20 之前执行过本脚本的存量库，只需补执行 §1.6 这一条**
   （脚本非幂等，不要整体重跑）。回滚说明：`MEDIUMTEXT -> JSON` 前必须先跑
   `POST /open-api/v1/security/reencrypt-sweeps {"mode":"decrypt","scope":"all"}` 至 succeeded 并复核无
   `enc$v1$` 残留，否则 `ALTER` 会以 3140 失败（§2.2 第 5 项实测）。
3. **SQLite**：`db_ddl_sqlite.sql:485` 已是 `TEXT`，无需改类型；仅补一行注释说明「信封列，并非 JSON」。
4. **代码零改动**：DAO 用 `string` 绑定（`table_providers.go:35`），加解密在 `provider.go` 的 `toDAOParam`/
   `fromDAO`，与列类型无关；`unmarshalKeys` 照常解析明文 JSON。
5. **跨仓建库副本（不在本仓修改，作为移交项）**：`ai-gateway/api_db_ddl.sql:465`、
   `ai-gateway/kubernetes/deploy/mysql-deploy.yaml:596` 也声明了 `providers.api_keys JSON`，它们是
   `ai-gateway`（发布/装配仓）里的建库副本，**由该仓按自身发布流程同步为 `MEDIUMTEXT`**，本卡不跨仓改动；
   已 grep 全组织，无第三方查询依赖该列的 `JSON` 类型（无 `JSON_EXTRACT`/`JSON_VALID` 用法）。

### 5.2 迁移窗口与容量

- JSON→文本转换只能 `ALGORITHM=COPY`（实测 `INPLACE` 直接报 1846），COPY 必须带锁（`LOCK=NONE` 报 1846）
  → 迁移期间**阻塞写、允许读**；
- `providers` 表行数通常只有数十~数百，表重建毫秒级；仍需避免与长事务/大查询并行，并纳入发布窗口；
- 列内容体积上限从 `JSON`（受 `max_allowed_packet` 约束）收紧到 16MB，对 provider key 池无实际影响。

**存储开销实测（MySQL 8.4.9，同样的值灌两列，2026-10-21）**：改文本**不会**放大存储，反而略小——
`JSON` 的二进制表示带每文档字典/偏移开销且恒走 LOB 页，文本就是实际字节：

| 形态 | 值大小 × 行数 | `JSON` 列 | `MEDIUMTEXT` 列 |
|------|---------------|-----------|-----------------|
| 3 key（真实形态） | 229 B × 5000 | 2576 KB | 1552 KB（−40%） |
| 100 key 大池 | 7693 B × 500 | 8720 KB | 4624 KB（−47%） |
| 3 key（早期一组） | 240 B × 1000 | 352 KB | 336 KB（−4%） |

补充定性：`MEDIUMTEXT` **不预分配** 16MB（只存实际字节 + 3 字节长度前缀，比 `TEXT` 多的行内开销是 1 字节）；
读写路径仍是 Go `string`，与列类型无关；该列无索引、不进 `WHERE`/`ORDER BY`/`GROUP BY`，无排序/临时表风险。
**唯一实质增量来自加密本身**（与列类型无关、且已随设计冻结生效）：信封 = `7 + 4×ceil((N+29)/3)` 字节，
即 229 B 明文 → 351 B（×1.53）、7693 B → 10303 B（×1.34），代价是「落盘密文」这一安全目标的对价。

### 5.3 方案 A 的代价（需接受）

1. **失去 DB 级 JSON 合法性校验**：此前非法 JSON 会被 MySQL 拒写，改为文本后只能靠 Go 侧 marshal 保证。
   当前 `unmarshalKeys`（`provider.go:435-444`）解析失败**静默返回 nil**，即坏值会表现为「provider 没有 key」
   而非报错（更难排查）。→ 由 §6.3 同批补偿。
2. **半升级部署会以 500 暴露**：先换二进制、后改表结构时，Provider 写接口 500（读接口正常）。→ 由 §6.2 同批
   启动自检把「忘执行 DDL」前移为启动期错误。

## 6. 代码/DDL 改动清单

**全部改动同一批交付（不分阶段）**，原因有二：

- §6.2 的启动自检单独发布会把「写接口 500」升级为「服务拒绝启动」，必须与 §6.1 的 DDL 同批，否则是净负面；
- §6.3 是 §6.1 的必要补偿——列从 `JSON` 降为文本后失去 DB 级 JSON 校验，读路径必须自己把坏值报出来。

### 6.1 数据层：DDL 双写 + 升级脚本（同批）

| # | 文件 | 改动 |
|---|------|------|
| 1 | `db_ddl.sql:467` | `JSON` → `MEDIUMTEXT NULL` + 注释（§5.1-1） |
| 2 | `database/db_ddl_upgrade_to_0.0.11.sql` | 新增 §1.6（含 `ALGORITHM=COPY, LOCK=SHARED`）+ 手动步骤补注 + 回滚说明 |
| 3 | `db_ddl_sqlite.sql:485` | 仅注释（类型已是 `TEXT`） |
| — | `ai-gateway` 仓的两份建库副本 | **不在本仓修改**：作为移交项通知该仓同步（见 §10） |

### 6.2 启动期列类型自检：`stateful.CheckEncryptedColumnTypes()`（同批）

调用点紧随 `main.go:112` 的 `CheckSecretAtRest`：

- 仅当 `SecretRing() != nil` 时生效（无 keyring 时明文 JSON 与 `JSON` 列兼容，不应阻断渐进启用）；
- MySQL：`SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND ((TABLE_NAME='providers' AND COLUMN_NAME='api_keys') OR (TABLE_NAME='api_keys' AND COLUMN_NAME='api_key'))`，
  `DATA_TYPE='json'` 即报错，报文直接给出待执行语句与脚本位置（`database/db_ddl_upgrade_to_0.0.11.sql §1.6`）；
- SQLite：`PRAGMA table_info(...)`，声明类型含 `JSON` 才报（当前恒过）；
- 语义选择：**拒绝启动**（与 `CheckSecretAtRest` 的 fail-fast 纪律一致，且错误信息可自助修复）；
- 单测：SQLite 内存库建 JSON 类型表 → 断言返回错；正常表 → nil。

### 6.3 读路径错误上抛：`unmarshalKeys`（同批，补偿方案 A 的代价）

`storage/rdb/provider/provider.go:435-444`：`unmarshalKeys` 改为返回 `(keys []ProviderKey, err error)`，
`fromDAO`（`:251`）对解析失败返回错误（**密文与坏值都不外泄**，与 §4「fromDAO 返回 error，密文不传播」同款）。
理由：列为文本后 DB 不再兜底校验，静默 nil 会把「配置损坏」伪装成「provider 未配 key」。

### 6.4 不改动

- `lib/xcrypto`（信封格式不变）、`stateful.EncryptIfEnabled`/`DecryptIfEnabled`、`model/keyrotate` 逻辑、
  `model/iroute_conf/exporter.go:113`（provider keys → 集群配置 `AIConf.Keys` 的导出加密）均零改动；
- OpenAPI 契约零改动（Provider 字段、响应、错误码表不变；信封永不进报文）。

## 7. 测试方案

### 7.1 DDL 静态一致性单测（防复发，新增）

`test/ddl/ddl_test.go`（`package ddl_test`，不要 MySQL/SQLite 实例，直接读文件）：

- 解析 `db_ddl.sql` / `db_ddl_sqlite.sql` 的 `CREATE TABLE`，断言敏感列白名单
  `{providers.api_keys, api_keys.api_key}` 的声明类型 ∈ 文本类（`TEXT`/`MEDIUMTEXT`/`VARCHAR`），
  **不得为 `JSON`**；
- 反向断言：DDL 中所有 `JSON` 列不包含上述白名单列（等价表述，便于读懂失败信息）；
- 附断言：`database/db_ddl_upgrade_to_0.0.11.sql` 存在对 `providers.api_keys` 的 `MODIFY`（升级路径不漏）。

### 7.2 MySQL 后端 SAR 集成（核心判据）

现状：`secret_at_rest` 模块固定 SQLite。改造（`test/integration/testutil`）：

1. `mysql.go`：抽出 `startMySQLServer(adminDSN, extraTOML string, monitorPortMode int)`；`StartServerWithMySQL`
   保持签名（内部传 `""`、`0`）；新增 `StartServerWithMonitorAuto(extraTOML)`——`AIAPI_MYSQL_DSN` 非空走向 MySQL，
   否则 `StartServerWithMonitor`；
2. `secret_at_rest_test.go`：`startSARServer` 改用自动变体；`openRawDB` 按 `sm.MySQLDSN()` 选 `mysql` / `sqlite-strip`
   驱动（直读断言语义不变）；SAR-1-003（共享 SQLite 文件 + `StartServerWithSharedInfra`）在 MySQL 下 `t.Skip`；
   SAR-2-003 预置 300 行的直写 SQL 保持方言中立。

新增用例 **SAR-4-001「MySQL 严格模式 Provider 写路径」**（同一实现双后端可跑，仅 schema 断言在 MySQL 下生效）：

1. 注入 keyring（active=1）→ `POST /providers`（含 1 个 key）→ **2xx**；
2. 直读 DB：`providers.api_keys` 为 `enc$v1$` 信封且 keyID=1；`information_schema` 断言
   `DATA_TYPE != 'json'`（SQLite 下跳过）；
3. `PATCH /providers/{name}` 带新 `keys` → 2xx → 直读 DB 信封刷新、keyID=1；
4. `POST /security/reencrypt-sweeps {"scope":"providers","mode":"reencrypt"}` → 轮询 `succeeded`，
   `summary.providers.rewritten ≥ 2`（seed `deepseek` + 新建），直读 DB 全部 provider 行均为信封；
5. `GET /providers/{name}` 读回业务明文，报文不含 `enc$v1$`。

同时把既有 SAR-1-002（建 provider + api-key 落盘信封）与 SAR-2-002（decrypt 回滚）纳入 MySQL 回归——
这两例在修复前即为本 issue 的最小复现。

### 7.3 单测/回归

- 单测（同批新增，覆盖 §6.2/§6.3）：`stateful` 包内 SQLite 内存库——
  `providers.api_keys` 声明为 `JSON` 且已加载 keyring → `CheckEncryptedColumnTypes` 返回错（报文含 §1.6 提示）；
  声明为 `MEDIUMTEXT` → nil；未加载 keyring（`JSON` 列）→ 仍为 nil（渐进启用不被阻断）；
  `storage/rdb/provider` 包内——`providers.api_keys` 存坏值（如 `not-json`）→ `fromDAO` 返回错而非空 key；
  合法值/空值/`null` → 行为不变；
- `go build ./...`、`go vet ./...`、`go test ./...` 全绿；`make test-model-cover-gate`（model ≥70%，
  本卡 model 层无改动）；
- SQLite 集成：`test/integration/tests/secret_at_rest/...`、`tests/provider/...`、`tests/innerapi/...` 全量 PASS
  （方案 A 对 SQLite 是无操作，预期零变化）；
- MySQL 集成：`AIAPI_MYSQL_DSN=... go test ./tests/secret_at_rest/...`（7.2）；另跑 `tests/provider/...` 确认
  无回归。

### 7.4 验收判据（对齐 issue 的观测指纹）

| 判据 | 期望 |
|------|------|
| `POST /open-api/v1/providers`（keyring 启用、契约合法） | 2xx；不再出现 `Error 3140 ... 'providers.api_keys'` |
| `GET /open-api/v1/providers/{name}` | 200，`keys` 为业务明文 |
| DB 直读 `providers.api_keys` | `enc$v1$` 信封，keyID = active |
| `db-security-observer /v1/db/encryption-state` | `providers.api_keys` 出现 encrypted 行、`plaintext_leak=false` |
| `POST /security/reencrypt-sweeps`（scope=providers） | `succeeded`，存量明文行收敛 |
| SC3201-TC017 `setup-readback` / SC1401-TC017 EX-07 400/500/截断 arm | 由 `FAILED_PRODUCT` / `BLOCKED_ENVIRONMENT` 转为可执行 |

## 8. 文档与契约同步

| 文档 | 改动 |
|------|------|
| `design-docs/api-define/` | **无改动**（接口契约零变化；本卡是运行行为向已批准设计收敛） |
| `design-docs/sys-design/数据库设计文档.md` | `providers` 建表片段与 §940 要点：`api_keys` 改为 `MEDIUMTEXT`，并写明「信封不是 JSON，故敏感列必须是文本类型」 |
| `design-docs/sys-design/details/密钥静态加密.md:9-10,20` | 同口径补充一句约束：整列信封 ⇒ 列类型必须为文本类 |
| `2026-10-05-db-encryption-at-rest/design-changes.md` §5 / §9 | 以**修订注记**方式追加（不重写冻结正文）：§5 补 `providers.api_keys` 的 `MODIFY`，§9 补「MySQL 严格模式 Provider 写路径」用例 |
| `test/integration/tests/secret_at_rest/design.md` | 补「后端选择（SQLite / MySQL 自动）」段与 SAR-4-001 登记 |
| `docs/zh_cn/upgrade.md`（v0.0.11） | 补一条：升级需执行升级脚本 §1.6；已执行过旧脚本的库补执行该单条语句 |
| `CHANGELOG.md`（`[Unreleased] → Fixed`） | `POST /providers`、`PATCH /providers/{name}` 在启用 DB 落盘加密时 500 的修复；含升级提示 |

## 9. 发布与回滚

**同批交付**：§6.1（DDL）+ §6.2（启动自检）+ §6.3（读路径错误上抛）在同一版本内落地，顺序为
**先执行 DDL（升级脚本 §1.6 / 新建库直接 `db_ddl.sql`）→ 再替换二进制**；若同批替换则 §6.2 的启动自检会把
「漏执行 DDL」直接变成启动失败（而不是运行期 500），这正是该自检的目的。

**回滚到 v0.0.10（无加密能力）**：
1. `POST /open-api/v1/security/reencrypt-sweeps {"mode":"decrypt","scope":"all"}` → 至 `succeeded`；
2. 复核两列无 `enc$v1$` 残留（`GET /security/reencrypt-sweeps/{task_id}` + DB 抽检）；
3. 如需把列类型改回 `JSON`（仅当回滚涉及依赖该类型的工具链）：此时存量均为合法 JSON，`ALTER` 可成功；
   Go 侧对 `MEDIUMTEXT` 无感，**保留文本类型不构成回滚障碍**；
4. 回滚二进制。

**已升级库的补丁路径**：`ALTER TABLE providers MODIFY COLUMN api_keys MEDIUMTEXT NULL ...,
ALGORITHM=COPY, LOCK=SHARED;`（一条语句，勿整体重跑升级脚本）。

## 10. 风险与遗留

| 风险 | 对策 |
|------|------|
| 迁移锁窗口（COPY 阻塞写） | providers 行数小；发布窗口执行；执行前确认无长事务 |
| 半升级（换二进制未改表） | §6.2 启动自检 fail-fast + 升级文档显式提示 |
| 失去 DB 级 JSON 校验 | §6.3 让 `unmarshalKeys` 失败上抛；写入仍由 Go marshal 保证 |
| 发布/部署仓建表副本漂移 | 本仓不改跨仓文件；移交 `ai-gateway` 仓按自身流程同步，并在发布清单加一项「grep 全组织 `api_keys.*JSON` 为 0」 |
| 客户自定义 SQL 依赖 `JSON` 类型 | 组织内 grep 无 `JSON_EXTRACT`/`JSON_VALID` 用法；对外文档说明该列「JSON 文本」语义不变 |

**不开的坑（明确不做）**：不改信封格式、不改 keyrotate 逻辑、不为该列引入 `JSON_VALID` 之类的二次校验（信封与
JSON 本来就互斥，任何「两全」方案都会重新引入 §5 否决的 B/C 方案的耦合）。

## 11. 实施记录

2026-10-21 同批落地完毕（§6.1 + §6.2 + §6.3 + §7.1/§7.2），并完成 SQLite 与 MySQL 双后端验证。

**改动文件**

| 层 | 文件 | 内容 |
|----|------|------|
| DDL | `db_ddl.sql:467` | `api_keys` `JSON` → `MEDIUMTEXT NULL`（注释标明信封语义） |
| DDL | `db_ddl_sqlite.sql:485` | 仅补注释（已是 `TEXT`，SQLite 不校验 JSON） |
| DDL | `database/db_ddl_upgrade_to_0.0.11.sql` | 新增 §1.6（`ALGORITHM=COPY, LOCK=SHARED`）+ §3.3 补丁路径与回滚说明 |
| 代码 | `stateful/security.go` | 新增 `CheckEncryptedColumnTypes()` + MySQL（`information_schema`）/SQLite（`PRAGMA table_info`）两条探测 |
| 代码 | `stateful/config_database.go` | 提取 `bfeDBName` 常量（`BFEDB` 复用），供驱动判定 |
| 代码 | `main.go:112-121` | `CheckSecretAtRest` 之后调用新自检，失败即 `stateful.Exit` |
| 代码 | `storage/rdb/provider/provider.go` | `unmarshalKeys` 改为返回 error（不回显值），`fromDAO` 解析失败即报错 |
| 测试 | `stateful/security_columns_test.go`（新增） | 4 例：JSON+keyring 拒启（含报文断言）/ 反向列 / TEXT 通过 / 无 keyring 与未知驱动跳过 |
| 测试 | `storage/rdb/provider/provider_test.go` | 新增坏值用例（`not-json` 报错、`''`/`null`/`[]` 仍为空 key）；helper 拆出 `setupTestStoragerWithDB` |
| 测试 | `test/ddl/ddl_test.go`（新增） | 静态一致性：两套 DDL 的信封列非 `JSON`、升级脚本含 §1.6 的 `MODIFY ... MEDIUMTEXT` 与 `ALGORITHM=COPY, LOCK=SHARED` |
| 测试 | `test/integration/testutil/mysql.go` | `startServerWithMySQL(adminDSN, extraTOML, monitorPortMode)` + `StartServerWithMonitorAuto` |
| 测试 | `test/integration/tests/secret_at_rest/secret_at_rest_test.go` | `openRawDB` 双驱动、`requireSQLiteBackend`、SAR-1-003 在 MySQL 跳过、批量种子改为合法 key JSON、新增 SAR-4-001 |
| 文档 | SAR `design.md`、`sys-design/数据库设计文档.md`、`sys-design/details/密钥静态加密.md`、`2026-10-05-.../design-changes.md` §4/§5/§9 修订注记、`docs/zh_cn/upgrade.md`、`CHANGELOG.md` | 口径同步 |

**验证结果**

- 构建与静态检查：`go build ./...`、`go vet ./...` 通过；重建 `ai-gateway-api.exe`（集成测试用二进制）。
- 单测：`go test ./...` 全量通过（exit 0）。
- 覆盖率门禁：`go test -coverprofile ./model/...` → **85.0%** ≥ 70%。
- 集成（SQLite）：`tests/secret_at_rest` 10 例全 PASS（含新增 SAR-4-001）；`tests/provider/...`、
  `tests/innerapi/...` 全 PASS（`unmarshalKeys` 收紧后无回归）。
- 集成（MySQL 8.4.9，`AIAPI_MYSQL_DSN`）：`tests/secret_at_rest` 9 PASS + 1 SKIP（SAR-1-003 依赖共享
  SQLite 文件，按设计跳过）；`tests/provider/...`、`tests/innerapi/...` 全 PASS。
- **红绿验证（缺陷可判据化）**：把 `db_ddl.sql` 的该列临时改回 `JSON` 后，MySQL 后端 SAR-4-001 立即失败
  （进程被启动自检拦下：`server not ready`）；用一次性探针直连该库验证自检报错原文为
  `security: column(s) providers.api_keys declared as JSON but hold enc$v1$ envelopes; ... Run the schema change in database/db_ddl_upgrade_to_0.0.11.sql §1.6 ...`；
  执行 §1.6 的 `ALTER` 后同一探针输出 `GUARD-OK`，且信封 `enc$v1$AAAA` 可正常写入。改回 `MEDIUMTEXT` 后
  恢复全绿，证明用例确实能拦住 issue #264。

**结论**：加密启用时 Provider 建/改与 `scope=providers` 的收敛在 MySQL 严格模式下恢复可用，落库为信封、读回为
明文；「换二进制未改表结构」的部署在启动期即被拦下并给出可直接执行的修复语句；列降为文本后的坏值不再静默
降级为「无 key」。OpenAPI 契约与数据面（BFE/conf-agent）零影响。

**遗留移交项（不计入本仓改动）**：`ai-gateway` 仓的 `api_db_ddl.sql:465` 与
`kubernetes/deploy/mysql-deploy.yaml:596` 仍是 `api_keys JSON`，需由该仓按自身发布流程改为 `MEDIUMTEXT`
（本卡只提供变更内容与校验点：`grep -rn 'api_keys.*JSON'` 应为 0）。本仓的 `db_ddl.sql`、
`db_ddl_upgrade_to_0.0.11.sql` 已闭环，集成测试与启动自检均只依赖本仓 DDL。

**沉淀评估（六步法 Step 6）**：本卡是「设计缺口 + 列类型前置条件」，机制本身（DAO 透明加解密、信封格式、
收敛任务）未变，无需新增 `sys-design/details/` 文档；相关约束已内联进 `details/密钥静态加密.md` 与
`sys-design/数据库设计文档.md`，并在冻结设计文档中以修订注记形式留痕。