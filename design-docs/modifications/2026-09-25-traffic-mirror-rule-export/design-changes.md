# traffic_mirror_rules 集合资源与 mirror_rule.data 导出 —— 设计变更说明

## 1. 概述

### 1.1 变更背景

流量镜像（shadow traffic）能力落地：生产请求正常转发的同时，BFE `mod_traffic_mirror` 模块按规则把请求副本异步发往镜像目标集群（发布前验证/新模型双跑/兜底演练），响应读空丢弃、结果仅入统计。控制面需要提供镜像规则的全量读写与配置导出，数据面按"规则文件由 ai-gateway-api 生成、conf-agent 热加载下发"的既有模式消费 `mirror_rule.data`。

### 1.2 变更目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-09-25 |
| 涉及仓库 | `ai-gateway-api` |
| 变更类型 | 新增集合资源 + 新增导出 topic |
| 参照样板 | `2026-09-24-ai-cache-rule-export`（同为 AI 模块规则集合 + 导出；本次几乎全照其结构） |

### 1.3 设计原则

| 原则 | 说明 |
|------|------|
| 框架零改动 | 导出框架（`topic + generator + MD5 签名 + config_versions + 轮询`）是通用机制，本次纯登记式扩展 |
| 集合资源 | 规则不分条寻址（无 `/{id}` API），Open API 仅集合级 GET/PUT 全量读写，照 `ai-cache-rules` / `global-route-rules` 先例 |
| 契约先行 | 导出 JSON 字段 tag 已与 BFE `mod_traffic_mirror` 规则加载器（`mirror_rule_load.go`）逐字段核对并冻结（§4），两侧开发以本节为准 |
| 合规默认 | 镜像副本复制完整对话历史与系统提示词，Header 黑名单默认剔除鉴权/会话头是合规底线（§4.2 易错点 2） |
| 敏感面最小化 | 模块调参（分层超时/并发/熔断）不进导出链路，走静态 conf + CopyFiles（§7） |

---

## 2. 资源模型

### 2.1 集合资源 `traffic_mirror_rules`

- Open API 仅暴露集合级读写：`GET /open-api/v1/traffic-mirror-rules`（全量查询）+ `PUT /open-api/v1/traffic-mirror-rules`（整体替换）；
- 无 `/{id}` 端点：`id` 为表内自增排序字段，**不暴露到 API**（照 `global-route-rules` "Hide internal id from response" 先例）；
- 无 `enabled` 字段：PUT 提交的列表即生效集合，"禁用一条规则" = 从列表移除；单条临时停用 = `percentage=0`（命中但不采样）；
- `cond` **必填**，全部匹配显式写 `default_t()`（与 ai_cache_rules 一致）：空串无法区分"故意全匹配"与"漏传"，而空 cond 静默生效 = 100% 镜像 = 双倍推理成本；BFE 加载器虽接受空 cond，控制面不生成；
- 语义：**一组有序规则，first-match-wins**。顺序 = PUT 数组顺序 = `id` 升序 = GET 返回顺序 = 导出数组顺序；不设显式 `priority` 字段；
- PUT 为单事务整体重建（delete-all + insert-all）：不在提交列表中的规则即删除；`{"rules": []}` 清空全部规则（**镜像总开关 = 清空本集合**）；
- 约束：同集合内 `name` 唯一；**同集合内 `cond` 唯一（含两条均为空串）**——BFE `MirrorRuleConfListFile.Check` 对同 product 内重复 cond 拒绝加载，控制面 fail-fast 拦截。

### 2.2 与 ai_cache_rules 的关键差异

| 维度 | ai_cache_rules | traffic_mirror_rules |
|------|----------------|----------------------|
| `cond` | 必填 | 必填（全匹配显式 `default_t()`，与缓存一致） |
| 引用校验 | 无引用字段 | **`mirror_cluster` 存在性校验**（endpoint 层，422） |
| cond 集合内去重 | 不做（BFE 兜底） | **必须做**（cond 必填后，重复即两条相同表达式） |
| 导出字段 | 最小字段集（5 个） | **全字段**（7 个，无二期预留字段） |
| 默认值风险 | 低（缓存不花推理钱） | **percentage 缺省 100 = 全量镜像 = 双倍推理成本**；Web 表单引导保守值 |
| 默认黑名单 | 不涉及 | `remove_headers` 缺省填 `["Authorization","Cookie","X-Api-Key"]` |
| topic | `mod_ai_cache` | `mod_traffic_mirror` |

