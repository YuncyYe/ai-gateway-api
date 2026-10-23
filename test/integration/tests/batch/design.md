# Batch（批量任务管控）测试用例设计文档

## 1. 模块概述

Batch 模块覆盖"批量与异步任务支持（一期）"的控制面管控 OpenAPI：

- `GET /open-api/v1/batches`：批量任务列表（过滤 + 主键游标分页）
- `GET /open-api/v1/batches/{batch_id}`：批量任务详情（DB 优先，miss 回源 Redis）
- `POST /open-api/v1/batches/{batch_id}/cancel`：取消批量任务（DB 抢占 → 出网 provider → 幂等释放 → 审计）
- `GET /open-api/v1/batch-files/{file_id}`：批量文件元数据查询（provider 必填 query 参数）

接口语义详见 `design-docs/api-define/OpenAPI接口定义/batches.md` 与
`design-docs/modifications/2026-10-07-ai-batch-support/`。

## 2. 测试目标与环境

### 2.1 测试目标

| 目标 | 说明 |
|------|------|
| 列表 | 空表返回空数组 + `next_cursor`；status/provider/api_key_id 过滤生效；产品线隔离；limit >200 钳制为 200；cursor 翻页不重复不遗漏 |
| 详情 | 不存在 → 404（BATCH_NOT_FOUND）；直写一行 → 200 且字段完整（settle_status / reserve_units 定点整数 / over_reserved / sync_source）；关联 batch_files 按 §2.2 形状组装在 Data 顶层（无 task/files 包装层，issue #249 合同锁定） |
| 取消 | 不存在 → 404；已终态（completed）→ 409；cancelling（受理中）重放 → 200 幂等（重发 cancel，issue #274）；keyless provider（keys: []）→ 200 且无鉴权出网（issue #274）；非终态但 provider 不可达 → 502；provider 记录缺失 → 500（实现语义，见 §4.3）；成功 → 200 cancelling + operation_logs 审计 |
| 文件 | 直写 batch_files → 200 字段正确；不存在 → 404；provider 缺失 → 422；产品线隔离 |

### 2.2 测试环境

- 真实 `ai-gateway-api.exe` 子进程 + SQLite（`sqlite-strip` 驱动）+ miniredis
- `SkipTokenValidate=true`（假 visitor 为系统管理员，Authorizer 直接放行）
- 无 provider 出网：除 cancel 成功用例（本测试进程内起 httptest 假
  provider）外，不出网；502 用例指向未监听回环端口（连接拒绝）
- 数据准备：`testutil db.go` 方式直写 SQLite `batch_tasks` /
  `batch_files` / `providers`（模块内共享 helper 包 `tests/batch/helper`）

### 2.3 产品线解析：缺陷记录与修复（2026-10-07）

**原缺陷**：四个路由（`/batches`、`/batches/{batch_id}`、
`/batches/{batch_id}/cancel`、`/batch-files/{file_id}`）均不含
`product_id` / `product_name` 路径变量，而 `McProductProbe` 仅从 mux
路径变量注入产品线上下文，`McUserProbe` 只做身份认证不注入产品线，
导致处理器 `ibasic.MustGetProduct` 在任何环境下都失败
（422 `Fail To Get Product`），业务逻辑不可达。

**修复（本轮随 MySQL 验证落地，endpoints/openapi_v1/batches/product.go）**：
`resolveProduct` 优先取已注入的产品线上下文；未注入时回退到配置的
默认产品线 `AIRouteInnerProductName`（与 ai_route 等模块的
`default_product.go` 同模式）。设计文档"产品线由调用凭证解析"的
token→产品线注入（McUserProbe 侧）仍是后续增强，本组端点在凭证解析
补齐前对系统管理员/默认产品线可用。

**测试承载**：`BT-x-000` 为可达性冒烟（验证端点进入业务语义而非
422）；`RequireBatchAPI` 守卫保留为产品线解析回归时的防御性跳过。
种子数据以 `product_name=AI_product`（= 测试环境默认产品线）为可见行。

## 3. 目录结构

```
batch/
├── design.md
├── helper/
│   └── helper.go            # 探测守卫 + SQLite 直写种子工具
├── list/
│   └── list_test.go         # BT-1-000 ~ BT-1-005
├── detail/
│   └── detail_test.go       # BT-2-000 ~ BT-2-007
├── cancel/
│   └── cancel_test.go       # BT-3-000 ~ BT-3-009（BT-3-007 为 MySQL 并发用例）
└── file/
    └── file_test.go         # BT-4-000 ~ BT-4-004
```

## 4. 用例设计

### 4.1 列表（BT-1）

