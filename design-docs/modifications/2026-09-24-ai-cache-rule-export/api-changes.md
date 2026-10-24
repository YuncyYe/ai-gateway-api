# /ai-cache-rules 与 /configs/ai-cache-rule —— API 契约变更

## 1. 变更概览

| 变更类型 | 端点 | 说明 |
|----------|------|------|
| 新增 | `GET /open-api/v1/ai-cache-rules` | 全量查询缓存规则集合（按优先级排序返回） |
| 新增 | `PUT /open-api/v1/ai-cache-rules` | 全量更新缓存规则集合（整体替换，事务完成） |
| 新增 | `GET /inner-api/v1/configs/ai-cache-rule` | 配置导出（conf-agent 轮询拉取） |

设计取向：**不提供 `/{id}` 单条规则操作接口**（无 POST 单建 / GET 单查 / PATCH / PUT 单改 / DELETE 单删）。规则总量小、整组维护，集合级全量读写是唯一入口，照 `global-route-rules`（`GetGlobalRouteRules`/`SetGlobalRouteRules`）先例。

既有接口零变更。PUT 记录一条操作审计（before/after 为整个规则集合快照，照 `model/rate_limit_policy/operation_log.go` 模式）。

## 2. 字段统一定义（Open API 词汇：小写下划线）

> 注意双层契约：Open API 字段为小写/下划线；导出给 BFE 的 JSON tag 是另一套词汇（`cacheKeyStrategy` 等），见 design-changes.md §4，两套不要混用。

| 字段 | 类型 | 必填 | 说明 | 校验 |
|------|------|------|------|------|
| `name` | string | 是 | 规则名称，同一集合内唯一（可读性/审计用，非寻址键） | 1-128 字符；集合内不得重名 |
| `cond` | string | 是 | BFE 条件表达式，如 `req_path_in("/v1/chat/completions", false) && req_body_json_in("model", "deepseek-chat", false)` | 必须能通过 BFE `condition.Build` 编译（服务端调 `lib/validate.ConditionExpression`） |
| `cache_key_strategy` | string | 否 | 缓存键策略 | `lastQuestion`（默认）/ `allQuestions` / `disabled` |
| `cache_ttl` | int | 否 | 缓存 TTL（秒） | ≥ 0；`0` 表示不过期；默认 `0` |
| `max_body_bytes` | int64 | 否 | 请求体大小上限（字节），超限不缓存 | > 0；默认 1048576（1MB） |
| `max_value_bytes` | int64 | 否 | 缓存值大小上限（字节），超限不缓存 | > 0；默认 1048576（1MB） |
| `created_at` / `updated_at` | string | - | 创建/更新时间（RFC3339，仅 GET 响应携带） | - |

说明：

- **`id` 不暴露**：表内 `id` 仅为排序与导出顺序服务（自增、PUT 整体重建），照 `global-route-rules` "Hide internal id from response" 先例，API 不出现在请求与响应中；
- **无 `enabled` 字段**：PUT 提交的列表即生效集合，"禁用一条规则" = 从列表中移除（见 §6 语义边界）。

集合对象结构：

```json
{
  "rules": [
    { "...": "规则对象，字段见上表" }
  ]
}
```

## 3. Open API 明细

### 3.1 GET /open-api/v1/ai-cache-rules

- 鉴权：`iauth.FA(iauth.FeatureAICache, iauth.ActionRead)`；
- 无分页参数：整组返回（规则总量小，照 `global-route-rules` 无分页先例）；
- 响应 Data：`{"rules": [...]}`，数组按优先级升序——**数组顺序即 first-match-wins 语义下的匹配顺序，即导出到 BFE 的顺序**；
- 空集合返回 `{"rules": []}`。

### 3.2 PUT /open-api/v1/ai-cache-rules

- 鉴权：`iauth.FA(iauth.FeatureAICache, iauth.ActionUpdate)`；
- 请求体：`{"rules": [...]}` 完整规则集合（数组顺序即优先级；`rules` 为 null 按 `[]` 处理——清空全部规则）；
- **整体替换语义**：服务端在单事务内重建规则集合（delete-all + insert-all，按数组顺序写入，新 `id` 自增即优先级序）；
- 校验（全部通过才落库，任一失败整体 422，集合不变）：
  1. 每条规则字段校验（§2 校验列）；
  2. 集合级校验：集合内 `name` 不得重复；
- 响应 Data：更新后的完整集合（同 GET 形态）；
- 操作审计：记录一条 update 审计，`before`/`after` 为整个集合快照（API 小写词汇，遵守 #201 nil-guard / #205 词汇纪律）。

