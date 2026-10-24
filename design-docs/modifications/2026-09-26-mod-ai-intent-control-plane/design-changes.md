# intent_config 单例资源与 intent_questions.data 导出 —— 设计变更说明

## 1. 概述

### 1.1 变更背景

BFE 数据面 `mod_ai_intent`（`7e482d90`）消费 `conf/mod_ai_intent/intent_questions.data`
（Version + MinConfidence + Questions[]）并支持路由条件原语
`req_ai_intent_in(...)`。控制面需要：维护意图配置（问题集 + 门控阈值）并版本化
下发；升级 bfe 依赖使 cond 编译校验识别新原语。

### 1.2 变更目标

1. `intent_config` 资源：OpenAPI 全量维护 → DB 持久化 → InnerAPI 导出
   `intent_questions.data` → conf-agent 落盘 → BFE 热更；
2. 路由规则消费方式**不变**（cond 直传），意图条件由调用方直接书写
   `req_ai_intent_in(...)`；
3. 校验闭环与 bfe 依赖升级。

### 1.3 设计原则

- **契约冻结源在 BFE**：`intent_questions.data` 的 JSON 结构与字段名以
  `bfe/conf/mod_ai_intent/intent_questions.data` 及
  `bfe/docs/zh_cn/configuration/mod_ai_intent/intent_questions.data.md` 为准；
- **版本即内容**：version 是下发链路的变更检测依据——内容变更则 PUT 生成新
  version；导出侧 version 未变即返回 nil（不触发落盘/热更）；
- **单行覆盖、不留历史**：无历史版本需求，单表单行覆盖式存储，回滚 = 重新
  PUT 旧内容（从操作日志/人工备份找回）；
- **cond 直传零侵入**：不改路由规则 OpenAPI 模型、不做条件合成、不做引用完整性
  校验——未配置问题名的条件在 BFE 运行时"永不命中"（fail-safe），天然安全；
- **最小范围**：本方案只新增"意图配置"一个资源及其导出链路。

## 2. 资源模型

### 2.1 单例资源 `intent_config`

**全局单例，单行覆盖式**：

| 维度 | 取值 |
|------|------|
| 作用域 | 全局（全部 BFE 实例共享同一份意图配置） |
| 写入 | 全量 PUT：覆盖唯一一行，`version` 更新为服务端新生成的时间戳 |
| 读取 | GET 返回当前行（version 不在 OpenAPI 暴露，见 api-changes §2.1） |
| 历史 | **不保存**：无 `enabled` 列、无版本表；回滚素材依赖操作日志/人工备份 |
| 与 BFE 的对应 | 唯一一行 ⇔ 一份 `intent_questions.data` |

> 命名说明：资源名为 `intent-config`，内容含 `questions` + `min_confidence` +
> `version`；BFE 数据文件名 `intent_questions.data` 已发布且 `QuestionsPath`
> 可配，保持不动（文件名 ≠ 资源名，有意保留）。
>
> 空 questions 语义：`questions` 允许为空数组——表示**停用意图分类**：BFE 侧
> 所有问题的答案恒为 unknown，`req_ai_intent_in(...)` 永不命中，流量回落默认
> 路由；规则无需修改，发布非空配置即恢复。作为故障时的软开关使用。

### 2.2 与路由规则的关系（cond 直传）

- `intent_config` 定义"有哪些问题、选项是什么、门控阈值多少"；
- 路由规则 cond 中按问题名/选项引用，例：
  `req_ai_intent_in("task_type", "test_writing", 0.9)`；
- **控制面不做引用完整性校验**（关键决策 #4）：cond 中引用未配置的问题名时，
  BFE 运行时该条件永不命中、规则 fall through 到下一条——fail-safe 语义，
  不报错、不阻断；配置补齐后规则自动生效，无需改规则。

## 3. 数据模型

### 3.1 表结构 `intent_config`（MySQL，`db_ddl.sql`）

