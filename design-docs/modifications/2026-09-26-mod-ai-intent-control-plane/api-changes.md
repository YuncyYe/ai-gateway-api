# /intent-config 与 /configs/mod-ai-intent —— API 契约变更

## 1. 变更概览

| 类型 | 端点/字段 | 变化 |
|------|-----------|------|
| Open API 新增 | `GET /open-api/v1/intent-config` | 查询当前生效意图配置（问题集 + 门控阈值） |
| Open API 新增 | `PUT /open-api/v1/intent-config` | 全量覆盖式更新（生成新版本） |
| Open API 不变 | 路由规则（route rules） | **无变更**：cond 直传模式，意图条件直接在 cond 字符串中书写 `req_ai_intent_in(...)` |
| Inner API 新增 | `GET /inner-api/v1/configs/mod-ai-intent` | 导出 `intent_questions.data` |
| 依赖 | `go.mod` bfe 升级 | cond 编译校验（`lib/validate`）随之识别 `req_ai_intent_in`，无代码改动 |

## 2. 字段统一定义（Open API 词汇：小写下划线）

### 2.1 intent_config 资源

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| min_confidence | number | 否 | 全局门控阈值 [0,1]，默认 0.6 |
| questions | object[] | 是 | 问题数组（结构 2.2），**0–10 个**；空数组 = 停用意图分类（所有意图条件不命中，流量走默认路由），作为故障软开关 |
| created_at / updated_at | string | 出参 | RFC3339 |

> `version` 是下发链路（InnerAPI 导出 → conf-agent 落盘 → BFE 数据文件）的
> 内部契约字段，**不在 OpenAPI 暴露**；版本由服务端在 PUT 成功时内部生成。

### 2.2 questions[] 元素（透传 BFE 契约）

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| name | string | 是 | 问题名，全局唯一 |
| type | string | 是 | `choice` 或 `score` |
| instructions | string | 是 | 判定指令 |
| criteria | map<string,string> | type=choice 必填 | 选项名→描述，1–10 项，选项名不含 `|`，与 `levels` 互斥 |
| levels | object[] | type=score 必填 | `{name, description}` 低到高 1–10 档，与 `criteria` 互斥 |
| min_confidence | number | 否 | 逐问题覆盖全局阈值 [0,1] |

> 数量上限说明：questions 数组控制面一期允许 **0–10** 个，choice 选项、score
> 档位为 **1–10**（BFE 侧协议上限为 255，控制面更严不影响兼容；放宽时改控制面
> 校验即可，无需动 BFE）；questions 为空数组 = 停用意图分类的软开关。

## 3. Open API 明细

### 3.1 GET /open-api/v1/intent-config

查询当前生效版本。

**响应 200**：

```json
{
  "min_confidence": 0.6,
  "questions": [
    {"name": "task_type", "type": "choice", "instructions": "这条请求属于哪类研发任务？",
     "criteria": {"coding": "编写或修改代码、调试、重构、代码审查",
                   "test_writing": "编写测试用例、单元测试、集成测试、补充断言",
                   "doc_writing": "编写文档、README、注释、接口说明、使用示例"}},
    {"name": "complexity", "type": "score", "instructions": "这个任务的复杂度如何？",
     "min_confidence": 0.7,
     "levels": [{"name": "simple", "description": "单步即可完成"},
                 {"name": "medium", "description": "多步但模式常见"},
                 {"name": "complex", "description": "需要深入推理或跨模块设计"}]}
  ],
  "created_at": "2026-09-26T12:00:00+08:00",
  "updated_at": "2026-09-26T12:00:00+08:00"
}
```

**响应 404**：尚未发布过意图配置。

### 3.2 PUT /open-api/v1/intent-config

全量覆盖式更新。语义：整体验证通过后生成新版本并置为生效。

**请求**：同 GET 响应结构（无 `created_at`/`updated_at`）。

**响应 200**：同 GET 结构（version 由服务端内部生成，不在响应暴露）。

**校验失败 422**（错误码见 §5，仓库 xerror 约定：参数错误统一 422）：

- `questions` 超过 10 个；
- `name` 重复/为空；`type` 非 `choice|score`；
- `criteria`/`levels` 缺失或与 type 不匹配、超 1–10 项/档、选项名含 `|`；
- `min_confidence` 越界（全局或逐问题）。

### 3.3 路由规则使用意图条件（cond 直传，接口无变更）

路由规则的请求/响应结构**不变**。意图条件在 `rules[].Cond` 中直接书写：

```json
{
  "rules": [{
    "name": "test-to-flash",
    "Cond": "req_ai_intent_in(\"task_type\", \"test_writing\", 0.9) && req_ai_intent_in(\"complexity\", \"simple|medium\")",
    "targets": [{"cluster_name": "cluster_flash", "model": "deepseek-v4.1-flash", "weight": 100}],
    "fallbacks": [{"cluster_name": "cluster_kimi", "model": "kimi-2.8"}]
  }]
}
```

