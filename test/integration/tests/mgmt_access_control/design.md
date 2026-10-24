# 管理面 IP 白名单（mgmt_access_control）集成测试设计

> 配套方法论：`.agents/skills/ai-gateway-api-integration-test-design/SKILL.md`。
> 功能实现：`stateful/access_control.go`、`endpoints/middleware/ip_probe.go`
> 等（变更说明 `design-docs/modifications/2026-10-04-mgmt-ip-whitelist/`）。
> 模块前缀：**MAC**（Mgmt Access Control）。

---

## 1. 被测对象与特殊性

管理面 IP 白名单是**服务端横切中间件**（非 OpenAPI 业务域资源），被测对象是控制面 HTTP 服务本身的行为：

| 被测面 | 说明 |
|--------|------|
| 主端口（默认 8183）网络层拦截 | `McIPProbe` 中间件：白名单放行 / 403 拒绝（先于鉴权） |
| 路径分组 | `/open-api/v1`（OpenAPI）、`/`（Dashboard 静态）、`/inner-api/v1`（InnerAPI）三类入口独立规则 |
| 可信代理 XFF 解析 | `TrustedProxies` 内对端按 XFF 右起解析；不可信对端忽略 XFF |
| Audit 观察模式 | 命中拒绝条件只记录不拒绝 |
| 热加载 | monitor 端口（8284）`/reload/access_control`：重读 toml 单段 → 校验 → 编译 → 原子替换 |
| 启动校验 | 启用态空网段等危险配置 fail-fast 拒绝启动 |

**与常规模块的差异**：不同用例需要**不同的服务端配置**（白名单规则、可信代理、Audit、启动失败），因此本模块不使用 TestMain 共享实例，而是**每个用例经 `testutil.StartServerWithExtraConfig` / `StartServerWithMonitor` 启动独立实例**（框架已支持的 per-test 配置注入模式，同 quota_period_reset 先例）。测试客户端与被测服务同机（127.0.0.1），通过配置是否包含 `127.0.0.0/8` 与 `TrustedProxies` 构造"白名单/非白名单/可信代理"三种视角；XFF 头由本模块自带的 raw HTTP helper 按需设置（全局 Client 不支持自定义头且不暴露 HTTP 状态码）。

## 2. 入口探针选择

| 入口 | 探针请求 | 放行预期 | 备注 |
|------|----------|----------|------|
| OpenAPI | `GET /open-api/v1/api-keys` | HTTP 200，顶层 `ErrNum=0`（列表接口） | 合同依据 `api-define/api-keys.md` |
| Dashboard 静态 | `GET /` | HTTP ≠ 403（集成环境无 static 目录，实际 404 页面） | 断言点：白名单不拦截静态路径时状态码非 403；拦截时为 403 JSON |
| InnerAPI | `GET /inner-api/v1/configs/mod-api-key` | HTTP ≠ 403 | 断言点同左（handler 自身语义与白名单无关） |

**拒绝统一断言**（合同依据 `api-define/00-common.md` 新增 403）：HTTP 状态码 = 403，响应体可解析出顶层 `ErrNum=403` 且 `ErrMsg` 含 `Access Forbidden`。

## 3. 场景总览（用例编号登记）

| 编号 | 用例名 | 前置配置（extraTOML） | 步骤与断言要点 |
|------|--------|------------------------|----------------|
| MAC-1-001 | 默认关闭全放行 | 无 `[AccessControl]` 段 | OpenAPI 探针 200 → 基线（向后兼容） |
| MAC-1-002 | 启用且本地在白名单 | Enable=true；`/` 与 `/inner-api/v1` 规则均含 127.0.0.0/8 | 三入口探针全部放行（OpenAPI 200；静态/Inner ≠403） |
| MAC-1-003 | 启用且本地不在白名单 | Enable=true；规则为 10.0.0.0/8、10.10.0.0/16 | 三入口全部 403 + ErrNum=403 + ErrMsg 归因；随后热加载改回含本地白名单 → 恢复 200（拒绝无持久副作用，服务可用性不被破坏） |
| MAC-2-001 | 可信代理按 XFF 判定 | TrustedProxies=[127.0.0.0/8]；mgmt 规则=192.168.1.0/24 | XFF=192.168.1.10 → 200；XFF=203.0.113.9 → 403；无 XFF（对端 127.0.0.1 不在白名单）→ 403 |
| MAC-2-002 | 伪造 XFF 无效 | TrustedProxies 为空；mgmt 规则=192.168.1.0/24 | XFF=192.168.1.10（伪造白名单 IP）→ 仍 403 |
| MAC-3-001 | Audit 观察模式放行 | Enable=true；mgmt 规则 Audit=true 且不含本地 | 探针放行（200）——观察模式只记录不拒绝；响应为正常业务报文而非 403 包装 |
| MAC-4-001 | 热加载生效 | StartServerWithMonitor 启动（Enable 缺省=false）→ 探针 200 → 改写 conf 增加 `[AccessControl]`（启用，不含本地）→ POST `/reload/access_control` → 响应体含 `access_control reloaded` → 同一进程探针变 403（不发重启） | 热加载原子替换生效 |
| MAC-4-002 | 热加载失败保留旧配置 | 续 MAC-4-001 实例：改写 conf 为非法配置（Enable=true 且规则 Subnets 为空）→ reload → 响应体含 `"error"` → 探针仍为 403（旧配置保持） | 与启动共用 fail-fast 校验 |
| MAC-4-003 | 热加载切换 Audit | 续 MAC-4-001 实例：改写 conf 为 Audit=true → reload 成功 → 探针从 403 恢复 200 | 观察模式可经热加载切换 |
| MAC-5-001 | 启用态空网段拒启动 | Enable=true 且唯一规则 Subnets=[] | `StartServerWithExtraConfig` 返回错误（进程 fail-fast 退出，端口永不就绪） |

> 用例计数：9。编号在实现 `_test.go` 中以注释标注（`// MAC-x-xxx`）。

## 4. 设计检查清单对照（SKILL.md）

| 检查项 | 覆盖情况 |
|--------|----------|
| #3 4xx 回读零变更 | MAC-1-003：拒绝后热加载恢复 200，证明无持久副作用；MAC-4-002：非法 reload 后行为不变 |
| #4 非法值/边界 + 错误归因 + 防泄漏 | MAC-4-002/MAC-5-001：非法配置被拒且旧配置零泄漏到运行时；403 的 ErrMsg 归因到 access whitelist |
| #11 返回形状顶层键 | 拒绝响应统一断言顶层 `ErrNum`/`ErrMsg`；放行响应断言 OpenAPI 业务报文（非 403 包装） |
| #12 合同一致性 | 断言依据：`api-define/00-common.md` 403 定义、conf 样例注释语义、变更说明决策表；发现实现与合同矛盾时先停（本模块无矛盾） |
| 并发 | 不涉及（无写路径） |
| 审计日志 | 拒绝事件为网络层行为，不产生 `operation_logs`；访问日志落盘但 log4go 缓冲使实时断言不可靠，**不纳入自动化断言**，留运维核查（已知限制） |

## 5. 运行方式

```bash
cd ai-gateway-api && go build -o ai-gateway-api.exe .   # 先重建二进制
cd ai-gateway-api/test/integration
go test -v -count=1 -timeout 300s ./tests/mgmt_access_control/...
```
