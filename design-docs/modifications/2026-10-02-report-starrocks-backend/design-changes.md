# 报表查询新增 StarRocks 后端 设计变更

> 接口定义见同目录 `api-changes.md`；变更背景与目标见 `change-summary.md`。
> 权威来源：对接设计稿 §6.2 / §7 / §8（StarRocks 相关条目）。
> 平行模板：`../2026-10-02-report-clickhouse-backend/design-changes.md`
> （两后端结构同构，本文只写差异与 StarRocks 专属内容）。

## 1. 代码结构

```
storage/starrocksreport/              # 新增包：StarRocks 实现（纯查询，无 JOB）
├── report.go                         # 五类查询的 SQL 构建与扫描（gendry builder，同 dorisreport）
└── report_test.go                    # 方言模板快照测试（克隆 dorisreport/report_test.go）

stateful/container/rdb/components.go  # initReport() 增加 case "starrocks"（§4）
stateful/config.go                    # ReportConfig.Backend 注释枚举扩展（四取值）
model/ireport/types.go                # BackendCaps.Backend 注释枚举扩展
conf/ai_gateway_api.toml              # 新增 StarRocks 形态注释样例（§3）
design-docs/api-define/OpenAPI接口定义/report.md   # 后端支持标注更新（见 api-changes.md）
design-docs/sys-design/details/报表查询模块.md     # 四后端标注更新
```

与 clickhousereport 的差异根源：SR 走 **MySQL 协议 + gendry builder**（与 dorisreport
完全同机制），因此 starrocksreport 是 dorisreport 的**同源克隆**（而非 CH 那样的
fmt 模板重写）；clickhousereport 的 CH 专属陷阱（flatten_nested、别名遮蔽、
toJSONString、quantile 三列、NaN 守卫）**均不适用**。

## 2. 驱动与数据源（零改动）

- `DbConfig` / `FormatDSN()` / `NewDB()`：**零改动**；`[Databases.starrocks_db]`
  以 `Driver = "mysql"` 走现有全部路径（go-sql-driver/mysql + gendry 均已在 go.mod）；
- `Addr` 填 SR FE 的 **9030**；`AllowNativePasswords = true` 必须显式开启
  （与 Doris 段同坑：`mysql.Config.FormatDSN` 零值序列化为 false 导致
  mysql_native_password 认证被拒）；
- 连接池参数语义不变；`DbGet(name)` 按名取连接，装配层无感知。

## 3. 数据源与配置

`conf/ai_gateway_api.toml` 新增第四形态注释样例（既有三段不变）：

```toml
# ===== 报表基于 StarRocks（本方案新增后端） =====
[Databases.starrocks_db]
Driver = "mysql"              # StarRocks FE MySQL-protocol port, 零新驱动
DBName = "bfe_observability"
Addr = "127.0.0.1:9030"       # SR FE query_port
User = "report_read"          # 查询账号，仅 SELECT 即可
Passwd = "******"
AllowNativePasswords = true   # 必须显式开启（同 doris 段说明）
MaxOpenConns = 20
MaxIdleConns = 5

[Report]
Backend = "starrocks"         # 报表基于 StarRocks（本方案新增后端）
Datasource = "starrocks_db"
Database = ""                 # 库名覆盖（可选；表名支持 db.table 前缀）
EnableAggregateJob = false    # 以下三项仅 backend=mysql 时生效；
AggregateIntervalSec = 60     # StarRocks 分钟聚合由异步物化视图维护，
RetentionDays = 7             # 保留期由动态分区属性 + partition_ttl 自管
EnablePartitionMgmt = false
```

`Backend` 取值语义扩展（`stateful/config.go` `ReportConfig` 结构零改动，仅注释更新）：

| 取值 | 装配行为 | 后台 JOB |
|------|----------|----------|
| `"mysql"` | mysqlreport + 可选 JOB | 有（既有） |
| `"doris"` | dorisreport（纯查询） | 无（Doris INSERT JOB 承担，既有） |
| `"clickhouse"` | clickhousereport（纯查询） | 无（MV + TTL 承担，既有） |
| `"starrocks"` | starrocksreport（纯查询） | 无（异步 MV + 动态分区承担） |
| `""` | 不装配，`/report/*` 404 | — |
| 其他 | 启动失败 `unsupported [Report].Backend` | — |

数据源最小权限：查询账号仅 `bfe_observability` 库两表 `SELECT`。

