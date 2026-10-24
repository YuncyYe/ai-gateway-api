# Issue #274：keyless provider 批量任务取消 500——控制面修复说明

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/274](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/274)

> 管理面取消批量任务 `POST /open-api/v1/batches/{batch_id}/cancel` 对 `keys: []` 的 provider 下的
> `in_progress` 批次返回 HTTP 500：`Biz Exception: batch provider <provider> unavailable`，且不出网。
> 给 provider 补任意 key 后取消立即 200，去掉 key 又稳定 500——触发条件只有 `keys` 为空。后果是
> 取消意图无法落地：控制面已抢占为 `cancelling`，provider/数据面/Redis 仍是 `in_progress`；
> 重试先撞 409 假冲突，等对账把状态降回 `in_progress` 后才能再试——而降级本身又丢弃了取消意图。

产品条款依据（Oracle）：

- `design-docs/api-define/OpenAPI接口定义/providers.md:61`：`keys` 为 `[]ProviderKey`，**非必填；
  默认空数组 `[]`**——keyless 是合法配置；
- 同目录 `clusters.md:191`：`keys` 为空数组表示**不配置 API-Key**；
- 同目录 `batches.md` §3.3：取消 = 控制面直调 provider cancel 端点，前置依赖只有
  "控制面 → provider **出网**可达"——条款没有"provider 必须配置 key"这一前置。

代码把合法的 keyless 配置判为"控制面数据异常"并 500，是运行行为偏离条款。系统级验证用例：
`test-cases/SC1401/sc1401_tc026_test.go` 的 `cancel-replay` 断言。

## 2. 根因（当前 HEAD `0d9e2f1` 逐环核实，2026-10-23）

issue 的根因链 7 环全部与源码一致，此处按"修复相关度"重组为三组。

### 2.1 直接失败点：`resolveProviderAccess` 无 keyless 分支（实锤）

`model/ibatch/cancel.go:153-188`：

- `:165-168` 实例检查通过（`pickProviderInstance` 取第一个未禁用实例）；
- `:176-179` `pickProviderKey(provider, keyName)` 对 `keys: []` 两轮遍历（`:233-245`）均无元素
  返回 nil → `errProviderUnavailable(providerName)`；
- `errors.go:45-47` 该错误用 `WrapModelErrorWithMsg` 包装为 `etModel`；
- `lib/xerror/resolve.go:67-68` `etModel` 只设 `Type="Biz Exception"`，`ErrNo` 保持默认 **500**；
- `endpoints/openapi_v1/batches/cancel.go:47-53` 对该错误无特殊映射，默认渲染 → 观测到的 500。

`cancel.go:150-151` 的注释把"无可用 key"与"provider 记录不存在、无可用实例"并列为
"控制面数据异常"，与 `providers.md:61` / `clusters.md:191` 的 keyless 合法语义冲突——
这是缺陷的认识论根源。

### 2.2 同族放大：`resolveProviderAccess` 是四条出网链路的公共门禁（实锤，issue 未列全）

grep 全库调用点，`resolveProviderAccess` 同时服务：

| 调用点 | 链路 | keyless 下的现状 |
|--------|------|------------------|
| `cancel.go:86` | 管理面取消 | 500（本 issue） |
| `advance.go:189` | 对账 job 职责②状态推进 | 每 tick `Warn` 空转（`advance.go:70`），keyless 任务**永不推进** |
| `advance.go:129` | 职责②b 终态产物补漏 | 同上，补漏不可达 |
| `settle.go:155`（经 `resolveProviderAccessCached:276`） | 职责③兜底结算（provider key 下载输出文件） | 下载不可达，`completed` 任务无法兜底结算 |

即 issue 观测到的 500 只是 cancel 单点；同一门禁使 keyless provider 下的批量任务
**状态推进、产物补漏、兜底结算全部失效**，只是它们的表现形式是 Warn 日志而非接口 500。
修复必须落在公共函数，四条链路一起恢复。

### 2.3 意图丢失链：抢占不回滚 + 409 假冲突 + 对账降级（实锤）