---

## 3. 数据模型

### 3.1 表结构 `traffic_mirror_rules`

参照 `ai_cache_rules`（`db_ddl.sql`，行号随主干漂移）的模式，MySQL DDL：

```sql
-- create traffic_mirror_rules (流量镜像规则表)
DROP TABLE IF EXISTS `traffic_mirror_rules`;
CREATE TABLE `traffic_mirror_rules` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（排序/优先级用，不暴露API）',
  `name` VARCHAR(128) NOT NULL COMMENT '规则名称（集合内唯一，可读性/审计用）',
  `cond` TEXT COMMENT 'BFE条件表达式，如 req_path_prefix_in(...) && req_body_json_in("model", ...)；空串/NULL=全匹配',
  `mirror_cluster` VARCHAR(128) NOT NULL COMMENT '镜像目标cluster名（引用cluster表，PUT时校验存在性）',
  `percentage` INT NOT NULL DEFAULT 100 COMMENT '镜像采样百分比（0-100），0=命中但不采样',
  `remove_headers` TEXT COMMENT '镜像副本剔除Header黑名单（JSON数组）；NULL=控制面导出时填默认黑名单',
  `set_headers` TEXT COMMENT '镜像副本注入Header（JSON对象）；NULL=不注入',
  `body_rewrites` TEXT COMMENT 'body字段改写（JSON数组[{"path","value"}]）；一期path仅允许model；NULL=不改写',
  `path_rewrite` VARCHAR(1024) COMMENT '镜像路径整体替换（query保留）；NULL/空串=不改写',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  UNIQUE KEY `uk_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='流量镜像规则表';
```

要点：

- **无 `enabled` 列**（全量替换模型下冗余，见 §2.1）；
- `uk_name` 唯一键兜底集合内重名（服务端校验先行，DB 为最后防线）；
- `cond` 存储归一为空串（`NOT NULL DEFAULT ''` 亦可），导出恒输出 `"cond": "<值或空>"`；
- **`remove_headers` 列必须保留 NULL 语义**：NULL = 用户未提交（导出时控制面填默认黑名单），`'[]'` = 用户显式清空（导出不剔除）。两者不可归一；
- `set_headers` / `body_rewrites` / `path_rewrite` 的 NULL 仅表示"未配置"，无显式空值语义，导出零值（`{}` / `[]` / `""`）；
- SQLite 版本（`db_ddl_sqlite.sql`）照 `ai_cache_rules` 的 SQLite 惯例改写：类型映射（`BIGINT`→`INTEGER`、`TEXT`/`VARCHAR`→`TEXT`、`DATETIME`→`TEXT`）+ `updated_at` 由触发器维护。新增表对存量库为零迁移（新装即全量）。

### 3.2 DAO 与 storager

| 层 | 文件 | 说明 |
|----|------|------|
| DAO | `storage/rdb/internal/dao/table_traffic_mirror_rules.go` | 表级操作：全量查询（id 升序）+ `ReplaceAll`（事务内 delete-all + insert-all），照 `table_ai_cache_rules.go` |
| storager | `storage/rdb/traffic_mirror/traffic_mirror.go` | `model/traffic_mirror` 的 storager 接口实现，事务走 `itxn.TxnStorager` |
| 接口 | `model/traffic_mirror/traffic_mirror.go` | storager 接口定义：`FetchAll(ctx)` + `ReplaceAll(ctx, rules)` |

---

## 4. 导出契约（冻结）

> 本节是控制面/数据面的**接口契约**，已与 BFE `mod_traffic_mirror` 规则加载器（`bfe_modules/mod_traffic_mirror/mirror_rule_load.go` 的 `MirrorRuleConfFile`）逐字段核对。任何一侧改字段必须先改本节并知会另一侧。

### 4.1 文件结构

```json
{
  "Version": "20260925103000",
  "Config": {
    "<product>": [ /* MirrorRuleConfFile 数组，first-match-wins */ ]
  }
}
```

- `Version` / `Config` 首字母大写：conf-agent 提取版本与配置的**硬契约**；
- `<product>` 从运行时配置取：`stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（默认 `AI_product`），**不要硬编码**——与 mod_ai_cache 导出一致；
- `Config` 的 value 为数组；数组顺序 = `id` 升序 = PUT 提交顺序。