**执行逻辑**：1. 鉴权 → 2. `xreq.BindJSON` 绑参 → 3. `validate.AICacheRules(param)` 全量校验（**失败则记录一条失败审计后返回 422**，身份固定为集合、before 快照取自库中现状而非请求体）→ 4. 事务内重建集合 → 5. 操作审计（成功）→ 6. 返回完整集合。

**请求示例**：

```json
{
  "rules": [
    {
      "name": "cache-deepseek-chat",
      "cond": "req_path_in(\"/v1/chat/completions\", false) && req_body_json_in(\"model\", \"deepseek-chat\", false)",
      "cache_key_strategy": "lastQuestion",
      "cache_ttl": 3600,
      "max_body_bytes": 1048576,
      "max_value_bytes": 1048576
    },
    {
      "name": "cache-all-models-long-ttl",
      "cond": "req_path_in(\"/v1/chat/completions\", false)",
      "cache_ttl": 86400
    }
  ]
}
```

## 4. Inner API 明细

### 4.1 GET /inner-api/v1/configs/ai-cache-rule

| 项目 | 说明 |
|------|------|
| 用途 | conf-agent 按 `ReloadIntervalMs` 轮询拉取 `ai_cache.data` 内容 |
| 查询参数 | `version`：conf-agent 本地当前版本（可选，首轮为空） |
| 鉴权 | `iauth.FA(iauth.FeatureAICache, iauth.ActionExport)`（走 `McUserProbe` 内部凭证） |
| 增量语义 | 服务端对生成内容做 MD5 签名并与 `config_versions` 中 `name="mod_ai_cache"` 的最新版本比对：签名相同返回旧版本号（HTTP Data 为 null，conf-agent 不落盘）；不同则插入新版本（版本号 = 时间戳 `20060102150405`） |
| 响应结构 | `{"Version": "...", "Config": {"<product>": [ ...规则... ]}}`——`Version`/`Config` 首字母大写是 conf-agent 提取版本的**硬契约**，不可改 |

**响应示例**：

```json
{
  "Version": "20260924103000",
  "Config": {
    "AI_product": [
      {
        "cond": "req_path_in(\"/v1/chat/completions\", false) && req_body_json_in(\"model\", \"deepseek-chat\", false)",
        "cacheKeyStrategy": "lastQuestion",
        "cacheTTL": 3600,
        "maxBodyBytes": 1048576,
        "maxValueBytes": 1048576
      }
    ]
  }
}
```

字段 tag 全表（含 BFE 侧默认值与二期可扩展字段）见 design-changes.md §4.2。

## 5. 校验与错误码

| 场景 | HTTP status | ErrNum | 说明 |
|------|-------------|--------|------|
| 参数校验失败（name 格式/长度、cond 编译失败、枚举非法、ttl<0、字节上限 ≤0、集合内 name 重名） | 422 | 422 | `Param Illegal`，`xerror.WrapParamErrorWithMsg` |
| 未授权 / 权限点未授予 | 401 / 403 | - | 既有鉴权行为 |

无 404（无 `/{id}` 端点）、无 409（name 冲突是请求集合内部矛盾，属参数非法）。

## 6. 语义边界速查

| 场景 | 行为 |
|------|------|
| PUT 整体替换 | 服务端 delete-all + insert-all（单事务），**不在提交列表中的规则即删除** |
| PUT `{"rules": []}` 或 `rules=null` | 清空全部规则；导出为 `Config` 中 product 对应空数组；BFE 无规则命中，天然放行 |
| 多条规则同时命中一个请求 | BFE first-match-wins；顺序 = PUT 数组顺序 = `id` 升序 = GET 返回顺序 |
| 临时停用一条规则 | 从 PUT 列表中移除（保留配置由客户端自持）；无 enabled 开关 |
| `cache_ttl=0` | 导出 `cacheTTL: 0`，BFE 语义为不过期（持久缓存） |
| 提交顺序与 `id` | PUT 整体重建后 `id` 按数组顺序重新自增，导出顺序与提交顺序一致 |
| 导出与 PUT 的时序 | 导出由 conf-agent 轮询触发，PUT 不保证即时生效；生效时延 = ReloadIntervalMs + BFE reload |
| 一期不支持的字段（响应模板、GJSON 路径） | Open API 不暴露；BFE 用内置默认（见 design-changes.md §4.4） |

## 7. 权限点

`model/iauth/features.go` 新增：

```go
// ai cache
FeatureAICache Feature = "AICache"
```

