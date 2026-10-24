# 批量与异步任务支持（一期：批量透传正确化）——变更摘要

## 1. 背景

需求《批量与异步任务支持需求分析（AI 网关场景）》一期（P0）：让 provider 原生
OpenAI Batch API（`/v1/files` + `/v1/batches`）经过网关时"计费正确、粘性可靠、
可管控"。provider 批量计费为 5 折 + 24h 完成窗口，当前网关把批量流量按 chat 价
错计费，且批量任务（一次创建、事后完成）的生命周期与成本在控制面无承载实体。

BFE 数据面落地方案见 `bfe/docs/zh_cn/modifications/2026-10-07-ai-batch-support/design-changes.md`
（端点识别、mod_ai_batch 模块、预留/结算/释放、batch 粘性、限流）。本次变更是
控制面配套：批量价格模型与导出、批量限流配置、批量任务实体与管控 API、对账 job。

## 2. 目标

1. `model_prices` 支持批量价：`mode` 白名单新增 `batch`，价格行新增
   `batch_discount`（默认 0.5），导出时展开为显式 batch 价格行随 `ModelTable`
   下发 BFE。
2. `rate_limit_policies` 支持批量限流维度：新增可空 `batch_limits` JSON 段，
   导出时生成 BFE `ai_rate_limit.data` 策略 `rules.batch` 段。
3. 新增批量任务实体：`batch_files` / `batch_tasks` 两张表（双 DDL），承载
   OpenAI batch 状态机与"预留-结算-释放"三态对账。
4. 管控 API：`/open-api/v1/batches` 列表/详情/取消 + `/open-api/v1/batch-files`
   查询；取消由控制面直调 provider（新 egress 依赖）。
5. 对账 job：从共享 Redis `BATCH_*` 键扫描同步任务实体、推进状态、兜底结算、
   对账巡检——**不依赖报表后端**（MySQL/Doris/CH/StarRocks 可插拔）。

## 3. 范围

- **涉及面**：`ai-gateway-api` 控制面（model_prices、rate_limit_policy、新
  batches 域、导出、job）、`db_ddl.sql`/`db_ddl_sqlite.sql`。
- **不涉及面**：BFE 数据面（另行落地）、ai-gateway-web（批量任务页，纯前端）、
  log-reader / observability（pb 字段映射与 Doris 加列）。
- **数据面影响**：`ModelTable` 增加 batch 价格行、`ai_rate_limit.data` 增加
  `rules.batch` 段（旧 BFE 忽略未知 JSON 字段，安全）；BFE 与控制面共享 Redis
  新增 `BATCH_*` 键。

## 4. 关键决策

| 决策 | 说明 |
|------|------|
| 折扣在导出时展开（D2） | `batch_discount` 不在数据面运行时计算；导出展开为 `mode=batch` 显式价格行，数据面纯查表；报表库同时存在 chat/batch 两行价，"批量节省金额"可直接 SQL 派生；手工维护的 batch 行优先于系数展开 |
| 任务同步源是 Redis 而非日志管道（D4） | BFE 与控制面共享 Redis（`QUOTA_*` 同集群）；`BATCH_TASK` hash 内含任务同步全部字段（含 BFE 原地更新的结算字段）；报表后端是可插拔配置（`[Report].Backend`），不能作为管控链路依赖；访问日志管道只承载成本报表与抽样核对 |
| 取消由控制面直调 provider（D8） | 任务实体记录 `key_name`，控制面持有 provider keys 与实例地址；cancel 不过数据面、无粘性问题；与兜底结算共用同一 egress 能力 |
| 多实例协调全部外置（§3.6） | job 用 MySQL named lock（`mysqlreport.Job` 惯例）；cancel 用 DB 条件更新抢占（affected_rows=0 → 409）；结算/释放用 Redis 原子锁 + `WHERE settle_status='reserved'` 条件更新——锁失效双跑幂等收敛，不双倍结算 |
| 预留-结算-释放三态 | 创建时按"行数 × 预估 token 上限 × batch 价"预留（Redis `BATCH_RESERVE` 镜像计数），完成时按结果文件实际 usage 实结（允许透支 + `over_reserved` 标记），cancel/expired/failed 整额释放 |
| `batch_limits` 可为空 | 省略/null = 策略不参与批量限流；存量行零迁移；全空仍受 BFE 全局硬顶约束，不留"完全不限"路径 |

## 5. 关联文档

