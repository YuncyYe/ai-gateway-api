# mod_ai_context 上下文压缩与裁剪 —— 控制面设计变更说明

> 数据面（BFE）侧规格已冻结并实现，见 BFE 仓 `docs/zh_cn/modifications/2026-10-01-ai-context-compress/design-changes.md`（commit `bf441cb5`，含加载器契约与 SC24 集成测试）；本文档只覆盖 ai-gateway-api 控制面。

## 1. 概述

### 1.1 变更背景

BFE `mod_ai_context` 一期已入库。数据面规则加载器（`context_rule_load.go`）的新契约：

1. 规则数据文件 `context_rule.data` 为**顶层 `Defaults` 块 + `Config` map[product]规则数组**两段结构；
2. 规则元素契约：`cond`/`mode` 必填（mode 缺失或非法、cond 编译失败 → 整文件拒载），`maxContextTokens`/`reserveTokens` 可选；
3. **前向兼容行为**：对规则条目及 `Defaults` 中的未知字段忽略并计数告警（`CTX_CFG_UNKNOWN_FIELD`），不拒载——二期字段（规则级 `override`、Defaults 内 `summary`）届时下发到旧数据面安全降级，机制已验证；
4. 预算窗口数据源：一期数据面用**模型名启发式**（claude 200k / gemini 1M / codex 400k）+ **128k 兜底**（已实现并经 SC24 验证）；`ModelTable` 精确 `ContextWindow` 挂点届时对接（见 §11）。

### 1.2 变更目标

1. 新增集合资源 `ai_context_rules`（Open API GET/PUT 全量读写）；
2. 新增单例设置资源 `ai_context_settings`（GET 默认值兜底 / PUT upsert）；
3. 新增 `ContextRuleGenerator` 导出 `context_rule.data`（`Defaults` 块恒导出），topic `mod_ai_context`；
4. 与 BFE 已实现的加载器逐字段对齐（tag、默认值、量纲语义），两侧契约冻结。

### 1.3 设计原则

- **登记式扩展**：不改导出框架、不动 conf-agent、不加新导出机制；
- **照 ai-cache 双先例**：集合资源照 `ai_cache_rules`（phase-1）、单例设置照 `ai_cache_semantic_settings`（二期），评审与测试路径完全复用；
- **默认值下沉对齐**：控制面默认值常量与 BFE `setDefaults` 完全一致（0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95），空表即默认；
- **二期字段一期不预留**：一期 API 与表只含一期字段（`override`/`summary` 不在其中）；phase-2 以标准 additive 变更引入（Open API 加可选字段 + 表加可空 JSON 列），BFE 未知字段容忍保证升级平滑——与数据面"一期契约只定义一期消费字段"的原则对称。

## 2. 资源模型

### 2.1 集合资源 `ai_context_rules`

- 元素：`cond`（Y）/ `mode`（Y，四枚举）/ `max_context_tokens`（N）/ `reserve_tokens`（N）；
- 形态、优先级语义（数组顺序 = id 升序 = first-match-wins）、无 `enabled`、无 `/{id}`，全部照 `ai-cache-rules` phase-1 决策；
- `mode` 与 ai-cache 的 `cacheKeyStrategy` 不同：**必填**——BFE 对缺失 mode 拒载整个文件，控制面必须在入口拦截（422）。

### 2.2 单例资源 `ai_context_settings`

| 维度 | 说明 |
|------|------|
| 形态 | 全系统单例（单行表），GET 读全量 + PUT 全量写（upsert） |
| 生命周期 | 独立于规则集合：rules 清空后 Defaults 块仍导出 |
| 寻址 | 无 id/name；API 路径即身份（`/ai-context-settings`） |
| 空表语义 | GET 返默认值对象；导出用默认值——不显式插入默认行，避免双真相 |

### 2.3 精确 `context_window`（一期范围外）

一期**不下发**模型上下文窗口：数据面启发式 + 兜底已够用（窗口误差仅影响触发时机，全程 fail-open），且 `model_prices` 是定价表、能力规格属职责错位。二期随"模型能力域"统一设计（候选：`model_prices.limits` JSON 透传、模型注册域），届时 InnerAPI 填充 `AIConf.ModelTable.Models[].ContextWindow`，BFE 挂点对接。本期零改动（见 §11 待决策点 3）。

