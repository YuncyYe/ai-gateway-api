# ai-cache 规则导出：新增 ai_cache_rules 集合资源 —— 变更摘要

## 1. 背景

AI 网关一期缓存能力（ai-cache，简化版：仅 Redis 精确匹配）需要在控制面提供规则管理入口。数据面由 BFE `mod_ai_cache` 模块消费规则文件（`ai_cache.data`），按既有约定：**规则文件由 ai-gateway-api 生成、conf-agent 轮询拉取并触发 BFE 热加载**。

ai-gateway-api 现有配置导出框架（`topic + 生成器 + MD5 签名 + config_versions 表 + Inner API 轮询`）是通用机制，本次改动为**登记式**：不改框架本身。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-09-24 |
| 涉及仓库 | `ai-gateway-api` |
| 变更类型 | 新增集合资源 `ai_cache_rules`（Open API GET/PUT 全量读写 + Inner API 导出） |
| 产出 | 新表 + DAO + model（读写/导出/审计）+ 两组 endpoint + 权限点 + 装配 + 测试 + 设计文档 |
| 预估工作量 | 7-9 个开发日（含测试与设计文档） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 资源形态 | **集合资源**（顶级，不嵌套在 api-key/entity 下） | 缓存规则区分维度是请求特征（cond 表达式），与凭证/租户无关；租户隔离在数据面缓存键前缀实现，控制面不生成 Redis key |
| 2 | API 形态 | **仅集合级 GET + PUT 全量更新两个端点**，不提供 `/{id}` 单条操作接口 | 照 `global-route-rules` 先例（`GetGlobalRouteRules`/`SetGlobalRouteRules`）；规则总量小、整组维护，全量替换语义最简单、无单资源状态分歧 |
| 3 | 优先级表达 | **不设 `priority` 字段**，数组顺序（= `id` 升序）即优先级 | 简化版语义为"一组有序规则，first-match-wins" |
| 4 | 规则启停 | **不设 `enabled` 字段**，PUT 提交的列表即生效集合 | 全量替换模型下"禁用一条规则"="从列表移除"，per-rule enabled 是冗余状态；generator 导出全表 `id` 升序，无过滤逻辑 |
| 5 | Redis 连接配置 | **不由控制面导出**，放 BFE 静态 `mod_ai_cache.conf`，经 conf-agent `CopyFiles` 机制下发 | 连接信息与规则生命周期不同（近乎不变）；减少 Redis 密码在导出链路中的暴露面；与 `mod_ai_token_auth.conf`/`mod_ai_rate_limit.conf` 同款 |
| 6 | 导出字段范围 | 一期导出**最小字段集**（cond / cacheKeyStrategy / cacheTTL / maxBodyBytes / maxValueBytes），其余字段由 BFE 规则加载器 `setDefaults` 填默认 | 控制面只管理规则语义字段；命中响应模板、GJSON 提取路径等二期再纳入 |
| 7 | dashboard | **二期**再做，一期用 Open API | 范围控制 |
| 8 | 导出契约 | 已与 BFE `mod_ai_cache` 规则加载器（`ProductRuleConfFile`）逐字段核对冻结，见 design-changes.md §4 | 控制面/数据面接口契约是两侧开发的前提 |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `db_ddl.sql`/`db_ddl_sqlite.sql` 的 `ai_cache_rules` 表；`storage/rdb/internal/dao/table_ai_cache_rules.go`；`storage/rdb/ai_cache/`；`model/ai_cache/`（manager/storager 接口/generator/operation_log/mocks）；`endpoints/openapi_v1/ai_cache/`；`endpoints/innerapi_v1/ai_cache/export.go` |
| 主要修改 | `model/iauth/features.go`（FeatureAICache + scope 映射）；`model/shared/types.go`（参数结构）；`lib/validate/validate.go`（`AICacheRules` 校验）；`endpoints/openapi_v1/endpoints.go`、`endpoints/innerapi_v1/endpoints.go`（注册）；`stateful/container/components.go`、`stateful/container/rdb/components.go`（装配） |
| 明确不动 | 导出框架（`model/iversion_control`）、conf-agent（零代码改动）、`model/imods/` 既有 exporter、api-key/entity/rate_limit_policy/global_route_rules 各域 |
| 接口契约 | Open API 新增 2 个端点；Inner API 新增 1 个导出端点；无既有接口变更 |
| 数据迁移 | 无（全新表） |

## 5. 非本仓登记点（链路协同，缺一不可）

| 位置 | 改动 | 状态 |
|------|------|------|
| BFE 数据面 `mod_ai_cache` | 模块实现 + monitor `/reload/mod_ai_cache` + conf 目录放 `mod_ai_cache.conf` 与初始 `ai_cache.data`；导出字段 tag 已冻结 | 已完成（契约见 design-changes.md §4） |
| `bfe-access-pb` | 访问日志 `ai_cache_status`/`ai_cache_key` 字段（日志 proto 789/790） | 已完成（v0.3.7） |
| `conf-agent/conf/conf-agent.toml` | 新增 `[Reloaders.mod_ai_cache]`：`ConfAPI=/inner-api/v1/configs/ai-cache-rule`、`ReloadFile=ai_cache.data`、`BFEReloadAPI=/reload/mod_ai_cache`、`CopyFiles=["ai_cache.data","mod_ai_cache.conf"]` | 待落地（conf-agent 代码泛化，无需改 Go 代码） |
| `ai-gateway/kubernetes/deploy/bfe-configmap.yaml` | conf-agent.toml 段同步上述配置；bfe.conf `Modules=` 加 `mod_ai_cache` | 待落地 |
| `integration-test` 仓 | `conf_agent_config_builder.go` 的 `EnabledReloaders` 合法值含 `mod_ai_cache` | 已完成（SC20 场景） |
| ai-gateway-web（dashboard） | 缓存规则管理页面 | 二期 |

## 6. 文档配套

- `api-changes.md`：Open API `/ai-cache-rules` GET/PUT + Inner API `/configs/ai-cache-rule` 导出契约；
- `design-changes.md`：表结构、导出契约冻结表、读写与生成器逻辑、装配、测试计划、WBS、待决策点。
