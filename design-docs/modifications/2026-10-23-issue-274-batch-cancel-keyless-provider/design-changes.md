# Issue #274 控制面：批量任务取消兼容 keyless provider（条款与代码变更明细）

## 1. 设计目标

让批量任务的四条控制面出网链路（取消 / 对账状态推进 / 终态产物补漏 / 兜底结算）兼容
`keys: []` 的合法 keyless provider（`providers.md:61`、`clusters.md:191`），并修复同一
失败链路上的两个衍生缺陷：对账把 `cancelling` 降回 `in_progress`（取消意图丢失）、
`cancelling` 重放的 409 假冲突。

**变更边界**：不改 SQL/DDL/存储结构；不改数据面；`PreemptBatchTaskCancel` 语义
（仅首个转换者成功）不变；终态重放 409 不变；配置了 key 的 provider 出网仍携带原鉴权头。

## 2. api-define 变更（Step 3）：`OpenAPI接口定义/batches.md` §3.3

### 2.1 前置依赖（:301-303）

现有：

> - 控制面 → provider **出网**可达（降级部署无出网时，本接口退化为不可用，批量任务仅支持展示）。

改为：

> - 控制面 → provider **出网**可达（降级部署无出网时，本接口退化为不可用，批量任务仅支持展示）。
> - provider 未配置 API-Key（`keys: []`，见 `providers.md`）时，取消请求**不携带鉴权头**
>   出网——与数据面对 keyless cluster 的转发语义一致。

### 2.2 处理逻辑 1（:317）

现有：

> 1. **DB 条件更新抢占**：`UPDATE ... SET status='cancelling' WHERE batch_id=? AND status NOT IN (终态)`；`affected_rows=0` → 返回 409（已终态，或其他实例正在处理）；

改为：

> 1. **DB 条件更新抢占**：`UPDATE ... SET status='cancelling' WHERE batch_id=? AND status NOT IN (终态, cancelling)`；`affected_rows=0` 时取任务当前状态判别：已终态 → 返回 409；已为 `cancelling` → **200 幂等重放**（重发 provider cancel 保证取消意图落地，预留释放为幂等 no-op，不新增 release/charge/settlement transition）；

（"重发 cancel"的理由：`cancelling` 可能来自"首次抢占成功但出网失败/进程崩溃"的残留，
重放是恢复意图的机会窗口；provider cancel 幂等。）

### 2.3 错误码表（:359-366）

409 行：

> | 409 | 任务已终态，或他实例正在处理（条件更新抢占失败） |

改为：

> | 409 | 任务已终态（`cancelling` 受理中的重放返回 200，见处理逻辑 1） |

其余行不动。

## 3. sys-design 变更（Step 4）：`details/批量任务与对账.md`

### 3.1 §3.2 状态机（:47-59）

"终态不回退"条目下补一条同级规则：

> - **cancelling 不回退（取消意图保持）**：`cancelling` 是控制面受理取消后断言的状态，
>   非 provider 回报状态。对账职责② 遇 provider 滞后仍报 `in_progress`/`queued` 等
>   非终态时，本地保持 `cancelling` 并补发一次 provider cancel（幂等，fail-open 下轮重试）；
>   直至 provider 回报终态（`cancelled`/`completed`/`expired`/`failed`）正常收敛。

### 3.2 §5 职责②：状态推进（:100-102）

现有末句"幂等：upsert 天然可重入。"后追加：

> **取消意图保持**：本地状态为 `cancelling` 且 provider 回报非终态非 `cancelling` 时，
> 不回写状态，并补发 `POST /batches/{id}/cancel`（复用 cancel 出网实现，失败告警、
> 下轮重试）——覆盖"抢占成功但首次出网未落地"的崩溃空洞。

### 3.3 §6.1 五步流程（:133-141）

- 第 2 步（:138）"取 key 拼地址"补一句：

  > provider `keys` 为空数组时跳过取 key，出网请求不携带鉴权头（keyless 合法配置，
  > `providers.md` keys 条款）。

- 第 4 步（:140）"affected_rows=0 返回 409"改为与 §2.2 相同的判别口径
  （终态 409；已 `cancelling` 幂等 200 重放）。

### 3.4 §6.2 竞态收敛表（:143-152）

cancel×cancel 行（:152）：

> `cancel×cancel` 由第 4 步 DB 抢占（affected_rows=0 → 409）互斥。

改为：

> `cancel×cancel` 由第 4 步 DB 抢占互斥：首个转换者受理（affected=1）；重放者
> （affected=0 且当前状态 `cancelling`）走 200 幂等重放——重发 provider cancel 幂等、
> 释放幂等，不会双倍冲正；终态抢占失败仍 409。

## 4. 代码变更清单（Step 5 对照）

