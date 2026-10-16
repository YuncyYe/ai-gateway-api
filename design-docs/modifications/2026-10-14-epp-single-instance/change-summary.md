# EPP 单实例化——分配粒度改为 group、删除 primary/standby 变更摘要

## 1. 背景

EPP 调度体系当前设计（含 `2026-09-08-epp-scheduling-integration`、`2026-09-14-epp-pool-unify-group-size` 两次演进后的状态）：

- `/epp-pool` 每组允许 1~2 个实例（2 实例 = 主 + 备）；
- `/epp-assignments` 视图中每个 cluster 展开为 `primary` + `standby` 两字段（standby 为读时展开）；PUT 覆写需指定 `group_name` + `primary_instance_id`；
- 存储 `epp_assignments` 记录 `{cluster, group_name, primary_instance_id}`；
- InnerAPI `epp_data` 导出 `assignment[cluster] = {primary, standby}`；`server_data_conf` 的 `EPPAddr` 为 `[主, 备]` 有序列表。

经调研确认该主备设计有问题：当前阶段 EPP 以单实例部署，主备语义带来双倍配置面、读时展开的隐式逻辑（`expandPrimaryAndStandby`）、以及跨组件（BFE failover、EPP 角色状态机）的复杂联动，收益不抵成本。

## 2. 目标

1. `/epp-pool`：保留 `groups[].instances` 数组形态（为未来扩容预留），组规模校验收紧为**每组恰好 1 个实例**（拒绝空组与 ≥2 实例的组）。
2. `/epp-assignments`：**分配粒度收敛为 group**——视图元素只保留 `{cluster, group}`（`primary`/`standby`/`instances`/`degraded` 字段全部删除，实例详情由 `/epp-pool` 按组名派生，未分配/分配悬空统一由 `unassigned_clusters` 枚举）；PUT 请求体只剩 `{"group_name"}`。
3. **彻底删除系统中的 primary/standby 概念**：InnerAPI `epp_data` 的 assignment 条目改为 `{group: "g1"}`（cluster→group 映射）；`EPPAddr` 恒为单元素；模型层读时展开逻辑删除。
4. 存储层：`epp_assignments` 表**删除 `primary_instance_id` 列**（schema 变更，升级语句追加至 `database/db_ddl_upgrade_to_0.0.11.sql` §4）；`epp_instances` 表不变。

## 3. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api`（主）；`ai-gateway-epp`（契约适配，另仓排期）；`bfe`（零改动）；`ai-gateway-web`（前端适配，另排期） |
| 代码 | `model/epp_pool/`（校验、AssignmentParam、视图构建、分配器、覆写、导出）、`storage/rdb/epp_pool/`（去列）、`endpoints/openapi_v1/epp_assignments/`（data.go/put.go）、`model/icluster_conf/`（注释） |
| schema | `db_ddl.sql`、`db_ddl_sqlite.sql` 删列；`database/db_ddl_upgrade_to_0.0.11.sql` 追加 §4 删除列（DROP COLUMN + 回滚说明） |
| 测试 | 单测 7 个文件（`model/epp_pool` ×4、`endpoints/openapi_v1` ×2、`model/icluster_conf` ×1）；集成测试 6 个文件 + 测试基线 DDL 迁移 |
| 文档 | `api-define/OpenAPI接口定义/epp-pool.md`、`epp-assignments.md`；`api-define/InnerAPI接口定义/epp-data.md`、`server-data-conf.md`；`sys-design/` 4 篇；CHANGELOG |
| 不涉及 | `group` 概念（保留，升级为分配单元）；`epp_instances` 表；`epp_config` 编译与 `EndpointPickerConfig` 结构；BFE 生产代码；EPP 运行时 `cell.Role`/`Demote` 删除（另仓随本次一并执行） |

## 4. 关键决策

| 决策 | 说明 |
|------|------|
| 组规模恰 1 实例 | `validateGroupSize`：`n != 1` 拒绝，错误信息 `"epp pool group %q requires exactly 1 instance, got %d"`。数组字段形态保留，未来多实例扩容只需放松该校验。 |
| 分配粒度 = group | `epp_assignments` 只存 `{cluster, group_name}`；实例由 group 派生。`allocateCluster` 只选组不再选实例；`OverrideAssignment(ctx, cluster, group)` 去掉实例参数；有效分配条件 = "group 存在于池中且非空"。 |
| `/epp-assignments` 视图只留 `{cluster, group}` | `instances` 是 pool 数据的跨接口重复（单一事实源在 `/epp-pool`，前端按组名 join）；`degraded` 的信息已由 `unassigned_clusters` 覆盖且悬空分配由 reconciler 自愈。原 degraded 语义（group 被移除/组内无实例）归入 unassigned。 |
| InnerAPI assignment 条目 = `{group}` | `AssignmentEntry{Primary, Standby}` → `{Group string}`，与存储粒度一致。EPP 侧需新增自身 group 身份标识（`EppDataWatcher.instance` 改名为 `eppgroup`；`-epp-group` flag / `AI_GATEWAY_EPP_GROUP`），`resolveRole` 改为 `resolveAssignmentEntry` 返回 mine bool，`cell.Role` 一并删除。 |
| `EPPAddr` 形态不变 | `[]string` 类型保留，取 group 实例地址，恒单元素；未分配/悬空 cluster 仍导出 WRR 无 EPPAddr（既有降级语义不变）。BFE 零改动。 |
| 存量数据处理 | 升级前先将池收敛为单实例组（旧版本最后一次 PATCH）；未收敛时组实例按 id 序派生，可能有一次指向切换；升级脚本附回滚 `ADD COLUMN`。 |

## 5. 发版（摘要）

- **breaking 面**：`/epp-pool` PATCH 校验收紧；`/epp-assignments` GET/PUT 契约重构；`/configs/epp_data/config` assignment 字段重构；`epp_assignments` 删列。EPP 侧（ai-gateway-epp）**不兼容旧版**：不提供双字段兼容形态。
- **发版顺序**：ai-gateway-epp 与 api + schema 升级 + 前端 **同版本同步发布**，不允许跨版本混部（混部时 EPP 全部 no-match 显式告警，不会静默错误）；bfe 任意顺序。
