# 上游错误体归一（normalize_upstream_error）：ai-gateway-api 控制面设计变更说明

> 本文档只描述 `ai-gateway-api` 控制面的修改。BFE 数据面修改**已实施完毕**：
> `AIConf.NormalizeUpstreamError` 配置加载与校验（`bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go`）、
> 归一拦截点与 SSE 事件过滤器（`bfe_server/reverseproxy_ai_error.go`）、错误目录扩充
> （`bfe_basic/request_ai_basic.go`）、`bfe-access-pb v0.3.12`（proto 字段 810-815）、
> SC27 集成测试 10 例全绿，见 `bfe/docs/zh_cn/modifications/2026-10-06-upstream-error-normalization/design-changes.md`。

## 1. 概述

### 1.1 变更背景

BFE 数据面已实现上游错误体归一（统一错误码）：上游厂商错误（OpenAI / Anthropic /
Gemini envelope 及 SSE 流内错误事件）归一为网关统一错误目录与 OpenAI 兼容响应体，
状态码按统一映射表重映射，归一链路内嵌 cluster key 脱敏。数据面配置挂在集群
`AIConf.NormalizeUpstreamError`，per-cluster 灰度（`Enabled` / `StreamEnabled` 独立开关）。

控制面需要提供该配置的载体（OpenAPI 管理）与导出（InnerAPI → `cluster_conf.data`），
否则 BFE 侧只能手工编辑 `cluster_table.data` 验证，且会被控制面下一次导出覆盖。

### 1.2 配置归属：cluster 级 `llm_config.normalize_upstream_error`

| 决策 | 说明 |
|------|------|
| 归属 cluster（非 provider） | BFE `AIConf` 是 per-cluster 配置；灰度计划按集群分批开启（先镜像集群、再全量，四阶段见 BFE 修改说明 §7），cluster 级提供所需粒度 |
| 模式对齐 `key_affinity` | 与 `2026-08-26-ai-key-session-affinity` 完全同构：`llm_config` 新增对象字段 → `newAIConf` 导出映射 → 无 DDL |
| 对象整体透传（非拍平字段） | 归一配置是独立内聚对象，整体映射到 BFE `*cluster_conf.UpstreamErrorNormalizeConf`，不做字段拍平 |
| 默认值不下发 | 缺省字段不在导出结果中合成默认值，由 BFE `Effective()` 在加载期合成（单一默认值来源，避免双端漂移） |

### 1.3 变更范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api` |
| 涉及模块 | `model/icluster_conf`（数据模型 + 导出 + 校验）、`lib/validate`（校验规则注册）、接口定义文档、schema/导出测试 |
| 变更类型 | cluster `llm_config` 新增可选对象字段 + 校验 + 导出透传 |
| 变更规模 | 5~6 处代码改动（含易漏的手工列举点，见 §5）+ 文档 + 测试；**无 DDL** |

### 1.4 兼容性

- **旧 BFE + 新控制面**：BFE 的 `AIConf` JSON 反序列化忽略未知字段，`NormalizeUpstreamError` 被丢弃，行为不变；
- **新 BFE + 旧控制面**：字段缺省 = `nil` = BFE 关闭归一（零值语义），行为不变；
- **存量数据**：`clusters.llm_config` 无该字段，导出为 `nil`，零迁移。

## 2. 数据模型改动

### 2.1 `model/icluster_conf/cluster.go`

**(a) 新增结构体**（紧随 `KeyAffinity`，`:217` 之后）：

```go
// NormalizeUpstreamError configures upstream error normalization
// (unified error codes) for this cluster. Nil means disabled (BFE keeps
// the historical pass-through behavior).
type NormalizeUpstreamError struct {
    // Enabled turns on non-streaming normalization (upstream 4xx/5xx
    // rewritten into the unified AiError body; status remapped).
    Enabled *bool `json:"enabled"`
    // StreamEnabled turns on streaming (SSE) normalization, independent of
    // Enabled for gray release (error-event payload rewrite + truncation
    // marking).
    StreamEnabled *bool `json:"stream_enabled"`
    // UnrecognizedAction: passthrough (default) or rewrite_generic.
    UnrecognizedAction *string `json:"unrecognized_action"`
    // MaxBodyBytes bounds the error body read; exceeding bodies are treated
    // as unrecognized. Values <= 0 / unset fall back to the BFE default
    // (64 KiB).
    MaxBodyBytes *int64 `json:"max_body_bytes"`
    // RedactSecrets masks cluster credential material in outgoing upstream
    // error content. Default true; explicit false disables.
    RedactSecrets *bool `json:"redact_secrets"`
}
```