| 文件 | 动作 | 说明 |
|------|------|------|
| `model/ibatch/cancel.go` `resolveProviderAccess` | 修改 | 三分支：`len(provider.Keys)==0` → keyless 放行（authHeader 置空）；非空走 `pickProviderKey`，nil 才报 `errProviderUnavailable`（:176-179 挪入分支） |
| `model/ibatch/cancel.go` `postProviderCancel` | 修改 | `if access.authHeader != ""` 守卫后再 `Header.Set`（:122） |
| `model/ibatch/cancel.go` `Cancel` | 修改 | affected=0 分支（:66-84）末尾增加判别：`FetchTask(..., false)` 取 DB 权威状态，`cancelling` → 重解析 access + 重发 cancel + 幂等释放 + 审计后 200 返回；终态/未知 → 409 不变 |
| `model/ibatch/cancel.go:150-151` 注释 | 修改 | "无可用 key = 数据异常" → keyless 合法化表述 |
| `model/ibatch/advance.go` `getProvider` | 修改 | 同上空 header 守卫（:313） |
| `model/ibatch/advance.go` `advanceOneTask` | 修改 | 解析 batchResp 后、终态防御前插入"取消意图保持 + 补发 cancel"分支；`changed` 计算相应剔除被 hold 的 status（:217-248） |
| `model/ibatch/errors.go:43-44` 注释 | 修改 | errProviderUnavailable 适用面收窄为"provider 不存在/无实例/key 明文全空" |
| `model/ibatch/advance.go` `providerAccess` 相关注释 | 修改 | 补"authHeader 空 = keyless 无鉴权出网" |
| `storage/rdb/batch/batch_task.go:74-94` 注释 | 修改 | affected=0 语义细分说明（存储语义不变，SQL 不动） |
| `settle.go` / `advance.go` 补漏 / `backfillOneTask` | 不改代码 | 经公共 `resolveProviderAccess` 自动恢复 keyless 出网（change-summary §2.2） |

## 5. 测试设计（change-summary §7 的明细）

### 5.1 单测（`model/ibatch`，新增/扩充）

- `cancel_test.go`（或现有 resolve 测试文件）表驱动覆盖 §4 的三分支与守卫；
- 关键回归闸门（**当前 HEAD `0d9e2f1` 上必须失败**）：
  1. keyless provider cancel → 200 且 provider 收到无鉴权 POST；
  2. `cancelling` 重放 → 200（现状 409）；
  3. 对账遇 `cancelling` + provider `in_progress` → 状态不降级且收到补发 cancel。
- 防回归闸（修复前后都应通过）：终态重放 409；keyed provider 出网带头；
  provider 不存在/无实例/明文全空 500。

### 5.2 集成测试（`test/integration/tests/batch/`）

BT-3-009（keyless cancel 200 + 无鉴权头 + 预留释放）、BT-3-010（cancelling 重放 200
+ 无新增结算 transition）、BT-3-011（终态重放 409 回归闸）、job 侧对账意图保持用例
（编号落地时按 `tests/batch/design.md` §6 登记，断言要点见 change-summary §7.2）。
种子与假 provider 准备复用 BT-3-006 的现成模式，无需新增 helper 字段
（`helper.InsertBatchTask` 已支持指定 provider/key_name/status）。

### 5.3 系统级

issue 五条验收判据即系统级判据；SC1401-TC026 `cancel-replay` 由 FAIL 转 PASSED，
其余子断言不回退。

## 6. 与其他修改记录的关系

- **#271**（同目录 2026-10-23）：响应形状零值显式。无交集——#274 改行为语义与错误码，
  不改响应字段集；`batchTaskView` 投影原样复用。
- **#254**（2026-10-19 状态与结算修复）：#254 修"字段有值读不出"，本方案修"keyless
  出网被硬拒绝 + 意图被降级"。两者在 SC1401-TC026 上对账/结算断言上互补，需一起回归。
- **#249**（详情数据形状）：遗留的 `id`/`key_name`/`idem_key` 契约超集裁剪不受本方案影响。

## 7. 实施记录

已于 2026-10-23 按本文档落地，基线 `0d9e2f1`。要点与偏差：

1. §4 代码清单与实际一致（cancel.go / advance.go / errors.go / 存储注释，settle.go 与
   backfill 经公共 `resolveProviderAccess` 自动恢复，未改代码——同 §4 末行预判）；
2. **实施中补入 BT-3-007（MySQL 并发用例）契约改写**（change-summary §11.2.1）：
   R3 使 cancelling 重放 409→200，原"1×200 + 19×409 + 恰好 1 次出网"契约不再成立，
   按"全部 200 + 20 次幂等出网 + 20 条成功审计"改写；
3. pickProviderKey 按名命中空明文 key 的透传为既有语义，测试按 keyName 不匹配/为空
   路径钉死 500 边界（change-summary §11.2.2）；
4. job 侧意图保持无集成 harness，由单测覆盖（change-summary §11.2.3）；
5. 验证：`go build/vet` 通过；`go test ./...` 全量通过；model 覆盖率 85.0% ≥ 70%；
   batch 集成模块（SQLite）全过；旧代码 worktree 复现实验三个闸门用例全部 FAIL、
   修复后全部通过；MySQL 门待用例（BT-3-007）待 DSN 环境复跑。明细见
   change-summary §11.3 / §11.4。