## 4. 装配（`stateful/container/rdb/components.go` `initReport()`）

现有 switch 新增 case（位于 `case "clickhouse"` 之后）：

```go
case "starrocks":
    // StarRocks aggregation is maintained by the warehouse-side async
    // materialized view; retention by dynamic_partition + partition_ttl.
    // The api only queries, no local job (see design-docs
    // modifications/2026-10-02-report-starrocks-backend).
    container.ReportManager = ireport.NewReportManager(starrocksreport.New(db, cfg.Database, "starrocks"))
```

- `BackendCaps.Backend = "starrocks"`；`SupportedDimensions` 声明**全量 12 维**
  （`model/ireport/types.go` 现有 12 个 `Dimension*` 常量全声明；SR 聚合侧 40 维齐全，
  含 cache/mirror/intent 三维，无需门控降级）；
- manager 门控逻辑零改动自动放行；`Backend = ""` 不装配 404、非法值启动报错不变。

## 5. SQL 方言（与 dorisreport 逐点核对）

### 5.1 构造机制

与 dorisreport **完全同构**：gendry `builder.BuildSelect`、where 键带操作符、
`_groupby`/`_orderby` 特殊键；沿用三类字面量内联纪律（bucketSec、LIMIT/OFFSET、
UNION ALL kind）；扫描层（rowToMetricPoint / overviewResultFromRow / scanLogRow /
CostFixedPointToAmount）逐字克隆。

### 5.2 方言对照表（对接设计稿 §6.2，相对 dorisreport 现状逐点核对）

| 构造 | Doris（现状 dorisreport） | StarRocks | 说明 |
|------|---------------------------|-----------|------|
| 时间桶 | `CAST(FLOOR(TIMESTAMPDIFF(SECOND,'1970-01-01 00:00:00',ts_min)/<b>)*<b> AS SIGNED)`（2026-10-02 修复后） | **同（逐字复用）** | MySQL 函数集兼容；SIGNED 在 SR 的接受度为实施验证门（§8 风险 1） |
| 时间过滤 | `ts_min >= FROM_UNIXTIME(?)` | 同 | unix 秒入参绑定 |
| 分位数 | `PERCENTILE_APPROX(all_time, 0.5/0.9/0.99)` | `percentile_approx(all_time, 0.5/0.9/0.99)` | 同名函数小写即可（SR 函数名大小写不敏感，模板保持小写风格统一） |
| 空串谓词 | `col != ''` | 同 | SR 聚合表 40 维 NOT NULL DEFAULT ''（同 Doris 口径） |
| LIMIT/OFFSET | 字面量内联 | 同 | 同纪律 |
| 维度转字符 | `CAST(col AS CHAR)` | `CAST(col AS CHAR)` | SR 支持 |
| distribution 空值归一 `"unknown"` 桶 | CASE 表达式 | 同构 CASE | 口径一致 |
| `cache_tokens` UNION ALL 双臂 | 有 | 同（SR 支持 UNION ALL） | kind 字面量内联 |
| 表名 schema 前缀 | `db.table` | 同 | `table()` 同构 |
| 分桶边界 | manager 既有 `BucketSeconds()` | 同（manager 不变） | |

结论：**starrocksreport 的 SQL 文本与当前 dorisreport 近乎逐字一致**
（差异收敛于函数名大小写风格与 backend 标识），快照测试克隆后仅需替换包名与
`Capabilities().Backend` 断言。这就是对接设计稿 §6.2"同源克隆 + 上表差异点修正，
风险低"的落地形态。

### 5.3 时区纪律

与 Doris 同款：`FROM_UNIXTIME(?)` 绑定 unix 秒、出参 unix 秒桶；SR 会话时区
不影响 epoch 语义。调试 SQL 渲染时间字符串时显式指定时区（运维侧约束）。

## 6. 与 StarRocks 数仓侧资产的对接要点（查询层视角）

