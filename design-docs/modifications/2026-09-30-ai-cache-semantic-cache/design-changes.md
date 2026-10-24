# ai-cache 语义缓存控制面扩展 —— 设计变更说明

> 本文是 `2026-09-24-ai-cache-rule-export` 的增量设计。数据面（BFE）侧规格已冻结并实现，见 BFE 仓 `docs/zh_cn/modifications/2026-09-30-ai-cache-semantic-cache/design-changes.md`；本文档只覆盖 ai-gateway-api 控制面。

## 1. 概述

### 1.1 变更背景

BFE `mod_ai_cache` 二期语义缓存已入库（origin `v1.8.9-dev`，commit `94d5e935`）。数据面规则加载器的新契约：

1. 规则元素新增可选字段 `enableSemanticCache`（json tag，规则级语义开关）；
2. 规则数据文件新增顶层 `Semantic` 块（`topK`/`threshold`/`thresholdRelation`，模块级全局调优参数，随文件热加载）。

控制面需要：规则导出带上开关字段、提供全局设置的存储与 API、生成器把设置合并进 `ai_cache.data` 顶层。

### 1.2 变更目标

1. `ai_cache_rules` 表加列 `enable_semantic_cache`，Open API rules GET/PUT 透传；
2. 新增单例设置资源 `ai_cache_semantic_settings`（GET 默认值兜底 / PUT upsert）；
3. `AICacheRuleGenerator` 导出顶层 `Semantic` 块（恒导出），版本流覆盖设置变更；
4. 与 BFE 已实现的加载器逐字段对齐（tag、默认值、量纲语义），两侧契约冻结。

### 1.3 设计原则

- **登记式扩展**：不改导出框架、不动 conf-agent、不加新 Feature；
- **单例设置全量写**：单行表 + upsert，语义最简，与 rules 集合资源风格一致；
- **默认值下沉对齐**：控制面默认值与 BFE `setDefaults` 完全一致（`1 / 0.15 / lt / false`），空表即默认；
- **设置与规则生命周期解耦**：清空规则不影响 `Semantic` 块导出。

## 2. 资源模型

### 2.1 既有集合资源 `ai_cache_rules`（加字段）

元素新增 `enable_semantic_cache`（bool，默认 false）。资源形态、优先级语义（数组顺序 = id 升序 = first-match-wins）、无 `enabled`、无 `/{id}` 等 phase-1 决策**全部保留**。

### 2.2 新增单例资源 `ai_cache_semantic_settings`

| 维度 | 说明 |
|------|------|
| 形态 | 全系统单例（单行表），非集合；GET 读全量 + PUT 全量写（upsert） |
| 生命周期 | 独立于规则集合：rules 清空后设置仍在并继续导出 |
| 寻址 | 无 id/name；API 路径即身份（`/ai-cache-semantic-settings`） |
| 空表语义 | GET 返默认值对象；导出用默认值——**不显式插入默认行**，避免双真相 |

### 2.3 与 rate_limit_policy / imods 的关系

- 不像 rate_limit_policy（apikey 子资源、带 Redis key 生成），本设置无下游寻址需求；
- **不走 `model/imods` 模式**：imods 每个 manager 产出独立 topic 文件（如 `mod_body_process.data`），而 `Semantic` 块必须并入 `ai_cache.data` 顶层（BFE 只加载这一个文件）；imods 的 `ModBodyProcessConf` 形态（`Version + Config`）也无法表达顶层并列键。故设置为 ai_cache 域内子资源，由 `AICacheRuleGenerator` 合并导出。

## 3. 数据模型

### 3.1 迁移 DDL（`db_ddl.sql`）

```sql
-- 规则表加列：语义缓存开关（存量规则默认关闭，向后兼容）
ALTER TABLE `ai_cache_rules`
  ADD COLUMN `enable_semantic_cache` TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否启用语义缓存: 0-否, 1-是';

-- 语义全局设置（单行表；空表 = 默认值 1/0.15/lt）
CREATE TABLE `ai_cache_semantic_settings` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（恒为1语义，物理单行）',
  `top_k` INT NOT NULL DEFAULT 1 COMMENT '语义检索TopK（1-10）',
  `threshold` DOUBLE NOT NULL DEFAULT 0.15 COMMENT '相似度阈值（量纲与 threshold_relation 一致，0-2）',
  `threshold_relation` VARCHAR(8) NOT NULL DEFAULT 'lt' COMMENT '阈值比较: gt/gte/lt/lte',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI缓存语义全局设置表（单行）';
```

`db_ddl_sqlite.sql` 按惯例去 COMMENT 改写；已交付环境提供 upgrade 迁移脚本（ALTER + CREATE，幂等）。SQLite 触发器维护 `updated_at` 的惯例照 `ai_cache_rules`。

### 3.2 DAO 与 storager

- `table_ai_cache_rules.go`：CRUD 带新列（默认值填充在 model 层，同 phase-1 惯例）；
- 新 `table_ai_cache_semantic_settings.go` + `storage/rdb/ai_cache/settings_storager.go`：
  - `Get(ctx) (*SettingsRow, error)`——空表返回 `(nil, nil)`（**非 error**），由 model 层转默认值对象；
  - `Upsert(ctx, row) error`——单行表 `delete-all + insert` 或 `INSERT ... ON DUPLICATE KEY UPDATE`，单事务；
  - storager 接口 + fake 进 `mocks_test.go`（callback 模式，照 phase-1）。

