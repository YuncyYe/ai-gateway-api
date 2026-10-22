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

// setupEntityType 创建一个测试用 Entity-Type，返回 typeName。
func setupEntityType(t *testing.T) string {
	t.Helper()
	typeName := testutil.UniqueEntityTypeName()
	_, err := testutil.CreateEntityType(typeName, 1)
	require.NoError(t, err, "setup entity-type failed")
	return typeName
}

// batchLimitsEntityBody 构造内嵌 batch_limits 的 entity body。
func batchLimitsEntityBody(name, typeName string, limits map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		"type": typeName,
		"rate_limit_policy": map[string]interface{}{
			"enabled": true,
			"rules": map[string]interface{}{
				"batch_limits": limits,
			},
		},
	}
}

// createBatchLimitsEntity 创建内嵌 batch_limits 的 Entity 并注册清理。
func createBatchLimitsEntity(t *testing.T, typeName, desc string, limits map[string]interface{}) string {
	t.Helper()
	body := batchLimitsEntityBody(testutil.UniqueEntityName(), typeName, limits)
	resp, err := testutil.GetClient().Post("/open-api/v1/entities", body)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	idVal, err := testutil.GetDataField(resp, "id")
	require.NoError(t, err)
	entityID := idVal.(string)
	t.Cleanup(func() {
		_ = testutil.DeleteEntity(entityID)
	})
	return entityID
}

// fetchEntityRules 读取 Entity 的 rate_limit_policy.rules。
func fetchEntityRules(t *testing.T, entityID string) map[string]interface{} {
	t.Helper()
	resp, err := testutil.GetClient().Get("/open-api/v1/entities/" + entityID)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	policy, ok := data["rate_limit_policy"].(map[string]interface{})
	require.True(t, ok, "rate_limit_policy must exist")
	rules, ok := policy["rules"].(map[string]interface{})
	require.True(t, ok, "rate_limit_policy.rules must exist")
	return rules
}

// findBatchSegment 导出 rate-limit-policy，按挂载 API-Key 的值定位其层级
// 策略（含祖先 Entity 内联策略），返回 rules.batch 段；未找到返回 nil。
func findBatchSegment(t *testing.T, apiKeyValue string) map[string]interface{} {
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

	var policiesMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data["RateLimitPolicies"], &policiesMap))

	for _, policyKey := range policies {
		policyRaw, ok := policiesMap[policyKey]
		require.True(t, ok, "policy %s must exist in RateLimitPolicies", policyKey)
		var policy struct {
			Rules struct {
				Batch json.RawMessage `json:"batch"`
			} `json:"rules"`
		}
		require.NoError(t, json.Unmarshal(policyRaw, &policy))
		if len(policy.Rules.Batch) == 0 {
			continue
		}
		var seg map[string]interface{}
		require.NoError(t, json.Unmarshal(policy.Rules.Batch, &seg))
		return seg
	}
	return nil
}

// mountAPIKeyOnEntity 在 Entity 下挂载一个不带限流策略的 API-Key，
// 返回 api key 值（导出绑定按 api key 值定位）。
func mountAPIKeyOnEntity(t *testing.T, entityID, desc string) (string, string) {
	t.Helper()
	apiKeyID, apiKeyValue, err := testutil.CreateAPIKeyWithKey(desc, entityID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = testutil.DeleteAPIKey(apiKeyID)
	})
	return apiKeyID, apiKeyValue
}

// ---------- ET-BL-1-001 创建内嵌 batch_limits 四维度成功 ----------

func TestBatchLimits_CreateFourDimensions(t *testing.T) {
	typeName := setupEntityType(t)
	t.Cleanup(func() { _ = testutil.DeleteEntityType(typeName) })

	entityID := createBatchLimitsEntity(t, typeName, "et-bl-create", map[string]interface{}{
		"max_create_rpm":     30,
		"max_active_batches": 5,
		"max_file_bytes":     104857600,
		"max_file_lines":     100000,
	})

	// 回读：rate_limit_policy.rules.batch_limits 四维度一致
	rules := fetchEntityRules(t, entityID)
	bl, ok := rules["batch_limits"].(map[string]interface{})
	require.True(t, ok, "rate_limit_policy.rules.batch_limits must exist")
	assert.Equal(t, float64(30), bl["max_create_rpm"])
	assert.Equal(t, float64(5), bl["max_active_batches"])
	assert.Equal(t, float64(104857600), bl["max_file_bytes"])
	assert.Equal(t, float64(100000), bl["max_file_lines"])
}

// ---------- ET-BL-1-002 负维度值 → 422 ----------

