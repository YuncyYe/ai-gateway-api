# Issue #258：`PUT /open-api/v1/ai-cache-rules` 在"已有规则 + 新增规则"全量提交时稳定 500（`insert data not match`）修复方案

> 2026-10-20 修订（第 2 版）：主方案由"保留只读时间戳、写路径忽略"改为**契约收敛——响应不再返回 `created_at`/`updated_at`**（owner 提议，与 `/ai-context-rules`、`/global-route-rules` 既有口径对齐）。原方案降级为备选（§5.5）。

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/258](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/258)

> `PUT /open-api/v1/ai-cache-rules` 的语义是全量替换，因此"在已有规则基础上新增一条规则"时必须把旧规则和新规则一起提交。若客户端按最自然的方式编辑——先 `GET` 读出当前规则集、追加新规则、再整体 `PUT` 回去——接口稳定返回 `500 {"ErrNum":500,"ErrMsg":"Database Exception: insert data not match"}`，规则集保持不变。
>
> 触发条件是提交的多条规则中 `updated_at` 的存在性不一致：`GET` 返回的旧规则带 `created_at`/`updated_at`，新建规则不带。服务端把 `created_at` 统一补齐，但 `updated_at` 保持原样，随后按"每行实际非空字段"拼批量 INSERT，两行列集合不同，gendry 拒绝构造 SQL 并返回 `insert data not match`。

产品条款依据（Oracle，本方案将同步修订，见 §7）：

- `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md` §1（第 43 行）："**响应只读字段**（仅 GET/PUT 响应携带，提交时忽略）：`rules[].created_at`、`rules[].updated_at`"；
- §2.1（第 114 行）："单事务内整体替换规则集合（先删除全部旧规则，再按数组顺序写入）；任一校验失败或事务失败则整体回滚，集合保持原状"；
- 词表侧同源声明：`model/shared/types.go:261` "CreatedAt/UpdatedAt are read-only response fields (RFC3339)"。

影响（P1）：控制面 `GET → 追加 → 整体 PUT` 这条最自然的编辑路径稳定失败，精确/语义缓存的规则准备与回归（`SC1401-TC017`/`TC018`/`TC028`）被阻断；调用方只能依赖"剥掉所有时间戳"或"把新规则放在第一位"这类未文档化的变通，且后者会静默丢弃旧行的 `updated_at`；错误以 `Database Exception` 形式暴露，误导排障方向。无数据损坏（事务回滚，规则集保持原状）。

## 2. 根因（对当前检出逐环直读核实，2026-10-20）

### 2.1 调用链逐环

1. **接口层** `endpoints/openapi_v1/ai_cache/endpoints.go:64-93`：`AICacheRulesUpdateAction` 经 `xreq.BindJSON` 绑定 `shared.AICacheRulesParam`（含 `created_at`/`updated_at`），`validate.AICacheRules`（`lib/validate/validate.go:709-...`）只校验业务字段，**不触碰时间戳**，随后调用 `SetAICacheRules`。
2. **模型层** `model/ai_cache/ai_cache_manager.go:125-132`：逐条 `aiCacheRuleParamFromShared(rule)` 转成存储层 `AICacheRuleParam`，在 `m.txn.AtomExecute` 事务内调用 `storager.ReplaceAll`。
3. **根因所在** `model/ai_cache/ai_cache.go:102-136`（`aiCacheRuleParamFromShared`）：第 115-116 行把 `param.CreatedAt`/`param.UpdatedAt` **原样透传**；函数其余逻辑只补齐 `cache_key_strategy`/`cache_ttl`/`max_body_bytes`/`max_value_bytes`/`enable_semantic_cache` 五个业务默认值。于是"提交里带时间戳的行"与"不带的行"在存储层产生**不同的字段存在性**。
4. **存储适配层** `storage/rdb/ai_cache/ai_cache.go:77-94`（`aiCacheRuleParamToData`）：第 91-92 行继续透传 `CreatedAt`/`UpdatedAt` 为指针；`ReplaceAll`（第 62-75 行）把整批交给 DAO。
5. **DAO 层** `storage/rdb/internal/dao/table_ai_cache_rules.go:84-101`（`TAICacheRuleCreate`）：多行分支为每行**只补 `CreatedAt`**（第 94-96 行 `if one.CreatedAt == nil { one.CreatedAt = internal.PTimeNow() }`），**不补 `UpdatedAt`**；随后 `internal.Create`（`storage/rdb/internal/dao/internal/curd.go:72-73`）→ `Struct2AssignList`（`.../internal/builder.go:31-37`）。
6. **拼 SQL** `storage/rdb/internal/dao/internal/builder.go:43-96`（`struct2map`）：第 65-70 行对 nil 指针 `continue`（nil-skip 约定，见 `design-docs/sys-design/details/部分更新语义与DAO-nil-skip约定.md` §1），因此 `updated_at` 为 nil 的行**少一列**。
7. **gendry** `builder/dao.go:377-397`（`buildInsert`）：第 384 行 `fields = resolveFields(setMap[0])` —— 列集合取自**第一行**；第 389-392 行遍历后续行，`mapItem[field]` 缺失即 `return errInsertDataNotMatch`（`dao.go:11`，错误文本 `insert data not match`）；后续行**多出**的列被直接忽略。错误经 `xerror.WrapDaoError`（`lib/xerror/wrap.go:69`）→ `etDao` → `Resolve` 置 `Type="Database Exception"`（`lib/xerror/resolve.go:71-72`），输出 HTTP 500。

