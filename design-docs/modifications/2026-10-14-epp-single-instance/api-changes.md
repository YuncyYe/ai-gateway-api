# EPP 单实例化 API 变更说明

变更记录：`change-summary.md`。涉及接口：`/epp-pool`（GET + PATCH）、`/epp-assignments`（GET + PUT）、InnerAPI `GET /configs/epp_data/config`、`GET /configs/tls_conf/server_data_conf`（EPP 相关部分）。

## 1. `/epp-pool`

**请求/响应结构无变化**，仅 PATCH 校验收紧：

| 项 | 原 | 新 |
|----|----|----|
| 每组实例数 | 1 个（仅主）或 2 个（主+备）；拒绝空组与 ≥3 实例的组 | **恰好 1 个**；拒绝空组与 ≥2 实例的组 |
| 422 文案 | `epp pool group %q requires 1 or 2 instances, got %d` | `epp pool group %q requires exactly 1 instance, got %d` |

`groups[].instances` 保留数组形态（未来多实例扩容预留）。`GET /epp-pool` 响应结构不变。

```json
// PATCH 合法（不变形态，组内恒单元素）
{ "groups": [
    { "name": "g1", "instances": [ { "id": "epp-a", "host": "10.0.0.1", "port": 9002 } ] },
    { "name": "g2", "instances": [ { "id": "epp-b", "host": "10.0.0.2", "port": 9002 } ] } ] }
// PATCH 422（原合法，现拒绝）
{ "groups": [ { "name": "g1", "instances": [
    { "id": "epp-a", "host": "10.0.0.1", "port": 9002 },
    { "id": "epp-b", "host": "10.0.0.2", "port": 9002 } ] } ] }
```

## 2. `/epp-assignments`

### 2.1 GET 全量视图

**响应元素字段：**

```json
// 变更前
{ "cluster": "cluster-a", "group": "g1",
  "primary": { "id": "epp-a", "host": "10.0.0.1", "port": 9002 },
  "standby": { "id": "epp-b", "host": "10.0.0.2", "port": 9002 },
  "degraded": false }

// 变更后（只保留 cluster + group；实例详情由 /epp-pool 按组名派生）
{ "cluster": "cluster-a", "group": "g1" }
```

- `clusters[]` 只含**有效分配**；`primary`/`standby`/`instances`/`degraded` 四个 key 物理删除（非恒 null）。
- 原 `degraded: true` 场景（分配的 group 被移出池或组内无实例）与"从未分配"统一由 `unassigned_clusters` 枚举；悬空分配由 reconciler 自愈。
- `unassigned_clusters`、`idle_groups` 字段与语义不变。

### 2.2 PUT 覆写（`PUT /epp-assignments/{cluster}`）

| 项 | 原 | 新 |
|----|----|----|
| 请求体 | `{ "group_name": "g2", "primary_instance_id": "epp-b" }` | `{ "group_name": "g2" }` |
| 校验 | 组存在、primary 在组实例列表中、与 standby 不同地址 | 组存在且非空 |
| 响应 | `{cluster, group, primary, standby}` | `{ "cluster": "cluster-a", "group": "g2" }` |

```json
// 请求 before → after
{ "group_name": "g2", "primary_instance_id": "epp-b" }
{ "group_name": "g2" }
// 响应 after
{ "cluster": "cluster-a", "group": "g2" }
```

## 3. InnerAPI

### 3.1 `GET /configs/epp_data/config`（消费方：ai-gateway-epp）

**输入**：`GET /configs/epp_data/config?version=<string>`（version 可选；未变化时返回 `Config: null`，语义不变）。

**输出 `Config.assignment` 段：**

```json
// 变更前
"assignment": {
  "cluster-a": { "primary": "epp-a", "standby": "epp-b" },
  "cluster-c": { "primary": "epp-c", "standby": null }
}
// 变更后
"assignment": {
  "cluster-a": { "group": "g1" }
}
```

