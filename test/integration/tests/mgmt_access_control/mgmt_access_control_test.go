// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License. All rights reserved.

// 管理面 IP 白名单集成测试（模块前缀 MAC，场景登记见同目录 design.md）。
// 被测对象为服务端横切中间件而非业务域资源，各用例以独立服务端实例
// 注入不同 [AccessControl] 配置；客户端与服务同机（127.0.0.1），通过
// 是否包含 127.0.0.0/8 与 TrustedProxies 构造白名单/非白名单/可信代理视角。
package mgmt_access_control_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probePathOpenAPI / probePathDashboard / probePathInner 为三类管理入口探针。
const (
	probePathOpenAPI  = "/open-api/v1/api-keys"
	probePathDashboard = "/"
	probePathInner    = "/inner-api/v1/configs/mod-api-key"
)

// acSectionTOML 是各用例注入的 [AccessControl] 配置段模板。
const acSectionTOML = `
[AccessControl]
Enable = %s
TrustedProxies = [%s]

[[AccessControl.Rules]]
Name = "inner-api"
PathPrefix = "/inner-api/v1"
Subnets = [%s]
Audit = %s

[[AccessControl.Rules]]
Name = "mgmt"
PathPrefix = "/"
Subnets = [%s]
Audit = %s
`

// startACServer 启动一个带指定 [AccessControl] 段的独立服务端实例。
// withMonitor=true 时同时启用监控端口（热加载用例）。
func startACServer(t *testing.T, extraTOML string, withMonitor bool) *testutil.ServerManager {
	t.Helper()

	var (
		sm  *testutil.ServerManager
		err error
	)
	if withMonitor {
		sm, err = testutil.StartServerWithMonitor(extraTOML)
	} else {
		sm, err = testutil.StartServerWithExtraConfig(extraTOML)
	}
	require.NoError(t, err)
	t.Cleanup(sm.Shutdown)
	testutil.SetServerURL(sm.ServerURL)
	return sm
}

// probeResult 为探针请求的归一化结果。
type probeResult struct {
	Status int
	ErrNum int
	ErrMsg string
	Raw    string
}

