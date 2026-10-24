# AI Context Settings 测试用例设计文档

## 1. 模块概述

AI Context Settings（AI 上下文压缩全局设置）模块负责 `mod_ai_context` 降级管线（裁剪层 + 改写层）的全部模块级调优参数管理：**单行覆盖式全量读写**（不提供历史版本查询与单字段更新），独立于规则集合维护——清空 `/ai-context-rules` 不影响本设置。空表 = 文档默认值 `0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95`（与 BFE `setDefaults` 一一对应），GET 返回默认值对象（无时间戳字段）；PUT upsert 单行覆盖，省略字段（含 `rewrite` 子对象或其字段）逐项回填默认。响应**不返回** `created_at`/`updated_at`（全行覆盖语义，时间戳无信息价值）。

## 2. 接口列表

| 编号 | 接口名称 | 方法 | 路径 | 说明 |
|------|----------|------|------|------|
| CTXS-1 | 全量更新上下文压缩全局设置 | PUT | `/open-api/v1/ai-context-settings` | upsert（不存在则插入，存在则整行覆盖） |
| CTXS-2 | 查询上下文压缩全局设置 | GET | `/open-api/v1/ai-context-settings` | 空表返回默认值对象（无时间戳），非 404 |

## 3. 测试用例统计

| 接口 | 测试用例数 |
|------|-----------|
| 全量更新上下文压缩全局设置（PUT） | 6 |
| 查询上下文压缩全局设置（GET） | 2 |
| **合计** | **8** |

> CTXS-1-004 为表驱动用例，含 9 个负向子用例（trigger_ratio 两值 + 预算/图片/字符三负值 + 两枚举 + rewrite 三值）。

## 4. 认证方式

测试环境配置 `SkipTokenValidate=true`，所有请求无需携带认证头。OpenAPI 权限 `FeatureAIContext + ActionUpdate/ActionRead`，测试环境跳过。

## 5. 目录结构

```
ai_context_settings/
├── design.md
├── get/
│   └── get_test.go               # CTXS-2-001 ~ CTXS-2-002（GET）
└── update/
    └── update_test.go            # CTXS-1-001 ~ CTXS-1-006（PUT）
```

## 6. 全量更新上下文压缩全局设置（PUT）

### 6.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | AI Context Settings |
| 接口名称 | 全量更新上下文压缩全局设置 |
| 方法 | PUT |
| 路径 | `/open-api/v1/ai-context-settings` |
| 说明 | 单事务 upsert 单行表（delete-all + insert）；校验失败整体 422，设置不变 |

### 6.2 接口参数说明

#### 6.2.1 请求参数（Body，全部可省略走默认）

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| trigger_ratio | float64 | N | proactive 触发阈值（占预算比例） | (0, 1]，默认 0.7 |
| keep_latest_images | int | N | 保留最近 N 张内联图片，0=不裁图 | ≥ 0，默认 2 |
| tool_result_max_chars | int | N | 单条 tool 结果最大字符数，0=不截断 | ≥ 0，默认 2000 |
| thinking_policy | string | N | thinking 块策略 | `trim-all-but-last` / `keep`，默认 trim-all-but-last |
| chars_per_token | int | N | 文本估算系数（字节/token） | ≥ 1，默认 4 |
| image_token_estimate | int | N | 单张内联图片估值 token | ≥ 0，默认 1200 |
| rewrite | object | N | 改写层子对象 | 未传整体按默认 |
| rewrite.strength | string | N | 改写强度 | `lite` / `full`，默认 lite |
| rewrite.protected_survival_rate | float64 | N | fidelity gate 存活率阈值 | (0, 1]，默认 0.95 |

#### 6.2.2 返回数据字段

设置对象（回填后完整值）。约束：响应恰 7 顶层键 `{trigger_ratio, keep_latest_images, tool_result_max_chars, thinking_policy, chars_per_token, image_token_estimate, rewrite}`（rewrite 恰 2 键 `{strength, protected_survival_rate}`），**不返回** `created_at`/`updated_at`。

### 6.3 测试场景总览

| 编号 | 场景 | 测试类型 | 缺陷家族 | 简要说明 |
|------|------|---------|---------|---------|
| CTXS-1-001 | 全量自定义值 | 正常参数 | 家族1 | 8 项全部显式非默认 → 200，PUT 响应/GET 回读逐字段一致，键集合精确 7+2 |
| CTXS-1-002 | 部分字段回填默认 | 正常参数 | 家族1 | 只传 trigger_ratio=0.8 与 rewrite.strength=full → 其余 6 项回填默认、rewrite.protected_survival_rate 回填 0.95（逐字段合并） |
| CTXS-1-003 | 空对象 = 全默认 | 边界值 | - | PUT `{}` → 8 项全默认 |
| CTXS-1-004 | 422 负向矩阵 | 异常参数 | 家族3/4 | 表驱动 9 子用例：trigger_ratio 0/1.1、keep_latest_images=-1、tool_result_max_chars=-1、thinking_policy 非法、chars_per_token=0、rewrite.strength 非法、rewrite.protected_survival_rate 0/1.1 → 全 422 + 错误消息归因字段名（代表子用例）+ GET 回读零变更 |
| CTXS-1-005 | 合同锁定 | 合同一致性 | 家族11/12 | 响应 marshal 后 NotContains `created_at`/`updated_at`；键集合精确 7+2 |
| CTXS-1-006 | 操作审计 | 审计 | 家族7（#201/#155） | PUT 成功记 update 审计（change_summary 键集合精确、快照含 trigger_ratio/rewrite）；PUT 422 记失败审计且身份固定不依赖请求体 |