- 条目从 `{primary, standby}`（实例 id）改为 **`{group}`**（组名），与分配存储粒度一致；
- 无有效分配的 cluster 不出现在 assignment 段（不变）；
- `epp_config` 段、`{Version, Config}` 信封、版本控制机制不变。

**EPP 侧适配（另仓）**：EPP 需新增自身 group 身份标识（`EppDataWatcher.instance` 改名为 `eppgroup`；`-epp-group` / `AI_GATEWAY_EPP_GROUP`），`resolveRole` 改为 `resolveAssignmentEntry(self, e) (mine bool)`（`cell.Role` 一并删除）。**不兼容旧版**：不提供双字段兼容形态，两侧同版本同步发布（见 §4）。

### 3.2 `GET /configs/tls_conf/server_data_conf`（消费方：bfe）

**输入**：`GET /configs/tls_conf/server_data_conf?version=<string>`（version 可选）。

**输出 `ClusterConf.Cluster.*.GslbBasic` 中 EPP 相关部分：**

```json
// 变更前
"GslbBasic": { "BalanceMode": "EPP", "EPPAddr": ["10.0.0.1:9002", "10.0.0.2:9002"] }
// 变更后（取分配 group 的实例地址，恒单元素；字段形态 []string 不变）
"GslbBasic": { "BalanceMode": "EPP", "EPPAddr": ["10.0.0.1:9002"] }
```

- 无有效分配的 EPP cluster 导出为 `BalanceMode: "WRR"` 且无 `EPPAddr`（既有降级语义不变）；
- bfe 侧零改动：`EPPAddr *[]string` 通用列表，单元素已支持（含单测覆盖）。

## 4. 兼容性矩阵与 CHANGELOG

| 契约 | 变化类型 | 消费方 | 处置 |
|------|----------|--------|------|
| `PATCH /epp-pool` 校验 | breaking（写入面收紧） | 部署流程/前端 | 422 文案明确；部署剧本同步更新；存量池先收敛为单实例组 |
| `/epp-assignments` GET/PUT | breaking（视图只剩 `{cluster, group}`；PUT 请求体只剩 `group_name`） | ai-gateway-web | 与 API 同步发版 |
| `/configs/epp_data/config` assignment | breaking（`{primary, standby}` → `{group}`） | ai-gateway-epp | **与 EPP 同版本同步发布，不允许跨版本混部**（混部时 EPP 全部 no-match 显式告警） |
| `epp_assignments` schema | breaking（删列） | 部署/运维 | 升级语句追加至 `database/db_ddl_upgrade_to_0.0.11.sql` §4，附回滚说明；升级前备份 |
| `EPPAddr` | 形态兼容（恒单元素） | bfe | 任意顺序 |

`CHANGELOG.md` `[Unreleased]` 建议条目：

```markdown
### Changed
- **Breaking** `PATCH /epp-pool`: tighten group-size validation to exactly 1 instance per group (backup/standby pairs are no longer accepted).
- **Breaking** `/epp-assignments`: the assignment view is reduced to `{cluster, group}` (`primary`/`standby`/`instances`/`degraded` removed); PUT body is now `{"group_name"}` only. Assignment granularity is now the EPP group, not a single instance.
- **Breaking** InnerAPI `GET /configs/epp_data/config`: `assignment[cluster]` entry changed from `{primary, standby}` to `{group}`. ai-gateway-epp ships in the same release (no cross-version compatibility; mixed versions fail loudly via `ai_epp_assignment_no_match`). See `design-docs/modifications/2026-10-14-epp-single-instance/`.
- **Breaking** schema: `epp_assignments.primary_instance_id` column dropped (migration statement appended to `database/db_ddl_upgrade_to_0.0.11.sql` §4).
- `server_data_conf` `GslbBasic.EPPAddr` is now always a single-element list; no BFE change required.
```
