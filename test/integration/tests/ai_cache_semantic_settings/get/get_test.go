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

package ai_cache_semantic_settings_get_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	settingsPath        = "/open-api/v1/ai-cache-semantic-settings"
	defaultTopK         = 1
	defaultThreshold    = 0.15
	defaultThresholdRel = "lt"
)

// defaultSettingsKeys 为空表默认值对象的精确键集合（无时间戳字段）。
var defaultSettingsKeys = []string{"top_k", "threshold", "threshold_relation"}

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

// ---------- ACSS-2-001 空表 GET 返回默认值对象（无时间戳字段） ----------

// TestSemanticSettings_Get_EmptyDefaults 验证设置行不存在时 GET 返回 200 默认值对象
// （top_k=1 / threshold=0.15 / threshold_relation=lt），键集合恰为 3 业务字段、
// 无 created_at/updated_at——单例资源空表非 404。
// 注意：本用例依赖包内源码顺序最先执行（本包唯一写操作在 ACSS-2-002）。
func TestSemanticSettings_Get_EmptyDefaults(t *testing.T) {
	resp := getSettings(t)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))

	// 键集合精确 3 键：无时间戳字段（合同：默认值对象无 created_at/updated_at）。
	assert.ElementsMatch(t, defaultSettingsKeys, keysOf(data),
		"default settings object must carry exactly the 3 fields (no timestamps)")

	topKVal, ok := data["top_k"].(float64)
	require.True(t, ok, "top_k should be number, got %T", data["top_k"])
	assert.Equal(t, float64(defaultTopK), topKVal)
	assert.InDelta(t, defaultThreshold, data["threshold"], 1e-9)
	assert.Equal(t, defaultThresholdRel, data["threshold_relation"])
}

// ---------- ACSS-2-002 PUT 自定义后 GET 回读一致（含时间戳键） ----------

func TestSemanticSettings_Get_ReadBack(t *testing.T) {
	put, err := testutil.GetClient().Put(settingsPath, map[string]interface{}{
		"top_k": 8, "threshold": 1.25, "threshold_relation": "gt",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, put)

	get := getSettings(t)
	var getData map[string]interface{}
	require.NoError(t, json.Unmarshal(get.Data, &getData))

	assert.Equal(t, float64(8), getData["top_k"])
	assert.InDelta(t, 1.25, getData["threshold"], 1e-9)
	assert.Equal(t, "gt", getData["threshold_relation"])
	assert.NotEmpty(t, getData["created_at"], "created_at must be present after write")
	assert.NotEmpty(t, getData["updated_at"], "updated_at must be present after write")
	assert.Len(t, keysOf(getData), 5,
		"stored settings response must carry exactly the 3 fields + created_at/updated_at")

	// PUT 响应与 GET 回读逐字段一致（家族1 往返）。
	var putData map[string]interface{}
	require.NoError(t, json.Unmarshal(put.Data, &putData))
	assert.Equal(t, putData, getData)
}
