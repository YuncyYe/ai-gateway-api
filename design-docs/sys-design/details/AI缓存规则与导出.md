# AI 缓存规则与导出

> 2026-09-30 二期增量：语义缓存（`enable_semantic_cache` 规则开关 + 顶层 `Semantic` 全局块 + 单例设置资源 `ai_cache_semantic_settings`）。BFE 数据面已实现（`docs/zh_cn/modifications/2026-09-30-ai-cache-semantic-cache/`，bfe `v1.8.9-dev`）。变更方案见 `design-docs/modifications/2026-09-30-ai-cache-semantic-cache/`。下文以"二期"标注增量部分。

## 1. 概述

AI 缓存规则（`ai_cache_rules`）是 AI 网关缓存能力（一期 Redis 精确匹配；二期语义缓存）的控制面配置入口。数据面由 BFE `mod_ai_cache` 模块消费规则文件（`ai_cache.data`）：命中规则的请求由 BFE 直接从缓存构造响应（非流式 JSON / 流式 SSE），不再转发到后端；访问日志记录 `ai_cache_status`（hit / hit_semantic / miss / skip）、语义命中时的 `ai_cache_semantic` / `ai_cache_similarity`（791/792）与 `ai_cache_key`。

规则文件由 ai-gateway-api 生成、conf-agent 轮询拉取并触发 BFE 热加载，与既有配置导出链路（`topic + generator + MD5 签名 + config_versions + Inner API 轮询`）完全一致，本次为纯登记式扩展，导出框架零改动。

---

## 2. 资源模型

### 2.1 集合资源 `ai_cache_rules`

- Open API 仅暴露集合级读写：`GET /open-api/v1/ai-cache-rules`（全量查询）+ `PUT /open-api/v1/ai-cache-rules`（整体替换），照 `global-route-rules` 先例；
- **无 `/{id}` 端点**：不提供单条规则的增删改查；`id` 为表内自增排序字段，**不暴露到 API**；
- **无 `enabled` 字段**：PUT 提交的列表即生效集合，"禁用一条规则" = 从列表移除；
- **一组有序规则，first-match-wins**：顺序 = PUT 数组顺序 = `id` 升序 = GET 返回顺序 = 导出数组顺序；不设显式 `priority` 字段；
- `cache_key_strategy=disabled` 是**数据面缓存豁免**语义（BFE `mod_ai_cache` 命中即返回不缓存并阻断后续规则），与控制面"规则不在列表即不存在"是两个概念；典型用法是"全部缓存、除外某模型/路径"（BFE 侧条件表达式难以表达否定子句）；
- PUT 为单事务整体重建（delete-all + insert-all）：不在提交列表中的规则即删除；`{"rules": []}` 清空全部规则。

### 2.3 单例资源 `ai_cache_semantic_settings`（二期）

- 语义缓存**全局调优参数**（`top_k` / `threshold` / `threshold_relation`）：描述向量检索与判定的全局特性，部署内一份——评审明确不做规则级重复配置（避免过度设计）；
- **不走 `model/imods` 模式**：imods 每个 manager 产出独立 topic 文件（如 `mod_body_process.data`），而 `Semantic` 块必须并入 `ai_cache.data` 顶层（BFE 只加载这一个文件）；故设置为 ai_cache 域内子资源，由 `AICacheRuleGenerator` 合并导出；
- Open API：`GET/PUT /open-api/v1/ai-cache-semantic-settings`，**upsert 语义**（单行表，不存在则插入、存在则整行覆盖）；GET 空表返默认值对象（`1 / 0.15 / lt`）；
- **生命周期独立于规则集合**：清空 `/ai-cache-rules` 不影响设置，`Semantic` 块仍导出；
- 复用 `FeatureAICache` 权限点，不新增 Feature。

### 2.2 与 rate_limit_policy 的关键差异

| 维度 | rate_limit_policy | ai_cache_rules |
|------|-------------------|----------------|
| 资源形态 | api-key/entity 下的子资源（沿层级继承合并） | 全局集合资源（整组替换） |
| API 形态 | 内嵌在 api-key/entity 端点里 | 集合级 GET/PUT（照 global-route-rules） |
| 导出内容 | 策略定义 + 绑定关系 + 需生成 Redis key | 仅规则数组；无绑定、无 Redis key |
| Redis 连接 | 不下发（静态 conf + CopyFiles） | 不下发（同左） |
| topic | `mod_ai_rate_limit` | `mod_ai_cache` |

