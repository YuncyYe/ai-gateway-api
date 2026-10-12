# EPP 运行时参数：ai-gateway-api 控制面设计变更说明

> 本文档只描述 `ai-gateway-api` 控制面为接入 EPP 运行时参数（`epp_runtime`）所做的设计改造。接口契约见本目录 `api-changes.md`；摘要见本目录 `change-summary.md`。设计来源：`218-ai-gateway-api支持EPP非调度参数设计文档v0.2.md`；既有 EPP 调度对接见 `../2026-09-08-epp-scheduling-integration/design-changes.md`；BFE 侧定义见 `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go`。

## 1. 概述

### 1.1 变更目标

在 OpenAPI 写入口、模型层、存储层与 `server_data_conf` 导出中接入 BFE 侧 EPP 运行时参数：

1. 新增可选对象 `epp_runtime`，本期对外开放 `check`（→ `GslbBasic.EPPCheck`）与 `timeout`（→ `GslbBasic.EPPTimeout`）。
2. 固定下发 `EPPTLS{Plaintext:true}`（统一明文，取代 BFE legacy `{Insecure:true}` 默认）与 `EPPBreaker{Disabled:true}`（关闭熔断），二者不对用户暴露。
3. `check` / `timeout` 未设置的子字段按 BFE 默认补齐并落盘。
4. 写入时做 BFE 等价校验，非法配置 4xx 拒绝。
5. 新增 `clusters.epp_runtime` 列（JSON text，可空），沿用 `epp_config` 的原始 JSON 存取模式。
6. 未配置时对 WRR cluster 零影响；老库加列即可。

### 1.2 可复用基础（代码事实，已核实）

| 能力 | 位置 | 说明 |
|------|------|------|
| 请求结构体 | `endpoints/openapi_v1/product_cluster/create.go:55-64` | `UpsertParam`，`create` 与 `update` 共用；`BalanceMode *string` / `EppConfig *json.RawMessage` 是新增字段的范式 |
| 原始 JSON 归一 | `create.go:399-411` | `normalizeEppConfigRaw`：`nil`/`""`/`"null"` → `nil`（未设置不落盘） |
| 请求→模型映射 | `create.go:244-296`（`clusterParamControlModel`）、`update_basic.go:128-207`（`clusterParamControlModel4Update`） | `EppConfig: normalizeEppConfigRaw(param.EppConfig)` |
| cluster 写入校验入口 | `model/icluster_conf/epp.go:44-88` | `validateClusterBalanceConfig`：`epp_config` 非空即校验；由 `CreateCluster`（`cluster.go:446-455`）/`UpdateCluster`（`cluster.go:772-783`）调用 |
| 严格解析范式 | `model/epp_pool/epp_config.go:89-105` | `ParseEppConfig` 用 `json.Decoder` + `DisallowUnknownFields`，空串返回 `nil` |
| 模型字段（原始 JSON 模式） | `model/icluster_conf/cluster.go:150-156` / `:311-317` | `ClusterParam.EppConfig *string` / `Cluster.EppConfig string`；`getBalanceMode()` 在 `:329-336` |
| 导出填充 | `model/icluster_conf/cluster.go:1278-1310` | `NewBfeClusterConf` 构造 `GslbBasic`；`if balanceMode == BalanceModeEPP` 分支（`:1299-1310`） |
| 操作日志快照 | `model/icluster_conf/cluster_operation_log.go:72-136,331-341` | `clusterParamToMap` / `clusterToMap` + `eppConfigSnapshotValue`（JSON 字符串→纯 `interface{}`，供脱敏递归） |
| DAO | `storage/rdb/internal/dao/table_clusters.go:60-62 / 131-133` | `TCluster` 值字段 / `TClusterParam` 指针字段，`db` tag 驱动 |
| DAO nil-skip | `storage/rdb/internal/dao/internal/builder.go:43-96` | 指针为 nil 时跳过该列（PATCH 语义） |
| 存储映射 | `storage/rdb/cluster_conf/cluster.go:254-316 / 338-397` | `newCluster` / `newDaoClusterParam` 直传 `BalanceMode` / `EppConfig` |
| BFE 结构体 | `bfe/.../cluster_conf_load.go:475-529` | `EPPCheckConf` / `EPPTimeoutConf` / `EPPTLSConf` / `EPPBreakerConf` |
| BFE 默认值 | `bfe/.../cluster_conf_load.go:434-445` | `DefaultEPPCheckInterval` 等常量 |
| BFE 校验/补默认 | `bfe/.../cluster_conf_load.go:936-941,969-1117` | `GslbBasicConfCheck` + `EPPxxxConfCheck` |

