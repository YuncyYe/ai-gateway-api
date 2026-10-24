# Doris 报表后端对齐缓存/镜像/意图字段（二期）：设计变更（ai-gateway-api）

> 权威分期与 Doris 数仓侧设计见设计稿《数据报表-Doris 后端设计方案》
> （v0.8/doris-report，§4 Doris 侧由 ai-gateway-observability 落地）。
> 本文只描述 ai-gateway-api 仓库内的具体设计变更（设计稿 §5）。

## 1. dorisreport 三个新维度 SQL 分支（`storage/dorisreport/report.go`）

按既有方言模板补齐一期未实现的分支，SQL 结构与 mysqlreport 已验证版本同源，
仅方言差异：

### 1.1 `buildRankingsSQL`：维度排行

- `dimension` 支持 `ai_cache_status` | `mirror_hit` | `ai_intent_answer`；
- TopN 按 `request_count` 排序（沿用既有排行语义）；
- 字符串维度空值不进排行：沿用既有空值谓词风格
  （如 `ai_cache_status != ''`），与 mysqlreport 口径一致；
- 取数来源为聚合表新 KEY 维度列（`bfe_ai_metrics_1m.ai_cache_status` 等，
  Doris 侧 AGGREGATE KEY 重建后存在，见 observability 修改说明 §4.3）。

### 1.2 `buildDistributionSQL` / `distributionNameExpr`：占比分布

- 三新维度占比分布；
- 空值归一：字符串维度空值（`''`）归一为 `"unknown"` 桶；
  `mirror_hit` 为 `"0"` / `"1"` 桶（对齐 mysqlreport 的归一表达式）；
- 分母口径与既有 distribution 一致（窗口内 `SUM(request_count)`）。

### 1.3 `buildTimeSeriesSQL`：按维度拆序列

- 三新维度 × 既有指标集（`qps` / `tokens` / `cache_tokens` / `cost` 等）
  的取数分支；`cache_tokens` 保持 UNION ALL 双臂（`kind` 区分），维度列以
  `name` 带入双臂、ORDER BY 追加维度列（与 mysqlreport 同构）；
- 时间桶渲染沿用 `2026-09-15-report-fixes` 的两后端一致方案
  （`TIMESTAMPDIFF` 算术表达式渲染 Unix 秒），本方案不触碰；
- **`latency` × dimension 的分位数语义**：Doris 单序列 `latency` 额外返回
  的 `p50/p90/p99` 是**整桶口径**（明细表 `PERCENTILE_APPROX` 按桶合并）。
  维度拆序列后一个桶对应多条序列，按桶合并会把整桶分位数错配到单条序列，
  因此 dimension 非空时不附加分位数——该形态仅返回 `avg/max`，与 MySQL 后端
  "恒无分位数"的降级语义对齐，两后端口径一致（前端按字段有无降级，既有约定）。

### 1.4 明细过滤（不涉及）

logs 的缓存/镜像/意图过滤谓词（`cache_status` / `mirror_hit` /
`intent_question` / `intent_answer` / `intent_source`）已由一期
`logWhere` 落地并支持 Doris 方言，本方案不重复实现。

### 1.5 Doris 3.0 分页方言修复（SC36 集成测试实跑发现）

Doris 3.0.8 **无法解析预编译语句中 LIMIT 子句的占位符**：`LIMIT ?,?` 与
`LIMIT ? OFFSET ?` 均在解析器报 `mismatched input 'LIMIT'`（字面量 LIMIT 正常）。
此前 dorisreport 沿用 gendry 的 `_limit` 绑定参数，导致 logs / rankings 查询在
真实 Doris 上不可用（mysqlreport 与既有 harness 以 MySQL 为数据源，均未暴露）。
修复：dorisreport 的 `buildLogsSQL` / `buildRankingsSQL` 将分页改为**整数字面量
内联**（`LIMIT <pageSize> OFFSET <offset>` / `LIMIT <limit>`）——page/offset/limit
均为 manager 钳制后的服务端整数，内联安全且与 `bucketExpr` 内联桶宽的先例一致。
`bfe_ai_request_log.log_time` 为 UTC 墙钟（observability 侧 2026-10-01 时区口径
修复），本仓查询层绑定 UTC 时间窗即可对齐，无需额外改动。

## 2. 能力门控拉平（`model/ireport/types.go` + `storage/dorisreport/report.go`）

1. `dorisreport.Capabilities().SupportedDimensions` 追加：
   `DimensionCacheStatus` / `DimensionMirrorHit` / `DimensionIntentAnswer`；
2. manager 的既有门控逻辑（维度白名单校验后追加后端能力校验，不支持则 422）
   自动放行三新维度，**manager 零改动**；
3. `mysqlreport.Capabilities()` 一期已含三维度，本期不变；
4. 门控移除后，三新维度在 Doris 后端由 422 转为正常下钻，响应结构与
   MySQL 后端一致（前端无感）。

