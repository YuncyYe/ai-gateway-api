# Issue #289：k8s_pool 镜像补齐——设计变更说明

> 基线：`899b107`（v0.0.11-dev）。本方案保留 `providers.k8s_instance_pool` 镜像、cluster 派生池快照与 `/k8s_pools` fan-out，**不实现**启动 backfill / 周期 reconcile（决策 D4）。

## 1. 设计基准：provider 的两类实例来源

| 维度 | `instance_pool`（手工来源） | `k8s_pool`（K8s 来源） |
|---|---|---|
| 事实源 | `providers.instance_pool` | `k8s_pools.instances` |
| 实例载体 | 无镜像，直接进 cluster 派生池 | `providers.k8s_instance_pool` 镜像 → cluster 派生池 |
| 写入方 | 人经 OpenAPI 写 provider | service-controller 经 InnerAPI 全量替换 |
| provider 创建/更新 | 写自身字段 + hook 同步派生池 | **引用建立/变化时解析 `k8s_pools` 写镜像**（本方案新增）+ hook 同步派生池 |
| pool PUT/DELETE | 不涉及 | fan-out 刷新镜像 + 派生池（现状保留） |
| `cluster_table` 导出 | 读派生池快照 | 读派生池快照（不变） |

不变量：

- I1：`k8s_pools` 是 k8s 来源实例的唯一事实源；
- I2：k8s 模式下 `k8s_instance_pool` 是其只读镜像；
- I3：cluster 派生池是 `EffectiveInstancePool(provider)` 的快照；
- I4：`instance_pool` 在 k8s 模式休眠，不参与有效池；
- I5：provider 创建/更新、pool PUT/DELETE 是镜像的合法写入时机；本期不实现自动 reconcile；
- I6：偏差通过重新 PUT pool（触发 fan-out）修复；
- I7：空池/缺池 = 零实例，导出空条目；
- I8：手工来源行为与改造前完全一致。

## 2. 代码改动

### 2.1 `model/iprovider`：reader 接口与 hydration

```go
// K8sPoolInstanceReader 由 k8s_pool storage 实现。
type K8sPoolInstanceReader interface {
    FetchK8sPoolInstances(ctx context.Context, poolName string) ([]ProviderInstance, error)
}

// ProviderManager 新增字段与 setter（构造顺序不变的注入方式）
func (m *ProviderManager) SetK8sPoolInstanceReader(r K8sPoolInstanceReader)
```

`hydrateK8sInstancePool(ctx, source, poolName, mirror)`：

- `source != k8s_pool`：不写镜像（返回 nil）；
- `poolName` 为空：参数错误；
- reader 未注入：明确错误（`k8s pool instance reader is not configured`）；
- 读 `k8s_pools`：DB 错误返回错误；pool 不存在返回**非 nil 空列表**；
- 把解析结果写入 `*mirror`。

`CreateProvider`（`provider.go:184`）：

- 校验顺序不变（`ValidateProviderParam` 仍拒绝请求体携带 `k8s_instance_pool`，422）；
- 事务内、写库之前调用 hydration：`instance_source=k8s_pool` 时按 `k8s_pool_name` 读 `k8s_pools` 写入镜像；
- 创建时不可能已有 cluster 引用该 provider（`CreateCluster` 会校验 provider 存在），无需同步派生池。

`UpdateProvider`（`provider.go:230`）：

- 事务内，写库之前，在原有 `mergedSource` 计算之后插入镜像维护：

  ```go
  if mergedSource == InstanceSourceK8sPool {
      needHydrate := existing.InstanceSource != InstanceSourceK8sPool ||
          mergedPoolName != existingPoolName ||
          param.K8sPoolName != nil
      if needHydrate {
          // 读 k8s_pools 并设置 param.K8sInstancePool
      }
  } else if len(existing.K8sInstancePool) > 0 {
      // 切回手工：清空镜像（保留现有逻辑）
  }
  ```

- 触发条件（D3）：

  | 条件 | 场景 |
  |---|---|
  | `existing.InstanceSource != k8s` | 手工 → k8s |
  | `mergedPoolName != existingPoolName` | k8s A → B |
  | `param.K8sPoolName != nil` | 显式重新提交池名（web PATCH 总会带，等价“重新保存即修复”） |

- hydration 后走既有 hook 链路：`newProvider := applyProviderUpdate(existing, param)`（含新镜像），`EffectiveInstancePool(existing)` 与 `EffectiveInstancePool(newProvider)` 不等则触发 `ProviderInstancePoolSyncer` 同步派生池。现有“将本次未提供的 `InstancePool`/`Keys`/`Models` 恢复为 nil”的逻辑必须保留。

边界矩阵：

