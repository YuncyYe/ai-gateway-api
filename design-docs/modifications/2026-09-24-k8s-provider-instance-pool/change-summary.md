# K8s 场景下 Provider 实例池维护（instance_source + k8s_pools）变更摘要

## 1. 背景

K8s 服务发现场景下，Provider 的创建往往先于任何实例就绪（Service 刚创建、Pod 未调度、发现组件尚未完成首次同步），而当前 `instance_pool` 必填 ≥1 的校验造成"创建 Provider 要先有实例，发现实例要先有 Provider"的死锁。同时，控制面管理员与 K8s 发现组件写的是同一个 `instance_pool` 数组，任何一方的全量替换都会抹掉另一方的修改。

核心思路：Provider 引入 `instance_source` 声明实例供给方式（`instance_pool` 人维护 / `k8s_pool` 发现维护），新增 `/k8s_pools` InnerAPI 作为发现组件的唯一写入通道，Provider 上的 `k8s_instance_pool` 为系统维护的只读镜像。"创建归人、成员归发现"，冲突在模型上不存在。

## 2. 目标

| 期 | 目标 | 验证标准 |
|---|------|----------|
| P0 | provider 引入 `instance_source`（本期仅 `instance_pool` 合法），实例池校验条件化；syncer 空池语义从"跳过"改为"清空" | ① 存量 provider 创建/更新行为不变（默认模式校验规则原样）；② `instance_source=k8s_pool` 返回 422；③ 单测覆盖新分支，`make test-model-cover-gate` 通过 |
| P1 | `instance_source=k8s_pool` 模式启用：provider 三字段、`/k8s_pools` InnerAPI、`k8s_instance_pool` 只读镜像与同步事务、下游改读有效池 | ① 集成测试按全链路场景全过（PUT pool → 建 `k8s_pool` provider → 镜像出现 → cluster 引用 → 导出含实例 → 增删实例 → 导出跟随 → DELETE pool → 镜像清空、导出空条目）；② InnerAPI 文档与接口清单更新；③ 老 provider（无新字段行）读写行为不变（默认值兜底） |

## 3. 范围

| 范围 | 说明 |
|------|------|
| 涉及仓库 | `ai-gateway-api`（本说明）；发现组件（service-controller）对接 `/k8s_pools` 的改动不在本仓库 |
| 主要模块 | `model/iprovider`、`model/icluster_conf`、新增 `model/ik8s_pool` + `storage/rdb/k8s_pool`、`endpoints/innerapi_v1/k8s_pools`、`db_ddl.sql` / `db_ddl_sqlite.sql` |
| 接口契约 | OpenAPI `/providers` 新增 3 字段与条件校验；InnerAPI 新增 `/k8s_pools` 四端点 |
| 数据迁移 | `providers` 表加 3 列（默认值兜底，零迁移）；新表 `k8s_pools` |
| 数据面影响 | BFE 零改动。空池数据面行为已查证：导出为空条目、BFE 加载接受、该 cluster 请求 500（`BK_NO_BACKEND`），由告警覆盖 |

## 4. 关键决策

| 决策 | 说明 |
|------|------|
| 分 P0/P1 两期 | P0 只做结构调整（`instance_source` 落地 + syncer 空池语义），对用户行为零变化；P1 才放开 `k8s_pool` 模式与 `/k8s_pools`，避免一次变更面过大 |
| 空池只在 `k8s_pool` 模式合法 | `instance_pool` 模式维持 ≥1 规则不变；合法空池只出现在 `k8s_pool` 模式 |
| 不设过渡期方案 | controller 直写 `PUT /providers/{name}/instance-pool` + 404 退避的过渡期形态不实施，直接被 `/k8s_pools` 取代，避免 controller 对接面迁移两次。"Provider 只能由人创建"的边界仍然有效并被 `/k8s_pools` 形态完全满足 |
| 有效池单一消费口径 | 抽出 `effectiveInstancePool()`：`source==k8s_pool ? k8s_instance_pool : instance_pool`；syncer、cluster 创建快照、相等比较等一切下游一律改读有效池 |
| syncer 空池语义为清空 | 删除 `cluster.go:906` 早退：有效池为空时引用 cluster 的派生池同步清空（含空列表），与 K8s 实例动态生成/消失的语义一致 |
| DELETE pool 无引用保护 | 被引用的 `/k8s_pools/{name}` 允许删除；引用者的 `k8s_instance_pool` 置空、cluster 派生池清空（单事务内）。"池不存在" ≡ "零实例" |
| `k8s_pool_name` 与 K8s Service 名无耦合 | 池名是控制面内普通标识符，Service→池名映射由 controller 自行约定，API 模型不感知 K8s 语义 |
| `instance_pool` 在 k8s 模式休眠保留 | 不参与有效池、不做成员数校验，切回 `instance_pool` 模式时自动恢复生效，作为人工兜底 |
| 导出侧零改动 | `exporter.go` 空池 skip + 空条目下发的既有语义正是所需（维持"发空条目"语义是 BFE 加载接受空池的前提） |

## 5. 关联文档

- 详细设计：`design-changes.md`
- 接口变更：`api-changes.md`
- 相关接口定义：`design-docs/api-define/OpenAPI接口定义/providers.md`、新增 `design-docs/api-define/InnerAPI接口定义/k8s-pools.md`
- 先例记录：`design-docs/modifications/2026-08-28-issue-106-provider-instance-pool-sync/`（syncHook 事务内同步机制）

## 6. 状态

- [x] 变更说明文档（change-summary / api-changes / design-changes）
- [x] `api-define/` 更新并 review（providers.md、新增 k8s-pools.md、02-interface-list.md）
- [x] `sys-design/` 更新并 review（模型层 §4.14/§4.16、details/provider与cluster概念分离、数据库/存储层设计文档、summary.md）
- [x] P0+P1 代码实现与单测（`go test` 全绿；model 覆盖率 82.8% ≥ 70% 门禁）
- [x] 集成测试：新增 `tests/provider/k8s_pool/`（PUT→镜像→引用→导出→增删→DELETE 全链路；联动场景 KP-6：N:1 fan-out/PUT 清空/模式切换脱离恢复；按 skill 补齐 KP-7：4xx 回读零变更/PATCH 省略矩阵/审计 diff_keys）与 `tests/innerapi/k8s_pools/`（IN-K8S 生命周期/参数校验/共享池读路径/拒绝写入零变更 + mysql 并发用例，已登记各自 design.md）+ 存量 create/partial_update/instance_pool_sync/innerapi 导出回归全绿
- [x] 测试驱动缺陷修复：审计日志批量写入在混合"无 change_summary 条目"时整批被 gendry 拒写（insert data not match）且静默丢弃——`storage/rdb/ioperlog/operation_log.go` 改为恒写 change_summary 列（空摘要序列化为 `"null"`），由 KP-7-007 审计用例暴露并锁定
- [x] 测试基建加固：测试服务器 SQLite DSN 增加 `_pragma=busy_timeout(5000)`（`testutil/server.go`）——审计日志刷盘恢复写入后与请求事务在单写者模型下瞬态争用曾致 `TestInnerAPI_ExportVersionMonotonic` 偶发 SQLITE_BUSY 500
- [ ] MySQL 真实环境 DDL 执行验证（本机仅有 SQLite 路径验证）