| 编号 | 场景 | 测试类型 | 数据准备 | 预期 |
|------|------|---------|---------|------|
| BT-1-000 | 可达性冒烟：产品线回退后端点可进入业务逻辑 | 冒烟 | - | 200（不再 422 `Fail To Get Product`） |
| BT-1-001 | 空表列表 | 正常参数 | 空表 | 200；`list=[]`（非 null）；`next_cursor=0` |
| BT-1-002 | status / provider / api_key_id 过滤 + 产品线隔离 | 正常参数 | 直写 4 行（含其它产品线 1 行） | 各过滤仅命中目标行；`OTHER_product` 行任何组合不可见 |
| BT-1-003 | limit 上限钳制 | 边界值 | 直写 205 行 | `limit=250` 返回 200 行；`next_cursor != 0` |
| BT-1-004 | cursor 翻页 | 正常参数 | 直写 7 行 | `limit=3` 翻至 `next_cursor=0`；收集 id 无重复无遗漏 |
| BT-1-005 | 列表元素零值键必现（issue #271） | 返回数据 | 直写零值行 1 行（`settled`，金额 0） | 200；`Data.list[0]` 的 `est_lines` / `usage_*` / `reserve_units` / `settle_units` 键存在且为 0，`output_file_id` / `terminal_at` 键存在且为 `""`；原始报文 token 精确匹配；防"只修详情、列表残留" |

**断言要点**

- `Data.list` 空表形态为 `[]`（家族约定：顶层键存在且为数组，参照
  ai_cache AC-2-001）
- 过滤断言采用"目标行可见 + 非目标行不可见"双侧断言，防"全空误过"
- 翻页收集限定本用例 tag 前缀行，避免同模块其它用例数据干扰

### 4.2 详情（BT-2）

| 编号 | 场景 | 测试类型 | 数据准备 | 预期 |
|------|------|---------|---------|------|
| BT-2-000 | 可达性冒烟（ghost → 404 业务语义） | 冒烟 | - | 404 |
| BT-2-001 | 任务不存在 | 异常参数 | - | 404 |
| BT-2-002 | 直写一行 → 字段完整（**2026-10-16 按合同重写**，issue #249；**2026-10-19 补 filename**，issue #254） | 返回数据 | 直写任务 1 行 + 输入/输出文件各 1 行（含 filename） | 200；Data 顶层即任务字段（§2.1，无 task/files 包装层）；`reserve_units=250000000` 且原始报文为整数 token（1e-8 定点，不做 ÷1e8）；`batch_files` 恰含输入/输出两条，元素键集合精确为 §2.2 六字段（file_id/provider/purpose/filename/bytes/created_at），`filename` 为 `input.jsonl`/`output.jsonl`，原始报文无 direction 等内部字段 |
| BT-2-003 | 产品线隔离 | 正常参数 | 直写 `OTHER_product` 行 1 行 | 404（按不存在处理） |
| BT-2-004 | 无关联文件 | 返回数据 | 直写任务 1 行（无输入/输出文件 ID） | 200；`batch_files` 键缺席（omitempty，无文件不输出空数组） |
| BT-2-005 | purpose 按方向映射（issue #254） | 返回数据 | 直写任务 1 行 + 输入/输出文件各 1 行，`purpose` 列写 provider 原值 `batch`、`filename` 留空 | 200；`purpose` 分别为 `batch_input`/`batch_output`（列内原值不外泄）；`filename` 键缺席（omitempty）；原始报文无 `direction` |
| BT-2-006 | 零值终态任务零值键必现（issue #271） | 返回数据 | 直写 `settled` / `released` 零值行各 1 行（金额 0、`terminal_at=NULL`） | 200；`est_lines` / `usage_*` / `reserve_units` / `settle_units` 键存在且为 0（map `ok` 存在性 + 原始报文整数 token 双锁），`output_file_id` / `terminal_at` 键存在且为 `""`，报文不含 `"terminal_at":null`；`usage_source` 键缺席（契约缺口，锁现行为） |
| BT-2-007 | 非零回归（issue #271） | 返回数据 | 直写 `settled` 非零行 1 行（`settle_units=180000000`、`terminal_at` 非空） | 200；`settle_units` 为整数 token 180000000（不 ÷1e8）；`terminal_at` 为 RFC3339 字符串而非 `""`——防"无条件输出 0/空串"过修；修复前后都应通过 |
| BT-2-008 | 对账落库路径 filename 不丢（issue #272） | 返回数据 | 任务行 helper 直写；文件行经**生产 upsert 通路**（`batch.NewBatchStorager` + `UpsertBatchFile`，模拟对账 sync 写入）登记 input 文件（filename 非空），随后二次 upsert 传空 filename | 200；`batch_files[0]`（input）`filename` 键存在且值为种子值（map `ok` 存在性 + 原始报文 token `"filename":"input.jsonl"` 双锁）；二次空值 upsert 后键仍在（`NULLIF+COALESCE` 不回退）；修复前本用例必须失败（DAO 丢列 → DB 行 filename 恒 NULL → 键缺席），修复后通过 |