---

## 3. 数据模型

### 3.1 表结构

```sql
CREATE TABLE `ai_cache_rules` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（排序/优先级用，不暴露API）',
  `name` VARCHAR(128) NOT NULL COMMENT '规则名称（集合内唯一，可读性/审计用）',
  `cond` TEXT NOT NULL COMMENT 'BFE条件表达式，如 req_path_in(...) && req_body_json_in("model", ...)',
  `cache_key_strategy` VARCHAR(32) NOT NULL DEFAULT 'lastQuestion' COMMENT '缓存键策略：lastQuestion|allQuestions|disabled',
  `cache_ttl` INT NOT NULL DEFAULT 0 COMMENT '缓存TTL（秒），0表示不过期',
  `max_body_bytes` BIGINT NOT NULL DEFAULT 1048576 COMMENT '请求体大小上限（字节），超限不缓存',
  `max_value_bytes` BIGINT NOT NULL DEFAULT 1048576 COMMENT '缓存值大小上限（字节），超限不缓存',
  `enable_semantic_cache` TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否启用语义缓存: 0-否, 1-是（二期）',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  UNIQUE KEY `uk_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI缓存规则表';
```

要点：

- **无 `enabled` 列**（全量替换模型下冗余，见 §2.1）；
- `uk_name` 唯一键兜底集合内重名（服务端校验先行，DB 为最后防线）；
- SQLite 版本（`db_ddl_sqlite.sql`）照 `rate_limit_policies` 的 SQLite 惯例改写：类型映射（`BIGINT`→`INTEGER`、`TEXT`/`VARCHAR`→`TEXT`、`DATETIME`→`TEXT`）+ `updated_at` 由触发器维护；新表对存量库为零迁移；
- **二期加列**（存量环境 upgrade 脚本，幂等）：`ALTER TABLE ai_cache_rules ADD COLUMN enable_semantic_cache TINYINT(1) NOT NULL DEFAULT 0`——存量规则语义关闭，向后兼容。

### 3.1.1 语义全局设置表 `ai_cache_semantic_settings`（二期，单行）

```sql
CREATE TABLE `ai_cache_semantic_settings` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（恒为1语义，物理单行）',
  `top_k` INT NOT NULL DEFAULT 1 COMMENT '语义检索TopK（1-10）',
  `threshold` DOUBLE NOT NULL DEFAULT 0.15 COMMENT '相似度阈值（量纲与 threshold_relation 一致，0-2）',
  `threshold_relation` VARCHAR(8) NOT NULL DEFAULT 'lt' COMMENT '阈值比较: gt/gte/lt/lte',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI缓存语义全局设置表（单行）';
