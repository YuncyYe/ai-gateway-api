# Intent Config 测试用例设计文档

## 1. 模块概述

Intent Config（AI 意图配置）是配合 BFE `mod_ai_intent` 模块的单例配置资源：
意图分类的问题集 + 全局置信度门控阈值。OpenAPI 为**单例级全量读写**
（GET/PUT，单行覆盖式存储，固定 `id=1` upsert，不保存历史版本），InnerAPI
导出 `intent_questions.data`（Version 内嵌文件原样，ai-route 形态）。

关键语义：

- `version` 为下发链路内部字段，OpenAPI 不暴露；PUT 成功即内部生成新版本
  （`yyyyMMddHHmmss`，冲突 +1s 顺延）；
- `questions: []` 为**停用意图分类的软开关**（合法，正常下发）；
- `questions` 0–10 个；`type` ∈ `choice|score`；choice 必填 `criteria`（1–10
  项、选项名非空不含 `|`），score 必填 `levels`（1–10 档、Name 唯一），二者
  互斥；全局/逐问题 `min_confidence` ∈ [0,1]，缺省 0.6；
- 校验失败整个 PUT 422（Param Illegal），配置保持原状，并记录失败审计；
- 路由规则的意图条件不随本资源下发：cond 中直传 `req_ai_intent_in(...)`
  （控制面只做语法编译校验，见 expression_verify）。

## 2. 接口列表

| 编号 | 接口名称 | 方法 | 路径 | 说明 |
|------|----------|------|------|------|
| IC-1 | 全量更新意图配置 | PUT | `/open-api/v1/intent-config` | 单行覆盖 upsert（固定 id=1）；生成新内部版本；响应同 GET 结构（无 version） |
| IC-2 | 查询意图配置 | GET | `/open-api/v1/intent-config` | 查询当前生效配置；未发布 404；已发布但 `questions: []` 正常返回空数组 |
| IC-E | 导出 AI 意图配置 | GET | `/inner-api/v1/configs/mod-ai-intent` | 导出 `intent_questions.data`；未发布/版本未变返回 `Data: null` |

## 3. 测试用例统计

| 接口 | 测试用例数 |
|------|-----------|
| 全量更新意图配置（PUT） | 13 |
| 查询意图配置（GET） | 3 |
| 导出 AI 意图配置（InnerAPI） | 7 |
| **合计** | **23** |

> IC-1-013 为 `//go:build mysql` 并发用例（家族10），需 `AIAPI_MYSQL_DSN`，SQLite 环境不编译。

## 4. 认证方式

测试环境配置 `SkipTokenValidate=true`，所有请求无需携带认证头。OpenAPI 权限
`FeatureAIIntent + ActionRead/ActionUpdate`，InnerAPI 权限
`FeatureAIIntent + ActionExport`，测试环境均跳过。

## 5. 目录结构

```
intent_config/
├── design.md
├── update/
│   ├── update_test.go            # IC-1-001 ~ IC-1-012（PUT）
│   └── concurrency_mysql_test.go # IC-1-013（//go:build mysql，并发单例覆盖）
└── get/
    └── get_test.go               # IC-2-001 ~ IC-2-003（GET）

innerapi/intent_config/
└── intent_config_test.go         # IC-E-001 ~ IC-E-007（导出）
```

## 6. 全量更新意图配置（PUT）

| 编号 | 用例 | 预期 |
|------|------|------|
| IC-1-001 | 最小参数（省略 min_confidence） | 200；min_confidence 回填 0.6 |
| IC-1-002 | 完整参数（choice + score 混合） | 200；响应键精确为 min_confidence/questions/created_at/updated_at；无 version/id |
| IC-1-003 | `{"questions": []}` 软开关 | 200；GET 回读 `[]`（非 404） |
| IC-1-004 | 两次 PUT 覆盖 | GET 只保留第二次内容；单行存储 |
| IC-1-005 | 校验失败矩阵（缺 questions/非数组/超 10 个/type 非法/name 空/instructions 空/缺 criteria/选项超 10/选项含 `\|`/重名/score 缺 levels/档超 10/score 带 criteria/档重名/全局及逐问题 min_confidence 越界） | 422 且 message 可归因到具体字段；GET 回读不变；**InnerAPI 导出不变（防泄漏）**；无 500 |
| IC-1-006 | 成功审计 | resource_type=intent_config、update、status=1、身份固定 intent_config |
| IC-1-007 | 失败审计 | 422 同样记录 status=2 审计，ErrorMsg 非空 |
| IC-1-008 | 非法 JSON | 422 |
| IC-1-009 | min_confidence 边界与缺省（家族4/#1） | 0/1 合法回读；-0.001/1.001 422 归因 min_confidence 且导出不变；省略回落 0.6（GET 锁定） |
| IC-1-010 | questions 上界与字段必填（家族4） | 恰 10 个问题合法（响应与导出各 10 条）；score 恰 10 档/选项合法；逐问题阈值越界 422 |
| IC-1-012 | 审计 diff_keys 精确匹配（家族7/#201） | 跨秒第二次 PUT 后 diff_keys 恰为 [min_confidence, questions, updated_at]（ElementsMatch）；before 快照为首次内容 |
| IC-1-013 | 并发单例覆盖（家族10，`//go:build mysql`） | N=20 并发 PUT 全 2xx、无 500/deadlock；终态等于某次完整提交（无混合状态） |

## 7. 查询意图配置（GET）

| 编号 | 用例 | 预期 |
|------|------|------|
| IC-2-001 | 未发布 | 404 |
| IC-2-002 | 发布后回读 | 200；内容与 PUT 提交一致 |
| IC-2-003 | 发布空 questions 后回读 | 200；`questions: []` |

## 8. 导出 AI 意图配置（InnerAPI）

| 编号 | 用例 | 预期 |
|------|------|------|
| IC-E-001 | 未发布首拉 | 200，`Data: null` |
| IC-E-002 | 发布后首拉 | Data 键精确为 PascalCase `Version/MinConfidence/Questions`；Questions 子字段透传 |
| IC-E-003 | 携带 version 再拉（未变化） | 200，`Data: null` |
| IC-E-004 | 再次 PUT 后拉取 | Version 变化、内容更新 |
| IC-E-005 | PUT 空 questions 后导出 | `Questions: []` 正常下发（软开关） |
| IC-E-006 | 版本单调（家族8/#142） | 同秒双 PUT：两次导出 Version 严格递增；旧 version 增量拉取返回新数据 |
| IC-E-007 | min_confidence 数值文本形态（家族8/#9/#102） | 原始 body 含 `"MinConfidence":0.655`；无科学计数法、无精度丢失 |
