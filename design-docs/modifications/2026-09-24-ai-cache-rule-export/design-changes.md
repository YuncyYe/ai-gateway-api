# ai_cache_rules 集合资源与 ai_cache.data 导出 —— 设计变更说明

## 1. 概述

### 1.1 变更背景

AI 网关一期缓存能力（ai-cache，简化版：仅 Redis 精确匹配）落地，控制面需要提供缓存规则的全量读写与配置导出。数据面 BFE `mod_ai_cache` 模块按"规则文件由 ai-gateway-api 生成、conf-agent 热加载下发"的既有模式消费 `ai_cache.data`。

### 1.2 变更目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-09-24 |
| 涉及仓库 | `ai-gateway-api` |
| 变更类型 | 新增集合资源 + 新增导出 topic |
| 参照样板 | 导出链路 `model/rate_limit_policy` + `endpoints/innerapi_v1/rate_limit_policy`；集合全量读写 `endpoints/openapi_v1/global_route_rules`（`Get`/`Set` 模式）；最简导出 `model/imods/mod_body_process.go` |

### 1.3 设计原则

| 原则 | 说明 |
|------|------|
| 框架零改动 | 导出框架（`topic + generator + MD5 签名 + config_versions + 轮询`）是通用机制，本次纯登记式扩展 |
| 集合资源 | 规则不分条寻址（无 `/{id}` API），Open API 仅集合级 GET/PUT 全量读写，照 `global-route-rules` 先例；与 rate_limit_policy 的子资源模型**刻意不同** |
| 控制面不生成 Redis key | 租户/凭证隔离由数据面在缓存键内实现（键带凭证前缀），控制面导出物中不出现任何 Redis key 逻辑（`shared.BuildBFERateLimitRedisKey` 那套不需要） |
| 契约先行 | 导出 JSON 字段 tag 已与 BFE `mod_ai_cache` 规则加载器逐字段核对并冻结（§4），两侧开发以本节为准 |
| 敏感面最小化 | Redis 连接/密码不进导出链路，走静态 conf + CopyFiles（§7） |

---

## 2. 资源模型

### 2.1 集合资源 `ai_cache_rules`

- Open API 仅暴露集合级读写：`GET /open-api/v1/ai-cache-rules`（全量查询）+ `PUT /open-api/v1/ai-cache-rules`（整体替换）；
- 无 `/{id}` 端点：不提供单条规则的增删改查；`id` 为表内自增排序字段，**不暴露到 API**（照 `global-route-rules` "Hide internal id from response" 先例）；
- 无 `enabled` 字段：PUT 提交的列表即生效集合，"禁用一条规则" = 从列表移除；
- `cache_key_strategy=disabled` 是**数据面缓存豁免**语义（BFE `Match` 命中即返回不缓存并阻断后续规则），与控制面"规则不在列表即不存在"是两个概念，勿混淆；典型用法是"全部缓存、除外某模型/路径"（BFE 侧条件表达式难以表达否定子句）；
- 语义：**一组有序规则，first-match-wins**。顺序 = PUT 数组顺序 = `id` 升序 = GET 返回顺序 = 导出数组顺序；不设显式 `priority` 字段；
- PUT 为单事务整体重建（delete-all + insert-all）：不在提交列表中的规则即删除；`{"rules": []}` 清空全部规则。

### 2.2 与 rate_limit_policy 的关键差异

| 维度 | rate_limit_policy | ai_cache_rules |
|------|-------------------|----------------|
| 资源形态 | api-key/entity 下的子资源（沿层级继承合并） | 全局集合资源（整组替换） |
| API 形态 | 内嵌在 api-key/entity 端点里 | 集合级 GET/PUT（照 global-route-rules） |
| 导出内容 | 策略定义 + 绑定关系 + 需生成 Redis key | 仅规则数组；无绑定、无 Redis key |
| Redis 连接 | 不下发（同左） | 不下发（同左） |
| topic | `mod_ai_rate_limit` | `mod_ai_cache` |

---

## 3. 数据模型

### 3.1 表结构 `ai_cache_rules`

参照 `rate_limit_policies`（`db_ddl.sql` 约 :490 起，行号随主干漂移）的模式，MySQL DDL：

