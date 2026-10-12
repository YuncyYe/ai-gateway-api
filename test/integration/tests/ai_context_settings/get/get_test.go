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

package ai_context_settings_get_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const settingsPath = "/open-api/v1/ai-context-settings"

// defaultSettingsKeys 为空表默认值对象的精确顶层键集合（7 业务字段，无时间戳）。
var defaultSettingsKeys = []string{
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

func getSettings(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(settingsPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// assertSettingsDefaults 断言设置对象为 8 项文档默认值（含 rewrite 子对象）。
func assertSettingsDefaults(t *testing.T, data map[string]interface{}) {
	t.Helper()
	assert.InDelta(t, 0.7, data["trigger_ratio"], 1e-9)
	assert.Equal(t, float64(2), data["keep_latest_images"])
	assert.Equal(t, float64(2000), data["tool_result_max_chars"])
	assert.Equal(t, "trim-all-but-last", data["thinking_policy"])
	assert.Equal(t, float64(4), data["chars_per_token"])
	assert.Equal(t, float64(1200), data["image_token_estimate"])

	rewrite, ok := data["rewrite"].(map[string]interface{})
	require.True(t, ok, "rewrite must be an object")
	// keysOf 基于 map 迭代序，必须按集合比较，否则 ~50% 概率假失败
	//（Go map 迭代序随机；2026-10-12 全量集成 -count=8 复现 4/8 失败）。
	assert.ElementsMatch(t, []string{"strength", "protected_survival_rate"}, keysOf(rewrite))
	assert.Equal(t, "lite", rewrite["strength"])
	assert.InDelta(t, 0.95, rewrite["protected_survival_rate"], 1e-9)
}

// ---------- CTXS-2-001 空表 GET 返回默认值对象（无时间戳字段） ----------

// TestAIContextSettings_Get_EmptyDefaults 验证设置行不存在时 GET 返回 200 默认值对象
// （0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95），顶层键集合恰 7 键、
// 无 created_at/updated_at——单例资源空表非 404。
// 注意：本用例依赖包内源码顺序最先执行（本包内只有 CTXS-2-002 会写入设置）。
func TestAIContextSettings_Get_EmptyDefaults(t *testing.T) {
	resp := getSettings(t)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))

	assert.ElementsMatch(t, defaultSettingsKeys, keysOf(data),
		"default settings object must carry exactly the 7 top-level fields (no timestamps)")
	assertSettingsDefaults(t, data)
}

// ---------- CTXS-2-002 PUT 自定义后 GET 回读一致 ----------

func TestAIContextSettings_Get_ReadBack(t *testing.T) {
	put, err := testutil.GetClient().Put(settingsPath, map[string]interface{}{
		"trigger_ratio":         0.8,
		"keep_latest_images":    4,
		"tool_result_max_chars": 4000,
		"thinking_policy":       "keep",
		"chars_per_token":       3,
		"image_token_estimate":  800,
		"rewrite":               map[string]interface{}{"strength": "full", "protected_survival_rate": 0.9},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, put)

	get := getSettings(t)
	var getData map[string]interface{}
	require.NoError(t, json.Unmarshal(get.Data, &getData))

	assert.Equal(t, float64(4), getData["keep_latest_images"])
	assert.Equal(t, "keep", getData["thinking_policy"])
	getRewrite, ok := getData["rewrite"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "full", getRewrite["strength"])
	assert.InDelta(t, 0.9, getRewrite["protected_survival_rate"], 1e-9)

	// PUT 响应与 GET 回读逐字段一致（家族1 往返）。
	var putData map[string]interface{}
	require.NoError(t, json.Unmarshal(put.Data, &putData))
	assert.Equal(t, putData, getData)
}
