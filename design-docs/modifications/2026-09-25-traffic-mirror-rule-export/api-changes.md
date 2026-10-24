# /traffic-mirror-rules 与 /configs/traffic-mirror-rule —— API 契约变更

## 1. 变更概览

| 变更类型 | 端点 | 说明 |
|----------|------|------|
| 新增 | `GET /open-api/v1/traffic-mirror-rules` | 全量查询镜像规则集合（按优先级排序返回） |
| 新增 | `PUT /open-api/v1/traffic-mirror-rules` | 全量更新镜像规则集合（整体替换，事务完成） |
| 新增 | `GET /inner-api/v1/configs/traffic-mirror-rule` | 配置导出（conf-agent 轮询拉取） |

设计取向：**不提供 `/{id}` 单条规则操作接口**（无 POST 单建 / GET 单查 / PATCH / DELETE 单删）。规则总量小、整组维护，集合级全量读写是唯一入口，照 `ai-cache-rules` / `global-route-rules` 先例。

既有接口零变更。PUT 记录一条操作审计（before/after 为整个规则集合快照，照 `model/ai_cache/operation_log.go` 模式）。

## 2. 字段统一定义（Open API 词汇：小写下划线）

> 注意双层契约：Open API 字段为小写/下划线；导出给 BFE 的 JSON tag 是另一套词汇（`mirrorCluster` / `percentage` 等），见 design-changes.md §4，两套不要混用。

| 字段 | 类型 | 必填 | 说明 | 校验 |
|------|------|------|------|------|
| `name` | string | 是 | 规则名称，同一集合内唯一（可读性/审计用，非寻址键） | 1-128 字符；集合内不得重名 |
| `cond` | string | 是 | BFE 条件表达式，如 `req_path_prefix_in("/v1/chat/completions", true) && req_body_json_in("model", "gpt-4o", false)`；**全部匹配须显式写 `default_t()`**（与 `/ai-cache-rules` 一致，空串/缺省不接受） | **必填**；必须能通过 BFE `condition.Build` 编译（`lib/validate.ConditionExpression`）；同一集合内不得重复 |
| `mirror_cluster` | string | 是 | 镜像目标 cluster 名（须为控制面已创建的 cluster） | 1-128 字符；endpoint 层做存在性校验（不存在 → 422） |
| `percentage` | int | 否 | 镜像采样百分比 | 0-100；缺省 `100`（全量镜像；建议 Web 表单引导保守值） |
| `remove_headers` | []string | 否 | 镜像副本剔除的敏感 Header 黑名单。**缺省（未提交该字段）= 控制面填默认黑名单 `["Authorization","Cookie","X-Api-Key"]`；显式提交空数组 `[]` = 不剔除**（高级场景自担合规风险） | 元素非空；集合内查重无意义不校验 |
| `set_headers` | map[string]string | 否 | 镜像副本注入的自定义 Header | key/value 非空；`X-Bfe-Mirror` 由 BFE 在缺失时兜底注入 `true`，用户配置的同名 key 优先生效——**同名 key 不建议配置**（标识语义以 BFE 注入为准） |
| `body_rewrites` | []object | 否 | body JSON 字段改写（新模型双跑验证），元素为 `{"path","value"}` | **一期 `path` 仅允许 `"model"`**（与 BFE `Check` 硬校验一致）；`path`/`value` 必填、value 非空 |
| `path_rewrite` | string | 否 | 镜像路径整体替换（query 保留）；空串/缺省 = 不改写 | 以 `/` 开头 |
| `created_at` / `updated_at` | string | - | 创建/更新时间（RFC3339，仅 GET 响应携带） | - |

说明：

- **`id` 不暴露**：表内 `id` 仅为排序与导出顺序服务（自增、PUT 整体重建），照 `global-route-rules` "Hide internal id from response" 先例，API 不出现在请求与响应中；
- **无 `enabled` 字段**：PUT 提交的列表即生效集合，"禁用一条规则" = 从列表中移除；单条临时停用可用 `percentage=0`（命中但不采样，BFE 计 `skip_total{reason="sample"}`）；
- **`cond` 必填**，全部匹配显式写 `default_t()`（与 ai_cache 一致）：空串无法区分"故意全匹配"与"漏传"，而空 cond 静默生效 = 100% 流量镜像 = 双倍推理成本，不接受隐式默认。

集合对象结构：

```json
{
  "rules": [
    { "...": "规则对象，字段见上表" }
  ]
}
```

## 3. Open API 明细

### 3.1 GET /open-api/v1/traffic-mirror-rules