1. `cancel.go:61-84`：`PreemptBatchTaskCancel` 在出网**之前**把任务抢占为 `cancelling`，
   失败路径不回滚 → 500 发生后管理面 `cancelling`、provider `in_progress` 三视图分裂；
2. 重试时抢占 `affected=0`（WHERE 显式排除 `cancelling`，`storage/rdb/batch/batch_task.go:90-92`）
   → `errBatchConflict` 409（`cancel.go:79-83`）。此时并无并发实例在处理，
   是"上一个失败请求的残留状态"——**假冲突**；
3. 对账职责② `advance.go:217-248`：`patch.Status = &batchResp.Status`（`:219`）无条件用
   provider 回报覆盖本地状态；`IsTerminalStatus`（`:240`）只挡终态，
   **没有 `cancelling` 不回退规则**——provider 滞后仍报 `in_progress` 时，本地 `cancelling`
   被降回 `in_progress`，用户已发出的取消意图被对账静默丢弃。这正是 issue 验收判据 3
   点名禁止的行为，也与 sys-design `批量任务与对账.md` §3.2 "终态不回退"的意图
   （`:49` "provider 返回状态原样存储、终态不回退"）同源：**cancel 意图是控制面断言的状态，
   不是 provider 回报的状态**。

## 3. 契约裁定

issue 标记"需要产品裁定"两项，本方案给出建议裁定与依据，随 DESIGN_GATE 确认：

### 裁定一：keyless provider 的 provider-native batch 必须可取消 —— **是（建议）**

依据：`providers.md:61` + `clusters.md:191` 明确 keyless 合法；`batches.md` §3.3 前置依赖只有
出网可达；且 keyless 是既定部署形态——issue 复现环境的受控后端
`fixture_mode: batch_v1` 无任何凭据字段，数据面 BFE 对 keyless cluster 转发本就不附加
上游鉴权（`bfe/bfe_modules/mod_ai_batch/mod_ai_batch_handlers.go:703-710` 的
`lastClusterKeyName` 允许 `keyName=""`，与数据面 `BATCH_TASK` 的 `key_name` 空值互相印证）。
控制面取消路径不带鉴权出网，与数据面上行链路语义一致。

→ 修复落在"取消/对账/结算路径兼容无 key"，**不**强制 provider 必须配置 key。
替代路径（修订 providers.md 强制 key）被否决：它会把一个已批准条款允许的合法配置判为
非法，与 #271 "条款是 Oracle、修代码不修条款"的处置原则一致。

### 裁定二：cancelling 重放 409 → 200 幂等 —— **是（建议）**

issue 验收判据 4："同一批次重复 cancel 幂等返回 200，不新增 release/charge/settlement
transition"。当前 409 来自 `batches.md` §3.3 处理逻辑 1 与 `批量任务与对账.md` §6.1 第 4 步、
§6.2 "cancel×cancel 由 DB 抢占互斥"的设计。409 对"已终态"是正确语义（条款保留）；
对"已 cancelling 的幂等重放"则是假冲突（issue 证据：补 key 重试先撞 409，误导排障）。
改为：终态 → 409 不变；已 cancelling → 200 幂等（重发 provider cancel 保证意图落地，
释放幂等 no-op）。OpenAI Batch API 重复 cancel 亦返回 200，语义对齐。

## 4. 影响面

- **接口**：`POST /open-api/v1/batches/{batch_id}/cancel`（500→200；cancelling 重放 409→200；
  终态重放 409 不变）；对账 job 行为（keyless 任务恢复推进/补漏/兜底结算；
  `cancelling` 不再被降级）。
- **消费方**：SC1401-TC026 `cancel-replay` 断言（修复后转 PASSED）；重试取消的运维脚本
  （409→200 需公告为"修复误导性错误码"而非破坏——200 是更宽的成功语义）。
- **数据面**：无改动。控制面 keyless 出网对齐 BFE 既有 keyless 转发语义。
- **DB/DDL/导出**：无改动；`PreemptBatchTaskCancel` SQL 不变（仍排除 cancelling），
  只在 model 层对 `affected=0` 增加状态判别。
- **版本**：不涉及接口版本升级。

