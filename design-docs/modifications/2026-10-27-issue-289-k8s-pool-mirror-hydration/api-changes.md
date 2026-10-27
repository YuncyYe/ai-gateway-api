# Issue #289：k8s_pool 镜像补齐——API/文档变更说明

## 1. 变更范围

| 接口 | 变更类型 | 说明 |
|------|----------|------|
| OpenAPI `/providers`（POST/PATCH/GET） | **契约不变，语义更新** | `k8s_instance_pool` 的维护时机补充“provider 创建/更新（引用建立/变化）”；请求/响应字段与校验规则不变 |
| InnerAPI `/k8s_pools`（PUT/GET/DELETE） | 结构不变，DELETE 语义变更 | 端点、鉴权、请求/响应结构不变；DELETE 改为引用感知（未引用删行；被引用 warning + 清空保留 + fan-out 清空） |
| OpenAPI `/clusters` | 不变 | 仅修复 PATCH 时派生池刷新数据源（`provider.InstancePool` → `EffectiveInstancePool`），接口契约不变 |
| DDL | 无 | `providers.k8s_instance_pool` 列保留 |

## 2. OpenAPI `/providers` 语义更新

### 2.1 镜像维护时机（文档表述更新）

`design-docs/api-define/OpenAPI接口定义/providers.md`：

- 字段表 `k8s_instance_pool` 行：说明更新为“系统在 provider 创建/更新（`instance_source`/`k8s_pool_name` 引用建立或变化、或显式重新提交池名）与 `/k8s_pools` PUT/DELETE 时同步刷新；本期无自动 backfill，镜像漂移后需重新 PUT pool 修复”；
- §2.1 创建执行逻辑：`instance_source=k8s_pool` 分支补充——创建时在同一事务内按 `k8s_pool_name` 读取 `k8s_pools` 写入镜像；池不存在则镜像为空数组；
- §2.4 更新语义补充：
  - `instance_source=k8s_pool` 且发生“来源切换 / `k8s_pool_name` 变化 / 显式重新提交 `k8s_pool_name`”时，系统重新读取 `k8s_pools` 刷新镜像并同步引用 cluster 派生池；
  - 刷新时 `k8s_pools` 读取失败导致本次 PATCH 失败（事务回滚）；池不存在按零实例处理；
  - 其余分支（模式切换校验、休眠 `instance_pool`、只读 422）不变；
- §校验规则汇总（`k8s_instance_pool` 只读条目）：同步更新维护时机描述。

### 2.2 `/clusters` 文档校准（clusters.md）

随本变更一并修正与有效池口径不一致的旧表述（接口契约不变）：

- 健康检查 `host` 缺省取值说明：`instance_pool` → **有效实例池**（k8s 模式为 `k8s_instance_pool` 镜像）；
- 创建集群执行逻辑第 4 步：“读取其 `instance_pool`” → “读取其**有效实例池**”；
- 更新集群说明：“请更新对应 provider 的 `instance_pool`” → 区分手工来源（更新 `instance_pool`）与 k8s 来源（由 `/k8s_pools` 维护）；补充“引用 provider 的 cluster 更新时按有效实例池刷新自动创建的实例池”。

### 2.3 不变的契约

- 请求/响应 JSON 结构、`k8s_instance_pool` 只读（携带 422）、`instance_pool` 休眠语义、池名校验、模式切换校验、`/k8s_pools` 只读视图——全部不变。

## 3. InnerAPI `/k8s_pools` 文档更新

`design-docs/api-define/InnerAPI接口定义/k8s-pools.md`：

- 开篇补充：provider 的池引用（`instance_source` / `k8s_pool_name`）由人经 OpenAPI `/providers` 维护；**引用建立/变化时系统会读取 `k8s_pools` 刷新 provider 镜像**；
- §5 删除 pool 改为**引用感知语义**（行为变更）：
  - 未被 provider 引用：删除 `k8s_pools` 中该 pool 条目；
  - 被引用：打印 warning 日志（正在删除被引用的 k8s pool，将清空实例池而不是删除条目，记录 pool 名与引用方数量）→ 清空该条目实例列表但**保留条目** → fan-out 将所有引用方 provider 的 `k8s_instance_pool` 置空并逐个同步其 cluster 派生池（清空）；
  - 被引用时 DELETE 成功后条目仍在，`GET` 返回 200 且 `instance_count=0`；
- §6 同步事务语义补充说明：
  - 镜像刷新有两个触发源：本域 PUT/DELETE 的 fan-out；以及 OpenAPI provider 创建/更新时的 hydration（同一事务内“读 `k8s_pools` → 写镜像 → hook 同步派生池”）；
  - DELETE 的第 1 步按引用与否分两种情形（删行 / 清空保留 + warning）；
  - 存量镜像漂移的修复方式：重新 PUT 一次 pool（本期无自动 backfill / 周期 reconcile）；
- 端点路径、请求/响应结构与错误码不变。

`design-docs/api-define/OpenAPI接口定义/k8s-pools.md`（#290 只读接口）：§3“与 provider 的关系”补充镜像同源与刷新时机说明（provider 创建/更新 hydration + 本域 fan-out；漂移后重新 PUT 修复）。

## 4. 数据面与联动

- BFE / conf-agent：零改动（`cluster_table` 格式与空条目语义不变）；
- ai-gateway-controller：零改动（继续 PUT `/k8s_pools`）；
- ai-gateway-web：零改动（详情页已按 `k8s_pool_name` 走 #290 只读接口展示实时实例）。
