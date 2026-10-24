# 管理面 IP 白名单：设计变更说明

> 配套：《change-summary.md》（背景、目标、关键决策、范围）。
> 本文给出可落地的详细设计：配置模型、中间件、XFF 解析算法、启动校验、监控、**热加载**、测试计划、WBS。

---

## 1. 现状代码事实（设计约束来源）

| 事实 | 位置 | 对本设计的约束 |
|------|------|----------------|
| 单 HTTP 服务：negroni + mux，`graceful.Run` | `main.go:113-132` | 新增中间件挂全局链即可覆盖全部入口 |
| 全局中间件顺序：MCRecovery → MCLogger → MCCors | `endpoints/router.go:81-83` | McIPProbe 插入 MCLogger 之后、MCCors 之前 |
| Dashboard 静态分支走 `NotFoundHandler`（`fileHandler`） | `router.go:65-74` | mux v1.8 的 ServeHTTP **不对 NotFoundHandler 应用 `Use` 中间件**（集成测试 MAC-1-003 实证），须在该分支手动包裹同一中间件链 |
| OpenAPI 子路由挂 `McProductProbe + McUserProbe`；InnerAPI 挂 `McUserProbe` | `openapi_v1/endpoints.go:56-62`、`innerapi_v1/endpoints.go:48-58` | 白名单先于鉴权（网络层），放在全局链即天然满足 |
| 鉴权中间件无 Authorization 头直接放行 | `endpoints/middleware/user_probe.go:26-48` | 现有链路无任何"先于鉴权"的拦截点，需新增 |
| 中间件包装范式 `convert()` | `endpoints/middleware/convert.go:40-41` | `Action func(*http.Request) (*http.Request, error)` → `McIPProbe` 直接套用 |
| 服务端配置 `ServerConfig` + go-playground/validator | `stateful/config.go:41-46` | 新配置段沿用同一加载/校验路径 |
| `LoadConfig` 当前**不保留**配置文件路径 | `stateful/config.go:110-136` | 热加载需新增包级变量留存路径（§7.2） |
| 监控端口 8284，web_monitor 支持 `/monitor/*`、`/reload/*`；reload 框架自带 `isValidForReload` 源地址校验 | `stateful/metrics.go:55-73`、`go-lib/web-monitor/web_monitor/web_monitor.go:93-191`、`:244-259` | 计数器随默认 gatherer 暴露；热加载挂 `/reload/*`，且天然有源地址校验 |
| reload handler 签名 `func(url.Values) (string, error)`（BFE 先例） | `bfe/bfe_modules/mod_ai_rate_limit/mod_ai_rate_limit.go:393` | `ReloadAccessControl` 沿用同签名 |
| 错误类型字符串 → `Resolve()` 映射 HTTP 码（401/402 等） | `lib/xerror/wrap.go:36-46`、`resolve.go:79-98` | 403 走同机制新增 `etAccessForbidden` |

---

## 2. 配置模型

### 2.1 Go 结构（`stateful/config.go` 新增）

```go
type AccessRuleConf struct {
    Name       string   `toml:"Name"       validate:"required"`
    PathPrefix string   `toml:"PathPrefix" validate:"required,startswith=/"`
    Subnets    []string `toml:"Subnets"    validate:"required,min=1,dive,cidr"`
    Audit      bool     `toml:"Audit"`
}

type AccessControlConf struct {
    Enable         bool             `toml:"Enable"`
    TrustedProxies []string         `toml:"TrustedProxies" validate:"dive,cidr"`
    Rules          []AccessRuleConf `toml:"Rules"`
}

// 编译产物：运行期唯一事实源，atomic.Value 承载
type CompiledAccessControl struct {
    Enabled   bool
    trustNets []netip.Prefix
    rules     []compiledRule // 按 PathPrefix 长度降序
}

type compiledRule struct {
    conf AccessRuleConf
    nets []netip.Prefix
}
```