### 4.2 字段 tag 全表（`MirrorRuleConfFile`）

| 导出 JSON tag | Go 字段 | 一期是否导出 | BFE 缺省填充（`setDefaults`） | BFE 校验（`Check`） |
|---------------|---------|--------------|------------------------------|---------------------|
| `cond` | `Cond *string` | **是**（恒输出；空串 = 全匹配） | `nil → ""` | 非空必须 `condition.Build` 编译通过 |
| `mirrorCluster` | `MirrorCluster *string` | **是（必导、显式非空）** | 无默认——**缺失即加载失败**（`CheckNilField`） | 非空 |
| `percentage` | `Percentage *int` | 是 | `nil → 100` | ∈ [0, 100] |
| `removeHeaders` | `RemoveHeaders []string` | 是（**控制面缺省填默认黑名单**；显式 `[]` 导出不剔除） | `nil → []`（**注意：BFE 默认不剔除，默认黑名单是控制面行为**） | - |
| `setHeaders` | `SetHeaders map[string]string` | 是（空输出 `{}`） | `nil → {}` | - |
| `bodyRewrites` | `BodyRewrites []*MirrorBodyRewriteConfFile` | 是（空输出 `[]`） | `nil → []` | 元素 `path` 必须为 `"model"`（一期硬校验）；`path`/`value` 为必填指针 |
| `pathRewrite` | `PathRewrite *string` | 是（空输出 `""`） | `nil → ""` | - |

**易错点**：

1. **同 product 内 `cond` 唯一（BFE 对空 cond 重复也拒绝）**：BFE `MirrorRuleConfListFile.Check` 对重复 cond 报错；控制面 `cond` 必填后，重复只可能来自两条相同表达式（如都写 `default_t()`），PUT 校验必须拦截（与 ai_cache 的"不去重"决策不同）；
2. **`removeHeaders` 两侧默认值语义不同**：BFE `setDefaults` 默认空列表（不剔除任何头）；默认黑名单 `["Authorization","Cookie","X-Api-Key"]` 是**控制面在导出时**对"用户未提交 `remove_headers`"的填充行为。两侧联调时若以"直接手写 BFE conf"验证默认行为会得出相反结论；
3. **`X-Bfe-Mirror` 由 BFE 缺省注入**（`buildMirrorHeader`：用户未配置时置 `true`），控制面 `setHeaders` 不需要、也不应默认携带；
4. `bodyRewrites` 一期仅 `path="model"`，BFE `Check` 硬校验；控制面 Open API 层同步校验，错误在写入前暴露；
5. BFE 对指针字段（`cond`/`mirrorCluster`/`percentage`/`pathRewrite`）做 `CheckNilField`：经 `setDefaults` 后 `cond`/`percentage`/`pathRewrite` 恒非 nil，`mirrorCluster` 无默认值——**控制面漏导 `mirrorCluster` 属 P0 级契约事故**（导出 struct 中该字段为非指针必填，漏填编译期即报错）；
6. 导出 struct 一律不用 `omitempty`：规则全字段恒输出，导出物显式、可 diff。

### 4.3 控制面校验与 BFE 校验的对应关系

| 校验 | 控制面（Open API 层） | 数据面（BFE 加载层） |
|------|----------------------|----------------------|
| cond 编译 | `lib/validate.ConditionExpression`（调 BFE `condition.Build`） | `condition.Build` |
| cond 集合内唯一 | **是** | 是（含空 cond 重复，控制面已不生成） |
| mirror_cluster 非空 | 是 | 是 |
| mirror_cluster 存在性 | **是**（endpoint 层，`FetchClusterList` 按名过滤；照 `lib/validate` "checked separately by the endpoint" 先例） | 否（异步侧仅计 `fail_total{reason="resolve"}`） |
| percentage ∈ [0,100] | 是 | 是 |
| remove_headers 元素非空 | 是 | 否 |
| body_rewrites path="model"、value 非空 | 是 | 是 |
| path_rewrite 以 `/` 开头 | 是 | 否 |
| 集合内 name 唯一 | 是（`uk_name` 兜底） | - |

