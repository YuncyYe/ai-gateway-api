# API-Key / Entity 内联限流策略补齐 batch_limits——变更摘要

## 1. 背景

2026-10-07 批量与异步任务支持（一期，见
`design-docs/modifications/2026-10-07-ai-batch-support/`）把批量限流维度
`batch_limits` 定义在共享 `RateLimitPolicy` 公共类型
（`design-docs/api-define/OpenAPI接口定义/00-common.md` §9）与独立策略资源
`/rate-limit-policies`（`rate-limit-policy.md`）中，导出为 BFE
`ai_rate_limit.data` 策略 `rules.batch` 段。

实现侧，API-Key 与 Entity 的内联 `rate_limit_policy` 使用同一
`shared.RateLimitPolicyParam` 结构：`endpoints/openapi_v1/api_key/checker.go`
与 `endpoints/openapi_v1/entity/validator.go` 均以 `validate.RateLimitPolicy`
校验（含 `batch_limits` 维度与"至少一个维度"约束），内联策略落库到
`rate_limit_policies` 行（`storage/rdb/rate_limit_policy` 序列化
`batch_limits` 列），行为与独立策略资源完全一致。

但接口定义文档 `api-keys.md` / `entities.md` 的 `rate_limit_policy` 示例与
约束条款未包含 `batch_limits`，读者无法从内联策略入口发现该能力；
`00-common.md` §9 的 `rules` 必填口径仍写"三者至少配置其一"，未把
`batch_limits` 计入。本次补齐接口定义，运行行为不变。

## 2. 目标

1. `api-keys.md` / `entities.md`：内联 `rate_limit_policy.rules` 示例补充
   `batch_limits` 段；约束条款补充批量限流口径（按 API-Key 计数、省略/null
   语义、全局硬顶兜底）。
2. `00-common.md` §9：`rules` 必填口径由"三者至少配置其一"更正为"四者"
   （`tpm` / `rpm` / `max_concurrency(>=0)` / `batch_limits`）。
3. 不改动实现、DDL、导出格式与数据面行为。

## 3. 范围

- **涉及面**：`design-docs/api-define/` 文档（`api-keys.md`、`entities.md`、
  `00-common.md`）。
- **不涉及面**：控制面代码（能力已在共享类型与校验/存储路径中）、BFE 数据面、
  ai-gateway-web（批量限流控制台表单另行跟进）。
- **兼容性**：请求体向后兼容——旧客户端不传 `batch_limits` 行为不变；新客户端
  按公共类型传入即可生效。

## 4. 关键口径（与实现一致）

| 口径 | 说明 |
|------|------|
| 配置入口 | 独立策略资源 `/rate-limit-policies` 与 API-Key / Entity 内联 `rate_limit_policy.rules.batch_limits` 等价（同一 `RateLimitPolicy` 类型、同一 `validate.RateLimitPolicy` 校验、同一落库路径） |
| 计数维度 | 四维度均按发起请求的 API-Key 计（`max_active_batches` 比较该 `ClientKeyId` 的活跃批量任务数），与 `model` 无关（批量创建请求体无 model 字段） |
| 层级合并 | API-Key 与各层 Entity 的内联/引用策略都参与检查，同一维度按 AND 生效（等价取最小值）；文件字节/行数再与 `mod_ai_batch.data` 全局硬顶取最小值 |
| 省略/null | 该策略不参与批量限流；不配置不代表批量不限流，仍受全局硬顶约束 |
| 校验 | 各维度 `>= 0`；`enabled=true` 时 `tpm` / `rpm` / `max_concurrency(>=0)` / `batch_limits` 至少配置其一 |

## 5. 影响

- 无 DDL、无代码、无数据面变更；`rainway-book` 第十二章/第二十二章/第二十三章
  已同步说明该入口（内联策略与独立策略资源均支持）。
- 控制台（ai-gateway-web）限流表单暂未提供 `batch_limits` 输入，本次仅
  补齐 API 契约；表单支持另行安排。

## 6. 测试口径

本次为文档补齐、运行行为不变，实现侧已有覆盖：

- **API-Key 内联路径（已有）**：`test/integration/tests/api_key/batch_limits/batch_limits_test.go`
  —— AK-BL-1-001 创建内嵌 `batch_limits` 四维度成功并回读一致；
  AK-BL-1-002 负维度值返回 422；AK-BL-1-003 InnerAPI 导出 `rules.batch`
  段与 `redis_key`；AK-BL-1-004 不带 `batch_limits` 的策略无 batch 段；
  AK-BL-1-005 `max_create_rpm=0` 不生成 redis_key。
- **模型/共享层（已有）**：`model/rate_limit_policy/rate_limit_policy_batch_test.go`
  （模型层落库/导出）、`model/shared/rate_limit_redis_key_test.go`
  （含 `BatchLimits` 的 Redis Key 构建与清理）。

随本次变更补充（已实现）：

- **Entity 内联路径用例族**：`test/integration/tests/entity/batch_limits/batch_limits_test.go`
  （编号 ET-BL-1-001 起，与 AK-BL 对称）：ET-BL-1-001 创建内嵌 `batch_limits`
  四维度成功并回读一致；ET-BL-1-002 负维度值 422；ET-BL-1-003 InnerAPI 导出
  `rules.batch` 段与 redis_key（经挂载 API-Key 的层级绑定定位）；ET-BL-1-004
  不带 `batch_limits` 的策略无 batch 段；ET-BL-1-005 `max_create_rpm=0` 不生成
  redis_key；ET-BL-1-006 PATCH 整对象替换语义（省略 `batch_limits` 即清空该段）。
- **端点级单测**：`endpoints/openapi_v1/api_key/checker_test.go`（create/update
  各三例）与 `endpoints/openapi_v1/entity/validator_test.go`（三例）补充
  `batch_limits` 校验用例（合法四维度、`enabled=true` 仅配 batch_limits 合法、
  负维度值 422）。
- 已通过：相关包 `go build`/`go test`；SQLite 后端的集成用例本地执行通过
  （MySQL 后端由 CI 覆盖）。
- 尚未落地：ai-gateway-web 批量限流表单（另行安排）。
