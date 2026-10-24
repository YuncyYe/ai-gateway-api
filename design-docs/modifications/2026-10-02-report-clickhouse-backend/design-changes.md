# 报表查询新增 ClickHouse 后端 设计变更

> 接口定义见同目录 `api-changes.md`；变更背景与目标见 `change-summary.md`。
> 权威来源：对接设计稿 §3.3 / §6.3 / §7 / §8（ClickHouse 相关条目）。
> 代码依据以当前工作区状态为准（勘察日期 2026-10-02）。

## 1. 代码结构

```
storage/clickhousereport/             # 新增包：ClickHouse 实现（纯查询，无 JOB）
├── report.go                         # 五类查询的 SQL 构建与扫描（fmt 模板 + 白名单校验）
└── report_test.go                    # 方言模板快照测试（仿 dorisreport/report_test.go）

stateful/config_database.go           # FormatDSN() 增加 case "clickhouse"（§2）
stateful/container/rdb/components.go  # initReport() 增加 case "clickhouse"（§4）
stateful/config.go                    # ReportConfig.Backend 注释枚举扩展为 mysql|doris|clickhouse
model/ireport/types.go                # BackendCaps.Backend 注释枚举扩展（types.go:331）
conf/ai_gateway_api.toml              # 新增 ClickHouse 形态注释样例（§3）
design-docs/api-define/OpenAPI接口定义/report.md   # 后端支持标注更新（见 api-changes.md）
```

结构对齐 `storage/dorisreport/`（report.go 单文件 + 快照单测，无 job.go）。
`New(db *sql.DB, database string, backend string)` 构造函数签名与 dorisreport 同构，
`backend` 参数取 `"clickhouse"` 写入 `Capabilities().Backend`。

## 2. 驱动接入（`stateful/config_database.go`）

### 2.1 现状

`DbConfig`（config_database.go:33-42）匿名嵌入 `mysql.Config`（go-sql-driver 的 DSN 结构，
TOML 平铺映射）+ `Driver` + 连接池字段。`FormatDSN()`（config_database.go:44-56）的
driver 白名单硬编码仅 `mysql` / `sqlite`（常量 `DriverMySQL` / `DriverSQLite`，
config_database.go:28-31）。Doris 未单列——复用 `Driver = "mysql"` 走 FE 9030。

### 2.2 改动

新增常量与 case（`NewDB()` 的 `sql.Open(driverName, dsn)` 对 `"clickhouse"`
直通，sqlite 特判之外无 driver 名改写，连接池参数语义不变）：

```go
const DriverClickHouse = "clickhouse"

// FormatDSN() 新增：
case DriverClickHouse:
    // 仅复用嵌入 mysql.Config 的 Addr/User/Passwd/DBName 四字段组装 native DSN；
    // mysql 专有字段（TLS、timeout、Loc 等）不映射（§8 风险 5）。
    if c.Config.Addr == "" || c.Config.User == "" || c.Config.DBName == "" {
        return "", fmt.Errorf("clickhouse Addr/User/DBName are required")
    }
    return fmt.Sprintf("clickhouse://%s:%s@%s/%s?dial_timeout=10s&compress=lz4",
        c.Config.User, c.Config.Passwd, c.Config.Addr, c.Config.DBName), nil
```

- **协议端口**：`Addr` 填 native TCP **9000**（8123 是 HTTP 端口，clickhouse-go stdlib
  用 native 协议）；
- **版本**：`github.com/ClickHouse/clickhouse-go/v2 v2.40.0`——与 observability
  clickhouse-it 集成测试已验证版本对齐，stdlib 模式对接 `database/sql`；
- **DSN 组装不经过 `mysql.Config.FormatDSN()`**，因此不存在 Doris 形态
  `AllowNativePasswords` 零值序列化的坑（conf 注释已记录该坑，CH 形态天然免疫）；
- **驱动注册**：`clickhouse-go/v2` 以 blank import 方式在
  `storage/clickhousereport` 包内注册（report.go），`stateful` 的
  `sql.Open("clickhouse", …)` 即插即用；
- **不用 MySQL 兼容端口（9004）**：官方定位"尽力而为"，类型系统与函数支持有限，
  函数方言仍须独立 builder，省不下查询层工作量（change-summary.md §5）。

