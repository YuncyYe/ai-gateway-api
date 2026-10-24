# Traffic Mirror Rules 测试用例设计文档

## 1. 模块概述

Traffic Mirror Rules（流量镜像规则）模块负责镜像规则集合的管理，配合 BFE `mod_traffic_mirror` 模块使用（shadow traffic：生产请求正常转发的同时把副本异步发往镜像目标集群，响应读空丢弃、仅入统计）。该模块为**集合级全量读写**：不提供 `/{id}` 单条规则操作接口。规则按数组顺序匹配（first-match-wins），数组顺序即优先级（= 导出到 BFE 的顺序）。规则 `id` 为内部排序字段，不出现在 API 请求与响应中。

与 AI 缓存规则的关键差异（合同锁定点，本设计重点覆盖）：

1. `cond` **必填**（全匹配显式 `default_t()`），集合内 cond 唯一（重复 → 422）；
2. `mirror_cluster` **必填且须已存在**（endpoint 层存在性校验，不存在 → 422）——跨对象引用（家族5）；
3. `remove_headers` 的**两层默认语义**：缺省（未提交）→ 导出时控制面填默认黑名单 `["Authorization","Cookie","X-Api-Key"]`；显式 `[]` → 导出不剔除。Open API 回读时缺省字段不输出、显式 `[]` 输出空数组（家族1 省略 vs 显式空对照的最高危变体）；
4. `body_rewrites` 元素 `path` 一期硬校验仅允许 `"model"`；
5. Inner 导出为**规则全字段**（7 个 tag 恒输出，含空值零值 `{}`/`[]`/`""`），与 ai_cache 的最小字段集不同。

## 2. 接口列表

| 编号 | 接口名称 | 方法 | 路径 | 说明 |
|------|----------|------|------|------|
| TM-1 | 全量更新流量镜像规则 | PUT | `/open-api/v1/traffic-mirror-rules` | 全量替换（单事务 delete-all + insert-all）；`rules` 为 null/缺省按 `[]` 清空 |
| TM-2 | 查询流量镜像规则 | GET | `/open-api/v1/traffic-mirror-rules` | 全量查询（无分页），空集合返回 `{"rules": []}` |
| TMIE-1 | 导出流量镜像规则配置 | GET | `/inner-api/v1/configs/traffic-mirror-rule` | 供 BFE `mod_traffic_mirror` 消费；query `version` 可选（增量同步，Data 形状 `{"Config": {"AI_product": [...]}, "Version": "..."}`） |

## 3. 测试用例统计

| 接口 | 测试用例数 |
|------|-----------|
| 全量更新流量镜像规则（PUT） | 15 |
| 查询流量镜像规则（GET） | 4 |
| 导出流量镜像规则配置（InnerAPI） | 6 |
| **合计** | **25** |

> TM-1-015 为 `//go:build mysql` 并发用例（家族10），需 `AIAPI_MYSQL_DSN`，SQLite 环境不编译。

## 4. 认证方式

测试环境配置 `SkipTokenValidate=true`，所有请求无需携带认证头。OpenAPI 权限 `FeatureTrafficMirror + ActionUpdate/ActionRead`，InnerAPI 权限 `FeatureTrafficMirror + ActionExport`，测试环境均跳过。

## 5. 目录结构

```
traffic_mirror/
├── design.md
├── update/
│   ├── update_test.go            # TM-1-001 ~ TM-1-014（PUT）
│   └── concurrency_mysql_test.go # TM-1-015（//go:build mysql，并发全量替换）
└── get/
    └── get_test.go               # TM-2-001 ~ TM-2-004（GET）

innerapi/traffic_mirror/
└── traffic_mirror_test.go        # TMIE-1-001 ~ TMIE-1-006（导出）
```

## 6. 全量更新流量镜像规则（PUT）

### 6.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | Traffic Mirror Rules |
| 接口名称 | 全量更新流量镜像规则 |
| 方法 | PUT |
| 路径 | `/open-api/v1/traffic-mirror-rules` |
| 说明 | 单事务内整体替换规则集合；任一校验失败（含 mirror_cluster 存在性）或事务失败则整体回滚，集合保持原状 |

