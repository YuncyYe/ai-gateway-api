# Issue #276：批次控制面 `product_name` 契约与认证设计互斥——修复方案（方案 A′：契约剔除）

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/276](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/276)

> 批次控制面 `product_name` 的来源在三处声明中互不一致：API 文档要求认证中间件从调用凭证解析并强制注入，认证设计明确已移除 Product Scope 且 Token 不再绑定产品线，实现则以默认产品线兜底并在注释中承认该注入尚未实现。
>
> 因此按 `batches.md` §1 编写的调用方会误以为存在产品线隔离保障，而依赖该条款的 TC077（`A-01` 跨产品线按 404、`requires.two-isolated-product-identities`）无法构造前置身份，状态机停在 `DEFERRED/BLOCKED_ENVIRONMENT`。

Issue 结论：**需要产品裁定**——`batches.md` §1 是作废并改写为默认产品线语义（方案 A，issue 推荐），还是后续重新引入 Product Scope（方案 B）。

**本方案经产品 owner 裁定（2026-10-23）**：采纳方案 A 方向并升级为 **方案 A′（契约剔除）**——不只改写表述，而是从本组接口的**入参与返回值中彻底删除 `product_name`**：`product_name` 对 Open API 调用者不可见，实现内部固定使用默认产品线（`AIRouteInnerProductName`，测试环境即 `AI_product`）做查询过滤。

## 2. 根因核实（已对当前检出逐环直读核实，2026-10-23）

Issue 证据链全部属实，关键事实复核如下：