控制面校验的目标是把错误拦在写入前（fail-fast），BFE 校验是最后防线。其中 cond 唯一性与 mirror_cluster 存在性两条，BFE 或兜底不了（加载失败）或兜底代价高（运行时 resolve 失败），必须由控制面拦。

### 4.4 导出全字段策略

与 ai_cache 的"最小字段集"不同，本次导出**规则全字段**：`mod_traffic_mirror` 规则字段共 7 个，全部是规则语义字段，BFE 加载器无"二期预留字段"，不存在 ai_cache 那套"控制面管语义、BFE 填默认"的分层。唯一保留在 BFE 静态 conf 的是模块级调参（§7）。

### 4.5 Generator 逻辑（`TrafficMirrorRuleGenerator`）

1. 查全部规则，按 `id` 升序（**无 enabled 过滤**——提交列表即生效集合）；
2. 逐条组装导出结构：`cond`（空串原样）、`mirrorCluster`、`percentage`、`removeHeaders`（**列为 NULL 时填默认黑名单常量** `DefaultSensitiveHeaders = ["Authorization","Cookie","X-Api-Key"]`；显式值原样）、`setHeaders`（NULL → `{}`）、`bodyRewrites`（NULL → `[]`）、`pathRewrite`（NULL → `""`）；
3. 组装 `Config[AIRouteInnerProductName] = 规则数组`；
4. 空表时导出空数组（product 键仍 present，值为 `[]`）；
5. 套 `iversion_control.ExportConfig(ctx, ConfigTopicProductTrafficMirror, generator)` 标准流程：生成数据 → MD5 签名 → 比对 `config_versions`（`name="mod_traffic_mirror"`）→ 签名相同返回旧版本（增量，HTTP Data 为 null）→ 不同则插新版本（版本号 = 时间戳 `20060102150405`）。

### 4.6 Inner API 端点

照 `endpoints/innerapi_v1/ai_cache/export.go`（`ExportRoute` 模式）：

```go
var ExportRoute = &xreq.Endpoint{
    Path:       "/configs/traffic-mirror-rule",
    Method:     http.MethodGet,
    Handler:    xreq.Convert(ExportAction),
    Authorizer: iauth.FA(iauth.FeatureTrafficMirror, iauth.ActionExport),
}
```

- 注册：`endpoints/innerapi_v1/endpoints.go` 的 `endpoints()` 追加一行；
- handler 内：`export_util.NewExportFromReq(req)` 解析 `version` → `container.TrafficMirrorManager.ConfigExport(ctx, param.Version)`；
- topic 常量 `ConfigTopicProductTrafficMirror = "mod_traffic_mirror"` 定义在 `model/traffic_mirror/traffic_mirror_manager.go` 内（与 ai_cache 的 topic 定义在自身包内一致）。

---

## 5. model/traffic_mirror 设计

### 5.1 文件构成（照 `model/ai_cache/` 的组织）

| 文件 | 内容 |
|------|------|
| `traffic_mirror.go` | `TrafficMirrorRuleParam`/`TrafficMirrorRulesParam`（引用 `model/shared`）、数据结构 ↔ param 转换、storager 接口、默认黑名单常量 `DefaultSensitiveHeaders` |
| `traffic_mirror_manager.go` | `TrafficMirrorManager`：`GetTrafficMirrorRules` / `SetTrafficMirrorRules`（事务重建）/ `ConfigExport` + `TrafficMirrorRuleGenerator` + topic 常量；依赖经构造函数注入（storager、versionControlManager、operationLog、运行时 product 名），**不硬依赖 `stateful.DefaultConfig`**（测试可 mock，见 AGENTS.md 约定） |
| `operation_log.go` | PUT 审计（一条 update，`before`/`after` 为整个集合快照；API 小写词汇，遵守 #201 nil-guard / #205 词汇纪律） |
| `mocks_test.go` | 手写 callback mock（`fakeTrafficMirrorRuleStorager` 等） |

### 5.2 manager 方法草图

