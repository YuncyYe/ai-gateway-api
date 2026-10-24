# Provider 与 Cluster 概念分离

## 1. 背景

在 `ai-gateway-api` 控制面中，`/clusters` 资源长期同时承担两类职责：

- **Provider 职责**：下游模型提供商标识、后端实例池、模型端点、API Key 明文等。
- **Cluster 职责**：路由/转发策略、连接超时、健康检查等 BFE 集群参数。

这种混合导致：

- 同一 provider 被多个 cluster 引用时，`instance_pool`、`keys`、`model_endpoint` 重复配置。
- cluster 接口暴露 API Key 明文，且无法通过引用复用 provider 的 key。
- `model-prices` 的 `provider` 字段语义不清。
- 新增 provider 类型或协议时，cluster 模型不断膨胀。

## 2. 设计目标

| 目标 | 说明 |
|------|------|
| 职责分离 | `provider` = “我是谁、我能访问哪些模型、我的后端和密钥是什么”；`cluster` = “我如何转发、用哪些模型、key 权重如何分配”。 |
| 独立生命周期 | provider 可独立创建、更新、删除；cluster 通过引用 provider 获取后端能力。 |
| 数据安全 | cluster 不再存储 key 明文，只通过 name 引用 provider 中的 key。 |
| BFE 无感知 | 生成给 BFE 的配置保持原结构；变化仅在 `ai-gateway-api` 内部做“provider → 老配置”的转换。 |
| 弱引用 model-prices | `/model-prices` 的 `provider` 字段不再强制引用 `/providers`，降低配置顺序约束。 |

## 3. 概念定义

| 概念 | 定义 |
|------|------|
| **Provider** | 一个模型提供方，包含接入端点、可用模型、API Key、实例池、支持的协议。 |
| **Cluster** | 一个转发集群，决定把流量按什么模型、什么权重、什么策略转发到某个 provider。 |
| **Model Price** | 一个 (provider, model, mode) 的价格记录，`provider` 仅作为价格归集标识，不强制引用已存在的 provider。 |

## 4. 数据模型

### 4.1 Provider

```json
{
  "name": "deepseek",
  "description": "DeepSeek 官方 API",
  "model_endpoint": {
    "schema": "https",
    "uri": "/v1/models"
  },
  "models": ["deepseek-chat", "deepseek-coder"],
  "keys": [
    {"name": "key-primary", "key": "sk-aaaaaaaaaaaa"},
    {"name": "key-secondary", "key": "sk-bbbbbbbbbbbb"}
  ],
  "instance_source": "instance_pool",
  "instance_pool": [
    {"name": "backend-1", "addr": "api.deepseek.com", "weight": 100, "port": 443}
  ],
  "k8s_pool_name": null,
  "k8s_instance_pool": [],
  "model_protocols": ["openai"],
  "create_time": 1716883200,
  "update_time": 1716883200
}
```

> 上例为 `instance_source=instance_pool`（默认）形态：`k8s_pool_name` 为 `null`、`k8s_instance_pool` 为空数组。`instance_source=k8s_pool` 形态见 [providers.md](../../api-define/OpenAPI接口定义/providers.md)——此时 `instance_pool` 休眠保留（可为 `[]`），`k8s_pool_name` / `k8s_instance_pool` 由系统填充。

### 4.2 Cluster（新）

```json
{
  "name": "my-cluster",
  "description": "示例集群",
  "basic": { "...": "..." },
  "sticky_sessions": { "...": "..." },
  "passive_health_check": { "...": "..." },
  "llm_config": {
    "models": ["deepseek-chat", "deepseek-coder"],
    "model_mappings": [
      {"source_model": "gpt-4", "target_model": "deepseek-chat"}
    ],
    "keys": [
      {"name": "key-primary", "weight": 70},
      {"name": "key-secondary", "weight": 30}
    ],
    "key_policy": {
      "strategy": "weighted_random",
      "max_retries": 3,
      "retry_backoff_initial": 500,
      "retry_backoff_max": 5000
    },
    "key_affinity": {
      "enabled": true,
      "ttl": 600,
      "redis_prefix": "bfe:ai:key_affinity",
      "penalty_enable": true
    },
    "provider": "deepseek",
    "match_prefix": "deepseek/",
    "strip_prefix": true
  }
}
```

