# BFE 条件原语 time_weekday_in 支持——变更摘要

日期：2026-10-14　状态：**已完成**（控制面依赖已升级并通过单测；实现记录见 §7；前端面板按钮属另一仓库，本次未做）

## 1. 背景

BFE 数据面新增条件原语 `time_weekday_in(weekday_list[, zone])`：判断当前时间的星期是否属于给定集合
（`0`=周日、`1`=周一、…、`6`=周六，与 Go `time.Weekday` 及 cluster 分时段计价的 `weekdays` 约定一致）。
`zone` 为可选军事时区字母（`"H"`=UTC+8 等），**省略时按 `Z`（UTC）判定**；时间取进程系统时钟，
或请求头 `X-Bfe-Debug-Time`（便于复现任意星期）。

- 数据面提交：`bfe` `831135f2`（2026-10-14，已推送 `origin/v1.8.9-dev`）；
- BFE 侧修改说明：`bfe/docs/zh_cn/modifications/2026-10-14-time-weekday-in-primitive/design-changes.md`。

**控制面必须联动的原因**：控制面在保存/校验规则时会**真实编译** BFE 表达式，该编译能力来自 BFE 的
`bfe_basic/condition` 包，而该依赖经 `go.mod` 的 `replace` 指向本 fork 的**某次快照**。依赖不升级时，
用户提交含 `time_weekday_in` 的表达式会在控制面即被拒绝（`invalid route rule Cond: primitive time_weekday_in not found`），
规则根本到不了 BFE。

编译入口（全量 grep，非测试代码）：

| # | 位置 | 作用 | 触达的用户入口 |
| --- | --- | --- | --- |
| 1 | `lib/validate/validate.go:570-575`（`ConditionExpression` → `condition.Build`） | 保存前校验条件表达式 | `RouteRules`（`:599`）、`AICacheRules`（`:729`）、`AIContextRules`（`:836`）、`TrafficMirrorRules`（`:972`） |
| 2 | `model/route_rules/route_rules.go:351`（`RouteRulesManager.validateRouteRules`） | 路由表批量校验 | 路由表管理 |
| 3 | `model/iroute_conf/route_rule.go:226-229`（`ExpressionVerify` → `condition.Build`） | `/expression/verify` | OpenAPI `PATCH /expression/verify`（前端"校验表达式"） |

当前依赖快照：

```
go.mod:8    github.com/bfenetworks/bfe v1.8.8
go.mod:83   replace github.com/bfenetworks/bfe => github.com/mileszhang2016/bfe v1.4.1-0.20261006053058-d12472ad13a3
go.mod:85   // 本地联调时可临时切换：replace github.com/bfenetworks/bfe => ../bfe
```

即控制面实际编译的是 **2026-10-06** 的 fork 快照，其中不存在 `time_weekday_in`。

## 2. 目标

1. 控制面可校验并保存含 `time_weekday_in` 的条件表达式（覆盖 §1 表中全部入口）；
2. **零业务代码改动**：控制面刻意不做原语白名单（`lib/validate/validate.go:811-816` 的函数注释明确
   "编译留在 BFE 侧，以避免与原语版本耦合"），因此只需版本对齐；
3. 接口契约、导出格式、数据库结构均不变；
4. 补校验用例锁定行为，防止后续依赖回退时无人察觉。

## 3. 范围

- **涉及**：`ai-gateway-api`（`go.mod` / `go.sum` + 校验单测）；
- **不涉及**：`bfe`（已完成）、`conf-agent`、DDL、OpenAPI / InnerAPI 契约 —— 表达式以字符串形式随规则
  原样下发，导出链路无字段变化；因此本次**不产生 `api-changes.md`**（无接口变更）、**不产生 `design-changes.md`**
  （无方案分支）；
- **同迭代建议**：`ai-gateway-web` 表达式面板新增按钮（`src/components/Expression/ExpressionConfig.js` 的
  `system` 分组，现仅 `bfe_time_range` / `bfe_cluster_in` 两条），便于运营发现该原语；示例统一用两参形式
  （`time_weekday_in("0|6", "H")`）以避免误按 UTC 判定。