书写约定（控制面只校验语法，不校验问题名/选项的引用完整性——运行时未配置的
问题名"永不命中"，fail-safe）：

- 第一参：问题名，对应 `intent-config.questions[].name`；
- 第二参：`|` 分隔的选项列表（choice 填选项名，score 填档位名）；
- 第三参（可选）：显式置信门槛，省略 = 用该问题 `min_confidence`；显式零写作
  `0.0`。

BFE 侧完整语义见 `bfe/docs/zh_cn/condition/request/intent.md`。

## 4. Inner API 明细

### 4.1 GET /inner-api/v1/configs/mod-ai-intent

导出当前生效版本，供 conf-agent 落盘 `conf/mod_ai_intent/intent_questions.data`
（文件名为 BFE 侧既有契约，与资源名不同，见 design-changes §2.1 命名说明）。

**响应 200**（即文件内容）：

```json
{
  "Version": "20260926120000",
  "MinConfidence": 0.6,
  "Questions": [ ... ]
}
```

（导出字段为 BFE 文件契约的 PascalCase，与 Open API 的 camelCase 不同——
转换发生在 Generator，见 `design-changes.md` §4.1/4.4。）

**响应 204 / 空**：无生效版本（不导出，conf-agent 不落盘）。

**版本未变**：`iversion_control` 返回 nil，conf-agent 不触发落盘与 reload。

## 5. 校验与错误码

| 场景 | HTTP | error_code（沿用 xerror 惯例，参数错误统一 422） |
|------|------|------|
| intent-config JSON/字段非法 | 422 | INVALID_ARGUMENT（细分 message） |
| min_confidence 越界 | 422 | INVALID_ARGUMENT |
| 路由 cond 含 `req_ai_intent_in` 语法错误 | 422 | INVALID_ARGUMENT（`condition.Build` 编译失败；需 bfe 依赖已升级） |

## 6. 语义边界速查

| 问题 | 答案 |
|------|------|
| version 在哪看？ | version 是下发链路的内部字段，OpenAPI 不暴露；PUT 后内部生成新版本并即刻生效，审计走操作日志/DBA 查询 `intent_config` 表 |
| PUT 是覆盖还是合并？ | 全量覆盖；questions 整体替换 |
| 怎么快速停用意图分类（如决策服务故障）？ | PUT 一条 `questions: []` 的新版本（软开关）——所有意图条件不命中、流量走默认路由；恢复 = 发布非空配置，规则无需改动 |
| PUT 后旧版本去哪了？ | **不保存历史**（单行覆盖式存储）；回滚 = 重新 PUT 旧内容（从操作日志/人工备份找回），或临时 PUT `questions: []` 软开关停用 |
| 路由规则里引用了配置中不存在的问题名？ | 语法校验通过；BFE 运行时该条件**永不命中**（fail-safe），规则 fall through 到默认规则；配置补齐后自动生效 |
| BFE 版本较旧不认识新原语？ | 数据面前提：BFE ≥ `7e482d90`；旧 BFE 加载含 `req_ai_intent_in` 的规则会编译失败，升级前不要对旧集群发布意图规则 |
| 显式零门槛怎么写？ | cond 中写 `..., 0.0)`（整数字面量 `0` 是 INT 会被原型检查拒绝）；省略第三参则用问题阈值 |

## 7. 权限点

照 `ai_cache_rules` 先例登记（FAP 命名）：

- `intent_config:get` / `intent_config:put`（管理意图配置）
- 路由规则权限点不变（cond 直传，无新字段、无新权限点）

## 8. 代码变更清单（实施参考）

| 层 | 文件 |
|----|------|
| DDL | `db_ddl.sql`、`db_ddl_sqlite.sql`（新表 `intent_config`） |
| storage | `storage/rdb/iintent_config/`（DAO + storager） |
| model | `model/iintent_config/`（manager、generator、整体校验） |
| OpenAPI | `endpoints/openapi_v1/intent_config/`（endpoints/get/put）+ `endpoints/openapi_v1/endpoints.go` merge |
| InnerAPI | `endpoints/innerapi_v1/intent_config_export/`（或并入既有 export 组织）+ `endpoints/innerapi_v1/endpoints.go` |
| 容器/装配 | `lib/container/`、`stateful/` |
| 依赖 | `go.mod`：bfe 升级至含 `req_ai_intent_in` 的提交（`lib/validate` 零改动获得新原语校验） |

> 路由规则侧（`model/iai_route`、`lib/validate`、`route_rules`）**零改动**。

## 9. 测试变更建议

- 单测：manager 版本流、Generator、意图配置整体校验全分支；cover-gate ≥70%；
- 集成：PUT→GET→InnerAPI 导出契约断言；含 `req_ai_intent_in` 的 cond 规则
  在升级后通过校验并原样导出（升级前被拒的反向断言）；
- 回归：既有 route rules 单测/集成（cond 直传链路行为不变）。