### 2.2 顺序依赖由第 7 环直接决定

列集合 = 第一行的字段集合，缺列报错、多列忽略，因此：

| 提交顺序 | 列集合来源 | 结果 |
|---|---|---|
| 旧规则（带 `updated_at`）在前 + 新规则（不带）在后 | 取自带时间戳行（多一列） | 后续行缺 `updated_at` → **500 `insert data not match`** |
| 新规则（不带）在前 + 旧规则（带）在后 | 取自不带时间戳行 | 200，但旧行的 `updated_at` 被**静默丢弃**，回填 DB 默认值 |
| 全部带 / 全部不带时间戳 | 一致 | 200 |

### 2.3 复现证据（本次诊断，构造器层确定性复现）

用仓库自身的 `Struct2AssignList` + `NewInsertBuilder` 拼同一批数据（4 列：name/cond/created_at/updated_at），临时用例（跑完即删，无残留文件）输出：

```
old-first:  err=insert data not match | old cols=4 new cols=3
new-first:  err=<nil>
both-ts:    err=<nil>
no-ts:      err=<nil>
```

与 issue 的 HTTP 对照矩阵（含 2026-10-19/20 两次复核）逐格一致，说明缺陷**完全落在 SQL 构造层**，与 cond 语法、鉴权、网络、数据库连通性无关。运行时证据（`500 Database Exception: insert data not match`、失败后 `GET` 回读规则数不变）由 issue 提供。

### 2.4 为什么"整组替换资源不返回时间戳"就能从根上消掉这类缺陷

同一仓库内已有三处证据，说明"不暴露逐行时间戳"是本仓对整组替换资源的既定做法，且它天然免疫本缺陷：

| 资源 | 是否返回 `created_at`/`updated_at` | 写路径 | 是否出现 `insert data not match` |
|------|-----------------------------------|--------|----------------------------------|
| `/ai-context-rules` | **否**（`shared.AIContextRuleParam` 无该字段，`model/shared/types.go:294-307`） | `contextRuleRowFromShared`（`model/ai_context/ai_context.go:126-146`）只带业务字段；DAO 仍批量 INSERT（`table_ai_context_rules.go:109-119`） | 不会（列集合恒定） |
| `/ai-context-settings` | **否**（文档明确声明） | 单行覆盖 | 不会 |
| `/global-route-rules` | **否**（`rules` 元素为 `00-common.md` 的公共 `RouteRule` 类型） | 整组重写 | 不会 |
| `/ai-cache-rules`（本卡） | **是** | 批量 INSERT | **会** |
| `/traffic-mirror-rules` | 是 | 已改**逐条** INSERT（`table_traffic_mirror_rules.go:118-144`，注释记录了同一 gendry 限制） | 不会 |
| `/ai-cache-semantic-settings` | 是 | 单行覆盖 | 不会（但存在"提交时间戳被采信"的契约偏离，见 §5.6） |

更直接的证据：`ai-context-rules.md:93` 与 `ai-context-settings.md:83` 已白纸黑字写"**不返回** `created_at`/`updated_at`：本资源为整组替换语义（每次 PUT 全删重建），逐规则时间戳无信息价值（**与 `ai-cache-rules` 一致**）"——该括号在今天**是错的**，本方案正是让这句话变成事实。

另：整组替换语义下，逐行时间戳本身没有信息价值——每次 PUT 都是 delete-all + insert-all，`id` 自增重建，`created_at`/`updated_at` 恒等于"最近一次 PUT 的时刻"，无法表达"这条规则存在了多久"。

### 2.5 既有测试为何没拦住

- `test/integration/tests/ai_cache/update/update_test.go` 全部用例（AC-1-001~013）都是**新构造** `map[string]interface{}` 提交，从未把 `GET` 的响应体**回灌**给 `PUT`——而"回灌"正是缺陷的唯一触发形态；
- AC-1-003（全量替换）虽然"删1/改1/增1"，但三条都不带时间戳，列集合天然一致；
- `model/ai_cache/ai_cache_manager_test.go:451-468`（`TestAICacheRuleParam_Conversions` 的 "round trip preserves values"）反而**断言**时间戳被原样保留，等于把缺陷行为写成了期望值；
- 契约锁定测试（`test/integration/tests/schema/openapi/ai_cache.go:20-38`、`update_test.go:41-46` 的 9 键）钉住的是"字段存在"，与写入路径的列集合一致性无关。

## 3. 影响面

