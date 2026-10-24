# AI Cache Semantic Settings 测试用例设计文档

## 1. 模块概述

AI Cache Semantic Settings（AI 缓存语义全局设置）是配合 BFE `mod_ai_cache` 二期语义缓存（embedding + 向量相似度检索）的单例调优资源：**单行覆盖式全量读写**，不提供 `/{id}`、无 PATCH、无历史版本。三个业务字段均可省略走文档默认值（`top_k=1` / `threshold=0.15` / `threshold_relation=lt`）；空设置表时 GET 返回**默认值对象（无时间戳字段，HTTP 200 非 404）**，写入后响应携带 `created_at`/`updated_at`（RFC3339）。设置独立于规则集合维护（清空 `/ai-cache-rules` 不影响本设置），但共享同一条 Inner 导出版本流（导出顶层 `Semantic` 块恒导出，见 `innerapi/ai_cache` 模块 AICE-1-007~010）。

## 2. 接口列表

| 编号 | 接口名称 | 方法 | 路径 | 说明 |
|------|----------|------|------|------|
| ACSS-1 | 全量更新语义全局设置 | PUT | `/open-api/v1/ai-cache-semantic-settings` | upsert（不存在插入、存在整行覆盖）；校验失败 422 且设置不变 |
| ACSS-2 | 查询语义全局设置 | GET | `/open-api/v1/ai-cache-semantic-settings` | 空表返默认值对象（无时间戳）；写入后回读全量 |

## 3. 测试用例统计

| 接口 | 测试用例数 |
|------|-----------|
| 全量更新语义全局设置（PUT） | 5 |
| 查询语义全局设置（GET） | 2 |
| **合计** | **7** |

## 4. 认证方式

测试环境配置 `SkipTokenValidate=true`，所有请求无需携带认证头。OpenAPI 权限 `FeatureAICache + ActionUpdate/ActionRead`，测试环境均跳过。

## 5. 目录结构

```
ai_cache_semantic_settings/
├── design.md
├── update/
│   └── update_test.go            # ACSS-1-001 ~ ACSS-1-005（PUT）
└── get/
    └── get_test.go               # ACSS-2-001 ~ ACSS-2-002（GET）
```

## 6. 全量更新语义全局设置（PUT）

### 6.1 接口信息

| 项目 | 值 |
|------|-----|
| 模块 | AI Cache Semantic Settings |
| 接口名称 | 全量更新语义全局设置 |
| 方法 | PUT |
| 路径 | `/open-api/v1/ai-cache-semantic-settings` |
| 说明 | 单事务 upsert 单行表；三个字段均可省略走默认；校验失败整体 422、设置不变、记录失败审计 |

### 6.2 接口参数说明

#### 6.2.1 请求参数（Body）

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| top_k | int | N | 向量检索近邻个数 | 1–10，默认 1 |
| threshold | float64 | N | 相似度阈值 | 0–2，默认 0.15 |
| threshold_relation | string | N | 阈值比较方向 | `lt`（默认）/`lte`/`gt`/`gte`，大小写敏感 |

#### 6.2.2 返回数据字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| top_k | int | 向量检索近邻个数 |
| threshold | float64 | 相似度阈值 |
| threshold_relation | string | 阈值比较方向 |
| created_at | string | 创建时间（RFC3339，只读，仅写入后响应携带） |
| updated_at | string | 更新时间（RFC3339，只读，仅写入后响应携带） |

### 6.3 测试场景总览

| 编号 | 场景 | 测试类型 | 缺陷家族 | 简要说明 |
|------|------|---------|---------|---------|
| ACSS-1-001 | 全字段自定义 | 正常参数 | - | PUT 三字段自定义 → 200 回显 + 时间戳存在；GET 回读逐字段一致 |
| ACSS-1-002 | 部分字段省略回落默认 | 正常参数 | 家族1 | 仅 top_k / 仅 relation / 空对象 → 省略字段回落默认；整行覆盖（上次自定义值不保留） |
| ACSS-1-003 | 422 边界矩阵 | 异常参数 | 家族3/4/7 | 表驱动 9 子用例：top_k=0/11/类型错、threshold=-0.1/2.1/类型错、relation="LT"/"xx"/类型错 → 全 422 + 错误可归因 + GET 零变更 |
| ACSS-1-004 | 边界正值 | 边界值 | 家族4 | top_k∈{1,10}、threshold∈{0,2}、relation∈{lte,gt,gte} → 200 且回读一致（边界±1） |
| ACSS-1-005 | 非法 JSON | 异常参数 | 家族3 | 非 JSON 请求体 → 422 + GET 零变更 |

