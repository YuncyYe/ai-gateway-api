# 流量镜像规则导出：新增 traffic_mirror_rules 集合资源 —— 变更摘要

## 1. 背景

流量镜像（traffic mirroring / shadow traffic）能力需要在控制面提供镜像规则的统一管理入口：规则匹配（product + 条件表达式 + 百分比采样）、镜像目标 cluster、Header 黑名单/注入、body `model` 字段改写、路径改写。数据面由 BFE `mod_traffic_mirror` 模块消费规则文件（`mirror_rule.data`），按既有约定：**规则文件由 ai-gateway-api 生成、conf-agent 轮询拉取并触发 BFE 热加载**。

ai-gateway-api 现有配置导出框架（`topic + 生成器 + MD5 签名 + config_versions 表 + Inner API 轮询`）是通用机制，本次改动为**登记式**：不改框架本身。参照样板为 `2026-09-24-ai-cache-rule-export`（同为"AI 模块规则集合 + 导出"形态）。

> 口径调整：需求分析中控制面参照 `model/imods/mod_body_process.go` 模式；本设计改为照 **ai-cache 先例**（独立 `model/traffic_mirror/` 包 + `iversion_control` 导出框架），`model/imods/` 既有 exporter 不动。两者目标相同（规则 CRUD + 配置导出），ai-cache 模式是更新的登记方式。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-09-25 |
| 涉及仓库 | `ai-gateway-api` |
| 变更类型 | 新增集合资源 `traffic_mirror_rules`（Open API GET/PUT 全量读写 + Inner API 导出） |
| 产出 | 新表 + DAO + model（读写/导出/审计）+ 两组 endpoint + 权限点 + 装配 + 测试 + 设计文档 |
| 预估工作量 | 7-9 个开发日（含测试与设计文档） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 资源形态 | **集合资源**（顶级，不嵌套在 apikey/entity 下） | 镜像规则区分维度是请求特征（cond 表达式）+ 目标 cluster，与凭证/租户无关；BFE 数据面侧计费/鉴权天然隔离（镜像请求不经 token auth），控制面无 per-tenant 语义 |
| 2 | API 形态 | **仅集合级 GET + PUT 全量更新两个端点**，不提供 `/{id}` 单条操作接口 | 照 `ai-cache-rules` / `global-route-rules` 先例；规则总量小、整组维护，全量替换语义最简单 |
| 3 | 优先级表达 | **不设 `priority` 字段**，数组顺序（= `id` 升序）即优先级 | BFE 规则表 first-match-wins；语义与 ai_cache_rules 一致 |
| 4 | 规则启停 | **不设 `enabled` 字段**，PUT 提交的列表即生效集合 | 全量替换模型下"禁用一条规则"="从列表移除"；单条临时停用可用 `percentage=0` 表达（语义：命中但不采样） |
| 5 | 模块基础配置不下发 | 分层超时（Connect/TTFB/Total）、并发上限、队列容量、熔断阈值等**不进导出链路**，放 BFE 静态 `mod_traffic_mirror.conf`，经 conf-agent `CopyFiles` 机制下发 | 与 `mod_ai_cache.conf` 的 Redis 连接配置同款决策：调参项与规则生命周期不同，减少导出链路复杂度 |
| 6 | 导出字段范围 | 一期导出**规则全字段**（cond / mirrorCluster / percentage / removeHeaders / setHeaders / bodyRewrites / pathRewrite） | 与 ai_cache 的"最小字段集"决策不同：mirror 规则字段少且全部是规则语义字段，BFE 加载器 `Check` 要求 `mirrorCluster` 必填指针字段，无"二期预留字段"问题 |
| 7 | `remove_headers` 默认值 | **控制面缺省填默认黑名单** `["Authorization","Cookie","X-Api-Key"]`；显式提交空数组 = 不剔除 | 需求 FR-5 与合规要求默认剔除鉴权/会话头；注意 BFE 侧 `setDefaults` 默认是空列表（不剔除），"默认黑名单"是控制面行为，两侧语义不同（契约表已标明） |
| 8 | `mirror_cluster` 引用校验 | PUT 时 endpoint 层做存在性校验（`icluster_conf.ClusterManager.FetchClusterList` 按名批量过滤），不存在 → 422 | 照 `lib/validate` 注释"Existence in cluster configuration is checked separately by the endpoint"的先例；BFE 对未知 cluster 只在异步侧计 `fail_total{reason="resolve"}`，fail-fast 应拦在写入前 |
| 9 | cond 必填与全匹配表达 | **cond 必填**，全部匹配显式写 `default_t()`（与 `/ai-cache-rules` 一致）；集合内 cond 唯一（重复 → 422） | 空串无法区分"故意全匹配"与"漏传"，而空 cond 静默生效 = 100% 流量镜像 = 双倍推理成本，不接受隐式默认；必填后"双空重复"场景自然消失，BFE `Check` 的重复 cond 防线继续兜底 |
| 10 | dashboard | **二期**再做，一期用 Open API | 范围控制，照 ai_cache 先例 |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `db_ddl.sql`/`db_ddl_sqlite.sql` 的 `traffic_mirror_rules` 表；`storage/rdb/internal/dao/table_traffic_mirror_rules.go`；`storage/rdb/traffic_mirror/`；`model/traffic_mirror/`（manager/storager 接口/generator/operation_log/mocks）；`endpoints/openapi_v1/traffic_mirror/`；`endpoints/innerapi_v1/traffic_mirror/export.go` |
| 主要修改 | `model/iauth/features.go`（FeatureTrafficMirror + scope 映射）；`model/shared/types.go`（参数结构）；`lib/validate/validate.go`（`TrafficMirrorRules` 校验）；`endpoints/openapi_v1/endpoints.go`、`endpoints/innerapi_v1/endpoints.go`（注册）；`stateful/container/components.go`、`stateful/container/rdb/components.go`（装配） |
| 明确不动 | 导出框架（`model/iversion_control`）、conf-agent（零代码改动）、`model/imods/` 既有 exporter、ai_cache/rate_limit_policy/global_route_rules 各域 |
| 接口契约 | Open API 新增 2 个端点；Inner API 新增 1 个导出端点；无既有接口变更 |
| 数据迁移 | 无（全新表） |