```sql
-- create ai_cache_rules (AI缓存规则表)
DROP TABLE IF EXISTS `ai_cache_rules`;
CREATE TABLE `ai_cache_rules` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（排序/优先级用，不暴露API）',
  `name` VARCHAR(128) NOT NULL COMMENT '规则名称（集合内唯一，可读性/审计用）',
  `cond` TEXT NOT NULL COMMENT 'BFE条件表达式，如 req_path_in(...) && req_body_json_in("model", ...)',
  `cache_key_strategy` VARCHAR(32) NOT NULL DEFAULT 'lastQuestion' COMMENT '缓存键策略：lastQuestion|allQuestions|disabled',
  `cache_ttl` INT NOT NULL DEFAULT 0 COMMENT '缓存TTL（秒），0表示不过期',
  `max_body_bytes` BIGINT NOT NULL DEFAULT 1048576 COMMENT '请求体大小上限（字节），超限不缓存',
  `max_value_bytes` BIGINT NOT NULL DEFAULT 1048576 COMMENT '缓存值大小上限（字节），超限不缓存',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  UNIQUE KEY `uk_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI缓存规则表';
```

要点：

- **无 `enabled` 列**（全量替换模型下冗余，见 §2.1）；
- `uk_name` 唯一键兜底集合内重名（服务端校验先行，DB 为最后防线）；
- SQLite 版本（`db_ddl_sqlite.sql`）照 `rate_limit_policies` 的 SQLite 惯例改写：类型映射（`BIGINT`→`INTEGER`、`TEXT`/`VARCHAR`→`TEXT`、`DATETIME`→`TEXT`）+ `updated_at` 由触发器维护。新增表对存量库为零迁移（新装即全量）。

### 3.2 DAO 与 storager

| 层 | 文件 | 说明 |
|----|------|------|
| DAO | `storage/rdb/internal/dao/table_ai_cache_rules.go` | 表级操作：全量查询（id 升序）+ `ReplaceAll`（事务内 delete-all + insert-all），照 `table_rate_limit_policies.go` |
| storager | `storage/rdb/ai_cache/ai_cache.go` | `model/ai_cache` 的 storager 接口实现，事务走 `itxn.TxnStorager` |
| 接口 | `model/ai_cache/ai_cache.go` | storager 接口定义：`FetchAll(ctx)` + `ReplaceAll(ctx, rules)` |

---

## 4. 导出契约（冻结）

> 本节是控制面/数据面的**接口契约**，已与 BFE `mod_ai_cache` 规则加载器（`bfe_modules/mod_ai_cache/cache_rule_load.go` 的 `ProductRuleConfFile`）逐字段核对。任何一侧改字段必须先改本节并知会另一侧。

### 4.1 文件结构

```json
{
  "Version": "20260924103000",
  "Config": {
    "<product>": [ /* ProductRuleConfFile 数组，first-match-wins */ ]
  }
}
```

- `Version` / `Config` 首字母大写：conf-agent 提取版本与配置的**硬契约**；
- `<product>` 从运行时配置取：`stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（`stateful/config.go`，默认 `AI_product`），**不要硬编码**——与 mod_api_key/mod_body_process 导出一致；
- `Config` 的 value 为数组；数组顺序 = `id` 升序 = PUT 提交顺序。

### 4.2 字段 tag 全表（`ProductRuleConfFile`）

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

**易错点**：

1. 流式答案路径的 tag 是 `cacheStreamValueFrom`（不是 `cacheStreamFrom`）——Go 字段名与 JSON tag 刻意不同，二期开放该字段时照抄 tag；
2. `cond` 没有默认值，**每条导出规则必须带 `cond`**；
3. 同一 product 内 `cond` 不可重复（BFE `ProductRuleConfListFile.Check` 拒绝）；控制面不做集合内 cond 去重（BFE 加载报错兜底），但 Open API 层对每条 cond 做编译校验；

> 2026-10-20 修订（issue #261）：控制面已改为在 PUT 时对集合内 `cond` 查重（422），本条“控制面不做去重”的决策被取代；见 modifications/2026-10-20-issue-261-ai-cache-rules-duplicate-cond/。

4. BFE 规则加载器对**所有**字段（含未导出的）在 `setDefaults` 后统一 `CheckNilField`——控制面漏导任何"标记为一期导出"的字段会直接导致 BFE 加载失败，属 P0 级契约事故。

### 4.3 控制面校验与 BFE 校验的对应关系

| 校验 | 控制面（Open API 层） | 数据面（BFE 加载层） |
|------|----------------------|----------------------|
| cond 编译 | `lib/validate.ConditionExpression`（调 BFE `condition.Build`） | `condition.Build` |
| cacheKeyStrategy 枚举 | 是 | 是 |
| cacheTTL ≥ 0 | 是 | 是 |
| maxBodyBytes/maxValueBytes > 0 | 是 | 是 |
| 模板含 `%s` | 一期不暴露该字段 | 是（用默认模板恒过） |
| 集合内 name 唯一 | 是（`uk_name` 兜底） | - |
| 集合内 cond 去重 | 是（PUT 422，issue #261） | 是（最后防线） |

