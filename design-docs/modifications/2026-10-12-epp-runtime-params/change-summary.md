# EPP 运行时参数（`epp_runtime`）接入（ai-gateway-api 控制面改造）变更摘要

## 1. 背景

BFE（2026-09-14 起）已在 `cluster_conf.data` 的 `GslbBasic` 中实现 EPP 传输/健康检查/超时/熔断能力（`EPPTLS` / `EPPCheck` / `EPPTimeout` / `EPPBreaker`），用于 BFE 访问 EPP 调度器的连接安全、后台健康检查滞回与超时控制。但 **ai-gateway-api 的 cluster 接口 / 存储 / `server_data_conf` 导出均未建模这组字段**，导致 api 托管部署下：

- `EPPTLS` 恒为 `nil` → BFE 加载期回落到 legacy 默认（TLS + 跳过证书校验，并打 warning）→ **EPP 明文不可达**，EPP 节点无法接入；
- 健康检查 / 超时 / 熔断参数无法经控制面下发，只能靠 BFE 本地默认，托管场景不可运维。

现网部署形态为 **api 托管（控制面唯一写入口）+ conf-agent 下发 + BFE 消费**，缺少 OpenAPI 路径即无法完成 EPP 参数配置。本方案补齐该缺口。

> 议题：`rainway-ai-gateway/ai-gateway-api` issue #218。设计来源：`218-ai-gateway-api支持EPP非调度参数设计文档v0.2.md`。

## 2. 目标

- 在 OpenAPI `/clusters` 新增可选嵌套对象 **`epp_runtime`**，承载 BFE 侧 EPP 运行时参数：
  - **本期对外开放**：`check`（→ `GslbBasic.EPPCheck`）、`timeout`（→ `GslbBasic.EPPTimeout`）。
  - **固定下发、不对用户暴露**：`EPPTLS{Plaintext:true}`、`EPPBreaker{Disabled:true}`。
- 在 `clusters` 表新增 `epp_runtime` 列（JSON text），按既有 `epp_config` 的"原始 JSON 透传"模式存取，GET 原样回读。
- 在 `NewBfeClusterConf` 的 EPP 分支把 `epp_runtime` 映射为 `GslbBasic.{EPPCheck,EPPTimeout}`，并固定填充 `EPPTLS` / `EPPBreaker`。
- 为 `check` / `timeout` 未设置的子字段提供**与 BFE 完全一致的默认值**（`2s`/`3`/`45s`/`2`、`500ms`/`3s`）。
- 写入时对 `check` / `timeout` 做 BFE 等价校验，非法配置在 api 侧拒绝（4xx），杜绝 BFE reload 失败导致的 conf-agent 死循环。
- 向后兼容：未配置 `epp_runtime` 的 **WRR** cluster 导出与现状完全一致；老库经 `--reinit` 或加列后读写正常。

**非目标**：

- 不实现 EPP 调度配置（`epp_config` 已存在，编译进 `epp_data`，与本组字段分层）。
- 不修复 BFE 侧 EPP 参数热更新语义（另立 BFE issue）。
- 不改变 `BalanceMode` / `EPPAddr` 既有派生逻辑。
- 不开放 `tls`（`Insecure` / `CAFile`）与 `breaker` 参数（`WindowSize`/`MinVolume`/`ErrorRatePercent`/`OpenTimeout` 暂注释）。

## 3. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api`（无新增端点、无新增表） |
| 主要模块 | `endpoints/openapi_v1/product_cluster/`（请求/回读）、`model/icluster_conf/`（模型/校验/默认/导出/操作日志）、`storage/rdb/`（DAO 与存储映射） |
| 接口契约 | OpenAPI `/clusters` 新增可选对象 `epp_runtime`；InnerAPI `server_data_conf` 的 `GslbBasic` 段新增 `EPPCheck`/`EPPTimeout` 并固定 `EPPTLS`/`EPPBreaker` |
| 数据库 | `clusters` 表新增 `epp_runtime` 列（JSON text，可空）；无新表 |
| BFE 影响 | 仅消费 `server_data_conf` 新增字段，无 BFE 代码改动 |
| conf-agent 影响 | 无（版本变化触发下发与 `/reload/server_data_conf` 自动生效） |
| EPP 影响 | 无 |