| existing 来源 | 请求 source | 请求 pool name | 行为 |
|---|---|---|---|
| 手工 | 手工 | - | 原逻辑；镜像保持空 |
| 手工 | k8s | 必填 | 解析镜像；hook 同步派生池 |
| k8s | k8s | 相同/显式 | 解析镜像；漂移则 hook 同步 |
| k8s | k8s | 不同 | 解析新池；hook 同步 |
| k8s | k8s | 未提供 | 不解析；镜像由 fan-out 维护 |
| k8s | 手工 | - | 清空镜像；hook 写入 `instance_pool` |
| 任意 | 请求体带 `k8s_instance_pool` | - | 校验阶段 422 |

> 现有契约注意：`ValidateProviderParam` 只按请求体判断来源；k8s provider 的 PATCH 必须显式携带 `instance_source=k8s_pool`（web 已如此）。本方案不改变该契约。

### 2.2 `model/ik8s_pool` 与 storage：读取接口

- `ik8s_pool.K8sPoolStorager` 内嵌 `iprovider.K8sPoolInstanceReader`；
- `storage/rdb/k8s_pool.RDBK8sPoolStorager` 新增 `FetchK8sPoolInstances`：pool 存在返回实例列表；不存在返回非 nil 空列表；DB 错误向上返回；
- 读操作在调用方事务内执行（`ctx` 为 `*lib.DBContext` 时复用同一 `sql.Tx`），保证“读 pool + 写 provider”同事务。

### 2.3 `model/icluster_conf/cluster.go:840` 修复

```go
// 修复前
Instances: providerInstancesToClusterInstances(provider.InstancePool),
// 修复后
Instances: providerInstancesToClusterInstances(
    iprovider.EffectiveInstancePool(provider)),
```

- 手工模式：`EffectiveInstancePool` = `instance_pool`，行为不变；
- k8s 模式：派生池刷新为镜像实例，PATCH cluster 不再清空；
- **cluster 更新不会修改 provider 镜像**：`UpdateCluster` 只调用 `poolStorager.UpdatePool` 写派生池，不写 `providers` 表；
- 未携带 `llm_config.provider` 的 PATCH 不进入该同步块，派生池不被写。

### 2.4 容器装配（`stateful/container/rdb/components.go`）

```go
container.K8sPoolStorager = k8sPoolStorage.NewRDBK8sPoolStorager(stateful.NewBFEDBContext)
container.ProviderManager.SetK8sPoolInstanceReader(container.K8sPoolStorager) // 新增
container.K8sPoolManager = ik8s_pool.NewK8sPoolManager(...)                    // 现状不变
```

不新增 backfill goroutine、不新增配置项。

### 2.5 `ik8s_pool.DeletePool`：引用感知删除（行为变更）

`DELETE /inner-api/v1/k8s_pools/{name}` 由“无引用保护、一律删行”改为引用感知语义：

1. 查询 `k8s_pools` 条目，不存在返回 404；
2. 查出所有 `instance_source=k8s_pool && k8s_pool_name==name` 的 provider（引用方）；
3. **无引用方**：删除 `k8s_pools` 中该 pool 条目；
4. **有引用方**：
   - 打印 warning 日志：正在删除被引用的 k8s pool，将清空实例池而不是删除条目（记录 pool 名与引用方数量）；
   - 清空该条目实例列表，**保留条目**（`last_sync_time` 更新）；
   - 复用 fan-out（`syncProviderMirrors(ctx, name, []ProviderInstance{})`）将所有引用方 provider 镜像置空，并逐个同步其 cluster 派生池（清空）；

全部在同一事务内完成，任一步失败整体回滚。被引用时 DELETE 返回 200，此后 `GET /k8s_pools/{name}` 返回 200 + `instance_count=0`；provider 视角下“池不存在”与“池存在但零实例”仍为同一状态（有效池均为空）。

### 2.6 明确不改动

- `cluster_table` 导出（`exporter.go`）：仍读派生池快照；
- batch（`cancel.go:259`）：仍读 `EffectiveInstancePool`；
- fan-out（`k8s_pool.go` `syncProviderMirrors`）：保持现状；
- DDL / provider API 字段 / web / controller：无改动；
- **不实现** `ReconcileProviderMirrors`、启动 backfill、周期 reconcile（决策 D4，后续如需自动化另立变更单）。

## 3. 行为矩阵（改造前 → 改造后）

| 场景 | 改造前 | 改造后 |
|---|---|---|
| PUT pool → 建 provider → 建 cluster → 不再 PUT | 导出为空（#289） | 创建 provider 时镜像=池实例，导出含实例 |
| 建 provider → 建 cluster → PUT pool | fan-out 补齐 | 不变 |
| pool PUT 增删实例 | fan-out 镜像+派生池 | 不变 |
| PATCH cluster（k8s provider） | 休眠 `instance_pool` 清空派生池 | 用镜像刷新，导出保持实例 |
| PATCH provider 手工→k8s | 空镜像清空派生池 | 解析镜像，派生池=池实例 |
| PATCH provider k8s A→B | hook 不触发，导出仍旧 A | 解析 B，hook 同步 |
| PATCH provider k8s 同池名（web 重新保存） | 不刷新 | 重新解析，修复漂移 |
| PATCH provider k8s→手工 | 清空镜像 + hook 写手工池 | 不变 |
| DELETE 被引用/未引用 pool | 一律删行 + fan-out 清空 | 引用感知：未引用删行；被引用 warning + 清空保留 + fan-out 清空（§2.5） |
| 存量坏数据 | 永久为空 | 运维重新 PUT pool 修复（本期无自动 backfill） |
| `k8s_pools` 读失败 | fan-out 事务回滚 | provider 创建/引用变化写请求失败并回滚；已有快照不变 |
| 手工来源全链路 | - | 完全不变 |