1. **冲突条款确实存在**：`design-docs/api-define/OpenAPI接口定义/batches.md` 共 6 处 `product` 相关条款——§1 全局约定（`:17`「由认证中间件从调用凭证解析并**强制注入**为查询条件……所有查询仅返回调用方所属产品线的数据」）、§2.1 字段表（`:81`「中间件强制注入，只读」）、§3.1 列表约束（`:162`）、§3.2 详情错误码（`:285`「404（……或任务不属于调用方产品线）」）、§3.4 文件查询约束（`:395`）与错误码（`:418`）。
2. **认证设计已移除该前提**：`design-docs/sys-design/details/认证授权机制.md` §2.3（`:51`「已移除 `Product` Scope」）、§4.4（`:209-210`）、§5.3（`:248` `McProductProbe` 仅解析 URL Path 变量）；变更单 `design-docs/modifications/2026-07-27-openapi-optimize/design-changes.md` §2.4（`:147`、`:156-160`、`:173`）——Token `scope` 仅 `System`/`Support`，`product_name` 字段删除，属 approved/normative 状态。
3. **注入链路在本组路由不存在**：`endpoints/openapi_v1/batches/endpoints.go:35-61` 四条路由（`/batches`、`/batches/{batch_id}`、`/batches/{batch_id}/cancel`、`/batch-files/{file_id}`）均无 `product_id`/`product_name` 路径变量，Authorizer 为 `iauth.FA` 而非 `iauth.FAP`；`endpoints/middleware/product_probe.go:38-61` 两变量皆空即 `return req, nil`；`endpoints/middleware/user_probe.go:26-48` 只写 `VisitorContext`，不写 `ProductContext`。
4. **实现以默认产品线兜底**：`endpoints/openapi_v1/batches/product.go:26-49` `resolveProduct` 先 `ibasic.MustGetProduct`，失败回退 `stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（注释已按本卡校准为最终口径）。
5. **`product_name` 当前实际暴露在返回值中**：`endpoints/openapi_v1/batches/view.go:32` `batchTaskView.ProductName`（列表/详情/取消三端点共享视图）；`endpoints/openapi_v1/batches/file.go:47` 直出内部实体 `*ibatch.BatchFile`（含 `product_name` 及其他内部字段）。已知消费方：ai-gateway-web 批量任务详情抽屉展示 `detail.product_name`（`ai-gateway-web/src/modules/BatchTasks/components/BatchView.vue:36-37`）。
6. **查询按 `product_name` 强制过滤**：`model/ibatch/manager.go:171-174` `FetchTask`/`FetchFile`/`Cancel` 均以 `product_name` 为强制条件（Redis 回源路径经 apiKey 解析校验 product_name）；列表 `endpoints/openapi_v1/batches/list.go:47,65` 同样以 `resolveProduct` 结果为过滤条件。数据面写入侧 `product_name` 经 api_key 解析补齐（`design-docs/sys-design/details/批量任务与对账.md:31`）。
7. **后果**：契约层面给出虚假隔离承诺；TC077 `A-01` 与 `two-isolated-product-identities` 无实现依据，长期 `BLOCKED_ENVIRONMENT`；若数据面记录带非默认产品线，控制面固定按默认产品线过滤，这些任务在列表/详情/取消/文件查询中不可见或 404（issue 标为【推断待确认】，方案 A′ 下转为显式文档化的已知限制，见 §6.5）。

**根因**：`batches.md` §1 在 `140b626`（2026-10-07「批量与异步任务支持（一期）」）引入，**晚于** 2026-07-27「彻底移除 Product Scope」决策，却仍按旧模型撰写；此后 #249 / #254 / #271 三卡只修实现，未回改 §1。

## 3. 影响面

| 项目 | 结论 |
|------|------|
| 接口范围 | `/open-api/v1/batches` 列表/详情/取消、`/open-api/v1/batch-files/{file_id}` 四条路由的**响应形状**（`product_name` 键剔除）与契约表述 |
| 运行时行为 | 响应变化：四端点返回值不再含 `product_name`；查询过滤语义不变（内部仍按默认产品线过滤） |
| 调用方 | **ai-gateway-web** 批量任务详情抽屉（`BatchView.vue:36-37` 展示 `detail.product_name`，剔除后取不到值）——需 web 侧同步删除展示行，记入联动跟进；四端点为 2026-10-07 新增，无其他已知消费方 |
| 测试资产（本仓库） | `tests/schema/openapi/batch.go` 契约锁、`detail_test.go:126` / `file_test.go:83` 取值断言需改为**键缺席断言**；BT-1-002 / BT-2-003 / BT-4-004 过滤断言不变 |
| 测试资产（外部） | `integration-test` 仓库 SC1301-TC077（`A-01` 断言、`two-isolated-product-identities` capability），需修订 |
| 数据/配置/数据面 | 无 DDL 变更、无配置变更、无 BFE / conf-agent 联动；`batch_tasks` / `batch_files` 表 `product_name` 列保留（内部过滤仍依赖） |

## 4. 目标

消除 `batches.md` 与 `认证授权机制.md` / 2026-07-27 变更单之间的互斥表述，使 `product_name` 对 Open API 调用者**完全不可见**（不入参、不返回）；实现内部固定按默认产品线过滤；解除 TC077 的前置身份阻断。响应形状变化仅限「剔除 `product_name` 键」，其余字段语义不变。

## 5. 方案裁定：A vs B，及 A 升级为 A′

| 维度 | 方案 A′：契约剔除 `product_name`（产品 owner 裁定，2026-10-23） | 方案 B：重新引入 Product Scope |
|------|------|------|
| 与已批准决策的关系 | 与 2026-07-27 变更单（approved/normative）**一致**，且暴露面最小 | 需**推翻**已批准的权限模型收敛决策 |
| 契约形态 | `product_name` 不出现在入参/返回值；无「只读字段」继续暗示多产品线概念 | 恢复凭证→产品线绑定与注入链路 |
| 改动面 | 文档 8 处 + 视图层 2 处（`view.go` 删字段、`file.go` 视图化）+ 本仓库测试断言/schema | 认证模型、Token 结构、McUserProbe 注入、四条路由补 `FAP`/路径变量、API 重暴露产品线绑定接口、测试资产重建 |
| 回归风险 | 响应少一个键（web 详情抽屉一处展示联动） | 触碰全部 OpenAPI 鉴权链路 |
| 未来恢复路径 | `user_products` 表与 `FAP` 机制保留在代码中（`model/iauth/authorization.go:159`、`authentication.go:585`），§2.4.4 已预留重新引入路径 | —— |

**裁定结论：方案 A′。** 相对 issue 推荐的方案 A（文档化为「服务端解析、只读」字段），产品 owner 明确 `product_name` 对 Open API 调用者应不可见——只读字段仍会向调用者暴露内部租户概念，剔除才是与「单产品线视角」自洽的最小契约。

## 6. 最终方案（方案 A′）

### 6.1 `batches.md`：契约剔除

- **§1 全局约定（`:17`）** 改写为：

  > - **产品线口径**：本组接口为**单产品线视角**，`product_name` **不出现在本组接口的入参与返回值中**——服务端内部按配置项 `AIRouteInnerProductName` 对应的默认产品线过滤查询（解析逻辑见 `endpoints/openapi_v1/batches/product.go` 的 `resolveProduct`）；数据面经 api_key 解析落入其他产品线的任务/文件，对本组接口不可见（按不存在处理），见 §6「已知限制」。

- **§2.1 BatchTask**：JSON 示例删 `"product_name": "AI_product",` 键；字段表删 `product_name` 整行。
- **§3.1 列表**：成功示例删 `product_name` 键；约束块删「`product_name` 由……不接受外部传参」整条（该字段已非接口参数/返回的一部分）。
- **§3.2 详情**：成功示例删 `product_name` 键；错误码 `:285` 维持「404（DB 与 Redis 均不存在该任务）」（跨产品线语义已删）。
- **§3.3 取消**：成功示例删 `product_name` 键。
- **§3.4 文件查询**：约束改写为「文件按服务端默认产品线过滤；非默认产品线的文件按不存在处理（404，见 §6「已知限制」）」；错误码 `:418` 维持「404（`(file_id, provider)` 不存在）」。
- **§6 已知限制** 改写为：

  > - **已知限制（issue #276，2026-10-23 校准）**：v1 批次控制面为**单产品线视角**——`product_name` 为内部字段，不出现在本组接口的入参与返回值中；服务端内部按默认产品线 `AIRouteInnerProductName` 过滤。Token 不绑定产品线（见 `design-docs/sys-design/details/认证授权机制.md` §2.3 与 `design-docs/modifications/2026-07-27-openapi-optimize/design-changes.md` §2.4）。数据面经 api_key 解析落入其他产品线的任务/文件，对本组接口不可见（按不存在处理）。该语义的回归用例见 `test/integration/tests/batch/`（BT-1-002 / BT-2-003 / BT-4-004 过滤口径；BT-2-002 / BT-4-001 键缺席口径）。若未来重新引入 Product Scope，本组接口的契约按届时设计另行修订。

### 6.2 代码改动（响应形状变化）

- `endpoints/openapi_v1/batches/view.go`：`batchTaskView` 删除 `ProductName` 字段（`:32`）与 `newBatchTaskView` 中对应赋值（`:68`），doc 注释标注 issue #276 契约剔除——**列表/详情/取消三端点响应同步变化**；
- `endpoints/openapi_v1/batches/file.go`：不再直出内部实体，改返回剔除 `product_name` 的视图（其余字段暂维持现状——`direction` / `api_key_id` / `lines` / `key_name` 等内部字段外泄是 issue #249 已登记的遗留，不在本卡扩面）；注释「product_name 强制隔离」同步清理；
- `endpoints/openapi_v1/batches/list.go:45`：注释「product_name 强制取中间件注入值」改为默认产品线口径；
- `endpoints/openapi_v1/batches/product.go:26-32`、`endpoints.go:15-18` 包 doc、`model/ibatch/manager.go:181-183`：注释已按本卡前轮工作校准为最终口径，无需再动。

### 6.3 测试资产（本仓库，断言级变更）

- `tests/schema/openapi/batch.go`：`BatchTaskDetailSchema` 必需键删 `product_name`（`:47`）、类型表删 `product_name` 条目（`:61`）；
- `tests/batch/detail/detail_test.go:126`：`assert.Equal(t, helper.TestProductName, data["product_name"])` 改为**键缺席断言**（issue #276 契约剔除，`product_name` 不得出现在详情响应）；
- `tests/batch/file/file_test.go:83`：同上改为键缺席断言；
- `tests/batch/design.md`：BT-2-002 / BT-4-001 断言要点登记键缺席口径；BT-1-002 / BT-2-003 / BT-4-004 过滤断言**不变**（内部过滤逻辑未动）；
- `tests/batch/helper/helper.go` `TestProductName` 保留——种子数据仍需经 `product_name=AI_product` 验证默认产品线过滤。

### 6.4 测试资产（`integration-test` 仓库，随本卡同步修订）

- `SC1301-TC077.json`：`requires.capabilities` 删除 `two-isolated-product-identities`；
- `SC1301-TC077.md` / `sc1301_tc077_test.go:190-230`：`A-01`（跨 product 按 404）**删除**并记录理由（引用本变更目录）；保留用例改为默认产品线口径——任务 `product_name` 非默认产品线时详情/取消/文件查询按 404、列表不出现（改写范式参照本仓库 BT-1-002 / BT-2-003 / BT-4-004）；若 TC077 的 oracle 引用 batches.md §2.1 字段表，同步删除 `product_name` 相关断言；
- 执行 `./scripts/e2e.sh status --tc-id SC1301-TC077 --real-verification=on` 确认不再因 `two-isolated-product-identities` 阻断。

### 6.5 多产品线数据可见性核实（issue【推断待确认】项落地）

方案 A′ 下该推断转为**显式文档化的已知限制**（§6.1 末段）。低成本核实：系统测试环境回读 `batch_tasks` 表，统计 `product_name <> AIRouteInnerProductName` 的记录数——若 >0，在 issue #276 下补评论说明实际影响面（这些任务当前确实不可见），严重性维持 P2 或按 issue 条款上调 P1 由产品 owner 裁决；若为 0，记录核实结果后关闭该项。

### 6.6 行为变化表

| 场景 | 修复前 | 修复后 |
|------|--------|--------|
| 列表/详情/取消响应 | 含 `product_name` 键 | **键剔除**，其余字段不变 |
| 文件查询响应 | 直出内部实体（含 `product_name` 等内部字段） | `product_name` 键剔除，其余字段暂维持（遗留登记） |
| 查询过滤语义 | 默认产品线过滤 | **不变** |
| 契约中 `product_name` | 「中间件强制注入，只读」 | 不存在（不入参、不返回） |
| TC077 | `BLOCKED_ENVIRONMENT` | `READY`（断言改为默认产品线口径） |
| ai-gateway-web 详情抽屉 | 展示 `detail.product_name` | 取不到值——web 删展示行（联动跟进） |

## 7. 验收判据映射（issue acceptance criteria）

| # | Issue 验收判据 | 本方案落点 |
|---|----------------|-----------|
| 1 | 两处文档表述唯一且一致，全文检索不再同时出现两种互斥口径 | §6.1 改写后 `grep -rn "强制注入\|调用凭证解析" design-docs/api-define/OpenAPI接口定义/batches.md` 无命中；与 `认证授权机制.md:51` 口径一致 |
| 2 | 维持「不绑定产品线」时 §1 写明默认产品线填充并删除 404 语义 | §6.1——比判据更进一层：`product_name` 完全不出现在契约中；`:285`/`:395`/`:418` 跨产品线 404 语义已删 |
| 3 | `product.go:26-32` 注释不得再出现「待 McUserProbe 侧补齐」 | 前轮已完成（本轮保持） |
| 4 | TC077 设计与 spec 不再要求 `two-isolated-product-identities` | §6.4（外部仓库跟进） |
| 5 | `A-01` 要么保留且有实现支撑，要么删除并记录理由 | §6.4 删除并记录理由（改为「非默认产品线不可见」可构造断言，范式 BT-1-002 / BT-2-003 / BT-4-004） |
| 6 | 不得回归：单产品线环境四条接口语义不变、`e2e.sh status` 不再阻断 | §6.6——「语义不变」按裁定细化为「过滤语义不变、响应剔除 `product_name`」；本仓库 `tests/batch/...` 全量回归（§8） |

## 8. 测试与回归

1. `go build ./...`、`go vet ./endpoints/openapi_v1/batches/... ./model/ibatch/...`；
2. `go test ./endpoints/openapi_v1/batches/... ./model/ibatch/...` 全绿；
3. `make test-model-cover-gate`（model ≥70%；视图层删字段不应引起覆盖率异常波动）；
4. 集成测试（`test/integration` 独立 module）`go test -count=1 ./tests/batch/... ./tests/schema/...` 强制真实重跑：BT-1-002 / BT-2-003 / BT-4-004 过滤断言不变应直接通过；BT-2-002 / BT-4-001 改为键缺席断言后通过；
5. web 联动不在本仓库验证——ai-gateway-web 删除 `BatchView.vue:36-37` 展示行后由前端侧回归。

## 9. 实施记录

实施已完成并验证（2026-10-23），按产品 owner 裁定的方案 A′（契约剔除）落地。前轮已完成的文档校准（sys-design 三处、2026-10-07 历史单注记、`product.go` / `manager.go` / `endpoints.go` 注释、design.md §2.3 登记、BT 过滤断言更名）继续有效，本轮在其上返工契约与响应形状。

**改动文件**（本轮返工部分）：

- `design-docs/api-define/OpenAPI接口定义/batches.md`：§1（`:17`）改为「`product_name` 不出现在入参与返回值中」；§2.1 删字段表行与示例键；§3.1 删约束条与示例键；§3.2 / §3.3 删示例键；§3.4 约束改写；§6 已知限制改写（回链过滤口径与键缺席口径用例）；
- `design-docs/sys-design/接口层设计文档.md:365`：改为「内部字段，不出现在入参与返回值中」；
- `endpoints/openapi_v1/batches/view.go`：`batchTaskView` 删 `ProductName` 字段与赋值（doc 注释标注 issue #276）；新增 `batchFileView` / `newBatchFileView`（与 `ibatch.BatchFile` 同字段集、剔除 `product_name`）；
- `endpoints/openapi_v1/batches/file.go`：响应改经 `newBatchFileView` 投影（不再直出内部实体），注释清理；
- `endpoints/openapi_v1/batches/list.go:44-46`、`model/ibatch/types.go:185-187`：遗留旧措辞（「强制取中间件注入值」「产品线隔离」）清理；
- 测试：`tests/schema/openapi/batch.go`（`BatchTaskDetailSchema` 删 `product_name` 必需键与类型条目）、`tests/batch/detail/detail_test.go:126`、`tests/batch/file/file_test.go:83`（取值断言改为**键缺席断言**）、`tests/batch/design.md`（BT-2-002 / BT-4-001 登记键缺席口径 + 断言要点补契约剔除锁）、`endpoints/openapi_v1/batches/view_test.go`（键集合锁删 `product_name`）、`batches_test.go`（`TestBatchFileOneAction` 适配视图返回 + 断言措辞）。

**验证结果**：

- 构建与静态检查：`go build ./...`、`go vet ./endpoints/openapi_v1/batches/... ./model/ibatch/...` 通过；
- 单元测试：`go test -count=1 ./endpoints/openapi_v1/batches/... ./model/ibatch/...` 全绿（含改键集合锁后的 `TestBatchTaskView_KeySet`、改视图断言后的 `TestBatchFileOneAction`）；
- model 覆盖率门禁：85.0% ≥ 70%，通过；
- 集成测试（`test/integration` 独立 module，`go test -count=1 ./tests/batch/... ./tests/schema/...`）：batch（cancel 12.6s / detail 2.8s / file 2.6s / list 5.5s）与 schema（innerapi / openapi / report）**全部 PASS**。注意：集成 harness 复用项目根预编译 `ai-gateway-api.exe`（`testutil/server.go:277-280`），已重新 `go build -o ai-gateway-api.exe .` 后重跑——首轮失败即因二进制过期，属流程问题已纠正并在此记录；
- 验收判据 1 全文检索：`batches.md` 不再出现「强制注入」「调用凭证解析」「调用方产品线」；互斥旧口径仅存于 2026-10-07 历史变更单（带修订注记）与本变更记录的引述中。

**联动跟进（本仓库之外）**：

- **ai-gateway-web**：`src/modules/BatchTasks/components/BatchView.vue:36-37` 详情抽屉展示 `detail.product_name`，契约剔除后该处取不到值——需 web 侧删除展示行（或改取默认产品线文案）并回归；
- **SC1301 harness 仓库**：TC077 资产修订（§6.4）——删 `two-isolated-product-identities` capability、`A-01` 删除登记并改为「非默认产品线不可见」断言（范式 BT-1-002 / BT-2-003 / BT-4-004），TC077 oracle 若引用 §2.1 字段表需同步删 `product_name`；
- **系统测试环境**：§6.5 `batch_tasks` 表非默认产品线记录计数回读核实，结果回填 issue #276 评论。

**沉淀评估（六步法 Step 6）**：契约修正已在 `batches.md` §1/§6 承载，`batchFileView` 的视图归属在 `view.go` doc 注释说明；无新机制、不新增 `sys-design/details/` 文档。

**若产品 owner 后续裁定方案 B**：本卡文档保留作为方案 A′ 备案；按 `design-changes.md` §2.4.4 预留路径（`user_products` 表 + `FAP` 机制均在）单独立项「重新引入 Product Scope」，须显式登记为未完成设计项，不得以「已生效条款」形式合入。
