# Issue #261：`PUT /open-api/v1/ai-cache-rules` 接受集合内重复 `cond`（BFE 整文件拒载、配置长期不收敛）修复方案

> 2026-10-20：**方案已批准并实施完毕**（owner 批准修订设计决策，代码/契约/夹具/文档四层同批落地，验证结果见 §12）。本卡性质是「设计决策缺口」而非「实现偏离设计」——`2026-09-24-ai-cache-rule-export/design-changes.md:131` 与 `design-docs/sys-design/details/AI缓存规则与导出.md:147/228` 原文明写「控制面不做集合内 cond 去重（BFE 加载报错兜底）」，本卡即修订该决策为控制面 fail-fast（BFE 校验保留为最后防线）。

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/261](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/261)

> 管理面 `PUT /open-api/v1/ai-cache-rules` 接受同一 product 内两条 `cond` 完全相同的规则（仅 `name` 不同），返回 HTTP 200 并持久化；导出的 `ai_cache.data` 被 BFE `mod_ai_cache` 整体拒载（`can't have same cond`），conf-agent 因该版本永远无法应用而每 5s 重试同一文件、始终不切换 `mod_ai_cache` symlink，BFE 长期运行旧规则集。

事故现场（2026-10-20 13:26:24 起）核心事实：PUT 返回 200 → 导出含重复 `cond` → BFE `/reload/mod_ai_cache` 拒载 → conf-agent `consecutive: 530` 次失败、symlink 停在 `20261020165336`、持续 4 小时以上不收敛；期间依赖新规则的请求实际落在旧规则上，而管理面唯一的信号是 200 success。

Oracle（产品条款）：

- `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md#§2.1`：PUT 为整组替换，配置生效时延 = conf-agent 轮询周期 + BFE 热加载时间；
- `design-docs/api-define/InnerAPI接口定义/ai-cache-rule.md:111`、`sys-design/details/AI缓存规则与导出.md:147/228`：同一 product 内 `cond` 不可重复，重复即 BFE 拒载（最后防线）；
- `modifications/2026-09-24-ai-cache-rule-export/design-changes.md:131/144`："集合内 cond 去重：控制面 = 否 / 数据面 = 是"，并声明"控制面校验的目标是把错误拦在写入前（fail-fast），BFE 校验是最后防线"。

即：**同一条款里"fail-fast"与"cond 不去重"互相矛盾**，本卡修的是这对矛盾（同仓 `TrafficMirrorRules`、`AIContextRules` 已经按 fail-fast 口径实现，是可直接比对的先例）。

## 2. 现状与根因（2026-10-20 对当前检出逐环直读）

### 2.1 调用链逐环

1. **接口层** `endpoints/openapi_v1/ai_cache/endpoints.go:64-93`（`AICacheRulesUpdateAction`）：`xreq.BindJSON` → `validate.AICacheRules` → 失败则 `RecordSetAICacheRulesFailure` 并返回 422 → `SetAICacheRules`。
2. **缺陷所在** `lib/validate/validate.go:712-773`（`AICacheRules`）：只维护 `nameSet`（声明 `:722`，判断 `:734-737`），`cond` 仅做非空（`:739-741`）与 `ConditionExpression` 编译校验（`:742-744`），**没有集合内查重**。两条仅 `name` 不同的规则因此畅通。
3. **模型层** `model/ai_cache/ai_cache_manager.go:111-152`（`SetAICacheRules`）：单事务 `ReplaceAll`（delete-all + insert-all）落库，返回 200，控制面进入"已成功"状态。
4. **导出** `model/ai_cache/ai_cache_manager.go:230-...`（`AICacheRuleGenerator`）/`ConfigExport:203-221`：导出字段为 `cond`/`cacheKeyStrategy`/`cacheTTL`/`maxBodyBytes`/`maxValueBytes`/`enableSemanticCache`（**无 `name`**），两条相同 `cond` 原样进入 `Config[<product>]` 数组。
5. **数据面拒载** BFE `bfe_modules/mod_ai_cache/cache_rule_load.go:263-276`（`ProductRuleConfListFile.Check`）：以 `*rule.Cond` 字符串为 map key，第二次出现即 `can't have same cond[%s]`，整个配置文件拒载。
6. **不收敛** conf-agent `conf_reload/reloader.go:83`、`:160-169`：reload 失败 → `noteFailure` → 直接 return，不进入 `UpdateDefaultConfDir`，symlink 不切换；下一轮 probe 仍返回同一未应用版本 → 每 5s 重试同一文件。