控制面校验的目标是把错误拦在写入前（fail-fast），BFE 校验是最后防线。

> 2026-10-20 修订（issue #261）：控制面已改为在 PUT 时对集合内 `cond` 查重（422），本条“控制面不做去重”的决策被取代；见 modifications/2026-10-20-issue-261-ai-cache-rules-duplicate-cond/。

### 4.4 导出最小字段集策略

一期 generator 每条规则**只导出** `cond` / `cacheKeyStrategy` / `cacheTTL` / `maxBodyBytes` / `maxValueBytes` 五个字段，其余字段由 BFE `setDefaults` 填默认。理由：

- 控制面只管理"规则语义"字段；命中响应模板、GJSON 提取路径属高级调优项，一期不开放可降低 API 与表结构复杂度；
- 二期开放时：表加列 → Open API 加字段 → generator 加导出字段，契约表（§4.2）已预留 tag，无需再与数据面对齐命名。

### 4.5 Generator 逻辑（`AICacheRuleGenerator`）

1. 查全部规则，按 `id` 升序（**无 enabled 过滤**——提交列表即生效集合）；
2. 组装 `Config[AIRouteInnerProductName] = 规则数组`（规则结构使用与 §4.2 tag 完全一致的导出专用 struct，定义在 `model/ai_cache/` 内）；
3. 空表时导出空数组（product 键仍 present，值为 `[]`）；
4. 套 `iversion_control.ExportConfig(ctx, ConfigTopicProductAICache, generator)` 标准流程：生成数据 → MD5 签名 → 比对 `config_versions`（`name="mod_ai_cache"`）→ 签名相同返回旧版本（增量，HTTP Data 为 null）→ 不同则插新版本（版本号 = 时间戳 `20060102150405`）。

### 4.6 Inner API 端点

照 `endpoints/innerapi_v1/rate_limit_policy/export.go`（`ExportRoute` 模式）：

```go
var ExportRoute = &xreq.Endpoint{
    Path:       "/configs/ai-cache-rule",
    Method:     http.MethodGet,
    Handler:    xreq.Convert(ExportAction),
    Authorizer: iauth.FA(iauth.FeatureAICache, iauth.ActionExport),
}
```

- 注册：`endpoints/innerapi_v1/endpoints.go` 的 `endpoints()` 追加一行；
- handler 内：`export_util.NewExportFromReq(req)` 解析 `version` → `container.AICacheManager.ConfigExport(ctx, param.Version)`；
- topic 常量 `ConfigTopicProductAICache = "mod_ai_cache"` 定义在 `model/ai_cache/ai_cache_manager.go` 内（与 rate_limit 的 topic 定义在自身包内一致）。

---

## 5. model/ai_cache 设计

### 5.1 文件构成（照 `model/rate_limit_policy/` 的组织 + `global_route_rules` 的读写形态）

| 文件 | 内容 |
|------|------|
| `ai_cache.go` | `AICacheRuleParam`/`AICacheRulesParam`（引用 `model/shared`）、数据结构 ↔ param 转换、storager 接口 |
| `ai_cache_manager.go` | `AICacheManager`：`GetAICacheRules` / `SetAICacheRules`（事务重建）/ `ConfigExport` + `AICacheRuleGenerator` + topic 常量；依赖经构造函数注入（storager、versionControlManager、operationLog、运行时 product 名），**不硬依赖 `stateful.DefaultConfig`**（测试可 mock，见 AGENTS.md 约定） |
| `operation_log.go` | PUT 审计（一条 update，`before`/`after` 为整个集合快照；API 小写词汇，遵守 #201 nil-guard / #205 词汇纪律） |
| `mocks_test.go` | 手写 callback mock（`fakeAICacheRuleStorager` 等） |

### 5.2 manager 方法草图

```go
func (m *AICacheManager) GetAICacheRules(ctx context.Context) ([]*shared.AICacheRuleParam, error)          // id 升序
func (m *AICacheManager) SetAICacheRules(ctx context.Context, param *shared.AICacheRulesParam) (*shared.AICacheRulesParam, error) // 事务：delete-all + insert-all（按数组顺序）
func (m *AICacheManager) ConfigExport(ctx context.Context, lastVersion string) (*ExportAICacheRuleConfig, error) // 照 rate_limit_policy_manager.go:128 先例：返回具体配置类型（签名相同/增量时返回 nil），ExportData 包装由 generator 完成
```