### 6.4 测试场景详细设计

#### 6.4.1 CTXS-1-001：全量自定义值（正常参数，家族1）

PUT 8 项全部显式非默认值（trigger_ratio=0.8 / keep_latest_images=4 / tool_result_max_chars=4000 / thinking_policy=keep / chars_per_token=3 / image_token_estimate=800 / rewrite={full, 0.9}）→ 200；PUT 响应与 GET 回读逐字段一致；顶层键集合精确 7 键、rewrite 精确 2 键；响应无时间戳。

#### 6.4.2 CTXS-1-002：部分字段回填默认（正常参数，家族1）

只传 `{"trigger_ratio":0.8,"rewrite":{"strength":"full"}}` → trigger_ratio=0.8、rewrite.strength=full 原样；其余 5 顶层字段回填默认；rewrite.protected_survival_rate 回填 0.95（子对象逐字段合并，非整体覆盖）。

#### 6.4.3 CTXS-1-003：空对象 = 全默认（边界值）

PUT `{}` → 8 项全默认（0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95）。

#### 6.4.4 CTXS-1-004：422 负向矩阵（异常参数，家族3/4）

表驱动 9 子用例（见 6.3 表）；每个负向后 GET 回读与操作前快照一致（家族3）。

#### 6.4.5 CTXS-1-005：合同锁定（合同一致性，家族11/12）

对 CTXS-1-001 的 200 响应：`json.Marshal(Data)` NotContains `created_at`/`updated_at`；顶层键 `assert.ElementsMatch` 精确 7 键、rewrite 2 键（禁 `Contains`，#201 幻影键不敏感）。

#### 6.4.6 CTXS-1-006：操作审计（审计，家族7 / #201 / #155）

照 ai_cache AC-1-010 手法：成功路径以特征值 `trigger_ratio=0.81` 轮询匹配 `resource_type=ai_context_settings, action=update, status=1` 审计，断言 `change_summary` 键集合精确 `{before, after, diff_keys}`、快照含 `trigger_ratio`/`rewrite` 且 JSON 序列化后无 `"id"`；失败路径（trigger_ratio=1.5 → 422）断言 `status=2` 审计出现且 `resource_id=resource_name=ai_context_settings`（服务端固定身份 #155）。

---

## 7. 查询上下文压缩全局设置（GET）

### 7.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | AI Context Settings |
| 接口名称 | 查询上下文压缩全局设置 |
| 方法 | GET |
| 路径 | `/open-api/v1/ai-context-settings` |
| 说明 | 设置行不存在时返回默认值对象（无时间戳字段），HTTP 200 |

### 7.2 测试场景总览

| 编号 | 场景 | 测试类型 | 缺陷家族 | 简要说明 |
|------|------|---------|---------|---------|
| CTXS-2-001 | 空表默认值 | 边界值 | 家族11 | 8 项默认 + rewrite 子对象，键集合恰 7 顶层键（无时间戳） |
| CTXS-2-002 | PUT 后回读一致 | 返回数据 | 家族1 | PUT 全量自定义 → GET 逐字段一致 |

### 7.3 测试场景详细设计

#### 7.3.1 CTXS-2-001：空表默认值（边界值，家族11）

GET（空表）→ 200；顶层键集合精确 `{trigger_ratio, keep_latest_images, tool_result_max_chars, thinking_policy, chars_per_token, image_token_estimate, rewrite}`（无 created_at/updated_at）；8 项默认断言：0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / rewrite={lite, 0.95}。
注意：本用例依赖包内源码顺序最先执行（本包内只有 CTXS-2-002 会写入设置）。

#### 7.3.2 CTXS-2-002：PUT 后回读一致（返回数据，家族1）

PUT 全量自定义 → GET 逐字段一致（深比较）。

---

## 8. 依赖与数据准备

1. 单例资源无跨资源引用；测试包各自独立数据库（PID 隔离）。
2. GET 包内 CTXS-2-001 依赖源码顺序最先执行；update 包用例间以 PUT 全行覆盖自包含。

## 9. 注意事项

1. 422 用例必须带"GET 回读零变更"断言（家族3）。
2. rewrite 子对象为逐字段默认回填：仅传 strength 时 protected_survival_rate 仍回默认（CTXS-1-002 锁定该语义）。
3. 响应合同：恰 7 顶层键 + rewrite 2 键，无时间戳（CTXS-1-005 锁定）。