## 3. 数据源与配置

`conf/ai_gateway_api.toml` 新增第三形态注释样例（既有 mysql / doris 两段不变）：

```toml
# ===== 报表基于 ClickHouse（本方案新增后端） =====
[Databases.clickhouse_db]
Driver = "clickhouse"         # 新增 driver：clickhouse-go/v2 stdlib native TCP（go.mod 变更，见 §8）
DBName = "bfe_observability"
Addr = "127.0.0.1:9000"       # native TCP 端口；8123 为 HTTP
User = "report_read"          # 查询账号，仅 SELECT 即可
Passwd = "******"
MaxOpenConns = 20
MaxIdleConns = 5

[Report]
Backend = "clickhouse"        # 报表基于 ClickHouse（本方案新增后端）
Datasource = "clickhouse_db"
Database = ""                 # 库名覆盖（可选，默认取数据源的 DBName；表名支持 db.table 前缀）
EnableAggregateJob = false    # 以下三项仅 backend=mysql 时生效；
AggregateIntervalSec = 60     # ClickHouse 分钟聚合由消费明细的物化视图同步维护，
RetentionDays = 7             # 保留期由两表 TTL 表达式自管
EnablePartitionMgmt = false
```

`Backend` 取值语义扩展（`stateful/config.go` `ReportConfig` 结构零改动，仅注释更新）：

| 取值 | 装配行为 | 后台 JOB |
|------|----------|----------|
| `"mysql"` | mysqlreport + 可选聚合/分区 JOB | 有（既有） |
| `"doris"` | dorisreport（纯查询） | 无（Doris CREATE JOB 承担，既有） |
| `"clickhouse"` | clickhousereport（纯查询） | 无（CH 物化视图 + TTL 承担） |
| `""` | 不装配，`/report/*` 404 | — |
| 其他 | 启动失败 `unsupported [Report].Backend` | — |

数据源最小权限：查询账号仅 `bfe_observability` 库两表 `SELECT`（CH 侧聚合与 TTL
均为库内机制，api 进程零写入）。

## 4. 装配（`stateful/container/rdb/components.go` `initReport()`）

现有 switch（components.go:416-454）新增 case，位置在 `case "doris"` 之后：

```go
case "clickhouse":
    // ClickHouse 聚合由数仓侧物化视图维护、保留期由 TTL 自管，api 只查；无本地 JOB
    container.ReportManager = ireport.NewReportManager(clickhousereport.New(db, cfg.Database, "clickhouse"))
```

- `BackendCaps.Backend = "clickhouse"`；`SupportedDimensions` 声明**全量**
  （`model/ireport/types.go` 现有 12 个 `Dimension*` 常量全声明，
  CH 聚合表 40 维齐全，含 cache/mirror/intent 三维，无需门控降级）；
- manager 的既有门控逻辑（白名单校验 + 后端能力校验）零改动自动放行；
- `Backend = ""` → `return nil`（不装配）、非法值 → 启动报错的既有行为不变。

## 5. SQL 构造与方言映射（核心交付物）

### 5.1 构造纪律

- **gendry 不可用于 CH**（MySQL 方言 builder）——clickhousereport 用
  **fmt 模板 + 白名单校验**构造 SQL，扫描沿用 `database/sql` 标准行扫描；
- **CH 别名遮蔽规则（实测 26.10 验证）**：SELECT 别名会屏蔽同名列并作用于整个查询——
  `SUM(all_time_sum) AS all_time_sum` 使后续任何 `all_time_sum` 引用（如
  `MAX(all_time_sum/request_count) AS latency_max`）解析到聚合自身，报
  "Aggregate function ... is found inside another aggregate function"。
  因此**外层聚合别名一律不得与内层列同名**，本包统一加 `_total` 后缀
  （`all_time_sum_total` / `request_count_total` / `input_tokens_total` …，
  含 rankings/distribution 的排序键引用）；扫描层按位置取值，别名变更零影响；
- 沿用 dorisreport 的字面量内联纪律，**仅三类值允许内联**（均经 manager 校验/clamp
  后拼入，dorisreport 对应位置见 report.go:221-224、464-468、583-587）：
  时间桶宽 `bucketSec`、LIMIT/OFFSET、UNION ALL 的 `'cache_read'`/`'cache_write'`
  kind 字面量；**字符串值一律绑定参数**（`?`）；