```

要点：**空表 = 默认值**（不显式插入默认行，单一真相在 `model/ai_cache/` 的默认值常量）；`Get` 空表返回 `(nil, nil)` 由 model 层转默认值对象；`Upsert` 采用 delete-all + insert（单行表）；SQLite 版本照 `ai_cache_rules` 惯例改写（含 `updated_at` 触发器）。

### 3.2 Open API 数据形态

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

| 字段 | 类型 | 说明 | 合法性条件 |
|------|------|------|------------|
| `rules` | array | 规则列表，按数组顺序匹配（first-match-wins），顺序即优先级 | 必填；`null` 按 `[]` 处理（清空全部规则） |
| `rules[].name` | string | 规则名称（可读性/审计用，非寻址键） | 必填；1-128 字符；同一集合内不得重复 |
| `rules[].cond` | string | BFE 条件表达式，命中即对该请求启用缓存 | 必填；必须能通过 BFE `condition.Build` 编译 |
| `rules[].cache_key_strategy` | string | 缓存键策略；`disabled` 为显式不缓存（缓存豁免，阻断后续规则匹配） | 非必填；`lastQuestion`（默认）/ `allQuestions` / `disabled` |
| `rules[].cache_ttl` | int | 缓存 TTL（秒），`0` 表示不过期 | 非必填；未传时默认 `0`；≥ 0 |
| `rules[].max_body_bytes` | int64 | 请求体大小上限（字节），超限不缓存 | 非必填；未传时默认 1048576（1MB）；> 0 |
| `rules[].max_value_bytes` | int64 | 缓存值大小上限（字节），超限不缓存 | 非必填；未传时默认 1048576（1MB）；> 0 |
| `rules[].enable_semantic_cache` | bool | 语义缓存开关（二期）；`cache_key_strategy=disabled` 时无效（BFE 忽略，不报错） | 非必填；未传时默认 `false` |

**不返回** `rules[].created_at`/`rules[].updated_at`：本资源为整组替换语义（每次 PUT 全删重建，`id` 自增重排），逐规则时间戳无信息价值（与 `/ai-context-rules` 一致，issue #258）；请求体若携带这两个键，按未知字段忽略。规则 `id` 为内部排序字段，不出现在 API 请求与响应中。

---

## 4. Open API 读写语义

### 4.1 接口

| Method | Path | 权限 | 说明 |
|--------|------|------|------|
| GET | `/open-api/v1/ai-cache-rules` | `FeatureAICache + ActionRead` | 全量查询；空集合返回 `{"rules": []}`；按优先级升序返回（顺序同导出到 BFE 的顺序） |
| PUT | `/open-api/v1/ai-cache-rules` | `FeatureAICache + ActionUpdate` | 全量更新（整体替换）；返回更新后全量集合 |
| GET | `/open-api/v1/ai-cache-semantic-settings` | `FeatureAICache + ActionRead` | 查询语义全局设置（二期）；空表返默认值对象 |
| PUT | `/open-api/v1/ai-cache-semantic-settings` | `FeatureAICache + ActionUpdate` | 全量更新语义全局设置（upsert，单行覆盖） |

### 4.2 校验（`lib/validate.AICacheRules`，任一失败则整个 PUT 422，集合不变）

| 校验项 | 规则 |
|--------|------|
| `rules` | `null` 按 `[]` 处理；单条为 null 拒绝 |
| 每条 `name` | 非空、1-128 字符 |
| 每条 `cond` | 非空 + `ConditionExpression`（内部调 BFE `condition.Build` 编译） |
| 每条 `cache_key_strategy` | ∈ `lastQuestion` / `allQuestions` / `disabled` |
| 每条 `cache_ttl` | ≥ 0 |
| 每条 `max_body_bytes` / `max_value_bytes` | > 0 |
| 每条 `enable_semantic_cache`（二期） | bool 类型；无组合校验——`disabled` 策略上的开关由 BFE 忽略（两侧一致，不报错） |
| 集合内 `name` 唯一 | 遍历查重（422，请求集合内部矛盾，不用 409） |

控制面校验把错误拦在写入前（fail-fast）；BFE 侧校验（最后防线）：`cond` 必须 `condition.Build` 编译通过、`cacheKeyStrategy` 枚举、`cacheTTL >= 0`、`maxBodyBytes`/`maxValueBytes > 0`、同一 product 内 `cond` 不可重复（BFE `ProductRuleConfListFile.Check` 拒绝；控制面不做集合内 cond 去重）。

### 4.3 事务重建

PUT 的执行语义为**单事务整体重建**：

1. `itxn.TxnStorager.AtomExecute` 开启事务（禁止 ad-hoc 事务）；
2. delete-all：删除 `ai_cache_rules` 全表旧数据；
3. insert-all：按提交数组顺序逐条写入；新 `id` 自增序即优先级序，与 GET/导出顺序一致；
4. 事务失败整体回滚，集合保持原状；
5. 事务成功后记录操作日志（`resource_type=ai_cache_rule`，`before`/`after` 为整个集合快照）；失败也记录一条失败审计（照 rate_limit_policy 的失败审计先例）。

本接口为整组规则的唯一写入口，配置生效时延 = conf-agent 轮询周期（`ReloadIntervalMs`）+ BFE 热加载时间。

### 4.4 语义设置读写语义（二期）

PUT `/ai-cache-semantic-settings` 为单事务 **upsert**：delete-all + insert（单行表）；校验（`lib/validate.AICacheSemanticSettings`）：`top_k` ∈ [1,10]、`threshold` ∈ [0,2]、`threshold_relation` ∈ 四枚举，全部字段可省略走默认（`1 / 0.15 / lt`）；任一失败 422 且设置不变（记失败审计）。事务成功记录 update 审计（`resource_type=ai_cache_semantic_settings`，`before` 空表时为默认值快照）。

---

## 5. 导出契约（冻结）

> 本节是控制面/数据面的**接口契约**，已与 BFE `mod_ai_cache` 规则加载器逐字段核对冻结。任何一侧改字段必须先改本节并知会另一侧。

### 5.1 文件结构

```json
{
  "Version": "20260930103000",
  "Semantic": {
    "topK": 1,
    "threshold": 0.15,
    "thresholdRelation": "lt"
  },
  "Config": {
    "<product>": [ /* ProductRuleConfFile 数组，first-match-wins */ ]
  }
}
```

- `Version` / `Config` 首字母大写：conf-agent 提取版本与配置的**硬契约**；
- `Semantic`（二期新增）为顶层并列键，**恒导出**（设置行不存在时用默认值 `1 / 0.15 / lt`）；旧版 BFE 加载器忽略未知顶层字段，向后兼容；
- `<product>` 从运行时配置取：`stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（默认 `AI_product`），**不要硬编码**——与 mod_api_key/mod_body_process 导出一致；
- `Config` 的 value 为数组；数组顺序 = `id` 升序 = PUT 提交顺序。