### 2.2 控制面判据与数据面判据必须逐字节一致

BFE 的去重 key 是导出后的 `cond` **原始字符串**（无 trim、无规范化、无 AST 归一）。因此控制面唯一正确的判据也是"提交集合内 `cond` 字符串两两不相等"：

- 语义等价但字面不同的两个表达式（如 `a && b` 与 `b && a`）BFE **不会**判重，控制面也不应判重——否则出现"控制面 422、数据面本来能加载"的假阳性；
- 反过来，任何 trim/大小写/空白折叠都会让控制面放过 BFE 会拒载的集合，重新打开本缺陷。

### 2.3 为什么现有回归全绿却没拦住

**本仓测试夹具本身就在使用重复 `cond`**，把缺陷编码成了期望值（这些用例导出的 `ai_cache.data` 若真送进 BFE 会被整体拒载，只是因为集成测试不跑 BFE 才"绿"）：

| 文件 | 位置 | 重复 cond 的用法 |
|---|---|---|
| `test/integration/tests/ai_cache/update/update_test.go` | AC-1-002 `:256-263`、AC-1-003 `:290-311`、AC-1-009 `:495-499`、AC-1-011 `:613-615`、AC-1-013 `:654-661`、AC-1-014 `:679-681`、AC-1-015 `:725-735` | 多条规则共用 `validAICacheCond`，仅 `name`/`cache_ttl` 不同 |
| `test/integration/tests/ai_cache/get/get_test.go` | `:95-97`、`:145-150` | 同上 |
| `test/integration/tests/innerapi/ai_cache/ai_cache_test.go` | `:155-159`、`:246-255`、`:273-280`、`:289-291`、`:433-436` | 同上，且**断言导出数组里 cond 逐条相同**（用 cond+TTL 锁顺序） |
| `test/integration/tests/schema/openapi/openapi_schema_test.go` | `:894-899` | PUT 2 条同 cond 做形状锁定 |
| `test/integration/tests/schema/innerapi/innerapi_schema_test.go` | `:708-717` | PUT 2 条同 cond 做导出形状锁定 |
| `endpoints/openapi_v1/ai_cache/endpoints_test.go` | `:205-209` | PUT 2 条 `default_t()` 测语义开关 |

单测侧 `lib/validate/validate_test.go:606-708`（`TestAICacheRules`）也没有"不同 name、相同 cond → 拒绝"的用例，`dup := validRule()` 那行（`:652`）测的是**重名**。

补充一条容易被忽略的连带影响：**AC-1-011（混合集合假事务，`update_test.go:602-629`）的"2 条合法 + 1 条非法"里两条合法规则同 cond**。加校验后它在第 2 条就被"重复 cond"拦下，用例仍然 422 + 零变更（断言不红），但**失败原因被抢占**、装配不再表达"编译失败导致假事务"的原意，必须一并改成"两条合法规则 cond 互不相同"。（AC-1-007 值矩阵每条规则独立提交，不受影响。）

## 3. 影响面

| 项目 | 内容 |
|------|------|
| 接口 | `PUT /open-api/v1/ai-cache-rules`（唯一受影响写入口）；GET / InnerAPI 导出行为不变 |
| 触发条件 | 提交集合内两条及以上规则的 `cond` 字符串完全相同 |
| 修复前 | 200 + 持久化 → 导出 → BFE 整文件拒载 → conf-agent 无限重试、symlink 不推进（旧规则继续生效） |
| 修复后 | **422**（param error，含重复的 cond 文本），集合保持原状，记一条失败操作日志 |
| 数据面 | 零改动（BFE `Check` 原样保留为最后防线） |
| 数据/DDL | 无 DDL、无数据迁移 |
| 仓内消费者 | 无（`ai-gateway-web/src/modules/` 无 AI 缓存模块；导出不含 `name`，前端不受契约字段影响）；**但集成测试夹具需同步迁移（见 §6.3）** |
| 仓外消费者 | E2E `SC1401-TC017/018/028` 等规则准备脚本、E2E harness 的 baseline 快照/回滚（issue 推断项：它把非法集合当基线反复回写） |
| 存量数据 | 修复只拦**新写入**；历史库中已持久化的重复 `cond` 集合升级后仍会被导出、仍会被 BFE 拒载（处置见 §8） |

## 4. 目标与验收判据