### 1.3 现有缺口

| 缺口 | 现状 |
|------|------|
| 接口无 EPP 运行时字段 | `UpsertParam` 仅 `balance_mode` + `epp_config`；无 `EPPCheck`/`EPPTimeout`/`EPPTLS`/`EPPBreaker` 入口 |
| 模型/存储未建模 | `ClusterParam`/`Cluster`、`clusters` 表均无对应字段/列 |
| 导出缺失 | `NewBfeClusterConf` 的 `GslbBasic` 不填 `EPPTLS`/`EPPCheck`/`EPPTimeout`/`EPPBreaker` → BFE 回落 legacy `{Insecure:true}` → EPP 明文不可达 + warning |
| 无写入校验 | 无 duration/阈值校验入口，非法配置只能到 BFE reload 才失败（conf-agent 死循环风险） |

---

## 2. 字段设计与默认值

### 2.1 控制面模型（`model/icluster_conf/epp_runtime.go`，新增）

```go
package icluster_conf

// EppRuntimeParam 是 BFE 侧 EPP 运行时参数的控制面形态（原始 JSON 存取）。
// 本期仅开放 Check/Timeout；TLS/Breaker 固定下发，结构体保留注释备用。
type EppRuntimeParam struct {
    // TLS 当前阶段不支持配置（更新接口不接收），导出固定 plaintext=true。
    //TLS     *EppTLSParam     `json:"tls,omitempty"`
    Check   *EppCheckParam   `json:"check,omitempty"`
    Timeout *EppTimeoutParam `json:"timeout,omitempty"`
    // 熔断当前阶段不在控制面暴露，导出固定 disabled=true。
    //Breaker *EppBreakerParam `json:"breaker,omitempty"`
}

type EppCheckParam struct {
    Disabled         *bool   `json:"disabled,omitempty"`
    CheckInterval    *string `json:"check_interval,omitempty"`
    FailThreshold    *int    `json:"fail_threshold,omitempty"`
    Cooldown         *string `json:"cooldown,omitempty"`
    SuccessThreshold *int    `json:"success_threshold,omitempty"`
}

type EppTimeoutParam struct {
    Connect *string `json:"connect,omitempty"`
    Call    *string `json:"call,omitempty"`
}

// 后续开放时启用（结构体暂保留）：
//type EppTLSParam struct { Insecure *bool; CAFile *string; Plaintext *bool }
//type EppBreakerParam struct { Disabled *bool; WindowSize *int; MinVolume *int; ErrorRatePercent *int; OpenTimeout *string }
```

常量默认值（对齐 BFE `cluster_conf_load.go:434-445`）：

```go
const (
    DefaultEppCheckInterval    = "2s"
    DefaultEppFailThreshold    = 3
    DefaultEppCooldown         = "45s"
    DefaultEppSuccessThreshold = 2
    DefaultEppConnectTimeout   = "500ms"
    DefaultEppCallTimeout      = "3s"
)
```

### 2.2 字段映射与默认值