### 5.2 字段 tag 全表（`ProductRuleConfFile`）

| 导出 JSON tag | Go 字段 | 一期是否导出 | BFE 缺省填充（`setDefaults`） | BFE 校验（`Check`） |
|---------------|---------|--------------|------------------------------|---------------------|
| `cond` | `Cond *string` | **是（必导）** | 无默认——**缺失即加载失败**（`CheckNilField`） | 必须 `condition.Build` 编译通过 |
| `cacheKeyStrategy` | `CacheKeyStrategy *string` | 是 | `lastQuestion` | ∈ `lastQuestion` / `allQuestions` / `disabled` |
| `cacheTTL` | `CacheTTL *int` | 是 | 模块 `Basic.DefaultCacheTTL`（默认 3600，conf 可配） | ≥ 0（0 = 不过期） |
| `cacheKeyFrom` | `CacheKeyFrom *string` | 否（二期） | `""`（用内置默认 GJSON 路径 `messages.@reverse.0.content`） | - |
| `cacheValueFrom` | `CacheValueFrom *string` | 否（二期） | `choices.0.message.content` | - |
| `cacheStreamValueFrom` | `CacheStreamFrom *string` | 否（二期） | `choices.0.delta.content` | - |
| `cacheToolCallsFrom` | `CacheToolCallsFrom *string` | 否（预留） | `""` | - |
| `responseTemplate` | `ResponseTemplate *string` | 否（二期） | 内置非流式模板（JSON，`%s` 占位） | 必须含 `%s` |
| `streamResponseTemplate` | `StreamResponseTemplate *string` | 否（二期） | 内置 SSE 模板（`%s` 占位） | 必须含 `%s` |
| `maxBodyBytes` | `MaxBodyBytes *int64` | 是 | 1048576（1MB） | > 0 |
| `maxValueBytes` | `MaxValueBytes *int64` | 是 | 1048576（1MB） | > 0 |
| `enableSemanticCache` | `EnableSemanticCache *bool` | **是（二期起恒导出）** | `false` | bool；`cacheKeyStrategy=disabled` 时 BFE 忽略（不报错） |

### 5.2.1 顶层 `Semantic` 块 tag 全表（`SemanticConfFile`，二期）

| 导出 JSON tag | Go 字段 | 默认 | BFE 校验 | 说明 |
|---------------|---------|------|----------|------|
| `topK` | `TopK *int` | 1 | ∈ [1,10] | 向量检索近邻个数 |
| `threshold` | `Threshold *float64` | 0.15 | ∈ [0,2] | 相似度阈值（量纲随 relation；cosine distance 语境保守默认，须按模型校准） |
| `thresholdRelation` | `ThresholdRelation *string` | `lt` | ∈ `lt`/`lte`/`gt`/`gte` | distance 语义越小越相似 / similarity 语义越大越相似 |

