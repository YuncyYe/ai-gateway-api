# K8s 场景下 Provider 实例池维护：API 接口变更说明

## 1. 变更范围

| 接口类型 | 变更内容 |
|----------|----------|
| OpenAPI `/providers` | 新增 `instance_source` / `k8s_pool_name` / `k8s_instance_pool` 三字段；实例池校验条件化；新增 422 场景 |
| InnerAPI | 新增 `/k8s_pools` 域四端点（发现组件专用写入通道） |
| OpenAPI `/clusters` | 无变更。cluster 对 provider 的引用方式不变 |

---

## 2. OpenAPI 变更：`/providers`

### 2.1 新增字段

| 字段 | 类型（响应 / 请求） | 可写 | 说明 |
|------|--------------------|------|------|
| `instance_source` | `string` / `*string` | 人（POST/PATCH） | 实例供给方式，枚举 `instance_pool`（默认）/ `k8s_pool`。P0 仅接受 `instance_pool` |
| `k8s_pool_name` | `*string` / `*string` | 人 | `instance_source=k8s_pool` 时必填；普通名称校验，与 K8s Service 名无耦合 |
| `k8s_instance_pool` | `[]Instance` / `*[]Instance` | **只读**（请求出现即 422） | 系统从 `/k8s_pools` 同步的镜像；`instance_source=instance_pool` 时为空 |

`instance_pool` 字段语义调整：`instance_source=k8s_pool` 时**不参与有效池、不做成员数校验**（休眠保留，切回 `instance_pool` 模式时自动恢复生效，作为人工兜底）；`instance_source=instance_pool` 时维持现状规则。

### 2.2 条件校验规则（`model/iprovider/provider.go`）

| 场景 | 行为 |
|------|------|
| `instance_source` 缺省 | 按 `instance_pool` 对待，校验规则与现状完全一致（P0 对用户行为零变化） |
| P0 传 `instance_source=k8s_pool` | 422（`k8s_pool mode not supported yet`，P1 放开） |
| `instance_source=instance_pool` | 现有规则原封不动：`instance_pool` ≥1、`(addr,port)` 去重、≥1 个 `weight>0`；空池仍 422；`k8s_pool_name` 若传入则保留（休眠，不生效） |
| `instance_source=k8s_pool` | `k8s_pool_name` 必填；`instance_pool` 休眠（不参与校验） |
| 任意模式，请求体携带 `k8s_instance_pool` | 422（只读字段） |
| PATCH 改 `instance_source` / `k8s_pool_name` | 支持（模式切换）；切换后有效池变化，经 syncHook 事务内同步下游 cluster 派生池 |
| provider 引用尚不存在的 pool | 允许；`k8s_instance_pool` 为空直至首次 PUT（池不存在 ≡ 零实例） |

### 2.3 有效池口径

```
effective pool = instance_source == "k8s_pool" ? k8s_instance_pool : instance_pool
```

cluster 创建快照、provider 更新同步（syncer）、相等比较等一切下游一律改读有效池。cluster 引用 `k8s_pool` 模式 provider 的接口行为不变。

### 2.4 响应示例（k8s_pool 模式）

```json
{
  "name": "svc-a-provider",
  "instance_source": "k8s_pool",
  "k8s_pool_name": "svc-a",
  "instance_pool": [],
  "k8s_instance_pool": [
    {"addr": "10.0.0.1", "port": 8000, "weight": 100},
    {"addr": "10.0.0.2", "port": 8000, "weight": 100}
  ],
  "models": ["deepseek-chat"],
  "create_time": 1716883200,
  "update_time": 1716883200
}
```

---

## 3. InnerAPI 新增：`/k8s_pools`

发现组件（service-controller）专用写入通道，机器 token 鉴权，独立于 OpenAPI 的人写面。

### 3.1 端点清单

| 方法 | 端点 | 行为 |
|------|------|------|
| PUT | `/inner-api/v1/k8s_pools/{name}/instances` | 全量替换、幂等 upsert（条目不存在则创建）；body 为 Instance 数组（`addr`/`port` 必填，`weight` 缺省 100） |
| GET | `/inner-api/v1/k8s_pools/{name}` | pool 条目：`name` + `instances` + `instance_count` + `last_sync_time`（取行 `updated_at`） |
| GET | `/inner-api/v1/k8s_pools` | 全部 pool：`[{"name", "instance_count", "last_sync_time"}]` |
| DELETE | `/inner-api/v1/k8s_pools/{name}` | **无引用保护**：删除并把引用者的 `k8s_instance_pool` 置空（池不存在 ≡ 零实例） |

### 3.2 请求/响应示例

`PUT /inner-api/v1/k8s_pools/svc-a/instances` 请求体：

```json
[
  {"addr": "10.0.0.1", "port": 8000, "weight": 100},
  {"addr": "10.0.0.2", "port": 8000, "weight": 100}
]
```

`GET /inner-api/v1/k8s_pools/svc-a` 响应（Data 内容）：

```json
{
  "name": "svc-a",
  "instances": [
    {"addr": "10.0.0.1", "port": 8000, "weight": 100},
    {"addr": "10.0.0.2", "port": 8000, "weight": 100}
  ],
  "instance_count": 2,
  "last_sync_time": 1716883200
}
```

`GET /inner-api/v1/k8s_pools` 响应（Data 内容）：

```json
{
  "list": [
    {"name": "svc-a", "instance_count": 3, "last_sync_time": 1716883200},
    {"name": "svc-b", "instance_count": 0, "last_sync_time": 1716883200}
  ]
}
```

### 3.3 同步事务与错误处理

- PUT 与 DELETE 共用 fan-out，在 `itxn` 单事务内完成：写 `k8s_pools` 行 → 查出所有 `instance_source=k8s_pool && k8s_pool_name==name` 的 provider → 逐个刷新其 `k8s_instance_pool` → 逐个触发 cluster 派生池同步（复用 syncer 同步函数）；部分失败整个事务回滚。
- cluster_table version 由 `ExportConfig` MD5 签名机制在下次导出自动 bump，无需手工干预。
- 鉴权沿用 router 级 `McUserProbe` 中间件；如现有 InnerAPI 有 scope/动作细分，为 k8s_pools 域配独立 action，对齐 `epp_data` 域既有做法。

---

## 4. 数据库变更

| 表 | 变更 | 文件 |
|---|------|------|
| `providers` | 加 `instance_source` VARCHAR(32) NOT NULL DEFAULT 'instance_pool'（P0）、`k8s_pool_name` VARCHAR(255) NULL、`k8s_instance_pool` JSON NULL（P1） | `db_ddl.sql`、`db_ddl_sqlite.sql` |
| `k8s_pools` | 新表：`name` 唯一键、`instances` JSON、`created_at`/`updated_at`（P1） | 同上 |

存量行取默认值即现状，零数据迁移。

---

## 5. 文档同步项

- `design-docs/api-define/OpenAPI接口定义/providers.md`：§1 数据模型字段表 +3 行（`k8s_instance_pool` 只读标注）、§2.1/§2.4 执行逻辑补条件校验与 422 场景、§3 校验规则追加条件校验表、响应示例更新。
- `design-docs/api-define/InnerAPI接口定义/k8s-pools.md`（新增）：四端点定义、请求/响应示例、鉴权、错误码。
- `design-docs/api-define/InnerAPI接口定义/02-interface-list.md` 清单编号与 `README.md` 索引。
- `design-docs/sys-design/模型层设计文档.md`（provider 域）与 `details/provider与cluster概念分离.md`。