- 鉴权：`iauth.FA(iauth.FeatureTrafficMirror, iauth.ActionRead)`；
- 无分页参数：整组返回（规则总量小，照 `global-route-rules` 无分页先例）；
- 响应 Data：`{"rules": [...]}`，数组按优先级升序——**数组顺序即 first-match-wins 语义下的匹配顺序，即导出到 BFE 的顺序**；
- 空集合返回 `{"rules": []}`。

### 3.2 PUT /open-api/v1/traffic-mirror-rules

- 鉴权：`iauth.FA(iauth.FeatureTrafficMirror, iauth.ActionUpdate)`；
- 请求体：`{"rules": [...]}` 完整规则集合（数组顺序即优先级；`rules` 为 null 按 `[]` 处理——清空全部规则）；
- **整体替换语义**：服务端在单事务内重建规则集合（delete-all + insert-all，按数组顺序写入，新 `id` 自增即优先级序）；
- 校验（全部通过才落库，任一失败整体 422，集合不变）：
  1. 每条规则字段校验（§2 校验列，含 `cond` 必填）；
  2. 集合级校验：集合内 `name` 不得重复；集合内 `cond` 不得重复（如两条均写 `default_t()`——BFE 同 product 内拒绝重复 cond）；
  3. 引用校验：`mirror_cluster` 必须已存在（endpoint 层调 `icluster_conf.ClusterManager.FetchClusterList` 按名批量过滤）；
- 响应 Data：更新后的完整集合（同 GET 形态）；
- 操作审计：记录一条 update 审计，`before`/`after` 为整个集合快照（API 小写词汇，遵守 #201 nil-guard / #205 词汇纪律）。

**执行逻辑**：1. 鉴权 → 2. `xreq.BindJSON` 绑参 → 3. `validate.TrafficMirrorRules(param)` 全量字段与集合校验（**失败则记录一条失败审计后返回 422**，身份固定为集合、before 快照取自库中现状而非请求体）→ 4. `mirror_cluster` 存在性校验（失败同样 422 + 失败审计）→ 5. 事务内重建集合 → 6. 操作审计（成功）→ 7. 返回完整集合。

**请求示例**：

```json
{
  "rules": [
    {
      "name": "mirror-gpt4o-to-shadow-v2",
      "cond": "req_path_prefix_in(\"/v1/chat/completions\", true) && req_body_json_in(\"model\", \"gpt-4o\", false)",
      "mirror_cluster": "cluster_shadow_v2",
      "percentage": 10,
      "body_rewrites": [
        {"path": "model", "value": "deepseek-v3"}
      ]
    },
    {
      "name": "mirror-all-chat-fallback-drill",
      "cond": "req_path_prefix_in(\"/v1/chat/completions\", true)",
      "mirror_cluster": "cluster_fallback_drill",
      "percentage": 1,
      "remove_headers": ["Authorization", "Cookie", "X-Api-Key", "X-Custom-Secret"]
    }
  ]
}
```

第一条缺省 `remove_headers` → 控制面填默认黑名单；第二条显式扩展黑名单。

## 4. Inner API 明细

### 4.1 GET /inner-api/v1/configs/traffic-mirror-rule

