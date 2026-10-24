# 报表 MySQL 驱动升级 v1.9.3 + 配置样例补 Net/InterpolateParams 变更摘要

> 本变更处理 `2026-10-02-report-starrocks-backend` 实施期实机发现、记录在案的两个遗留问题：
> ① go-sql-driver v1.6.0 宽结果集 NULL 位图 bug 的根治（升级驱动）；② conf 样例 doris 段
> 补 `Net = "tcp"`（mysql.Config 零值陷阱）。升级后的验证过程中**新实锤了 StarRocks FE
> 二进制协议的服务端缺陷**，SR 形态维持文本协议（InterpolateParams）方案，详见 §2 问题 3。

## 1. 背景

### 问题 1：go-sql-driver v1.6.0 宽结果集 NULL 位图 bug（驱动侧，已根治）

- **现象**：`/report/logs` 端点（49 列投影）在"绑定参数 + 宽结果集"（COM_STMT 二进制协议）
  下，行内 `''`/NULL 串位、个别非空值丢为 NULL。mysql CLI 与无参查询走 COM_QUERY 文本协议，
  结果正确；同一查询用 v1.9.x 驱动对真实 MySQL 8.4 全部正确，确系驱动 bug。
- **根因**：v1.6.0 对 COM_STMT 二进制协议结果集 NULL 位图的解析错位。
- **影响面**：api 生产部署的 mysql 后端及 mysql-driver 形态后端（doris/starrocks）的
  logs 宽投影查询；升级后 **MySQL 后端该缺陷已根治**（集成 MySQL 组 v1.9.3 二进制协议
  全绿）。

### 问题 2：conf 样例 doris 段缺 `Net = "tcp"`（配置样例缺陷，已补齐）

- **现象**：`mysql.Config.Net` 为零值时 `FormatDSN` 会把 DSN 的地址段**一并丢弃**，驱动
  回退默认 `127.0.0.1:3306`——配置里的远程 Doris FE 地址被静默忽略。
- **修复**：doris 注释样例段补 `Net = "tcp"`（starrocks 段此前已补）。

## 2. 修复方案与实施期新发现

| 问题 | 方案 | 状态 |
|------|------|------|
| NULL 位图 bug | 升级 `github.com/go-sql-driver/mysql` v1.6.0 → v1.9.3（含该修复的最低维护线最新补丁版；≥1.9.2 即修复）。连带引入间接依赖 `filippo.io/edwards25519`（caching_sha2_password 认证缓存）。主模块与 `test/integration` 模块同步升级 | **已完成**（MySQL 后端验证通过） |
| doris 样例 | `conf/ai_gateway_api.toml` doris 注释样例段补 `Net = "tcp"` | 已完成 |
| **问题 3（新实锤）：SR FE 二进制行包缺陷（服务端，与驱动版本无关）** | SR 生产/测试配置维持 `InterpolateParams = true`（文本协议）；conf starrocks 样例补该项；doris 样例补同款防御性建议（binary 路径待 Doris 环境恢复后验证） | 规避已完成；根治需 SR FE 修复 |

### 问题 3 详细：StarRocks FE COM_STMT 二进制行包编码缺陷

升级驱动后，StarRocks 真实实例集成组在**未开 InterpolateParams**（二进制协议，与生产一致）
下全部 logs 用例 500（panic 被恢复中间件捕获）。排查链：

1. api 日志：`panic: slice bounds out of range [:184] with capacity 178`，
   `go-sql-driver/mysql@v1.9.3 packets.go readRow`（binaryRows 行解析）。
2. 独立探针（同驱动直连 SR FE 3.5.21，真实 DDL `bfe_ai_request_log` + 真实 49 列投影 +
   真实 6 行种子）**稳定复现**；同探针对 MySQL 8.4 二进制协议全对 → 排除驱动解析问题，
   定位为 **FE 行包畸形**。
