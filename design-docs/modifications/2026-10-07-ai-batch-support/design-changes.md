# 批量与异步任务支持（一期）——详细设计（控制面）

## 1. 批量价格模型（model_prices）

**改动点**：`model/imodel_price/`（mode 白名单、`batch_discount` 字段、校验）、
导出（`model/icluster_conf/cluster.go` `newAIConf`，:1325-1366）。

现状：`ModelPrice` 唯一键 `(provider, model, mode)`（`model_price.go:35-63`），
mode 白名单（`validate.go:28-42`）无 `batch`；无折扣字段（折扣只能以更高价键值
表达）。

设计：

1. mode 白名单新增 `batch`（`file` 不进价格体系——文件操作不计费，数据面对
   ModeFile 请求不做查价）。
2. `ModelPrice` 新增可选 `batch_discount`（0~1，默认 0.5），挂在顶层或
   metadata；语义为"导出时把本行各 token 价格键 × 系数展开生成 `mode=batch`
   价格行"，tier 价同样展开。
3. **导出时展开**（而非数据面运行时算）：`newAIConf` 组装 `ModelTable` 时生成
   batch 行一并下发；数据面 `LookupModelPrice(cluster, model, "batch")` 纯查表。
   展开式覆盖机制：手工维护的 `(provider, model, mode=batch)` 行优先于系数
   展开行（导出时检测冲突，手工行优先 + 告警日志）。
4. 校验：`batch_discount` ∈ (0, 1]；与手工 batch 行并存时按覆盖机制处理；
   校验规则与数据面 `AIConfCheck` 对齐（AGENTS.md 双端对齐要求）。
5. dashboard：价格表单加"批量折扣"输入（默认 0.5），批量价列只读展示展开结果
   （ai-gateway-web，独立仓库）。

报表口径：报表库同时存在 `mode=chat` 与 `mode=batch` 两行价，"批量节省金额"
= (chat 价 − batch 价) × usage，在报表 SQL 层派生（`batch_savings` 派生指标）。

## 2. 批量限流配置（rate_limit_policies）

**改动点**：`model/rate_limit_policy/`（`RateLimitPolicyParam` 新增
`BatchLimits`、`db_ddl.sql:493-502` 加可空 JSON 列）、导出
（`ExportRateLimitPolicyConfig`，:187-243）、`model/shared/rate_limit_redis_key.go`。

```go
// 新增
type BatchLimits struct {
    MaxCreateRPM     int   `json:"max_create_rpm"`
    MaxActiveBatches int   `json:"max_active_batches"`
    MaxFileBytes     int64 `json:"max_file_bytes"`
    MaxFileLines     int   `json:"max_file_lines"`
}
// RateLimitPolicyParam 新增字段
BatchLimits *BatchLimits `json:"batch_limits"`   // nil = 该策略不参与批量限流
```

导出：策略 `rules` 内生成 `batch` 段（`omitempty`），`max_create_rpm` 需要
redis_key（生成惯例同 `RL_TPM_rlp-0001_0`，如 `RL_BATCH_<policyId>_rpm`）；
文件上限为请求内本地校验无 key；`max_active_batches` 复用数据面共享的
`BATCH_ACTIVE:<api_key_id>` ZSET 不重复配 key。

多策略组合语义（数据面执行，控制面配置注释写明）：逐策略 AND 等效取 min；
策略 `Models` 只约束 tpm/rpm/concurrency、不约束 batch 段；全空时仍受 BFE
`mod_ai_batch.data` 全局硬顶约束。`batch_limits` 四个维度全部按 apikey 计、
与 model 无关（batch 创建请求体无 model 字段）。

## 3. 任务实体（新表，双 DDL）

**改动点**：`db_ddl.sql` + `db_ddl_sqlite.sql`（仓库惯例双 DDL）、
`storage/rdb/` DAO、`model/ibatch/`。

