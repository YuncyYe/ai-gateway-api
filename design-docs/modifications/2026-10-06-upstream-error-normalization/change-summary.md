# 上游错误体归一（normalize_upstream_error）——变更摘要

## 1. 背景

BFE 数据面已实现上游错误体归一（统一错误码，银行需求 #3）：上游厂商错误归一为
网关统一错误目录与 OpenAI 兼容响应体，状态码按统一映射表重映射（上游
401/402/403 → 502 `UPSTREAM_AUTH_ERROR`，与客户端自身凭证错误的 401 区分），
归一链路内嵌 cluster key 脱敏，SSE 流内错误事件载荷归一、流截断检测入访问日志。
数据面配置挂在集群 `AIConf.NormalizeUpstreamError`，per-cluster 独立灰度。

本次变更是让 `ai-gateway-api` 控制面能够管理并下发该配置，替代"手工编辑
cluster_table.data 且会被导出覆盖"的临时路径。

## 2. 目标

1. cluster 数据模型 `llm_config` 新增 `normalize_upstream_error` 可选对象字段
   （`enabled` / `stream_enabled` / `unrecognized_action` / `max_body_bytes` /
   `redact_secrets`）。
2. InnerAPI 导出 `server_data_conf` 时映射为 BFE `AIConf.NormalizeUpstreamError`。
3. 校验规则与 BFE `AIConfCheck` 逐条对齐，防止配置穿透。
4. 保持向后兼容：字段缺省 = BFE 关闭归一（历史透传行为）；无 DDL（复用
   `clusters.llm_config` JSON 列）；新旧 BFE/控制面任意组合安全。

## 3. 范围

- **涉及面**：`ai-gateway-api` 控制面（OpenAPI `/clusters`、InnerAPI 导出、校验）。
- **不涉及面**：BFE 数据面运行时逻辑（已完成）、conf-agent、数据库表结构。
- **数据面影响**：BFE `AIConf` 新增 `NormalizeUpstreamError` 字段（已实现，
  `bfe-access-pb` 无需再变）。

## 4. 关键决策

| 决策 | 说明 |
|------|------|
| 配置归属 cluster | BFE `AIConf` 为 per-cluster；灰度计划按集群分批（镜像集群先行），cluster 级提供所需粒度；模式对齐 `key_affinity` |
| 复用 `llm_config` JSON 列 | 无 DDL、零迁移；与 `key_affinity` 落地方式一致 |
| 默认值不下发 | 缺省字段不在导出中合成默认值，由 BFE `Effective()` 加载期合成（单一默认值来源） |
| `redact_secrets` 用 `*bool` | 区分"未配置"与"显式 false"（默认 true 的特性），与 BFE 侧类型一致 |
| 校验双端对齐 | 控制面校验规则与 BFE `AIConfCheck` 逐条一致（枚举、范围） |

## 5. 关联文档

- 详细设计：`design-changes.md`
- 接口变更：`api-changes.md`
- BFE 侧修改说明：`bfe/docs/zh_cn/modifications/2026-10-06-upstream-error-normalization/design-changes.md`
- BFE 侧配置文档：`bfe/docs/zh_cn/configuration/server_data_conf/cluster_conf.data.md` §9.4
- 错误码总表：`bfe/docs/zh_cn/sys_design/ai_error_codes.md` §2.5

## 6. 实施阶段

| 阶段 | 内容 | 状态 | 关键文件 |
|------|------|------|----------|
| 1 | BFE 数据面实现（配置加载/拦截点/SSE 过滤器/错误目录/脱敏/日志字段） | ✅ 已完成 | `bfe/bfe_server/reverseproxy_ai_error.go`、`bfe/bfe_basic/request_ai_basic.go`、`bfe/bfe_config/.../cluster_conf_load.go` |
| 2 | bfe-access-pb proto 扩展（810-815 六字段） | ✅ 已完成 | `bfe-access-pb` v0.3.12（已推送 origin） |
| 3 | 控制面数据模型 + 校验（`llm_config.normalize_upstream_error`） | 🔄 待实现 | `ai-gateway-api/model/icluster_conf/cluster.go`、cluster 参数校验入口 |
| 4 | InnerAPI 导出（`newAIConf` 映射）+ schema/单测/集成测试 | 🔄 待实现 | `ai-gateway-api/model/icluster_conf/cluster.go`、schema 测试 |
| 5 | 文档同步（OpenAPI/InnerAPI 接口定义、sys-design 三件套） | 🔄 待实现 | `ai-gateway-api/design-docs/api-define/...` |

## 7. 实现结果

- 当前状态：数据面与 proto 已完成（SC27 集成测试 10 例全绿，SC02/SC05/SC08/SC25
  回归全绿），控制面尚未适配。
- 控制面适配后，用户创建/更新 cluster 时可通过 `llm_config.normalize_upstream_error`
  按集群灰度开启归一；InnerAPI 导出的 `cluster_conf.data` 自动携带该配置。