## 4. 导出契约（冻结，已与 BFE 逐字段核对）

### 4.1 文件结构

```json
{
  "Version": "<时间戳 20060102150405>",
  "Semantic": { "topK": 1, "threshold": 0.15, "thresholdRelation": "lt" },
  "Config": { "<product>": [ /* 规则数组，元素见 4.2 */ ] }
}
```

- `Version`/`Config` 首字母大写为 conf-agent 硬契约（phase-1 冻结，不变）；
- `Semantic` 为顶层并列键，**恒导出**（键顺序建议 `Version` → `Semantic` → `Config`，JSON 对象键序对两侧解析均无影响，仅为可读性）；
- BFE 旧版加载器忽略未知顶层字段，向后兼容。

### 4.2 字段 tag 全表

**顶层 `Semantic` 块**（对应 BFE `SemanticConfFile`）：

| 导出 tag | 类型 | 默认 | BFE 校验 | 说明 |
|----------|------|------|----------|------|
| `topK` | int | 1 | 1–10 | 向量检索近邻个数 |
| `threshold` | float64 | 0.15 | 0–2 | 相似度阈值（量纲随 relation） |
| `thresholdRelation` | string | `lt` | lt/lte/gt/gte | distance 语义越小越相似，similarity 语义越大越相似 |

**规则元素新增**（对应 BFE `ProductRuleConfFile`）：

| 导出 tag | 类型 | 默认 | BFE 行为 |
|----------|------|------|----------|
| `enableSemanticCache` | bool | false | 规则开关；`cacheKeyStrategy=disabled` 时忽略（不报错） |

其余规则字段（`cond`/`cacheKeyStrategy`/`cacheTTL`/`maxBodyBytes`/`maxValueBytes` 等）与 phase-1 §4.2 冻结表一致，未动。

### 4.3 控制面校验与 BFE 校验的对应

| 项 | 控制面（本仓） | BFE | 关系 |
|----|----------------|-----|------|
| `top_k` 范围 | 1–10，422 | 1–10，规则加载失败 | 控制面前置拦截 |
| `threshold` 范围 | 0–2，422 | 0–2 | 同上 |
| `threshold_relation` 枚举 | 四值，422 | 四值，加载失败 | 同上 |
| `enable_semantic_cache` | bool 类型 | disabled 规则忽略 | 组合不报错，两侧一致 |

### 4.4 Generator 逻辑（`AICacheRuleGenerator` 扩展）

```go
func (m *AICacheManager) AICacheRuleGenerator(ctx context.Context) (*iversion_control.ExportData, error) {
    rules := 查全部规则(id 升序, phase-1 逻辑不变)
    semantic := m.settingsStorager.Get(ctx)
    if semantic == nil {
        semantic = 默认值行{TopK: 1, Threshold: 0.15, ThresholdRelation: "lt"}
    }

    data := &ExportData{
        Version: 时间戳,
        Semantic: &SemanticConf{           // 新增：恒导出
            TopK:              semantic.TopK,
            Threshold:         semantic.Threshold,
            ThresholdRelation: semantic.ThresholdRelation,
        },
        Config: map[product][]RuleConf{
            productName: 转换(rules, 每条带 EnableSemanticCache),
        },
    }
    return data, nil
}
```

要点：