## 5. 方案

### R1（主修复）：`resolveProviderAccess` 区分"未配置 key"与"key 配置异常"

`model/ibatch/cancel.go:153-188` 改为三分支：

```go
// provider 记录不存在 / 无可用实例 / 配置了 key 但无可用明文 → errProviderUnavailable（真数据异常）。
// keys 为空数组（providers.md:61 合法配置，clusters.md:191 "不配置 API-Key"）→ 不携带鉴权头出网。
access := &providerAccess{baseURL: baseURL, path: path}
if len(provider.Keys) > 0 {
    key := pickProviderKey(provider, keyName)
    if key == nil {
        return nil, errProviderUnavailable(providerName)
    }
    access.authHeader, access.authValue = iprovider.BuildAuthHeader("openai", key.Key)
}
return access, nil
```

配套：

1. `postProviderCancel`（`cancel.go:122`）与 `getProvider`（`advance.go:313`）加守卫
   `if access.authHeader != "" { req.Header.Set(...) }`——空 header 名会直接污染请求行，
   必须跳过而不是 Set 空值。`providerAccess` 注释补"`authHeader` 为空 = keyless，无鉴权出网"。
2. `pickProviderKey` 保持现有回退语义（keyName 匹配缺失回退第一个可用明文）——
   **有意偏离 issue 建议的"显式引用不存在 key_name 报配置错误"**：provider 更换 key 名后
   存量任务仍可取消，可用性优于严格性；全部 key 明文为空仍判数据异常（nil）。
3. 注释修正：`cancel.go:150-151` 与 `errors.go:43-44` 去掉"无可用 key = 控制面数据异常"的
   归类，改为"keys 为空不鉴权出网；配置了 key 但无可用明文才是数据异常"。

一处修复，四条链路（取消/状态推进/产物补漏/兜底结算）同时恢复（§2.2）。

### R2（同链缺陷）：对账"取消意图不回退" + 补发 healing

`model/ibatch/advance.go` `advanceOneTask`，在解析 batchResp 之后、组装 patch 之前
（`:217` 与 `:240` 终态防御之间）插入：

```go
if task.Status == TaskStatusCancelling && patch.Status != nil &&
    !IsTerminalStatus(batchResp.Status) && batchResp.Status != TaskStatusCancelling {
    // 取消意图不回退（issue #274 验收判据 3）：provider 滞后仍报
    // in_progress/queued/validating/finalizing 时，本地保持 cancelling；
    // 并补发一次 provider cancel（幂等）保证意图落地——覆盖"抢占成功但
    // 首次出网失败/进程崩溃"的空洞。失败 fail-open，下轮对账重试。
    if err := m.postProviderCancel(ctx, access,
        access.baseURL+access.path+"/batches/"+task.BatchID+"/cancel"); err != nil {
        stateful.AccessLogger.Warn("ibatch: reissue cancel failed, batch=%s: %v", task.BatchID, err)
    }
    patch.Status = nil // 状态不回退；request_counts/output_file_id/endpoint 照常刷新
}
```

语义边界：

- provider 报**终态**（`cancelled`/`completed`/`expired`/`failed`）→ 正常收敛、记
  `terminal_at`，与 §3.2 "cancel 后 provider 实际跑完按 completed 正常结算"一致，不受影响；
- provider 报 `cancelling` → 同值，天然无变化；
- `changed` 计算（`:221-222`）相应调整：status 被 hold 时 `changed` 只看其余字段，
  避免每 tick 空写 DB。

### R3（合同修订）：cancel 幂等重放 200

`model/ibatch/cancel.go:66-84` `affected=0` 分支，在返回 409 前增加状态判别
（Redis 回源补登重试逻辑 `:67-74` 保留不动）：