1. 同一集合内 `cond` 重复 → **422**，集合零变更，错误信息可定位（`duplicate ai cache rule cond: <cond>`）；
2. 合法集合（`cond` 两两不同）的一切既有行为零变化：默认值回填、顺序即优先级、单事务整体替换、`{"rules":[]}` 清空、审计快照、Inner 导出；
3. 与同族资源口径统一：`AIContextRules`（`validate.go:840/852-855`）、`TrafficMirrorRules`（`validate.go:965/988-991`）已是同款校验，本卡把 `AICacheRules` 补齐——`design-changes.md` 里"照 `AICacheRules` 全量校验先例"的反向引用随之成立；
4. 现有夹具全部迁移为 `cond` 唯一后，导出内容对 BFE 可加载（测试侧不再固化非法集合）；
5. 文档不再出现"控制面不做集合内 cond 去重"的表述（除已标注被取代的历史记录）。

| issue 断言 | 判据 | 本方案落点 |
|---|---|---|
| A-01 重复 `cond` 是非法配置，数据面必须拒绝 | BFE 拒载不变 | §6.1（不动 BFE）+ §6.3 AC-1-017 |
| A-02 一次成功写入应在轮询周期 + 热加载时间内收敛 | 200 不再对应"必然不可加载"的集合 | §6.1（把非法集合挡在写入前） |
| A-03 fail-fast（该场景原明文例外） | 控制面 422 | §6.1 + §6.2（删掉例外表述） |

## 5. 方案候选与取舍

| 方案 | 做法 | 结论 |
|------|------|------|
| **A（采纳）** | `validate.AICacheRules` 增加 `condSet`，集合内 `cond` 重复 → 422；BFE `Check` 保留为最后防线 | 与 `AIContextRules`/`TrafficMirrorRules` 逐字对齐；把错误拦在写入前，200 重新等价于"可加载"；无 DDL、无数据面联动、无契约字段变化 |
| B | 保留现有行为，只在 GET/监控/审计中暴露"未收敛 + 不可加载原因" | **不可行（本期）**：控制面拿不到应用状态——conf-agent 是只拉不推的单向链路，`config_versions` 只记录"导出过什么"，不记录"BFE 是否加载成功"。要做得新增数据面 → 控制面的回报通道（跨仓、跨版本、跨故障域），成本远高于本卡收益 |
| C | 只改 conf-agent 可运维性（退避、日志去重、告警） | 有效但**不在本仓**（conf-agent 独立仓库），且只是把噪音降下来，不修"200 ≠ 生效"；记为跨仓建议（§9） |
| D | 导出侧校验：`AICacheRuleGenerator`/`ConfigExport` 发现重复 `cond` 就返回错误、拒绝生成新版本 | **否决**：BFE 拒载变成 InnerAPI 报错，conf-agent 仍在同一版本上空转 5s 重试，收敛性零改善；还把配置不可用的原因从"数据面校验"搬到"控制面导出"，可观测性没有净增益，且违反"导出层只做搬运"的职责边界 |
| E | DAO/DB 层加固（`cond` 唯一索引 / `ReplaceAll` 前查重并静默去重） | **否决**：`cond` 为 `TEXT`，MySQL 前缀索引既拦不住长表达式又污染表结构；"静默去重"会悄悄丢规则，比 422 更危险 |

## 6. 最终方案（方案 A）

### 6.1 代码改动（唯一代码改动点）

`lib/validate/validate.go` 的 `AICacheRules`（`:712-773`），照 `TrafficMirrorRules`（`:982-991`）的写法内联 6 行：

```go
	nameSet := map[string]struct{}{}
	condSet := map[string]struct{}{}          // 新增，与 nameSet 同处声明（:722 附近）
	...
		if err := ConditionExpression(*rule.Cond); err != nil {
			return xerror.WrapParamErrorWithMsg("ai cache rule %s cond: %v", *rule.Name, err)
		}
		if _, ok := condSet[*rule.Cond]; ok {  // 新增：编译通过后再判重
			return xerror.WrapParamErrorWithMsg("duplicate ai cache rule cond: %s", *rule.Cond)
		}
		condSet[*rule.Cond] = struct{}{}
```

要点：