设置存于 Open API 单例资源 `ai_cache_semantic_settings`（小写下划线词汇），生成器合并导出；空表用默认值（单一真相在 `model/ai_cache/` 默认值常量，与 BFE `setDefaults` 数值一一对应）。

**一期导出最小字段集**：每条规则只导出 `cond` / `cacheKeyStrategy` / `cacheTTL` / `maxBodyBytes` / `maxValueBytes` 五个字段，其余字段由 BFE `setDefaults` 填默认。理由：

- 控制面只管理"规则语义"字段；命中响应模板、GJSON 提取路径属高级调优项，一期不开放可降低 API 与表结构复杂度；
- 二期开放时：表加列 → Open API 加字段 → generator 加导出字段，契约表已预留 tag，无需再与数据面对齐命名。

**易错点**：

1. 流式答案路径的 tag 是 `cacheStreamValueFrom`（不是 `cacheStreamFrom`）——Go 字段名与 JSON tag 刻意不同，二期开放该字段时照抄 tag；
2. `cond` 没有默认值，**每条导出规则必须带 `cond`**；
3. 同一 product 内 `cond` 不可重复；控制面不做集合内 cond 去重（BFE 加载报错兜底）；
4. BFE 规则加载器对**所有**字段（含未导出的）在 `setDefaults` 后统一 `CheckNilField`——控制面漏导任何"标记为一期导出"的字段会直接导致 BFE 加载失败，属 P0 级契约事故。

---

## 6. 生成器流程

### 6.1 入口

```go
func (m *AICacheManager) ConfigExport(ctx context.Context, lastVersion string) (*iversion_control.ExportData, error)
```

### 6.2 `AICacheRuleGenerator` 流程

1. 查全部规则，按 `id` 升序（**无 enabled 过滤**——提交列表即生效集合）；
2. （二期）查语义设置行，空表用默认值（`1 / 0.15 / lt`）组装顶层 `Semantic` 块；
3. 组装 `Config[AIRouteInnerProductName] = 规则数组`（规则结构使用与 §5.2 tag 完全一致的导出专用 struct，定义在 `model/ai_cache/` 内；二期起每条带 `EnableSemanticCache`）；
4. 空表时导出空数组（product 键仍 present，值为 `[]`）；**`Semantic` 块不受规则清空影响，恒导出**；
5. 套 `iversion_control.ExportConfig(ctx, ConfigTopicProductAICache, generator)` 标准流程：生成数据 → MD5 签名（覆盖含 `Semantic` 的全量内容）→ 比对 `config_versions`（`name="mod_ai_cache"`）→ 签名相同返回旧版本（增量，HTTP Data 为 null）→ 不同则插新版本（版本号 = 时间戳 `20060102150405`，同 Topic 严格单调递增，机制见《InnerAPI配置导出与版本控制.md》）。**修改语义设置同样驱动版本流**。

topic 常量 `ConfigTopicProductAICache = "mod_ai_cache"` 定义在 `model/ai_cache/ai_cache_manager.go` 内（与 rate_limit 的 topic 定义在自身包内一致）。

### 6.3 Inner API 端点

```go
// endpoints/innerapi_v1/ai_cache/export.go
var ExportRoute = &xreq.Endpoint{
    Path:       "/configs/ai-cache-rule",
    Method:     http.MethodGet,
    Handler:    xreq.Convert(ExportAction),
    Authorizer: iauth.FA(iauth.FeatureAICache, iauth.ActionExport),
}

func ExportAction(req *http.Request) (interface{}, error) {
    param, err := export_util.NewExportFromReq(req) // 解析 version
    if err != nil {
        return nil, err
    }
    return container.AICacheManager.ConfigExport(req.Context(), param.Version)
}
```

conf-agent 侧登记（`conf-agent.toml`，非本仓库代码改动）：`[Reloaders.mod_ai_cache]`：`ConfAPI=/inner-api/v1/configs/ai-cache-rule`、`ReloadFile=ai_cache.data`、`BFEReloadAPI=/reload/mod_ai_cache`、`CopyFiles=["ai_cache.data","mod_ai_cache.conf"]`。