| 项目 | 内容 |
|------|------|
| 接口 | `PUT /open-api/v1/ai-cache-rules`（全量替换）与 `GET /open-api/v1/ai-cache-rules` |
| 触发条件 | 请求体多行 `created_at`/`updated_at` 存在性不一致，且带时间戳的行排在前面（修复后该形态不再可能导致失败） |
| 修复前错误形态 | HTTP 500 `Database Exception: insert data not match`；事务回滚、集合保持原状 |
| 修复后 | HTTP 200，按提交顺序重建集合；**响应规则元素为 7 个业务字段**（时间戳字段不再出现在请求/响应契约中） |
| 响应形状变化 | 9 键 → 7 键（**破坏性**，见 §5.4） |
| 调用方 | 无仓内消费者：`ai-gateway-web` 目前**没有** AI 缓存模块（`ai-gateway-web/src/modules/` 下无对应目录，`src/` 内无 `ai-cache` 引用）；时间戳不参与 `ai_cache.data` 导出（`model/ai_cache/ai_cache_manager.go:38-45`）。仓外消费者：E2E 用例（`SC1401-TC017/018/028`）与可能的自动化脚本 |
| 数据/DDL | 无 DDL 变更、无数据迁移（`ai_cache_rules.created_at`/`updated_at` 保留为服务端维护列，`db_ddl.sql:517-518`、`db_ddl_sqlite.sql:543-544`） |
| 数据面 | 无 BFE / conf-agent 联动（时间戳不参与导出） |

## 4. 目标

1. `GET → 末尾追加不带时间戳的新规则 → 整体 PUT` 返回 **200**，集合按提交顺序重建，新规则写入；
2. **旧客户端兼容**：请求体仍携带 `created_at`/`updated_at`（issue 复现 curl 的原文形态）同样返回 200，且两种排列结果一致——字段从契约中移除后，反序列化按 `encoding/json` 默认行为忽略未知键（`lib/xreq/param.go:69-82` 未启用 `DisallowUnknownFields`），不会退化为 422；
3. 响应契约与 `/ai-context-rules`、`/global-route-rules` 口径统一：整组替换资源不返回逐行时间戳；
4. 合法业务字段（name/cond/strategy/ttl/bytes/semantic）的默认值回填、顺序语义、校验（422）与事务回滚语义零变化；
5. 补"回读结果原样 PUT"与"旧客户端携带时间戳"两条回归，使同类缺陷不能再无声复发；
6. 契约锁定测试（响应键集合）在同一提交内从 9 键改为 7 键，文档与实现不出现中间态漂移。

## 5. 最终方案

### 5.1 三种候选与取舍

| 方案 | 做法 | 结论 |
|------|------|------|
| **A（采纳，主方案）** | **契约收敛**：`shared.AICacheRuleParam` 删除 `CreatedAt`/`UpdatedAt`，写入/响应均不再携带；同步改文档 | 与 `/ai-context-rules`、`/ai-context-settings`、`/global-route-rules` 口径一致；把缺陷面**从契约中移除**（没有字段就没有列集合分歧）；对旧客户端天然兼容（未知键被忽略）；时间戳在整组替换语义下本无信息价值 |
| B（备选，非破坏性） | 保留响应字段，`aiCacheRuleParamFromShared` 把 `CreatedAt`/`UpdatedAt` 视为只读字段丢弃（置 nil） | 不动契约即可闭环缺陷，但保留了"每次 PUT 都会被刷新、且不承载信息"的字段，与三个同族资源继续不一致；需维持"写路径必须丢弃只读字段"这一隐性约定 |
| C（否决） | `TAICacheRuleCreate` 为多行统一补 `UpdatedAt` | ①把客户端提交的 `updated_at` 落库，允许伪造更新时间、掩盖真实改动时刻；②仍依赖"补列"而非"消列"，语义方向错误 |

**推荐 A**，理由是它与仓库既有设计收敛方向一致（`ai-context-*` 文档已自称"与 ai-cache-rules 一致"）、消灭缺陷面而非绕过它、且仓内无消费者、迁移成本可控。A 属**破坏性响应变更**，需 owner/产品确认后按 §5.4 的流程落地；若产品不接受破坏性变更，退回 B 同样可以闭环 issue 的全部验收项。

### 5.2 方案 A 改动清单（代码 + 契约 + 测试）

**模型层 / 词表**

1. `model/shared/types.go:257-272`：删除 `CreatedAt`/`UpdatedAt` 两个字段，注释改为：
   ```go
   // AICacheRuleParam defines an AI cache rule. ...（业务字段说明不变）
   // 本资源为整组替换语义（每次 PUT 全删重建，id 自增重排），逐规则时间戳无信息
   // 价值，故不作为响应字段返回，也不从请求体接受（与 AIContextRuleParam 一致）。
   ```
2. `model/ai_cache/ai_cache.go:46-57`：存储层 `AICacheRuleParam` 删除 `CreatedAt`/`UpdatedAt`（写路径不再需要透传；读路径不再向 API 暴露）；
3. `model/ai_cache/ai_cache.go:102-136`：`aiCacheRuleParamFromShared` 删除第 115-116 行；
4. `model/ai_cache/ai_cache.go:139-155`：`aiCacheRuleParamToShared` 删除第 152-153 行；
5. `model/ai_cache/operation_log.go:175-181`：审计快照 `aiCacheRuleParamToMap` 删除两个时间戳键（`before`/`after` 快照随之与 `/ai-context-rules` 同形）。

