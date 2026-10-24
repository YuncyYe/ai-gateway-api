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

package batch_limits_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	var err error
	sm, err = testutil.StartServerAuto()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	sm.Shutdown()
	os.Exit(code)
}

// batchLimitsBody 构造内嵌 batch_limits 四维度（全正数）的 api_key body。
func batchLimitsBody(desc string, limits map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"description": desc,
		"rate_limit_policy": map[string]interface{}{
			"enabled": true,
			"rules": map[string]interface{}{
				"batch_limits": limits,
			},
		},
	}
}

// ---------- AK-BL-1-001 创建内嵌 batch_limits 四维度成功 ----------

func TestBatchLimits_CreateFourDimensions(t *testing.T) {
	body := batchLimitsBody("ak-bl-create", map[string]interface{}{
		"max_create_rpm":     30,
		"max_active_batches": 5,
		"max_file_bytes":     104857600,
		"max_file_lines":     100000,
	})
	resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", body)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	idVal, err := testutil.GetDataField(resp, "id")
	require.NoError(t, err)
	apiKeyID := idVal.(string)
	defer testutil.DeleteAPIKey(apiKeyID)

	// 回读：rate_limit_policy.rules.batch_limits 四维度一致
	getResp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
	require.NoError(t, err)
	testutil.AssertSuccess(t, getResp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(getResp.Data, &data))
	policy, ok := data["rate_limit_policy"].(map[string]interface{})
	require.True(t, ok, "rate_limit_policy must exist")
	rules, ok := policy["rules"].(map[string]interface{})
	require.True(t, ok, "rate_limit_policy.rules must exist")
	bl, ok := rules["batch_limits"].(map[string]interface{})
	require.True(t, ok, "rate_limit_policy.rules.batch_limits must exist")
	assert.Equal(t, float64(30), bl["max_create_rpm"])
	assert.Equal(t, float64(5), bl["max_active_batches"])
	assert.Equal(t, float64(104857600), bl["max_file_bytes"])
	assert.Equal(t, float64(100000), bl["max_file_lines"])
}

// ---------- AK-BL-1-002 负维度值 → 422 ----------

func TestBatchLimits_NegativeDimensions(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value int
	}{
		{"max_create_rpm=-1", "max_create_rpm", -1},
		{"max_active_batches=-1", "max_active_batches", -1},
		{"max_file_bytes=-1", "max_file_bytes", -1},
		{"max_file_lines=-1", "max_file_lines", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := batchLimitsBody("ak-bl-neg-"+tt.key, map[string]interface{}{
				tt.key: tt.value,
			})
			resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", body)
			require.NoError(t, err)
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, "batch_limits")
		})
	}
}

// fetchBatchSegment 导出 rate-limit-policy，按 api key 值定位策略并返回
// rules.batch 段原始 JSON（不存在返回 nil）。
func fetchBatchSegment(t *testing.T, apiKeyValue string) (map[string]interface{}, string) {
	t.Helper()
	resp, err := testutil.GetClient().Get("/inner-api/v1/configs/rate-limit-policy")
	require.NoError(t, err)
	require.Equal(t, 200, resp.ErrNum)

	var data map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(resp.Data, &data))

	var bindings map[string][]string
	require.NoError(t, json.Unmarshal(data["ApikeyRateLimitPolicyBindings"], &bindings))
	policies, ok := bindings[apiKeyValue]
	require.True(t, ok, "api key %s must have bound policies", apiKeyValue)
	require.NotEmpty(t, policies)
	policyKey := policies[0]

	var policiesMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data["RateLimitPolicies"], &policiesMap))
	policyRaw, ok := policiesMap[policyKey]
	require.True(t, ok, "policy %s must exist in RateLimitPolicies", policyKey)

	var policy struct {
		Rules struct {
			Batch json.RawMessage `json:"batch"`
		} `json:"rules"`
	}
	require.NoError(t, json.Unmarshal(policyRaw, &policy))
	if len(policy.Rules.Batch) == 0 {
		return nil, policyKey
	}
	var seg map[string]interface{}
	require.NoError(t, json.Unmarshal(policy.Rules.Batch, &seg))
	return seg, policyKey
}