`SetAICacheRules` 要点：

- 单事务内完成整体替换，`itxn.TxnStorager` 包裹（禁止 ad-hoc 事务）；
- 数组顺序写入 → 新 `id` 自增序即优先级序，与 GET/导出顺序一致；
- 事务失败整体回滚，集合保持原状；
- 审计在事务成功后记录（失败也记录一条失败审计，照 rate_limit_policy 的失败审计先例）。

---

## 6. 参数校验（`lib/validate`）

新增 `AICacheRules(param *shared.AICacheRulesParam)`（集合级全量校验，照 `RouteRules(param)` 先例；任一失败则整个 PUT 422，集合不变）：

| 校验项 | 规则 | 失败 |
|--------|------|------|
| rules | null 按 `[]` 处理；单条为 null 拒绝 | `WrapParamErrorWithMsg` |
| 每条 name | 非空、1-128 字符 | `WrapParamErrorWithMsg` |
| 每条 cond | 非空 + `ConditionExpression`（validate.go:535，内部调 BFE `condition.Build`） | `WrapParamErrorWithMsg` |
| 每条 cache_key_strategy | ∈ `lastQuestion`/`allQuestions`/`disabled` | `WrapParamErrorWithMsg` |
| 每条 cache_ttl | ≥ 0 | `WrapParamErrorWithMsg` |
| 每条 max_body_bytes / max_value_bytes | > 0 | `WrapParamErrorWithMsg` |
| 集合内 name 唯一 | 遍历查重 | `WrapParamErrorWithMsg`（422，请求集合内部矛盾，不用 409） |
| 集合内 cond 唯一 | 遍历查重（逐字节比较，与 BFE 同判据；issue #261） | `WrapParamErrorWithMsg`（422） |

---

## 7. Redis 连接配置不下发

`cache` 连接块（serviceName/host/port/password/database）**不进导出链路**，放 BFE 静态 `mod_ai_cache.conf`（BFE 侧配置文件，Redis 段风格对齐 `mod_ai_rate_limit.conf`：BNS serviceName + 连接池 + 超时），由 conf-agent `CopyFiles` 随规则文件一起落盘。conf-agent 与控制面均零额外代码改动，仅需 TOML 登记 `[Reloaders.mod_ai_cache]`（见 change-summary.md §5）。

---

## 8. 容器装配