```go
func (m *TrafficMirrorManager) GetTrafficMirrorRules(ctx context.Context) ([]*shared.TrafficMirrorRuleParam, error)          // id 升序
func (m *TrafficMirrorManager) SetTrafficMirrorRules(ctx context.Context, param *shared.TrafficMirrorRulesParam) (*shared.TrafficMirrorRulesParam, error) // 事务：delete-all + insert-all（按数组顺序）
func (m *TrafficMirrorManager) ConfigExport(ctx context.Context, lastVersion string) (*ExportTrafficMirrorRuleConfig, error) // 照 ai_cache_manager 先例：返回具体配置类型（签名相同/增量时返回 nil），ExportData 包装由 generator 完成
```

`SetTrafficMirrorRules` 要点：

- 单事务内完成整体替换，`itxn.TxnStorager` 包裹（禁止 ad-hoc 事务）；
- 数组顺序写入 → 新 `id` 自增序即优先级序，与 GET/导出顺序一致；
- 事务失败整体回滚，集合保持原状；
- 审计在事务成功后记录（失败也记录一条失败审计，照 ai_cache 的失败审计先例）。

---

## 6. 参数校验（`lib/validate`）

新增 `TrafficMirrorRules(param *shared.TrafficMirrorRulesParam)`（集合级全量校验，照 `AICacheRules(param)` 先例；任一失败则整个 PUT 422，集合不变）：

| 校验项 | 规则 | 失败 |
|--------|------|------|
| rules | null 按 `[]` 处理；单条为 null 拒绝 | `WrapParamErrorWithMsg` |
| 每条 name | 非空、1-128 字符 | `WrapParamErrorWithMsg` |
| 每条 cond | **必填**；`ConditionExpression`（validate.go 内部调 BFE `condition.Build`） | `WrapParamErrorWithMsg` |
| 每条 mirror_cluster | 非空、1-128 字符（**存在性在 endpoint 层**，不在此处） | `WrapParamErrorWithMsg` |
| 每条 percentage | ∈ [0, 100] | `WrapParamErrorWithMsg` |
| 每条 remove_headers | 元素非空 | `WrapParamErrorWithMsg` |
| 每条 set_headers | key/value 非空 | `WrapParamErrorWithMsg` |
| 每条 body_rewrites | 元素 `path` 必填且 = `"model"`；`value` 必填非空 | `WrapParamErrorWithMsg` |
| 每条 path_rewrite | 空或 `/` 开头 | `WrapParamErrorWithMsg` |
| 集合内 name 唯一 | 遍历查重 | `WrapParamErrorWithMsg`（422，请求集合内部矛盾，不用 409） |
| **集合内 cond 唯一** | 遍历查重 | `WrapParamErrorWithMsg` |

---

## 7. 模块基础配置不下发

`mod_traffic_mirror.conf`（分层超时 `ConnectTimeoutMs`/`TTFBTimeoutMs`/`TotalTimeoutMs`、`MaxMirrorBodyBytes`、`MaxResponseBodyBytes`、`MaxConcurrent`、`QueueCapacity`、`CircuitBreakerFailThreshold`、`CircuitBreakerCooldownSec`）**不进导出链路**，放 BFE 静态 conf 文件，由 conf-agent `CopyFiles` 随规则文件一起落盘。conf-agent 与控制面均零额外代码改动，仅需 TOML 登记（见 change-summary.md §5）：

```toml
[Reloaders.mod_traffic_mirror]
ConfAPI = "/inner-api/v1/configs/traffic-mirror-rule"
ReloadFile = "mirror_rule.data"
BFEReloadAPI = "/reload/mod_traffic_mirror"
CopyFiles = ["mirror_rule.data", "mod_traffic_mirror.conf"]
```

---

## 8. 容器装配