- 聚合表读取纪律：CH 聚合表为 SummingMergeTree（排序键全 40 维，observability
  已交付资产），merge 只折叠真正同维组合的行——**所有查询按"GROUP BY 维度 +
  SUM(指标)"构造，禁止 `SELECT *` 直读聚合表**（对接设计稿 §5.4 冒烟实测结论）。

### 5.2 方言映射表（相对 dorisreport 逐点核对）

| 构造 | Doris（现状 report.go） | ClickHouse | 说明 |
|------|--------------------------|------------|------|
| 时间桶 | `CAST(FLOOR(TIMESTAMPDIFF(SECOND,'1970-01-01',ts_min)/<b>)*<b> AS BIGINT)` | `intDiv(toUnixTimestamp(ts_min), <b>) * <b>` | unix 秒算术，无时区歧义 |
| 时间过滤（聚合表） | `ts_min >= FROM_UNIXTIME(?)` | `ts_min >= fromUnixTimestamp(?)` | CH 内部按 epoch 比较，列存 UTC 与函数返回值语义一致 |
| 分位数（仅 overview/latency 时查明细表） | `PERCENTILE_APPROX(all_time, 0.5/0.9/0.99)` | 三个独立列 `quantile(0.5)(all_time)` / `quantile(0.9)(...)` / `quantile(0.99)(...)` | t-digest 近似，与 PERCENTILE_APPROX 同类算法族，跨引擎数值"近似一致"，比对测试给容差（§6）。**实现取三独立列而非 `quantiles()` 元组**：`database/sql` 扫描元组不可靠；同为 t-digest 单次遍历，语义等价。另注：CH 对空输入的 `quantile` 返回 NaN（Doris 返回 NULL），扫描层加 `math.IsNaN` 守卫，空窗口行为与 Doris 对齐（字段省略） |
| 空串谓词 | `col != ''` | 同（聚合表 40 维全 NOT NULL `DEFAULT ''` 天然兼容；可空列包 `ifNull`） | 与 Doris 侧"聚合维度空值归一"口径对齐 |
| LIMIT/OFFSET | 字面量内联（Doris 不支持绑定） | `LIMIT <size> OFFSET <off>` 字面量内联 | 同纪律 |
| 维度转字符（rankings/distribution） | `CAST(col AS CHAR)` | `toString(col)` | |
| distribution 空值归一 `"unknown"` 桶 | CASE 表达式 | 同构 CASE（`if(col = '', 'unknown', toString(col))`） | 与 dorisreport `distributionNameExpr` 口径一致 |
| `cache_tokens` UNION ALL 双臂 | 有（report.go:380-402） | 同（CH 支持 UNION ALL） | kind 字面量内联 |
| 表名 schema 前缀 | `db.table` | 同 | `table()` 方法同构 |
| 分桶边界 | manager 既有 `BucketSeconds()`（60/300/1800s） | 同（manager 不变） | |

### 5.3 时区纪律

- api 入参为 unix 秒、出参为 unix 秒桶，CH 比较按 epoch 天然免疫服务器会话时区；
- 任何面板/调试 SQL 渲染时间字符串时必须显式 `toDateTime(x, 'UTC')`，
  禁止依赖会话时区（CH 侧运维 SQL 同约束，已在对端 HOWTO 声明）。

### 5.4 查询路径映射（与 dorisreport 一一对应）