- 挂载进 `Config`（`Config.AccessControl`），全局访问点 `stateful.DefaultConfig.AccessControl`；
- 自定义 validator tag `cidr`：内部用 `netip.ParsePrefix` 校验（允许 `/32`、`/128` 单主机写法）；
- **编译函数抽公共**：`CompileAccessControl(*AccessControlConf) (*CompiledAccessControl, error)`，启动与热加载共用同一份校验/编译代码，杜绝两条路径语义漂移；
- 启动时：`LoadConfig` 解析 → validator 校验 → `CompileAccessControl` → 写入原子变量（见 §7.2）。

### 2.2 TOML 配置示例（`conf/ai_gateway_api.toml` 追加，随注释样例发布）

```toml
# ---------------------------------
# Management Plane Access Control (IP Whitelist)
# 管理面 IP 白名单：保护 Dashboard / OpenAPI / InnerAPI 三类管理入口
# 变更生效方式：curl -X POST http://<pod-ip>:8284/reload/access_control（热加载），
#             或优雅重启（GracefulTimeOutInMs）兜底
[AccessControl]
Enable = false
# 可信代理网段（LB / Ingress / BFE 反代出口）。仅在请求对端属于可信代理时才解析 XFF，
# 不可信来源的 X-Forwarded-For 一律忽略（防伪造）。
TrustedProxies = ["10.244.0.0/16", "172.16.0.0/12"]

# InnerAPI：仅基础设施网段（conf-agent / BFE / EPP）
[[AccessControl.Rules]]
Name       = "inner-api"
PathPrefix = "/inner-api/v1"
Subnets    = ["10.10.0.0/16"]
Audit      = false    # true=观察模式：只记录不拒绝

# 管理 API + Dashboard：仅运维网段（前缀最长匹配优先于上方 inner-api 命中自身前缀）
[[AccessControl.Rules]]
Name       = "mgmt"
PathPrefix = "/"
Subnets    = ["192.168.1.0/24", "100.64.0.0/16"]
Audit      = false
```

### 2.3 规则匹配语义

```go
// 编译期按 PathPrefix 长度降序预排序；取第一条前缀命中
func matchRule(rules []compiledRule, path string) *compiledRule {
    for i := range rules {
        if strings.HasPrefix(path, rules[i].conf.PathPrefix) {
            return &rules[i]
        }
    }
    return nil // 未命中规则 → 放行（默认兼容）
}
```

---

## 3. 中间件设计

### 3.1 新增 `endpoints/middleware/ip_probe.go`

```go
// IPProbeAction 返回 req 表示放行；返回 error 表示拒绝（convert() 统一经 xerror.Resolve 渲染）
func IPProbeAction(req *http.Request) (*http.Request, error) {
    cc := stateful.LoadCompiledAccessControl() // atomic.Value 原子加载；nil（未配置/未启用）即旁路
    if cc == nil || !cc.Enabled {
        return req, nil
    }

    clientIP := resolveClientIP(req, cc.trustNets) // 见 §4
    if clientIP == nil {
        // fail-open：不因白名单自身缺陷拒绝对管理面的访问，但可见
        metricMgmtReject.WithLabelValues("unresolvable").Inc()
        logUnresolvable(req)
        return req, nil
    }

    rule := matchRule(cc.rules, req.URL.Path)
    if rule == nil {
        return req, nil
    }
    if prefixContains(rule.nets, *clientIP) {
        return req, nil
    }

    // 拒绝：留痕（访问日志由 MCLogger 保证落 method/path/RemoteAddr/403）
    metricMgmtReject.WithLabelValues(rule.conf.Name).Inc()
    logAccessReject(rule.conf.Name, clientIP, req) // 规则名/客户端IP/RemoteAddr/XFF/UA/audit 标识

    if rule.conf.Audit {
        return req, nil // 观察模式放行
    }
    return nil, xerror.WrapAccessForbiddenErrorWithMsg("access denied by management ip whitelist")
}
```