### 6.4 测试场景详细设计

#### 6.4.1 ACSS-1-001：全字段自定义

PUT `{"top_k":6,"threshold":0.9,"threshold_relation":"lte"}` → 200；响应三字段精确回显、`created_at`/`updated_at` 非空、键集合恰 5 键；GET 回读与 PUT 响应 DeepEquals。

#### 6.4.2 ACSS-1-002：部分字段省略回落默认（家族1）

- 仅 `top_k=5` → `threshold=0.15`、`threshold_relation="lt"`；
- 仅 `threshold_relation="gte"`（上次写入 top_k=5）→ `top_k` 回落 1（整行覆盖语义，非字段合并）；
- 空对象 `{}` → 全默认值，响应含时间戳。

#### 6.4.3 ACSS-1-003：422 边界矩阵（家族3/4/7）

先落合法基线（`3/0.42/gt`）并取快照；9 个负向子用例各自断言：`ErrNum=422`（且非 500，家族7/#183）、错误消息可归因到具体字段（家族4）、GET 快照与基线 DeepEquals（家族3 零变更）。`relation="LT"` 锁定**大小写敏感**。

#### 6.4.4 ACSS-1-004：边界正值（家族4）

`top_k=1`、`top_k=10`、`threshold=0`、`threshold=2`、`relation=lte/gt/gte` 五个合法边界组合 → 200，PUT 响应与 GET 回读逐字段一致。

#### 6.4.5 ACSS-1-005：非法 JSON

`RawBody("PUT", path, "not-json")` → 422 且 GET 零变更。

---

## 7. 查询语义全局设置（GET）

### 7.1 测试场景总览

| 编号 | 场景 | 测试类型 | 缺陷家族 | 简要说明 |
|------|------|---------|---------|---------|
| ACSS-2-001 | 空表默认值对象 | 边界值 | 家族11 | 空表 GET → 200（非 404）默认值 `1/0.15/lt`；键集合恰 3 键，无 created_at/updated_at |
| ACSS-2-002 | 写入后回读一致 | 正常参数 | 家族1 | PUT 自定义 → GET 回读一致且含时间戳键；PUT 响应与 GET DeepEquals |

### 7.2 测试场景详细设计

#### 7.2.1 ACSS-2-001：空表默认值对象（家族11）

单例空表**非 404**：GET 返回 200 默认值对象；键集合 `ElementsMatch` 精确 `{top_k, threshold, threshold_relation}`（禁止 Contains——幻影时间戳键不敏感，#201）；值锁定 `1/0.15/lt`。

> 本用例依赖包内源码顺序最先执行：Go 测试按源码顺序串行运行，本包内唯一写操作在 ACSS-2-002。

#### 7.2.2 ACSS-2-002：写入后回读一致（家族1）

PUT `{"top_k":8,"threshold":1.25,"threshold_relation":"gt"}` → GET 回读逐字段一致、`created_at`/`updated_at` 非空、键集合恰 5 键；PUT 响应与 GET 响应 DeepEquals。

---

## 8. 依赖与数据准备

1. 本模块为全局单例，无跨资源引用；测试间状态按源码顺序传递（PUT 用例在前、GET 用例在后）。
2. Inner 导出侧（顶层 `Semantic` 块恒导出、设置变更驱动版本流、清空规则后 `Semantic` 仍在）由 `tests/innerapi/ai_cache` 模块 AICE-1-007~010 覆盖。
3. 校验规则出处：`lib/validate.AICacheSemanticSettings`（top_k∈[1,10]、threshold∈[0,2]、relation∈lt/lte/gt/gte 大小写敏感）。

## 9. 注意事项

1. 每个负向用例（422）都必须带"GET 零变更"断言（家族3）。
2. 默认值对象与写入后响应是**两种键形态**（无时间戳 / 含时间戳）：schema 侧用单个 schema（Required 3 业务字段 + Optional 时间戳）兼容，定向键集合断言分别锁定。
3. threshold 为 float64，断言用 `InDelta`（1e-9）规整。