```go
// affected=0 区分"已终态"（409，条款保留）与"已 cancelling"（幂等重放 200）。
cur, ferr := m.FetchTask(ctx, batchID, productName, false) // DB 权威状态
if ferr == nil && cur != nil && cur.Status == TaskStatusCancelling {
    access, err := m.resolveProviderAccess(ctx, cur.Provider, cur.KeyName)
    if err != nil { ... return nil, err }                    // keyless 走 R1 不再失败
    if err := m.postProviderCancel(ctx, access, cancelURL); err != nil {
        ... return nil, err                                  // 出网失败按原映射（502/超时）
    }
    if m.redis != nil { _ = m.releaseReserve(ctx, batchID) } // 幂等：簿记已删则 no-op
    cur.Status = TaskStatusCancelling
    m.recordCancelOperation(ctx, batchID, TaskStatusCancelling, nil) // 审计"幂等重放"
    return cur, nil
}
// 终态/未知竞态 → errBatchConflict（409）
```

要点：

- 重放**重发** provider cancel 而非仅返回 200：覆盖"首次抢占成功但出网未落地"的崩溃空洞，
  provider cancel 幂等（OpenAI 重复 cancel 亦 200），重发安全；R2 的补发是对账侧兜底，
  两者叠加形成双保险。
- `FetchTask(..., false)` 取 DB 权威状态，避免 Redis 回源视图的滞后判读。
- 释放保留调用是幂等 no-op（簿记键已删，`releaseReserve:201-203` 直接返回），
  满足"不新增 release transition"。
- 惊群评估：`storage/rdb/batch/batch_task.go:76-79` 注释以"避免惊群重复出网"为由要求
  重放 409。改 200 后 N 个并发重放会产生 N 次幂等 cancel；cancel 出网廉价、重放由
  客户端重试驱动且并发度低，本期接受。如需严格单出网，后续可加 SETNX 互斥（见 §9 遗留）。

## 6. 行为变化表

| 场景 | 现状 | 修复后 |
|------|------|--------|
| keyless provider（`keys: []`）cancel | 500 `Biz Exception`，不出网，抢占残留 | 200 `cancelling`，无鉴权头出网（验收判据 1/2） |
| keyless 任务对账推进/产物补漏/兜底结算 | 每 tick Warn 空转，永不推进/结算 | 正常 GET/下载（无鉴权），全链路恢复 |
| 对账遇 `cancelling` + provider 报 `in_progress` | 降回 `in_progress`，取消意图丢失 | 保持 `cancelling` + 补发 cancel（验收判据 3） |
| cancel 重放（当前 `cancelling`） | 409 假冲突 | 200 幂等：重发 cancel，释放 no-op（验收判据 4） |
| cancel 重放（终态） | 409 | 409（不变） |
| 配置 key 的 provider cancel | 携带 `Authorization: Bearer <key>` | 不变（验收判据 5） |
| provider 不存在 / 无实例 / key 明文全空 | 500 | 500（不变，真数据异常） |
| `A-upload-create` / `A-state-accounting` / `cleanup-residue` | PASSED | PASSED（不变，验收判据 5） |

## 7. 测试

### 7.1 单测（`model/ibatch`）

1. `resolveProviderAccess` 表驱动：`keys=nil` / `keys=[]` → 无错误且 `authHeader==""`；
   `keys` 非空 + keyName 空/命中/缺失回退 → 带对应 key；`keys` 非空但明文全空 →
   `errProviderUnavailable`；provider 不存在 / 实例全禁用 → 同前。
2. `postProviderCancel` / `getProvider`：`authHeader==""` 时 httptest 服务端断言请求**不含**
   `Authorization`/`x-api-key`；非空时断言头值（防"Set 空 header 名"污染请求）。
3. `Cancel` keyless：provider keys 为空 → 200，`status=cancelling`，httptest 收到无鉴权
   POST `/v1/batches/{id}/cancel`（**当前 HEAD 此用例必须失败 = 复现 issue**）。
4. `Cancel` 幂等重放：种子任务已 `cancelling` → 200 且 httptest 再次收到 cancel POST；
   已 `completed` → 409（防"终态也 200"回归）。
5. `advanceOneTask` 意图保持：本地 `cancelling` + provider 返回 `in_progress` →
   patch 不写 status、收到补发 cancel POST、返回不 changed（仅状态维度）；
   本地 `cancelling` + provider 返回 `cancelled` → 收敛终态、`terminal_at` 记值。