| 文件 | 改动 |
|------|------|
| `stateful/container/rdb/components.go` | 声明 ai_cache storager（DAO 注入） |
| `stateful/container/components.go` | 声明 `AICacheManager`，注入 storager + `iversion_control` manager + operationLog + `stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（**仅装配点允许触碰 DefaultConfig**，manager 内不直连） |
| `endpoints/openapi_v1/endpoints.go` | 注册 `ai_cache.Endpoints` |
| `endpoints/innerapi_v1/endpoints.go` | 注册 `ai_cache.ExportRoute` |

---

## 9. 测试计划

### 9.1 单元测试（硬门禁）

| 位置 | 覆盖点 |
|------|--------|
| `model/ai_cache/mocks_test.go` | `fakeAICacheRuleStorager`（callback 字段模式，照 `mocks_test.go` 惯例） |
| `model/ai_cache/ai_cache_manager_test.go` | `SetAICacheRules` 事务重建（按序写入、旧数据清除、失败回滚）；`GetAICacheRules` 顺序；`AICacheRuleGenerator`：**id 升序**、product 名取自注入配置（含非默认名）、空表导出空数组、字段 tag 与 §4.2 逐字一致（marshal 后断言 JSON key 集合）；`ConfigExport` 版本比对/增量返回；PUT 审计（成功 + 失败两路） |
| `endpoints/openapi_v1/ai_cache/endpoints_test.go` | 绑参、422（逐条字段非法 / 集合内重名）、响应不含内部 `id`、空集合往返 |
| `lib/validate` | cond 非法、枚举非法、ttl<0、字节上限 ≤0、name 重名 |

`make test-model-cover-gate`：model 层语句覆盖率 ≥ 70% 是硬门禁。新源文件带 Apache 2.0 / Rainway 许可头（`make license-fix`）。

### 9.2 集成测试（`test/integration/tests/`）

| 用例 | 断言 |
|------|------|
| PUT → GET 往返 | 提交 3 条（含省略可选字段）→ GET 回读精确等于提交值（默认值回填、顺序一致） |
| 全量替换语义 | 二次 PUT 增 1 删 1 改 1 → GET 精确等于第二次提交（被删规则消失） |
| 清空 | PUT `{"rules":[]}` → GET `{"rules":[]}` |
| 校验 | 任一规则 cond 语法错误 / 集合内 name 重名 → 422 且 GET 集合不变 |
| Inner 导出 | PUT 集合 → `GET /inner-api/v1/configs/ai-cache-rule` 首拉含规则且 tag 为 `cacheKeyStrategy` 风格、顺序与提交一致；二次拉取版本不变（增量）；清空 → 导出空数组 |

### 9.3 回归

`go build ./...`、`go vet ./...`、`go test ./...`、`make test-model-cover-gate` 全绿；既有 topic 导出不回退（本变更不动 `iversion_control` 与既有 generator）。

---

## 10. 风险与回滚

| 风险 | 等级 | 规避 |
|------|------|------|
| 导出 tag 与 BFE 加载器字段错位（如把 `cacheStreamValueFrom` 写成 `cacheStreamFrom`） | P0 | §4.2 契约表 + 单测对 marshal 结果做 JSON key 精确断言；两侧联调时以 BFE `ProductRuleConfLoad` 能加载为验收 |
| 漏导 `cond` 导致 BFE `CheckNilField` 加载失败 | P0 | cond 在导出 struct 中为非指针必填字段，漏填编译期即报错 |
| PUT 部分失败导致集合新旧混杂 | P1 | 单事务 delete-all + insert-all，失败整体回滚；集成测试断言"422 后集合不变" |
| 提交顺序与导出顺序不一致 | P1 | insert 按数组顺序 + `id` 升序导出，单测断言顺序 |
| version 契约破坏（`Version`/`Config` 改名） | P0 | 导出 struct tag 冻结，单测断言顶层 key |

回滚：本次全部为新增（新表、新包、新端点、新 topic），回滚 = 撤注册 + 撤装配 + drop 表，对既有功能零影响；conf-agent/BFE 侧去掉 `mod_ai_cache` 登记即可。

---

## 11. WBS

| 编号 | 任务 | 产出 | 预估 |
|------|------|------|------|
| A1 | 设计文档（本目录三步：change-summary / api-changes / design-changes；随后 api-define 与 sys-design 同步） | `design-docs/modifications/2026-09-24-ai-cache-rule-export/` | 0.5 天 |
| A2 | DDL + DAO | 两份 DDL、`table_ai_cache_rules.go`、`storage/rdb/ai_cache/`（含 `ReplaceAll` 事务） | 0.5 天 |
| A3 | model 层 | `model/shared` 结构、`model/ai_cache/`（读写 + 导出 + 审计 + mocks） | 2 天 |
| A4 | Open API | `endpoints/openapi_v1/ai_cache/`（GET + PUT，照 global_route_rules）+ `lib/validate.AICacheRules` + 注册 | 1 天 |
| A5 | Inner API + 权限 + 装配 | `endpoints/innerapi_v1/ai_cache/`、`FeatureAICache` + scope、`stateful/container` 两处 | 1 天 |
| A6 | 测试与门禁 | §9 全部用例，覆盖率达 70% 门禁 | 1.5 天 |
| A7 | 联调支持 | 与 conf-agent TOML、BFE `mod_ai_cache` 端到端联调（SC20 场景已就位） | 1 天 |

---

## 12. 待决策点（含建议）

| 问题 | 建议 | 影响 |
|------|------|------|
| Redis 实例：复用 rate_limit/token_auth 的还是独立实例/db index？ | 一期复用，`mod_ai_cache.conf` 里配独立 `database` 编号做逻辑隔离 | 只影响静态 conf 内容，不影响本设计 |
| 规则是否需要显式 `priority` 字段？ | 一期不需要，提交数组顺序（= `id` 升序）+ first-match-wins；文档已写清语义（§2.1） | 表结构 |
| API 形态：集合级 GET/PUT 还是单条 CRUD？ | **已决策**：集合级 GET/PUT 全量替换（无 `/{id}`），照 global-route-rules 先例 | Open API 面 |
| 是否需要 per-rule `enabled`？ | **已决策**：不需要，PUT 列表即生效集合，禁用=移除（§2.1） | 表结构 + generator |
| 导出 JSON 字段名/tag | **已冻结**（§4.2，与 BFE 加载器逐字段核对） | 两侧开发的前提 |
| dashboard 是否同期？ | 二期，一期 Open API 先行 | 范围 |
| 二期是否开放响应模板/GJSON 路径字段？ | 建议二期按 §4.2 预留 tag 开放；`cacheToolCallsFrom` 为 tool-calls 预留，不随二期开放 | 表结构 + API |
