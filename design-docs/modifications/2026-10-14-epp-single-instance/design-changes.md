# EPP 单实例化——分配粒度改为 group、删除 primary/standby 设计变更说明

变更记录：`change-summary.md`。涉及分层：存储层（`storage/rdb/epp_pool` + DDL）、模型层（`model/epp_pool`、`model/icluster_conf`）、端点层（`endpoints/openapi_v1/epp_assignments`）。

## 1. 存储层

### 1.1 `epp_assignments` 表删除 `primary_instance_id` 列

**原：** `epp_assignments(cluster PK, group_name NOT NULL, primary_instance_id NOT NULL, create_time, update_time)`，update_time 触发器在 UPDATE 时刷新。

**新：** `epp_assignments(cluster PK, group_name NOT NULL, create_time, update_time)`，触发器不变。

- `db_ddl.sql`、`db_ddl_sqlite.sql` 同步删列；
- `database/db_ddl_upgrade_to_0.0.11.sql` 追加 §4 删除列：`ALTER TABLE epp_assignments DROP COLUMN primary_instance_id;`，附回滚 `ADD COLUMN` 与回填说明（回滚后列值需以组内实例回填或置空交由 reconciler 修复）；
- `storage/rdb/epp_pool/`：`UpsertAssignment` / `FetchAssignment` / `FetchAssignmentList` 的列清单去列。

### 1.2 数据语义迁移

升级后实例由 group 派生。**建议升级前先将池收敛为单实例组**（旧版本最后一次 `PATCH /epp-pool` 写入单实例组）；若未收敛，双实例组的 `instances`/`EPPAddr` 导出将包含组内全部实例（按 id 序），可能造成一次指向切换，且新校验下任何 PATCH 都会 422 要求先收敛。

## 2. 模型层（`model/epp_pool`）

### 2.1 组规模校验（`epp_pool.go` `validateGroupSize`）

**原：** `n < 1 || n > 2` 拒绝，文案 `"requires 1 or 2 instances"`。

**新：**

```go
func (m *EppPoolManager) validateGroupSize(group *InstanceGroup) error {
	if n := len(group.Instances); n != 1 {
		return xerror.WrapParamErrorWithMsg("epp pool group %q requires exactly 1 instance, got %d", group.Name, n)
	}
	return nil
}
```

### 2.2 `AssignmentParam` 去掉 `PrimaryInstanceID`

**原：** `{Cluster, GroupName, PrimaryInstanceID}`（注释：primary only；standby is expanded at read time）。

**新：** `{Cluster, GroupName}`——分配只到 group，实例由 group 派生。

### 2.3 视图构建（`assignment.go`）

- `ClusterAssignmentView{Primary, Standby, Degraded}` → `{Cluster, Group *string}`：`Instances`/`Degraded` 字段删除，视图只记组名。
- `buildAssignmentsView`：有效分配条件 = **group 存在于池中且非空** → `entry.Group = &assignment.GroupName`，计入 `occupiedGroups`/`validAssignedClusters`；否则该 cluster 计入 `UnassignedClusters`（原 degraded 半态取消，悬空分配由 reconciler 自愈）。
- `expandPrimaryAndStandby`（读时展开 standby）**整个函数删除**。

### 2.4 分配与覆写

| 函数 | 原 | 新 |
|------|----|----|
| `allocateCluster` | 选组 + 选组内空闲 primary 实例，写 `{cluster, group, primary_instance_id}` | 只选组（占用最少/空闲组），写 `{cluster, group}`；唯一键冲突重试逻辑不变 |
| `OverrideAssignment` | `(ctx, clusterName, groupName, primaryInstanceID)`，校验实例在组内 | `(ctx, clusterName, groupName)`，校验"组存在且非空" |
| `findValidAssignment` | primary 实例外池内 → 有效 | group 存在且非空 → 有效 |
| `repairOne` | 同组重选 primary / 跨组重分配 / 清除 | 动作枚举不变；无实例级重选，"组内 primary 被移除"场景不再存在（组即分配单元） |
| `GetAssignmentEndpoints` | `[primary, standby]` 有序地址（standby 读时展开） | group 全部实例地址（正常恒单元素）；接口签名不变 |

### 2.5 epp_data 导出（`epp_data.go`）

**原：**

```go
type AssignmentEntry struct {
	Primary string  `json:"primary"`
	Standby *string `json:"standby"`
}
```

**新：**

```go
type AssignmentEntry struct {
	Group string `json:"group"` // epp group name（cluster → group 映射，与分配存储粒度一致）
}
```

`EppDataGenerator` 的 assignment 段填充相应改为 `entry.Group` 派生；无有效分配（unassigned/悬空）的 cluster 继续不出现在 assignment 段。

### 2.6 icluster_conf（注释级）

`model/icluster_conf/epp.go` `EPPAssignmentResolver` 接口注释 `[0]=primary, [1]=standby` → "group 实例地址列表（当前阶段恒单元素）"；`fetchEPPAssignmentAddrs` 与 `cluster.go` EPP 导出分支注释同步。逻辑零改动。

## 3. 端点层（`endpoints/openapi_v1/epp_assignments`）

### 3.1 响应结构（`data.go`）

```go
// 变更前
type ClusterAssignmentData struct {
	Cluster  string        `json:"cluster"`
	Group    *string       `json:"group"`
	Primary  *InstanceData `json:"primary"`
	Standby  *InstanceData `json:"standby"`
	Degraded bool          `json:"degraded"`
}
type ClusterAssignmentOverrideData struct {
	Cluster string        `json:"cluster"`
	Group   *string       `json:"group"`
	Primary *InstanceData `json:"primary"`
	Standby *InstanceData `json:"standby"`
}

// 变更后
type ClusterAssignmentData struct {
	Cluster string `json:"cluster"`
	Group   string `json:"group"` // clusters[] 只含有效分配
}
type ClusterAssignmentOverrideData struct {
	Cluster string `json:"cluster"`
	Group   string `json:"group"` // 覆写成功必然有效分配，恒非空
}
```