```sql
-- 批量文件（同步源：Redis BATCH_FILE）
batch_files(
  id BIGINT PK AUTO_INCREMENT,
  file_id VARCHAR(128) NOT NULL,
  provider VARCHAR(128) NOT NULL,
  key_name VARCHAR(128),
  api_key_id VARCHAR(128) NOT NULL,
  product_name VARCHAR(128) NOT NULL,
  direction VARCHAR(8) NOT NULL,        -- input | output
  purpose VARCHAR(32),
  line_count INT,
  bytes BIGINT,
  first_seen_at DATETIME,
  last_seen_at DATETIME,
  UNIQUE KEY uk_file_provider (file_id, provider),
  KEY idx_apikey (api_key_id)
)

-- 批量任务（权威实体；状态机对齐 OpenAI，枚举预留 ended/deleted 方言）
batch_tasks(
  id BIGINT PK AUTO_INCREMENT,
  batch_id VARCHAR(128) NOT NULL,
  api_key_id VARCHAR(128) NOT NULL,
  product_name VARCHAR(128) NOT NULL,
  entity_id VARCHAR(128),
  provider VARCHAR(128) NOT NULL,
  key_name VARCHAR(128),                -- 控制面直调 provider（cancel/兜底结算）用
  endpoint VARCHAR(256),
  input_file_id VARCHAR(128),
  output_file_id VARCHAR(128),
  status VARCHAR(16) NOT NULL,          -- validating/queued/in_progress/finalizing/
                                        -- completed/expired/failed/cancelled/cancelling
  request_counts JSON,
  est_lines INT,
  usage_input_tokens BIGINT,
  usage_output_tokens BIGINT,
  usage_source VARCHAR(16),             -- download | reconcile
  reserve_units BIGINT,                 -- 1e-8 定点，与数据面同口径
  settle_units BIGINT,
  settle_status VARCHAR(16) NOT NULL,   -- reserved | settled | released（三态对账主字段）
  over_reserved TINYINT DEFAULT 0,      -- 实际超出预留标记
  sync_source VARCHAR(8) DEFAULT 'redis', -- redis | log（日志补登标记，§6 职责 4）
  idem_key VARCHAR(128) NOT NULL UNIQUE,  -- = batch_id
  created_at DATETIME, updated_at DATETIME, terminal_at DATETIME,
  KEY idx_status (status), KEY idx_apikey (api_key_id), KEY idx_terminal (terminal_at)
)
```

状态机：provider 返回状态原样存；终态不回退；二期方言扩展（Anthropic `ended`、
Gemini `deleted`）为枚举增量。

## 4. 管控 API（endpoints/openapi_v1/batches/）

**改动点**：新域（`xreq.Endpoint` 惯例）、`stateful/container` 接线（DB、Redis、
provider HTTP client、解密器，全部复用现有组件）。

| 端点 | 说明 |
|---|---|
| `GET /open-api/v1/batches` | 列表；过滤 product/api_key/entity/provider/status/时间窗；索引列 + 主键游标分页；`product_name` 强制注入（McProductProbe） |
| `GET /open-api/v1/batches/{batch_id}` | 详情：DB miss → 回源 Redis `BATCH_TASK`（覆盖 job 落库前窗口）→ 再 miss 404；返回状态机/用量/预留/结算/释放/over_reserved/关联文件 |
| `POST /open-api/v1/batches/{batch_id}/cancel` | 五步：查任务（DB→Redis）→ 取 provider key + 拼地址（实例 + protocol_paths.openai）→ 出网 POST cancel（不可达 502 / provider 404 / 已终态 409）→ DB 条件更新抢占（`UPDATE ... SET status='cancelling' WHERE batch_id=? AND status NOT IN (终态)`，affected_rows=0 → 409）+ Redis `BatchRelease` 幂等释放 → operation_logs 审计 |
| `GET /open-api/v1/batch-files/{file_id}` | 文件元数据查询 |

> 2026-10-23 修订（issue #276）：上表中"`product_name` 强制注入（McProductProbe）"的表述被取代——本组路由不含产品路径变量、`McProductProbe` 无从注入，且 Token 不绑定产品线（2026-07-27 决策），实际口径为服务端解析默认产品线 `AIRouteInnerProductName`；本条原表述保留作历史记录，现行口径见 `modifications/2026-10-23-issue-276-batch-product-name-contract-fix/` 与 `api-define/OpenAPI接口定义/batches.md` §1/§6。

竞态（与对账 job、与 BFE 查询路径）：共用 `BATCH_SETTLED`（结算去重）与
`reserved_units 清零`（释放去重）两把锁，cancel×job、cancel×cancel 均收敛；
cancel 后 provider 实际跑完（cancelling→completed）按 completed 正常结算。

鉴权/多租户：`McProductProbe + McUserProbe + iauth.FA`（新 feature/action）；
写操作进 `operation_logs`（change_summary 带 batch_id + 前后状态）。

**新依赖**：控制面 → provider 出网（cancel 与兜底结算共用）。不允许出网的部署
降级：cancel 退化为仅展示、兜底结算退化为只依赖下载拦截 + 预留 TTL 释放
（对账精度下降，部署文档标注）。

