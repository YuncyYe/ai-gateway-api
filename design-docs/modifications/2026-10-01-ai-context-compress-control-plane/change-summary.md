# mod_ai_context 上下文压缩与裁剪：控制面变更摘要

## 1. 背景

BFE 数据面已完成 `mod_ai_context` 一期（无损裁剪 L1–L2 + 规则改写 P2，OpenAI chat 协议，fail-open），修改方案见 BFE 仓 `docs/zh_cn/modifications/2026-10-01-ai-context-compress/design-changes.md`（已推 origin `v1.8.9-dev`，commit `bf441cb5`；集成测试场景 SC24 同步入库）。

数据面消费的控制面产物为 `context_rule.data`（规则文件，BFE 热加载），结构为**顶层 `Defaults` 调优块 + `Config` map[product]规则数组**。控制面需要：规则集合资源、全局设置单例资源、导出生成器。

本次改动沿用既有导出框架（`topic + 生成器 + MD5 签名 + config_versions + Inner API 轮询`），仍是**登记式**：不改框架本身。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-10-01 |
| 涉及仓库 | `ai-gateway-api` |
| 变更类型 | 新增集合资源 `ai_context_rules`（Open API GET/PUT 全量读写）+ 新增单例资源 `ai_context_settings`（GET/PUT upsert）+ 新增导出端点（`context_rule.data`） |
| 产出 | 2 张新表 + DAO/storager + model（读写/导出/审计）+ Open API 4 个新端点 + 测试 + 设计文档 |
| 预估工作量 | 5 个开发日（含测试与设计文档） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 资源形态 | **集合资源** `ai_context_rules`（顶级），仅集合级 GET/PUT 两端点，无 `/{id}`、无 `enabled`、顺序即优先级 | 完全照 `ai-cache-rules` 先例（`2026-09-24-ai-cache-rule-export`）；规则总量小、整组维护 |
| 2 | `Defaults` 块控制面载体 | **单例设置表** `ai_context_settings`（单行 + upsert），由生成器合并进 `context_rule.data` 顶层，恒导出 | 照 `ai-cache-semantic-settings` 先例；BFE 只读 `context_rule.data` 一个文件，顶层键必须生成器合并（不走 `model/imods` 独立 topic 文件） |
| 3 | `mode` 字段 | **必填**，枚举 `off/conservative/balanced/aggressive` | BFE 加载器对缺失/非法 mode 整文件拒载，控制面前置 422 拦截 |
| 4 | 二期字段 `override`/`summary` | **一期不预留**：API 与表只含一期字段；phase-2 以增量方式引入（Open API 加可选字段 + 表加可空 JSON 列），BFE 未知字段容忍保证平滑升级 | 一期契约只定义一期代码消费的字段；二期属标准 additive 修改，无迁移负担 |
| 5 | 精确 `context_window` 数据源 | **一期不下发**：数据面用模型名启发式（claude 200k / gemini 1M / codex 400k）+ 128k 兜底（已实现，SC24 验证）；二期随模型能力域统一设计后下发 | `model_prices` 是定价表，能力规格属职责错位；一期窗口误差仅影响触发时机（早压/晚压）且全程 fail-open |
| 6 | 权限点 | 新增 `FeatureAIContext`（scope 映射照 `FeatureAICache`），rules/settings/export 三个 Action | 与 ai-cache 域平级的新功能域 |
| 7 | 数据面连接配置 | `mod_ai_context.conf`（INI，仅 ProductRulePath + OpenDebug）由 BFE 静态 conf 经 conf-agent `CopyFiles` 下发，**不由控制面导出** | 照 ai-cache 决策 5：连接/路径类静态配置不进导出链路 |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `db_ddl.sql`/`db_ddl_sqlite.sql` 的 `ai_context_rules`、`ai_context_settings` 两表；`storage/rdb/ai_context/`；`model/ai_context/`（manager/storager 接口/generator/operation_log/mocks）；`endpoints/openapi_v1/ai_context/`；`endpoints/innerapi_v1/ai_context/export.go` |
| 主要修改 | `model/iauth/features.go`（FeatureAIContext + scope）；`model/shared/types.go`（参数结构）；`lib/validate/validate.go`（`AIContextRules`/`AIContextSettings`）；两个 `endpoints.go` 注册；`stateful/container/` 装配 |
| 明确不动 | 导出框架（`model/iversion_control`）、conf-agent（零代码改动）、`model/imods/` 既有 exporter、`model/imodel_price/`（定价域，见决策 5）、ai-cache 域各资源、ai_route/cluster 各域 |
| 接口契约 | Open API 新增 4 个端点（rules GET/PUT + settings GET/PUT）；Inner API 新增 1 个导出端点；**无任何既有端点/表结构变更** |
| 数据迁移 | 仅 2 张新表（无既有表加列） |

## 5. 非本仓登记点（链路协同）

| 位置 | 改动 | 状态 |
|------|------|------|
| BFE 数据面 `mod_ai_context` | 模块实现 + 加载器（`Defaults` 块 + 前向兼容未知字段告警）+ `/reload/mod_ai_context` + conf 样例；导出字段 tag 已冻结 | **已完成**（commit `bf441cb5`，已推 origin `v1.8.9-dev`） |
| `bfe-access-pb` | 访问日志 `ai_context_*` 字段（793-796） | **已完成**（v0.3.11 已发布，bfe 已切换正式版本） |
| bfe 集成测试 | SC24 场景（10 个 TC 全绿） | **已完成** |
| `conf-agent/conf/conf-agent.toml` | 新增 `[Reloaders.mod_ai_context]`：`ConfAPI=/inner-api/v1/configs/ai-context-rule`、`ReloadFile=context_rule.data`、`BFEReloadAPI=/reload/mod_ai_context`、`CopyFiles=["context_rule.data","mod_ai_context.conf"]` | 待落地（conf-agent 代码泛化，无需改 Go 代码） |
| `ai-gateway/kubernetes/deploy/bfe-configmap.yaml` | conf-agent.toml 段同步上述配置；bfe.conf `Modules=` 加 `mod_ai_context` | 待落地 |
| `integration-test` 仓 | `conf_agent_config_builder.go` 的 `EnabledReloaders` 合法值含 `mod_ai_context` | 待落地 |
| ai-gateway-web（dashboard） | 上下文压缩规则/设置管理页面 | 二期 |

## 6. 文档配套

- `api-changes.md`：Open API `/ai-context-rules`、`/ai-context-settings` 契约 + Inner API `/configs/ai-context-rule` 导出契约；
- `design-changes.md`：表结构、导出契约冻结表（与 BFE 加载器逐字段核对）、生成器逻辑、装配、测试计划、WBS、待决策点。