### 4.3 字段变更对比

| 变更 | 字段 | 说明 |
|------|------|------|
| 移除（cluster 顶层） | `instance_pool` | 下沉到 provider。 |
| 移除（`llm_config`） | `model_endpoint` | 下沉到 provider。 |
| 移除（`llm_config`） | `provider_type` | 由 provider 的 `model_protocols` 替代。 |
| 改造（`llm_config.keys`） | `keys` | 只保留 `name` + `weight`，`name` 必须对应 provider 中存在的 key。 |
| 改造（`llm_config.models`） | `models` | 须为 provider `models` 的子集。 |
| 强化（`llm_config.provider`） | `provider` | 必填，且必须引用已存在的 provider。 |
| 保留 | `model_mappings`、`key_policy`、`match_prefix`、`strip_prefix` | 行为不变。 |
| 新增 | `key_affinity` | 基于 Redis + `ClientKeyId` 的会话级 Key 亲和性配置，包含 `enabled`、`ttl`、`redis_prefix`、`penalty_enable`。 |

### 4.4 实例供给方式（`instance_source`）

K8s 服务发现场景下，provider 引入 `instance_source` 声明实例成员的来源，配合 `k8s_pool_name`（池引用）与 `k8s_instance_pool`（只读镜像）三个字段：

| 概念 | 定义 |
|------|------|
| **实例供给方式（`instance_source`）** | provider 实例成员的来源：`instance_pool`（人维护，默认）/ `k8s_pool`（发现组件维护） |
| **有效池（effective pool）** | provider 对下游暴露的实例集合：`instance_source == "k8s_pool" ? k8s_instance_pool : instance_pool`。cluster 创建快照、实例池同步、导出等一切下游的唯一消费口径 |
| **所有权边界** | "创建归人、成员归发现"：provider 资源由人创建；K8s 池成员对控制台只读，摘流等运维动作走 K8s 侧手段（摘 endpoint/打标签） |

- **模式语义**：`instance_pool` 模式下 `instance_pool` 维持现状规则（≥1、`(addr,port)` 去重、≥1 个 `weight>0`），`k8s_pool_name` 休眠保留、不生效；`k8s_pool` 模式下 `instance_pool` 休眠保留、不参与有效池与校验（人工兜底），`k8s_pool_name` 必填。两个休眠字段均在非激活模式下保留原值、切回激活模式时自动恢复生效。
- **`/k8s_pools` 通道**：K8s 池（`k8s_pools`）是控制面内独立资源，成员列表的唯一写入方是发现组件（service-controller），经 InnerAPI `/k8s_pools` 四端点写入；provider 侧 `k8s_instance_pool` 为系统维护的只读镜像，人通过 OpenAPI `/providers` 维护池引用（`instance_source` / `k8s_pool_name`），不触碰池成员。`k8s_pool_name` 与 K8s Service 名无耦合，Service→池名映射由发现组件自行约定。
- **空池语义**：合法空池只出现在 `k8s_pool` 模式。syncer 删除空池早退，有效池为空时同步清空引用 cluster 的派生池（含空列表）；数据面行为为导出空条目、BFE 加载接受、该 cluster 请求 500（`BK_NO_BACKEND`），由告警覆盖。provider 可引用尚不存在的 pool（池不存在 ≡ 零实例）。

## 5. 接口变化

### 5.1 新增 `/providers`

| 方法 | 端点 | 含义 |
|------|------|------|
| POST | `/providers` | 创建 provider |
| GET | `/providers` | 分页/过滤查询 provider 列表 |
| GET | `/providers/{provider_name}` | 查询单个 provider |
| PATCH | `/providers/{provider_name}` | 部分更新 provider；若修改 `keys`/`models` 导致已有 cluster 引用失效，返回 409 |
| DELETE | `/providers/{provider_name}` | 删除 provider |
| POST | `/providers/tools/discover-models` | 触发模型发现 |

`PATCH /providers/{provider_name}` 请求体**不能包含 `name`**，名称由 URI 路径参数唯一指定，若请求体携带 `name` 返回 422。

部分更新 provider 时，如果请求体显式修改了 `instance_pool`、`keys` 或 `models`：