- **判重位置在编译之后**：两条相同的非法表达式，返回的是"编译失败"（更根本的原因），而不是"重复"；
- **错误文案与 `TrafficMirrorRules` 同构**（`duplicate <资源> rule cond: %s`），带上完整 `cond`（与 `AIContextRules`/`TrafficMirrorRules` 一致；`cond` 为多 primitive 组合表达式，通常不超过数百字节，不做截断以保持三处一致）；
- **判据是原始字符串**（见 §2.2），不 trim、不归一化；
- 走既有 `xerror.WrapParamErrorWithMsg` → HTTP 422，`AICacheRulesUpdateAction:70-75` 会在返回前 `RecordSetAICacheRulesFailure`（失败审计的 before 快照取自库中现状），无需改 handler；
- 函数注释同步：在现有注释里补一句"collection-level cond uniqueness mirrors the BFE `ProductRuleConfListFile.Check` contract；duplicate conds make the whole exported file unloadable (issue #261)"。

**不做**：

- 不抽公共 helper（现为 3 处各 6 行的内联写法；抽 helper 会顺带改 `AIContextRules`/`TrafficMirrorRules` 的已审代码，扩大评审面。第四处出现时再抽）；
- 不动 `model/ai_cache`、`storage/rdb`、DAO、DDL、导出 generator、BFE 模块；
- 不动 `/ai-cache-semantic-settings`（单例，无 cond 集合）。

### 6.2 契约文档改动

1. `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md`
   - `:36` `rules[].cond` 行的"合法性条件"补为：`必填；必须能通过 BFE condition.Build 编译；同一集合内不得重复（重复即 BFE 整文件拒载）`；
   - §1 **约束**（`:45-59`）补一条：`同一集合内 cond 不得重复（逐字节比较）：BFE mod_ai_cache 以 cond 作为 product 内的规则键，重复会使整个 ai_cache.data 拒载、conf-agent 无限重试同一版本。控制面在 PUT 时 fail-fast（422），BFE 加载校验为最后防线。`；
   - §2.1 **执行逻辑** `:108` 第 2 步由"校验 `rules` 中规则名称是否重复"改为"校验 `rules` 中规则 `name` 与 `cond` 是否重复"。
2. `design-docs/api-define/InnerAPI接口定义/ai-cache-rule.md:111`：`同一 product 内 cond 不可重复` 后补一句"（控制面 `PUT /open-api/v1/ai-cache-rules` 已在写入前拒绝重复 cond，本接口不再产生该类非法文件；BFE 校验保留为最后防线）"。
3. `design-docs/sys-design/details/AI缓存规则与导出.md`
   - §4.2 校验表（`:134-146`）新增一行：`| 集合内 cond 唯一 | 遍历查重（422；与 BFE product 内 cond 唯一同判据，逐字节比较） |`；
   - `:147` 的"控制面不做集合内 cond 去重"改为"控制面在 PUT 时对集合内 `cond` 查重（422，issue #261）"；
   - §5.2 易错点 `:228`（"3. 同一 product 内 `cond` 不可重复；控制面不做集合内 cond 去重（BFE 加载报错兜底）"）同句替换。
4. `design-docs/modifications/2026-09-24-ai-cache-rule-export/design-changes.md`（历史记录，**不改结论、只加注记**）
   - `:131` 第 3 条、`:144` 表格行后加脚注：`> 2026-10-20 修订（issue #261）：控制面已改为 PUT 时对集合内 cond 查重（422），本条"控制面不做去重"的决策被取代；见 modifications/2026-10-20-issue-261-ai-cache-rules-duplicate-cond/。`
   - §6 参数校验表（`:204-...`）在"集合内 name 唯一"后新增"集合内 cond 唯一"一行，并同样标注修订来源。
5. `CHANGELOG.md` `[Unreleased] > Fixed`：新增一条——`PUT /open-api/v1/ai-cache-rules` 现在拒绝集合内重复的 `cond`（422，集合不变）。此前重复 `cond` 会被接受并导出，使 BFE `mod_ai_cache` 整文件拒载（`can't have same cond`）、conf-agent 无限重试同一版本且 symlink 不切换，管理面却只看到 200；对齐 `/ai-context-rules`、`/traffic-mirror-rules` 的既有前置校验（issue #261）。
   - 不标 **Breaking**：该形态此前写入的结果是"必然不可加载"，没有可用行为被收走；但对"读回存量非法集合 → 改一条 → 整体 PUT"的调用方是新 422，需在 §8 的升级说明里写清处置。

### 6.3 测试

**单元测试** `lib/validate/validate_test.go::TestAICacheRules`（`:606-708`）新增：

