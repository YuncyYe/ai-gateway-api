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

package ai_context_settings_update_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	settingsPath             = "/open-api/v1/ai-context-settings"
	aiContextSettingsAuditID = "ai_context_settings"
)

// settingsKeys 为设置响应的精确顶层键集合（7 业务字段，无时间戳）。
var settingsKeys = []string{
	"trigger_ratio", "keep_latest_images", "tool_result_max_chars",
	"thinking_policy", "chars_per_token", "image_token_estimate", "rewrite",
}

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

func putSettings(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(settingsPath, body)
	require.NoError(t, err)
	return resp
}

func getSettings(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(settingsPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// settingsSnapshot 取当前设置快照，用于回读零变更断言。
func settingsSnapshot(t *testing.T) map[string]interface{} {
	t.Helper()
	resp := getSettings(t)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	return data
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// assertSettingsKeys 锁定响应键集合：顶层恰 7 键、rewrite 恰 2 键（家族11/12 / #201）。
func assertSettingsKeys(t *testing.T, data map[string]interface{}) {
	t.Helper()
	assert.ElementsMatch(t, settingsKeys, keysOf(data), "settings keys must exactly match contract")
	rewrite, ok := data["rewrite"].(map[string]interface{})
	require.True(t, ok, "rewrite must be an object")
	assert.ElementsMatch(t, []string{"strength", "protected_survival_rate"}, keysOf(rewrite))
}

// assertNoTimestamps 锁定响应不携带 created_at/updated_at（marshal 后 NotContains）。
func assertNoTimestamps(t *testing.T, resp *testutil.APIResponse) {
	t.Helper()
	bs, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	assert.NotContains(t, string(bs), "created_at")
	assert.NotContains(t, string(bs), "updated_at")
}

// ---------- CTXS-1-001 全量自定义值（家族1：往返一致 + 键集合锁定） ----------

func TestAIContextSettings_Update_FullCustomValues(t *testing.T) {
	resp := putSettings(t, map[string]interface{}{
		"trigger_ratio":         0.8,
		"keep_latest_images":    4,
		"tool_result_max_chars": 4000,
		"thinking_policy":       "keep",
		"chars_per_token":       3,
		"image_token_estimate":  800,
		"rewrite":               map[string]interface{}{"strength": "full", "protected_survival_rate": 0.9},
	})
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assertSettingsKeys(t, data)
	assert.InDelta(t, 0.8, data["trigger_ratio"], 1e-9)
	assert.Equal(t, float64(4), data["keep_latest_images"])
	assert.Equal(t, float64(4000), data["tool_result_max_chars"])
	assert.Equal(t, "keep", data["thinking_policy"])
	assert.Equal(t, float64(3), data["chars_per_token"])
	assert.Equal(t, float64(800), data["image_token_estimate"])
	rewrite := data["rewrite"].(map[string]interface{})
	assert.Equal(t, "full", rewrite["strength"])
	assert.InDelta(t, 0.9, rewrite["protected_survival_rate"], 1e-9)

	// GET 回读与 PUT 响应逐字段一致。
	getData := settingsSnapshot(t)
	assert.Equal(t, data, getData, "GET readback must equal PUT response field-by-field")
}

// ---------- CTXS-1-002 部分字段回填默认（rewrite 子对象逐字段合并） ----------

func TestAIContextSettings_Update_PartialFields(t *testing.T) {
	resp := putSettings(t, map[string]interface{}{
		"trigger_ratio": 0.8,
		"rewrite":       map[string]interface{}{"strength": "full"},
	})
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assertSettingsKeys(t, data)

	// 显式值原样。
	assert.InDelta(t, 0.8, data["trigger_ratio"], 1e-9)
	rewrite := data["rewrite"].(map[string]interface{})
	assert.Equal(t, "full", rewrite["strength"])

	// 其余 5 顶层字段回填默认。
	assert.Equal(t, float64(2), data["keep_latest_images"])
	assert.Equal(t, float64(2000), data["tool_result_max_chars"])
	assert.Equal(t, "trim-all-but-last", data["thinking_policy"])
	assert.Equal(t, float64(4), data["chars_per_token"])
	assert.Equal(t, float64(1200), data["image_token_estimate"])

	// rewrite 未传字段回填默认（子对象逐字段合并，非整体覆盖）。
	assert.InDelta(t, 0.95, rewrite["protected_survival_rate"], 1e-9)
}

// ---------- CTXS-1-003 空对象 = 全默认（边界值） ----------

func TestAIContextSettings_Update_EmptyBodyDefaults(t *testing.T) {
	resp := putSettings(t, map[string]interface{}{})
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assertSettingsKeys(t, data)
	assert.InDelta(t, 0.7, data["trigger_ratio"], 1e-9)
	assert.Equal(t, float64(2), data["keep_latest_images"])
	assert.Equal(t, float64(2000), data["tool_result_max_chars"])
	assert.Equal(t, "trim-all-but-last", data["thinking_policy"])
	assert.Equal(t, float64(4), data["chars_per_token"])
	assert.Equal(t, float64(1200), data["image_token_estimate"])
	rewrite := data["rewrite"].(map[string]interface{})
	assert.Equal(t, "lite", rewrite["strength"])
	assert.InDelta(t, 0.95, rewrite["protected_survival_rate"], 1e-9)
}

// ---------- CTXS-1-004 422 负向矩阵（家族3/4：整体拒绝 + 回读零变更） ----------

func TestAIContextSettings_Update_RejectionMatrix(t *testing.T) {
	// 先写入一份合法自定义设置作为"现状"快照。
	putResp := putSettings(t, map[string]interface{}{
		"trigger_ratio": 0.8, "keep_latest_images": 4,
	})
	testutil.AssertSuccess(t, putResp)
	before := settingsSnapshot(t)

	tests := []struct {
		name string
		body map[string]interface{}
		// wantErrField 非空时断言错误消息可归因到该字段（家族4；照 ai_cache
		// AC-1-005/006 的 ErrMsg 归因先例）。
		wantErrField string
	}{
		{"trigger_ratio=0", map[string]interface{}{"trigger_ratio": 0}, "trigger_ratio"},
		{"trigger_ratio=1.1", map[string]interface{}{"trigger_ratio": 1.1}, "trigger_ratio"},
		{"keep_latest_images=-1", map[string]interface{}{"keep_latest_images": -1}, ""},
		{"tool_result_max_chars=-1", map[string]interface{}{"tool_result_max_chars": -1}, ""},
		{"thinking_policy 非法", map[string]interface{}{"thinking_policy": "drop"}, ""},
		{"chars_per_token=0", map[string]interface{}{"chars_per_token": 0}, ""},
		{"rewrite.strength 非法", map[string]interface{}{"rewrite": map[string]interface{}{"strength": "max"}}, "rewrite.strength"},
		{"rewrite.protected_survival_rate=0", map[string]interface{}{"rewrite": map[string]interface{}{"protected_survival_rate": 0}}, ""},
		{"rewrite.protected_survival_rate=1.1", map[string]interface{}{"rewrite": map[string]interface{}{"protected_survival_rate": 1.1}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := putSettings(t, tt.body)
			testutil.AssertErrCode(t, resp, 422)
			if tt.wantErrField != "" {
				assert.Contains(t, resp.ErrMsg, tt.wantErrField,
					"error message must be attributable to field %q (家族4)", tt.wantErrField)
			}

			after := settingsSnapshot(t)
			assert.Equal(t, before, after, "rejected PUT must leave the settings unchanged (家族3)")
		})
	}
}

// waitForAIContextSettingsAudit 轮询操作日志，返回匹配 match 的首条记录。
// 单例资源的所有 PUT 审计身份相同（resource_id 恒为 ai_context_settings），
// 因此必须按 change_summary 内容匹配到具体操作，不能取 List[0]。
func waitForAIContextSettingsAudit(t *testing.T, startTs int64, status string, match func(*testutil.OperationLogEntry) bool) *testutil.OperationLogEntry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, _, err := testutil.QueryOperationLogs(map[string]string{
			"resource_type": "ai_context_settings",
			"action":        "update",
			"status":        status,
			"start_time":    fmt.Sprintf("%d", startTs),
			"page_size":     "100",
		})
		require.NoError(t, err)
		for i := range result.List {
			if match(&result.List[i]) {
				return &result.List[i]
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("operation log not found after 15s (status=%s, since=%d)", status, startTs)
	return nil
}

// ---------- CTXS-1-006 操作审计（家族7 / #201 / #155） ----------

func TestAIContextSettings_Update_OperationLog(t *testing.T) {
	t.Run("success_audit", func(t *testing.T) {
		// 0.81 为特征值（本包其他用例均写 0.8），用于把审计精确匹配到本用例。
		const markerRatio = 0.81
		startTs := time.Now().Add(-2 * time.Second).Unix()

		putResp := putSettings(t, map[string]interface{}{
			"trigger_ratio": markerRatio,
		})
		testutil.AssertSuccess(t, putResp)

		entry := waitForAIContextSettingsAudit(t, startTs, "1", func(e *testutil.OperationLogEntry) bool {
			after, ok := e.ChangeSummary["after"].(map[string]interface{})
			if !ok {
				return false
			}
			ratio, ok := after["trigger_ratio"].(float64)
			return ok && ratio == markerRatio
		})
		require.NotNil(t, entry)

		assert.Equal(t, "update", entry.Action)
		assert.Equal(t, "ai_context_settings", entry.ResourceType)
		// 单例资源身份固定，不依赖请求体。
		assert.Equal(t, aiContextSettingsAuditID, entry.ResourceID)
		assert.Equal(t, aiContextSettingsAuditID, entry.ResourceName)
		assert.Equal(t, float64(1), entry.Status)

		// change_summary 键集合精确为 before/after/diff_keys（#201：禁止 Contains）。
		cs := entry.ChangeSummary
		require.NotNil(t, cs)
		assert.ElementsMatch(t, []string{"before", "after", "diff_keys"}, keysOf(cs))

		// before/after 快照为设置对象（小写 API 词汇 + rewrite 子对象）。
		for _, key := range []string{"before", "after"} {
			snap, ok := cs[key].(map[string]interface{})
			require.True(t, ok, "change_summary.%s should be object", key)
			assert.Contains(t, snap, "trigger_ratio")
			assert.Contains(t, snap, "rewrite")
		}

		// 快照 JSON 序列化后不含内部 "id" 键。
		raw, err := json.Marshal(cs)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"id"`, "audit snapshot must not leak internal id")
	})

	t.Run("failure_audit", func(t *testing.T) {
		startTs := time.Now().Unix()

		// PUT 422（trigger_ratio 越界）：合同要求同样记录失败审计（家族7/#155）。
		resp := putSettings(t, map[string]interface{}{"trigger_ratio": 1.5})
		testutil.AssertErrCode(t, resp, 422)
		assert.Contains(t, resp.ErrMsg, "trigger_ratio", "error message must be attributable to trigger_ratio field (家族4)")

		entry := waitForAIContextSettingsAudit(t, startTs, "2", func(e *testutil.OperationLogEntry) bool {
			return e.ResourceType == "ai_context_settings" && e.Action == "update"
		})
		require.NotNil(t, entry)

		assert.Equal(t, float64(2), entry.Status, "failed PUT must be audited with failed status")
		// 身份取自服务端固定资源标识，不依赖请求体（#155）。
		assert.Equal(t, aiContextSettingsAuditID, entry.ResourceID)
		assert.Equal(t, aiContextSettingsAuditID, entry.ResourceName)
		assert.NotEmpty(t, entry.ErrorMsg, "failed audit should carry error reason")
	})
}

// ---------- CTXS-1-005 合同锁定（家族11/12：无时间戳） ----------

func TestAIContextSettings_Update_NoTimestamps(t *testing.T) {
	resp := putSettings(t, map[string]interface{}{
		"trigger_ratio": 0.8,
		"rewrite":       map[string]interface{}{"strength": "full"},
	})
	testutil.AssertSuccess(t, resp)
	assertNoTimestamps(t, resp)

	getResp := getSettings(t)
	assertNoTimestamps(t, getResp)
}
