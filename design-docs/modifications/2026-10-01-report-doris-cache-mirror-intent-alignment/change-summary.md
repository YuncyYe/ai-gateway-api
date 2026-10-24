# Doris 报表后端对齐缓存/镜像/意图字段（二期）变更摘要

> 本文档覆盖《数据报表-Doris 后端设计方案》（v0.8/doris-report，2026-09-29）的
> **ai-gateway-api 侧改动**（设计稿 §3 总体方案、§5 查询层改动、§6 发布顺序）。
> 设计稿 §4 的 Doris 数仓侧改动由 ai-gateway-observability 仓落地，见其修改说明
> `doris/docs/modifications/2026-09-29-report-cache-mirror-intent-doris-alignment/`。
> 前置依赖：一期《Report 体现 ai-cache / 流量镜像 / ai-intent 访问日志字段》
> （`2026-09-27-report-cache-mirror-intent-fields/`）已在 MySQL 后端交付全量能力。

## 1. 背景

v0.7《数据报表-多存储与API化设计方案》确立"一套报表 API、两种存储后端、配置切换"
架构，`[Report].Backend = "mysql" | "doris"` 的装配逻辑（`stateful/config.go`
`ReportConfig`、`stateful/container/rdb/components.go` `initReport()`）**已在代码中
存在**。v0.8 一期按"MySQL 先行"交付三组字段（缓存/镜像/意图）的报表能力：

- log-reader 字段注册、MySQL 明细表 +10 列、MySQL 聚合表 +3 维度、mysqlreport
  查询层全量已交付；
- **dorisreport 仅交付"明细支撑"部分**（overview 明细计数、logs 新列与过滤、
  timeseries `cache_tokens`）；三个新维度（`ai_cache_status` / `mirror_hit` /
  `ai_intent_answer`）由 `Capabilities()` 能力门控在 manager 层返回 422。

由此产生的错位（本二期必须消除）：

1. `storage/dorisreport/report.go` 的 SQL 已引用 `ai_cache_status` 等新列，而
   Doris 侧表结构、Routine Load 映射、聚合 JOB 均未加列——`Backend = "doris"`
   部署下触碰新列的查询直接报"列不存在"；
2. 三个新维度的下钻请求在 Doris 后端按门控返回 422，两后端能力不齐。

一期已在 MySQL 上把口径与产品形态验证完毕（集成测试组 B 全绿），二期 Doris 只剩
对齐工作，口径零重新决策。

## 2. 目标

| # | 目标 | 验证标准 |
|---|------|----------|
| 1 | dorisreport 查询层与 mysqlreport 能力拉平：三个新维度下钻可用，能力门控移除 | 三新维度的 rankings / distribution / timeseries 请求在 Doris 后端返回数据而非 422 |
| 2 | `Backend = "doris"` 下五类端点全部可用，不再出现列不存在错误 | 升级后 overview 新指标、logs 新列、timeseries `cache_tokens` 正常返回 |
| 3 | 报表后端配置契约文档化 | `conf/ai_gateway_api.toml` 样例（两形态）与 `Backend` 取值语义表入档；**配置解析零改动**（既有 `ReportConfig` / `initReport()` 已支持） |
| 4 | 两后端口径一致 | 同一份 pb 日志同时跑两条链路，同窗口 overview/timeseries/rankings/distribution 数值一致（分位数按既有差异保留：仅 Doris 返回 P50/P90/P99） |

## 3. 非目标

- 不改动 MySQL 侧任何已交付内容（DDL、JOB、mysqlreport）；
- 不改 log-reader、BFE、ai-gateway-web（前端按字段有无降级，新维度返回结构与 MySQL 一致即自然兼容）；
- 不引入任何新配置项（`Backend` 缺省不装配 404、非法值启动报错的行为不变）；
- 不重建 Doris 聚合表历史数据（分钟表只回看近期窗口，新维度从切换时刻起积累，同一期口径）；
- 不做 MySQL 侧分位数补齐（开放问题维持 v0.7 结论）。

## 4. 范围