| dorisreport builder | ClickHouse 对应 | 表 |
|---------------------|------------------|----|
| `buildOverviewMetricsSQL` / `buildOverviewDetailCountsSQL` / `buildOverviewCostSQL` | 同构三查询 | 聚合表 / 明细表 / 聚合表按 `ai_cost_currency` 分组 |
| `buildOverviewPercentileSQL` / `buildLatencyPercentileSQL` | `quantile(0.5/0.9/0.99)(all_time)` 三独立列（t-digest） | 明细表（latency 时查） |
| `buildTimeSeriesSQL` / `buildCacheTokensTimeSeriesSQL` | 同构（UNION ALL 双臂） | 聚合表 |
| `buildRankingsSQL`（含 `rankingEmptyPredicate`） | 同构，空值谓词绑参 | 聚合表 |
| `buildDistributionSQL`（含 `distributionNameExpr`） | 同构 CASE 归一 | 聚合表 |
| `buildLogsCountSQL` / `buildLogsSQL`（`logRowFields` 47 列投影） | 同构投影；`log_time` 输出改用 `toUnixTimestamp(log_time)`；四个复杂列渲染为 JSON 文本——clickhouse-go stdlib 将 Nested/Array 值作为 Go slice 返回，`database/sql` 无法扫描进字符串（MySQL/Doris 侧这些列本就是 JSON 文本）。**物理 schema 要点（实测 26.10 验证）**：服务端 `flatten_nested` 默认 1，`Nested(key,value)` 建表后落为平行子列 `req_headers.key`/`req_headers.value`（基名只是虚拟投影，observability 侧 MV 已按子列展开），故投影必须 `toJSONString(CAST(arrayMap((k, v) -> (k, v), req_headers.key, req_headers.value) AS Array(Tuple(key String, value String)))) AS req_headers` 重组为具名 Tuple 数组，输出形态与 MySQL JSON 文本一致；`ai_rate_limit_hits`（Array(Tuple)）与 `ai_auth_reject_quota_plans`（Array(String)）不受 flatten 影响，`toJSONString(col)` 直出；空数组渲染 `'[]'`（MySQL 侧为 NULL，语义等价） | 明细表 |
| `metricsWhere` / `detailWhere` / `logWhere` | 同构 WHERE 链 | — |

logs 明细投影的 `log_time` 列：dorisreport 用
`TIMESTAMPDIFF(SECOND, '1970-01-01 00:00:00', log_time)` 保证时区中立
（report.go:519-569），CH 形态用 `toUnixTimestamp(log_time)`（列存 UTC，
返回 epoch），两者语义等价且 CH 侧更简。

## 6. 测试计划

| 层 | 测试 | 要点 |
|----|------|------|
| 单测 | `clickhousereport` SQL builder | 方言模板快照（`assertSnapshot` 全串锁定 SQL + args，仿 dorisreport/report_test.go 模式）；`Capabilities()` 声明全量维度断言；`intDiv`/`toString`/`quantiles`/`fromUnixTimestamp` 模板正确性；空串谓词；LIMIT/OFFSET 内联；UNION ALL kind 字面量 |
| 集成 | 两组（tests/report/query/clickhouse_test.go）：组 1 借 MySQL 实例（`REPORT_MYSQL_DSN` 门，未设置 Skip）走 `StartReportServerWithBackend("clickhouse", …)`，断言装配后路由已注册（参数校验 422、有效请求精确 500 区别于未装配 404——CH 方言 SQL 在 MySQL 上必失败，与 doris 组"MySQL 兼容子集可断言数据"分界）；组 2 真实 CH 端到端（新环境变量 `REPORT_CLICKHOUSE_DSN` + `REPORT_CLICKHOUSE_DDL_DIR`，testutil 新增 `StartClickHouseReportServer`：临时库 + observability DDL + CH 兼容种子，用完 DROP） | 组 2 五端点结构与口径断言（聚合种子手算值与 MySQL/doris 组一致；CH 种子因 TTL 7 天将时间平移 2026→2030、Nested 列置 NULL，偏差见 clickhouse_seed_test.go 头注释）；latency 分位数只断言字段存在（t-digest 微小数据集不保证精确）；logs 覆盖 toJSONString 修复后的复杂列投影 |
| 双引擎口径比对 | 同一份 demo 消息集同时灌 Doris / ClickHouse（两消费组各收一份，observability demo/ 三个样例 normal/rate_limit/auth_reject），同窗口五端点比对 | QPS/Token/错误率/限流/成本误差为 0；分位数（Doris PERCENTILE_APPROX / CH quantile 均为近似算法）断言相对误差 ≤ 5% 且量级一致 |
| 回归 | `make test-model-cover-gate`（model/ ≥70%）与全仓 `make test` | 含 `model/ireport` 与全部 storager；`stateful` 的 FormatDSN 新 case 需补单测 |

## 7. 发布顺序