| OpenAPI | BFE `GslbBasic` | 类型 | 默认 | 校验 |
|---------|-----------------|------|------|------|
| `check.disabled` | `EPPCheck.Disabled` | `bool` | `false` | bool |
| `check.check_interval` | `EPPCheck.CheckInterval` | `*string` | `"2s"` | 正 duration |
| `check.fail_threshold` | `EPPCheck.FailThreshold` | `*int` | `3` | `>= 1` |
| `check.cooldown` | `EPPCheck.Cooldown` | `*string` | `"45s"` | 正 duration |
| `check.success_threshold` | `EPPCheck.SuccessThreshold` | `*int` | `2` | `>= 1` |
| `timeout.connect` | `EPPTimeout.Connect` | `*string` | `"500ms"` | 正 duration |
| `timeout.call` | `EPPTimeout.Call` | `*string` | `"3s"` | 正 duration |
| （无输入） | `EPPTLS.Plaintext` | `bool` | `true` | — |
| （无输入） | `EPPBreaker.Disabled` | `bool` | `true` | — |

> 默认补齐在**写入时**完成并落盘（`ApplyDefaults`），使 GET 回读即完整生效值；BFE 加载期对未设置字段同样补这些默认，二者等价。

### 2.3 解析与默认补齐

- `ParseEppRuntime(raw string) (*EppRuntimeParam, error)`：`strings.TrimSpace` 后为空 → `(nil, nil)`；否则 `json.Decoder` + `DisallowUnknownFields` 解析（未知字段即报错，覆盖 `tls`/`breaker`）。
- `(p *EppRuntimeParam) ApplyDefaults()`：对 `check`/`timeout` 中为 nil 的子字段填入 §2.2 默认；`check`/`timeout` 对象为 nil 时**不合成空对象**（保持"未设置不下发、由 BFE 补默认"的语义）。
- `(p *EppRuntimeParam) Marshal() (string, error)`：`json.Marshal` 得规范化 JSON 落盘。

---

## 3. 校验设计

在 cluster 写入路径复用 `validateClusterBalanceConfig` 的入口风格，新增运行时校验（`model/icluster_conf/epp_runtime.go`）：

```go
func (p *EppRuntimeParam) Validate() error // 字段级
func validateClusterEppRuntime(runtime *string) (*string, error) // 解析+校验+补默认+规范化，返回待落盘 JSON
```

规则（与 BFE `EPPCheckConfCheck` / `EPPTimeoutConfCheck` 对齐）：

| 字段 | 规则 | 错误消息（示例） |
|------|------|------------------|
| `check.check_interval` | `time.ParseDuration` 成功且 `> 0` | `epp_runtime.check.check_interval is not a valid positive duration` |
| `check.cooldown` | 同上 | `epp_runtime.check.cooldown ...` |
| `check.fail_threshold` | `>= 1` | `epp_runtime.check.fail_threshold must be >= 1` |
| `check.success_threshold` | `>= 1` | `epp_runtime.check.success_threshold must be >= 1` |
| `timeout.connect` | 正 duration | `epp_runtime.timeout.connect ...` |
| `timeout.call` | 正 duration | `epp_runtime.timeout.call ...` |
| 未知字段（`tls`/`breaker` 等） | 解析报错 | `epp_runtime: unknown field "tls"` |

- 校验错误统一经 `xerror.WrapParamErrorWithMsg(...)` 返回 4xx。
- **校验前移理由**：BFE reload 失败会导致 conf-agent 不切符号链接并以同因反复重试（死循环），必须在 api 写入时拦截非法配置。
- `epp_runtime` 非空即校验，与 `balance_mode` 无关（休眠保留时也校验，与 `epp_config` 一致）。

写入接线（`model/icluster_conf/cluster.go`）：

- `CreateCluster`（`:446-455` 区）在 `validateClusterBalanceConfig` 之后追加：
  ```go
  eppRuntimeRaw, err := validateClusterEppRuntime(param.EppRuntime) // old=nil
  if err != nil { return err }
  if param.EppRuntime != nil { *param.EppRuntime = *eppRuntimeRaw }
  ```
- `UpdateCluster`（`:772-783` 区）同法，追加 `oldEppRuntime`（未携带时保留存储值）。