| 文件 | 改动 |
|------|------|
| `stateful/container/rdb/components.go` | 声明 traffic_mirror storager（DAO 注入） |
| `stateful/container/components.go` | 声明 `TrafficMirrorManager`，注入 storager + `iversion_control` manager + operationLog + `stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（**仅装配点允许触碰 DefaultConfig**，manager 内不直连） |
| `endpoints/openapi_v1/endpoints.go` | 注册 `traffic_mirror.Endpoints` |
| `endpoints/innerapi_v1/endpoints.go` | 注册 `traffic_mirror.ExportRoute` |

`mirror_cluster` 存在性校验的依赖：endpoint 层直接持有 `icluster_conf.ClusterManager`（容器已装配），用 `ClusterFilter{Names: [...]}` 一次批量查回做集合差集；不把 cluster 校验下沉进 `model/traffic_mirror`（保持 manager 单一职责，也避免校验逻辑与导出逻辑抢同一个 manager 的依赖面）。

---

## 9. 测试计划

### 9.1 单元测试（硬门禁）

| 位置 | 覆盖点 |
|------|--------|
| `model/traffic_mirror/mocks_test.go` | `fakeTrafficMirrorRuleStorager`（callback 字段模式，照 `mocks_test.go` 惯例） |
| `model/traffic_mirror/traffic_mirror_manager_test.go` | `SetTrafficMirrorRules` 事务重建（按序写入、旧数据清除、失败回滚）；`GetTrafficMirrorRules` 顺序；`TrafficMirrorRuleGenerator`：**id 升序**、product 名取自注入配置（含非默认名）、空表导出空数组、字段 tag 与 §4.2 逐字一致（marshal 后断言 JSON key 集合）、**remove_headers 缺省填默认黑名单 / 显式 `[]` 不填**、setHeaders/bodyRewrites/pathRewrite 空值输出 `{}`/`[]`/`""`；`ConfigExport` 版本比对/增量返回；PUT 审计（成功 + 失败两路） |
| `endpoints/openapi_v1/traffic_mirror/endpoints_test.go` | 绑参、422（逐条字段非法 / 集合内重名 / **cond 重复** / **mirror_cluster 不存在**）、响应不含内部 `id`、空集合往返 |
| `lib/validate` | cond 非法、cond 缺失、cond 集合内重复、percentage 越界、body_rewrites path 非 model、path_rewrite 非 `/` 开头、set_headers 空 key |

`make test-model-cover-gate`：model 层语句覆盖率 ≥ 70% 是硬门禁。新源文件带 Apache 2.0 / Rainway 许可头（`make license-fix`）。

### 9.2 集成测试（`test/integration/tests/`）

| 用例 | 断言 |
|------|------|
| PUT → GET 往返 | 提交 3 条（含缺省 cond、缺省 remove_headers、含 body_rewrites）→ GET 回读精确等于提交值（顺序一致） |
| 全量替换语义 | 二次 PUT 增 1 删 1 改 1 → GET 精确等于第二次提交（被删规则消失） |
| 清空 | PUT `{"rules":[]}` → GET `{"rules":[]}` |
| 校验 | cond 语法错误 / 集合内 name 重名 / cond 重复 / mirror_cluster 不存在 → 422 且 GET 集合不变 |
| 导出默认值 | 缺省 remove_headers 的规则导出含 `"removeHeaders": ["Authorization","Cookie","X-Api-Key"]`；显式 `[]` 导出 `"removeHeaders": []` |
| Inner 导出 | PUT 集合 → `GET /inner-api/v1/configs/traffic-mirror-rule` 首拉含规则且 tag 为 `mirrorCluster` 风格、顺序与提交一致；二次拉取版本不变（增量）；清空 → 导出空数组 |

### 9.3 回归

`go build ./...`、`go vet ./...`、`go test ./...`、`make test-model-cover-gate` 全绿；既有 topic 导出不回退（本变更不动 `iversion_control` 与既有 generator）。

---

## 10. 风险与回滚

| 风险 | 等级 | 规避 |
|------|------|------|
| 导出 tag 与 BFE 加载器字段错位（如把 `mirrorCluster` 写成 `mirror_cluster`） | P0 | §4.2 契约表 + 单测对 marshal 结果做 JSON key 精确断言；两侧联调时以 BFE `MirrorRuleConfLoad` 能加载为验收 |
| 两条相同 cond 规则（如都写 `default_t()`）导致 BFE 加载失败（`can't have same cond`） | P0 | 控制面 PUT 校验 cond 集合内唯一，单测覆盖重复场景 |
| 漏导 `mirrorCluster` 导致 BFE `CheckNilField` 加载失败 | P0 | 导出 struct 中为非指针必填字段，漏填编译期即报错 |
| `removeHeaders` 两侧默认语义错位（控制面默认黑名单 vs BFE 默认空） | P1 | §4.2 易错点 2 已标明；单测断言"缺省填黑名单、显式 `[]` 不填"两条路径 |
| 未知 `mirror_cluster` 规则入库 → BFE 运行时 `resolve` 失败计数 | P1 | endpoint 层存在性校验（422 拦截） |
| percentage 缺省 100 被误用为保守默认值 → 镜像成本翻倍 | P1 | Open API 文档与 Web 表单引导保守值；PUT 审计完整记录 percentage；上线前对运营做默认值说明 |
| PUT 部分失败导致集合新旧混杂 | P1 | 单事务 delete-all + insert-all，失败整体回滚；集成测试断言"422 后集合不变" |
| 提交顺序与导出顺序不一致 | P1 | insert 按数组顺序 + `id` 升序导出，单测断言顺序 |