## 5. 对账 job（model/ibatch/job.go）

**改动点**：新 job，复用 `storage/mysqlreport/job.go:53-140` 的 named lock +
循环框架（每分钟 tick、MySQL `GET_LOCK` 多副本互斥）。

职责：

1. **Redis 同步落库（主事件源）**：SCAN `BATCH_TASK:*` / `BATCH_FILE:*`（键 TTL
   48h ≫ 同步周期 1min，无丢失窗口），按 idem 幂等 upsert；BFE 下载拦截结算时已把
   `usage_in/usage_out/settle_status=settled` 原地更新进 `BATCH_TASK`（as-built 见
   `bfe/bfe_modules/mod_ai_batch/resp_capture.go:153-157`），job 直接取用。
   **不读报表库**——报表后端可插拔，不作管控链路依赖。
2. **状态推进**：非终态任务直调 provider `GET /v1/batches/{id}`（key 出网），
   更新 status/output_file_id/request_counts；发现 output_file_id 补登记
   `batch_files`。
3. **兜底结算**：completed 且 `settle_status='reserved'` 且超宽限（30min）→
   控制面用 provider key 下载输出文件解析 usage、直写 Redis 结算
   （`BATCH_SETTLED` 幂等）+ 条件更新 `settle_status='settled'`；expired/
   failed/cancelled → `released`。条件更新 `WHERE settle_status='reserved'`
   保证多实例/重跑不双倍结算。
4. **对账巡检**：Redis `BATCH_RESERVE` 占用 vs DB 在途任务总额（差异告警）；
   金额核对以 `batch_tasks.settle_units` 为基准与日志后端 batch 成本抽样比对
   （误差应为 0；非 MySQL 后端降级按日任务级比对）。**日志补登**：读取日志后端
   带 batch_id 的批量行，对 DB 与 Redis 均缺失的任务（BFE Redis 写失败的
   fail-open 场景）补登 `batch_tasks`（`sync_source='log'`）+ 告警；日志后端
   不可用时跳过仅告警。
5. **TTL 清理**：终态任务保留 N 天（retention 可配）；Redis `BATCH_*` 键 TTL
   自然消亡。

配额侧：`BatchReserve/Release` 与控制面 `QUOTA_*`、`quotacache`（1e-8 定点）
同集群同口径，job 可直接读写核对；`pass_when_no_enough_quota` 语义由数据面
执行，控制面不改配额计划模型。

## 6. 多实例部署

| 组件 | 协调机制 |
|---|---|
| 管控读接口 | 无状态；DB/Redis 共享 |
| cancel 并发 | DB 条件更新抢占（affected_rows=0 → 409），与 keyrotate 锁表 409 先例同思路 |
| 对账 job | MySQL `GET_LOCK` named lock，每 tick 抢锁 |
| 锁失效双跑 | 全部写操作幂等收敛：SCAN upsert、条件更新 settle 状态、`BATCH_SETTLED`/Lua 原子锁——最坏重复一次幂等操作，不双倍结算 |
| SQLite 部署 | 控制库恒为 MySQL/SQLite；named lock 为 MySQL 特性，SQLite 模式锁退化空操作，约定单实例（现有报表 job 同一约定） |

原则：跨实例协调状态一律放 Redis（原子 Lua）或 DB（条件更新/named lock），
进程内不保留批量任务状态。依赖注入遵循 AGENTS.md（manager 不硬编码
`stateful.DefaultConfig/DefaultClientSet`，测试可 mock）。

## 7. 报表（model/ireport）

- 明细表 `bfe_ai_request_log` 加列（batch_id/file_id/lines/bytes/batch_status），
  log-reader 映射 + Doris routine load SQL（observability 仓库）。
- 分钟聚合 `bfe_ai_metrics_1m` 确认 mode 维度已存在则零改动。
- 新增 `batch_savings` 派生指标（chat/batch 价差 × usage，SQL 派生）；批量任务
  数/行数/成功率/平均完成时长按 mode=batch 行天然可得；任务级"TTFT"以
  创建→completed 时长近似并标注口径。

## 8. 测试与验收

- 单测：model 层 ≥70% 覆盖率（`make test-model-cover-gate`）；mock 用手写
  callback fakes（AGENTS.md TESTING.md 惯例）。
- 集成测试（integration-test 仓库）：全生命周期功能等价、计费对账差额为 0、
  三态对账、cancel 竞态、job 兜底结算、Redis 写失败日志补登、多实例 named
  lock 互斥。