**存储适配层**

6. `storage/rdb/ai_cache/ai_cache.go:77-94`（`aiCacheRuleParamToData`）与 `:96-113`（`aiCacheRuleDataToParam`）：删除两个时间戳字段的赋值；
7. `storage/rdb/internal/dao/table_ai_cache_rules.go`：**不改**——`TAICacheRuleParam` 的 `CreatedAt`/`UpdatedAt` 仍供查询条件/排序使用；`TAICacheRuleCreate` 继续为多行补 `CreatedAt`（第 94-96 行），`updated_at` 由 DB 默认值（MySQL `DEFAULT CURRENT_TIMESTAMP`、SQLite 同）填充，这正是"时间戳由服务端维护"的既有形状。

**契约文档**

8. `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md`：删除 §1 "响应只读字段"整块（第 43-48 行）与两处示例中的时间戳行（第 137-138、147-148 行）；§2.1 返回数据说明（第 120 行）与 §2.2（第 177 行）改为"字段同第 1 节数据模型"，并补一句：
   > **不返回** `created_at`/`updated_at`：本资源为整组替换语义（每次 PUT 全删重建，`id` 自增重排），逐规则时间戳无信息价值（与 `/ai-context-rules` 一致）；操作审计时间见 `GET /operation-logs`。
9. `design-docs/sys-design/details/AI缓存规则与导出.md:119`：同一句替换原"响应只读字段…"段。

**测试（详见 §6）**

10. 契约锁定：`test/integration/tests/schema/openapi/ai_cache.go:20-38`（9 键 → 7 键）、`test/integration/tests/ai_cache/update/update_test.go:41-46`（`ruleContractKeys`）与 `:131-142`（`assertRuleMatches` 去掉两条 `NotEmpty`）；
11. `model/ai_cache/ai_cache_manager_test.go`：`TestAICacheRuleParam_Conversions`（:435-469）"round trip preserves values" 改写/删除（字段已不存在，编译期即锁死），另新增 JSON 契约单测（§6.1）；
12. 集成新增 AC-1-014~016（§6.3）。

### 5.3 为什么 A 能修掉本缺陷（机制说明）

- 字段从结构体移除后，`GET` 响应不含时间戳 → "读取—追加—写回"的客户端**不会**再带时间戳；
- 旧客户端/脚本仍带时间戳时，`json.Decoder`（`lib/xreq/param.go:74-81`）默认忽略结构体中不存在的键 → 两行数据的字段存在性**恒定一致** → gendry 列集合一致 → 200；
- 存储层不再接收 `UpdatedAt`，`TAICacheRuleCreate` 补 `CreatedAt`、DB 补 `updated_at`，写入路径不再存在"部分行有该列、部分行没有"的形态；
- 因此 issue 的整张顺序对照矩阵（无论新旧规则谁在前、带不带时间戳）全部收敛为 200，且不再有"`updated_at` 被静默丢弃"的隐式行为。

### 5.4 决策门槛与风险（A 的代价）

1. **破坏性契约变更**：响应规则元素由 9 键变为 7 键。需 owner/产品确认，并在 `CHANGELOG.md` 的 `[Unreleased] > Changed` 以 **Breaking** 标注（参照 `0.0.11-preview.1` 中 `/epp-pool`、`/epp-assignments` 的既有写法）。当前版本号为 `0.0.11-preview.x`，不在正式语义化大版本保护期内，仓库已有多次 **Breaking** 先例；
2. **仓外同步**：`ai-gateway-e2e-test` 的 `SC1401-TC017/018/028` 若断言 `created_at`/`updated_at`，需由 TC 侧同步调整（跨仓边界，本仓不改其他仓库文件）；`ai-gateway-web/design-docs/` 若镜像了 `ai-cache-rules.md`，需前端 owner 用同措辞同步；
3. **审计快照**：`operation-log` 的 `before`/`after` 不再包含逐规则时间戳。影响评估：`diff_keys` 只做集合快照顶层键级比较（`model/ioperlog/change_summary.go:57-76`），现有断言只期望 `["rules"]`（`update_test.go:545-557`），因此**测试与既有行为均不受影响**；审计时间仍由日志条目自身的 `created_at` 提供；
4. **可观测性**：失去"某条规则行何时被创建"的排查线索。该线索在整组替换语义下恒等于"最近一次 PUT 时刻"，信息价值低于 `GET /operation-logs` 的审计记录；
5. **回滚路径**：若上线后确认需要恢复字段，按同一路径加回结构体与文档即可，无数据迁移（DB 列一直在写）；
6. **同族 `ai-cache-semantic-settings` 的处理**：可选（§5.6），建议拆卡，避免本卡同时动两个资源扩大评审面。

### 5.5 备选方案 B（若产品拒绝破坏性变更）

保留响应字段，在模型层把只读字段丢弃：