---

## 4. 存储与数据库

### 4.1 模型字段

| 层 | 字段 | 类型 | 说明 |
|----|------|------|------|
| `ClusterParam` | `EppRuntime` | `*string` | 请求携带的原始 JSON（未设置 nil，DAO 跳过） |
| `Cluster` | `EppRuntime` | `string` | 读取的原始 JSON（`""` = 未设置） |

### 4.2 DAO 与存储映射

- `storage/rdb/internal/dao/table_clusters.go`：
  - `TCluster` 增 `EppRuntime string \`db:"epp_runtime"\``（`EppConfig` 之后）。
  - `TClusterParam` 增 `EppRuntime *string \`db:"epp_runtime"\``。
- `storage/rdb/cluster_conf/cluster.go`：
  - `newCluster` 增 `EppRuntime: dc.EppRuntime`。
  - `newDaoClusterParam` 增 `EppRuntime: param.EppRuntime`。
- DAO 为反射/`db` tag 驱动，新增 tag 自动参与 INSERT/SELECT/UPDATE，无需维护列清单。

### 4.3 DDL

`clusters` 表新增列（放在 `epp_config` 之后）：

```sql
-- db_ddl.sql（MySQL）
`epp_runtime` text COMMENT 'EPP运行时配置(JSON: check/timeout)',

-- db_ddl_sqlite.sql（SQLite）
epp_runtime TEXT,
```

### 4.4 迁移

**无 migration 框架**（沿用既定约定，见 `../2026-09-08-epp-scheduling-integration/design-changes.md §7`）：新列可空，存量数据无需迁移。

- 新装：全量 DDL 建表即含新列。
- 存量 SQLite 库：需 `scripts/deploy_local_sqlite.sh --reinit`（或在部署时手工执行 `ALTER TABLE clusters ADD COLUMN epp_runtime TEXT;`）。
- 存量 MySQL 库：`ALTER TABLE clusters ADD COLUMN epp_runtime text COMMENT 'EPP运行时配置(JSON: check/timeout)';`

> `db_ddl_report_*.sql` 不含 `clusters` 表，无需变更；`output/db_ddl*.sql` 为构建产物，不手改。

---

## 5. 导出设计

在 `NewBfeClusterConf`（`model/icluster_conf/cluster.go:1278-1310`）的 EPP 分支（`if balanceMode == BalanceModeEPP`，`:1299-1310`）内填充。**注意 BFE 字段为值类型**（`EPPCheckConf.Disabled`/`EPPTLSConf.Plaintext`/`EPPBreakerConf.Disabled` 均为 `bool`，非指针）。

```go
if balanceMode := cluster.getBalanceMode(); balanceMode == BalanceModeEPP {
    if addrs, ok := fetchEPPAssignmentAddrs(ctx, eppResolver, cluster.Name); ok {
        clusterConf.GslbBasic.BalanceMode = lib.PString(BalanceModeEPP)
        clusterConf.GslbBasic.EPPAddr = &addrs

        // TLS 固定明文（不做用户配置）
        clusterConf.GslbBasic.EPPTLS = &cluster_conf.EPPTLSConf{Plaintext: true}

        // 运行时参数：check / timeout（未配置整体对象时不下发，BFE 补默认）
        if cluster.EppRuntime != "" {
            if rt, err := ParseEppRuntime(cluster.EppRuntime); err == nil && rt != nil {
                if c := rt.Check; c != nil {
                    clusterConf.GslbBasic.EPPCheck = &cluster_conf.EPPCheckConf{
                        Disabled:         derefBool(c.Disabled, false),
                        CheckInterval:    c.CheckInterval,
                        FailThreshold:    c.FailThreshold,
                        Cooldown:         c.Cooldown,
                        SuccessThreshold: c.SuccessThreshold,
                    }
                }
                if t := rt.Timeout; t != nil {
                    clusterConf.GslbBasic.EPPTimeout = &cluster_conf.EPPTimeoutConf{
                        Connect: t.Connect,
                        Call:    t.Call,
                    }
                }
            }
        }

        // 熔断固定关闭
        clusterConf.GslbBasic.EPPBreaker = &cluster_conf.EPPBreakerConf{Disabled: true}
    } else {
        clusterConf.GslbBasic.BalanceMode = lib.PString(BalanceModeWRR)
    }
}
```