| 资产（observability 已交付） | api 侧影响 |
|------------------------------|------------|
| 明细表 `bfe_ai_request_log`：DUPLICATE KEY 四列前缀、RANGE 动态分区（-7/+3）、复杂列原生 `ARRAY<STRUCT>` | 五端点明细路径（logs 投影 47 列、overview 明细计数、分位数）按既有列名直读；**复杂列直接投影**（MySQL 协议返回 JSON 文本预期，§8 风险 2 验证） |
| Routine Load `bfe_ai_log_load_routine`（json 打平列 + UTC 墙钟换算） | 无（写入侧）；查询谓词 `col != ''` 等口径与打平结果一致（同 Doris） |
| 异步 MV `bfe_ai_metrics_1m`（`REFRESH ASYNC EVERY 1 MINUTE`，物化粒度 40 维全键，`partition_ttl = "7 DAY"`） | 聚合表读法不变：按维度 GROUP BY + SUM(指标) 折叠，MV 物化行粒度与 Doris 聚合表一致，直查安全；端到端新鲜度 1~2 分钟（与 Doris JOB 同量级） |
| 演进约束：基表加列后 MV 需重建才纳入新维度 | 与 Doris AGGREGATE KEY 重建心智一致，api 按既有列读取无额外负担 |

## 7. 测试计划

| 层 | 测试 | 要点 |
|----|------|------|
| 单测 | `starrocksreport` SQL builder | 克隆 dorisreport/report_test.go 快照（`assertSnapshot` 全串锁定 SQL + args）；`Capabilities()` 声明全量 12 维断言；`Backend="starrocks"` 标识断言；重点锁定：`CAST AS SIGNED` 桶表达式、`percentile_approx` 小写、UNION ALL 双臂、LIMIT/OFFSET 内联 |
| 集成 | 两组（`tests/report/query/starrocks_test.go`，仿 clickhouse_test.go）：**组 1 借 MySQL**（`REPORT_MYSQL_DSN` 门，走 `StartReportServerWithBackend("starrocks", …)`）：gendry SQL 须在 MySQL 8.4 真实执行（dorisreport 已证明可行，SIGNED 修复后），断言五端点数据与 MySQL 组手算值一致；**组 2 真实 SR**（新环境变量 `REPORT_STARROCKS_DSN` + `REPORT_STARROCKS_DDL_DIR`，testutil 新增 helper：临时库 + observability `starrocks/sqls` DDL + 种子，占位符 `${STARROCKS_DATABASE}`/`${INIT_PARTITION_DATE}` 替换） | 组 2 五端点结构与口径断言（同 CH 组种子手算值）；logs 复杂列内容断言（req_headers JSON 文本形态）；latency 分位数字段存在性（percentile_approx 近似，不锁微小数据集精确值）；空窗口分位数省略行为；组 1/组 2 与 Doris/CH 组**错峰**（SR 与 Doris 端口互斥，真实实例组一次只跑一个） |
| 回归 | `make test-model-cover-gate`（model/ ≥70%）与全仓 `go test ./...` | 含 model/ireport 与全部 storager |

## 8. 风险（api 侧）

1. **~~`CAST AS SIGNED` 在 StarRocks 3.5.21 的接受度~~（已实机验证通过，2026-10-02）**：
   本机 SR 3.5.21 实测 `CAST(FLOOR(TIMESTAMPDIFF(...)/300)*300 AS SIGNED)` 正常返回
   （1915696800），`percentile_approx` 对 6 值种子精确返回中位数 150——**维持 SIGNED，
   无需回退 BIGINT**；借-MySQL 集成组的数据断言模式成立。记录：MySQL 8.4 仅接受
   SIGNED/UNSIGNED（2026-10-02 dorisreport 修复已证实 BIGINT 在 MySQL 非法），SR 两者均接受。
2. **~~logs 复杂列线格式~~（已实机验证通过，2026-10-02）**：SR `ARRAY<STRUCT>` 经
   MySQL 协议 SELECT 返回 **JSON 文本**（实测 `[{"key":"X-Test","value":"1"},...]`，
   空数组为 `[]`），与 Doris JSON 列线格式一致，扫描层 NullString 直收，**无需
   to_json 包装**。同时实机确认种子灌入语法：裸 `CAST(varchar AS ARRAY<STRUCT>)` 报
   Not support cast，**必须 `CAST(parse_json('...') AS ARRAY<STRUCT<...>>)`**——
   集成测试 fixture 按此构造（与 observability MV 的还原手法一致）。
3. **`mysql.Config.Net` 零值陷阱（实施期实机发现，已处理）**：`Net` 为空时
   `FormatDSN` 丢弃整个 `protocol[(address)]` 段，驱动回退 `127.0.0.1:3306`——
   借-MySQL 组因目标恰为 3306 而"侥幸可用"，直连 9030 必败。conf starrocks 样例已补
   `Net = "tcp"` 并注明。doris 注释样例同款隐患已由
   [2026-10-02-report-mysql-driver-upgrade](../2026-10-02-report-mysql-driver-upgrade/change-summary.md)
   补齐。