func fetchAPIKeyValue(t *testing.T, apiKeyID string) string {
	t.Helper()
	resp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	val, err := testutil.GetDataField(resp, "key")
	require.NoError(t, err)
	return val.(string)
}

// ---------- AK-BL-1-003 InnerAPI 导出：rules.batch 段 + redis_key ----------

func TestBatchLimits_InnerExportBatchSegment(t *testing.T) {
	body := batchLimitsBody("ak-bl-export", map[string]interface{}{
		"max_create_rpm":     30,
		"max_active_batches": 5,
		"max_file_bytes":     104857600,
		"max_file_lines":     100000,
	})
	resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", body)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	idVal, err := testutil.GetDataField(resp, "id")
	require.NoError(t, err)
	apiKeyID := idVal.(string)
	defer testutil.DeleteAPIKey(apiKeyID)

	apiKeyValue := fetchAPIKeyValue(t, apiKeyID)

	seg, _ := fetchBatchSegment(t, apiKeyValue)
	require.NotNil(t, seg, "rules.batch segment must exist for policy with batch_limits")
	assert.Equal(t, float64(30), seg["max_create_rpm"])
	assert.Equal(t, float64(5), seg["max_active_batches"])
	assert.Equal(t, float64(104857600), seg["max_file_bytes"])
	assert.Equal(t, float64(100000), seg["max_file_lines"])

	// redis_key：实现全限定形态为
	// default_bfe_rlp-{policyID}_RL_BATCH_rlp-{policyID}_rpm
	//（model/shared.BuildBatchRateLimitRedisKey，生成惯例同 RL_TPM/RL_RPM）。
	// 仅 max_create_rpm>0 时生成（该策略 30>0）。
	redisKey, ok := seg["redis_key"].(string)
	require.True(t, ok, "rules.batch.redis_key must exist when max_create_rpm>0")
	assert.Contains(t, redisKey, "RL_BATCH")
	assert.Contains(t, redisKey, "rpm")
}

// ---------- AK-BL-1-004 不带 batch_limits 的策略无 batch 段 ----------

func TestBatchLimits_InnerExportNoBatchSegment(t *testing.T) {
	resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", map[string]interface{}{
		"description": "ak-bl-nobatch",
		"rate_limit_policy": map[string]interface{}{
			"enabled": true,
			"rules": map[string]interface{}{
				"tpm": []interface{}{
					map[string]interface{}{
						"name": "1m", "model": "*", "window_minutes": 1, "max_tokens": 10000, "step_minutes": 1,
					},
				},
			},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	idVal, err := testutil.GetDataField(resp, "id")
	require.NoError(t, err)
	apiKeyID := idVal.(string)
	defer testutil.DeleteAPIKey(apiKeyID)

	apiKeyValue := fetchAPIKeyValue(t, apiKeyID)

	seg, _ := fetchBatchSegment(t, apiKeyValue)
	assert.Nil(t, seg, "policy without batch_limits must have no rules.batch segment")
}

// ---------- AK-BL-1-005 max_create_rpm=0 不生成 redis_key ----------

func TestBatchLimits_InnerExportZeroCreateRPMNoKey(t *testing.T) {
	body := batchLimitsBody("ak-bl-zero-rpm", map[string]interface{}{
		"max_create_rpm":     0,
		"max_active_batches": 5,
	})
	resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", body)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	idVal, err := testutil.GetDataField(resp, "id")
	require.NoError(t, err)
	apiKeyID := idVal.(string)
	defer testutil.DeleteAPIKey(apiKeyID)

	apiKeyValue := fetchAPIKeyValue(t, apiKeyID)

	seg, _ := fetchBatchSegment(t, apiKeyValue)
	require.NotNil(t, seg, "rules.batch segment must exist (max_active_batches>0)")
	_, hasKey := seg["redis_key"]
	assert.False(t, hasKey, "redis_key must be omitted when max_create_rpm=0")
}