```go
// model/ai_cache/ai_cache.go（aiCacheRuleParamFromShared）
rule := &AICacheRuleParam{
    Name: param.Name, Cond: param.Cond,
    CacheKeyStrategy: param.CacheKeyStrategy, CacheTTL: param.CacheTTL,
    MaxBodyBytes: param.MaxBodyBytes, MaxValueBytes: param.MaxValueBytes,
    EnableSemanticCache: param.EnableSemanticCache,
    // CreatedAt/UpdatedAt are read-only response fields: a full-replace PUT
    // rebuilds every row, so the submitted values are ignored. Leaving them nil
    // also keeps the column set identical across rows — gendry derives the
    // INSERT column set from the first row and rejects later rows missing a
    // column with "insert data not match" (issue #258).
}
```

配套：`model/shared/types.go:261` 注释补 "ignored on write"；`TestAICacheRuleParam_Conversions` 的 round-trip 断言改为"写入丢弃、读出保留"；新增"两行时间戳混排 → 传给 ReplaceAll 的两行均为 nil"的单测。此时响应仍为 9 键，`schema`/契约锁测试无需改动。

B 的代价：字段继续存在但每次 PUT 刷新，调用方容易误以为可跨 PUT 保留（本次缺陷的认知来源之一）；同时必须长期维护"只读字段在写路径必须丢弃"这条隐性约定。

### 5.6 同族一致性（可拆卡，非 #258 必需）

`/ai-cache-semantic-settings` 同样返回 `created_at`/`updated_at`（`ai-cache-semantic-settings.md:23-28`），且 `semanticSettingsRowFromShared`（`model/ai_cache/ai_cache.go:170-194`）把提交值透传落库——单行覆盖不会 500，但允许提交伪造时间戳。按 §2.4 的同一理由（整组替换/全行覆盖 → 时间戳无信息价值；`ai-context-settings.md:83` 已不返回），建议按 A 的口径一并移除。若本期严格限定范围，先做规则集合，此卡另开。

### 5.7 独立加固（可选，与 A/B 均无关）

`TAICacheRuleReplaceAll`（`table_ai_cache_rules.go:115-125`）可改为逐条 `internal.Create`（对齐 `TTrafficMirrorRuleReplaceAll` 的既有先例与注释），把"同批列集合必须一致"从正确性前提降级为性能优化，防未来新增可空列再犯。A 落地后本缺陷已不可能由时间戳触发，故此项纯属护栏，成本是 N 次 INSERT 往返（规则集为人工维护的小集合，单事务内开销可忽略）。**建议不做**，把该约束写进细节文档约定（§7）即可；若 owner 倾向加固，则按 traffic-mirror 的写法落地并在代码注释里保留 gendry 限制说明。

### 5.8 行为变化表（以 A 为准）

| 场景 | 修复前 | 修复后 |
|------|--------|--------|
| GET → 追加不带时间戳的新规则（新规则在末尾）→ PUT | **500** `Database Exception: insert data not match` | **200**，新规则写入，顺序=提交顺序 |
| GET → 追加不带时间戳的新规则（新规则在首位）→ PUT | 200，旧行 `updated_at` 被静默丢弃 | 200，与上一行**完全一致** |
| 旧客户端整批带 `created_at`/`updated_at`（issue 复现 curl 原文） | 顺序相关：或 500，或写入并污染时间戳 | **200**，时间戳键被忽略 |
| GET 响应规则元素键集合 | 9 键（含 `created_at`/`updated_at`） | **7 键**（业务字段） |
| PUT 响应规则元素键集合 | 9 键 | **7 键** |
| 业务字段校验失败（非法 cond、重名等） | 422 + 事务未开启 + 失败审计 | 422（不变） |
| `rules=[]` 或 `rules` 缺省（清空） | 200 | 200（不变） |
| 默认值回填（strategy/ttl/bytes/semantic） | 有 | 不变 |
| 审计 `diff_keys` / `change_summary` 结构 | `before`/`after`/`diff_keys`，`diff_keys=["rules"]` | 不变（快照内的逐规则时间戳消失） |
| Inner 导出 `ai_cache.data` | 不含时间戳 | 不变 |

### 5.9 不改的东西

- **校验层与错误码**：`validate.AICacheRules` 不动——字段移除后请求体中的时间戳是被忽略的未知键，**不**新增 422；
- **接口层路由与 handler**：`endpoints/openapi_v1/ai_cache/endpoints.go` 零改动（结构体字段变化自动作用于绑定与渲染）；
- **DAO 结构体与建表**：`TAICacheRuleParam.CreatedAt/UpdatedAt` 保留（查询条件/排序仍用），DDL 与索引不变；
- **导出与数据面**：`ExportAICacheRule`、`AICacheRuleGenerator`、Inner 导出/版本控制全部不动；
- **规则 `id` 语义**：全量替换后自增新 id，与顺序即优先级的既有设计一致；
- **`/traffic-mirror-rules`、`/intent-config` 等其他资源**：不动（各自独立卡）。

## 6. 测试

### 6.1 单元测试（`model/ai_cache/`）