---

## 7. Redis 连接配置不下发

`cache` 连接块（serviceName/host/port/password/database）**不进导出链路**，放 BFE 静态 `mod_ai_cache.conf`（Redis 段风格对齐 `mod_ai_rate_limit.conf`：BNS serviceName + 连接池 + 超时），由 conf-agent `CopyFiles` 随规则文件一起落盘。理由：

- 连接信息与规则生命周期不同（近乎不变）；
- 减少 Redis 密码在导出链路中的暴露面；
- 与 `mod_ai_token_auth.conf` / `mod_ai_rate_limit.conf` 同款。

Redis 实例一期建议复用 rate_limit/token_auth 的实例，在 `mod_ai_cache.conf` 里配独立 `database` 编号做逻辑隔离；该选择只影响静态 conf 内容，不影响本设计。

---

## 8. 与 rate_limit_policy 导出的差异

| 维度 | rate_limit_policy 导出 | ai_cache_rules 导出 |
|------|------------------------|---------------------|
| 导出内容 | 策略定义 + API-Key 绑定关系（两段） | 仅规则数组（一段） |
| Redis key 生成 | 控制面为每条 TPM/RPM 规则生成稳定 `redis_key` 并下发（`RL_TPM_rlp-<id>_<name>` 等） | **无 Redis key 生成**；缓存键由数据面在请求处理时按 `cacheKeyStrategy` 构造（键带凭证前缀，租户隔离在数据面实现） |
| enabled 过滤 | 跳过 `enabled=false` 的策略 | 无过滤，全表 `id` 升序导出 |
| Entity 层级合并 | 沿 `parent_id` 递归收集策略 ID | 无层级、无绑定概念，全局一份 |
| 产物文件 | `rate_limit_policies.json` + `api_key_rl_policy_bindings.json` | `ai_cache.data`（单文件，`Version` + `Config`） |
| 规则变更副作用 | 改名 = 删旧 + 增新，旧规则 Redis Key 被清理 | 控制面无 Redis Key 清理职责；全量替换后 BFE 按新集合加载 |
| 版本控制 | `mod_ai_rate_limit` topic | `mod_ai_cache` topic（独立版本线） |

---

## 9. BFE 侧预期行为

BFE `mod_ai_cache` 收到 `ai_cache.data` 并热加载后：

1. 请求到达时按数组顺序逐条匹配规则（first-match-wins）；
2. 命中 `cache_key_strategy=disabled` 的规则：不读不写缓存，直接放行到后端，且不再匹配后续规则（缓存豁免）；
3. 命中其余规则：按 `cacheKeyStrategy` 构造缓存键（键内带凭证前缀实现租户隔离），**Redis 精确查询命中则直接构造响应**（非流式 JSON / 流式 SSE 模板）——精确命中绝不触发 embedding；
4. （二期）Redis 未命中且规则 `enableSemanticCache=true`（且 BFE 已配置 `[embedding]`/`[vector]`）：问题文本 embedding → Chroma 向量 TopK 检索（强制 `tenant_id` + TTL 过滤）→ 按 `Semantic` 块阈值判定，命中返回缓存答案（状态 `hit_semantic`）；未命中则回源，回写时答案写 Redis（`SETEX`，受 `cacheTTL` 等约束），`(question, embedding, answer)` 异步写向量库；
5. 无规则命中：天然放行，等同未启用缓存；
6. 访问日志记录 `ai_cache_status`（hit / hit_semantic / miss / skip）、语义命中时的 `ai_cache_semantic` / `ai_cache_similarity`（791/792，bfe-access-pb v0.3.10）与 `ai_cache_key`；语义命中置 `AiCacheHit=true`，`mod_ai_token_auth` 跳过配额扣减（二期零改动）。

> BFE 具体缓存实现（Redis 连接池、序列化）不在本文档范围；Redis 连接以静态 `mod_ai_cache.conf` 为准（§7）。

---

## 10. 边界情况