## 3. 数据模型

### 3.1 迁移 DDL（`db_ddl.sql`）

```sql
-- 上下文压缩规则集合
CREATE TABLE `ai_context_rules` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（数组顺序=id 升序=优先级）',
  `cond` VARCHAR(1024) NOT NULL COMMENT 'bfe 条件表达式',
  `mode` VARCHAR(16) NOT NULL COMMENT '压缩档位: off/conservative/balanced/aggressive',
  `max_context_tokens` INT NOT NULL DEFAULT 0 COMMENT '预算上限覆盖(token), 0=用模型表窗口',
  `reserve_tokens` INT NOT NULL DEFAULT 0 COMMENT '预留输出token, 0=自动clamp(窗口x15%,256,16000)',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI上下文压缩规则表';

-- 上下文压缩全局调优设置（单行表；空表 = 默认值）
CREATE TABLE `ai_context_settings` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（恒为1语义，物理单行）',
  `trigger_ratio` DOUBLE NOT NULL DEFAULT 0.7 COMMENT 'proactive触发阈值(占预算比例, 0-1]',
  `keep_latest_images` INT NOT NULL DEFAULT 2 COMMENT '保留最近N张内联图片, 0=不裁图',
  `tool_result_max_chars` INT NOT NULL DEFAULT 2000 COMMENT '单条tool结果最大字符数, 0=不截断',
  `thinking_policy` VARCHAR(32) NOT NULL DEFAULT 'trim-all-but-last' COMMENT 'thinking块策略: trim-all-but-last/keep',
  `chars_per_token` INT NOT NULL DEFAULT 4 COMMENT '文本估算系数(字节/token), 中文密集可调3',
  `image_token_estimate` INT NOT NULL DEFAULT 1200 COMMENT '单张内联图片估值token',
  `rewrite_strength` VARCHAR(8) NOT NULL DEFAULT 'lite' COMMENT '改写强度: lite/full',
  `rewrite_protected_survival_rate` DOUBLE NOT NULL DEFAULT 0.95 COMMENT 'fidelity gate保护token存活率阈值(0-1]',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI上下文压缩全局设置表（单行）';
```

`db_ddl_sqlite.sql` 按惯例去 COMMENT 改写；已交付环境提供 upgrade 迁移脚本（2 CREATE，幂等）。SQLite 触发器维护 `updated_at` 的惯例照 `ai_cache_rules`。**本期无任何既有表变更**。

### 3.2 DAO 与 storager

- `table_ai_context_rules.go` / `table_ai_context_settings.go` + `storage/rdb/ai_context/`：
  - rules：照 ai-cache 的集合 CRUD（delete-all + insert-all 单事务）；
  - settings：`Get` 空表返回 `(nil, nil)`（非 error），manager 层转默认值对象；`Upsert` 单行 `delete-all + insert`，单事务；
- storager 接口 + fake 进 `mocks_test.go`（callback 模式）。

## 4. 导出契约（冻结，已与 BFE 逐字段核对）

### 4.1 文件结构

```json
{
  "Version": "<时间戳 20060102150405>",
  "Defaults": { /* §4.2，恒导出 */ },
  "Config": { "<product>": [ /* §4.3 规则数组 */ ] }
}
```

- `Version`/`Config` 首字母大写为 conf-agent 硬契约（照 ai-cache 惯例）；`Defaults` 为顶层并列键，恒导出；
- 二期字段（`summary`/`override`）一期不出现在导出中；phase-2 引入后由生成器原样透传，当前 BFE 对未知字段忽略并计数告警。

### 4.2 顶层 `Defaults` 块字段全表（对应 BFE `DefaultsConfFile`）