**(b) `LLMConfig` 加字段**（`:224` 起，紧随 `KeyAffinity`）：

```go
type LLMConfig struct {
    ...
    KeyAffinity            *KeyAffinity            `json:"key_affinity"`
    NormalizeUpstreamError *NormalizeUpstreamError `json:"normalize_upstream_error"` // 新增
    Provider               *string                 `json:"provider"`
    ...
}
```

**(c) `FillDefaults` 不加默认值**：nil 即未配置（与 `key_affinity` 同策略，默认值由 BFE `Effective()` 合成）。

### 2.2 存储层

**无需修改**。`clusters.llm_config` 为 JSON 文本列（与 `key_affinity` 落地时一致），
直接扩展；`db_ddl.sql` / `db_ddl_sqlite.sql` 不变。

### 2.3 与 BFE 侧类型的映射

控制面结构（snake_case / 全指针）与 BFE 结构（PascalCase / `RedactSecrets *bool`）
字段一一对应，导出时转换：

| 控制面（`NormalizeUpstreamError`） | BFE（`cluster_conf.UpstreamErrorNormalizeConf`） | 缺省语义 |
|------|------|------|
| `enabled` (*bool) | `Enabled` (bool) | false |
| `stream_enabled` (*bool) | `StreamEnabled` (bool) | false |
| `unrecognized_action` (*string) | `UnrecognizedAction` (string) | `""` → BFE 按 `passthrough` 处理 |
| `max_body_bytes` (*int64) | `MaxBodyBytes` (int64) | 0 → BFE 按 65536 处理 |
| `redact_secrets` (*bool) | `RedactSecrets` (*bool) | nil → BFE 按 true 处理 |

> 指针语义的差异是刻意的：`redact_secrets` 必须区分"未配置"与"显式 false"
> （默认 true 的特性），故 BFE 侧也是 `*bool`；其余字段 BFE 侧为值类型 +
> `Effective()` 合成默认值，控制面直通即可。

## 3. 校验规则

在 cluster 参数校验入口（`LLMConfig` 校验函数，与 `key_policy` / `key_affinity`
同位置）新增，规则与 BFE `AIConfCheck` 完全对齐（`cluster_conf_load.go`）：

| 字段 | 校验规则 | 错误 |
|------|----------|------|
| `enabled` / `stream_enabled` | 可选；若传入必须为 bool | 422 参数错误 |
| `unrecognized_action` | 可选；若传入必须为 `passthrough` / `rewrite_generic` | 422 参数错误 |
| `max_body_bytes` | 可选；若传入必须 ∈ [0, 4194304]（0 = 用默认） | 422 参数错误 |
| `redact_secrets` | 可选；若传入必须为 bool | 422 参数错误 |

约束：**控制面校验与 BFE `AIConfCheck` 的规则表保持逐条一致**，防止"控制面放
行、BFE 加载失败"的配置穿透（参照 `2026-09-16-issue-172/173` 的校验对齐原则）。

## 4. 配置导出逻辑改动

`model/icluster_conf/cluster.go` 的 `newAIConf`（`:1424` 起，`KeyAffinity`
映射块 `:1435-1440` 之后）新增：

```go
if llmConfig.NormalizeUpstreamError != nil {
    aiConf.NormalizeUpstreamError = &cluster_conf.UpstreamErrorNormalizeConf{
        Enabled:            derefBool(llmConfig.NormalizeUpstreamError.Enabled, false),
        StreamEnabled:      derefBool(llmConfig.NormalizeUpstreamError.StreamEnabled, false),
        UnrecognizedAction: derefString(llmConfig.NormalizeUpstreamError.UnrecognizedAction, ""),
        MaxBodyBytes:       derefInt64(llmConfig.NormalizeUpstreamError.MaxBodyBytes, 0),
        RedactSecrets:      llmConfig.NormalizeUpstreamError.RedactSecrets, // 指针直通，保留"未配置 vs 显式 false"
    }
}
```