## 4. 关键决策

| 决策 | 说明 |
| --- | --- |
| 仅升级依赖，不在控制面登记原语白名单 | 与 `validate.go:811-816` 既有约定一致：原语清单只维护在 BFE 侧，避免两处清单漂移与版本耦合 |
| 通过 `replace` 指向 fork 新提交，不改 `require` 版本 | 沿用仓库既有做法（`go.mod:83-85`）；`require` 保持 `v1.8.8`，由 `replace` 覆盖 |
| 校验用例进 `TestConditionExpression` | 沿用 `lib/validate/validate_test.go:322-350` 的表驱动风格 |
| 不放宽任何校验 | 非法取值仍由 BFE 侧 `condition.Build` 在保存期拦截并返回 422（参数错误），控制面不加额外规则 |
| 不做数据面版本探测 | 控制面不感知 BFE 运行版本；版本对齐由上线顺序保证（见 §7） |

## 5. 关联文档

- BFE 侧修改说明：`bfe/docs/zh_cn/modifications/2026-10-14-time-weekday-in-primitive/design-changes.md`
- BFE 侧设计文档与联动方案：`document-ai-gateway/迭代系统设计/v0.8/增加判断weekday的原语/`
  （`time_weekday_in原语-BFE设计方案.md`、`time_weekday_in原语-控制面与前端联动方案.md`）
- BFE 原语文档：`bfe/docs/zh_cn/condition/system/time.md` 的 `time_weekday_in` 小节

## 6. 实施阶段

| 阶段 | 内容 | 状态 | 关键文件 |
| --- | --- | --- | --- |
| 1 | BFE 数据面实现（原语 + 单测 + 文档 + 集成场景 SC32） | ✅ 已完成（提交 `831135f2`，已推送 origin） | `bfe/bfe_basic/condition/{primitive.go,build.go,parser/semant.go}` |
| 2 | 控制面依赖升级到含该原语的提交 | ✅ 已完成（`v1.4.1-0.20261014135930-831135f2bfa5`） | `ai-gateway-api/go.mod:83`、`go.sum` |
| 3 | 校验单测（5 条用例）与接口漂移配套 | ✅ 已完成（另需给 `model/ibatch` 测试 mock 补 `HGetAll`，见 §7） | `lib/validate/validate_test.go`、`model/ibatch/redis_adapter_test.go` |
| 4 | 前端表达式面板按钮（可选，建议同迭代） | 🔄 待实现（另一仓库，本次未做） | `ai-gateway-web/src/components/Expression/ExpressionConfig.js` |

阶段 2 的操作口径：

```bash
cd ai-gateway-api
go mod edit -replace github.com/bfenetworks/bfe=github.com/mileszhang2016/bfe@831135f2
go mod tidy     # 生成新的伪版本号并更新 go.sum
# 本地联调可临时改用 go.mod:85 注释的 ../bfe，勿提交
```

阶段 3 的用例（追加到 `TestConditionExpression` 的 `cases`）：

| name | cond | wantErr |
| --- | --- | --- |
| `weekday_in with zone` | `time_weekday_in("0\|6", "H")` | false |
| `weekday_in default zone` | `time_weekday_in("1")` | false |
| `weekday_in combined` | `time_weekday_in("0\|6", "H") && bfe_periodic_time_range("090000H", "180000H", "")` | false |
| `weekday_in invalid weekday` | `time_weekday_in("7")` | true |
| `weekday_in invalid zone` | `time_weekday_in("0", "QQ")` | true |

## 7. 实现结果（2026-10-14 完成）

**实现方式**：与 §4 的决策一致——只升级依赖，不在控制面登记原语白名单。

| 改动文件 | 内容 |
| --- | --- |
| `go.mod:83` | `replace github.com/bfenetworks/bfe => github.com/mileszhang2016/bfe v1.4.1-0.20261014135930-831135f2bfa5`（`require v1.8.8` 不变） |
| `go.sum` | 新增该伪版本的 `h1:` 与 `/go.mod` 两行（旧伪版本两行移除） |
| `lib/validate/validate_test.go` | `TestConditionExpression` 新增 5 条用例（见 §6 表） |
| `model/ibatch/redis_adapter_test.go` | `fakeRawRedisClient` 补 `HGetAll` 空实现（**接口漂移配套**，见下） |
| `design-docs/sys-design/details/条件原语校验与BFE依赖版本对齐.md` | 新增（Step 6 沉淀），并在 `sys-design/summary.md` 登记索引 |