- **JSON 契约锁（新增，A 的核心回归）**：`TestAICacheRuleJSONContract`——把一条规则（经 `aiCacheRuleParamToShared` 后的形态）`json.Marshal`，断言键集合精确为 7 键 `{name, cond, cache_key_strategy, cache_ttl, max_body_bytes, max_value_bytes, enable_semantic_cache}`，且 **不含** `created_at`/`updated_at`/`id`/`enabled`（与 AC-1-009 的家族11/12 口径一致）；
- `TestAICacheRuleParam_Conversions`（`ai_cache_manager_test.go:435-469`）：删除/改写 "round trip preserves values"（字段已删，编译期即锁死），保留"nil 输入"与"默认值回填"两个子用例；
- `TestAICacheManager_SetAICacheRules`：断言传给 `replaceAllFn` 的规则不带时间戳（字段已不存在，改为断言 `replaceRules` 的业务字段与顺序，并补"旧客户端 body 含时间戳"的 manager 级用例不必做——反序列化层已忽略）；
- `TestAICacheManager_SetAICacheRules` 审计子用例：`before`/`after` 快照中不再出现 `created_at`/`updated_at`（若既有断言涉及，一并调整）。

### 6.2 契约/集成（真实二进制）

`test/integration/tests/schema/openapi/ai_cache.go`：`Required` 由 9 键改为 7 键，删除两行 `TypeString` 定义，注释同步（"合同 7 字段"）。

`test/integration/tests/ai_cache/update/update_test.go`（编号接续 AC-1-013）：

- `ruleContractKeys`（:41-46）改为 7 键；`assertRuleMatches`（:131-142）删除 `created_at`/`updated_at` 的非空断言；
- **AC-1-014 回读原样 PUT**（幂等编辑）：PUT 集合 A（2 条）→ GET → 把 GET 的 `rules` 原样作为 PUT body → 200、`rules` 仍为 2 条、业务字段逐字段一致、键集合 7 键；
- **AC-1-015 旧客户端携带时间戳**（issue 复现原文，核心验收）：用 issue curl 的 body 形态（旧规则带 `created_at`/`updated_at`、新规则不带）PUT → **200**（修复前 500）、2 条规则写入、顺序=提交顺序；顺序反转再跑一次 → 同样 200；
- **AC-1-016 时间戳被忽略**：提交显式伪造值 `"2000-01-01T00:00:00Z"` → 200，响应中**不出现** `created_at`/`updated_at` 键（而非"值不等于伪值"），业务字段等于提交值。

`test/integration/tests/ai_cache/get/get_test.go`：补"GET 无时间戳键"的键集合断言（若 schema 套件已覆盖，可只加一条正向断言）。

文档同步：`test/integration/tests/ai_cache/design.md`——§3 统计 `PUT 12 / 合计 21` → `PUT 15 / 合计 24`（顺带补登 AC-1-013：该用例已在 `update_test.go:635` 落地，但统计表与目录结构仍写"AC-1-001 ~ AC-1-011"，属既存文档漂移）；§5 目录结构注释改为 `AC-1-001 ~ AC-1-016`；§6 用例表与 §6.4 详细设计补 AC-1-014~016。

### 6.3 回归验证

1. `go build ./...`、`go vet ./...`、`go test ./... -count=1` 全绿；
2. `make test-model-cover-gate`（model ≥ 70%，本次改 `model/ai_cache` 与 `model/shared`，须确认无覆盖率回落）；
3. 集成：`go test -count=1 ./tests/ai_cache/... ./tests/schema/...` 全 PASS（schema 是契约锁，必跑）；再跑全量 `./tests/...`（若出现既有并行抖动，按 `-p 1` 串行复核并单独标注，参照 `2026-10-17-issue-244-description-length-validation` §9.5）;
4. 靶向复现：按 issue `## 复现步骤` 第 2 步的 curl 原文重放 → 期望 **200** 且 `GET` 回读 2 条、响应无时间戳键；再按第 4 步的执行矩阵逐行重放（含顺序反转、全带、全不带、单条），全部 200 且行为与顺序无关；
5. `SC1401-TC017/TC018/TC028` 的规则准备步骤应具备转 PASS 条件；其中若有断言 `created_at`/`updated_at` 的用例，需 TC 侧同步（§5.4 第 2 条）。

## 7. 文档与契约

**契约变更（A 的实质）**：

- `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md`：删除 §1 "响应只读字段"块（第 43-48 行）与示例中的时间戳；§2.1 返回数据（第 120 行）、§2.2（第 177 行）改为"字段同第 1 节数据模型"，并补"不返回 `created_at`/`updated_at`（整组替换语义，与 `/ai-context-rules` 一致）"；§1 字段说明表的 `rules` 行"必填；`null` 按 `[]` 处理"等既有约束不变；
- `design-docs/sys-design/details/AI缓存规则与导出.md:119`：同句替换；
- `design-docs/sys-design/接口层设计文档.md` §4.4 段尾补一条约定（可复用知识）：
  > 整组替换（delete-all + insert-all）类资源的**逐行时间戳不作为响应字段**：`created_at`/`updated_at` 恒等于最近一次 PUT 时刻，无信息价值；不暴露字段同时也消除"批量 INSERT 的列集合不一致"这一 gendry 限制（见 issue #258、`/ai-context-rules`）。
