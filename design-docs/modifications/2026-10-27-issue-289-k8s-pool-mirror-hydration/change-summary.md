# Issue #289：k8s_pool 模式 provider 引用建立/变更时镜像未解析——修复方案（方案 A：镜像补齐）

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/289](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/289)

现象：`k8s_pools/k8s_default_vllm-backend_http_szyf` 有 4 个实例，provider `yf-vllm-backends` 为 `instance_source=k8s_pool` 且 `k8s_pool_name` 引用该池，但 `k8s_instance_pool=[]`；引用该 provider 的 cluster `yf-vllm-cluster` 在 `cluster_table` 导出中为空条目 `"yf-vllm-cluster": {}`。

根因链（已对当前检出 `899b107` 逐环核实）：

1. `providers.k8s_instance_pool` 的唯一写入方是 `/k8s_pools` PUT/DELETE 的 fan-out（`model/ik8s_pool/k8s_pool.go:179` `syncProviderMirrors`）；
2. `CreateProvider`（`model/iprovider/provider.go:184`）不读 `k8s_pools`，创建时镜像固定写成 `[]`（`storage/rdb/provider/provider.go:201`）；
3. `UpdateProvider`（`model/iprovider/provider.go:230`）不解析 `k8s_pools`：`instance_pool -> k8s_pool` 时 hook 同步的是空镜像；`k8s_pool_name A -> B` 时新旧有效池相同、hook 不触发；
4. `CreateCluster` 用 `EffectiveInstancePool`（镜像）建派生池（`model/icluster_conf/cluster.go:528`）→ 镜像为空则派生池为空；
5. `UpdateCluster` 用 `provider.InstancePool`（k8s 模式下的休眠字段，`model/icluster_conf/cluster.go:840`）刷新派生池 → 任何 PATCH cluster 都会把 k8s 派生池写成空；
6. 无启动 backfill / 周期 reconcile，存量坏数据只能等下一次 pool PUT（controller 只在 Service/Endpoints 变更时触发，不保证发生）。

时间线证据：`k8s_pools.last_sync_time=1792995148`（06:12:28 UTC）早于 `providers.create_time=1792998977`（07:16:17 UTC）——pool 先写入，provider 一小时后才创建，期间无 fan-out 事件补镜像。

## 2. 方案裁定：A（镜像补齐，保留物化视图）

| 维度 | 方案 A（本方案） | 方案 B（实时解析，曾评估为备选，不实施） |
|------|------|------|
| provider 镜像 | 保留 | 删除 |
| fan-out | 保留 | 删除 |
| `cluster_table` 导出 | 读派生池快照（不变） | 按 `k8s_pool_name` 实时查 |
| 改动面 | 小（provider/cluster 两处 + reader 注入） | 大（删镜像列、导出重写、backfill） |
| DDL | 无 | 有 |
| 一致性 | 最终一致，收敛于写入口 + 手动 re-PUT | 导出时强一致 |

裁定结论：**方案 A**。保留镜像与物化视图，补齐“引用建立/变更”写入口的镜像解析；不实现自动 backfill（见决策 D4），存量数据由运维重新 PUT pool 修复。

## 3. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api` |
| 主要模块 | `model/iprovider`（hydration）、`model/icluster_conf`（cluster.go:840 修复）、`model/ik8s_pool`（接口内嵌 reader + DeletePool 引用感知语义）、`storage/rdb/k8s_pool`（新增读取方法）、`stateful/container/rdb/components.go`（装配） |
| 接口契约 | OpenAPI `/providers` 与 InnerAPI `/k8s_pools` 的**请求/响应结构不变**；`DELETE /k8s_pools/{name}` 行为语义变更（引用感知）；其余语义变化见 `api-changes.md` |
| 数据迁移 | 无 DDL |
| 数据面影响 | BFE 零改动 |
| 明确不做 | 启动 backfill、周期 reconcile、`ReconcileProviderMirrors`、导出实时解析（方案 B）、镜像列删除 |

## 4. 关键决策

| # | 决策 | 说明 |
|---|------|------|
| D1 | 保留镜像与 fan-out | 最小改动；镜像仍是 k8s 来源的有效池载体 |
| D2 | `CreateProvider` 创建时解析镜像 | k8s 模式创建时在同一事务内读 `k8s_pools` 写入镜像 |
| D3 | `UpdateProvider` 按条件解析 | 触发条件：来源切换 / `k8s_pool_name` 变化 / 显式重新提交池名（web PATCH 总会带 `k8s_pool_name`，等价于“重新保存即修复”）；不做到任何 PATCH 都读池，避免无关更新依赖 `k8s_pools` 可用性 |
| D4 | **本期不实现启动 backfill / 周期 reconcile** | 不生成 `ReconcileProviderMirrors`、启动 goroutine、相关配置；存量数据通过重新 PUT pool 触发现有 fan-out 修复 |
| D5 | `UpdateCluster` 用 `EffectiveInstancePool` 替换 `InstancePool` | 修复 k8s 模式下 PATCH cluster 清空派生池的问题；手工模式行为不变 |
| D6 | hydration 读池失败 fail-close | provider 创建/引用变化时 `k8s_pools` 读取失败则写请求失败并回滚，不产生空镜像半成品；池不存在按零实例处理（创建/更新成功） |
| D7 | 只读字段校验位置不变 | `k8s_instance_pool` 仍由模型层 `ValidateProviderParam` 拒绝（422）；内部 hydration 在校验之后、写库之前设置，不经过客户端校验 |
| D8 | DELETE pool 引用感知（行为变更） | 未被 provider 引用 → 删除 `k8s_pools` 条目；被引用 → warning 日志 + 清空实例保留条目 + fan-out 清空引用方镜像与 cluster 派生池；被引用时 DELETE 返回 200，GET 返回 200 + `instance_count=0` |

## 5. 存量数据修复（运维）

发布后，对受影响 pool 重新执行一次全量：

```shell
curl -X PUT "http://api-server:port/inner-api/v1/k8s_pools/k8s_default_vllm-backend_http_szyf/instances" \
  -H "Authorization:Token TOKEN_STRING" -H "Content-Type: application/json" \
  -d '[{"addr":"192.168.122.238","port":8000,"weight":100},{"addr":"192.168.122.238","port":8001,"weight":100},{"addr":"192.168.122.238","port":8002,"weight":100},{"addr":"192.168.122.238","port":8003,"weight":100}]'
```

现有 fan-out 会把实例写入 provider 镜像并同步 cluster 派生池，随后 `cluster_table` 导出出现 backend（版本由导出签名自动 bump）。

## 6. 关联文档

- 详细设计：`design-changes.md`
- 接口/文档同步：`api-changes.md`
- 方案 B（删除镜像 + 导出实时解析）：曾评估为备选，**不实施**；如后续需要单一事实源，另立变更单

## 7. 状态

- [x] 变更说明文档（change-summary / design-changes / api-changes）
- [x] `api-define/` 更新（providers.md、clusters.md、InnerAPI/OpenAPI k8s-pools.md、02-interface-list.md）
- [x] `sys-design/` 同步（模型层/存储层/数据库设计文档：DELETE pool 引用感知语义）
- [ ] 代码实现（`model/iprovider` hydration + `cluster.go:840` 修复 + reader 装配）
- [ ] 单测（创建/更新 hydration、UpdateCluster、只读字段 422 回归）
- [ ] 集成测试（`tests/provider/k8s_pool/` 重写“mirror 必为空”旧断言 + 本次 bug 回归 + PATCH cluster 不清空 + DELETE 引用感知）
- [ ] 存量数据 re-PUT 修复与 `cluster_table` 验证