- 需求分析：`document-ai-gateway/迭代系统设计/v0.8/批量与异步任务支持/批量与异步任务支持需求分析.md`
- 主设计：`document-ai-gateway/迭代系统设计/v0.8/批量与异步任务支持/批量与异步任务支持-技术实现方案.md`
- BFE 侧修改说明：`bfe/docs/zh_cn/modifications/2026-10-07-ai-batch-support/design-changes.md`
- 接口变更：`api-changes.md`；详细设计：`design-changes.md`

## 6. 实施阶段

| 阶段 | 内容 | 状态 | 关键文件 |
|------|------|------|----------|
| 1 | 价格模型：`mode` 白名单 + `batch_discount` + 导出展开 | ✅ 已实现 | `model/imodel_price/`、`model/icluster_conf/cluster.go`（`newAIConf`） |
| 2 | 限流模型：`batch_limits` 列 + 导出 `rules.batch` 段 + redis_key 生成 | ✅ 已实现 | `model/rate_limit_policy/`、`model/shared/rate_limit_redis_key.go` |
| 3 | 双 DDL：`batch_files` / `batch_tasks` + model/DAO 层 | ✅ 已实现 | `db_ddl.sql`、`db_ddl_sqlite.sql`、`storage/rdb/`、`model/` |
| 4 | 管控 API：`/open-api/v1/batches[/*]` + `/batch-files`（DB miss 回源 Redis） | ✅ 已实现 | `endpoints/openapi_v1/batches/`、`model/ibatch/` |
| 5 | cancel：DB 条件更新抢占 + provider 直调 + 释放预留 + 审计 | ✅ 已实现 | `model/ibatch/`、`model/quotacache`（Redis） |
| 6 | 对账 job：Redis SCAN 同步、状态推进、兜底结算、巡检、TTL 清理 | ✅ 已实现（巡检/日志补登/TTL 清理二期） | `model/ibatch/job.go`（仿 `storage/mysqlreport/job.go` named lock） |
| 7 | 报表：`batch_savings` 派生指标、任务级核对口径 | 🔄 待实现 | `model/ireport/` |
| 8 | dashboard 批量任务页（ai-gateway-web） | 🔄 待实现 | 独立仓库 |
| 9 | 测试：单测（model 覆盖率 ≥70%）+ 集成测试对账套件 | ✅ 单测已实现（model 84.9%、ibatch 87.7%）；集成测试待实现 | `integration-test/`（独立仓库） |

## 7. 实现结果

控制面代码已全部落地（阶段 1-6、9 单测部分），`go build ./...` 通过，
`go test ./model/... ./endpoints/... ./storage/...` 全绿。

**实现校准记录**（设计文档与 as-built 的偏差，文档已同步）：

1. **Redis 键契约以 BFE 实现为准**：`BATCH_FILE:<cluster>:<file_id>` 与
   `BATCH_TASK` 的 `cluster` 字段（BFE 运行时概念，控制面 provider 列承载读回值）；
   预留簿记为 `BATCH_RESERVE_BATCH:<batch_id>`（按配额计划逐计划记账，
   token_auth `batch_quota.go`），`BATCH_SETTLED` 为结算/释放共用占位。控制面
   `model/ibatch` 的 Redis 抽象（`redis_adapter.go`，单 key Lua 与 BFE 脚本同
   语义）按此契约实现；SCAN 经 redigo 对 bns 解析出的全部实例逐实例游标扫描
   （底层 bfe redis_client 无 SCAN 原语）。
2. **cancel 顺序**：先 DB 条件更新抢占、后出网调 provider（provider 失败不回滚
   `cancelling`，由状态推进收敛）；Redis 回源任务先补登再抢占一次，消 409 窗口。
3. **兜底结算价格**：`settle_units = usage_in×input + usage_out×output`（基价，
   tier 价一期未接入）；查不到 batch 价 → warn + `settle_units=0` + 照常推进
   （巡检金额核对发现后人工处理）。
4. **job 职责④（对账巡检/日志补登）与⑤（TTL 清理）为二期**；job 默认关闭，
   `[BatchJob] Enable=true` 开启（依赖控制面→provider 出网）。

**遗留**：

- MySQL 方言 DAO/named lock 经 sqlmock/SQLite 实测，未实机跑 MySQL；
- 多集群 bns 若挂代理集群，SCAN 入口可能重复扫描（幂等 upsert 收敛，仅多耗开销）；
- 阶段 7（报表 batch_savings）、8（dashboard）、跨仓集成测试待后续。