- `design-docs/sys-design/details/部分更新语义与DAO-nil-skip约定.md` 新增 §8「集合级全量替换与批量 INSERT 的列集合一致性」：说明 nil-skip 在 INSERT 场景的副作用（列缺失 → `insert data not match`，列集合取自首行、多余列被忽略）、三种既有应对（**不暴露该字段**（ai-context-rules、ai-cache-rules 本次）、**逐条插入**（traffic-mirror-rules）、**恒写该列**（ioperlog 批量日志）），以及"只读响应字段不得进入写路径"的约定；关联 issue #258；
- `model/shared/types.go` 的 `AICacheRuleParam` 注释与 `AIContextRuleParam`（`:296-301`）对齐，后者的"（aligned with ai-cache-rules）"一句在此次改动后**才成立**，无需再改；
- `CHANGELOG.md`：`[Unreleased] > Changed` 增 **Breaking** 一条——`GET/PUT /open-api/v1/ai-cache-rules` 的规则元素不再返回 `created_at`/`updated_at`（整组替换语义下无信息价值，与 `/ai-context-rules` 一致）；同时 `[Unreleased] > Fixed` 增一条——修复混排时间戳导致的 `500 Database Exception: insert data not match`（issue #258）；
- 变更记录即本文档。

## 8. 遗留 / 待确认

1. ~~**破坏性变更的批准**（A 的前置条件）~~ → **已关闭（2026-10-20）**：owner 指示按方案 A 执行，响应规则元素 9 → 7 键已在 `CHANGELOG.md` 的 `[Unreleased] > Changed` 标注 **Breaking**；
2. **仓外同步**：`ai-gateway-e2e-test`（TC 断言）与 `ai-gateway-web/design-docs`（文档镜像）——本仓不改其他仓库文件，需对应 owner 跟进；
3. **同族 `ai-cache-semantic-settings`**（§5.6）：建议单独拆卡，避免与规则集合同批扩大评审面；
4. **畸形时间戳的兼容面**：字段移除后该问题一并消失（未知键被忽略）；若日后采用 B，则非 RFC3339 的时间戳仍会在 `BindJSON` 阶段 422——这是 B 的一个额外劣势，记录备查；
5. **DAO 层无编译期护栏**：gendry 的"同批列集合一致"约束只在运行时体现（`builder/dao.go:384-392`）。本期用"不暴露字段 + 文档约定"兜住；如未来引入批量写入的公共封装，可在 `internal.Create` 增加批量形状自检，让失败变为可归因的 DAO 错误而非 `insert data not match`（独立改进项）。

## 9. 实施状态

**已完成（2026-10-20，方案 A：契约收敛）** —— 契约、代码、测试、文档四层在同一批改动内落地，并在真实二进制上完成正向验证与反向对照。owner 在评审第 2 版方案后指示按方案 A 执行，故 §8 第 1 条的"批准"环节已完成。

### 9.1 改动文件

契约 / 文档（6）：

- `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md`：§1 删除「响应只读字段」表，改为「**不返回** `rules[].created_at`/`rules[].updated_at`：整组替换语义…（与 `/ai-context-rules` 一致）；请求体若携带按未知字段忽略」；§2.1/§2.2 的返回数据说明与两处示例去掉时间戳；
- `design-docs/sys-design/details/AI缓存规则与导出.md`：§4 字段说明同步为「不返回…（issue #258）」；
- `design-docs/sys-design/接口层设计文档.md`：新增 §4.4.5「整组替换类资源的逐行时间戳」（同族资源对照表 + 3 条约定）；
- `design-docs/sys-design/details/部分更新语义与DAO-nil-skip约定.md`：新增 §8「整组替换与批量 INSERT 的列集合一致性」（gendry 首行定列集合、三种既有应对、约定）；
- `design-docs/sys-design/数据库设计文档.md`：§6.10 的 `created_at`/`updated_at` 行标注「服务端维护、不暴露到 Open API」，说明列表补一条；
- `CHANGELOG.md`：新增 `[Unreleased]` 段，`Changed` 记 **Breaking**（响应 9 → 7 键）、`Fixed` 记 #258 的 500 修复。

代码（4）：

- `model/shared/types.go`：`AICacheRuleParam` 删除 `CreatedAt`/`UpdatedAt`，注释写明"整组替换语义 + 不暴露字段同时消除批量 INSERT 列集合不一致"；
- `model/ai_cache/ai_cache.go`：存储层 `AICacheRuleParam` 同步删除两个时间戳字段；`aiCacheRuleParamFromShared`/`aiCacheRuleParamToShared` 不再传递；
- `model/ai_cache/operation_log.go`：审计快照 `aiCacheRuleParamToMap` 不再输出逐规则时间戳（`before`/`after` 随之与 `/ai-context-rules` 同形）；
- `storage/rdb/ai_cache/ai_cache.go`：`aiCacheRuleParamToData`/`aiCacheRuleDataToParam` 同步删除。

> DAO 与 DDL **未改**（§5.2 第 7 项预期）：`TAICacheRuleCreate` 仍为多行补 `created_at`，`updated_at` 走 DB 默认值（MySQL `DEFAULT CURRENT_TIMESTAMP` / SQLite 同）；`TAICacheRuleParam` 的两个字段保留供查询条件与排序使用。

