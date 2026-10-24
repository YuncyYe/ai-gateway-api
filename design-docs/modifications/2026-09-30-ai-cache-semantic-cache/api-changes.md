# /ai-cache-rules 扩展与 /ai-cache-semantic-settings —— API 契约变更

> 本文件是 `2026-09-24-ai-cache-rule-export` 的增量变更：既有端点形态全部保留，仅元素新增字段 + 新增单例设置端点 + 导出响应新增顶层键。未提及之处以 phase-1 契约为准。

## 1. 变更概览

| 变更类型 | 端点 | 说明 |
|----------|------|------|
| 修改（元素加字段） | `GET /open-api/v1/ai-cache-rules` / `PUT /open-api/v1/ai-cache-rules` | 规则元素新增可选字段 `enable_semantic_cache` |
| 新增 | `GET /open-api/v1/ai-cache-semantic-settings` | 查询语义全局设置（单例，不存在时返回默认值） |
| 新增 | `PUT /open-api/v1/ai-cache-semantic-settings` | 全量更新语义全局设置（upsert 语义） |
| 修改（响应加键） | `GET /inner-api/v1/configs/ai-cache-rule` | 导出 JSON 新增顶层 `Semantic` 块（恒导出） |

设计取向：设置为**单例资源**（全系统一份，非集合），形态照 phase-1 集合资源的"GET 读全量 + PUT 全量写"两步风格，无 `/{id}`、无 PATCH。既有 `ai-cache-rules` 端点继续**不提供单条操作接口**（phase-1 决策不变）。

## 2. 字段统一定义（Open API 词汇：小写下划线）

### 2.1 规则元素新增字段（并入 phase-1 字段表）

| 字段 | 类型 | 必填 | 说明 | 校验 |
|------|------|------|------|------|
| `enable_semantic_cache` | bool | 否 | 是否启用语义缓存（该规则命中且 Redis 未命中时走 embedding + 向量检索）；`cache_key_strategy=disabled` 的规则上该字段无效（被忽略，不报错） | 默认 `false`；无需组合校验 |

- GET 响应**始终携带**该字段（默认填充 `false`），与 phase-1 `cache_key_strategy` 等默认值填充风格一致；
- PUT 请求可省略（按 `false` 处理）；`rules` 集合级校验（name 唯一等）不变。

### 2.2 语义全局设置对象（新）

| 字段 | 类型 | 必填 | 说明 | 校验 |
|------|------|------|------|------|
| `top_k` | int | 否 | 向量检索近邻个数，仅取最优者判定 | 1–10，默认 `1` |
| `threshold` | float64 | 否 | 相似度阈值（量纲由 `threshold_relation` 决定） | 0–2，默认 `0.15`（Chroma cosine distance 语境保守值，上线前须按 embedding 模型校准） |
| `threshold_relation` | string | 否 | 阈值比较方向 | `lt`（默认）/ `lte` / `gt` / `gte`；distance 语义（越小越相似）用 `lt/lte`，similarity 语义用 `gt/gte` |

设置对象无 `name`/`id`/`created_at` 之外的寻址字段（单例）；GET 响应携带 `created_at`/`updated_at`（RFC3339）。

```json
{ "top_k": 1, "threshold": 0.15, "threshold_relation": "lt" }
```

## 3. Open API 明细

### 3.1 GET /open-api/v1/ai-cache-semantic-settings

- 鉴权：`iauth.FA(iauth.FeatureAICache, iauth.ActionRead)`；
- 设置行不存在时返回**默认值对象**（`1/0.15/lt`，无时间戳字段），与 BFE 侧缺省行为一致；
- 响应 Data：设置对象（§2.2）。

### 3.2 PUT /open-api/v1/ai-cache-semantic-settings

- 鉴权：`iauth.FA(iauth.FeatureAICache, iauth.ActionUpdate)`；
- 请求体：设置对象（§2.2，三个字段均可省略走默认）；
- **upsert 语义**：单事务内不存在则插入、存在则整行更新（单行表，无并发增长概念）；
- 校验失败整体 422，设置不变；
- 响应 Data：更新后的完整设置对象（同 GET）；
- 操作审计：记录一条 update 审计，`before` 为库中现状（空表时为默认值对象快照），`after` 为新值。

**执行逻辑**：1. 鉴权 → 2. `xreq.BindJSON` 绑参 → 3. `validate.AICacheSemanticSettings(param)`（失败记失败审计后 422）→ 4. 事务 upsert → 5. 操作审计（成功）→ 6. 返回完整设置。

### 3.3 既有 rules GET/PUT 的变化点

- 请求/响应元素新增 `enable_semantic_cache`（§2.1）；
- `validate.AICacheRules` 增加该字段的类型/默认值处理（bool 无需范围校验）；
- 其余语义（整体替换、顺序即优先级、无 enabled、无 `/{id}`）与 phase-1 完全一致。

## 4. Inner API 明细（导出响应变更）

### 4.1 GET /inner-api/v1/configs/ai-cache-rule

端点路径、鉴权、增量语义、`Version`/`Config` 硬契约**均不变**（phase-1 契约全部保留）。唯一变化：响应 JSON **新增顶层 `Semantic` 块，恒导出**：

```json
{
  "Version": "20261010120000",
  "Semantic": {
    "topK": 1,
    "threshold": 0.15,
    "thresholdRelation": "lt"
  },
  "Config": {
    "AI_product": [
      {
        "cond": "req_path_in(\"/v1/chat/completions\", false) && req_body_json_in(\"model\", \"deepseek-chat\", false)",
        "cacheKeyStrategy": "lastQuestion",
        "cacheTTL": 3600,
        "enableSemanticCache": true,
        "maxBodyBytes": 1048576,
        "maxValueBytes": 1048576
      }
    ]
  }
}
```