- 三条 `cond` 互不相同 → `NoError`（正向锚点）；
- 不同 `name`、相同 `cond`（`cache_ttl`/`cache_key_strategy` 不同）→ `Error`（**核心回归**，修复前为 `NoError`）；
- 相同 `name`、相同 `cond` → 仍报 `duplicate ... name`（判重顺序：name 先于 cond，与 `TrafficMirrorRules` 一致）；
- 相同 `cond` 但都非法 → 报编译错误而非重复错误（校验顺序锁定）。

**集成新增**（`test/integration/tests/ai_cache/update/update_test.go`，接续 AC-1-016）：

- **AC-1-017 集合内重复 cond（异常参数，家族3/4）**：先 PUT 一条合法规则并取 `rulesSnapshot` → PUT 两条仅 `name` 不同、`cond` 完全相同的规则 → 断言 **422**、`ErrMsg` 含 `duplicate` 且含该 `cond` 原文、GET 回读与 `before` 快照逐字段一致（拒绝即零变更）、失败审计一条（`status` 为失败且 before 取自库中现状）；
- **AC-1-018 合法集合的幂等编辑不受影响（正常参数，家族1/11）**：PUT 2 条 `cond` 不同的规则 → GET → 原样 PUT → 200 且逐字段一致（证明新增校验不会误伤"读回—改—写回"路径，#258 的修复不回退）。

`test/integration/tests/ai_cache/design.md`：§3 统计 `全量更新（PUT）16 / 合计 25` → `18 / 27`；§5 目录注释 `AC-1-001 ~ AC-1-016` → `~ AC-1-018`；用例表补 AC-1-017/018；该文档 §6.4 补详细设计；§2 约束段落补"集合内 `cond` 唯一"；AC-1-001~016 的示例 `cond` 若为多规则同值，一并改为唯一形态。

**夹具迁移（与代码同批，必须做，否则约 20 处用例转红）**：给每个测试文件加一个唯一 `cond` 构造器，例如

```go
// aiCacheCondFor 在基准表达式上追加一个仅用于区分规则身份的 model 子句：
// 导出到 BFE 的 cond 必须逐条不同（issue #261），同名 cond 会使整文件拒载。
func aiCacheCondFor(suffix string) string {
	return fmt.Sprintf(`req_path_in("/v1/chat/completions", false) && req_body_json_in("model", %q, false)`, suffix)
}
```

迁移清单（§2.3 表格的六个文件）：多规则集合中每条规则的 `cond` 改为 `aiCacheCondFor(<case>-<n>)`；`test/integration/tests/innerapi/ai_cache/ai_cache_test.go` 中"用相同 cond + 不同 TTL 锁顺序"的断言改为"用唯一 cond 锁顺序"（迁移后 `cond` 本身就是期望值的一部分，锁定更强）；`endpoints_test.go:205-209` 两条 `default_t()` 改为两条不同表达式。

### 6.4 验证

1. `go build ./...`、`go vet ./...`、`go test ./... -count=1`（主模块与 `test/integration` 模块）；
2. `make test-model-cover-gate`（本次只改 `lib/validate`，确认 model 覆盖率无回落）；
3. 集成：`go test -count=1 ./tests/ai_cache/... ./tests/innerapi/ai_cache/... ./tests/schema/...` 全 PASS；再 `go test -count=1 -p 1 ./tests/...` 全量串行复核；
4. **靶向复现**：按 issue `## 复现步骤` 第 2 步的 body 原文重放 → 期望 **422** + `ErrMsg` 含 `duplicate ai cache rule cond`，`GET` 回读集合零变更，`GET /inner-api/v1/configs/ai-cache-rule` 不产生新版本（对比修复前的 200 + 新版本）；
5. **反向对照**：用 `git worktree add /tmp/issue261-old HEAD` 编出修复前二进制，跑 AC-1-017 → 期望 `expected ErrNum=422, got 200`，证明用例确实钉住缺陷；验证后 `git worktree remove --force` 清理。

## 7. 行为变化表