测试（5）：

- `model/ai_cache/ai_cache_manager_test.go`：`storageRule` 去时间戳；`TestAICacheRuleParam_Conversions` 的 round-trip 改为"业务字段"；新增 `TestAICacheRuleJSONContract`（断言规则词表恰为 7 字段）；审计子用例补"快照无 `created_at`/`updated_at`"断言；
- `endpoints/openapi_v1/ai_cache/endpoints_test.go`：fake storager 去时间戳，GET/PUT 用例改为断言响应 JSON **不含** `created_at`/`updated_at`；
- `test/integration/tests/schema/openapi/ai_cache.go` 与 `openapi_schema_test.go`：契约锁 9 → 7 键。**实施时发现第二处硬编码键集合**（`openapi_schema_test.go:910-928` 的定向合同锁，方案 §6.2 只列了 schema 文件），已一并修改——这是本卡相对方案的一处必要偏离；
- `test/integration/tests/ai_cache/update/update_test.go`：`ruleContractKeys` 改 7 键、`assertRuleMatches` 去掉两条 `NotEmpty`，新增 AC-1-014（回读原样 PUT）、AC-1-015（旧客户端携带时间戳，两种排列）、AC-1-016（伪造时间戳被忽略）；
- `test/integration/tests/ai_cache/design.md`：字段表与"约束"、§3 统计（PUT 12 → 16、合计 21 → 25，顺带补登此前漏登记的 AC-1-013）、§5 目录、AC-1-001 预期表、AC-1-009 键集合（8 → 7）全部更新，新增 §6.4.13~§6.4.16 详细设计。

### 9.2 验证结果

| 项目 | 命令 | 结果 |
|------|------|------|
| 构建 | `go build ./...` / `go build -o ai-gateway-api.exe .` | 通过；二进制重建（41,307,136 字节） |
| 静态检查 | `go vet ./...`（主模块）、`go vet ./...`（`test/integration` 模块） | 均干净 |
| 单元测试 | `go test ./... -count=1` | 全绿（无 FAIL） |
| 覆盖率门禁 | `go test -coverprofile=… ./model/...` → `go tool cover -func` | `total: (statements) 85.0%` ≥ 70%，通过 |
| 集成（本卡） | `go test -count=1 ./tests/ai_cache/...` | `get` 1.3s / `update` 11.3s，全 PASS |
| 集成（契约锁） | `go test -run "TestOpenAPI_Schema/ai_cache" ./tests/schema/openapi/...`、`-run "TestInnerAPI_Schema/ai_cache" ./tests/schema/innerapi/...` | 全 PASS（含 ai_cache / ai_cache_semantic_settings / ai_cache_rule 导出） |
| 集成（全量，串行） | `go test -count=1 -p 1 ./tests/...` | **exit 0，全部 ok**（约 12.9 分钟；含 `schema/openapi` 1.6s、`schema/innerapi` 1.5s、`quota_period_reset` 136.4s，串行模式下无既有并行抖动） |
| 新增用例细跑 | `go test -v -run` 指定 AC-1-014 / AC-1-015 / AC-1-016 三个用例 | 全 PASS（AC-1-015 含 `legacy_rule_first`、`legacy_rule_last` 两个子用例） |

### 9.3 靶向复现与反向对照

- **反向对照（证明新用例确实钉住缺陷）**：用 `git worktree add /tmp/issue258-old HEAD` 取出修复前代码编出二进制，替换 `ai-gateway-api.exe` 后跑 AC-1-015：
  - `legacy_rule_first` → `expected ErrNum=200, got ErrNum=500, ErrMsg=Database Exception: insert data not match`（与 issue 原文逐字一致）；
  - `legacy_rule_last` → 200，但对"响应不含 `created_at`/`updated_at`"的断言失败（旧实现回显时间戳）。
  即 issue 的顺序矩阵在新用例下被完整覆盖。
- **正向验证**：换回修复后二进制，AC-1-014/015/016 全 PASS——`legacy_rule_first` 与 `legacy_rule_last` 两种排列均 200、两条规则都写入、顺序=提交顺序、响应无时间戳键。
- 临时 worktree 已 `git worktree remove --force` 清理，工作区只保留本卡的正常改动。

### 9.4 Step 6 沉淀评估

**不新增 `sys-design/details/` 文档。** 本次的可复用知识已就地沉淀为两条约定：`接口层设计文档.md` §4.4.5（整组替换资源不暴露逐行时间戳、旧客户端兼容性来自"未知键忽略"）与 `部分更新语义与DAO-nil-skip约定.md` §8（nil-skip 在批量 INSERT 下的副作用与三种应对）。机制层面无新流程、无架构变化，`sys-design/summary.md` 索引无需变更。

### 9.5 遗留

见 §8：仓外同步（`ai-gateway-e2e-test` 的 TC 断言、`ai-gateway-web/design-docs` 的文档镜像）、同族 `ai-cache-semantic-settings`（建议拆卡）、DAO 层批量形状自检（独立改进项）。