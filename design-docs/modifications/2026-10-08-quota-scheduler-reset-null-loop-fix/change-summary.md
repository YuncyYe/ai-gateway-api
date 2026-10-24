# 配额周期重置死循环修复（quota scheduler `last_reset_at` NULL）——变更摘要

## 1. 背景

运行时发现：对 `GET /open-api/v1/api-keys` 不停刷新，部分 API-Key 的
`quota_plan.balance.used` 会变为 0（这些 key 此前因 token 消耗 used 不为 0），
期间未调用任何重置接口。

排查结论：`GET /api-keys` 链路本身为只读，不可能修改余额；真正的原因是
**配额周期重置调度器对 `last_reset_at IS NULL` 的配额计划陷入"每分钟重置一次
Redis 余额"的死循环**，list 接口只是如实展示了被重置后的结果。
本修复对应 GitHub issue [rainway-ai-gateway/ai-gateway-api#228](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/228)。

运行态证据（同目录 `额度重置问题分析结果.txt`，Redis MONITOR 约 2 分钟日志）：

- 每 60 秒一轮，每轮将同一组 21 个 key（13 个 API-Key 额度 + 8 个 Entity 额度）
  批量写回初始额度，与"每月重置"配置不符；
- 重置命令为 `EVALSHA 728af114... "set" "QUOTA_<key>" "<初始额度>"`，
  操作前经历 `SETEX quota:reset:scheduler:lock 300` 加锁与 `DEL` 释放，
  确证写入方为控制面配额重置调度任务；
- 对照组：日志中批量读取了 15 个 product 额度，每轮仅重置其中 13 个，
  另 2 个（`xUVg4ETjNCFNISZCtsuyyDbE`、`fBueg4K3HV9nyZpZlCP4L3A7`）始终未被
  重置——说明按计划的到期过滤生效、判定逐 plan 独立，问题收敛于
  "到期判断"环节而非"是否执行重置"环节。

## 2. 根因分析

### 2.1 GET 链路为只读（排除刷新导致写）

- `endpoints/openapi_v1/api_key/list.go:56` `ListAction` →
  `model/api_key/api_key.go:207` `FetchAPIKeyList`：事务内读 DB，事务外
  `populateQuotaBalances`（`api_key.go:377`）批量读 Redis 剩余量
  （`model/quotacache/redis.go:62` `BatchGetRemaining`）。
- `fillQuotaBalance`（`api_key.go:323`）计算 `used = quota - remaining`，
  `used < 0` 时钳制为 0。
- 全链路无任何 Redis / DB 写操作。响应中出现 `used = 0` 只说明那一刻
  Redis 中 `QUOTA_<key>` 的 remaining ≥ quota，即 key 被重置回了满额。

### 2.2 自动重置来源：调度器 NULL 死循环（主因）

`QuotaResetScheduler` 在进程初始化时无条件启动
（`stateful/container/rdb/components.go:419-424`），每 1 分钟执行一次
（`model/quota/scheduler.go:85`）。`ResetExpiredBalances`
（`model/quota/balance_sync.go:48`）的判定与兜底存在配合缺陷：

1. 捞出 `reset_period ∈ {weekly, monthly}` 且非 unlimited 的计划
   （`balance_sync.go:51-54`；枚举定义见 `lib/validate/validate.go:395-399`）。
2. `shouldResetByPeriod`：`lastResetAt == nil` 直接返回 true
   （`balance_sync.go:104-105`）。而 API 创建配额计划从不写 `last_reset_at`
   （`db_ddl.sql:430` 为 `DEFAULT NULL`），此类计划**永远被判为"需要重置"**。
3. 对该计划下所有 API-Key / Entity 执行 `ResetToQuotaAtomic`
   （`balance_sync.go:186/202`，Lua `SET QUOTA_<key> = quota` 满额）
   → remaining = quota → list 接口显示 used = 0。
4. 幂等兜底：`UPDATE quota_plans SET last_reset_at = now WHERE id = ? AND
   last_reset_at < ?`（`balance_sync.go:79-84`；DAO 字段 tag
   `db:"last_reset_at,<"`，`storage/rdb/internal/dao/table_quota_plans.go:76`）。
   当 `last_reset_at` 为 NULL 时，SQL 三值逻辑下 `NULL < x` 不成立 →
   **0 行命中，但 `internal.Update` 返回 `(0, nil)`**
   （`storage/rdb/internal/dao/internal/curd.go:99-121`）；上层只检查 err
   （`balance_sync.go:85-89`），打印成功日志。
5. 下一分钟：`last_reset_at` 仍为 NULL → 回到第 2 步。**每分钟重置一次的
   无限循环。**

**引入版本**：commit `5f0184f`（2026-08-31，"feat: quota reset scheduler
distributed lock and provider test docs"）。该 commit 将第 5 步由
"按 ID 无条件更新 `last_reset_at`"改为"`WHERE last_reset_at < periodStart`
条件更新"（本意是同一周期内幂等兜底），并引入裸 `SET` 的
`ResetToQuotaAtomic`（MONITOR 日志中的 `EVALSHA 728af114` 指纹）。
改动前无条件更新会正确回填 NULL 计划的重置时间，循环不成立；
改动后条件对 NULL 行恒命中 0 行，与 `shouldResetByPeriod` 的
`nil → true` 组合成死循环。`git log -S "LastResetAtBefore"` 全库仅该
commit 命中。

**伴生缺陷（issue #228 一并要求修复）**：当前流程为"先重置 Redis
（`resetAPIKeysRedisUsage`，`balance_sync.go:72`）→ 后条件认领
（`balance_sync.go:79`）"。条件更新本应用作"本轮归我重置"的认领标志，
但重置发生在认领之前——多实例（或锁失效窗口）下，多个实例可同时通过
到期判断并各自先把 Redis 拍回满额，只有其中一个认领成功，其余实例的
重置已成事实，造成同周期内重复重置；即使主缺陷修复后该竞态依然存在。

现象匹配：`reset_period = never` 的计划不受影响，故只有"某些 api-key"
出现 used 归零；连续刷新时大多数时刻采样到的都是刚被清零的状态，若当时
无流量消耗即看到 `used = 0`。

### 2.3 运行时证据与代码的对应关系

MONITOR 日志中的命令与代码一一对应：

| 观测到的命令 | 代码位置 | 说明 |
|--------------|----------|------|
| `SETEX quota:reset:scheduler:lock 300` / `DEL` | `model/quota/scheduler.go:118-138` | 调度器分布式锁（5 分钟 TTL + 续期），确证操作来自 `QuotaResetScheduler` |
| `EVALSHA 728af114... "set" QUOTA_<key> <quota>` | `model/quotacache/redis.go:26-29` `setQuotaScriptSrc` ← `balance_sync.go:186/202` `ResetToQuotaAtomic` | 全代码库唯一对 `QUOTA_*` 执行裸 `SET` 满额的 Lua；数据面 BFE 扣费（`bfe/bfe_modules/mod_ai_token_auth/token.go:93/117`）与惰性初始化使用不同脚本，可排除 |
| `EVALSHA b09e2339... "decrby"` | BFE `deductToken` / `deductRMB` | 正常扣减，与重置无关 |
| 每轮 21 key = 13 API-Key + 8 Entity | `balance_sync.go:147-212` `resetAPIKeysRedisUsage` | 与"按计划同时重置 API-Key 与 Entity"的实现一致 |
| 15 读 13 重置的对照组 | `balance_sync.go:51-68` | 被重置的 13 个 key 同属被判"到期"的计划；未重置的 2 个 key 属于 `reset_period=never`（未进入捞取范围，`balance_sync.go:52`）或 `last_reset_at` 已正确落在当前周期内的计划——到期过滤本身工作正常，失效的是"重置后记录 last_reset_at"这一步（第 4 步），与 §2.2 根因闭环一致 |

由此可排除的三个嫌疑：① list 查询链路（只读，§2.1）；② BFE 数据面写
Redis（脚本指纹不符）；③ Redis 自身 / key_affinity 等其他操作（日志已逐一
甄别，见分析结果文件第四节）。

### 2.4 其他可能使 used 归 0 的路径（次要，本次不修）

| 路径 | 位置 | 说明 |
|------|------|------|
| 配额计划编辑 | `model/quota/quota_plan_manager.go:263` `ApplyQuotaPlanChange` → `:276` `adjustQuota` | 单位变化 / unlimited 切换 / 新建计划时 `useReset=true` 整体重置；仅改 quota 时 `SetRemaining`（`model/quotacache/redis.go:88`）为 GET+INCRBY 非原子，与数据面扣费竞争可能把 remaining 推高超额，响应钳制 used=0 |
| 数据面 RMB 惰性初始化 | `bfe/bfe_modules/mod_ai_token_auth/token.go:117` `deductRMB` | key 缺失时 `SET key = quota` 再扣费；仅 RMB 单位，需 key 被清理/驱逐才会触发 |
| 单位不一致读放大 | `go-lib/quota/quota.go:53` `IsRMB` | DB `unit` 为 NULL/空（按 total_token 解析）而 Redis 存 RMB 定点值（×1e8）时 remaining 读成天文数字 → used 钳制为 0；表现为这些 key 恒为 0 |

## 3. 修复目标

1. 消除"判重置 → 重置 → 记不上 → 再判重置"的死循环：保证重置成功后
   `last_reset_at` 一定落库。
2. 幂等兜底必须能区分"真正写入成功"与"0 行命中"，避免静默失败。
3. 消除"先重置后认领"的竞态：条件认领成功是执行重置的前置条件，
   同周期内任一计划全局至多被一个实例重置一次。
4. 不改变现有对外接口行为；`reset_period = never` 的计划与数据面扣费逻辑
   不受影响。

## 4. 修复方案（关键决策）

| 决策 | 说明 |
|------|------|
| 兜底条件显式处理 NULL | DAO 层为 `last_reset_at` 增加"NULL 亦可命中"的更新路径，使条件更新语义等价于 `WHERE id = ? AND (last_reset_at IS NULL OR last_reset_at < ?)`；具体实现优先调整 `balance_sync.go` 的过滤结构 / DAO where 构造，避免影响其他使用 `<` tag 的调用点 |
| 先认领后重置（顺序调整） | 将条件更新挪到 `resetAPIKeysRedisUsage` 之前作为"认领"：affected > 0 才执行本轮重置；0 行（同周期已被认领，或命中异常）直接跳过——多实例 / 锁失效窗口下，同周期内任一计划全局至多被重置一次，同时保留 NULL 显式命中语义 |
| 校验 rows affected | 条件更新的 affected 行数是唯一的成功判据：不再仅检查 err；0 行不再打印误导性成功日志，按"已被认领"或"写入失败"区分记录 |
| 创建即初始化 `last_reset_at` | 计划创建路径（含 API-Key/Entity 嵌套创建）在 `reset_period ≠ never` 时将 `last_reset_at` 初始化为创建时间，从源头避免 NULL 进入调度判定 |
| 失败无副作用 | 认领成功但 Redis 重置部分失败：下周期因 `last_reset_at` 已推进不会补重置（与原语义一致，SET 满额幂等）；认领失败则不触碰 Redis，消除"重置已发生但标记未落库"的中间态 |
| 数据迁移 | 存量 `reset_period ∈ {weekly, monthly} AND last_reset_at IS NULL` 的行，由修复后的首次调度自然回填（affected 行数修复后可正确置位），无需手工 DDL |
| 不做变更 | `SetRemaining` 原子化（Lua 化）与单位一致性校验属独立改进，不在本次范围 |

## 5. 范围

- **涉及面**：`ai-gateway-api` 控制面（配额周期重置调度、`quota_plans` 写路径）。
- **不涉及面**：OpenAPI / InnerAPI 接口定义（无接口变化，故无 `api-changes.md`）；
  BFE 数据面扣费逻辑；数据库表结构（无 DDL）。
- **数据面影响**：无（数据面仍按 Redis 剩余量扣费与校验）。

## 6. 影响与兼容性

- 修复后，原本死循环重置的计划会在下一调度周期被重置一次并正确记录
  `last_reset_at`，之后按自然周/自然月边界正常重置——属于恢复设计语义，
  对这些计划下的 key 而言"余额被周期性清零"本就是配置意图。
- 多实例部署下，同周期内同一计划仅一个实例执行重置（DB 条件认领串行化）；
  相对修复前"多实例可能各自重置一次"，这属于收敛到设计语义，不引入新风险。
- `reset_period = never` 或 unlimited 的计划行为不变。
- 手工重置接口（`POST /api-keys/{id}/quota-plan/reset`，
  `updateLastResetAt=false`）语义不变，不参与本次修改。

## 7. 验证方法

根因已由运行态 Redis MONITOR 日志确证（见 §1、§2.3 及关联的运行时分析
文件）；以下为修复前后的验证步骤：

1. 构造计划：`reset_period=monthly`、有限额，绑定 API-Key 并产生少量消耗。
2. 修复前观察：
   - `SELECT id, reset_period, last_reset_at FROM quota_plans;` ——
     `last_reset_at` 恒为 NULL；
   - ai-gateway-api 日志每分钟出现 "Reset Redis value for API-Key ... to quota"
     与 "Reset balance for plan ..."；
   - Redis `GET QUOTA_<key>` 每分钟回到满额；list 接口 `used` 周期性归 0。
3. 修复后观察：
   - 首次调度后 `last_reset_at` 被正确回填，日志不再每分钟重复；
   - 周期边界（下周一 00:00 / 下月 1 日 00:00）仅重置一次；
   - `go test ./model/...` 与配额相关集成测试（`quota_reset`、`quota_update`、
     `list`）通过。

**集成验证（已完成）**：`test/integration/tests/quota_period_reset/` 新增
`quota_reset_null_guard_test.go`（用例 QR-4-001 ~ QR-4-005，设计见同目录
`design.md` §8）：

- QR-4-001/002：创建路径初始化验证（monthly → 非 NULL；缺省 → 保持 NULL）；
- QR-4-003/004：存量 NULL 计划触发一次重置后标记落库、同周期再次触发不重复
  重置（旧实现两处均失败）；
- QR-4-005：不调用任何接口、仅靠调度器自然 tick（~150s，`-short` 跳过），
  端到端复现并验证运行时症状消除。

本机执行结果：全部 PASS（整包 10 例，QR-4-005 耗时 ~133s）。注意：

- 集成测试子进程使用项目根目录预编译的 `ai-gateway-api.exe`，运行前需先
  重新构建（`make build` 或 `go build -o ai-gateway-api.exe .`），否则子进程
  为旧二进制、用例按旧行为失败（本修复验证过程中已踩坑并确认）。
- 新增用例暴露了既有用例 QR-2 的隐患：`TestQuotaPeriodReset_MultiInstanceLock`
  在启动实例 A **之后**才捕获 `origURL`，而 `StartServerWithSharedInfra(nil, "")`
  会把全局客户端 URL 重置为实例 A 地址，导致其 defer 恢复指向已关闭的实例、
  同包后续用例连接被拒绝。已将捕获/恢复移到启动实例之前（原 QR-2 是该包
  最后一个用例，故从未暴露）。

## 8. 实施阶段

| 阶段 | 内容 | 状态 | 关键文件 |
|------|------|------|----------|
| 1 | 调度改造：条件更新前置为认领（NULL 显式命中 + affected 校验），认领成功才重置 Redis | ✅ 已完成 | `model/quota/balance_sync.go`、`storage/rdb/quota/quota_plan.go`、`storage/rdb/internal/dao/table_quota_plans.go` |
| 2 | 创建路径初始化 `last_reset_at`（`reset_period ≠ never` 时） | ✅ 已完成 | `storage/rdb/quota/quota_plan.go`（`CreateQuotaPlan`，覆盖 API-Key/Entity 嵌套创建） |
| 3 | 单测：NULL 计划一次重置即回填、周期内不重置、0 行命中跳过且不触碰 Redis、并发认领仅一个实例执行重置 | ✅ 已完成 | `model/quota/balance_sync_test.go`、`model/quota/mocks_test.go`、`model/imods/mocks_test.go` |
| 4 | 回归：`go test ./model/...` 全绿，模型覆盖率 84.8%（门禁 70%） | ✅ 已完成 | — |

**落地说明（与 §4 决策的对应）**：

- §4"兜底条件显式处理 NULL"落地为专用 DAO 方法 `TQuotaPlanClaimPeriodReset`
  （参数化裸 SQL：`UPDATE quota_plans SET last_reset_at=? WHERE id=? AND
  (last_reset_at IS NULL OR last_reset_at < ?)`）。原因：gendry where map
  不支持 `IS NULL OR <` 组合，且三值逻辑下 `<` 对 NULL 行恒不成立——这正是
  原实现用 `db:"last_reset_at,<"` tag 静默丢失 NULL 分支的根源。原
  `QuotaPlanFilter.LastResetAtBefore` 字段仅服务于该路径，已随认领方法
  一并移除。
- §4"先认领后重置"落地为 `ResetExpiredBalances` 内顺序调整：claim 返回
  affected=0（本周期已被认领）或 err（写入失败）均跳过本轮 Redis 重置。
- §4"创建即初始化 `last_reset_at`"落在存储层 `CreateQuotaPlan`：创建路径
  全部经此落库（含 API-Key / Entity 嵌套创建），`reset_period` 为
  weekly/monthly 时初始化；存量 NULL 行由修复后首次调度自然回填。
- 手工重置接口与 `ResetBalance` 语义未动；`ResetToQuotaAtomic` 的使用范围
  不变。

## 9. 关联文档

- 跟踪 issue：[rainway-ai-gateway/ai-gateway-api#228](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/228)
  （缺陷报告：复现步骤、预期 vs 实际、影响评估与证据索引）。
- 运行时排查证据：同目录 `额度重置问题分析结果.txt`
  （Redis MONITOR 日志分析：60 秒重置周期、21 个 key 清单、Lua 脚本指纹、
  对照组与排除项）。
- 问题现象与排查过程：见本文 §1–§2（运行时 `GET /api-keys` 刷新观察）。
- 调度器设计背景：`design-docs/sys-design/details/InnerAPI配置导出与版本控制.md`
  及配额相关设计文档中关于周期重置的章节（修复后需同步语义描述）。