| 范围 | 说明 |
|------|----------|
| 涉及仓库 | `ai-gateway-api`（本仓库，查询层 + 契约文档）；Doris 数仓侧见 ai-gateway-observability 同期修改说明 |
| 修改模块 | `storage/dorisreport/report.go`（三新维度 SQL 分支）；`model/ireport/types.go`（`Capabilities()` 声明拉平）；`design-docs/api-define/OpenAPI接口定义/report.md`（去 "mysql only / doris 422" 标注）；配置契约样例文档化 |
| 测试适配 | dorisreport SQL builder 单测（方言模板快照，对齐 mysqlreport 已验证模板）；`Capabilities()` 声明断言；集成测试新增 Doris 组（仿既有组 B）；双链路数据一致性比对；`make test-model-cover-gate` 回归 |
| 涉及接口 | `GET /open-api/v1/report/overview`、`/timeseries`、`/rankings`、`/distribution`、`/logs`（详见 api-changes.md） |
| 数据迁移 | 无（查询层改造，无 DDL/JOB 变更） |
| 数据面影响 | 无（纯查询层 + 文档） |

## 5. 关键决策

| 决策 | 说明 |
|------|------|
| SQL 模板同源 mysqlreport | dorisreport 新维度分支的 SQL 结构与 mysqlreport 已验证版本同源，仅方言差异（TopN 排序、空值谓词、`distributionNameExpr` 归一表达式），口径零重新决策 |
| 空值归一口径 | distribution 字符串维度空值归一 `"unknown"` 桶；`mirror_hit` 为 `"0"`/`"1"` 桶；rankings 字符串维度空值不进排行（沿用既有空值谓词风格） |
| 门控移除方式 | 仅 `dorisreport.Capabilities().SupportedDimensions` 追加三维度声明；manager 的既有门控逻辑（白名单校验后追加后端能力校验）自动放行，**manager 零改动** |
| 配置契约纯文档化 | §3.2 只是把既有 `ReportConfig` 字段与 `initReport()` 装配行为写成文档与 toml 样例，不引入新配置项，降低二期变更面 |
| 发布顺序硬约束 | api 版本必须在 ai-gateway-observability Doris 侧步骤 1（明细表加列 + Routine Load 重建 + 聚合表重建）完成之后上线——升级前 Doris 部署新维度 422/新列查询报错，升级后全部正常；对 mysql/缺省部署零行为变化 |
| Doris 3.0 兼容性（跨仓联动） | Doris 侧落地时发现 Doris 3.0.8 不支持 `ARRAY<STRUCT>` 元素字段解引用（`.`/`['f']`/CAST 均不可用），既有 JOB 的 `ELEMENT_AT(...).field` 写法无法创建——已由 observability 侧改为 Routine Load `json_extract` 打平列方案；本仓查询层按打平后的聚合表列读取，无额外适配 |

## 6. 兼容性影响

- **Backend = "doris" 部署（行为变化，属修复性）**：升级前新维度请求 422、触碰新列
  查询报错；升级后全部正常。api 升级必须在 Doris 侧步骤 1 完成之后；
- **Backend = "mysql" 与缺省部署**：零行为变化（本变更对两形态无副作用）；
- **回滚**：api 查询层可独立回滚（老版本对 Doris 新列仅触发明细/概述路径，门控退回
  422）；Doris 表结构变更向后兼容旧版本 api（旧 SQL 不引用新列即不受影响），无需
  随 api 回滚；
- 错误信息变化：三新维度在 Doris 后端的 422 "mysql only until doris support lands"
  不再出现（详见 api-changes.md 错误码表）。

## 7. 关联文档

- 设计稿：《数据报表-Doris 后端设计方案》（`迭代系统设计/v0.8/doris-report/数据报表-Doris后端设计方案.md`，本变更的权威来源）
- 一期：`design-docs/modifications/2026-09-27-report-cache-mirror-intent-fields/`（MySQL 全量 + dorisreport 明细支撑 + 能力门控）
- 历史先例：`2026-09-15-report-query-api/`（报表子系统首次引入）、`2026-09-15-report-fixes/`（时区/分区边界修复，两后端口径）
- Doris 数仓侧（另一仓）：ai-gateway-observability `doris/docs/modifications/2026-09-29-report-cache-mirror-intent-doris-alignment/`（含 Doris 3.0 struct 解引用兼容性修复与 `_v2`+`REPLACE WITH TABLE` 重建流程）
- `design-docs/modifications/2026-10-01-report-doris-cache-mirror-intent-alignment/design-changes.md`
- `design-docs/modifications/2026-10-01-report-doris-cache-mirror-intent-alignment/api-changes.md`