| 场景 | 修复前 | 修复后 |
|---|---|---|
| PUT 两条仅 `name` 不同的规则（同 `cond`） | **200** + 持久化 → BFE 拒载、长期不收敛 | **422** `duplicate ai cache rule cond: ...`，集合不变，失败审计一条 |
| PUT 三条 `cond` 互不相同 | 200 | 200（不变） |
| PUT 语义等价但字面不同的两个 `cond`（如 `a && b` / `b && a`） | 200 | 200（BFE 同样不判重，口径一致） |
| PUT 重复 `name` + 重复 `cond` | 422（name） | 422（name，判重顺序不变） |
| GET 回读 → 改一条 → 整体 PUT（合法集合） | 200 | 200（AC-1-018 锁定） |
| GET 回读 → 改一条 → 整体 PUT（**存量**含重复 cond 的集合） | 200（继续产出不可加载配置） | **422**，需先按 §8 清理重复规则 |
| `{"rules":[]}` / `rules` 缺省 | 200 | 200（不变） |
| 默认值回填、顺序语义、单事务回滚、审计 `diff_keys`、Inner 导出格式 | — | 全部不变 |
| 数据面（BFE `Check`、conf-agent） | 拒载 + 5s 重试 | 不变（非法文件不再被生产出来） |

## 8. 存量非法集合的处置（升级后仍会踩到）

修复只作用于写入路径。事故现场这类**已经落库**的重复集合，升级后仍会被导出、仍会被 BFE 拒载（配置持续不收敛，且"读回—改—写回"会开始 422）。三层处置：

1. **排查（必做，写进升级说明/发布说明）**：
   ```sql
   SELECT cond, COUNT(*) AS n, GROUP_CONCAT(name ORDER BY id) AS names
   FROM ai_cache_rules GROUP BY cond HAVING n > 1;
   ```
2. **修复流程（必做，文档）**：`GET /open-api/v1/ai-cache-rules` → 对重复组保留一条（把需要的语义合并进保留的那条，或改写为等价但字面不同的表达式）→ 整体 PUT 回写。整体 PUT 是唯一写入口，清理动作本身即可触发一次正常导出与热加载。
3. **控制面护栏（可选，需 owner 定）**：`AICacheRuleGenerator` 导出前对集合做一次 `cond` 查重，命中时打**一条** ERROR 日志（内容含重复 `cond` 与规则 `name` 列表）——用"上次告警的内容签名"做进程内去重，避免退化成每轮询一条（issue 抱怨的日志噪音不能从 BFE 搬到控制面）；可选再加 `stateful.MetricAICacheRuleInvalidSet` 计数便于告警。**不改变导出语义**（仍导出、仍由 BFE 拒载），因此不影响数据面契约，也不会把现有故障从"BFE 拒载"变成"InnerAPI 报错"。缺点是给导出路径加了业务判断，与"导出只做搬运"的边界略有摩擦——若 owner 倾向于保持导出层纯净，此项留空，只做第 1、2 层。

## 9. 跨仓事项（本仓不改其他仓库文件）

1. **conf-agent（issue 候选 3）**：长时间 reload 失败时按指数退避 + 只打一条"reload keeps failing（首末时间/次数/版本）"收敛日志、暴露连续失败指标。本卡只记录建议，不阻塞 #261（本卡修的是"不再生产不可加载文件"，异常文件仍可能出现，兜底可运维性值得单独一张卡）。
2. **E2E harness**：issue 推断"cleanup 的 baseline 快照/回滚会把非法集合当基线回写"。fail-fast 落地后这类回写会拿到 422，harness 需要（a）在收到 422 时不把该快照记为下一轮基线；（b）日志里带出 422 原因，而不是静默忽略。
3. **E2E TC**：`SC1401-TC017/018/028` 及任何"准备多条规则只改 `name`/`ttl`"的用例，规则准备需改为 `cond` 唯一，否则会开始 422（这类用例此前实际一直跑在不可加载的配置上，`tc028manual-exact` 从未生效即为一例）。

## 10. 风险与回滚

| 风险 | 评估 |
|---|---|
| 破坏既有调用方脚本（批量复制规则只改 `name`） | 中。该脚本此前产出的配置从未生效过，所以"被拒绝"是暴露既有错误而非新增故障；用 422 + 明确文案 + 升级说明覆盖 |
| 存量集合导致"读回—改—写回"受阻 | 中。属预期行为（集合本身非法）；§8 给出排查与修复流程 |
| 校验顺序变化影响既有 422 文案（客户端按文案匹配） | 低。新增检查插在编译之后，不改任何既有分支的判断顺序与文案 |
| 覆盖率回落 | 低。`lib/validate` 为纯函数分支，新增 4 个断言即可 |
| 回滚 | 单文件 6 行 + 文档；移除 `condSet` 即回到旧行为，无数据迁移；夹具迁移**无需回滚**（`cond` 两两不同的集合在新旧实现下都合法，回滚后仍全绿） |

## 11. 待确认