**接口漂移配套（bump 的必做项，非本次目的的附加改动）**

新依赖的 `bfe_util/redis_client.Client` 在 `client.go:48` 新增 `HGetAll(key string) (map[string]string, error)`，
旧快照（2026-10-06）无此方法；控制面测试 mock `fakeRawRedisClient` 因此编译失败
（`does not implement redis_client.Client (missing method HGetAll)`）。已核实：

- **由本次 bump 引入**：用备份的旧 `go.mod`/`go.sum` 跑 `go test ./model/ibatch/...` 通过，
  且旧依赖 `client.go` 中 `HGetAll` 计数为 0；
- **生产代码零改动**：`RedisClientAdapter.HGetAll(ctx, key)` 走 Lua 脚本（`luaAdapterHashGetAll`），
  不调用原始接口方法，故行为不变，仅需给测试 mock 补 stub。

**验证结果**

| 项 | 命令（等价 Makefile target） | 结果 |
| --- | --- | --- |
| 能力验证（本次核心） | `go test ./lib/validate/ -run TestConditionExpression -v` | **15 个子用例全 PASS**（含新增 5 条）→ 升级后控制面可编译 `time_weekday_in` |
| 全仓编译 | `go build ./...` | 通过 |
| 静态检查 | `go vet ./...` | 通过（exit 0） |
| 全量单元测试 | `go test -gcflags="-N -l" -v -cover $(go list ./... \| grep -vE vendor)`（等价 `make test`） | **96 包全通过、0 失败（exit 0）** |
| model 覆盖率门禁 | `go test -gcflags="-N -l" -coverprofile=… ./model/...` + `go tool cover`（等价 `make test-model-cover-gate`） | **85.1% ≥ 70%，通过** |
| 相关包回归 | `go test ./model/ibatch/...` | 通过（对照：修复前 `[build failed]`） |

**环境说明（供后续排查参考）**：`gofmt -l` 在本仓工作区会标记**几乎所有** `.go` 文件——根因是
`core.autocrlf=true` 使工作区为 CRLF，与格式化无关（未被我改动的同目录文件同样被标记）；
本次两处代码改动的 `git diff --numstat` 分别为 `5/0` 与 `3/0` 行，未改动行尾。
另：`go mod tidy` 的失败曾被管道 `$?` 掩盖（首次 sumdb 404 时误判为成功），判断成功应以
**go.sum 是否出现新伪版本条目**为准。

**Step 3 / Step 4 的 review 结论**

- **Step 3（api-define）无需改动**：`api-define/OpenAPI接口定义/expression-verify.md` 仅给出表达式示例，
  不构成原语清单；本次无接口路径/方法/参数/字段/错误码变化，兼容性不受影响；
- **Step 4（sys-design）无需改动**：`sys-design/` 各文档无原语清单，本次亦无数据模型/流程/算法变化；
  机制类知识按 Step 6 沉淀为新的 details 文档（见上表末行）。

**上线顺序与验收口径**：① BFE（已完成推送）→ ② 本变更 → ③ `ai-gateway-web`。
升级后 `ConditionExpression("time_weekday_in(\"0|6\", \"H\")")` 返回 `nil`；`time_weekday_in("7")`、
`time_weekday_in("0", "QQ")` 返回参数错误；`/expression/verify` 对合法表达式返回校验通过。

**回滚**：回退依赖版本即可（回退期间该类表达式的**新增保存**会被拒，存量配置不受影响）；
回退 BFE 镜像前须先清理引用 `time_weekday_in` 的规则配置。

**仍待办理**：`ai-gateway-web` 表达式面板按钮（阶段 4，另一仓库，本次未做）；本仓库改动尚未提交。