| 场景 | 行为 |
|------|------|
| PUT `{"rules": []}` | 清空全部规则；导出空数组（product 键仍 present），BFE 侧无规则命中、天然放行 |
| PUT 部分规则校验失败 | 整个 PUT 422，单事务回滚，GET 集合不变 |
| 集合内 `name` 重名 | 422（请求集合内部矛盾，不用 409）；`uk_name` 为 DB 最后防线 |
| `cond` 编译失败 | 422（Open API 层 `ConditionExpression` 校验，fail-fast） |
| 集合内 `cond` 重复 | 控制面不拦截；BFE `ProductRuleConfListFile.Check` 拒绝加载（兜底） |
| 漏导一期字段 | BFE `CheckNilField` 加载失败（P0 级契约事故）；单测对 marshal 结果做 JSON key 精确断言防回归 |
| 漏导 `Semantic` 块 / `enableSemanticCache` | BFE 语义能力按默认值运行（**不报错**——二期字段全部可选，缺省即默认）；单测断言两键恒存在 |
| PUT 设置非法（top_k 越界 / relation 非法枚举） | 422，设置不变，记失败审计 |
| 清空规则后 `Semantic` 块 | **仍导出**（设置独立于规则生命周期）；BFE 无规则命中、天然放行 |
| `enable_semantic_cache=true` + `disabled` 策略 | 控制面透传不拦截；BFE 忽略该开关（组合不报错，两侧一致） |
| 旧版 BFE 收到顶层 `Semantic` | 忽略未知顶层字段，精确缓存行为不变（向后兼容） |
| 导出后规则又变更 | 下次轮询时 MD5 签名变化 → 新版本号 → conf-agent 拉取并热加载；生效时延 = 轮询周期 + 热加载时间 |
| 二期开放预留字段 | 表加列 → Open API 加字段 → generator 加导出字段，tag 照 §5.2 契约表，无需再对齐命名 |

---

## 11. 相关文件索引

| 文件 | 说明 |
|------|------|
| `model/ai_cache/ai_cache.go` | `AICacheRuleParam`/`AICacheRulesParam`、数据结构 ↔ param 转换、storager 接口 |
| `model/ai_cache/ai_cache_manager.go` | `AICacheManager`：集合全量读写、`ConfigExport`、`AICacheRuleGenerator`、topic 常量 |
| `model/ai_cache/operation_log.go` | PUT 审计（集合快照 before/after） |
| `storage/rdb/ai_cache/ai_cache.go` | `AICacheStorager` 实现：`FetchAll` + `ReplaceAll`（事务内 delete-all + insert-all） |
| `storage/rdb/internal/dao/table_ai_cache_rules.go` | `ai_cache_rules` 表 DAO（二期带 `enable_semantic_cache` 列） |
| `storage/rdb/internal/dao/table_ai_cache_semantic_settings.go`、`storage/rdb/ai_cache/settings_storager.go`（二期） | 语义全局设置 DAO / storager（`Get` 空表返 nil、`Upsert` 单行覆盖） |
| `endpoints/openapi_v1/ai_cache/` | Open API 端点（rules GET + PUT；二期新增 settings GET + PUT） |
| `endpoints/innerapi_v1/ai_cache/export.go` | Inner API 导出端点（`/configs/ai-cache-rule`，二期零代码改动——契约变化在生成器数据内） |
| `lib/validate/validate.go` | `AICacheRules` 集合级全量校验；二期新增 `AICacheSemanticSettings` |
| `stateful/container/components.go`、`stateful/container/rdb/components.go` | 容器声明与装配（AICacheStorager / AICacheManager；二期注入 settings storager） |
| `design-docs/api-define/OpenAPI接口定义/ai-cache-rules.md` | Open API 接口定义 |
| `design-docs/api-define/OpenAPI接口定义/ai-cache-semantic-settings.md`（二期） | 语义设置单例接口定义 |
| `design-docs/api-define/InnerAPI接口定义/ai-cache-rule.md` | Inner API 导出接口定义（二期含顶层 `Semantic` 块） |
| `design-docs/modifications/2026-09-24-ai-cache-rule-export/` | 一期变更摘要与设计变更说明 |
| `design-docs/modifications/2026-09-30-ai-cache-semantic-cache/`（二期） | 二期变更摘要、API 契约与设计变更说明 |