1. ~~**设计决策修订的批准**~~ → **已关闭（2026-10-20）**：owner 批准把"控制面不做集合内 cond 去重"修订为 PUT 时 fail-fast，本卡按方案 A 实施；
2. §8 第 3 层（导出侧告警护栏）是否本期做：建议**做日志、暂不做指标**，若 owner 认为导出层应保持纯净则整层留空；**本期未做**（只交付 §8 第 1、2 层的排查/修复流程，见 §12.4）；
3. §9 第 1 条（conf-agent 退避/告警）是否拆卡：**仍待定**，属 conf-agent 仓库；
4. E2E harness / TC 的迁移由对应仓库 owner 跟进（本仓不改其他仓库文件）。

## 12. 实施状态

**已完成（2026-10-20，方案 A）**：代码、契约、夹具、文档四层同批落地，并在真实二进制上完成正向验证与反向对照。§11 第 1 条的批准已取得。

### 12.1 改动文件

代码（1）：

- `lib/validate/validate.go`：`AICacheRules` 新增 `condSet`（声明与 `nameSet` 同位），在 `ConditionExpression` 编译之后判重，重复返回 `duplicate ai cache rule cond: %s`；函数注释写明"与 BFE `ProductRuleConfListFile.Check` 同判据、逐字节比较、无 trim/归一化"。

测试（7）：

- `lib/validate/validate_test.go`：`TestAICacheRules` 新增 4 组断言（不同 name 同 cond → Error 且含 cond 原文；唯一 cond → NoError 正向锚点；重名优先于重复 cond；两条相同非法表达式报编译错误而非重复错误）；
- `endpoints/openapi_v1/ai_cache/endpoints_test.go`：语义开关往返用例的两条规则改为不同 cond；新增子用例 `422 duplicate cond in collection`；
- `test/integration/tests/ai_cache/update/update_test.go`：夹具迁移（AC-1-002/003/006/009/011/013/014/015，共 19 条规则）；新增 `TestAICacheRules_Update_DuplicateCond`（AC-1-017）与 `TestAICacheRules_Update_UniqueCondIdempotentEdit`（AC-1-018）；
- `test/integration/tests/ai_cache/get/get_test.go`：AC-2-002（3 条）、AC-2-004（2 条）迁移为唯一 cond；
- `test/integration/tests/innerapi/ai_cache/ai_cache_test.go`：新增 `aiCacheCondFor`，AICE-1-001/004/005/010 的规则集合与导出断言改为唯一 cond（顺序锁由"相同 cond + 不同 TTL"升级为"cond 本身唯一"）；
- `test/integration/tests/schema/openapi/openapi_schema_test.go`、`.../schema/innerapi/innerapi_schema_test.go`：各自第二次 PUT 的第 2 条规则改用独立 cond，导出期望值同步；
- `test/integration/tests/ai_cache/design.md`：§3 统计 `PUT 16 → 18`、`合计 25 → 27`；§5 注释 `AC-1-001 ~ AC-1-018`；§6.2.1/§6.2.2 补 cond 唯一约束；§6.3 用例表补 AC-1-017/018；§6.4 新增 §6.4.17/§6.4.18 详细设计。

文档（6 + 本文件）：

- `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md`（§1 字段条件、§1 约束新增一条、§2.1 执行逻辑第 2 步）；
- `design-docs/api-define/InnerAPI接口定义/ai-cache-rule.md`（§3.2 说明：控制面已在写入前拒绝重复 cond）；
- `design-docs/sys-design/details/AI缓存规则与导出.md`（§4.2 校验表新增"集合内 `cond` 唯一"行、§4.2 段末改写、§5.2 易错点第 3 条改写、**§10 边界情况表"集合内 `cond` 重复"行由"控制面不拦截"改为"422（fail-fast）"** —— 该行不在方案清单内，属实施时发现的同类表述漂移，一并修正）；
- `design-docs/modifications/2026-09-24-ai-cache-rule-export/design-changes.md`（§4.2 易错点第 3 条与 §4.3 对应表加"已被 #261 取代"修订注记、§6 校验表新增一行；原结论不改写）；
- `CHANGELOG.md`（`[Unreleased] > Fixed` 新增一条，含"200 但数据面不可加载"的成因说明与被阻塞的收敛路径）；
- 本文件（方案 + 实施记录）。

### 12.2 验证结果