- 有效池变化（含变为空）经同步链路（syncer，事务内）刷新所有引用该 provider 的 cluster 派生实例池（EPP 子集群除外）。
- `keys` 删除/重命名会校验无 cluster 仍引用旧 key name；否则返回 409。
- `models` 删除会校验无 cluster 仍引用已被移除的 model；否则返回 409。

**实例供给方式（`instance_source` / `k8s_pool_name`）更新语义**：

- 两字段均可通过 PATCH 修改，包括**模式切换**（`instance_pool` ↔ `k8s_pool`）；切换后有效池随之变化，经同步链路自动刷新引用 cluster 的派生实例池（空有效池同样同步清空）。
- 切换时点校验：`instance_source=instance_pool` 要求 `instance_pool` 非空；`instance_source=k8s_pool` 要求 `k8s_pool_name` 非空。不满足返回 422。
- `instance_source=k8s_pool` 时 PATCH `instance_pool` 仅作休眠保留（不参与有效池、不触发同步），切回 `instance_pool` 模式时自动恢复生效。
- `k8s_instance_pool` 为只读镜像，请求体携带返回 422；其内容由系统根据 `/k8s_pools` 的变更自动刷新。

删除 provider 前，须校验无 `/clusters` 引用；`/model-prices` 中的同名 provider 不再作为阻塞条件。

### 5.2 `/clusters` 改造

URL 与 HTTP Method 不变，请求/响应体变化：

- 不再包含 `instance_pool`。
- `llm_config` 不再包含 `model_endpoint`、`provider_type`。
- `llm_config.provider` 变为必填。
- `llm_config.keys` 元素结构变为 `{name, weight}`。
- `PATCH /clusters/{cluster_name}` 请求体**不能包含 `name`**，名称由 URI 路径参数唯一指定，若请求体携带 `name` 返回 422。
- `DELETE /clusters/{cluster_name}` 在级联清理前，会检查该集群是否被 **AI 路由规则**（global / entity / api-key 级别）引用；若被引用，删除失败并返回引用冲突错误。

### 5.3 `/model-prices` 改造

- `provider` 字段含义保持为“Provider / Cluster 标识”，仅用于价格归集和查找；不再强制引用 `/providers` 中已存在的 provider。
- 新增 `GET /model-prices/actions/get-providers`：返回 `/model-prices` 数据中所有 `provider` 名称的去重列表。
- 导入 `model-list.yaml` 时，不再校验 provider 存在性，未知 provider 可正常写入。

## 6. 代码分层影响

### 6.1 模型层

- 新增 `model/iprovider/`：Provider 实体、CRUD、模型发现、引用检查。实例池校验按 `instance_source` 条件化：默认模式规则原封不动，`k8s_pool` 模式 `instance_pool` 不参与校验（休眠保留）。
- 抽出 `iprovider.EffectiveInstancePool`（有效池：`instance_source == "k8s_pool" ? k8s_instance_pool : instance_pool`）与 `ClusterManager.SyncProviderEffectivePool`（从 `ProviderInstancePoolSyncer` 的 hook 体中提取），消费点：
  - cluster 创建引用 provider 时的 pool/sub-cluster 快照；
  - provider 更新时同步引用 cluster 的派生池（syncer 删除空池早退，空有效池同样同步清空）；
  - 实例池相等比较（升级为先比较有效池）。
- 新增 `model/ik8s_pool/`：K8s 实例池域，manager 依赖 `itxn.TxnStorager`、provider storager、`ClusterManager`（依赖注入，禁直连 `stateful.Default*`）；`/k8s_pools` PUT/DELETE 的单事务 fan-out 复用 `ClusterManager.SyncProviderEffectivePool` 触发各引用 provider 的 cluster 派生池同步。
- `model/icluster_conf/ClusterManager` 注入 `iprovider.ProviderStorager`：
  - 创建/更新 cluster 时校验 provider 存在、models 子集、keys name 存在性。
  - 根据 provider 有效池自动生成/更新 pool/sub_cluster。
  - 以 hook 形式向 `ProviderManager.UpdateProvider` 注册 `ProviderInstancePoolSyncer`、`ProviderKeyRefChecker`、`ProviderModelRefChecker`：provider 更新时同步实例池，并反向校验 cluster 对 key / model 的引用完整性。
