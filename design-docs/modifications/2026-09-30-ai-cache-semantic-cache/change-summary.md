# ai-cache 语义缓存：控制面扩展（规则开关 + 语义全局设置）—— 变更摘要

## 1. 背景

BFE 数据面已完成 `mod_ai_cache` 二期语义缓存（混合模式：Redis 精确优先 → embedding → Chroma 向量检索 → 阈值判定；响应双写回），修改方案见 BFE 仓 `docs/zh_cn/modifications/2026-09-30-ai-cache-semantic-cache/design-changes.md`（已随数据面代码合入）。

数据面消费的控制面产物有两处变化，需要 ai-gateway-api 配合：

1. **规则级开关**：`ai_cache.data` 每条规则新增可选字段 `enableSemanticCache`（规则级语义缓存开关，按路由灰度用）；
2. **顶层全局块**：`ai_cache.data` 新增顶层 `Semantic` 块（`topK`/`threshold`/`thresholdRelation`，模块级全局调优参数，热加载）——**必须并入既有 `ai_cache.data` 文件顶层**，不能走独立 topic 文件（BFE 规则加载器只读这一个文件）。

本次改动沿用一期导出框架（`topic + 生成器 + MD5 签名 + config_versions + Inner API 轮询`），仍是**登记式**：不改框架本身。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-09-30 |
| 涉及仓库 | `ai-gateway-api` |
| 变更类型 | 既有集合资源 `ai_cache_rules` 增量字段 + 新增单例资源 `ai_cache_semantic_settings`（GET/PUT 全量读写）+ 既有导出端点契约扩展（顶层 `Semantic` 块） |
| 产出 | 1 张新表 + 1 次加列迁移 + DAO/storager 扩展 + model 扩展（读写/导出/审计）+ Open API 2 个新端点 + 测试 + 设计文档 |
| 预估工作量 | 4.5-5 个开发日（含测试与设计文档） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 语义调优参数的存放形态 | **模块级单例设置**（单行表 `ai_cache_semantic_settings`），不是规则字段 | 三参数描述向量检索与判定的全局特性，部署内一份即可；评审明确规则级重复配置属过度设计（见 BFE 侧方案 4.2 节设计取舍） |
| 2 | 设置如何到达 BFE | **`AICacheRuleGenerator` 合并导出**：读规则表 + 设置行，组装为 `ai_cache.data` 顶层 `Semantic` 块 | BFE 规则加载器只读 `ai_cache.data` 一个文件；**放弃一期方案中"沿用 `model/imods` 模块配置导出模式"的建议**——imods 产出独立 topic 文件（如 `mod_body_process.data`），不满足"并入同一文件顶层"的硬约束 |
| 3 | 加列 vs 新表承载规则开关 | `ai_cache_rules` **直接加列** `enable_semantic_cache`（TINYINT，默认 0） | 开关语义归属规则（按路由灰度），随规则整组替换维护 |
| 4 | `Semantic` 块导出时机 | **恒导出**（设置行不存在时导 BFE 同款默认值 `1/0.15/lt`） | 块体 3 个字段体量极小；恒导出使生成器逻辑无分支、设置与规则生命周期解耦；旧版 BFE 加载器忽略未知顶层字段，向后兼容 |
| 5 | Inner API 形态 | **不变**（仍 `GET /inner-api/v1/configs/ai-cache-rule`），仅响应多顶层 `Semantic` 键 | `Version`/`Config` 首字母大写契约不动；conf-agent 零改动；MD5 签名覆盖全量生成内容，设置变更自然产生新版本 |
| 6 | 权限点 | **复用 `FeatureAICache`**，不新增 Feature | 设置是 ai-cache 域的子资源；phase-1 的 scope 映射直接生效 |
| 7 | dashboard | 二期，本期 Open API 先行 | 范围控制（同 phase-1 决策 7） |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `db_ddl.sql`/`db_ddl_sqlite.sql` 的 `ai_cache_semantic_settings` 表；`ai_cache_rules` 加列迁移；`storage/rdb/ai_cache/` 设置 storager；`model/ai_cache/` settings 读写 + 生成器合并；`endpoints/openapi_v1/ai_cache/` 新增 settings 两端点 |
| 主要修改 | `model/shared/types.go`（`AICacheRuleParam` 加 `EnableSemanticCache` + 新增 `AICacheSemanticSettingsParam`）；`lib/validate/validate.go`（规则新字段 + `AICacheSemanticSettings` 校验）；`AICacheRuleGenerator`（合并 Semantic 块）；DAO 加列；装配 |
| 明确不动 | 导出框架（`model/iversion_control`）、conf-agent（零代码改动）、既有 Open API 端点形态（GET/PUT 集合级不变）、`model/imods/` 既有 exporter、权限 Feature 定义 |
| 接口契约 | Open API 新增 2 个端点（settings GET/PUT）；既有 rules GET/PUT 元素新增可选字段；Inner API 导出响应新增顶层 `Semantic` 键（冻结契约见 design-changes.md §4） |
| 数据迁移 | 加列 `enable_semantic_cache`（默认 0，存量规则语义关闭）+ 新建空设置表（空表 = 默认值语义） |

## 5. 非本仓登记点（链路协同）

| 位置 | 改动 | 状态 |
|------|------|------|
| BFE 数据面 `mod_ai_cache` | 语义缓存模块实现 + 规则加载器（顶层 `Semantic` 块 + `enableSemanticCache`）+ `[embedding]`/`[vector]` 配置 | **已完成**（commit `94d5e935`，已推 origin `v1.8.9-dev`） |
| `bfe-access-pb` | 访问日志 `ai_cache_semantic`/`ai_cache_similarity`（791/792） | **已完成**（tag `v0.3.10`，go.mod 已引用） |
| `conf-agent` | 零改动（`[Reloaders.mod_ai_cache]` phase-1 已配，`CopyFiles` 含 `ai_cache.data`） | 无需动作 |
| ai-gateway-web（dashboard） | 语义开关勾选 + 设置表单 | 二期 |

## 6. 文档配套

- `api-changes.md`：rules 元素新字段 + `/ai-cache-semantic-settings` GET/PUT 契约 + Inner API 导出响应变更；
- `design-changes.md`：表结构与迁移、导出契约冻结表（含顶层 `Semantic` 块 tag 全表）、生成器合并逻辑、校验、装配、测试计划、WBS、待决策点。