| 项目 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...`；`go build -o ai-gateway-api.exe .` | 通过；二进制重建（41,308,160 字节），集成测试跑的就是它 |
| 静态检查 | `go vet ./...`（主模块）、`go vet ./...`（`test/integration` 模块） | 均干净（exit 0） |
| 单元测试 | `go test ./... -count=1`（主模块） | 96 个包全 `ok`，exit 0 |
| 覆盖率门禁 | `go test -coverprofile=… ./model/...` → `go tool cover -func` | `total: 85.0%` ≥ 70%，通过（本次未改 `model/`） |
| 集成（本卡） | `go test -count=1 ./tests/ai_cache/...` | `get` 1.7s / `update` 16.7s，全 PASS |
| 集成（导出与契约锁） | `go test -count=1 ./tests/schema/openapi/ ./tests/schema/innerapi/ ./tests/innerapi/ai_cache/` | 全 PASS（3.2s / 3.0s / 2.0s） |
| 新增用例细跑 | `go test -run "TestAICacheRules_Update_DuplicateCond\|TestAICacheRules_Update_UniqueCondIdempotentEdit" -v` | 两条 PASS（5.08s / 0.02s） |
| 集成（全量串行） | `go test -count=1 -p 1 ./tests/...` | 见 §12.5 |

### 12.3 靶向复现与反向对照

- **反向对照（证明 AC-1-017 钉住缺陷）**：`git worktree add /tmp/issue261-old HEAD` 取修复前代码编出二进制、替换 `ai-gateway-api.exe` 后跑 AC-1-017：
  - 失败表现与 issue 现场逐条对应：PUT **未**返回 422（被接受），`rulesSnapshot` 与 `before` 不再相等（两条重复 cond 的规则已持久化，只差 `cache_ttl` 60/600 与 name），且 15s 内**查不到任何失败审计**（`operation log not found after 15s (status=2)`）——即"调用方只看到 200、非法集合进入库"的完整复现。
  - 临时 worktree 已 `git worktree remove --force` 清理，工作区只保留本卡的正常改动。
- **正向验证**：换回修复后二进制，AC-1-017 / AC-1-018 全 PASS；issue 复现 body 的形态（两条仅 `name` 不同、`cond` 逐字相同的规则）稳定 422，`ErrMsg` 含 `duplicate ai cache rule cond: req_path_in("/v1/chat/completions", false)`，集合零变更。

### 12.4 Step 6 沉淀评估与遗留

**不新增 `sys-design/details/` 文档。** 可复用知识已就地沉淀在三处：`AI缓存规则与导出.md` §4.2（校验表 + fail-fast 表述）、`design-changes.md` §4.3（控制面/数据面对应关系表）、`ai-cache-rules.md` §1（"同一集合内 cond 不得重复（逐字节比较）"及其 BFE 成因）。机制层面无新流程、无架构变化，`sys-design/summary.md` 索引无需变更。

遗留（均在本仓之外或其后续卡）：

1. **存量非法集合**（§8）：排查 SQL 与修复流程已写进本文件，但**未加到发布说明**，也未做导出侧告警护栏（§8 第 3 层，owner 未要求本期做）。事故现场这类已落库的集合升级后仍会持续不收敛，直到执行一次清理 PUT；
2. **conf-agent**：退避 / 收敛日志 / 连续失败指标仍未做（§9 第 1 条，建议单独拆卡）；
3. **E2E harness / TC**：baseline 快照回写与"只改 name/ttl"的规则准备脚本需同步（§9 第 2、3 条），否则会开始收到 422；
4. **同族清扫**：本次只补 `AICacheRules`；`RouteRules`（`lib/validate/validate.go:591`）等其它集合资源是否也存在"控制面放过、数据面拒载"的同类缺口，未在本卡范围内核对。

### 12.5 全量集成（串行）结果

`go test -count=1 -p 1 ./tests/...`（`test/integration` 模块，约 14.2 分钟）：**exit 0，127 个包全部 `ok`，0 个 FAIL**；其中本卡相关包 `tests/ai_cache/get` 1.5s、`tests/ai_cache/update` 17.0s、`tests/innerapi/ai_cache` 1.9s、`tests/schema/openapi` 2.0s、`tests/ai_cache_semantic_settings/*` 各约 1.1s，均为 `ok`。串行模式未出现既有并行抖动。

至此本卡的验收判据全部落地：重复 `cond` 稳定 422 且集合零变更（AC-1-017，反向对照已证明修复前为 200 + 落库 + 无审计）、合法集合的读回原样 PUT 仍 200（AC-1-018）、BFE 侧校验与导出格式零改动。