设计要点：

1. **EPPTLS / EPPBreaker 恒填**（在 EPP 且分配有效分支内）：未配置 `epp_runtime` 的现有 EPP cluster 也会显式输出明文与关闭熔断——这会使现有 EPP cluster 产生一次 version bump / reload（可接受的一次性代价），换取消除 BFE warning 与 EPP 明文可达。
2. **EPPCheck / EPPTimeout 仅在存储了 `epp_runtime` 时输出**：未配置时不下发，由 BFE 补默认（与现状一致，避免全量 EPP cluster 版本抖动）。
3. **降级不导出**：分配缺失降级为 `WRR` 时，落在 `else` 分支，不输出任何 EPP 运行时字段（BFE 也不消费），与既有降级语义一致。
4. **值类型赋值**：`EPPTLSConf{Plaintext:true}` / `EPPBreakerConf{Disabled:true}`；`EPPCheckConf.Disabled` 传值。
5. **版本控制**：`data_sign` 覆盖整份导出，导出内容变化自动产生新 version，conf-agent 自动感知，无需额外处理。

> feature flag（可选）：如需与 BFE 升级节奏解耦，可在导出侧加开关控制 `EPPTLS{Plaintext:true}` 的下发；本期默认不加，按部署顺序灰度。

---

## 6. 涉及文件清单

| 文件 | 改造点 |
|------|--------|
| `model/icluster_conf/epp_runtime.go`（新增） | `EppRuntimeParam`/`EppCheckParam`/`EppTimeoutParam`；`ParseEppRuntime` / `Validate` / `ApplyDefaults` / `Marshal`；默认常量 |
| `model/icluster_conf/cluster.go` | `ClusterParam.EppRuntime *string`；`Cluster.EppRuntime string`；`CreateCluster`/`UpdateCluster` 接入 `validateClusterEppRuntime`；`NewBfeClusterConf` EPP 分支填充 |
| `model/icluster_conf/cluster_operation_log.go` | `clusterParamToMap`/`clusterToMap` 纳入 `epp_runtime`（`runtimeSnapshotValue`，保持纯 JSON 类型） |
| `endpoints/openapi_v1/product_cluster/create.go` | `UpsertParam` 增 `EppRuntime *json.RawMessage`；`normalizeEppRuntimeRaw`；`clusterParamControlModel` 接入；`Validate` 接入 |
| `endpoints/openapi_v1/product_cluster/update_basic.go` | `clusterParamControlModel4Update` 接入（非 nil 替换，nil 保留） |
| `endpoints/openapi_v1/product_cluster/one.go` | `ClusterData` 增 `EppRuntime json.RawMessage`；`clusterModel2Control` 回读 |
| `storage/rdb/internal/dao/table_clusters.go` | `TCluster`/`TClusterParam` 增 `epp_runtime` 字段 |
| `storage/rdb/cluster_conf/cluster.go` | `newCluster`/`newDaoClusterParam` 映射 |
| `db_ddl.sql` / `db_ddl_sqlite.sql` | `clusters` 增 `epp_runtime` 列 |
| `storage/rdb/cluster_conf/cluster_test.go` | 内联 SQLite 建表 SQL 增列 |
| `test/integration/tests/schema/openapi/cluster.go` | `ClusterSchema` 增 `epp_runtime` Optional 字段与子 schema |
| 单元测试 | `epp_runtime_test.go`、`create_test.go`、`one_test.go`、存储与操作日志测试 |