- `model/icluster_conf/exporter.go` 在生成 `AIConf` 时：
  - 从 provider 读取有效池生成 BFE 实例池/子集群/集群。
  - 按 `name` join provider `keys` 与 cluster `llm_config.keys` 生成带明文的 `AIConf.Keys`。
  - 将 provider `model_protocols` 透传到 `AIConf.ModelProtocols`，供 BFE 做请求协议风格匹配。
- `model/imodel_price/Manager` 不再注入 `iprovider.ProviderStorager` 校验 model-prices 的 provider 引用；新增 `ListProviders(ctx)` 方法用于 `GET /model-prices/actions/get-providers`。

### 6.2 存储层

- 新增 `storage/rdb/provider/`：实现 `iprovider.ProviderStorager`。
- 新增 `table_providers.go` DAO。
- 新增 `storage/rdb/k8s_pool/`：实现 `ik8s_pool` 域 K8s 池存储（`k8s_pools` 表）。
- `storage/rdb/cluster_conf/pool.go` 数据来源不变（仍从 `pools` 表读取），但写入源头由 cluster 顶层变为 provider 有效池。
- `clusters` 表结构不变；`llm_config` JSON 结构变化。
- `storage/rdb/model_price/` 新增 `ListProviders(ctx) ([]string, error)` 方法，按 `provider` 字段聚合去重。

### 6.3 接口层

- 新增 `endpoints/openapi_v1/provider/`。
- 新增 `endpoints/innerapi_v1/k8s_pools/`：`/k8s_pools` 四端点，注册进 `endpoints/innerapi_v1/endpoints.go`。
- 移除 `endpoints/openapi_v1/tool/`（`/tools/get-models-from-provider` 能力由 `/providers/tools/discover-models` 替代）。
- 移除 `/model-provider-types`；模型访问协议枚举作为 `providers.model_protocols` 的合法性约束在 `providers.md` 中定义。
- `endpoints/openapi_v1/model_price/` 新增 `GET /model-prices/actions/get-providers` handler。

## 7. 数据库设计

### 7.1 新增 `providers` 表

```sql
CREATE TABLE `providers` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `description` varchar(1024) NOT NULL DEFAULT 'no desc',
  `model_endpoint` text,
  `models` text,
  `keys` text,
  `instance_pool` text,
  `instance_source` varchar(32) NOT NULL DEFAULT 'instance_pool' COMMENT '实例供给方式：instance_pool/k8s_pool',
  `k8s_pool_name` varchar(255) DEFAULT NULL COMMENT '引用的 K8s 实例池名称',
  `k8s_instance_pool` text COMMENT 'K8s 实例池只读镜像（JSON）',
  `model_protocols` text,
  `created_at` datetime NOT NULL,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `name_index` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

存量行由 `instance_source` 默认值兜底，即现状行为，零数据迁移。

### 7.2 `k8s_pools` 表（新增）

```sql
CREATE TABLE `k8s_pools` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `instances` text COMMENT '实例列表（JSON，发现组件全量维护）',
  `created_at` datetime NOT NULL,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `name_index` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

`name` 为唯一键；`instances` JSON 由发现组件经 `/k8s_pools` 全量替换，同一份实例列表可被多个 provider 引用（N:1）。

### 7.3 `clusters` 表变化

- `llm_config` JSON 移除 `model_endpoint`、`provider_type`。
- `llm_config.keys` 改为 `{name, weight}` 引用结构。
- `llm_config.provider` 必填，逻辑外键到 `providers.name`。

### 7.4 `model_prices` 表变化

- `provider` 字段为普通字符串，仅作为价格归集标识，不建立外键约束；保留普通索引以支持查询与聚合。

### 7.5 关系

- `providers.name` ← `clusters.llm_config.provider`（强引用）
- `providers.name` ← `model_prices.provider`（按名称弱关联，非强制）
- `k8s_pools.name` ← `providers.k8s_pool_name`（按名称弱关联，N:1，无删除保护）
- 删除 provider 前须校验无 `clusters` 引用；`model_prices` 同名记录不再阻塞删除。

## 8. BFE 配置生成转换

BFE 接收到的配置结构和字段与重构前完全一致：