- 规则元素导出新增 `enableSemanticCache`（camelCase，恒导出，默认 `false`）；
- `Semantic` 块字段 tag（`topK`/`threshold`/`thresholdRelation`）已与 BFE `SemanticConfFile` 逐字段冻结（BFE 侧已实现并入库，见 design-changes.md §4）；
- MD5 签名覆盖含 `Semantic` 的全量生成内容：修改设置 → 签名变化 → conf-agent 拉到新版本（版本号时间戳规则不变）。

## 5. 校验与错误码

| 场景 | HTTP status | ErrNum | 说明 |
|------|-------------|--------|------|
| `top_k` 越界、`threshold` 越界、`threshold_relation` 非法枚举 | 422 | 422 | `Param Illegal`，`xerror.WrapParamErrorWithMsg` |
| 设置校验失败 | 422 | 422 | 同上；记录失败审计，设置不变 |
| 未授权 / 权限点未授予 | 401 / 403 | - | 既有鉴权行为 |

无 404（单例资源空表返默认值，非 404）、无 409。

## 6. 语义边界速查

| 场景 | 行为 |
|------|------|
| 设置行不存在 | GET 返默认值；导出 `Semantic` 块用默认值 |
| PUT 设置后 rules 不动 | 导出同时含新 `Semantic` 与原规则（两者独立维护、共享版本流） |
| 清空规则（`{"rules":[]}`） | `Semantic` 块**仍导出**（设置独立于规则生命周期） |
| `enable_semantic_cache=true` 但 disabled 策略 | 导出原样透传；BFE 侧忽略（BFE 校验策略：组合不报错） |
| 导出时延 | PUT 不保证即时生效，时延 = ReloadIntervalMs + BFE reload（同 phase-1） |
| 旧版 BFE 收到 `Semantic` 块 | 忽略未知顶层字段，精确缓存行为不变（向后兼容） |

## 7. 权限点

**零新增**：复用 `FeatureAICache`（phase-1 已建）。

- settings GET → `ActionRead`；settings PUT → `ActionUpdate`；
- Inner API 导出 → `ActionExport`（phase-1 scope 映射直接覆盖）。

## 8. 代码变更清单（Step 5 实施参考）

| 层 | 位置 | 变更 |
|----|------|------|
| DDL | `db_ddl.sql` / `db_ddl_sqlite.sql` | `ai_cache_rules` 加列 `enable_semantic_cache`；新建 `ai_cache_semantic_settings` 单行表（见 design-changes.md §3） |
| 存储层 | `storage/rdb/internal/dao/table_ai_cache_rules.go` + 新 `table_ai_cache_semantic_settings.go`；`storage/rdb/ai_cache/` | 规则 DAO 加列；设置 storager（`Get`/`Upsert` 单行） |
| 模型层 | `model/shared/types.go` | `AICacheRuleParam` 加 `EnableSemanticCache *bool`；新增 `AICacheSemanticSettingsParam` |
| 模型层 | `model/ai_cache/` | `ai_cache.go` 加设置存储结构；`ai_cache_manager.go` 加 `GetSemanticSettings`/`SetSemanticSettings`（upsert + 审计委托）+ `AICacheRuleGenerator` 合并 `Semantic` 块；`operation_log.go` 复用；`mocks_test.go` 补 fake |
| 校验 | `lib/validate/validate.go` | `AICacheRules` 增加 bool 字段处理；新增 `AICacheSemanticSettings(param)`（范围 + 枚举） |
| 接口层 | `endpoints/openapi_v1/ai_cache/` | 新增 `AICacheSemanticSettingsGetRoute`/`AICacheSemanticSettingsUpdateRoute` 两 action（可新文件 `semantic_settings.go`）；注册进 `endpoints.go` |
| 装配 | `stateful/container/components.go` + `stateful/container/rdb/components.go` | settings storager 注入（ai_cache 条目扩展） |

Inner API 端点文件（`endpoints/innerapi_v1/ai_cache/export.go`）**不改代码**——契约变化全部在生成器数据内。

## 9. 测试变更建议（Step 5）

| 类型 | 位置 | 用例 |
|------|------|------|
| 单测 | `model/ai_cache/ai_cache_manager_test.go` | 生成器：`Semantic` 块恒存在且 tag 逐字一致（marshal 后断言 key 集合）、设置行缺省导出默认值、设置行存在导出配置值、修改设置→MD5/版本变化；`SetSemanticSettings` upsert（空表插入/存在更新）、GET 缺省默认值 |
| 单测 | `endpoints/openapi_v1/ai_cache/` | settings 绑参、422（top_k 越界/relation 非法）、GET 空表默认值往返、PUT→GET 一致；rules PUT 带 `enable_semantic_cache` 往返 |
| 单测 | `lib/validate` | 范围/枚举边界（0、2、10、11、非法字符串） |
| 门禁 | `make test-model-cover-gate` | model 层语句覆盖率 ≥ 70% 硬门禁；新源文件带 Apache 2.0 / Rainway 许可头 |
| 集成测试 | `test/integration/tests/` | PUT rules（含语义开关）→ GET 回读一致；GET settings 默认 → PUT 自定义 → 回读；Inner 导出含 `Semantic` 块与开关字段 → 改设置再导出 → 版本变化；rules 清空后 `Semantic` 块仍导出 |