```bash
go build ./... && go vet ./...
go test ./model/ibatch/...
```

### 7.2 集成测试（`test/integration/tests/batch/`，编号按 `design.md` §6 登记）

框架沿用 batch 模块现状：真实二进制子进程 + `testutil.StartServerAuto`（SQLite / MySQL
双后端），种子直写 `batch_tasks`，httptest 假 provider（复用 BT-3-006 的准备法）。

| 编号 | 场景 | 断言要点 |
|------|------|----------|
| BT-3-009 | keyless provider cancel（provider keys 置空） | 200 + `status=cancelling`；假 provider 收到 POST 且**无 Authorization 头**；Redis 预留已释放——判据 1/2 的本地回归闸门（HEAD 上必须失败） |
| BT-3-010 | cancelling 重放幂等 | 同批第二次 cancel → 200（非 409），假 provider 收到第二次 cancel，金额/结算字段无新增 transition——判据 4 的本地回归闸门 |
| BT-3-011 | 终态重放回归 | `cancelled` 任务 cancel → 409——防"R3 把终态也放成 200" |
| BT-5-00x（job 侧，落地时登记） | 对账意图保持 | 种子 `cancelling` + 假 provider 返回 `in_progress` → job 跑一轮后 DB 仍为 `cancelling` 且假 provider 收到补发 cancel；随后假 provider 转 `cancelled` → 收敛终态——判据 3 的本地回归闸门 |

运行：`bash test/scripts/run_module_tests.sh batch`（SQLite）；
`AIAPI_MYSQL_DSN=<admin-dsn>` 同命令跑 MySQL。用例行与断言要点同步登记进
`tests/batch/design.md` §4.3/§4.5 用例表。

### 7.3 系统级判据（issue 给定）

keyless provider 下 `in_progress` 批次 cancel → 200 `cancelling`；provider 收到一次无鉴权
cancel 并转 `cancelled`；管理面 / Redis / 数据面三视图一致收敛；重复 cancel 幂等 200；
SC1401-TC026 `TestSC1401TC026_BatchPassthroughAccounting/cancel-replay` 转 PASSED，
其余子断言不回退。

整体回归：`make test`。

## 8. 文档与契约同步（Step 3/4，条款明细见 design-changes.md）

- `batches.md` §3.3：前置依赖补 keyless 语义（"provider `keys: []` 时取消请求不携带鉴权头"）；
  处理逻辑 1 改为"affected=0 → 取当前状态：终态 → 409；已 `cancelling` → 200 幂等重放
  （重发 cancel，释放幂等）"；错误码 409 行修订为"任务已终态"。
- `批量任务与对账.md`：§3.2 状态机补"cancelling 不回退（取消意图是控制面断言状态）"；
  §5 职责② 补"意图保持 + 补发 cancel"；§6.1 第 2 步（keyless 取 key）与第 4 步
  （409/200 判别）；§6.2 cancel×cancel 行改写。
- `storage/rdb/batch/batch_task.go:74-94` 注释同步：`affected=0` 语义细分
  （终态/受理中由 model 层判别，存储层维持"仅首个转换者成功"不变）。
- `model/ibatch/cancel.go:150-151`、`errors.go:43-44` 注释同步（keyless 合法化表述）。

## 9. 实施状态与遗留

**实施状态：已于 2026-10-23 按 §5 落地**（六步法 Step 3-5，记录见 §10）。修复前基线
`0d9e2f1`；回归闸门（§10.3）证明三个关键用例在旧代码上稳定复现 issue。

遗留 / 待确认：

1. **惊群互斥（可选硬化）**：R3 后并发重放各自出网一次幂等 cancel。如需严格"单出网"，
   可给 cancel 出网加 `SETNX BATCH_CANCEL_LOCK:<batch_id>` 互斥（TTL 覆盖 provider 超时），
   另起小卡，不阻塞本期。
2. **keyName 悬空语义**：provider 更换 key 名后，存量任务的 `key_name` 匹配缺失——
   本期保持"回退第一个可用 key"（可用性优先）；是否改为配置错误待运营观察
   （本期有意不采纳 issue 的"显式引用不存在即报错"建议，理由见 §5.R1.2）。