### 6.2 接口参数说明

#### 6.2.1 请求参数（Body）

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| rules | array | Y | 规则列表，数组顺序即优先级 | 必填；`null`/缺省按 `[]` 处理（清空） |
| rules[].name | string | Y | 规则名称 | 必填；1-128 字符；同一集合内不得重复 |
| rules[].cond | string | Y | BFE 条件表达式 | **必填**；必须 `condition.Build` 编译通过；同一集合内不得重复 |
| rules[].mirror_cluster | string | Y | 镜像目标 cluster 名 | 必填；1-128 字符；**服务端校验存在性**（不存在 → 422） |
| rules[].percentage | int | N | 镜像采样百分比（0=命中不采样） | 0-100，默认 100 |
| rules[].remove_headers | array | N | 镜像副本剔除 Header 黑名单 | 元素非空；缺省 = 导出填默认黑名单；显式 `[]` = 不剔除 |
| rules[].set_headers | object | N | 镜像副本注入 Header | key/value 非空 |
| rules[].body_rewrites | array | N | body 字段改写 | 元素 `path` 必填且 = `"model"`；`value` 必填非空 |
| rules[].path_rewrite | string | N | 镜像路径整体替换 | 空或 `/` 开头 |

#### 6.2.2 返回数据字段

固定键（恒输出）：`name`、`cond`、`mirror_cluster`、`percentage`、`created_at`、`updated_at`。
可选键（提交非 null 才输出）：`remove_headers`、`set_headers`、`body_rewrites`、`path_rewrite`。

约束：响应**不得含内部 `id` 字段**；无 `enabled` 字段；`percentage` 缺省回填 100；可选字段缺省不输出（NULL 语义），显式 `[]` 输出空数组。

### 6.3 测试场景总览

| 编号 | 场景 | 测试类型 | 缺陷家族 | 简要说明 |
|------|------|---------|---------|---------|
| TM-1-001 | 最小参数 | 正常参数 | 家族1 | 仅 name+cond+mirror_cluster（真实 cluster）→ 200 且 percentage=100 回填；可选字段缺省不输出；GET 回读一致 |
| TM-1-002 | 完整参数 3 条 | 正常参数 | - | 显式非默认值（含 set_headers/body_rewrites/path_rewrite/显式 remove_headers/percentage 10/1/0）→ 200，响应顺序=提交顺序 |
| TM-1-003 | 全量替换 | 正常参数 | 家族1/3 | 删1/改1/增1 → GET 逐字段精确等于新提交集合；被删规则消失 |
| TM-1-004 | 清空 | 边界值 | - | `{"rules":[]}` 与 `{}`（rules 缺省）→ 200 且 GET `rules==[]`（非 null） |
| TM-1-005 | 省略 vs 显式空对照 | 正常参数 | 家族1（#147/#151/#152 最高危变体） | 同一规则 remove_headers 三态轮转：显式 `["X-A"]` → 省略（GET 不输出该键、导出默认黑名单）→ 显式 `[]`（GET 输出 `[]`、导出不剔除）；Inner 导出双重断言 |
| TM-1-006 | 非法 cond | 异常参数 | 家族3 | 语法错、`req_path_prefix_in` 单参数 → 422 + 错误可归因 cond + GET 回读零变更 |
| TM-1-007 | cond 缺省/空串 | 异常参数 | 家族12 合同锁定 | cond 缺省与 `""` → 422（合同必填，防静默全匹配=双倍成本）+ 回读零变更 |
| TM-1-008 | cond 集合内重复 | 异常参数 | 家族3/4 | 两条 `default_t()` → 422 + 可归因 + 回读零变更 |
| TM-1-009 | name 重名 | 异常参数 | 家族3 | 集合内重名 → 422 + 错误含重名 + 回读零变更 |
| TM-1-010 | mirror_cluster 不存在 | 异常参数 | 家族5（跨对象引用） | 引用未创建的 cluster → 422 + 错误含 cluster 名 + 回读零变更 |
| TM-1-011 | 非法值矩阵 | 异常参数 | 家族3/4 | 表驱动 9 子用例：name 空/129、percentage -1/101、body_rewrites path 非 model、value 空、null 元素、path_rewrite 无 `/`、set_headers 空 value、remove_headers 空元素、rules null 元素 → 全 422 + 回读零变更 |
| TM-1-012 | 边界正值 | 边界值 | 家族4 | name=128、percentage=0 与 100、path_rewrite=`"/v1/chat/completions"`、单 body_rewrite 正常 → 200 |
| TM-1-013 | 合同锁定 | 合同一致性 | 家族11/12 | 全字段提交时响应规则元素键集合精确为合同 10 键（6 固定 + 4 可选），无 `id`/`enabled`；缺省提交时可选键缺席 |
| TM-1-014 | 操作审计 | 审计 | 家族7（#201/#155） | PUT 成功记 update 审计（before/after 集合快照、diff_keys 精确匹配、快照无 `id`）；PUT 422 记失败审计且身份固定不依赖请求体 |
| TM-1-015 | 并发全量替换（MySQL） | 并发 | 家族10 | 20 goroutine 并发 PUT 互不相同小集合 → 无 5xx，最终集合与某次提交完全相等 |