## 5. 非本仓登记点（链路协同，缺一不可）

| 位置 | 改动 | 状态 |
|------|------|------|
| BFE 数据面 `mod_traffic_mirror` | 模块实现 + `/reload/mod_traffic_mirror` + conf 目录 `mod_traffic_mirror.conf`/`mirror_rule.data`；导出字段 tag 已随实现冻结（`bfe_modules/mod_traffic_mirror/mirror_rule_load.go`） | 已完成（v1.8.9-dev，随 BFE 提交 `d8e4f329`；集成场景 SC21 共 15 个用例全绿） |
| `bfe-access-pb` | 访问日志 `mirror_hit`/`mirror_cluster` 字段（日志 proto 842/843；844-850 一期保留为空） | 已完成（v0.3.8） |
| `conf-agent/conf/conf-agent.toml` | 新增 `[Reloaders.mod_traffic_mirror]`：`ConfAPI=/inner-api/v1/configs/traffic-mirror-rule`、`ReloadFile=mirror_rule.data`、`BFEReloadAPI=/reload/mod_traffic_mirror`、`CopyFiles=["mirror_rule.data","mod_traffic_mirror.conf"]` | 待落地（conf-agent 代码泛化，无需改 Go 代码） |
| `ai-gateway/kubernetes/deploy/bfe-configmap.yaml` | conf-agent.toml 段同步上述配置；bfe.conf `Modules=` 加 `mod_traffic_mirror` | 待落地 |
| `integration-test` 仓 | `conf_agent_config_builder.go` 的 `EnabledReloaders` 合法值含 `mod_traffic_mirror`；全链路场景（真实 conf-agent 下发 + 镜像行为验证） | 待落地 |
| ai-gateway-web（dashboard） | 镜像规则管理页、镜像率/成功率卡片 | 二期 |
| ai-gateway-observability | Grafana 镜像看板（QPS/状态码/错误分类/TTFB/token 成本） | 待落地 |
| Doris 报表 | "流量镜像"页签（按天/集群/model/apikey 聚合，先用 842/843 同步字段） | 待落地 |

## 6. 文档配套

- `api-changes.md`：Open API `/traffic-mirror-rules` GET/PUT + Inner API `/configs/traffic-mirror-rule` 导出契约；
- `design-changes.md`：表结构、导出契约冻结表、读写与生成器逻辑、校验、装配、测试计划、WBS、待决策点。