4. **FE 二进制协议宽结果集缺陷（实施期实机发现，已修正归因并规避）**：
   logs 47+ 列投影 + 绑定参数走 COM_STMT 二进制协议时，SR FE（MySQL 协议重实现）的
   行包编码在 JSON 列与 NULL 列相邻的行上畸形——驱动 v1.6.0 表现为 ''/NULL 静默串位，
   v1.9.3 表现为 packets.go readRow panic（升级验证时实锤，探针二分定位，见
   [2026-10-02-report-mysql-driver-upgrade](../2026-10-02-report-mysql-driver-upgrade/change-summary.md)
   §问题 3）。**同一 FE 缺陷的两种症状，与驱动版本无关**；文本协议（CLI/无参查询/
   `InterpolateParams = true`）下 FE 编码正确。doris / starrocks 后端的 logs 端点在
   **生产同样触发**，SR 生产配置必须开启 `InterpolateParams`（已写入 conf starrocks
   段样例）；根治需 SR FE 修复（拟提社区 issue）。（ClickHouse 后端因 clickhouse-go
   独立驱动不受此缺陷影响。）
5. **异步 MV 新鲜度**：`REFRESH ASYNC EVERY 1 MINUTE` + 分区对齐，端到端 1~2 分钟
   （与 Doris JOB 同量级）；集成测试采用单源种子（只灌基表明细，由 MV 聚合）+
   起 api 前轮询 MV 行数（2s 间隔、150s 上限，本机实测 3~34s）消除时序 flaky。
6. **环境互斥**：SR 与 Doris 同端口，真实实例组与 Doris 组不可同跑；借-MySQL 组不受
   影响（不占用 9030）。
7. **无 JOB 与配置语义**：三个 JOB 配置项对 starrocks 后端不生效（既有语义延伸，
   conf 样例已注明）；运维勿误以为 api 会代管 SR 聚合/保留。
8. **回滚耦合点**：`Backend = "starrocks"` 配置与老版本 api 不兼容（启动报错），
   配置回滚必须与 api 回滚同步（与 ClickHouse 变更同款，唯一回滚耦合点）。

## 9. 发布顺序

1. **StarRocks 数仓侧资产**：已交付（ai-gateway-observability `starrocks/`，
   HOWTO 一键部署，starrocks-it 全绿）——本方案前置已完成；
2. **ai-gateway-api 发布**（本变更）：starrocksreport + `initReport()` case +
   toml 注释样例 + `report.md` / `报表查询模块.md` 标注更新；对既有三种取值及
   缺省部署**零行为变化**（纯增量 case）；
3. **配置切换**：目标部署将 `[Report].Backend` 置为 `"starrocks"` 并指向
   `starrocks_db` 数据源（§3），重启生效；反向切换随时可用；
4. **回滚**：api 查询层独立回滚，但 `Backend = "starrocks"` 配置在老版本会启动
   报错——配置回滚须与 api 回滚同步；SR 侧资产与 Routine Load 消费位点保留，
   重新升级时从断点续传无重放。

## 10. 参考文档

- 权威设计稿：《数据报表-ClickHouse 与 StarRocks 对接设计方案》（v0.8 迭代系统设计稿）§4 StarRocks 数仓资产、§6.2 查询层改动、§9.2 版本能力确认项
- 平行模板：`../2026-10-02-report-clickhouse-backend/`（结构、两组集成测试、方言陷阱清单）
- dorisreport SIGNED 修复先例：`70181b9 fix(report-doris): 时间桶 CAST 由 BIGINT 改为 SIGNED`
- 代码依据：`model/ireport/types.go`、`stateful/config.go`、`stateful/config_database.go`、`stateful/container/rdb/components.go`、`storage/dorisreport/report.go` + `report_test.go`、`storage/clickhousereport/`（机制反例）、`test/integration/testutil/report_server.go`
- 数仓依据：ai-gateway-observability `starrocks/sqls/`（`bfe_observability.sql`、`bfe_ai_request_log.sql`、`bfe_ai_metrics_1m.sql`、`bfe_ai_log_load_routine.sql`）、`starrocks/docs/design/TABLE_DESIGN.md`、`api/depends_api/req_log.md`
- 环境依据：`environment/starrocks-installation.md`（3.5.21，MySQL 协议 9030，**与 Doris 端口互斥**）