func TestBatchLimits_NegativeDimensions(t *testing.T) {
	typeName := setupEntityType(t)
	t.Cleanup(func() { _ = testutil.DeleteEntityType(typeName) })

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
			body := batchLimitsEntityBody(testutil.UniqueEntityName(), typeName, map[string]interface{}{
				tt.key: tt.value,
			})
			resp, err := testutil.GetClient().Post("/open-api/v1/entities", body)
			require.NoError(t, err)
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, "batch_limits")
		})
	}
}

// ---------- ET-BL-1-003 InnerAPI 导出：rules.batch 段 + redis_key ----------

func TestBatchLimits_InnerExportBatchSegment(t *testing.T) {
	typeName := setupEntityType(t)
	t.Cleanup(func() { _ = testutil.DeleteEntityType(typeName) })

	entityID := createBatchLimitsEntity(t, typeName, "et-bl-export", map[string]interface{}{
		"max_create_rpm":     30,
		"max_active_batches": 5,
		"max_file_bytes":     104857600,
		"max_file_lines":     100000,
	})
	_, apiKeyValue := mountAPIKeyOnEntity(t, entityID, "et-bl-export-key")

	// Entity 内联策略经层级收集绑定到挂载的 API-Key，导出含 rules.batch 段。
	seg := findBatchSegment(t, apiKeyValue)
	require.NotNil(t, seg, "rules.batch segment must exist for entity policy with batch_limits")
	assert.Equal(t, float64(30), seg["max_create_rpm"])
	assert.Equal(t, float64(5), seg["max_active_batches"])
	assert.Equal(t, float64(104857600), seg["max_file_bytes"])
	assert.Equal(t, float64(100000), seg["max_file_lines"])

	// redis_key：实现全限定形态为
	// default_bfe_rlp-{policyID}_RL_BATCH_rlp-{policyID}_rpm
	//（model/shared.BuildBatchRateLimitRedisKey）。仅 max_create_rpm>0 时生成。
	redisKey, ok := seg["redis_key"].(string)
	require.True(t, ok, "rules.batch.redis_key must exist when max_create_rpm>0")
	assert.Contains(t, redisKey, "RL_BATCH")
	assert.Contains(t, redisKey, "rpm")
}

// ---------- ET-BL-1-004 不带 batch_limits 的策略无 batch 段 ----------

func TestBatchLimits_InnerExportNoBatchSegment(t *testing.T) {
	typeName := setupEntityType(t)
	t.Cleanup(func() { _ = testutil.DeleteEntityType(typeName) })

	body := map[string]interface{}{
		"name": testutil.UniqueEntityName(),
		"type": typeName,
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
	}
	resp, err := testutil.GetClient().Post("/open-api/v1/entities", body)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	idVal, err := testutil.GetDataField(resp, "id")
	require.NoError(t, err)
	entityID := idVal.(string)
	t.Cleanup(func() { _ = testutil.DeleteEntity(entityID) })

	_, apiKeyValue := mountAPIKeyOnEntity(t, entityID, "et-bl-nobatch-key")

	seg := findBatchSegment(t, apiKeyValue)
	assert.Nil(t, seg, "entity policy without batch_limits must have no rules.batch segment")
}

// ---------- ET-BL-1-005 max_create_rpm=0 不生成 redis_key ----------

func TestBatchLimits_InnerExportZeroCreateRPMNoKey(t *testing.T) {
	typeName := setupEntityType(t)
	t.Cleanup(func() { _ = testutil.DeleteEntityType(typeName) })

	entityID := createBatchLimitsEntity(t, typeName, "et-bl-zero-rpm", map[string]interface{}{
		"max_create_rpm":     0,
		"max_active_batches": 5,
	})
	_, apiKeyValue := mountAPIKeyOnEntity(t, entityID, "et-bl-zero-rpm-key")

	seg := findBatchSegment(t, apiKeyValue)
	require.NotNil(t, seg, "rules.batch segment must exist (max_active_batches>0)")
	_, hasKey := seg["redis_key"]
	assert.False(t, hasKey, "redis_key must be omitted when max_create_rpm=0")
}

// ---------- ET-BL-1-006 PATCH 整对象替换：省略 batch_limits 即清空 ----------

func TestBatchLimits_PatchReplaceClearsBatchLimits(t *testing.T) {
	typeName := setupEntityType(t)
	t.Cleanup(func() { _ = testutil.DeleteEntityType(typeName) })

	entityID := createBatchLimitsEntity(t, typeName, "et-bl-patch", map[string]interface{}{
		"max_create_rpm":     30,
		"max_active_batches": 5,
	})

	// PATCH 提交整对象 rules，省略 batch_limits（整对象替换语义）。
	patchResp, err := testutil.GetClient().Patch("/open-api/v1/entities/"+entityID, map[string]interface{}{
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
	testutil.AssertSuccess(t, patchResp)

	rules := fetchEntityRules(t, entityID)
	_, hasBatch := rules["batch_limits"]
	assert.False(t, hasBatch, "omitting batch_limits in PATCH must clear the segment")
}