// rawGet 直接发 GET（可带 XFF 头），返回状态码与按顶层键解析的 ErrNum/ErrMsg。
// 非 JSON 响应（如 Dashboard 静态 404 页面）的 ErrNum/ErrMsg 为 0/""，由调用方按场景断言。
func rawGet(t *testing.T, baseURL, path, xff string) probeResult {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	require.NoError(t, err)
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	r := probeResult{Status: resp.StatusCode, Raw: string(body)}
	var parsed struct {
		ErrNum int    `json:"ErrNum"`
		ErrMsg string `json:"ErrMsg"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		r.ErrNum = parsed.ErrNum
		r.ErrMsg = parsed.ErrMsg
	}
	return r
}

// assertRejected 按合同（api-define/00-common.md 403）断言网络层拒绝。
func assertRejected(t *testing.T, r probeResult) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, r.Status, "http status, raw=%s", r.Raw)
	assert.Equal(t, 403, r.ErrNum, "ErrNum top-level key, raw=%s", r.Raw)
	assert.Contains(t, r.ErrMsg, "Access Forbidden", "ErrMsg attribution, raw=%s", r.Raw)
}

// assertOpenAPIAllowed 断言 OpenAPI 探针放行（合同：成功码 200）。
func assertOpenAPIAllowed(t *testing.T, r probeResult) {
	t.Helper()
	assert.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	assert.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
}

// replaceAccessControlSection 改写服务端临时配置中的 [AccessControl] 段：
// 截取首个 "[AccessControl" 表头之前的全部内容，追加新段。热加载用例专用。
func replaceAccessControlSection(t *testing.T, confFile, newSection string) {
	t.Helper()

	data, err := os.ReadFile(confFile)
	require.NoError(t, err)

	lines := strings.Split(string(data), "\n")
	cut := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[AccessControl") {
			cut = i
			break
		}
	}
	if cut >= 0 {
		lines = lines[:cut]
	}

	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n\n" + strings.TrimSpace(newSection) + "\n"
	require.NoError(t, os.WriteFile(confFile, []byte(out), 0644))
}

// reloadAccessControl 触发 monitor 端口 /reload/access_control。
// 失败时 web_monitor 也返回 HTTP 200 + {"error":"..."}，故由调用方断言响应体。
func reloadAccessControl(t *testing.T, monitorURL string) string {
	t.Helper()
	require.NotEmpty(t, monitorURL, "monitor port not enabled")

	resp, err := http.Post(monitorURL+"/reload/access_control", "text/plain", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	return string(body)
}

// MAC-1-001 默认关闭：无 [AccessControl] 段时全放行（向后兼容基线）。
func TestMAC1_001_DefaultDisabledAllowsAll(t *testing.T) {
	sm := startACServer(t, "", false)

	r := rawGet(t, sm.ServerURL, probePathOpenAPI, "")
	assertOpenAPIAllowed(t, r)
}

// MAC-1-002 启用且本地网段在白名单：三类管理入口全部放行。
func TestMAC1_002_EnabledLocalWhitelisted(t *testing.T) {
	extra := sprintfAC(true, `"127.0.0.0/8"`, `"127.0.0.0/8"`, false, `"127.0.0.0/8"`, false)
	sm := startACServer(t, extra, false)

	assertOpenAPIAllowed(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
	// 集成环境无 static 目录，Dashboard 探针放行时返回 404 页面（关键：不是 403）
	assert.NotEqual(t, http.StatusForbidden, rawGet(t, sm.ServerURL, probePathDashboard, "").Status)
	// InnerAPI 探针放行时进入 handler 语义（关键：不是 403）
	assert.NotEqual(t, http.StatusForbidden, rawGet(t, sm.ServerURL, probePathInner, "").Status)
}

// MAC-1-003 启用且本地不在白名单：三类入口全部 403；随后热加载改回含本地白名单，
// 服务恢复 200 —— 证明拒绝无持久副作用（4xx 回读零变更家族）。
func TestMAC1_003_EnabledLocalNotWhitelisted(t *testing.T) {
	extra := sprintfAC(true, ``, `"10.10.0.0/16"`, false, `"10.0.0.0/8"`, false)
	sm := startACServer(t, extra, true) // 需要监控端口做恢复步骤

	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
	assertRejected(t, rawGet(t, sm.ServerURL, probePathDashboard, ""))
	assertRejected(t, rawGet(t, sm.ServerURL, probePathInner, ""))

	// 恢复：热加载改回含本地白名单，同一进程内恢复放行
	recoverSection := sprintfAC(true, ``, `"127.0.0.0/8"`, false, `"127.0.0.0/8"`, false)
	replaceAccessControlSection(t, sm.ConfFile(), recoverSection)
	msg := reloadAccessControl(t, sm.MonitorURL)
	assert.Contains(t, msg, "access_control reloaded")

	assertOpenAPIAllowed(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
}

// MAC-2-001 可信代理按 XFF 判定：对端 127.0.0.1 在 TrustedProxies 内，
// 白名单按 XFF 真实客户端 IP 判定；无 XFF 时对端自身须命中白名单。
func TestMAC2_001_TrustedProxyUsesXFF(t *testing.T) {
	extra := sprintfAC(true, `"127.0.0.0/8"`, `"10.10.0.0/16"`, false, `"192.168.1.0/24"`, false)
	sm := startACServer(t, extra, false)

	// XFF 命中白名单 → 放行
	assertOpenAPIAllowed(t, rawGet(t, sm.ServerURL, probePathOpenAPI, "192.168.1.10"))
	// XFF 未命中 → 拒绝
	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, "203.0.113.9"))
	// 无 XFF：对端 127.0.0.1 不在 192.168.1.0/24 → 拒绝
	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
}

// MAC-2-002 伪造 XFF 无效：对端不可信时 XFF 被忽略，伪造白名单 IP 仍被拒。
func TestMAC2_002_ForgedXFFIgnored(t *testing.T) {
	extra := sprintfAC(true, ``, `"10.10.0.0/16"`, false, `"192.168.1.0/24"`, false)
	sm := startACServer(t, extra, false)

	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, "192.168.1.10"))
}

// MAC-3-001 Audit 观察模式：命中拒绝条件仍放行，且为正常业务报文而非 403 包装。
func TestMAC3_001_AuditModeAllows(t *testing.T) {
	extra := sprintfAC(true, ``, `"10.10.0.0/16"`, true, `"10.0.0.0/8"`, true)
	sm := startACServer(t, extra, false)

	assertOpenAPIAllowed(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
}

// MAC-4-001 热加载生效：启动时未启用（放行）→ 改写 conf 启用（不含本地）→
// reload 成功 → 同一进程探针变 403（原子替换，无需重启）。
func TestMAC4_001_HotReloadTakesEffect(t *testing.T) {
	sm := startACServer(t, "", true)

	assertOpenAPIAllowed(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))

	section := sprintfAC(true, ``, `"10.10.0.0/16"`, false, `"10.0.0.0/8"`, false)
	replaceAccessControlSection(t, sm.ConfFile(), section)
	msg := reloadAccessControl(t, sm.MonitorURL)
	assert.Contains(t, msg, "access_control reloaded")

	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
}

// MAC-4-002 热加载失败保留旧配置：非法配置（启用态空网段）reload 报错，
// 运行配置保持上一次生效值。
func TestMAC4_002_HotReloadFailureKeepsOld(t *testing.T) {
	sm := startACServer(t, "", true)

	// 先使其生效一个"启用且拒绝"的配置
	section := sprintfAC(true, ``, `"10.10.0.0/16"`, false, `"10.0.0.0/8"`, false)
	replaceAccessControlSection(t, sm.ConfFile(), section)
	msg := reloadAccessControl(t, sm.MonitorURL)
	assert.Contains(t, msg, "access_control reloaded")
	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))

	// 非法配置 reload：响应体为 {"error":...}，且行为不变
	bad := `
[AccessControl]
Enable = true

[[AccessControl.Rules]]
Name = "broken"
PathPrefix = "/"
`
	replaceAccessControlSection(t, sm.ConfFile(), bad)
	msg = reloadAccessControl(t, sm.MonitorURL)
	assert.Contains(t, msg, `"error"`)
	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
}

// MAC-4-003 热加载切换 Audit：从"启用拒绝"切换到"Audit 观察"后放行恢复。
func TestMAC4_003_HotReloadSwitchToAudit(t *testing.T) {
	sm := startACServer(t, "", true)

	section := sprintfAC(true, ``, `"10.10.0.0/16"`, false, `"10.0.0.0/8"`, false)
	replaceAccessControlSection(t, sm.ConfFile(), section)
	msg := reloadAccessControl(t, sm.MonitorURL)
	assert.Contains(t, msg, "access_control reloaded")
	assertRejected(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))

	auditSection := sprintfAC(true, ``, `"10.10.0.0/16"`, true, `"10.0.0.0/8"`, true)
	replaceAccessControlSection(t, sm.ConfFile(), auditSection)
	msg = reloadAccessControl(t, sm.MonitorURL)
	assert.Contains(t, msg, "access_control reloaded")

	assertOpenAPIAllowed(t, rawGet(t, sm.ServerURL, probePathOpenAPI, ""))
}

// MAC-5-001 启动校验：启用态空网段 fail-fast，服务端启动失败。
// 注：进程 fail-fast 退出后 waitForReady 需等待其 10s 超时，属预期耗时。
func TestMAC5_001_EmptySubnetsRejectedAtStartup(t *testing.T) {
	bad := `
[AccessControl]
Enable = true

[[AccessControl.Rules]]
Name = "broken"
PathPrefix = "/"
`
	_, err := testutil.StartServerWithExtraConfig(bad)
	require.Error(t, err, "server with enabled-but-empty-subnets rule must fail to start")
}

// sprintfAC 组装 [AccessControl] 配置段。trustedProxies/subnets 传已带引号的
// TOML 字符串列表（如 "\"127.0.0.0/8\""），空串表示空列表。
func sprintfAC(enable bool, trustedProxies, innerSubnets string, innerAudit bool, mgmtSubnets string, mgmtAudit bool) string {
	return fmt.Sprintf(acSectionTOML,
		boolStr(enable), trustedProxies, innerSubnets, boolStr(innerAudit), mgmtSubnets, boolStr(mgmtAudit))
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