## 4. 错误语义

| 场景 | 行为 |
|---|---|
| 创建/更新 k8s provider，`k8s_pools` 读取失败 | 写请求失败（fail-close），事务回滚 |
| 创建/更新 k8s provider，pool 不存在 | 成功，镜像 `[]` |
| k8s provider 未注入 reader | 明确错误 |
| 请求体携带 `k8s_instance_pool` | 422（模型层校验，位置不变） |
| DELETE 被引用 pool | 200；条目保留、实例清空，fan-out 清空引用方镜像与派生池，附 warning 日志 |
| DELETE 未引用 pool | 200；条目删除 |
| pool PUT/DELETE fan-out 失败 | 事务回滚，InnerAPI 报错（controller 重试） |

## 5. 测试计划

### 5.1 单测

- 创建 k8s provider：池存在 → 镜像=池实例；池不存在 → 镜像 `[]`；reader 错误 → 创建失败；reader 未注入 → 明确错误；手工来源 → 镜像空；
- 更新：手工→k8s / k8s A→B / k8s 同池名显式提交 / k8s 未带池名 / k8s→手工 / 手工→手工；
- hook：四种来源组合的派生池同步；
- `UpdateCluster`：k8s provider PATCH 不清空派生池；手工模式行为不变；
- 只读字段 422 回归；
- `FetchK8sPoolInstances`：存在/不存在/空池/DB 错误（沿用未提交的 `storage/rdb/k8s_pool/k8s_pool_test.go`）；
- `DeletePool` 引用感知：未引用 → 删行；被引用 → 保留条目 + 实例清空 + fan-out 清空镜像/派生池 + warning 日志；不存在 → 404。

### 5.2 集成测试（`test/integration`）

1. **#289 回归（必须）**：PUT pool（4 实例）→ 创建 k8s provider → 断言 `k8s_instance_pool` 有 4 个 → 创建 cluster → `cluster_table` 立即含 4 个 backend（**不再次 PUT**）；
2. 原有顺序：建 provider → 建 cluster（空）→ PUT pool → 导出出现实例；
3. PATCH cluster 不清空：k8s provider + 非空池 → PATCH cluster → 导出仍有实例；
4. 池名切换 / 模式切换 / DELETE pool / 只读字段 422；
5. DELETE 引用感知（新增）：未被引用的 pool → 条目删除（GET 404）；被引用的 pool → 200，条目保留且 `instance_count=0`，引用方镜像与 cluster_table 均清空，warning 日志断言；
6. 手工来源回归；
7. 存量修复路径（可选）：测试工具构造“镜像空、派生池空、池有实例”旧数据 → 重新 PUT pool → 镜像与导出恢复（验证运维修复路径，不验证 backfill）。

重写 `tests/provider/k8s_pool/k8s_pool_test.go` 中“创建后镜像必为空、需再次 PUT”的旧断言（`:152-166`）。

### 5.3 验收命令

```bash
make test-model
make test-model-cover-gate
make test
make license-check

cd test/integration
go test ./tests/provider/... ./tests/k8s_pools/... ./tests/clusters/... ./tests/innerapi/...
```

## 6. 发布与回滚

1. 合并且发布（无 DDL、无停服要求）；
2. **存量修复（必须手动）**：对每个受影响 pool 重新 PUT 一次全量实例，触发 fan-out 修复镜像与派生池（见 change-summary §5）；
3. 验证 provider 镜像与 `cluster_table`；
4. 回滚：直接回滚代码即可，镜像列保留、已写入的正确数据不受影响。

## 7. 风险

| 风险 | 等级 | 缓解 |
|---|---|---|
| 物化视图一致性窗口（方案 A 固有） | 中 | 已知写入口同步维护；漂移后手动 re-PUT pool |
| provider PATCH 与 pool PUT 并发覆盖镜像 | 低 | 仅显式引用变化/重新提交时 hydration；发现漂移 re-PUT；后续可评审行锁/周期 reconcile |
| provider 创建/改名与 pool DELETE 并发留下悬空镜像 | 低 | 手动 re-PUT/再次 DELETE；正常路径不悬空 |
| provider 写依赖 `k8s_pools` 读 | 低 | fail-close 刻意为之；池不存在仍成功 |