3. **BFE `BATCH_TASK` 不写 `provider` 列**（`redis_view.go:73-77` 以 `cluster` 回退）：
   兼容层工作正常，非缺陷，不动。
4. **降级部署**（无出网，`providerQuerier=nil`）：`resolveProviderAccess:154-156` 仍返回
   `errProviderUnavailable` → cancel 500。与 §3.3"降级部署本接口退化为不可用"条款一致，
   维持现状；如需 503 语义另起小卡。
5. issue 提到的"对账降级是否单开 issue"：不单开，R2 直接覆盖（验收判据 3 要求）。

## 10. 六步法核对（方案阶段）

- [x] modifications 目录已创建（Step 1，`2026-10-23-issue-274-batch-cancel-keyless-provider/`）
- [x] 变更说明已编写（Step 2，本文档 + design-changes.md）
- [x] api-define 更新（Step 3，`batches.md` §3.3 前置依赖/处理逻辑 1/错误码表）
- [x] sys-design 更新（Step 4，`批量任务与对账.md` §3.2/§5 职责②/§6.1/§6.2 与存储层注释）
- [x] 代码按设计实现并通过测试（Step 5，§10.3 验证结果）
- [x] details 沉淀评估（Step 6）：R2 的"控制面断言状态不回退"规则已直接沉淀进
      `批量任务与对账.md` §3.2/§5，无需新建 details 文档

## 11. 实施记录（2026-10-23，六步法 Step 3-5）

代码、单测、集成测试与文档已按 §5 落地。

### 11.1 改动文件（与 §5/§7 方案对照）

| 文件 | 动作 | 说明 |
|------|------|------|
| `model/ibatch/cancel.go` | 修改 | `resolveProviderAccess` 三分支（R1，keyless 放行）；`postProviderCancel` 空 header 守卫；`Cancel` affected=0 状态判别 + 新增 `cancelReplay`（R3）；头部流程注释同步 |
| `model/ibatch/advance.go` | 修改 | `getProvider` 空 header 守卫；`advanceOneTask` 取消意图保持 + 补发幂等 cancel（R2）；`changed` 计算剔除被 hold 的状态 |
| `model/ibatch/errors.go` | 修改 | `errProviderUnavailable` 注释：适用面收窄，keyless 合法化（仅注释） |
| `storage/rdb/batch/batch_task.go` | 修改 | `PreemptBatchTaskCancel` 注释：affected=0 语义细分（SQL 未动） |
| `model/ibatch/cancel_test.go` | 修改 | +`TestCancel_KeylessProvider` / `TestCancel_ReplayIdempotent` / `TestCancel_ReplayTerminalConflict` / `TestResolveProviderAccess_KeylessAndBranches`；夹具参数化 `setupCancelFixtureWithKeys` |
| `model/ibatch/advance_test.go` | 修改 | +`TestAdvanceStatus_KeylessProviderNoAuthHeader` / `TestAdvanceStatus_CancellingIntentHold` / `TestAdvanceStatus_CancellingConvergesToCancelled`；夹具参数化 `setupAdvanceFixtureWithKeys` |
| `test/integration/tests/batch/cancel/cancel_test.go` | 修改 | BT-3-003 由"cancelling → 409"改写为"cancelling 重放 → 200 幂等"（R3 合同变更）；+BT-3-009 keyless cancel 200 无鉴权头 |
| `test/integration/tests/batch/cancel/concurrency_mysql_test.go` | 修改 | **BT-3-007 按 issue #274 契约改写**：20 并发 = 1 抢占受理 + 19 幂等重放 → 全 200、upstream 收 20 次幂等 cancel、20 条成功审计（原契约 1×200+19×409+恰好 1 次出网）——方案未列出的受影响用例，见 §11.2.1 |
| `test/integration/tests/batch/design.md` | 修改 | §2.1 目标表、§3 目录清单、§4.3 用例表（BT-3-003/007 改写 + BT-3-009 登记） |
| `design-docs/api-define/.../batches.md`、`sys-design/details/批量任务与对账.md` | 修改 | Step 3/4 条款同步（§8 清单） |