### 3.2 接线（2 处小改）

`endpoints/middleware/convert.go`（注册，紧随现有两行）：

```go
McIPProbe = convert(IPProbeAction)
```

`endpoints/router.go:81-83`（顺序注释必须写明位置约束，同 bfe_modules.go 的顺序注释纪律）：

```go
router.Use(middleware.MCRecovery)
router.Use(middleware.MCLogger)
router.Use(middleware.McIPProbe) // 位置约束：Logger 后（拒绝留痕）/ CORS 前（403 对预检生效）；勿移
router.Use(middleware.MCCors)
```

### 3.3 403 响应形态

- `lib/xerror/wrap.go` 新增 `etAccessForbidden = "Access.Forbidden"` 与 `WrapAccessForbiddenErrorWithMsg(msg string, args ...interface{})`；
- `lib/xerror/resolve.go` 的 `Resolve()` switch 新增 case：`ErrNo = 403, Type = "Access Forbidden"`；
- 响应体与现有错误渲染一致（`xreq.Result` JSON），外部调用方看到的是普通 403 + 结构化错误体。

---

## 4. 真实客户端 IP 解析（可信代理 + XFF）

```text
resolveClientIP(req, trustNets):
    remote = netip.ParseAddrPort(req.RemoteAddr) 取 Addr
    if remote 不在 trustNets:
        return remote                      // 不可信对端：忽略 XFF，伪造头无效
    // 对端是可信代理：XFF 右起向左，跳过可信代理，取首个非可信 IP
    xff = req.Header["X-Forwarded-For"] 按 "," 拆分并 trim
    for i = len(xff)-1 downto 0:
        ip = netip.ParseAddr(xff[i])
        if ip 合法 && ip.Unmap() 不在 trustNets:
            return ip.Unmap()
    return remote.Unmap()                  // 全链皆可信，退回对端地址
```

要点：

- **右起跳过可信代理**：与 BFE `mod_trust_clientip` 同源算法，多级反代（LB → BFE → API）下取到最靠近真实客户端的非可信 IP；
- `Addr.Unmap()` 统一 IPv4-mapped IPv6 形态后再做 `Prefix.Contains`，双栈环境判定一致；
- `X-Real-IP` 一期不采用（无链式语义、易伪造）；
- 解析任一环节失败（RemoteAddr 非法、XFF 全链可信等退化场景）→ 返回 nil，走 §3.1 的 fail-open 分支。

---

## 5. 启动校验（fail-fast）

| 校验项 | 行为 |
|--------|------|
| `Enable=true` 且任一规则 `Subnets` 为空 | `LoadConfig` 报错拒绝启动（错误信息含规则名） |
| 非法 CIDR（`netip.ParsePrefix` 失败） | 启动报错（validator `cidr` tag） |
| `0.0.0.0/0` 或 `::/0` 出现在 Subnets | 启动**告警日志**（语义等于未收敛，允许但提示） |
| `Enable=false` | 全部校验旁路，编译产物可为空；存量配置零改动启动 |

热加载重放同一份校验（§7.3），保证两条路径语义一致。

---

## 6. 监控与日志

### 6.1 Prometheus（`stateful/metrics.go`，照既有 Counter 注册模式）

```go
metricMgmtReject = prometheus.NewCounterVec(prometheus.CounterOpts{
    Name: "mgmt_access_reject_total",
    Help: "Rejected requests by management-plane IP whitelist",
}, []string{"rule"})
// rule = 规则名；rule = "unresolvable" 表示客户端 IP 解析失败（fail-open 但可见）
```

- 8284 端口 `/monitor/metrics` 随默认 gatherer 暴露（`stateful/metrics.go:55-73` 既有链路）；
- 告警建议：`mgmt_access_reject_total` 突增 = 扫描行为信号。

### 6.2 日志