**断言要点（BT-2-002，合同锁定）**

- 顶层形状锁：`Data.task` / `Data.files` 必须**不存在**——合同（batches.md
  §3.2）规定 Data 即 BatchTask 对象本身（家族11：防包装层回归，客户端按
  单对象解码静默得到全零值）
- 任务字段全部在 Data 顶层取值；`key_name` 属内部字段，合同 §2.1 未列出，
  不作为断言依据
- `batch_files` 元素键集合用 ElementsMatch 精确锁（防 #201 幻影键）；
  `direction`/`lines`/`key_name` 等内部字段曾随实现直出（缺陷形态），
  原始报文断言 `"direction"` 不得出现
- 历史教训：本用例初版按实现现状断言 task/files 包装与 direction 字段，
  把缺陷固化成"通行证"（反面模式 #5）；2026-10-16 服务端修复后按合同重写
- issue #254 起 `filename` 由 provider File 对象落库并输出，元素键集合锁
  由"五键"扩为 §2.2 六键；`purpose` 的映射关系（列内 provider 原值 →
  视图契约枚举）由 BT-2-005 单独锁定，避免"按实现现状断言"再次固化
- issue #271 起（BT-2-006/007）：键存在性必须走 map 的 `v, ok := data[k]`
  + `require.True(ok)`，不允许只断言值——结构体解码会给缺席键填零值，
  "值正确"断言发现不了键缺席（#271 的缺陷形态正是键缺席）；整数字段必须
  对 `resp.RawBody` 做 token 精确匹配；BT-2-006 在当前 HEAD 上必须失败
  （复现 #271），BT-2-007 修复前后都应通过

### 4.3 取消（BT-3）

| 编号 | 场景 | 测试类型 | 数据准备 | 预期 |
|------|------|---------|---------|------|
| BT-3-000 | 可达性冒烟（ghost cancel → 404 业务语义） | 冒烟 | - | 404 |
| BT-3-001 | 任务不存在 | 异常参数 | - | 404 |
| BT-3-002 | 已终态（completed） | 业务规则 | 直写 completed 行 | 409（抢占 affected=0） |
| BT-3-003 | cancelling（受理中中间态）重放 → 200 幂等（issue #274） | 业务规则 | 直写 cancelling 行 + provider 指向本进程 httptest 假 upstream | 200；Data.status=cancelling；假 upstream 恰好收到 1 次重发 cancel；审计 status=1（**issue #274 契约**：抢占 affected=0 时按当前状态判别——终态 409；已 cancelling → 200 幂等重放，重发 provider cancel 保证意图落地，释放为幂等 no-op。修复前为 409 假冲突：无并发实例在处理，409 是上个请求抢占残留的误导语义，且抢占可能是首次出网未落地的崩溃空洞。存储层"仅首个转换者成功"互斥语义不变） |
| BT-3-004 | 非终态 provider 不可达 | 异常路径 | 直写 in_progress 行 + provider 实例指向 `127.0.0.1:9` | 502（连接拒绝 → ErrUpstreamUnreachable） |
| BT-3-005 | provider 记录缺失 | 异常路径 | 直写 in_progress 行（provider 不存在） | **500**（实现语义：`errProviderUnavailable` 为模型错误 WrapModelErrorWithMsg，endpoints 未映射 502；api-changes.md §5 表述为"可映射 502 或 500"，以实际运行为准钉死） |
| BT-3-006 | cancel 成功 + 审计 | 正常参数 + 审计 | 直写 in_progress 行 + provider 指向本进程 httptest 假 upstream | 200；Data.status=cancelling；出网路径 `/v1/batches/{id}/cancel`；SQLite `operation_logs` 出现 `resource_type=batch_task`、`resource_id={batch_id}`、`action=update`、`status=1`、change_summary 含 batch_id 与 cancelling |
| BT-3-007 | 并发 cancel 抢占原子性（MySQL） | 并发正确性（家族10，`//go:build mysql`） | MySQL 后端；20 goroutine 同时 cancel 同一 in_progress 行；provider 指向本进程 httptest 假 upstream | **issue #274 契约**：恰好 1 个抢占受理（主路径出网）+ 19 个幂等重放（affected=0 且读回 cancelling → 200 重发 cancel）→ 全部 20 个 200；假 upstream 收到 20 次幂等 cancel；`operation_logs` 20 条成功审计（status=1，每次尝试各记一条）；任务最终 cancelling（修复前契约：1×200 + 19×409 + upstream 恰好 1 次——防惊群；#274 裁定并发重放出网均幂等，改判 200） |
| BT-3-008 | 取消响应零值键必现（issue #271） | 返回数据 | 直写零值非终态行（`reserve_units=0`）+ provider 指向本进程 httptest 假 upstream | 200；Data 中 `est_lines` / `usage_*` / `reserve_units` / `settle_units` 键存在且为 0，`output_file_id` / `terminal_at` 键存在且为 `""`（同 BT-2-006 规则）；防"只修详情、取消残留" |
| BT-3-009 | keyless provider（keys: []）cancel（issue #274） | 业务规则 | provider `api_keys=[]`（keyless 合法配置）+ 实例指向本进程 httptest 假 upstream + 直写 in_progress 行 | 200；Data.status=cancelling；假 upstream 收到的 cancel **不含 Authorization 头**（map 存在性断言）；与数据面 keyless cluster 转发语义一致。修复前为 500 `batch provider unavailable`（不出网、抢占残留、重试 409 假冲突） |