> 注：混合集合假事务（2 合法 + 1 非法 → 422 且全部未生效 + Inner 防泄漏）由 TM-1-011 的 `mixed_valid_invalid` 子用例与 TMIE-1-006 共同覆盖。

### 6.4 测试场景详细设计

#### 6.4.1 TM-1-001：最小参数（正常参数，家族1）

##### 设计思路

验证仅提交 `name`+`cond`+`mirror_cluster` 时：`percentage` 按合同回填 100；`remove_headers`/`set_headers`/`body_rewrites`/`path_rewrite` 缺省不输出（键缺席而非 null）；响应携带只读时间戳。GET 回读与 PUT 响应逐字段一致。`mirror_cluster` 使用 `testutil.CreateCluster` 创建的真实 cluster（正向存在性校验）。

##### 执行步骤

1. `cluster := CreateCluster(UniqueClusterName())`。
2. PUT `{"rules":[{"name":"<runID>-tm1001","cond":"default_t()","mirror_cluster":cluster}]}`。
3. 断言 200；固定键值断言（percentage=100）；`keysOf(rule)` 精确为 6 固定键（可选键全部缺席——家族1 省略语义）；created_at/updated_at 非空。
4. GET 回读与 PUT 响应逐字段一致。

##### 预期返回结果

**ErrNum**：200；键集合精确 `{name, cond, mirror_cluster, percentage, created_at, updated_at}`；percentage=100。

---

#### 6.4.2 TM-1-002：完整参数 3 条（正常参数）

##### 设计思路

3 条规则全部显式设置非默认且互不相同的参数（percentage 10/1/0、显式 remove_headers、set_headers、body_rewrites、path_rewrite），验证写入成功、响应顺序严格等于提交顺序。

##### 执行步骤

1. 创建 2 个真实 cluster（c1/c2）。
2. PUT 3 条：r1（percentage=10、set_headers、body_rewrites）、r2（percentage=1、显式扩展 remove_headers）、r3（percentage=0、path_rewrite、cond 用 model 条件）。
3. 断言 200；按数组下标逐字段断言与提交值一致（键集合含全部 10 键）。

##### 预期返回结果

**ErrNum**：200；`rules[i]` 各字段 Equals 提交值；`rules[2].percentage=0`（命中不采样语义可配置）。

---

#### 6.4.3 TM-1-003：全量替换（正常参数，家族1/3）

##### 设计思路

验证全量替换语义：不在提交列表中的规则即删除；修改的规则按新值生效；新增的规则出现。GET 回读逐字段精确等于新提交集合。

##### 执行步骤

1. PUT 集合 A（3 条：a1/a2/a3，共用 cluster c1）。
2. PUT 集合 B（2 条：改 a1 的 percentage、新增 b1；a2/a3 删除）。
3. GET 断言：长度 2；[0]=修改后的 a1 逐字段精确；[1]=b1 逐字段精确；响应中不存在 a2/a3 的 name。