1. **ClickHouse 数仓侧资产**：已交付（ai-gateway-observability `clickhouse/`，
   测试环境按 HOWTO 一键部署；clickhouse-it 全绿）——本方案的前置已完成；
2. **ai-gateway-api 发布**（本变更）：clickhousereport + `FormatDSN()` case +
   `initReport()` case + toml 注释样例 + `report.md` 标注更新（见 api-changes.md）；
   该版本对 `Backend = "mysql" | "doris"` 及缺省部署**零行为变化**（纯增量 case）；
3. **配置切换**：目标部署将 `[Report].Backend` 置为 `"clickhouse"` 并指向
   `clickhouse_db` 数据源（§3），重启生效；反向切换随时可用；
4. **回滚**：api 查询层独立回滚，**但** `Backend = "clickhouse"` 配置在老版本
   api 会启动报错——配置回滚须与 api 回滚同步（唯一回滚耦合点）；
   CH 侧存储资产与 Kafka 消费位点保留，重新升级时消费组从断点续传无重放。

## 8. 风险（api 侧）

1. **clickhouse-go 新依赖**：go.mod/go.sum 变更单独评审（版本 v2.40.0 对齐
   observability 已验证版本、license Apache-2.0、体积）；stdlib 模式与
   `database/sql` 语义（连接池、Prepare 行为）在集成测试中覆盖；
2. **ClickHouse 内存**：测试环境实例限 1G（environment 文档已记录），大窗口 +
   明细分位数直查会触发 `MEMORY_LIMIT_EXCEEDED`；生产部署建议
   `max_memory_usage` / `max_bytes_before_external_group_by` 入 HOWTO 生产建议，
   测试环境用例控制窗口大小；
3. **分位数为近似值**：`quantile()`（t-digest）与 Doris `PERCENTILE_APPROX`
   跨引擎为"近似一致"，比对测试按 §6 容差断言，api 响应不承诺跨引擎数值相等；
4. **at-least-once 重复消费**：CH Kafka 引擎 + MV 在提交位点前失败会重投，
   重复行被 SummingMergeTree 重复累计——数据侧风险（observability 已评估：
   日志统计场景接受小概率重复），api 侧无适配，监控与重算兜底在数仓侧；
5. **`mysql.Config` 嵌入结构的复用边界**：clickhouse case 只读取
   `Addr`/`User`/`Passwd`/`DBName` 四字段，其余 mysql 专有字段不映射也不报错
   （静默忽略），conf 样例与本文档须明确注明，避免运维误以为 TLS 等配置生效；
6. **单集群单落库约束**：Doris / ClickHouse 两消费组并存仅用于灰度比对期，
   生产仍按既定约束单落库；
7. **MV 演进约束**：CH 明细表加列后，聚合 MV 需重建才能纳入新维度——
   与 Doris AGGREGATE KEY 重建心智一致（observability 侧修改说明已记录），
   api 查询层按既有列读取，无额外负担。

## 9. 参考文档

- 权威设计稿：《数据报表-ClickHouse 与 StarRocks 对接设计方案》（v0.8 迭代系统设计稿，2026-10-01）§5 ClickHouse 数仓资产、§6.3 查询层改动
- 代码依据：`model/ireport/types.go`（`ReportStorager` / `BackendCaps` / `Dimension*`）、`stateful/config.go`（`ReportConfig`）、`stateful/config_database.go`（`DbConfig.FormatDSN()`）、`stateful/container/rdb/components.go`（`initReport()`）、`storage/dorisreport/report.go` + `report_test.go`、`test/integration/testutil/report_server.go`
- 数仓依据：ai-gateway-observability `clickhouse/sqls/`（`bfe_ai_request_log.sql`、`bfe_ai_log_kafka.sql`、`bfe_ai_log_load_mv.sql`、`bfe_ai_metrics_1m.sql`、`bfe_ai_metrics_1m_mv.sql`）、`clickhouse/docs/design/TABLE_DESIGN.md`、`clickhouse/docs/modifications/2026-10-01-clickhouse-dock/design-changes.md`、`api/depends_api/req_log.md`
- 环境依据：`environment/clickhouse-installation.md`（ClickHouse 26.10.1.1149，HTTP 8123 / Native 9000）