**设计文档更新**：本目录 `change-summary.md`/`api-changes.md`/`design-changes.md`；`design-docs/api-define/OpenAPI接口定义/clusters.md`；`design-docs/api-define/InnerAPI接口定义/server-data-conf.md`；`design-docs/sys-design/数据库设计文档.md`；`design-docs/sys-design/details/EPP调度对接.md`。

---

## 7. 测试计划

### 7.1 验收标准（checkable）

1. `POST /clusters`（`balance_mode=EPP` + 合法 `epp_runtime`）创建后：`GET` 回读 `epp_runtime` 与写入一致（含默认补齐值）；`server_data_conf` 中该 cluster `GslbBasic` 含 `EPPTLS{Plaintext:true}`、`EPPCheck`/`EPPTimeout`（按输入）、`EPPBreaker{Disabled:true}`。
2. 非法 `epp_runtime`（负 duration、`fail_threshold=0`、含 `tls` 字段）返回 422，且不落盘。
3. WRR cluster（未配置 `epp_runtime`）导出与现状完全一致。
4. EPP cluster（未配置 `epp_runtime`，分配有效）导出含固定 `EPPTLS`/`EPPBreaker`，无 `EPPCheck`/`EPPTimeout`。
5. EPP cluster 分配缺失时导出 `BalanceMode=WRR` 且无 EPP 运行时字段。
6. 导出的配置可被 BFE `GslbBasicConfCheck`（含 `EPPTLSConfCheck`）通过。

### 7.2 单元测试

- `epp_runtime`：`ParseEppRuntime`（空/合法/未知字段/坏 JSON）；`Validate` 各边界；`ApplyDefaults`；`Marshal` 规范化。
- cluster 写入：`validateClusterEppRuntime` 集成到 create/update（携带/缺省/替换语义）。
- 导出：`epp_runtime` → `GslbBasic.{EPPCheck,EPPTimeout}` 映射；`EPPTLS.Plaintext`/`EPPBreaker.Disabled` 恒 `true`；WRR 不导出；未配置行为符合验收 4/5。
- 存储：DB round-trip；未设置 nil-skip；操作日志快照为纯 JSON 类型（可被 `MaskSensitiveFields` 递归）。

### 7.3 集成测试

- OpenAPI：POST/PATCH/GET `/clusters` 的 `epp_runtime` 全链路；非法 4xx；携带 `tls` 422。
- 导出：`server_data_conf` 含 `GslbBasic` EPP 字段，BFE 校验通过。
- schema：`test/integration/tests/schema/openapi/cluster.go` 增 `epp_runtime` 后 `openapi_schema_test.go` 通过。

---

## 8. 风险与注意事项

| 风险 | 说明 | 缓解 |
|------|------|------|
| BFE fork 字段类型假设 | `Disabled`/`Plaintext` 若为指针，导出片段需相应调整 | 实现前以 `go doc` 对 pin 版本核对；仅编译期问题 |
| 存量库缺列 | 无 migration 框架，老库不加列导致读写失败 | 新列可空 + 上线时 `ALTER TABLE` / `--reinit`；部署前确认 |
| 现有 EPP cluster 版本抖动 | 「EPPTLS/EPPBreaker 恒填」会触发一次 version bump / reload | 可接受的一次性代价；必要时加 feature flag 灰度 |
| `tls`/`breaker` 行为预期 | 用户可能误以为可配置 | 严格解析→未知字段 422，并在错误信息与文档中明确 |
| 覆盖门禁 | model 覆盖率 ≥ 70% | 补齐单测，跑 `make test-model-cover-gate` |
| 与 BFE 校验漂移 | BFE 规则变更后 api 未同步 | 校验规则注明与 BFE `EPPxxxConfCheck` 对齐；BFE 变更时同步 |

**回滚**：改动为新增字段/列/分支，回退即还原相关文件；已落盘的 `epp_runtime` 对旧代码为未知列（不被读取），不影响旧逻辑；导出回退后 BFE 回落 legacy 默认。