---

#### 6.4.4 TM-1-004：清空（边界值）

##### 执行步骤

1. PUT 1 条规则后 PUT `{"rules":[]}` → 200；GET 断言顶层键含 `rules` 且为 `[]`（非 null）；响应 body 文本含 `"rules":[]`。
2. PUT 1 条规则后 PUT `{}`（rules 缺省）→ 200；GET 断言同上。

---

#### 6.4.5 TM-1-005：省略 vs 显式空对照（正常参数，家族1 最高危变体）

##### 设计思路

`remove_headers` 是双层默认语义字段：DB 列 NULL（未提交）与 `'[]'`（显式空）必须走不同导出路径。全量替换模型下对同一规则名做三态轮转，每态断言 GET 回读形状与 Inner 导出行：

| 轮次 | 提交 | GET 回读 | Inner 导出 removeHeaders |
|------|------|---------|--------------------------|
| 1 | `"remove_headers": ["X-Custom-Secret"]` | 输出 `["X-Custom-Secret"]` | `["X-Custom-Secret"]` |
| 2 | 省略 remove_headers | **键缺席**（非 null 非 []） | **默认黑名单** `["Authorization","Cookie","X-Api-Key"]` |
| 3 | `"remove_headers": []` | 输出 `[]`（键 present、空数组） | `[]`（不剔除） |

##### 执行步骤

1. 创建 cluster c1；PUT 单条规则（显式黑名单）→ GET + Inner 导出断言轮次 1。
2. PUT 同 name 规则省略 remove_headers → GET 断言 `remove_headers` 键缺席（ElementsMatch 6 固定键）；Inner 导出断言 `"removeHeaders":["Authorization","Cookie","X-Api-Key"]` 文本形态。
3. PUT 同 name 规则 `"remove_headers": []` → GET 断言键 present 且为 `[]`（body 文本 `"remove_headers":[]`）；Inner 导出断言 `"removeHeaders":[]`。

##### 预期返回结果

三轮全 200；GET/导出形状与上表逐格一致。

---

#### 6.4.6 TM-1-006：非法 cond（异常参数，家族3）

##### 请求参数（子用例）

```json
{"rules":[{"name":"<runID>-tm1006a","cond":"not_a_valid_expr(","mirror_cluster":"<c1>"}]}
{"rules":[{"name":"<runID>-tm1006b","cond":"req_path_prefix_in(\"/v1/chat/completions\")","mirror_cluster":"<c1>"}]}
```

##### 预期返回结果

**ErrNum**：422；**ErrMsg**：含 "cond" 可归因；GET 回读 DeepEquals 操作前集合（含时间戳）。

---

#### 6.4.7 TM-1-007：cond 缺省/空串（异常参数，家族12 合同锁定）

##### 设计思路

合同锁定：`cond` 必填（与 `/ai-cache-rules` 一致），全匹配显式 `default_t()`。防"漏传 cond 静默变成全匹配镜像"（双倍推理成本）。两种形态（缺省键、`""`）各断言 422 + 回读零变更。

##### 预期返回结果

**ErrNum**：422；**ErrMsg**：含 "cond"；GET 回读零变更。

---

#### 6.4.8 TM-1-008：cond 集合内重复（异常参数，家族3/4）

##### 设计思路

两条规则 cond 均为 `default_t()` → 422（BFE 同 product 内拒绝重复 cond，控制面 fail-fast）；错误消息可归因到重复 cond 值。

##### 预期返回结果

**ErrNum**：422；**ErrMsg**：含 "duplicate" 与 `default_t()`；GET 回读零变更。

---

#### 6.4.9 TM-1-009：name 重名（异常参数，家族3）

##### 预期返回结果

**ErrNum**：422；**ErrMsg**：含重名 name；GET 回读零变更。

---

#### 6.4.10 TM-1-010：mirror_cluster 不存在（异常参数，家族5）

##### 设计思路

