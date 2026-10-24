# 管理面 IP 白名单：变更摘要

## 1. 背景

控制面 `ai-gateway-api` 以单 HTTP 端口对外提供服务（`main.go:113-132`，默认 `0.0.0.0:8183`），同一端口承载三类管理入口：

| 路径 | 内容 | 现状防护 |
|------|------|----------|
| `/open-api/v1` | 管理 OpenAPI（密钥/模型/路由/报表等 20+ 资源域） | 仅 `McUserProbe` 鉴权（`endpoints/openapi_v1/endpoints.go:58`） |
| 其余路径 | Dashboard 静态资源（走 `NotFoundHandler` 静态分支） | **仅 MCRecovery，无任何访问控制**（`endpoints/router.go:65-74`） |
| `/inner-api/v1` | 内部导出 API（conf-agent/BFE/EPP 拉取配置） | 仅 `McUserProbe`（`endpoints/innerapi_v1/endpoints.go:52`） |

数据面 IP 白名单已由 `api_keys.subnet` 覆盖（BFE `mod_ai_token_auth`，`token_rule_table.go:194-212`），**管理面无网络层收敛手段**。客户《大模型网关必备功能点 v2》#15（核心必备）要求"仅指定网段可访问管理面，非白名单访问被拒"。

需求与实现方案全文见 `document-ai-gateway/迭代系统设计/v0.8/管理面IP白名单/`（需求分析 + 实现方案两份）。

## 2. 目标

| 项目 | 说明 |
|------|------|
| 变更日期 | 2026-10-04 |
| 涉及仓库 | `ai-gateway-api` |
| 变更类型 | 新增服务端横切能力：**HTTP 入口 IP 白名单中间件** + 服务端配置段 `[AccessControl]` + **热加载 handler**（`/reload/access_control`）；**非 DB 域资源**（无表、无 Open API、无导出） |
| 产出 | 1 个中间件（`endpoints/middleware/ip_probe.go`）+ 配置结构/编译校验（`stateful/config.go`）+ 403 错误类型（`lib/xerror`）+ Prometheus 计数器 + **热加载 handler（monitor 端口 `/reload/access_control`，原子替换）** + 单测/集成测试 + 设计文档 |
| 预估工作量 | 约 8-9 个开发日（含测试、热加载与设计文档） |

## 3. 关键决策