| BFE 配置项 | 来源（新模型） |
|------------|----------------|
| 实例池 / 子集群 / 集群 | `cluster` + provider 有效池（`instance_source == "k8s_pool" ? k8s_instance_pool : instance_pool`） |
| `AIConf.Models` | `cluster.llm_config.models` |
| `AIConf.ModelMappings` | `cluster.llm_config.model_mappings` |
| `AIConf.Keys` | `provider.keys`（key 明文） + `cluster.llm_config.keys`（weight）按 name join |
| `AIConf.KeyPolicy` | `cluster.llm_config.key_policy` + `cluster.llm_config.key_affinity`（`SessionAffinity*` 字段来自 `key_affinity`） |
| `AIConf.Provider` | `cluster.llm_config.provider` |
| `AIConf.MatchPrefix` / `StripPrefix` | `cluster.llm_config.match_prefix` / `strip_prefix` |
| `AIConf.ModelProtocols` | `provider.model_protocols`（按 `cluster.llm_config.provider` 引用透传） |
| `AIConf.ModelTable` | 由 `provider` 查询 `model-prices` 自动填充 |

## 9. 配置顺序

推荐顺序（`/model-prices` 可独立维护）：

```
/providers → /model-prices → /clusters → 路由规则
```

> 说明：`/model-prices` 与 `/providers` 之间为弱引用关系，实际配置时无需等待 `/providers` 数据就绪即可写入 `/model-prices`。

## 10. 数据迁移

### 10.1 自动迁移（推荐）

1. 扫描现有 cluster 记录。
2. 以 `cluster.llm_config.provider`（或 fallback 到 `provider_type`）作为 provider name。
3. 若 provider 不存在，自动创建：
   - `name` = provider
   - `instance_pool` = 原 cluster `instance_pool`
   - `model_endpoint` = 原 `llm_config.model_endpoint`
   - `models` = 原 `llm_config.models`
   - `keys` = 原 `llm_config.keys` 去掉 `weight`
   - `model_protocols` = 根据 `provider_type` 映射
4. 更新 cluster：删除 `instance_pool`、`model_endpoint`、`provider_type`；`keys` 改为 `{name, weight}`。
5. `model-prices` 中 `provider` 字段保持原值，不再强制要求对应 provider 存在。

### 10.2 兼容性

- OpenAPI 层面为破坏性变更。
- 建议以产品大版本或 API 版本切换发布。
- 若需平滑过渡，可在 `/v1/clusters` 保留只读兼容层。

## 11. 风险与注意事项

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 破坏性 API 变更 | 现有调用方需修改 | 提前发版说明；提供迁移指南；必要时保留只读兼容层。 |
| Key 明文迁移 | 迁移脚本处理敏感数据 | 日志脱敏；确保 key 加密存储不变。 |
| 多 cluster 共享 provider | 修改 provider 影响所有引用 cluster | `instance_pool` 变更自动同步到 cluster；`keys`/`models` 变更反向校验引用完整性，冲突时返回 409；删除时强制无引用。 |
| 更新接口误传 `name` | 调用方可能习惯在 Body 中重复名称 | 接口层显式拒绝请求体中的 `name`，返回 422；文档明确名称由 URI 决定。 |
| 删除 cluster 时存在路由规则引用 | 级联删除可能导致路由指向不存在集群 | 删除前检查 AI 路由规则引用，返回引用冲突错误，要求先解除引用。 |
| 模型发现失败 | discover-models 可能失败 | 辅助能力，支持手动维护 `models`。 |
| provider 命名冲突 | 多个 cluster 可能指向同一物理 provider | 提供手动合并能力；自动迁移以首次出现为准并告警。 |
| 历史 price 记录与实际 provider 脱节 | 成本计算时可能找不到对应 provider 的 metadata | 通过 `GET /model-prices/actions/get-providers` 与 `GET /providers` 对比，定期识别并补录缺失 provider。 |

## 12. 待确认事项

| 序号 | 事项 | 建议 |
|------|------|------|
| 1 | API 版本策略 | 是否需要在 `/v2` 引入新语义，保留 `/v1` 兼容层？ |
| 2 | `model_protocols` 默认值 | 是否允许不传，默认 `["openai"]`？ |
| 3 | 模型发现认证 key | 由调用方通过 `apikey` 参数传入，认证头风格由 `model_protocol` 决定。 |
| 4 | provider 是否保留 `provider_type` | 本期不保留；由 `model_protocols` + `name` 共同表达。 |