```sql
CREATE TABLE `intent_config` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `version` varchar(32) NOT NULL COMMENT '当前配置版本，时间戳格式 yyyyMMddHHmmss，每次 PUT 更新',
  `min_confidence` decimal(4,3) NOT NULL DEFAULT 0.600 COMMENT '全局置信度门控阈值 [0,1]',
  `questions` text NOT NULL COMMENT '问题集 JSON（可为 []，表示停用意图分类），结构同 BFE intent_questions.data 的 Questions[]',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
) COMMENT='AI 意图配置（mod_ai_intent intent_questions.data 数据源：问题集+门控阈值，单行覆盖式存储）';
```

`db_ddl_sqlite.sql` 给出等价 SQLite 方言（`text`/`integer` 化，参照
`ai_cache_rules` 的 sqlite 写法）。

### 3.2 DAO 与 storager

照 `model/rate_limit_policy` 的组织 + `global_route_rules` 的读写形态：

- `storage/rdb/iintent_config/`：`intentConfigStorager`
  - `Upsert(ctx, *IntentConfig) error` — 覆盖唯一行（不存在则插入，固定 id=1），
    version 由 manager 生成后随行写入
  - `Fetch(ctx) (*IntentConfig, error)` — 读取唯一行；无记录返回空（未发布态）
- `model/iintent_config/`：manager + storager 接口（`itxn.TxnStorager`
  事务，不开 ad-hoc 事务）；手写 callback mock（`fakeIntentConfigStorager`）。

Go 结构（持久化层）：