跨对象引用存在性：引用从未创建的 cluster 名 → 422；错误消息须含缺失 cluster 名（可归因）。正向路径由 TM-1-001 覆盖（真实 cluster 200）。

##### 执行步骤

1. 落合法集合（keeper，真实 cluster）。
2. PUT 规则 `mirror_cluster: "<runID>-no-such-cluster"` → 422，ErrMsg 含该 cluster 名。
3. GET 回读零变更。

---

#### 6.4.11 TM-1-011：非法值矩阵（异常参数，家族3/4，表驱动）

##### 设计思路

表驱动 9+1 子用例，覆盖 api-define 每条校验规则的负向路径，每个负向后接"GET 回读零变更"断言；最后追加混合集合假事务子用例（2 合法 + 1 非法 → 422 且全部未生效）。

| 子用例 | 请求要点 | 预期 |
|--------|---------|------|
| name 空串 | `name:""` | 422 |
| name 129 字符 | `name:"<129 chars>"` | 422 |
| percentage=-1 | `percentage:-1` | 422 |
| percentage=101 | `percentage:101` | 422 |
| body_rewrites path 非 model | `path:"temperature"` | 422（一期硬校验） |
| body_rewrites value 空 | `value:""` | 422 |
| body_rewrites null 元素 | `body_rewrites:[null]` | 422 |
| path_rewrite 无 / | `path_rewrite:"v1/internal"` | 422 |
| set_headers 空 value | `set_headers:{"X-A":""}` | 422 |
| remove_headers 空元素 | `remove_headers:["Authorization",""]` | 422 |
| rules null 元素 | `rules:[{...}, null]` | 422 |
| mixed_valid_invalid | 2 合法 + 1 cond 编译失败 | 422 + 回读零变更 |

---

#### 6.4.12 TM-1-012：边界正值（边界值，家族4）

##### 设计思路

边界±1 正向：name=128（上限）、percentage=0 与 100（闭区间两端）、path_rewrite=`"/v1/chat/completions"`、body_rewrites 单元素正常 → 200 且逐字段回读一致。

---

#### 6.4.13 TM-1-013：合同锁定（合同一致性，家族11/12）

##### 设计思路

- 全字段提交时：响应规则元素键集合 `ElementsMatch` 精确为 10 键（6 固定 + 4 可选），禁止 `Contains`（#201 幻影键不敏感）；
- 最小提交时：键集合精确为 6 固定键（可选键缺席，锁家族1 省略语义）；
- 全程不得出现 `id`/`enabled` 键。

---

#### 6.4.14 TM-1-014：操作审计（审计，家族7 / #201 / #155）

##### 设计思路

照 ai_cache 同族用例：成功路径等 `resource_type=traffic_mirror_rule, action=update, status=1` 审计，`change_summary` 键集合精确 `{before, after, diff_keys}`、`diff_keys` ElementsMatch 精确（集合变化时含 `rules`）、快照键名小写 API 词汇且无 `"id"`；失败路径（422）等 `status=2` 审计，身份固定 `traffic_mirror_rules` 不依赖请求体。

---

#### 6.4.15 TM-1-015：并发全量替换（MySQL，家族10）

##### 设计思路

`//go:build mysql` 隔离。20 个 goroutine 并发 PUT 互不相同的小集合（各 1 条 name/cond 唯一的规则，共用预建 cluster）→ 全部 2xx、无 5xx/死锁；结束后 GET 集合与某一次提交完全相等（无混合状态）。

---

## 7. 查询流量镜像规则（GET）

### 7.1 测试场景总览

| 编号 | 场景 | 测试类型 | 缺陷家族 | 简要说明 |
|------|------|---------|---------|---------|
| TM-2-001 | 空集合形状 | 边界值 | 家族11 | 顶层键含 `rules` 且为 `[]`（非 null） |
| TM-2-002 | 顺序 | 返回数据 | - | PUT 3 条后 GET 顺序=提交顺序 |
| TM-2-003 | 幂等 | 返回数据 | - | 连续两次 GET 响应逐字段一致 |
| TM-2-004 | 可选键缺席/在场形状 | 合同一致性 | 家族11 | 缺省提交规则不含 4 可选键；显式提交规则含全部 10 键（同响应内混合） |