- 访问日志：MCLogger 位于 McIPProbe 之前，拒绝请求天然落访问日志（method/path/RemoteAddr/状态码 403）；
- 业务日志：`logAccessReject` 输出规则名、解析出的客户端 IP、RemoteAddr 原文、XFF 原文、UA、`audit=true/false` 标识 → 写 access logger，可按 `audit=true` 检索"观察模式下本应拒绝的请求"清单（灰度核对手段）；
- 热加载审计：reload 成功/失败均写日志（时间、操作来源、旧规则数→新规则数，或错误详情）。

---

## 7. 热加载（本期实现）

### 7.1 注册与端点

- 注册点：`stateful/metrics.go` 的 `NewMonitorServerWithRun`（monitorServer 创建处，`:55-73`）追加：

```go
monitorServer.RegisterHandler(web_monitor.WebHandleReload, "access_control", ReloadAccessControl)
```

- 对外端点：`http://<pod-ip>:8284/reload/access_control`；
- handler 签名 `func(query url.Values) (string, error)`（web_monitor 支持的 reload 形态之一，BFE 先例 `bfe/bfe_modules/mod_ai_rate_limit/mod_ai_rate_limit.go:393`），成功返回信息如 `access_control reloaded: 2 rule(s)`；
- **安全性**：框架 reload 入口自带 `isValidForReload(remoteAddr)` 源地址校验（`web_monitor.go:244-259`）；叠加部署约束（监控端口仅集群内可达，NetworkPolicy 模板随部署样例给出）后才暴露热加载能力。

### 7.2 运行时配置载体

- `LoadConfig`（`stateful/config.go:110`）当前不保留配置文件路径：**新增包级变量** `confFilePath`（`LoadConfig` 首行赋值），供 reload 重读；
- `stateful` 内新增：

```go
var compiledAC atomic.Value // 存 *CompiledAccessControl；启动写入，reload 原子替换

func LoadCompiledAccessControl() *CompiledAccessControl {
    if v := compiledAC.Load(); v != nil {
        return v.(*CompiledAccessControl)
    }
    return nil
}

func ReloadAccessControl(query url.Values) (string, error) {
    // 1. 重读：仅解码 [AccessControl] 段（临时结构 DecodeFile，其他段忽略）
    // 2. 校验：与启动期相同的 fail-fast 规则（§5）
    // 3. 编译：CompileAccessControl（与启动共用）
    // 4. 原子替换：compiledAC.Store(new)
    // 5. 审计日志（时间、旧规则数→新规则数）
    // 任一步失败：返回错误，旧配置保持生效
}
```

- 中间件每请求经 `LoadCompiledAccessControl()` 原子加载，**不直接读 `DefaultConfig.AccessControl`**，保证 reload 切换无锁、无中间态。

### 7.3 与启动路径的语义一致性

校验与编译只有一份代码（`CompileAccessControl`），启动与 reload 共用：杜绝"启动时拒绝的配置热加载时放行"之类的路径漂移；reload 比启动多一步"仅解码单段 toml"，不触碰 `DefaultConfig` 其他字段。

### 7.4 K8s 运维闭环

ConfigMap 更新 → 卷同步（kubelet 异步，秒~分钟级）→ 逐 Pod 触发 reload：

```bash
for pod in $(kubectl get pod -n ai-gateway-system -l app=ai-gateway-api -o name); do
  kubectl exec -n ai-gateway-system "$pod" -- \
    curl -s -X POST http://127.0.0.1:8284/reload/access_control
done
```

逐 Pod 确认响应；失败 Pod 保持旧配置，可安全重试。裸机场景直接 `curl http://127.0.0.1:8284/reload/access_control`。

---

## 8. 测试计划