```go
type IntentConfig struct {
    Id            int64
    Version       string            // yyyyMMddHHmmss，每次 PUT 更新
    MinConfidence float64           // decimal(4,3)
    Questions     string            // JSON 文本，透传 BFE 结构（不在控制面展开字段）
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

**关键决策**：`Questions` 在控制面**以 JSON 文本透传**，不在 ai-gateway-api 定义
展开结构体——BFE 是契约冻结源，控制面只做**整体性校验**（JSON 合法、顶层字段
齐全、逐问题字段合法、选项名不含 `|`），与 BFE `questions_conf.go` 的校验保持
同口径（实现时两边对照，见 4.3）。

## 4. 导出契约（冻结）

### 4.1 文件结构

conf-agent 落盘文件：`conf/mod_ai_intent/intent_questions.data`（JSON，文件名
为 BFE 侧既有契约，不随资源改名）：

```json
{
  "Version": "20260926120000",
  "MinConfidence": 0.6,
  "Questions": [
    {
      "Name": "task_type",
      "Type": "choice",
      "Instructions": "这条请求属于哪类研发任务？",
      "Criteria": {
        "coding": "编写或修改代码、调试、重构、代码审查",
        "test_writing": "编写测试用例、单元测试、集成测试、补充断言",
        "doc_writing": "编写文档、README、注释、接口说明、使用示例"
      }
    },
    {
      "Name": "complexity",
      "Type": "score",
      "Instructions": "这个任务的复杂度如何？",
      "MinConfidence": 0.7,
      "Levels": [
        {"Name": "simple",  "Description": "单步即可完成"},
        {"Name": "medium",  "Description": "多步但模式常见"},
        {"Name": "complex", "Description": "需要深入推理或跨模块设计"}
      ]
    }
  ]
}
```

字段语义、合法性与热更语义全部以
`bfe/docs/zh_cn/configuration/mod_ai_intent/intent_questions.data.md` 为准
（要点：`Type` 仅 `choice|score`；choice 带 `Criteria`、score 带 `Levels`，
互斥且必填其一；`Version` 内容变更必须更新）。

### 4.2 版本语义

- 控制面 PUT 成功即生成新版本（`yyyyMMddHHmmss`，冲突时 +1s 顺延），覆盖
  单行存储；
- Generator 走 `iversion_control`：新增 topic `ConfigTopicIntentConfig =
  "intent_config"`；导出内容 version 与上次导出的相同返回 nil（不触发
  conf-agent 落盘与 BFE 热更）；
- BFE 侧对"版本不变的热更"也会跳过——双端同语义。

### 4.3 控制面校验与 BFE 校验的对应关系

| 校验项 | 控制面（本方案） | BFE（已存在） | 说明 |
|--------|------------------|---------------|------|
| JSON 合法、顶层字段齐全 | ✅ PUT 时 | ✅ 热更时 | 双端各自兜底 |
| Name 非空唯一、Type 枚举 | ✅ | ✅ | 同口径 |
| choice: Criteria 1–10、选项名唯一 | ✅ | ✅（BFE 上限 255） | 控制面更严 |
| score: Levels 1–10、Name 唯一、有序 | ✅ | ✅（BFE 上限 255） | 控制面更严 |
| 选项名不含 `\|` | ✅ | ✅（热更拒绝） | 同口径 |
| MinConfidence ∈ [0,1]（全局/逐问题） | ✅ | ✅ | 同口径 |
| 路由 cond 中 `req_ai_intent_in(...)` 语法 | ✅ 编译校验（`condition.Build`，随 bfe 依赖升级生效） | ✅ 规则加载时编译 | 控制面拦截语法错误；引用完整性不做校验（fail-safe，见 §2.2） |

### 4.4 导出最小字段集

导出即落盘内容本身（Version/MinConfidence/Questions），无裁剪、无富化。
Generator 逻辑（`IntentConfigGenerator`）：

1. `Fetch` 取当前行；无记录 → 返回 nil（不导出，BFE 维持现状）；
2. 组装 `IntentConfigDataExport{Version, MinConfidence, Questions(RawMessage)}`；
3. 交 `versionControlManager.ExportConfig(ctx, ConfigTopicIntentConfig, generator)`。

### 4.5 Inner API 端点

| 端点 | 说明 |
|------|------|
| `GET /inner-api/v1/configs/mod-ai-intent` | 导出当前配置；无数据返回 204（或按 export_util 惯例的空响应），注册进 `endpoints/innerapi_v1/endpoints.go` |

## 5. 路由规则消费方式（cond 直传，无模型变更）

路由规则 OpenAPI 与存储**完全不变**：调用方在 `rules[].Cond` 中直接书写意图
条件，控制面仅做编译校验（`lib/validate` 的 `ConditionExpression`），原样存储、
原样导出。示例：

```
req_ai_intent_in("task_type", "test_writing", 0.9) && req_ai_intent_in("complexity", "simple|medium")
```

书写约定（出自 BFE 侧文档，控制面只校验语法不校验语义）：

- 第一参：问题名，须与 `intent_config.questions[].name` 一致（引用了未配置的
  问题名 → 运行时永不命中，fail-safe）；
- 第二参：`|` 分隔的选项列表（choice 填选项名，score 填档位名）；
- 第三参（可选）：显式置信门槛；省略 = 用该问题 `min_confidence`；显式零写作
  `0.0`（整数字面量 `0` 是 INT，会被 BFE 原型检查拒绝）。

BFE 侧完整语义见 `bfe/docs/zh_cn/condition/request/intent.md`。

## 6. 参数校验（`lib/validate`）

本方案**不新增校验函数**：路由规则 cond 继续走既有 `ConditionExpression`
（`validate.go:534`，`condition.Build` 编译校验）。**前置依赖**：go.mod 的
bfe 升级到含 `req_ai_intent_in` 的版本后，新原语的语法校验自动生效（无需
代码改动）；语义层面（问题名/选项是否存在）不校验（§2.2 fail-safe 决策）。

意图配置自身（PUT intent-config）的校验在 `model/iintent_config` 内实现
（§4.3 口径），不经 `lib/validate`。

## 7. 模块基础配置不下发

`mod_ai_intent.conf`（决策服务地址/超时/缓存/熔断/显式头名）属部署期静态
配置，与 ai_cache 的 Redis 连接不下发同理：**本方案只下发 `intent_questions.data`**。

## 8. 容器装配

- `container` 新增 `IntentConfigManager` / `IntentConfigExporter`；
- `stateful` 初始化 storager（MySQL/SQLite 按现有惯例）；
- InnerAPI `endpoints.go` 登记 `/configs/mod-ai-intent`；
- OpenAPI `endpoints.go` `merge(intentconfig.Endpoints...)`；
- OpenAPI handler：`endpoints/openapi_v1/intent_config/`（`endpoints.go` +
  `get.go` + `put.go`，风格照 `openapi_v1/ai_route/`）。

## 9. 测试计划

### 9.1 单元测试（硬门禁，model 覆盖率 ≥70%）

- manager：首次 PUT 插入单行、再次 PUT 覆盖且 version 更新、version 冲突
  顺延、Fetch 无记录行为；
- Generator：有/无记录、version 未变返回 nil、JSON 透传正确；
- 意图配置整体校验：全分支（questions JSON 合法性、同口径校验项、空数组
  软开关语义）。

### 9.2 集成测试

- 链路：PUT intent-config → GET 回读 → InnerAPI 导出断言 JSON 契约 →
  （conf-agent 就位后）BFE 热更生效（该段属 conf-agent/BFE 侧）；
- 路由：含 `req_ai_intent_in(...)` 的 cond 规则在 bfe 依赖升级后通过校验并
  原样导出；旧版依赖下该 cond 被拒（升级前行为的反向断言）。

### 9.3 回归

- `make test-model-cover-gate`；
- 既有路由规则单测/集成（cond 直传链路行为不变）。

## 10. 风险与回滚

| 风险 | 缓解 |
|------|------|
| bfe 依赖升级引入编译/行为差异 | 单独一个 commit 升级并全量 `make test`；BFE 侧本次仅 additive 变更（新文件/新原语/词法一行），风险低 |
| 意图配置下发后 BFE 热更拒绝（校验口径漂移） | 双端校验同口径（4.3）；集成测试覆盖；回滚=重新 PUT 旧内容 |
| cond 引用未配置问题名导致规则不生效 | fail-safe 语义（永不命中走默认规则），非故障；发布意图规则前先发布 intent-config 即避免 |
| 旧 BFE 集群加载含新原语的规则 | 编译失败、规则不生效（BFE 加载期拒绝该规则）；升级 BFE 前不要对旧集群发布意图规则 |
| 手写 cond 语法错误 | `condition.Build` 存储前拦截（升级后） |
| 无历史版本，误操作后无法直接回滚 | 操作日志/人工备份找回旧内容重新 PUT；发布前 GET 留存副本 |

回滚：本功能全增量；回滚代码后旧库表保留无影响（新表不再读写）。数据回滚=
重新 PUT 旧内容（无服务端历史，需从操作日志/备份获取）。

## 11. WBS

| # | 任务 | 依赖 |
|---|------|------|
| 1 | go.mod 升级 bfe 依赖 + 全量测试 | — |
| 2 | DDL（MySQL/SQLite）+ DAO/storager | — |
| 3 | model/iintent_config（manager/generator/整体校验） | 1、2 |
| 4 | OpenAPI intent-config GET/PUT | 3 |
| 5 | InnerAPI /configs/mod-ai-intent + 容器装配 | 3 |
| 6 | 单测 + cover-gate | 全部 |
| 7 | 集成测试（本仓内） | 5 |
| 8 | conf-agent `[Reloaders.mod_ai_intent]`（另仓，契约冻结后） | 5 |

## 12. 决策记录与待决策点

**已决策**（评审确认）：

1. **不保存历史版本**：单行覆盖式存储，无 `enabled` 列/版本表；回滚素材依赖
   操作日志或人工备份。理由：无历史查询需求，覆盖式 + 软开关已够用。
2. 空 `questions` = 停用意图分类的软开关（§2.1）。
3. 路由规则保持 cond 直传，不做结构化 intent filter（关键决策 #3/#4）。

**待决策**（含建议）：

1. **BFE 数据文件名对齐**：`intent_questions.data` 与资源名 `intent-config`
   不一致（历史原因，已发布）。可选后续在 BFE 侧做纯改名（`QuestionsPath`
   可配，无兼容风险）；一期不动。
2. **二期是否引入结构化 intent filter**（BasicInfo 扩展 + 服务端合成 cond）：
   当前 cond 直传已够用；若 dashboard 需要意图条件的可视化编排（下拉选问题/
   选项），再评估引入。