| 导出 tag | 类型 | 默认 | BFE 校验 | 说明 |
|----------|------|------|----------|------|
| `triggerRatio` | float64 | 0.7 | (0,1] | proactive 触发阈值 |
| `keepLatestImages` | int | 2 | ≥0 | 保留最近 N 张内联图片，0=不裁图 |
| `toolResultMaxChars` | int | 2000 | ≥0 | 单条 tool 结果截断长度，0=不截断 |
| `thinkingPolicy` | string | trim-all-but-last | 枚举 | thinking 块策略 |
| `charsPerToken` | int | 4 | ≥1 | 文本估算系数 |
| `imageTokenEstimate` | int | 1200 | ≥0 | 单张内联图片估值 token |
| `rewrite` | object | — | — | 子对象，见下两行 |
| `rewrite.strength` | string | lite | lite/full | 改写强度 |
| `rewrite.protectedSurvivalRate` | float64 | 0.95 | (0,1] | fidelity gate 存活率阈值 |

### 4.3 规则元素字段全表（对应 BFE `ContextRuleConfFile`）

| 导出 tag | 类型 | 默认 | BFE 校验 | 说明 |
|----------|------|------|----------|------|
| `cond` | string | — | 必填，编译失败拒载 | bfe 条件表达式 |
| `mode` | string | — | 必填，四枚举 | off/conservative/balanced/aggressive |
| `maxContextTokens` | int | 0 | ≥0 | 0 = 用模型表窗口（一期模型表无窗口数据 → 启发式兜底） |
| `reserveTokens` | int | 0 | ≥0 | 0 = 自动 clamp(窗口×15%, 256, 16000) |

### 4.4 控制面校验与 BFE 校验的对应

| 项 | 控制面（本仓） | BFE | 关系 |
|----|----------------|-----|------|
| `mode` | 必填 + 四枚举，422 | 缺失/非法拒载整文件 | 控制面前置拦截 |
| `trigger_ratio` | (0,1]，422 | 同左，加载失败 | 同上 |
| 其余数值范围 | 与 §4.2/§4.3 一致，422 | 同左 | 同上 |

### 4.5 Generator 逻辑（`ContextRuleGenerator`）

```go
func (m *AIContextManager) ContextRuleGenerator(ctx context.Context) (*iversion_control.ExportData, error) {
    rules  := 查全部规则(id 升序)
    settings := m.settingsStorager.Get(ctx)
    if settings == nil {
        settings = 默认值行 // 常量与 BFE setDefaults 一一对应
    }
    data := &ExportData{
        Version: 时间戳,
        Defaults: 转换(settings),          // 恒导出，含 rewrite 子对象
        Config: map[product][]RuleConf{productName: 转换(rules)},
    }
    return data, nil
}
```

要点：`Defaults` 进入 MD5 签名内容（改设置 → 新版本）；topic 常量 `ConfigTopicProductAIContext = "mod_ai_context"` 定义在 `model/ai_context/`；产品名取 `AIRouteInnerProductName`（照 ai-cache）。

## 5. model/ai_context 设计

| 文件 | 内容 |
|------|------|
| `ai_context.go` | `ContextRuleRow`/`SettingsRow`（存储结构）+ `DefaultsConf`/`RuleConf`（导出结构）+ storager 接口 |
| `ai_context_manager.go` | `GetRules`/`SetRules`（集合全量替换 + 审计）；`GetSettings`（空表→默认值对象）/`SetSettings`（upsert + 审计）；`ContextRuleGenerator`（§4.5） |
| `operation_log.go` | 复用 ai-cache 模式（rules/settings 各记 update 审计，before 为空表/空集合时取默认值/空快照） |
| `mocks_test.go` | fake storager（callback 模式） |

默认值常量集中定义（`DefaultTriggerRatio=0.7` 等），注释标注与 BFE `setDefaults` 两侧同步义务。

## 6. 参数校验（`lib/validate`）

- `AIContextRules(param)`：元素 `mode` 必填四枚举、`cond` 非空 + `ConditionExpression` 编译校验、数值 ≥0（可空指针 nil→默认）；
- `AIContextSettings(param)`：逐项 nil→默认 + 范围/枚举校验（§4.4）；
- 校验入口：rules/settings PUT 绑参后全量校验，失败 422 + 失败审计。

## 7. 容器装配

- `stateful/container/rdb/components.go`：ai_context 条目 storager 声明与注入；
- `stateful/container/components.go`：`AIContextManager` 构造注入；
- `model/iauth/features.go`：`FeatureAIContext` + scope 映射（read/update/export）。

## 8. 测试计划