**断言要点**

- 502 与 500 的区分：provider 记录存在但网络不可达 → 502；provider 记录
  /实例/key 缺失 → 500。两种形态都覆盖并以实现为准记录
- 审计断言直查 `operation_logs`（后台批量刷盘，轮询最长 10s），
  不走 API，避免依赖 operation-logs 查询接口自身的行为
- BT-3-007 复用 traffic_mirror 的 MySQL 惯例：`AIAPI_MYSQL_DSN` 门控、
  `StartServerWithMySQL` 独立实例（自动建库/跑 db_ddl.sql/用毕 DROP）、
  自建 client；种子经 helper 的 MySQL 方言无关实现直写

### 4.4 文件（BT-4）

| 编号 | 场景 | 测试类型 | 数据准备 | 预期 |
|------|------|---------|---------|------|
| BT-4-000 | 可达性冒烟（ghost file → 404 业务语义） | 冒烟 | - | 404 |
| BT-4-001 | 直写 batch_files → 200 | 返回数据 | 直写文件 1 行（含 filename） | 200；file_id/provider/api_key_id/product_name/direction/purpose/filename/lines/bytes 全字段匹配 |
| BT-4-002 | 文件不存在 | 异常参数 | - | 404 |
| BT-4-003 | provider query 缺失 | 必填校验 | - | 422 |
| BT-4-004 | 产品线隔离 | 正常参数 | 直写 `OTHER_product` 文件 1 行 | 404 |

### 4.5 Redis 回源说明

测试环境 Redis 为 miniredis（`Bns=test.redis.miniredis`），`FetchTask` /
`FetchFile` 的 Redis 回源（HGETALL `BATCH_TASK:<id>` / `BATCH_FILE:<provider>:<id>`）
可用但无数据时按 miss 收敛到 404，不影响上述用例。DB 命中路径不触达
Redis。cancel 成功路径的 `releaseReserve` 在 miniredis 上幂等空转。

### 4.6 双后端运行（SQLite / MySQL）

本模块 TestMain 使用 `testutil.StartServerAuto`：默认 SQLite；设置
`AIAPI_MYSQL_DSN`（管理员 DSN，不带库名）后同一批用例在 MySQL 上运行
（自动建库 `ai_gateway_it_*`、执行主项目 `db_ddl.sql`、用毕 DROP）。
种子 SQL 双方言通用（时间值由 Go 侧传入），`helper.OpenDB` 依
`ServerManager.MySQLDSN()` 自动选择驱动。MySQL 专属并发用例
BT-3-007 以 `//go:build mysql` 隔离（未设 DSN 自动 Skip）。

## 5. 数据准备约定

- 任务行：`batch_id` 以用例编号 tag 为前缀（如 `bt-1-002-xxxxx-a`），
  便于过滤与清理；`idem_key` 取 `batch_id + "-idem"` 满足唯一约束
- `product_name=AI_product` 为"可见"行，`OTHER_product` 为隔离行
- 金额字段：`reserve_units=250000000`（2.5 RMB，1e-8 定点整数）
- provider 直写：`instance_pool` / `api_keys` 为 JSON 文本；
  `protocol_paths={"openai":"/v1"}`，cancel 出网路径即 `/v1/batches/{id}/cancel`

## 6. 用例编号规则

```
BT-{接口编号}-{场景编号}
```

接口编号：1=列表，2=详情，3=取消，4=文件；`BT-x-000` 固定为可达性冒烟用例。