### 11.2 与方案的偏差（实施中确认）

1. **BT-3-007 并发契约改写**（方案未列出）：R3 使"cancelling 重放"由 409 改为 200 并重发
   cancel，该 MySQL 并发用例原契约（1×200 + 19×409 + upstream 恰好 1 次出网）不再成立。
   按新契约改写并保留 //go:build mysql 门控。并发下抢占 affected=0 ⇒ 行已是 cancelling
   （否则 UPDATE 会命中受理），故全部调用确定性地走 200——无中间态竞态。
2. **pickProviderKey 语义边界**：按名命中但明文为空的 key 会透传（既有语义，OpenAPI 层
   校验保证 key 非空，正常路径不会出现）。单测"配置了 key 但无可用明文 → 500"据此按
   keyName 不匹配/为空的路径钉死（首版用例误按名命中空明文，运行失败后修正）。
3. **job 侧对账意图保持无集成用例**：batch 模块无 job 集成设施（职责② 由服务端调度器
   驱动，集成环境无可触发 harness），由单测 `TestAdvanceStatus_CancellingIntentHold` /
   `TestAdvanceStatus_CancellingConvergesToCancelled` 覆盖；方案中的 BT-5-00x 不落地。
4. **BT-3-010/011 编号归并**：幂等重放集成用例即改写的 BT-3-003；终态重放 409 回归由
   既有 BT-3-002（completed → 409）+ 单测 `TestCancel_ReplayTerminalConflict` 双锁。

### 11.3 验证结果

| 验证项 | 结果 |
|--------|------|
| `go build ./... && go vet`（主模块） | 通过 |
| `go test -coverprofile ./model/...` | 通过，model 覆盖率 **85.0%** ≥ 70% 门槛 |
| `go test ./...`（主模块全量单测） | 通过（EXIT 0） |
| `go test ./tests/batch/...`（SQLite 集成：含改写 BT-3-003、新增 BT-3-009） | 全部 ok（EXIT 0） |
| **本地回归闸门（复现实验）**：`git worktree` 检出基线 `0d9e2f1`（旧代码）+ 新测试 | `TestCancel_KeylessProvider` / `TestCancel_ReplayIdempotent` / `TestAdvanceStatus_CancellingIntentHold` **全部 FAIL**（复现 issue）；修复后全部通过——闸门成立 |
| **集成复现实验**：旧二进制（10-23 09:30 构建）跑新用例 | BT-3-009 实测 500 `batch provider ... unavailable`（与 issue 报告一致），BT-3-003 重写版失败；新二进制构建后全部通过 |
| MySQL 双后端（`AIAPI_MYSQL_DSN`） | 本机未设 DSN，BT-3-007 未执行；SQLite 默认后端全过 |

注：集成测试使用工程根 `ai-gateway-api.exe`（`testutil` 要求先构建），实施中已用
`go build -o ai-gateway-api.exe .` 重建；该二进制为 gitignore 产物，不入库。

### 11.4 验收判据对照（issue 给定）

| 判据 | 落点 | 状态 |
|------|------|------|
| 1. keyless cancel → 200 cancelling | R1 + BT-3-009 / `TestCancel_KeylessProvider` | ✅ |
| 2. provider 收到无鉴权 cancel 并转 cancelled | BT-3-009 无 Authorization 断言；cancelled 收敛由对账 `advanceOneTask` 终态路径（既有逻辑）+ `TestAdvanceStatus_CancellingConvergesToCancelled` | ✅ |
| 3. 三视图一致，对账不得降回 in_progress | R2 + `TestAdvanceStatus_CancellingIntentHold` | ✅ |
| 4. 重复 cancel 幂等 200，无新增 release/charge | R3 + BT-3-003 + `TestCancel_ReplayIdempotent`（释放幂等 no-op） | ✅ |
| 5. 相邻行为不回归 | `go test ./...` 全量 + batch 集成模块 + BT-3-007 并发改写 | ✅（MySQL 门待用例待 DSN 环境复跑） |