### 8.1 单元测试（model 层覆盖率 ≥ 70% 硬门禁）

| 位置 | 用例 |
|------|------|
| `model/ai_context/` | 生成器：`Defaults` 恒存在、tag 逐字一致（marshal 断言 key 集合）、空表默认值、设置变更→MD5/版本变化；rules 全量替换/空集合导出空数组；settings 空表默认值/upsert 往返；审计 before/after |
| `endpoints/openapi_v1/ai_context/` | rules/settings 绑参、422 边界（mode 缺失/非法枚举、trigger_ratio 越界）、空表往返、响应不含内部 id |
| `lib/validate` | 默认值填充（全 nil）、范围边界、枚举 |

### 8.2 集成测试（`test/integration/tests/`）

1. PUT rules（含 mode=balanced）→ GET 回读一致；
2. GET settings → 默认值；PUT 自定义值 → GET 回读；
3. Inner 导出：响应含 `Defaults` 块与规则字段（tag 断言）；改设置再导出 → 版本变化；清空 rules → `Defaults` 块仍在。

### 8.3 回归

- ai-cache 域全量测试不回归；
- 旧导出消费方不受影响：一期导出不含 `override`/`summary`；phase-2 引入后旧 BFE 忽略这两个键（SC24 TC 已覆盖该前向兼容行为）。

## 9. 风险与回滚

| 风险 | 应对 |
|------|------|
| 两侧默认值漂移 | 常量注释互标同步义务；集成测试断言导出值；BFE `setDefaults` 为最终兜底 |
| 设置行多行/误删 | 单行表约束（delete-all+insert）；Get 取 id 最小行并记 WARN 多行异常 |
| 老环境未跑迁移 | 建表失败在 DAO 初始化时显性报错（fail-fast） |
| 回滚 | 控制面回滚 = 删 2 张新表 + 回退代码；BFE 侧模块卸载即恢复（conf 默认 mode=off），可独立回滚 |

## 10. WBS

| 编号 | 任务 | 产出 | 预估 |
|------|------|------|------|
| C1 | DDL 迁移（两份 DDL + upgrade 脚本） | §3.1 | 0.5 天 |
| C2 | storage 层（两表 DAO + settings storager） | §3.2 | 0.5 天 |
| C3 | model 层（shared 参数 + 读写 + 生成器 + mocks + 审计） | §4.5/§5 | 1.5 天 |
| C4 | validate + Open API（rules/settings 四端点） | §6 + api-changes.md | 1 天 |
| C5 | 测试与门禁（单测 ≥70% + 集成用例） | §8 | 1 天 |
| C6 | 联调（对照 BFE 已实现加载器做端到端导出验证 + conf-agent/k8s 登记点落地） | SC24 链路 | 0.5-1 天 |

合计约 **5 个开发日**。

## 11. 待决策点（含建议）

| # | 问题 | 建议 | 影响 |
|---|------|------|------|
| 1 | 资源命名：`/ai-context-rules` vs `/ai-context-compress-rules` | **`ai-context-rules`**（本文默认）：与 `ai-cache-rules`（mod_ai_cache）对称，模块名 mod_ai_context 去前缀即 ai-context | 路由、权限点命名 |
| 2 | 二期字段 `override`/`summary` 是否一期入 DDL | **不入**（本文默认）：一期 API 与表只含一期字段；phase-2 以 additive 变更（API 加可选字段 + 表加可空 JSON 列）引入，BFE 未知字段容忍保证平滑 | DDL 与 API 文档范围 |
| 3 | 精确 `context_window` 二期数据源 | **一期不下发、二期专题设计**（本文默认）：候选 `model_prices.limits` JSON 透传（零迁移）或模型能力注册域；`ModelTable.Models[].ContextWindow` 挂点届时对接 | 二期变更范围 |
| 4 | `cond` 是否控制面编译校验 | **编译校验**（照 ai-cache 同款 `ConditionExpression`，评审后对齐先例）：PUT 时即拦截非法表达式（422），优于 BFE 加载期整文件拒载；编译随 go.mod bfe 依赖升级识别新原语 | validate 范围 |
| 5 | dashboard 是否本期同步 | 二期（同 ai-cache 决策），Open API 先行 | 范围 |