| 类型 | 用例 | 断言 |
|------|------|------|
| 单元 | CIDR 解析 | 合法/非法、`/32`/`/128`、v4-mapped v6 归一 |
| 单元 | XFF 解析 | 直连无 XFF；单级/多级可信代理；XFF 含不可信 IP 取最右非可信；**伪造 XFF（对端不可信 → 忽略 XFF）**；全链可信退回 RemoteAddr |
| 单元 | 规则匹配 | 最长前缀优先（`/inner-api/v1` vs `/` 并存）；无命中放行 |
| 单元 | Audit 模式 | 命中拒绝条件 → 放行 + 计数 + 日志 audit 标识 |
| 单元 | 启动校验 | 启用态空网段拒启动；`0.0.0.0/0` 告警；非法 CIDR 报错；Enable=false 全旁路 |
| 单元 | xerror | `WrapAccessForbiddenErrorWithMsg` → `Resolve()` 得 403/"Access Forbidden" |
| 单元 | 热加载 | 合法新配置 → 编译产物原子替换；非法配置 → 报错且旧配置保持；与启动共用 `CompileAccessControl` 的等价性 |
| 集成 | 端到端（`integration-test/` 新场景） | 非白名单来源 `GET /`（Dashboard）、`/open-api/v1/*`、`/inner-api/v1/*` → 403 JSON；白名单来源正常；伪造 XFF 不绕过；Audit 模式全放行且日志可见 |
| 集成 | 热加载端到端 | 修改 toml 网段 → `/reload/access_control` → 新网段即时生效（不发请求重启）；非法配置 reload → 报错且行为不变；reload 审计日志可查 |

回归基线：不带 `[AccessControl]` 段的旧配置启动，全部行为与现状一致。

---

## 9. WBS

| # | 任务 | 改动点 | 工作量 |
|---|------|--------|--------|
| 1 | 配置结构与编译校验 | `stateful/config.go` + 自定义 `cidr` validator + `CompileAccessControl` 抽取 + `confFilePath` 留存 | 0.5 d |
| 2 | XFF/IP 解析 | `endpoints/middleware/ip_probe.go`（resolveClientIP、prefixContains） | 1 d |
| 3 | 中间件主体 | 同文件 matchRule/IPProbeAction/Audit/留痕 + `convert.go` 注册（原子加载读取） | 1 d |
| 4 | 接线 | `endpoints/router.go` 插入 + 顺序注释 | 0.2 d |
| 5 | 403 错误类型 | `lib/xerror/wrap.go`、`resolve.go` | 0.3 d |
| 6 | 监控指标 | `stateful/metrics.go`（计数器） | 0.2 d |
| 7 | 单元测试 | §8 单元部分 | 1.5 d |
| 8 | 集成测试 | `integration-test/` 场景（含热加载） | 1 d |
| 9 | 部署样例 | `conf/ai_gateway_api.toml` 注释样例；k8s/docker-compose 样例与热加载脚本登记（非本仓） | 0.5 d |
| 10 | 热加载 | `ReloadAccessControl` + 单段 toml 重读 + `atomic.Value` 原子替换 + monitor 端口注册 + 审计日志 | 1 d |
| 11 | 文档 | 本目录两份 + 运维手册段落（防锁死指引/热加载操作） | 0.5 d |

合计约 8-9 人日。

---

## 10. 开放问题

1. `etAccessForbidden` 的展示文案（`Type = "Access Forbidden"`）与对外错误信息是否需要在响应中返回规则名（便于调用方自查，但也给探测者信息）——倾向：响应只给通用文案，规则名只进日志。
2. 是否增加 `xff_untrusted_total` 计数：对端不在 TrustedProxies 却携带 XFF 头的请求计数，帮助发现可信代理网段遗漏（部署期排障）。倾向：加，成本极低。
3. Audit 清单检索入口：仅日志（`audit=true`）还是加 monitor 端口专用 handler（`/monitor/access_control_audit`）？倾向：先日志。
4. 监控端口（8284）自身收敛：代码防护留二期，本期在部署样例中给出 NetworkPolicy/安全组模板。