3. 投影列子集二分：单列（含 JSON 列、NULL 列）全对；**JSON 列与 NULL 列相邻组合触发**
   （投影列 24-25 = `ai_auth_reject_quota_plans`(JSON) + `level1Name`(NULL) 即 panic；
   JSON 列居投影末位时后续无列、尾部垃圾被容忍故偶发"通过"）；行 1001（无相邻组合）全
   49 列通过，行 1004 全 49 列在行中部错位。同一行同样列组合在文本协议（CLI / 无参 /
   InterpolateParams）下全部正确。
4. 结论：SR FE（MySQL 协议重实现）对含 JSON 类型列的二进制行包编码存在缺陷，驱动
   v1.6.0 表现为静默串位（旧记录归因"驱动 bug"有误，实为同一 FE 缺陷的两种症状），
   v1.9.3 解析更严格故表现为 panic。**任何驱动版本都无法在二进制协议下安全对接 SR FE
   的宽结果集**，文本协议是唯一可靠路径。

**复现要点**（供向 StarRocks 提 issue）：SR 3.5.21，表含 `ARRAY<STRUCT<...>>`（经 MySQL
协议报告为 JSON 类型）与可空 VARCHAR 列；COM_STMT 执行 `SELECT` 使 JSON 列与 NULL 值列
在结果行中相邻；客户端按 MySQL 二进制行协议解析即错位（go-sql-driver v1.6.0 串位 /
v1.9.3 panic，mysql client 文本协议正确）。

## 3. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api`（本仓库）；ai-gateway-observability 无改动 |
| 依赖变更 | `go.mod`/`go.sum`（主模块 + `test/integration` 模块）：go-sql-driver v1.6.0 → v1.9.3，新增间接依赖 filippo.io/edwards25519 v1.1.0 |
| 配置样例 | `conf/ai_gateway_api.toml`：doris 段补 `Net = "tcp"` + `InterpolateParams = true`（建议，含验证状态注释）；starrocks 段补 `InterpolateParams = true`（必需，含 FE 缺陷注释） |
| 集成测试装配 | `test/integration/testutil/report_server.go`（保留 interpolateParams 字段并修正归因注释）；`test/integration/tests/report/query/starrocks_seed_test.go`（背景注释⑥改写为 FE 缺陷版） |
| 接口形状 | 无变化；SQL 语义无变化 |
| 数据迁移 | 无 |

## 4. 验证

- `go build ./...` / `go vet` 干净；model 覆盖率门 70% 通过（84.0%）。
- 单测全绿（storage/model/endpoints/stateful/lib）。
- 集成测试（本机实机环境）：MySQL 8.4 组 + ClickHouse 真实实例组 + StarRocks 真实实例组
  同跑全绿。其中 **MySQL 组经 v1.9.3 二进制协议验证**（问题 1 根治的反向验证）；
  StarRocks 组按文本协议（InterpolateParams，即推荐生产形态）验证。
- 探针取证：SR FE 3.5.21 二进制行包缺陷最小复现 + MySQL 8.4 对照 + 文本协议对照
  （过程与数据见 §2 问题 3）。
- Doris 组说明：Doris FE/BE 当前停机（BE rocksdb 故障待决策，见
  `environment/doris-installation.md` FAQ 4.1），doris 门控用例数据源为 MySQL（仅 backend
  标识差异，422 在查询层拦截），不受影响；Doris FE 的 binary 路径验证为后续项
  （conf 样例已给防御性建议）。

## 5. 关联文档

- 缺陷原始记录：[2026-10-02-report-starrocks-backend](./2026-10-02-report-starrocks-backend/design-changes.md) §"实施期实机发现"
- 报表模块长期文档：`design-docs/sys-design/details/report.md`（协议层事实对模块设计透明，无改动）
- 后续治理项：向 StarRocks 社区提 FE 二进制行包 issue（复现要点见 §2 问题 3）；Doris
  环境恢复后验证其 FE binary 路径并回写 conf 样例注释
