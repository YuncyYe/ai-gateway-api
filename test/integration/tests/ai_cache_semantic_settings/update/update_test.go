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

package ai_cache_semantic_settings_update_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	settingsPath         = "/open-api/v1/ai-cache-semantic-settings"
	defaultTopK          = 1
	defaultThreshold     = 0.15
	defaultThresholdRel  = "lt"
	settingsStoredKeyNum = 5
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

// ---------- helpers ----------

func putSettings(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(settingsPath, body)
	require.NoError(t, err)
	return resp
}

func putSettingsOK(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp := putSettings(t, body)
	testutil.AssertSuccess(t, resp)
	return resp
}

func getSettings(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(settingsPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// settingsSnapshot 取当前设置全量快照，用于拒绝后零变更断言。
func settingsSnapshot(t *testing.T) map[string]interface{} {
	t.Helper()
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(getSettings(t).Data, &data))
	return data
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// assertSettingsValues 断言设置对象的三个业务字段取值（数值做 float64 规整）。
func assertSettingsValues(t *testing.T, data map[string]interface{}, topK int, threshold float64, relation string) {
	t.Helper()
	topKRaw, ok := data["top_k"]
	require.True(t, ok, "top_k must be present")
	topKVal, ok := topKRaw.(float64)
	require.True(t, ok, "top_k should be number, got %T", topKRaw)
	assert.Equal(t, float64(topK), topKVal, "top_k")

	assert.InDelta(t, threshold, data["threshold"], 1e-9, "threshold")
	assert.Equal(t, relation, data["threshold_relation"], "threshold_relation")
}

// ---------- ACSS-1-001 全字段自定义 ----------

// TestSemanticSettings_Update_FullParams PUT 全字段自定义值 → 200，
// 响应三字段精确回显且携带时间戳；GET 回读逐字段一致。
func TestSemanticSettings_Update_FullParams(t *testing.T) {
	resp := putSettingsOK(t, map[string]interface{}{
		"top_k": 6, "threshold": 0.9, "threshold_relation": "lte",
	})

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assertSettingsValues(t, data, 6, 0.9, "lte")
	assert.NotEmpty(t, data["created_at"], "created_at should be present after write")
	assert.NotEmpty(t, data["updated_at"], "updated_at should be present after write")
	assert.Len(t, keysOf(data), settingsStoredKeyNum,
		"stored settings response must carry exactly the 3 fields + created_at/updated_at")

	get := getSettings(t)
	var getData map[string]interface{}
	require.NoError(t, json.Unmarshal(get.Data, &getData))
	assert.Equal(t, data, getData, "GET read-back must equal PUT response field-by-field")
}

// ---------- ACSS-1-002 部分字段省略 → 回落默认（全对象 upsert 语义） ----------

// TestSemanticSettings_Update_PartialFieldsFallBack 验证省略字段按文档默认值
// 回落（top_k=1 / threshold=0.15 / threshold_relation=lt），且为整行覆盖
// （上次写入的自定义值不保留）。
func TestSemanticSettings_Update_PartialFieldsFallBack(t *testing.T) {
	t.Run("only top_k keeps other defaults", func(t *testing.T) {
		resp := putSettingsOK(t, map[string]interface{}{"top_k": 5})
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		assertSettingsValues(t, data, 5, defaultThreshold, defaultThresholdRel)

		get := getSettings(t)
		testutil.AssertDataFieldEquals(t, get, "top_k", int64(5))
		testutil.AssertDataFieldEquals(t, get, "threshold", defaultThreshold)
		testutil.AssertDataFieldEquals(t, get, "threshold_relation", defaultThresholdRel)
	})

	t.Run("only relation resets prior custom values", func(t *testing.T) {
		// 上次写入 top_k=5；本次仅提交 relation → top_k 回落默认 1（整行覆盖非合并）。
		resp := putSettingsOK(t, map[string]interface{}{"threshold_relation": "gte"})
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		assertSettingsValues(t, data, defaultTopK, defaultThreshold, "gte")

		get := getSettings(t)
		testutil.AssertDataFieldEquals(t, get, "top_k", int64(defaultTopK))
		testutil.AssertDataFieldEquals(t, get, "threshold", defaultThreshold)
		testutil.AssertDataFieldEquals(t, get, "threshold_relation", "gte")
	})

	t.Run("empty body all defaults", func(t *testing.T) {
		resp := putSettingsOK(t, map[string]interface{}{})
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		assertSettingsValues(t, data, defaultTopK, defaultThreshold, defaultThresholdRel)
		testutil.AssertDataFieldNotEmpty(t, resp, "created_at")
	})
}

// ---------- ACSS-1-003 422 边界矩阵 + 校验失败设置不变 ----------

// TestSemanticSettings_Update_InvalidValueMatrix 表驱动覆盖 top_k/threshold/
// threshold_relation 越界、枚举大小写敏感与类型错误；每个负向后 GET 零变更。
func TestSemanticSettings_Update_InvalidValueMatrix(t *testing.T) {
	// 先落一个合法基线，使"设置不变"断言有判别力。
	putSettingsOK(t, map[string]interface{}{
		"top_k": 3, "threshold": 0.42, "threshold_relation": "gt",
	})
	before := settingsSnapshot(t)

	tests := []struct {
		name string
		body map[string]interface{}
		want string
	}{
		{"top_k zero", map[string]interface{}{"top_k": 0}, "top_k"},
		{"top_k eleven", map[string]interface{}{"top_k": 11}, "top_k"},
		{"top_k wrong type", map[string]interface{}{"top_k": "abc"}, "top_k"},
		{"threshold negative", map[string]interface{}{"threshold": -0.1}, "threshold"},
		{"threshold above two", map[string]interface{}{"threshold": 2.1}, "threshold"},
		{"threshold wrong type", map[string]interface{}{"threshold": "high"}, "threshold"},
		{"relation upper case", map[string]interface{}{"threshold_relation": "LT"}, "threshold_relation"},
		{"relation unknown", map[string]interface{}{"threshold_relation": "xx"}, "threshold_relation"},
		{"relation wrong type", map[string]interface{}{"threshold_relation": 123}, "threshold_relation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := putSettings(t, tt.body)
			// 错误码语义（家族7/#183）：管理写操作只允许 2xx/4xx，500 即缺陷。
			require.NotEqual(t, 500, resp.ErrNum, "write op must never return 500")
			testutil.AssertErrCode(t, resp, 422)

			assert.Equal(t, before, settingsSnapshot(t),
				"rejected PUT must leave settings unchanged (家族3)")
		})
	}
}

// ---------- ACSS-1-004 边界正值（家族4 边界±1） ----------

// TestSemanticSettings_Update_BoundaryValues 验证合法边界：top_k∈{1,10}、
// threshold∈{0,2}、relation∈{lte,gt,gte} 均 200 且回读一致。
func TestSemanticSettings_Update_BoundaryValues(t *testing.T) {
	boundaryCases := []struct {
		name      string
		body      map[string]interface{}
		wantTopK  int
		wantThres float64
		wantRel   string
	}{
		{"top_k lower bound", map[string]interface{}{"top_k": 1, "threshold": 0.5, "threshold_relation": "lt"}, 1, 0.5, "lt"},
		{"top_k upper bound", map[string]interface{}{"top_k": 10, "threshold": 0.5, "threshold_relation": "lt"}, 10, 0.5, "lt"},
		{"threshold zero", map[string]interface{}{"top_k": 2, "threshold": 0, "threshold_relation": "lte"}, 2, 0, "lte"},
		{"threshold two", map[string]interface{}{"top_k": 2, "threshold": 2, "threshold_relation": "gt"}, 2, 2, "gt"},
		{"relation gte", map[string]interface{}{"top_k": 2, "threshold": 1.5, "threshold_relation": "gte"}, 2, 1.5, "gte"},
	}
	for _, tt := range boundaryCases {
		t.Run(tt.name, func(t *testing.T) {
			resp := putSettingsOK(t, tt.body)
			var data map[string]interface{}
			require.NoError(t, json.Unmarshal(resp.Data, &data))
			assertSettingsValues(t, data, tt.wantTopK, tt.wantThres, tt.wantRel)

			get := getSettings(t)
			var getData map[string]interface{}
			require.NoError(t, json.Unmarshal(get.Data, &getData))
			assertSettingsValues(t, getData, tt.wantTopK, tt.wantThres, tt.wantRel)
		})
	}
}

// ---------- ACSS-1-005 非法 JSON ----------

func TestSemanticSettings_Update_InvalidJSON(t *testing.T) {
	before := settingsSnapshot(t)
	resp, err := testutil.GetClient().RawBody("PUT", settingsPath, "not-json", "application/json")
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 422)
	assert.Equal(t, before, settingsSnapshot(t), "malformed body must leave settings unchanged")
}