| # | 决策 | 结论 | 理由 |
|---|------|------|------|
| 1 | 配置载体 | **服务端 `ai_gateway_api.toml` 新增 `[AccessControl]` 段**，启动期静态加载 | 白名单是保护控制面 API **自身**的机制。若走 DB/OpenAPI 配置，错误配置会把运维锁死在外面且无法经 API 自救（鸡生蛋）；安全基线应随部署物（裸机配置文件 / K8s ConfigMap）分发与版本化 |
| 2 | 挂载点 | 全局中间件链 `endpoints/router.go`，**MCLogger 之后、MCCors 之前** | ① 拒绝事件必须进访问日志（FR 留痕）→ 位于 MCLogger 后；② 网络层拒绝不浪费 CORS 处理、403 对跨域预检同样生效 → 位于 MCCors 前；③ Dashboard 静态分支走 `NotFoundHandler`——**mux v1.8 对 NotFoundHandler 不应用 `Use` 中间件**（集成测试 MAC-1-003 实证），故 `router.go` 对该分支手动包裹同一中间件链，白名单才真正覆盖 Dashboard 登录页 |
| 3 | 规则模型 | `{Name, PathPrefix, Subnets, Audit}`，按 **PathPrefix 最长匹配**取一条规则；未命中任何规则 → 放行 | 单端口承载管理 API/Dashboard/InnerAPI 三类流量，需按路径分组差异化网段（InnerAPI 收敛到基础设施网段）；默认放行保证向后兼容 |
| 4 | 客户端 IP 判定 | `RemoteAddr` 优先；对端在 `TrustedProxies` 内时，从 `X-Forwarded-For` **右起跳过可信代理**取首个非可信 IP；不可信对端的 XFF **完全忽略** | LB/Ingress/BFE 反代部署下白名单的正确性前提；忽略不可信 XFF 使伪造头无效。算法与 BFE `mod_trust_clientip` 同源 |
| 5 | IP 库 | `net/netip`（`Addr`/`Prefix.Contains`，Go 1.24 已满足 `go.mod`），`Addr.Unmap()` 统一 v4-mapped 形态 | 数据面 `net.ParseCIDR` 的升级版；同时支持 IPv4/IPv6 与双栈归一 |
| 6 | 默认关闭 | `Enable=false`（默认）时中间件完全旁路；存量 `ai_gateway_api.toml` 零改动可升级 | 安全功能上线不改变存量部署行为 |
| 7 | 启动校验 | fail-fast：启用态任一规则 `Subnets` 为空 → `LoadConfig` 拒绝启动；`0.0.0.0/0`、`::/0` 启动告警；非法 CIDR 启动报错 | 把"锁死配置"挡在进程启动之前 |
| 8 | 观察模式 | 规则级 `Audit` 开关：命中拒绝条件仅记日志与计数（`audit=true` 标识），不真正拒绝 | 上线前灰度验证网段配置，防锁死（先观察后强制） |
| 9 | 403 错误类型 | `lib/xerror` 新增 `etAccessForbidden = "Access.Forbidden"` → `ErrNo 403 / Type "Access Forbidden"`，Wrap 函数 `WrapAccessForbiddenErrorWithMsg` | 沿用现有"错误类型字符串 → Resolve() 映射 HTTP 码"机制（`resolve.go:79-98`），与 401/402 同族；不新增并行错误通道 |
| 10 | 自身故障语义 | **fail-open**：客户端 IP 解析失败（如 RemoteAddr 异常）→ 放行，但计数器 `mgmt_access_reject_total{rule="unresolvable"}` + 日志 | 白名单自身缺陷不得导致管理面整体不可用 |
| 11 | 监控 | Prometheus CounterVec `mgmt_access_reject_total{rule}`，挂 8284 监控端口 `/monitor/metrics`（`stateful/metrics.go` 既有注册模式） | 非白名单来源突增 = 扫描行为信号，可接 Grafana 告警 |
| 12 | 热加载（本期实现） | monitor 端口（8284）注册 `/reload/access_control`（web_monitor 框架原生支持 `/reload/*`，handler 签名 `func(url.Values) (string, error)`，同 BFE `mod_ai_rate_limit` 先例，`bfe/bfe_modules/mod_ai_rate_limit/mod_ai_rate_limit.go:393`）；重读 toml `[AccessControl]` 段 → 同启动期校验 → 编译 → **原子替换**（`atomic.Value`），任一步失败保留旧配置 | 配置变更秒级生效、零重启窗口；配合 ConfigMap 卷更新形成 K8s 运维闭环；框架自带 `isValidForReload` 源地址校验（`web_monitor.go:244-259`），且监控端口应仅集群内可达，双重收敛后才暴露热加载能力 |
| 13 | `X-Real-IP` | 一期**不采用** | 单一来源、无链式语义、易被伪造；如需二期在可信代理前提下补充 |

## 4. 范围

| 范围 | 说明 |
|------|------|
| 主要新增 | `endpoints/middleware/ip_probe.go`（resolveClientIP / matchRule / IPProbeAction / Audit 分支 / 拒绝留痕） |
| 主要修改 | `stateful/config.go`（`AccessControlConf`/`AccessRuleConf` 结构 + toml 反序列化 + 启动期编译排序 + validator 自定义 `cidr` tag + **配置文件路径留存**）；`stateful/metrics.go`（拒绝计数器 + **monitor 端口注册 `/reload/access_control`**）；`endpoints/middleware/convert.go`（注册 `McIPProbe`）；`endpoints/router.go`（`router.Use(middleware.McIPProbe)` + 顺序注释）；`lib/xerror/wrap.go` + `resolve.go`（403 错误类型）；`conf/ai_gateway_api.toml`（配置样例注释） |
| 明确不动 | DB DDL（**零表、零迁移**）；OpenAPI / InnerAPI 端点（**无任何接口增删改**，有意为之）；导出框架（`model/iversion_control`）、conf-agent、BFE 数据面、鉴权体系（`model/iauth`）、Dashboard 前端 |
| 接口契约 | 无 OpenAPI/InnerAPI 变更；对外行为变化两处：① 启用后非白名单请求收到 403 JSON（`{ErrNo:403, Type:"Access Forbidden"}`）；② monitor 端口新增 `/reload/access_control` 内部端点（仅集群内可达） |
| 数据迁移 | 无 |

## 5. 非本仓登记点（链路协同）

