# k8s_pools 接口

K8s 实例池（`k8s_pools`）是控制面内独立的实例集合资源，成员列表的**唯一写入方是 K8s 发现组件**（service-controller）。引用该池的 provider（`instance_source=k8s_pool`）上 `k8s_instance_pool` 为系统维护的只读镜像；人通过 OpenAPI `/providers` 维护池引用（`instance_source` / `k8s_pool_name`），不触碰池成员。

与导出类接口不同，本域为资源读写接口（PUT/GET/DELETE），不支持 `version` 增量同步参数。

## 1. 接口信息

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | K8s 实例池成员维护与查询 | 供发现组件写入发现的实例快照；控制台/运维只读查询同步状态 |
| 端点前缀 | `/k8s_pools` | - |
| 鉴权 | router 级 McUserProbe + 端点级 feature/action 授权 | k8s_pools 域独立授权项；GET=Read、PUT=Update、DELETE=Delete |

| 序号 | 端点 | Method | 功能描述 |
|------|------|--------|----------|
| 1 | `/inner-api/v1/k8s_pools/{name}/instances` | PUT | 全量替换 pool 实例列表（幂等 upsert） |
| 2 | `/inner-api/v1/k8s_pools/{name}` | GET | 查询单个 pool 条目 |
| 3 | `/inner-api/v1/k8s_pools` | GET | 查询全部 pool 列表 |
| 4 | `/inner-api/v1/k8s_pools/{name}` | DELETE | 删除 pool（无引用保护） |

## 2. 全量替换实例列表

**基本信息**

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 全量替换指定 pool 的实例列表 | 条目不存在则创建（幂等 upsert）；可任意重试 |
| 端点 | `/inner-api/v1/k8s_pools/{name}/instances` | - |
| Method | PUT | - |

**输入参数（URI）**

| 参数名 | 类型 | 参数含义 | 必填 | 合法性条件 |
|--------|------|----------|------|------------|
| name | string | pool 名称 | Y | 长度 1-64 字符；仅允许字母、数字、`_`、`-`、`.`；不能以 `.`、`-`、`_` 开头或结尾；不能包含空白字符；与 K8s Service 名无耦合（Service→池名映射由发现组件自行约定） |

**输入参数（Body）**

Instance 数组（全量快照，空数组合法，表示"零实例"）：

| 参数名 | 类型 | 参数含义 | 必填 | 合法性条件 |
|--------|------|----------|------|------------|
| addr | string | 实例地址 | Y | 类型为 [Hostname](../OpenAPI接口定义/00-common.md#1-主机名hostname) |
| port | int | 实例端口 | Y | 类型为 [Port](../OpenAPI接口定义/00-common.md#3-网络端口port) |
| weight | int | 实例权重 | N | 取值范围 [0,100]；默认 `100` |

同一 pool 内 `(addr, port)` 组合不能重复。

**请求示例**

```shell
curl -X PUT "http://api-server:port/inner-api/v1/k8s_pools/svc-a/instances" \
  -H "Authorization:Token TOKEN_STRING" \
  -H "Content-Type: application/json" \
  -d '[
    {"addr": "10.0.0.1", "port": 8000, "weight": 100},
    {"addr": "10.0.0.2", "port": 8000, "weight": 100}
  ]'
```

**返回数据（Data内容）**

更新后的 pool 条目，结构同 §3 查询单个 pool 条目。

## 3. 查询单个 pool 条目

**基本信息**

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 查询指定 pool 的条目（实例列表 + 同步状态） | - |
| 端点 | `/inner-api/v1/k8s_pools/{name}` | - |
| Method | GET | - |

**输入参数（URI）**

| 参数名 | 类型 | 参数含义 | 必填 | 合法性条件 |
|--------|------|----------|------|------------|
| name | string | pool 名称 | Y | 同 §2 |

**返回数据（Data内容）**

| 字段 | 类型 | 说明 |
|------|------|------|
| name | string | pool 名称 |
| instances | []Instance | 当前实例列表（结构同 §2 Body 元素） |
| instance_count | int | 实例数量 |
| last_sync_time | int64 | 最近同步时间（unix 秒），取条目 `updated_at` |

**成功返回示例**

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "name": "svc-a",
        "instances": [
            {"addr": "10.0.0.1", "port": 8000, "weight": 100},
            {"addr": "10.0.0.2", "port": 8000, "weight": 100}
        ],
        "instance_count": 2,
        "last_sync_time": 1716883200
    },
    "WorkMode": "ModeNormal"
}
```

pool 不存在时返回 404。

## 4. 查询全部 pool 列表

**基本信息**

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 查询全部已存在的 pool 及各 pool 的实例数与最近同步时间 | - |
| 端点 | `/inner-api/v1/k8s_pools` | - |
| Method | GET | - |

**输入参数**

无。

**返回数据（Data内容）**

| 字段 | 类型 | 说明 |
|------|------|------|
| list | []object | pool 摘要列表，元素含 `name`、`instance_count`、`last_sync_time`（含义同 §3） |

**成功返回示例**

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "list": [
            {"name": "svc-a", "instance_count": 3, "last_sync_time": 1716883200},
            {"name": "svc-b", "instance_count": 0, "last_sync_time": 1716883200}
        ]
    },
    "WorkMode": "ModeNormal"
}
```

## 5. 删除 pool

**基本信息**

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 删除指定 pool | **无引用保护**：被 provider 引用时同样允许删除 |
| 端点 | `/inner-api/v1/k8s_pools/{name}` | - |
| Method | DELETE | - |

**输入参数（URI）**

| 参数名 | 类型 | 参数含义 | 必填 | 合法性条件 |
|--------|------|----------|------|------------|
| name | string | pool 名称 | Y | 同 §2 |

**执行逻辑**

1. 删除 `k8s_pools` 中该 pool 条目。
2. 将所有 `instance_source=k8s_pool && k8s_pool_name==name` 的 provider 的 `k8s_instance_pool` 置为空列表。
3. 逐个触发上述 provider 的 cluster 派生池同步（清空）。

provider 视角下"池不存在"与"池存在但零实例"是同一状态（有效池均为空）。

**返回数据（Data内容）**

Data 为 null。pool 不存在时返回 404。

## 6. 同步事务语义

§2 的 PUT 与 §5 的 DELETE 共用同一 fan-out，在单个数据库事务内完成：

1. 写 `k8s_pools` 行（PUT 更新 instances / DELETE 删行）；
2. 查出所有 `instance_source=k8s_pool && k8s_pool_name==name` 的 provider；
3. 逐个刷新其 `k8s_instance_pool`（PUT → 新列表；DELETE → 空列表）；
4. 逐个触发 cluster 派生池同步（复用 provider 更新时的实例池同步链路，cluster_table version 由导出签名机制自动 bump）。

任一步失败整个事务回滚。多个 provider 引用同一 pool（N:1）时共享同一份实例列表，无额外成本。

## 7. 错误码

| ErrNum | 场景 |
|--------|------|
| 200 | 成功 |
| 401 | 鉴权失败 |
| 402 | 没有调用权限（feature/action 授权不足） |
| 404 | GET/DELETE 的 pool 不存在 |
| 422 | 参数不合法（pool 名称非法、body 非合法 Instance 数组、`(addr, port)` 重复、`weight` 越界等） |
| 500 | 其他业务逻辑错误（含同步事务回滚） |