- **设置变更驱动版本流**：`Semantic` 进入 MD5 签名内容，改设置 → 新签名 → 新版本（复用 phase-1 `ExportConfig` 机制，零框架改动）；
- 导出结构体（`ExportData` 或同级）需增加 `Semantic *SemanticConf \`json:"Semantic"\`` 字段；
- 产品名取自 `stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（phase-1 决策不变）。

## 5. model/ai_cache 设计（增量）

### 5.1 文件构成（在 phase-1 文件上扩展）

| 文件 | 增量 |
|------|------|
| `ai_cache.go` | 新增 `SemanticSettingsRow`（存储结构）+ `SemanticConf`（导出结构）+ storager 接口方法 |
| `ai_cache_manager.go` | 新增 `GetSemanticSettings`（空表→默认值对象）/`SetSemanticSettings`（upsert 事务 + 审计）；`AICacheRuleGenerator` 合并 `Semantic`（§4.4） |
| `operation_log.go` | 复用 phase-1（settings PUT 记一条 update 审计，before 为空表时取默认值快照） |
| `mocks_test.go` | fake storager 补 settings 方法 |

### 5.2 manager 方法草图

```go
func (m *AICacheManager) GetSemanticSettings(ctx context.Context) (*shared.AICacheSemanticSettingsParam, error)
func (m *AICacheManager) SetSemanticSettings(ctx context.Context, param *shared.AICacheSemanticSettingsParam) (*shared.AICacheSemanticSettingsParam, error)
// Generator 合并见 §4.4
```

默认值常量集中定义于 `model/ai_cache/`（`DefaultSemanticTopK=1` 等），与 BFE `setDefaults` 数值一一对应，注释标注两侧同步义务。

## 6. 参数校验（`lib/validate`）

- `AICacheRules(param)`：元素增加 `enable_semantic_cache`（bool 指针，nil→false；无需范围校验）；其余 phase-1 校验不变；
- 新增 `AICacheSemanticSettings(param *shared.AICacheSemanticSettingsParam)`：
  - `TopK`：nil→默认 1；否则 ∈ [1,10]；
  - `Threshold`：nil→默认 0.15；否则 ∈ [0,2]；
  - `ThresholdRelation`：nil→默认 `lt`；否则 ∈ {lt,lte,gt,gte}（大小写敏感，与 BFE 一致）。

校验入口：settings PUT 绑参后全量校验，失败 422 + 失败审计。

## 7. 容器装配

- `stateful/container/rdb/components.go`：ai_cache 条目扩展 settings storager 声明与注入；
- `stateful/container/components.go`：`AICacheManager` 构造参数增加 settings storager（或按 phase-1 模式从容器取值），不改其他组件。

## 8. 测试计划

### 8.1 单元测试（硬门禁：model 层覆盖率 ≥ 70%）

| 位置 | 用例 |
|------|------|
| `model/ai_cache/ai_cache_manager_test.go` | 生成器：`Semantic` 恒存在、tag 逐字一致（marshal 断言 key 集合）、空表默认值、配置值透传、设置变更→版本/MD5 变化；`GetSemanticSettings` 空表默认值；`SetSemanticSettings` 空表插入/存在更新；审计 before/after |
| `endpoints/openapi_v1/ai_cache/` | settings GET/PUT 绑参、422 边界（0/2/10/11、非法枚举）、空表往返、rules 开关字段往返、响应不含内部 id |
| `lib/validate` | 默认值填充（全 nil）、范围边界、枚举大小写 |
| `storage/rdb/ai_cache/`（如按 phase-1 无独立测试则随 model） | settings Get 空表 (nil,nil)、Upsert 两次后单行 |

### 8.2 集成测试（`test/integration/tests/`）

1. PUT rules（含 `enable_semantic_cache=true`）→ GET 回读一致；
2. GET settings → 默认值；PUT 自定义值 → GET 回读；
3. Inner 导出：响应含 `Semantic` 块与开关字段（tag 断言）；改设置再导出 → 版本变化；清空 rules → `Semantic` 块仍在。

### 8.3 回归

- phase-1 全量单测/集成测试不回归（rules 无开关字段的旧请求体仍可 PUT——新字段可选）；
- 旧导出消费方（SC20 场景）不受影响：旧 BFE 忽略顶层 `Semantic`。

## 9. 风险与回滚

| 风险 | 应对 |
|------|------|
| 两侧默认值漂移（控制面/BFE 各有一份默认） | 常量注释互标同步义务；集成测试断言导出值；BFE `setDefaults` 为最终兜底 |
| 设置行被误删/多行 | 单行表约束（应用层 delete-all+insert）；Get 取 `id` 最小行并记 WARN 多行异常 |
| 老环境未跑迁移脚本 | 加列/建表失败在启动 DAO 初始化时显性报错（fail-fast，控制面语义） |
| 回滚 | 控制面回滚 = 还原迁移 + 回退代码；BFE 侧旧加载器忽略 `Semantic`/`enableSemanticCache`，可独立回滚 |

## 10. WBS

| 编号 | 任务 | 产出 | 预估 |
|------|------|------|------|
| C1 | DDL 迁移（两份 DDL + upgrade 脚本） | §3.1 | 0.5 天 |
| C2 | storage 层（规则加列 + settings storager） | §3.2 | 0.5 天 |
| C3 | model 层（shared 参数 + settings 读写 + 生成器合并 + mocks + 审计） | §4.4/§5 | 1.5 天 |
| C4 | validate + Open API（settings 两端点 + rules 字段） | §6 + api-changes.md §3 | 1 天 |
| C5 | 测试与门禁（单测 ≥70% + 集成用例） | §8 | 1 天 |
| C6 | 联调（对照 BFE 已实现加载器做端到端导出验证） | SC20/SC23 链路 | 0.5 天 |

合计约 **5 个开发日**。

## 11. 待决策点（含建议）

| # | 问题 | 建议 | 影响 |
|---|------|------|------|
| 1 | `Semantic` 块是否恒导出（vs 仅当有规则开启语义时导出） | **恒导出**（本文默认）：生成器无分支、设置与规则解耦、块体极小 | 生成器逻辑 |
| 2 | 设置行是否预置默认行（vs 空表=默认） | **空表=默认**（本文默认）：单一真相在常量 | DAO/Get 语义 |
| 3 | settings 端点是否并入 `/ai-cache-rules` 子路径（如 `/ai-cache-rules/semantic-settings`） | 顶级单例路径（本文默认），与 rules 平级、URL 更短 | 路由与权限映射（无实质差异） |
| 4 | dashboard 是否本期同步 | 二期（同 phase-1 决策），Open API 先行 | 范围 |