回滚：本次全部为新增（新表、新包、新端点、新 topic），回滚 = 撤注册 + 撤装配 + drop 表，对既有功能零影响；conf-agent/BFE 侧去掉 `mod_traffic_mirror` 登记即可。

---

## 11. WBS

| 编号 | 任务 | 产出 | 预估 |
|------|------|------|------|
| A1 | 设计文档（本目录三步：change-summary / api-changes / design-changes；随后 api-define 与 sys-design 同步） | `design-docs/modifications/2026-09-25-traffic-mirror-rule-export/` | 0.5 天 |
| A2 | DDL + DAO | 两份 DDL、`table_traffic_mirror_rules.go`、`storage/rdb/traffic_mirror/`（含 `ReplaceAll` 事务） | 0.5 天 |
| A3 | model 层 | `model/shared` 结构、`model/traffic_mirror/`（读写 + 导出 + 审计 + mocks） | 2 天 |
| A4 | Open API | `endpoints/openapi_v1/traffic_mirror/`（GET + PUT，照 ai_cache）+ `lib/validate.TrafficMirrorRules` + `mirror_cluster` 存在性校验 + 注册 | 1.5 天 |
| A5 | Inner API + 权限 + 装配 | `endpoints/innerapi_v1/traffic_mirror/`、`FeatureTrafficMirror` + scope、`stateful/container` 两处 | 1 天 |
| A6 | 测试与门禁 | §9 全部用例，覆盖率达 70% 门禁 | 1.5 天 |
| A7 | 联调支持 | conf-agent TOML 登记 + BFE `mod_traffic_mirror` 端到端联调（导出文件经 `MirrorRuleConfLoad` 加载验证） | 1 天 |

---

## 12. 待决策点（含建议）

| 问题 | 建议 | 影响 |
|------|------|------|
| 镜像目标集群的鉴权方式（需求开放问题 1：免鉴权白名单 / 规则配静态 Key / 目标侧 shadow key） | **一期不配置面凭证字段**，目标集群走免鉴权白名单 + 网络隔离；若二期选"规则配静态 Key 替换 Authorization"，Open API 加 `auth_override` 类字段（与 `remove_headers` 黑名单配合：覆盖而非追加），表加列 + 契约表加行 | 表结构 + API（二期） |
| `percentage` Open API 缺省 100 与"默认保守"的产品诉求冲突 | Open API 默认值对齐 BFE（100，保证往返一致）；**Web 表单默认值引导保守（如 10）**，产品诉求在交互层解决，不改契约 | 仅文档/Web |
| 是否需要模块级总开关（不清空规则即停镜像） | 一期**不需要**（清空集合 = 关镜像，语义已完备）；若运营反馈"保留规则但长期停用"诉求强，二期在 BFE 静态 conf 加 `Enable` 开关（属 §7 不下发面，零契约变更） | BFE conf（二期） |
| `remove_headers` 默认黑名单是否需要部署级可配 | 一期硬编码常量（与需求 FR-5 一致）；不同合规要求的部署通过显式提交 `remove_headers` 覆盖 | model 常量（二期可升配置） |
| 二期通用 GJSON PATH 改写（temperature/messages 等） | 保守评估；BFE `Check` 已单点放开（`BodyRewritePathModel` 常量），控制面同步放开校验即可，契约表 `bodyRewrites` 行已兼容 | 两侧校验（二期） |
| 二期"改写前镜像"挂点 / 响应 diff / Doris 镜像完成事件形态 | 见需求开放问题 4/6/7；均不影响本期表结构与导出契约 | 二期单独立项 |
