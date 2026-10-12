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
//limitations under the License.

// Package param_binding_test 是 xreq.BindForm/BindURI 类型转换错误的
// 跨域回归扫批（issue #233 根因修复）：所有以 *bool/*int/*int64/*int8
// struct tag 绑定 form 的字段，非法取值必须返回 HTTP 422
// "Param Illegal: invalid <name>: <value>"，不得 500 或回显 strconv 原文。
//
// 覆盖面（对 design-docs/modifications/2026-10-12-issue-233-bindform-conversion-500
// change-summary.md §6.2 扫批清单）：
//   - api_keys:   enabled / unlimited_quota / page / page_size
//   - route_tables(RouteRulesFilter): enabled / page
//   - security(reencrypt-sweeps): dry_run
//   - operation_logs: status / start_time
//   - batches: limit / cursor
//   - entities / entity_types: page（entity_types 另含 id/level）
package param_binding_test

import (
	"os"
	"strings"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	var err error
	sm, err = testutil.StartServer()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	sm.Shutdown()
	os.Exit(code)
}

// TestInvalidFormConversionResolves422 扫批：BindForm 数值/布尔字段非法取值。
func TestInvalidFormConversionResolves422(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		query map[string]string
		// wantMsg 为必含的字段感知文案片段（形如 "invalid enabled: not-bool"）
		wantMsg string
	}{
		// api_keys（APIKeyListReq）
		{"api_keys_enabled", "/open-api/v1/api-keys", map[string]string{"enabled": "not-bool"}, "invalid enabled: not-bool"},
		{"api_keys_unlimited_quota", "/open-api/v1/api-keys", map[string]string{"unlimited_quota": "not-bool"}, "invalid unlimited_quota: not-bool"},
		{"api_keys_page", "/open-api/v1/api-keys", map[string]string{"page": "abc"}, "invalid page: abc"},
		{"api_keys_page_size", "/open-api/v1/api-keys", map[string]string{"page_size": "abc"}, "invalid page_size: abc"},
		// route_tables（shared.RouteRulesFilter，覆盖 SC2101-TC029 场景）
		{"route_tables_enabled", "/open-api/v1/route-tables", map[string]string{"enabled": "not-bool"}, "invalid enabled: not-bool"},
		{"route_tables_page", "/open-api/v1/route-tables", map[string]string{"page": "abc"}, "invalid page: abc"},
		// security reencrypt-sweeps
		{"sweeps_dry_run", "/open-api/v1/security/reencrypt-sweeps", map[string]string{"dry_run": "not-bool"}, "invalid dry_run: not-bool"},
		// operation_logs
		{"operation_logs_status", "/open-api/v1/operation-logs", map[string]string{"status": "abc"}, "invalid status: abc"},
		{"operation_logs_start_time", "/open-api/v1/operation-logs", map[string]string{"start_time": "abc"}, "invalid start_time: abc"},
		// batches
		{"batches_limit", "/open-api/v1/batches", map[string]string{"limit": "abc"}, "invalid limit: abc"},
		{"batches_cursor", "/open-api/v1/batches", map[string]string{"cursor": "abc"}, "invalid cursor: abc"},
		// entities / entity_types（Pagination 内嵌 + 自有数值字段）
		{"entities_page", "/open-api/v1/entities", map[string]string{"page": "abc"}, "invalid page: abc"},
		{"entity_types_page", "/open-api/v1/entity-types", map[string]string{"page": "abc"}, "invalid page: abc"},
		{"entity_types_id", "/open-api/v1/entity-types", map[string]string{"id": "abc"}, "invalid id: abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := testutil.GetClient().Get(tc.path, tc.query)
			require.NoError(t, err)
			require.NotNil(t, resp)
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, "Param Illegal")
			assert.Contains(t, resp.ErrMsg, tc.wantMsg)
			assert.NotContains(t, resp.ErrMsg, "strconv")
		})
	}
}

// TestValidFormValuesUnchanged 合法取值/缺省/空值保持 200（成功路径零变化回归）。
func TestValidFormValuesUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		query map[string]string
	}{
		{"api_keys_bool_and_page", "/open-api/v1/api-keys", map[string]string{"enabled": "true", "unlimited_quota": "false", "page": "1", "page_size": "20"}},
		{"route_tables_bool", "/open-api/v1/route-tables", map[string]string{"enabled": "true"}},
		{"operation_logs_status", "/open-api/v1/operation-logs", map[string]string{"status": "1"}},
		{"batches_limit", "/open-api/v1/batches", map[string]string{"limit": "10"}},
		{"entity_types_level", "/open-api/v1/entity-types", map[string]string{"level": "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := testutil.GetClient().Get(tc.path, tc.query)
			require.NoError(t, err)
			require.NotNil(t, resp)
			testutil.AssertSuccess(t, resp)
		})
	}
}

// TestInvalidFormConversionNoLeak 显式断言错误信息不回显 Go 内部解析错误
// （错误信息卫生，issue #233 影响面条款）。
func TestInvalidFormConversionNoLeak(t *testing.T) {
	resp, err := testutil.GetClient().Get("/open-api/v1/api-keys", map[string]string{"enabled": "not-bool"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	testutil.AssertErrCode(t, resp, 422)
	for _, leak := range []string{"strconv", "ParseBool", "invalid syntax", "goroutine"} {
		assert.False(t, strings.Contains(resp.ErrMsg, leak), "ErrMsg leaks %q: %s", leak, resp.ErrMsg)
	}
}