---

## 8. 导出流量镜像规则配置（InnerAPI）

### 8.1 接口信息

| 项目 | 值 |
|-----|-----|
| 模块 | InnerAPI |
| 接口名称 | 导出流量镜像规则配置 |
| 方法 | GET |
| 路径 | `/inner-api/v1/configs/traffic-mirror-rule` |
| 说明 | 导出 BFE `mod_traffic_mirror` 规则文件（`mirror_rule.data`），支持 version 增量同步；每条规则**全字段 7 个 tag 恒输出**（`cond`/`mirrorCluster`/`percentage`/`removeHeaders`/`setHeaders`/`bodyRewrites`/`pathRewrite`，大写驼峰，与 Open API 词汇不同）；空值输出零值（`{}`/`[]`/`""`），不用 omitempty |

### 8.2 测试场景总览

| 编号 | 场景 | 测试类型 | 缺陷家族 | 简要说明 |
|------|------|---------|---------|---------|
| TMIE-1-001 | 首拉与全字段文本形态 | 正常参数 | 家族8 | PUT 2 条（缺省 remove_headers + 显式全字段）→ 7 tag 精确集合与值；原始 body 文本断言 `"mirrorCluster":"..."`、`"removeHeaders":["Authorization","Cookie","X-Api-Key"]`、`"setHeaders":{}`、`"bodyRewrites":[{"path":"model","value":"..."}]`、`"pathRewrite":""` |
| TMIE-1-002 | 显式空黑名单导出 | 正常参数 | 家族1/8 | 显式 `remove_headers:[]` 的规则导出 `"removeHeaders":[]`（与缺省填默认黑名单对照，锁两层默认语义） |
| TMIE-1-003 | 增量同步 | 正常参数 | - | 带当前 version 再拉 → Data null；不传 version 再拉 → 有数据且 Version 相同 |
| TMIE-1-004 | 版本单调 | 并发语义 | 家族8/#142 | 同一秒内连续两次不同 PUT → 轮询导出直到版本严格大于旧版本且内容收敛 |
| TMIE-1-005 | 清空导出 | 边界值 | 家族8 | PUT `{"rules":[]}` → Config.AI_product 为 `[]`（非 null，product 键 present，body 文本 `"AI_product":[]`） |
| TMIE-1-006 | 422 防泄漏 | 异常参数 | 家族4 | PUT 被拒 → 带旧 version 增量拉取 Data null、Version 不变且内容与拒绝前一致 |

---

## 9. 依赖与数据准备

1. `mirror_cluster` 引用真实 Cluster：用例经 `testutil.CreateCluster`（内部级联创建 provider + cluster）准备正向 fixture；负向用例使用 `UniqueClusterName()` 生成必定不存在的名字。
2. 规则 name 使用 run-scoped 唯一命名（`<runID>-tm<用例号>` 前缀），避免并发/重跑污染。
3. Inner 导出 product 键名取自测试环境配置 `AIRouteInnerProductName="AI_product"`。
4. TM-1-015 需 `AIAPI_MYSQL_DSN`（`go test -tags mysql`）；未设置时自动 Skip。
5. 测试前必须重建二进制（`go build -o ai-gateway-api.exe .`），否则测的是旧服务。

## 10. 注意事项

1. PUT 为全量替换，任一用例不得依赖执行顺序：依赖前置状态的用例先自行 PUT 构造集合。
2. 每个负向用例（422）都必须带"GET 回读零变更"断言（家族3）；涉及数据面下发的另加 Inner 导出防泄漏断言（家族4）。
3. 集合级资源无 `/{id}` 端点，审计身份固定为 `resource_type=traffic_mirror_rule`、`resource_id=resource_name=traffic_mirror_rules`。
4. `remove_headers` 相关用例必须同时断言 GET 形状与 Inner 导出行（家族1 省略/显式空 + 家族8 两层默认语义）。
5. 测试结束后集合内容不再恢复：全量替换语义保证后续用例自包含。