## 4. 关键决策

| 决策 | 说明 |
|------|------|
| 新增一等配置对象 `epp_runtime` | 与 `epp_config`（EPP 调度配置）分层：`epp_config` 决定 EPP 如何调度，`epp_runtime` 决定 BFE 如何连接/探活 EPP。两者随 cluster 生命周期管理，互不混淆。 |
| 只暴露 `check` / `timeout` | `tls` 与 `breaker` 本期不对外开放：`tls` 因 ai-gateway-api 整体尚不支持 TLS 配置（且 `CAFile` 需 BFE 侧 `ConfPathProc` 支持），`breaker` 因暂不提供熔断调参。二者在导出时固定为安全值。 |
| `EPPTLS` 固定 `Plaintext:true` | 统一明文接入 EPP，不依赖用户输入；避免 legacy `{Insecure:true}` 默认导致 EPP 明文不可达。 |
| `EPPBreaker` 固定 `Disabled:true` | 关闭熔断，行为确定；`WindowSize`/`MinVolume`/`ErrorRatePercent`/`OpenTimeout` 待后续开放时再启用。 |
| 存储沿用 `epp_config` 的原始 JSON 模式 | `ClusterParam.EppRuntime *string` + `Cluster.EppRuntime string`，DB 列 `text`，GET 原样回读；校验在写入时解析原始 JSON 完成。 |
| 默认值写入时补齐并落盘 | 与 BFE 默认对齐（见 §2.1 of design-changes.md）。写入时补齐使 GET 回读即完整生效值，配置自解释；BFE 侧对未设置字段本就会补同样默认，二者等价。 |
| 导出门控在 EPP 且分配有效分支 | 仅在 cluster 解析为 `BalanceMode=EPP`（即 `EPPAddr` 分配有效）时导出 `EPP runtime` 字段；分配缺失降级为 WRR 时不导出（BFE 也不消费），避免在半降级配置上产生误导字段。 |
| 校验前移（复刻 BFE） | duration 需为正、阈值满足范围；`epp_runtime` 严格解析（`DisallowUnknownFields`），`tls`/`breaker` 字段出现在请求中即因未知字段被拒绝（422）。理由：BFE reload 失败会导致 conf-agent 不切符号链接、以同因反复重试（死循环）。 |
| 不改变接口结构 | 无新端点、无新表、无 schema 版本变更，仅新增字段/列，降低对接与回归成本。 |

## 5. 分期落地

| 期 | 内容 |
|----|------|
| P0 | `epp_runtime` 字段（`check`/`timeout`）+ 默认值 + 校验；`clusters.epp_runtime` 列；导出填充 `EPPCheck`/`EPPTimeout` 与固定 `EPPTLS`/`EPPBreaker`；操作日志快照；OpenAPI / InnerAPI 文档同步（约 2–3 天） |
| P1 | （预留）`tls.Insecure`/`CAFile` 开放（依赖 BFE `ConfPathProc`）、`breaker` 调参开放、feature flag 灰度导出 |
| P2 | （预留）EPP 参数热更新语义修复（BFE 侧） |

## 6. 关联文档

- 接口变更：本目录 `api-changes.md`
- 设计变更：本目录 `design-changes.md`
- 设计来源：`218-ai-gateway-api支持EPP非调度参数设计文档v0.2.md`
- 既有 EPP 对接：`../2026-09-08-epp-scheduling-integration/`、`../2026-09-29-optimize-epp-arguments/`
- 接口定义：`design-docs/api-define/OpenAPI接口定义/clusters.md`、`design-docs/api-define/InnerAPI接口定义/server-data-conf.md`
- BFE 侧定义：`bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go`（`GslbBasicConf` 及 `EPPxxxConf` 与校验/默认）