| 位置 | 改动 | 状态 |
|------|------|------|
| `ai-gateway/kubernetes/` 清单 | ConfigMap 挂载 `[AccessControl]` 样例、Ingress 场景 TrustedProxies（Pod/Node 网段）配置指引、NetworkPolicy 收敛监控端口模板、热加载触发脚本样例（逐 Pod curl `/reload/access_control`） | 待落地 |
| `ai-gateway/docker-compose.yml` | 配置样例注释 | 待落地 |
| 集成测试仓 `integration-test/` | 新增管理面 IP 白名单端到端场景（403/放行/伪造 XFF/Audit/**热加载生效与失败保留旧配置**） | 待落地 |
| `bfe/docs` | 不涉及（数据面零改动） | — |

## 6. 文档配套

- `design-changes.md`：配置模型（Go 结构 + toml 示例）、中间件设计、XFF 解析算法、启动校验、监控指标、**热加载详细设计（注册/重读/原子替换/K8s 闭环）**、测试计划、WBS、开放问题；
- 需求/方案全文：`document-ai-gateway/迭代系统设计/v0.8/管理面IP白名单/管理面IP白名单需求分析.md`、`管理面IP白名单实现方案.md`（含 FR-1~FR-12、验收标准、风险对策）。

## 7. 六步法核对

- [x] Step 1：变更目录 `2026-10-04-mgmt-ip-whitelist`
- [x] Step 2：本变更摘要 + design-changes.md
- [x] Step 3：api-define —— `OpenAPI接口定义/00-common.md` 错误码表新增 403（唯一契约变化）；无接口增删改
- [x] Step 4：sys-design —— `接口层设计文档.md` §3 中间件链与中间件表同步（McIPProbe 行 + 位置约束注释）；沉淀 `details/管理面访问控制.md` 并登记 summary.md（见 Step 6）
- [x] Step 5：代码实现完成，`go build ./...`、`go vet`、全量 `go test ./...`（87 包 ok）通过；`conf/ai_gateway_api.toml` 注释样例已追加
- [x] Step 6：沉淀 `sys-design/details/管理面访问控制.md`（含实现踩坑记录）

## 8. 实现校准记录（Step 5 实际落地 vs 设计）

| 项 | 设计 | 实际实现 | 说明 |
|----|------|----------|------|
| 校验层次 | validator `cidr` tag + 编译期空网段检查 | 同左，**另修复**：`Rules` 切片需显式 `validate:"dive"`，否则 validator 不下钻、内层校验静默失效（单测捕获） | 已记录到 details 踩坑 #1 |
| 测试夹带 | 最小 toml | 需补全 `[Server]`/`[RunTime]`/`[Databases.bfe_db]` 必填字段（`LoadConfig` 校验整个 Config） | details 踩坑 #2 |
| 日志依赖 | 直接 `log.Logger` | 生产由 `config_logger.go` 注入；单测经 `setupTestLoggers` 设置并还原（顺手补齐了该 helper 对 `log.Logger` 的处理） | details 踩坑 #4 |
| Dashboard 静态分支覆盖 | 设计曾推断"`router.Use` 对 NotFoundHandler 同样生效、零改动覆盖" | **集成测试 MAC-1-003 证伪**：mux v1.8 的 ServeHTTP 不对 NotFoundHandler 应用 Use 中间件，白名单整体绕过静态路径（实测返回目录列表 200）。已修复：`router.go` 对 NotFoundHandler 手动包裹同一中间件链 | 本表新增；集成测试拦截真实缺陷的实证 |
| 监控端口绑定 | 未涉及 | 新增 `ServerConfig.MonitorAddr`（空=全网卡）；集成测试经 testutil 绑定 127.0.0.1，消除 Windows 防火墙弹窗（主端口测试模板早已绑定回环） | testutil 新增 StartServerWithMonitor |
| 错误渲染 | 403 JSON | 经 `xreq.ErrorRender` → `Result.parseError()` → `xerror.Resolve` → HTTP 403 + `ErrNum:403` 确认 | 代码核验，无需改动 |
| 集成测试 | `integration-test/` 新场景 | 本仓单测已覆盖（XFF 解析/最长前缀/Audit/403/伪造头/热加载成功与失败保留旧配置）；端到端场景随 `integration-test` 仓另行落地（登记于第 5 节） | 不变 |
| license header | `make license-check` | 本环境无 `make`，新文件均按仓内双头规范（Rainway 2026 + BFE 2021）手写，与 `config.go`/`main.go` 一致 | 待有 CI 环境复核 |

**验证结论**：`go build ./...` 通过；`go vet` 通过；`go test ./stateful ./endpoints/middleware ./lib/xerror` 全绿（新增 14 个用例）；全量 `go test ./...` 87 包全部通过、零回归；`gofmt` 对本次新增/修改文件干净（`config.go` 的 `RunTimeConfig` 对齐为存量问题，未越权修改）。