- Open API：GET → `ActionRead`，PUT → `ActionUpdate`；
- Inner API 导出：`ActionExport`；
- scope 映射：各 scope（`ScopeSupport` 等）按 rate_limit_policy 的先例给 `FeatureAICache` 授权（导出 scope 给 `ActionExport`，管理 scope 给 Read+Update）；若某 scope 不配置则该 scope 默认拒绝。

## 8. 代码变更清单（Step 5 实施参考）

| 层 | 位置 | 变更 |
|----|------|------|
| 接口层 | `endpoints/openapi_v1/ai_cache/` | 新建 `endpoints.go`：`AICacheRulesGetRoute`（GET）+ `AICacheRulesUpdateRoute`（PUT）+ 两个 action，照 `endpoints/openapi_v1/global_route_rules/endpoints.go` 模式（单文件双端点，绑参 → `validate` → `container.AICacheManager` → 隐藏内部 `id`） |
| 接口层 | `endpoints/openapi_v1/endpoints.go` | 注册 `ai_cache.Endpoints` |
| 接口层 | `endpoints/innerapi_v1/ai_cache/export.go` | `ExportRoute`：`Path=/configs/ai-cache-rule`、`Authorizer=iauth.FA(FeatureAICache, ActionExport)`、调 `container.AICacheManager.ConfigExport`；照 `endpoints/innerapi_v1/rate_limit_policy/export.go` |
| 接口层 | `endpoints/innerapi_v1/endpoints.go` | `endpoints()` 列表追加 `ai_cache.ExportRoute` |
| 校验 | `lib/validate/validate.go` | 新增 `AICacheRules(param *shared.AICacheRulesParam)`：逐条字段校验 + 集合内 name 去重；cond 走 `ConditionExpression`（内部调 BFE `condition.Build`，见 validate.go:535），照 `RouteRules(param)` 全量校验先例 |
| 模型层 | `model/shared/types.go` | `AICacheRuleParam`（规则字段）+ `AICacheRulesParam{Rules []*AICacheRuleParam}` |
| 模型层 | `model/ai_cache/` | 新建：`ai_cache.go`（storager 接口 + 数据结构）、`ai_cache_manager.go`（`GetAICacheRules`/`SetAICacheRules` 事务重建 + `ConfigExport` + `AICacheRuleGenerator` + `ConfigTopicProductAICache="mod_ai_cache"`）、`operation_log.go`、`mocks_test.go` |
| 权限 | `model/iauth/features.go` | `FeatureAICache` 常量 + scope 映射 |
| 存储层 | `storage/rdb/internal/dao/table_ai_cache_rules.go` + `storage/rdb/ai_cache/` | DAO + storager 实现（`ReplaceAll` 事务接口），照 rate_limit_policy 两层 |
| DDL | `db_ddl.sql` / `db_ddl_sqlite.sql` | `ai_cache_rules` 建表（见 design-changes.md §3.1） |
| 装配 | `stateful/container/components.go` + `stateful/container/rdb/components.go` | 声明 `AICacheManager` 与 rdb storager 并完成注入，照 rate_limit_policy 条目 |

## 9. 测试变更建议（Step 5）

| 类型 | 位置 | 用例 |
|------|------|------|
| 单测 | `model/ai_cache/mocks_test.go` | 手写 `fakeAICacheRuleStorager`（callback 字段模式） |
| 单测 | `model/ai_cache/ai_cache_manager_test.go` | `SetAICacheRules` 事务重建（顺序落库、旧数据清除、失败回滚）；`AICacheRuleGenerator`：**id 升序**、product 名取自注入配置（含非默认名）、空表导出空数组、字段 tag 与 design-changes.md §4.2 逐字一致（marshal 后断言 JSON key 集合）；`ConfigExport` 版本比对/增量返回；操作审计 |
| 单测 | `endpoints/openapi_v1/ai_cache/endpoints_test.go` | 绑参、422（逐条字段非法 / 集合内重名）、响应不含内部 `id`、空集合往返 |
| 单测 | `lib/validate` | cond 非法 422、枚举非法、ttl<0、字节上限 ≤0、name 重名 |
| 门禁 | `make test-model-cover-gate` | model 层语句覆盖率 ≥ 70% 硬门禁；新源文件带 Apache 2.0 / Rainway 许可头（`make license-fix`） |
| 集成测试 | `test/integration/tests/` | PUT 建集合 → GET 回读一致（顺序一致）；PUT 增删改混合 → 回读精确等于提交值；PUT `{"rules":[]}` → 清空；非法 cond / 重名 name → 422 且集合不变；Inner 导出端点（建集合 → 首拉含规则 → 更新 → 再拉版本变化；二次拉取版本不变=增量） |