## 3. 配置契约文档化（设计稿 §3.2，零代码改动）

- `conf/ai_gateway_api.toml` 增加两形态样例注释：MySQL 轻量形态
  （`EnableAggregateJob` / `RetentionDays` / `EnablePartitionMgmt` 生效）与
  Doris 标准形态（`Backend = "doris"`，三项 JOB 配置不生效——Doris 分钟聚合由
  数仓侧 INSERT JOB 维护、保留期由两表 `dynamic_partition` 属性自管）；
  Doris 数据源必须显式设 `AllowNativePasswords = true`（`mysql.Config.FormatDSN`
  会把零值序列化为 `allowNativePasswords=false`，Doris FE 的
  mysql_native_password 认证会被拒绝——集成测试实跑发现）；
- `Backend` 取值语义表（缺省 404 / mysql / doris / 非法值启动报错）入档；
- 连接方式说明：Doris 走 FE 的 MySQL 协议端口（9030），作为 `Databases` map
  的普通条目（`Driver = "mysql"`），不引入新客户端；
- `ReportConfig` 与 `initReport()` 的 `case "doris"` 分支**已存在**，
  本方案不引入任何新配置项。

## 4. 契约文档更新（`design-docs/api-define/OpenAPI接口定义/report.md`）

- 总说明去掉 "新维度一期仅 MySQL 后端可用，Doris 返回 422" 表述；
- `timeseries` / `rankings` / `distribution` 的 dimension 枚举表：三新维度
  可用后端标注由 "mysql；doris 422" 改为 "mysql / doris"；
- 错误码表：移除/修订
  `dimension <name> not supported by doris backend (mysql only until doris support lands)`
  条目；
- 保留两后端**既有**固有差异声明：延迟分位数（`latency_p50/p90/p99`）
  仅 Doris 后端返回，MySQL 后端恒无（v0.7 结论，本期不推进 MySQL 分位数）。

## 5. 不变项

- 五端点路由、参数、响应结构、鉴权（`FeatureReport` × `ReadAll`，无权限 402）
  不变；
- 时区口径沿用 `2026-09-15-report-fixes` 方案（`TIMESTAMPDIFF` 算术表达式），
  本方案不触碰；
- `Backend` 缺省不装配（`/report/*` 404）、非法值启动报错的装配行为不变；
- MySQL 侧任何已交付内容（DDL、JOB、mysqlreport）不变；
- 聚合表既有 SUM 指标列与取数口径不变，仅新增 3 个 KEY 维度的消费分支。

## 6. 发布顺序与回滚（摘要）

1. **先决条件**：ai-gateway-observability Doris 侧步骤 1 完成（明细表加列 +
   Routine Load 重建 + 聚合表 `_v2` 重建与 INSERT JOB 更新）；
2. **本仓发布**：dorisreport 新维度分支 + `Capabilities()` 拉平 +
   `report.md` 标注更新。对 `Backend = "mysql"` 与缺省部署零行为变化；
3. **配置切换**：部署方将 `[Report].Backend` 置为 `"doris"` 指向 Doris 数据源
   （样例见 §3），重启生效；反向切换随时可用；
4. **回滚**：本仓查询层可独立回滚（门控退回 422）；Doris 表结构变更向后兼容
   旧版本 api，无需随本仓回滚。

## 7. 测试计划

| 层 | 测试 | 要点 |
|----|------|------|
| 单测 | `storage/dorisreport` 新维度 SQL builder | 方言模板快照（对齐 mysqlreport 已验证模板）；`Capabilities()` 声明含三新维度；`distributionNameExpr` 的 unknown 归一与 0/1 桶 |
| 集成（Backend=doris 装配，MySQL 充当数据源） | 既有 `doris_gate_test.go` 翻转：`doris_dimensions_test.go` 断言三新维度 rankings/distribution/timeseries 返回数据且口径与 MySQL 后端一致（同种子数据逐值比对）；明细能力用例保留 | 三新维度返回数据（不再 422）；空值不进排行 / unknown 桶 / 0/1 桶 / 维度拆序列逐值口径 |
| 集成（真实 Doris，排期） | 仿既有组 B（真实 MySQL 8.4）新增 Doris 组 | 五端点结构与口径断言；logs 新列过滤（`cache_status=hit`、`mirror_hit=true`、`intent_answer=xxx`）；空结果/越权（402）/未装配（404） |
| 数据一致性 | 同一份 pb 日志同时跑「mod_log_mysql → MySQL」与「mod_kafka → Doris」两条链路，API 同窗口比对 | QPS/Token/错误率/三新维度计数误差为 0（同源数据）；分位数按既有差异仅 Doris 侧断言 |
| 回归 | `make test-model-cover-gate`（model/ ≥70%）与 `make test` | 含 `model/ireport` 与两 storager |