- `derefInt64` 如不存在则补一个（对齐既有 `derefInt` / `derefString` / `derefBool` 风格）；
- `nil` 时整个 `NormalizeUpstreamError` 不设置 → BFE 关闭（零值兼容）；
- 导出框架（`model/imods`、`model/iversion_control`、conf-agent）对 `AIConf`
  新字段无感知，随结构体透传，无改动。

## 5. 易漏的手工列举点（实施 checklist）

`KeyAffinity` 落地时的实际触点即本变更的触点，逐一核对：

1. `model/icluster_conf/cluster.go`：结构体 + `LLMConfig` 字段 + `newAIConf` 映射（§2.1/§4）；
2. cluster 参数校验函数：`NormalizeUpstreamError` 五字段校验（§3）；
3. **`endpoints/openapi_v1/product_cluster/create.go` 的 `normalizeLLMConfig`：手工字段列举函数，create/update 共用唯一咽喉点——实施时曾遗漏导致字段被静默丢弃（集成测试 CL-1-101 拦截），必须透传 `NormalizeUpstreamError`（不做默认值合成，与 key_affinity 的填默认行为刻意不同）**；
4. `test/integration/tests/schema/openapi/cluster.go` 与
   `test/integration/tests/schema/innerapi/innerapi_schema_test.go`：schema 断言补字段；
5. `model/icluster_conf/cluster_test.go`：导出器单测补用例
   （配置对象 → `cluster_conf.data` 字段存在且值正确；无该字段 → 导出为 nil）；
6. `endpoints/openapi_v1/product_cluster/create_test.go`：`normalizeLLMConfig` 透传/ nil 保持单测；
7. 接口定义文档：`design-docs/api-define/OpenAPI接口定义/clusters.md` 的
   `llm_config` 字段表 + `design-docs/api-define/InnerAPI接口定义/server-data-conf.md`
   的 `AIConf` 字段表；
8. `design-docs/sys-design/模型层设计文档.md` / `接口层设计文档.md` /
   `数据库设计文档.md`：`llm_config` 结构说明同步。

## 6. 测试计划

| 层 | 内容 |
|----|------|
| 单测 | `newAIConf` 映射：五字段全量 / 部分字段 / nil 三态；`derefInt64` |
| 单测 | 校验：合法值全通过；`unrecognized_action` 非法值、`max_body_bytes` 越界报 422 |
| 集成 | 导出器端到端：DB 中 cluster（含/不含 `normalize_upstream_error`）→ InnerAPI 导出 → `cluster_conf.data` 断言字段与值 |
| 集成 | OpenAPI：`POST/PUT /clusters` 写入 → `GET /clusters/{name}` 回读一致 |
| schema | openapi/innerapi schema 测试随字段补充更新 |
| 回归 | 存量 cluster（无该字段）导出结果与 BFE v1.8.x 行为兼容（字段缺省、BFE 关闭） |

## 7. 实施阶段

| 阶段 | 内容 | 状态 |
|------|------|------|
| 1 | BFE 数据面实现（配置加载/拦截点/SSE 过滤器/错误目录/脱敏） | ✅ 已完成（SC27 10 例全绿） |
| 2 | bfe-access-pb proto 扩展（810-815） | ✅ 已完成（v0.3.12 已推送） |
| 3 | 控制面数据模型 + 校验（本文档 §2/§3） | 🔄 待实现 |
| 4 | 控制面导出（本文档 §4）+ schema/单测/集成测试 | 🔄 待实现 |
| 5 | 文档同步（checklist §5） | 🔄 待实现 |

> 发布顺序：控制面与数据面**无发版耦合**（§1.4），可任意先后；生产灰度按
> BFE 修改说明 §7 四阶段执行（passthrough 采样 → 单集群 `Enabled` → 全量 `Enabled` +
> 镜像集群 `StreamEnabled` → 全量流式）。