| 项目 | 说明 |
|------|------|
| 用途 | conf-agent 按 `ReloadIntervalMs` 轮询拉取 `mirror_rule.data` 内容 |
| 查询参数 | `version`：conf-agent 本地当前版本（可选，首轮为空） |
| 鉴权 | `iauth.FA(iauth.FeatureTrafficMirror, iauth.ActionExport)`（走 `McUserProbe` 内部凭证） |
| 增量语义 | 服务端对生成内容做 MD5 签名并与 `config_versions` 中 `name="mod_traffic_mirror"` 的最新版本比对：签名相同返回旧版本号（HTTP Data 为 null，conf-agent 不落盘）；不同则插入新版本（版本号 = 时间戳 `20060102150405`） |
| 响应结构 | `{"Version": "...", "Config": {"<product>": [ ...规则... ]}}`——`Version`/`Config` 首字母大写是 conf-agent 提取版本的**硬契约**，不可改；`<product>` 取自 `stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（默认 `AI_product`），不硬编码 |

**响应示例**：

```json
{
  "Version": "20260925103000",
  "Config": {
    "AI_product": [
      {
        "cond": "req_path_prefix_in(\"/v1/chat/completions\", true) && req_body_json_in(\"model\", \"gpt-4o\", false)",
        "mirrorCluster": "cluster_shadow_v2",
        "percentage": 10,
        "removeHeaders": ["Authorization", "Cookie", "X-Api-Key"],
        "setHeaders": {},
        "bodyRewrites": [{"path": "model", "value": "deepseek-v3"}],
        "pathRewrite": ""
      }
    ]
  }
}
```

- 规则全字段恒导出（空值输出零值：`{}` / `[]` / `""`），导出物显式、可 diff；
- 空表时导出 `"Config": {"AI_product": []}`（product 键仍 present）；
- 字段 tag 全表（含 BFE 侧默认值）见 design-changes.md §4.2。

## 5. 校验与错误码

| 场景 | HTTP status | ErrNum | 说明 |
|------|-------------|--------|------|
| 参数校验失败（name 格式/长度/重名、cond 编译失败、cond 集合内重复、mirror_cluster 为空、percentage 越界、body_rewrites path 非 model、path_rewrite 非 / 开头、set_headers 空 key/value 等） | 422 | 422 | `Param Illegal`，`xerror.WrapParamErrorWithMsg` |
| `mirror_cluster` 引用不存在 | 422 | 422 | `Param Illegal`（请求体引用了不存在的资源，属参数非法；不采用 404——无 `/{cluster}` 寻址语义，照集合内重名同例） |
| 未授权 / 权限点未授予 | 401 / 403 | - | 既有鉴权行为 |

无 404（无 `/{id}` 端点）、无 409（name/cond 冲突是请求集合内部矛盾，属参数非法）。

## 6. 语义边界速查

| 场景 | 行为 |
|------|------|
| PUT 整体替换 | 服务端 delete-all + insert-all（单事务），**不在提交列表中的规则即删除** |
| PUT `{"rules": []}` 或 `rules=null` | 清空全部规则；导出为 `Config` 中 product 对应空数组；BFE 无规则命中，零镜像流量（**镜像"总开关"= 清空本集合**） |
| 多条规则同时命中一个请求 | BFE first-match-wins；顺序 = PUT 数组顺序 = `id` 升序 = GET 返回顺序 |
| 两条规则 cond 相同（如都写 `default_t()`） | 422：集合内 cond 唯一；BFE 同 product 内拒绝重复 cond，控制面 fail-fast |
| 临时停用一条规则 | 改 `percentage=0`（保留规则、停止采样）或从 PUT 列表移除 |
| `remove_headers` 缺省 vs 显式 `[]` | 缺省 → 导出默认黑名单（剔鉴权/会话头）；显式 `[]` → 导出空数组（不剔除，合规自负） |
| `percentage` 缺省 | 导出 `100`（全量镜像）；采样比例的成本责任在配置方，Web 表单应引导保守值 |
| `body_rewrites` 缺省 | 导出 `[]`，镜像副本 body 与生产一致 |
| 提交顺序与 `id` | PUT 整体重建后 `id` 按数组顺序重新自增，导出顺序与提交顺序一致 |
| 导出与 PUT 的时序 | 导出由 conf-agent 轮询触发，PUT 不保证即时生效；生效时延 = ReloadIntervalMs + BFE reload |
| 镜像目标的鉴权/凭证 | 一期不在规则内配置凭证（开放问题，见 design-changes.md §12）；目标集群侧通过免鉴权白名单 + 网络隔离承接 |
| 模块级调参（超时/并发/熔断阈值） | 不在 Open API 暴露；BFE 静态 `mod_traffic_mirror.conf` 经 conf-agent `CopyFiles` 下发（见 design-changes.md §7） |

## 7. 权限点

`model/iauth/features.go` 新增：

```go
// traffic mirror
FeatureTrafficMirror Feature = "TrafficMirror"
```

- Open API：GET → `ActionRead`，PUT → `ActionUpdate`；
- Inner API 导出：`ActionExport`；
- scope 映射：各 scope 按 `FeatureAICache` 的先例给 `FeatureTrafficMirror` 授权（导出 scope 给 `ActionExport`，管理 scope 给 Read+Update）；若某 scope 不配置则该 scope 默认拒绝。

## 8. 代码变更清单（Step 5 实施参考）

| 层 | 位置 | 变更 |
|----|------|------|
| 接口层 | `endpoints/openapi_v1/traffic_mirror/` | 新建 `endpoints.go`：`TrafficMirrorRulesGetRoute`（GET）+ `TrafficMirrorRulesUpdateRoute`（PUT）+ 两个 action，照 `endpoints/openapi_v1/ai_cache/endpoints.go` 模式（单文件双端点，绑参 → `validate` → `mirror_cluster` 存在性校验 → `container.TrafficMirrorManager` → 隐藏内部 `id`） |
| 接口层 | `endpoints/openapi_v1/endpoints.go` | 注册 `traffic_mirror.Endpoints` |
| 接口层 | `endpoints/innerapi_v1/traffic_mirror/export.go` | `ExportRoute`：`Path=/configs/traffic-mirror-rule`、`Authorizer=iauth.FA(FeatureTrafficMirror, ActionExport)`、调 `container.TrafficMirrorManager.ConfigExport`；照 `endpoints/innerapi_v1/ai_cache/export.go` |
| 接口层 | `endpoints/innerapi_v1/endpoints.go` | `endpoints()` 列表追加 `traffic_mirror.ExportRoute` |
| 校验 | `lib/validate/validate.go` | 新增 `TrafficMirrorRules(param *shared.TrafficMirrorRulesParam)`：逐条字段校验 + 集合内 name 去重 + **集合内 cond 去重**；cond 走 `ConditionExpression`，照 `AICacheRules(param)` 全量校验先例 |
| 模型层 | `model/shared/types.go` | `TrafficMirrorRuleParam`（规则字段；`RemoveHeaders *[]string` 用指针区分"缺省"与"显式空数组"）+ `TrafficMirrorRulesParam{Rules []*TrafficMirrorRuleParam}` |
| 模型层 | `model/traffic_mirror/` | 新建：`traffic_mirror.go`（storager 接口 + 数据结构 + 默认黑名单常量）、`traffic_mirror_manager.go`（`GetTrafficMirrorRules`/`SetTrafficMirrorRules` 事务重建 + `ConfigExport` + `TrafficMirrorRuleGenerator` + `ConfigTopicProductTrafficMirror="mod_traffic_mirror"`）、`operation_log.go`、`mocks_test.go` |
| 权限 | `model/iauth/features.go` | `FeatureTrafficMirror` 常量 + scope 映射 |
| 存储层 | `storage/rdb/internal/dao/table_traffic_mirror_rules.go` + `storage/rdb/traffic_mirror/` | DAO + storager 实现（`ReplaceAll` 事务接口），照 ai_cache 两层 |
| DDL | `db_ddl.sql` / `db_ddl_sqlite.sql` | `traffic_mirror_rules` 建表（见 design-changes.md §3.1） |
| 装配 | `stateful/container/components.go` + `stateful/container/rdb/components.go` | 声明 `TrafficMirrorManager` 与 rdb storager 并完成注入（manager 额外注入 `icluster_conf.ClusterManager` 供 endpoint 层存在性校验使用，或经构造函数传入只读接口） |

## 9. 测试变更建议（Step 5）

| 类型 | 位置 | 用例 |
|------|------|------|
| 单测 | `model/traffic_mirror/mocks_test.go` | 手写 `fakeTrafficMirrorRuleStorager`（callback 字段模式） |
| 单测 | `model/traffic_mirror/traffic_mirror_manager_test.go` | `SetTrafficMirrorRules` 事务重建（顺序落库、旧数据清除、失败回滚）；`TrafficMirrorRuleGenerator`：**id 升序**、product 名取自注入配置、空表导出空数组、字段 tag 与 design-changes.md §4.2 逐字一致（marshal 后断言 JSON key 集合）、**remove_headers 缺省填默认黑名单/显式空数组不填**；`ConfigExport` 版本比对/增量返回；操作审计 |
| 单测 | `endpoints/openapi_v1/traffic_mirror/endpoints_test.go` | 绑参、422（逐条字段非法 / 集合内重名 / cond 重复 / mirror_cluster 不存在）、响应不含内部 `id`、空集合往返 |
| 单测 | `lib/validate` | cond 非法、cond 集合内重复、percentage 越界、body_rewrites path 非 model、path_rewrite 非 / 开头 |
| 门禁 | `make test-model-cover-gate` | model 层语句覆盖率 ≥ 70% 硬门禁；新源文件带 Apache 2.0 / Rainway 许可头（`make license-fix`） |
| 集成测试 | `test/integration/tests/` | PUT 建集合 → GET 回读一致（顺序一致）；PUT 增删改混合 → 回读精确等于提交值；PUT `{"rules":[]}` → 清空；非法 cond / 重名 / cond 重复 / 未知 cluster → 422 且集合不变；Inner 导出端点（建集合 → 首拉含规则且 tag 为 `mirrorCluster` 风格、removeHeaders 缺省规则已填默认黑名单 → 二次拉取版本不变=增量） |