`InstanceData` 与 `newInstanceData` 随字段删除一并移除（本包不再引用）。

### 3.2 PUT 请求体（`put.go`）

```go
// 变更前
type OverrideParam struct {
	Cluster           string `uri:"cluster"`
	GroupName         string `json:"group_name"`
	PrimaryInstanceID string `json:"primary_instance_id"`
}
// 变更后
type OverrideParam struct {
	Cluster   string `uri:"cluster"`
	GroupName string `json:"group_name"`
}
// OverrideAction 调用：OverrideAssignment(ctx, cluster, groupName)
```

### 3.3 无改动的端点

`endpoints/openapi_v1/epp_pool/`（data/patch/get）、`endpoints/innerapi_v1/epp_data/export.go`、`endpoints/innerapi_v1/server_data/export.go`——结构或纯委托，零改动。

## 4. 测试调整

**单元测试：**

| 测试文件 | 调整 |
|----------|------|
| `model/epp_pool/mocks_test.go` | fake storager 去 `primary_instance_id` 列；`seedAssignments` 去掉实例 id 参数 |
| `model/epp_pool/epp_pool_manager_test.go` | 双实例组种子改单实例组（`TestPatchPool_FullReplace` 总数断言下调）；`TestPatchPool_Validation` 新增 `"two instances rejected"` 用例（422 + 文案 `"requires exactly 1 instance"`），保留空组 422；跨组重复 id/host 负例保持 |
| `model/epp_pool/assignment_test.go` | `expandPrimaryAndStandby` 用例删除；视图用例的 Primary/Standby/Degraded 断言改 Group（有效分配 = group 非空，degraded 条目不再出现、计入 unassigned）；`allocateCluster`/`repairOne` 用例去实例级参数；"同组重选 primary" 场景删除；`GetAssignmentEndpoints` 双元素期望改单元素 |
| `model/epp_pool/epp_data_test.go` | `entryA/B` 期望改 `AssignmentEntry{Group: "g1"}`；Standby 断言删除 |
| `endpoints/openapi_v1/epp_pool/endpoints_test.go` | `TestPatchAction` body 改单实例组；`TestPatchAction_GroupSize` 新增 2 实例 422 用例 |
| `endpoints/openapi_v1/epp_assignments/endpoints_test.go` | PUT body 去 `primary_instance_id`；响应断言改 `{cluster, group}`（marshal 后无 `primary`/`standby`/`instances`/`degraded` key）；覆写流程目标改另一组 |
| `model/icluster_conf/cluster_test.go` | `EPPAddr` 期望从双元素改单元素 |

**集成测试（`test/integration/tests/`）：**

| 测试文件 | 调整 |
|----------|------|
| 测试基线 DDL | 基线建库脚本 `db_ddl_sqlite.sql` 已同步删列 |
| `epp_pool/update/update_test.go` | `TestEppPool_Update_GroupSize`：双实例组"通过"用例改为 422 拒绝 + 池内容不变回读 |
| `epp_assignments/get/get_test.go` | 双实例组夹具改两个单实例组；primary/standby 断言改 `{cluster, group}`；EA-1-005 组缩容场景按单实例语义重写；degraded 条目断言改 unassigned_clusters |
| `epp_assignments/put/put_test.go` | 请求体去实例字段；EA-2-001 "standby 换主"改为"覆写到另一组"；响应断言改 `{cluster, group}` |
| `schema/openapi/epp_assignments.go` + `openapi_schema_test.go` | `EppClusterAssignmentSchema`/`...OverrideSchema` 只留 `{cluster, group}`（必填）；覆写流程改写 |
| `innerapi/epp_data/epp_data_test.go` | assignment 条目断言改 `{group}`；EPPAddr 长度 2 断言改 1；`fullStandby` 相关用例删除 |
| `schema/innerapi/innerapi_schema_test.go` | `EPPAddr [主,备]` 断言改单元素；`assertServerDataConfEpp` 更新 |

## 5. 文档同步

- `api-define/OpenAPI接口定义/epp-pool.md`：见 `api-changes.md` §1。
- `api-define/OpenAPI接口定义/epp-assignments.md`：见 `api-changes.md` §2。
- `api-define/InnerAPI接口定义/epp-data.md`：见 `api-changes.md` §3.1。
- `api-define/InnerAPI接口定义/server-data-conf.md` §3.3：见 `api-changes.md` §3.2。
- `sys-design/details/EPP调度对接.md`、`接口层设计文档.md`、`模型层设计文档.md`：standby 读时展开、组规模 1~2、`{group, primary, standby}` 视图、互为主备等描述清理。
- `sys-design/数据库设计文档.md`：`epp_assignments` 表结构去列。
- `CHANGELOG.md` `[Unreleased]`：新增条目（见 `api-changes.md` §4）。

## 6. 非目标

- 不删除 `group` 概念（保留为分配单元/标签）。
- 不改 `epp_instances` 表结构与 `/epp-pool` API 形状。
- 不改 `epp_config` 解析/编译（`EndpointPickerConfig`）与版本控制机制。
- 不改 bfe 生产代码（EPPAddr 通用列表天然兼容单元素）。
- EPP 运行时 `RoleStandby`/`StateStandby`/`Demote` 状态机随 `cell.Role` 一并删除（ai-gateway-epp 仓库，见该仓 `2026-10-14-epp-single-instance/design-changes.md` §3.4）。
- `epp_assignments` 列名不回迁历史数据语义（primary_instance_id 直接丢弃，实例以 group 派生为准）。
