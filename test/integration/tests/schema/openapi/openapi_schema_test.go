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

package openapi

import (
	"encoding/json"
	"fmt"
	"os"
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

func TestOpenAPI_Schema(t *testing.T) {
	t.Run("entity_types", testEntityTypeSchema)
	t.Run("entities", testEntitySchema)
	t.Run("api_keys", testAPIKeySchema)
	t.Run("providers", testProviderSchema)
	t.Run("clusters", testClusterSchema)
	t.Run("certificates", testCertificateSchema)
	t.Run("auth", testAuthSchema)
	t.Run("model_prices", testModelPriceSchema)
	t.Run("route_tables", testRouteTableSchema)
	t.Run("global_route_rules", testGlobalRouteRulesSchema)
	t.Run("ai_cache", testAICacheSchema)
	t.Run("traffic_mirror", testTrafficMirrorSchema)
	t.Run("intent_config", testIntentConfigSchema)
	t.Run("epp_pool", testEppPoolSchema)
	t.Run("epp_assignments", testEppAssignmentsSchema)
}

// ---------- entity-types ----------

func testEntityTypeSchema(t *testing.T) {
	typeName := testutil.UniqueEntityTypeName()

	resp, err := testutil.GetClient().Post("/open-api/v1/entity-types", map[string]interface{}{
		"type_name":   typeName,
		"description": "schema test",
		"level":       1,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	testutil.AssertSchema(t, resp, EntityTypeSchema)

	listResp, err := testutil.GetClient().Get("/open-api/v1/entity-types")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertPagedListSchema(t, listResp, EntityTypeSchema)

	oneResp, err := testutil.GetClient().Get("/open-api/v1/entity-types/" + typeName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, EntityTypeSchema)

	patchResp, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
		"description": "updated schema test",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, patchResp)
	testutil.AssertSchema(t, patchResp, EntityTypeSchema)

	t.Cleanup(func() {
		testutil.DeleteEntityType(typeName)
	})
}

// ---------- entities ----------

func testEntitySchema(t *testing.T) {
	typeName := testutil.UniqueEntityTypeName()
	_, err := testutil.CreateEntityType(typeName, 1)
	require.NoError(t, err)

	entityName := testutil.UniqueEntityName()
	createResp, err := testutil.GetClient().Post("/open-api/v1/entities", map[string]interface{}{
		"name":        entityName,
		"type":        typeName,
		"description": "schema test entity",
		"quota_plan": map[string]interface{}{
			"unlimited":    false,
			"quota":        1000000,
			"unit":         "total_token",
			"reset_period": "monthly",
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createResp)
	testutil.AssertSchema(t, createResp, EntitySchema)

	id, err := testutil.GetDataField(createResp, "id")
	require.NoError(t, err)
	entityID := id.(string)

	listResp, err := testutil.GetClient().Get("/open-api/v1/entities")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertPagedListSchema(t, listResp, EntityListItemSchema)

	oneResp, err := testutil.GetClient().Get("/open-api/v1/entities/" + entityID)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, EntitySchema)

	putResp, err := testutil.GetClient().Put("/open-api/v1/entities/"+entityID, map[string]interface{}{
		"name": entityName + "-updated",
		"type": typeName,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, putResp)
	testutil.AssertSchema(t, putResp, EntitySchema)

	patchResp, err := testutil.GetClient().Patch("/open-api/v1/entities/"+entityID, map[string]interface{}{
		"description":  "schema test updated",
		"allow_models": []string{"*"},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, patchResp)
	testutil.AssertSchema(t, patchResp, EntitySchema)

	qpResp, err := testutil.GetClient().Get("/open-api/v1/entities/" + entityID + "/quota-plan")
	require.NoError(t, err)
	testutil.AssertSuccess(t, qpResp)
	testutil.AssertSchema(t, qpResp, QuotaPlanWithBalanceSchema)

	resetResp, err := testutil.GetClient().Post("/open-api/v1/entities/"+entityID+"/quota-plan/reset", map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resetResp)
	testutil.AssertSchema(t, resetResp, QuotaResetResultSchema)

	t.Cleanup(func() {
		testutil.DeleteEntity(entityID)
		testutil.DeleteEntityType(typeName)
	})
}

// ---------- api-keys ----------

func testAPIKeySchema(t *testing.T) {
	typeName := testutil.UniqueEntityTypeName()
	_, err := testutil.CreateEntityType(typeName, 1)
	require.NoError(t, err)

	entityName := testutil.UniqueEntityName()
	entityID, err := testutil.CreateEntity(entityName, typeName, "")
	require.NoError(t, err)

	clusterName := testutil.UniqueClusterName()
	_, err = testutil.CreateCluster(clusterName)
	require.NoError(t, err)

	createResp, err := testutil.GetClient().Post("/open-api/v1/api-keys", map[string]interface{}{
		"description": "schema test",
		"entity_id":   entityID,
		"quota_plan": map[string]interface{}{
			"unlimited":    false,
			"quota":        1000000,
			"unit":         "total_token",
			"reset_period": "monthly",
		},
		"rate_limit_policy": map[string]interface{}{
			"enabled": true,
			"rules": map[string]interface{}{
				"tpm": []interface{}{
					map[string]interface{}{"name": "tpm-1m", "model": "*", "window_minutes": 1, "max_tokens": 10000, "step_minutes": 1},
				},
				"rpm": []interface{}{
					map[string]interface{}{"name": "rpm-1m", "model": "*", "window_minutes": 1, "max_requests": 100},
				},
				"max_concurrency": 50,
			},
		},
		"route_rules": map[string]interface{}{
			"enabled": true,
			"rules": []interface{}{
				map[string]interface{}{
					"name": "default",
					"cond": "default_t()",
					"targets": []interface{}{
						map[string]interface{}{"cluster_name": clusterName, "model": "", "weight": 100},
					},
					"fallbacks": []interface{}{},
				},
			},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createResp)
	testutil.AssertSchema(t, createResp, APIKeySchema)

	id, err := testutil.GetDataField(createResp, "id")
	require.NoError(t, err)
	apiKeyID := id.(string)

	listResp, err := testutil.GetClient().Get("/open-api/v1/api-keys")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertPagedListSchema(t, listResp, APIKeyListItemSchema)

	oneResp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, APIKeyListItemSchema)

	putResp, err := testutil.GetClient().Put("/open-api/v1/api-keys/"+apiKeyID, map[string]interface{}{
		"description": "schema test updated",
		"enabled":     true,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, putResp)
	testutil.AssertSchema(t, putResp, APIKeySchema)

	patchResp, err := testutil.GetClient().Patch("/open-api/v1/api-keys/"+apiKeyID, map[string]interface{}{
		"description": "schema test patched",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, patchResp)
	testutil.AssertSchema(t, patchResp, APIKeySchema)

	qpResp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID + "/quota-plan")
	require.NoError(t, err)
	testutil.AssertSuccess(t, qpResp)
	testutil.AssertSchema(t, qpResp, QuotaPlanWithBalanceSchema)

	resetResp, err := testutil.GetClient().Post("/open-api/v1/api-keys/"+apiKeyID+"/quota-plan/reset", map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resetResp)
	testutil.AssertSchema(t, resetResp, QuotaResetResultSchema)

	t.Cleanup(func() {
		testutil.DeleteAPIKey(apiKeyID)
		testutil.DeleteEntity(entityID)
		testutil.DeleteEntityType(typeName)
		testutil.DeleteCluster(clusterName)
	})
}

// ---------- providers ----------

func testProviderSchema(t *testing.T) {
	providerName := testutil.UniqueProviderName()

	createResp, err := testutil.GetClient().Post("/open-api/v1/providers", map[string]interface{}{
		"name":        providerName,
		"description": "schema test",
		"instance_pool": []interface{}{
			map[string]interface{}{"addr": "10.0.0.1", "weight": 100, "port": 8080},
		},
		"model_protocols": []string{"openai"},
		"protocol_paths":  map[string]interface{}{"openai": "/compatible-mode/v1"},
		"models":          []string{"deepseek-chat"},
		"time_zone":       "Asia/Shanghai",
		"tiers": []interface{}{
			map[string]interface{}{
				"name": "peak",
				"time_ranges": []interface{}{
					map[string]interface{}{
						"weekdays": []int{1, 2, 3, 4, 5},
						"start":    "09:00",
						"end":      "12:00",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createResp)
	testutil.AssertSchema(t, createResp, ProviderSchema)
	testutil.AssertDataFieldEquals(t, createResp, "time_zone", "Asia/Shanghai")

	listResp, err := testutil.GetClient().Get("/open-api/v1/providers")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertPagedListSchema(t, listResp, ProviderSchema)

	oneResp, err := testutil.GetClient().Get("/open-api/v1/providers/" + providerName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, ProviderSchema)

	patchResp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
		"description":     "schema test updated",
		"instance_pool":   []interface{}{map[string]interface{}{"addr": "10.0.0.1", "weight": 100, "port": 8080}},
		"model_protocols": []string{"openai"},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, patchResp)
	testutil.AssertSchema(t, patchResp, ProviderSchema)

	// Also validate anthropic protocol provider creation.
	anthropicProviderName := testutil.UniqueProviderName()
	anthropicResp, err := testutil.GetClient().Post("/open-api/v1/providers", map[string]interface{}{
		"name":            anthropicProviderName,
		"description":     "schema test anthropic",
		"model_protocols": []string{"anthropic"},
		"models":          []string{"claude-3-5-sonnet-20241022"},
		"instance_pool": []interface{}{
			map[string]interface{}{"addr": "10.0.0.1", "weight": 100, "port": 8080},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, anthropicResp)
	testutil.AssertSchema(t, anthropicResp, ProviderSchema)
	assertProviderModelProtocols(t, anthropicResp.Data, []string{"anthropic"})

	t.Cleanup(func() {
		testutil.DeleteProvider(providerName)
		testutil.DeleteProvider(anthropicProviderName)
	})
}

// assertProviderModelProtocols 校验 Provider 响应中的 model_protocols 字段值。
func assertProviderModelProtocols(t *testing.T, data []byte, want []string) {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal provider data: %v", err)
	}
	protocols, ok := payload["model_protocols"].([]interface{})
	if !ok || len(protocols) != len(want) {
		t.Fatalf("expected model_protocols=%v, got %v", want, payload["model_protocols"])
	}
	for i, v := range want {
		if protocols[i] != v {
			t.Fatalf("expected model_protocols[%d]=%q, got %v", i, v, protocols[i])
		}
	}
}

// ---------- clusters ----------

func testClusterSchema(t *testing.T) {
	providerName := testutil.UniqueProviderName()
	_, err := testutil.CreateProvider(providerName)
	require.NoError(t, err)

	clusterName := testutil.UniqueClusterName()

	createResp, err := testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
		"name":        clusterName,
		"description": "schema test",
		"llm_config": map[string]interface{}{
			"models":   []string{"deepseek-chat"},
			"provider": providerName,
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createResp)
	testutil.AssertSchema(t, createResp, ClusterSchema)

	listResp, err := testutil.GetClient().Get("/open-api/v1/clusters")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertListSchema(t, listResp, ClusterSchema)

	oneResp, err := testutil.GetClient().Get("/open-api/v1/clusters/" + clusterName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, ClusterSchema)

	patchResp, err := testutil.GetClient().Patch("/open-api/v1/clusters/"+clusterName, map[string]interface{}{
		"description": "schema test updated",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, patchResp)
	testutil.AssertSchema(t, patchResp, ClusterSchema)

	// EPP 模式集群：balance_mode/epp_config 字段与嵌套结构校验。
	eppProviderName := testutil.UniqueProviderName()
	_, err = testutil.CreateProvider(eppProviderName)
	require.NoError(t, err)

	eppClusterName := testutil.UniqueClusterName()
	eppClusterBody := map[string]interface{}{
		"name":         eppClusterName,
		"description":  "schema test epp",
		"balance_mode": "EPP",
		"epp_config": map[string]interface{}{
			"scheduling_profile":       "latency-first",
			"cache_affinity":           "high",
			"prefix_cache_affinity":    true,
			"session_affinity_enabled": true,
			"session_affinity_header":  "x-session-id",
			"kv_cache_utilization_max": 0.85,
			"flow_control": map[string]interface{}{
				"max_requests":          200,
				"queue_ttl":             45,
				"no_endpoint_queue_ttl": 120,
				"enable_eviction":       true,
			},
		},
		"llm_config": map[string]interface{}{
			"models":   []string{"deepseek-chat"},
			"provider": eppProviderName,
		},
	}
	eppCreateResp, err := testutil.GetClient().Post("/open-api/v1/clusters", eppClusterBody)
	require.NoError(t, err)
	testutil.AssertSuccess(t, eppCreateResp)
	testutil.AssertSchema(t, eppCreateResp, ClusterSchema)

	eppOneResp, err := testutil.GetClient().Get("/open-api/v1/clusters/" + eppClusterName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, eppOneResp)
	testutil.AssertSchema(t, eppOneResp, ClusterSchema)

	// epp_config 原样回读（存储保留用户原始 JSON，未携带字段不落盘）。
	assertEppConfigEcho(t, eppOneResp.Data, map[string]interface{}{
		"scheduling_profile":       "latency-first",
		"cache_affinity":           "high",
		"prefix_cache_affinity":    true,
		"session_affinity_enabled": true,
		"session_affinity_header":  "x-session-id",
		"kv_cache_utilization_max": 0.85,
		"flow_control": map[string]interface{}{
			"max_requests":          float64(200),
			"queue_ttl":             float64(45),
			"no_endpoint_queue_ttl": float64(120),
			"enable_eviction":       true,
		},
	})

	// WRR 集群携带 epp_config（休眠保留）：创建成功且 GET 原样返回。
	dormantClusterName := testutil.UniqueClusterName()
	dormantResp, err := testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
		"name":         dormantClusterName,
		"balance_mode": "WRR",
		"epp_config":   map[string]interface{}{"scheduling_profile": "throughput-first"},
		"llm_config": map[string]interface{}{
			"models":   []string{"deepseek-chat"},
			"provider": eppProviderName,
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, dormantResp)
	testutil.AssertSchema(t, dormantResp, ClusterSchema)

	dormantOneResp, err := testutil.GetClient().Get("/open-api/v1/clusters/" + dormantClusterName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, dormantOneResp)
	testutil.AssertSchema(t, dormantOneResp, ClusterSchema)
	assertEppConfigEcho(t, dormantOneResp.Data, map[string]interface{}{
		"scheduling_profile": "throughput-first",
	})

	t.Cleanup(func() {
		testutil.DeleteCluster(clusterName)
		testutil.DeleteCluster(eppClusterName)
		testutil.DeleteCluster(dormantClusterName)
		testutil.DeleteProvider(providerName)
		testutil.DeleteProvider(eppProviderName)
	})
}

// assertEppConfigEcho 校验 GET cluster 回读中 epp_config 与写入值一致（原样回读）。
func assertEppConfigEcho(t *testing.T, data []byte, want map[string]interface{}) {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal cluster data: %v", err)
	}
	eppConfig, ok := payload["epp_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("epp_config is not an object: %v", payload["epp_config"])
	}
	for key, wantVal := range want {
		require.Equal(t, wantVal, eppConfig[key], "epp_config.%s echo mismatch", key)
	}
}

// ---------- certificates ----------

func testCertificateSchema(t *testing.T) {
	certName := testutil.UniqueCertName()
	certPEM, keyPEM, err := testutil.GenerateTestCert(certName)
	require.NoError(t, err)

	createResp, err := testutil.GetClient().Post("/open-api/v1/certificates", map[string]interface{}{
		"cert_name":         certName,
		"description":       "schema test",
		"is_default":        true,
		"cert_file_content": certPEM,
		"key_file_content":  keyPEM,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createResp)
	testutil.AssertSchema(t, createResp, CertificateSchema)

	listResp, err := testutil.GetClient().Get("/open-api/v1/certificates")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertListSchema(t, listResp, CertificateSchema)

	oneResp, err := testutil.GetClient().Get("/open-api/v1/certificates/" + certName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, CertificateSchema)

	defaultResp, err := testutil.GetClient().Patch("/open-api/v1/certificates/"+certName+"/default", map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertSuccess(t, defaultResp)
	testutil.AssertSchema(t, defaultResp, CertificateSchema)

	t.Cleanup(func() {
		// 默认证书不能直接删除，需先创建另一个默认证书再删
		// 这里简单处理：保留默认证书，测试中忽略删除错误
		_ = testutil.DeleteCertificate(certName)
	})
}

// ---------- auth ----------

func testAuthSchema(t *testing.T) {
	userName := testutil.UniqueUserName()
	password := "Password123!"

	createUserResp, err := testutil.GetClient().Post("/open-api/v1/auth/users", map[string]interface{}{
		"user_name": userName,
		"password":  password,
		"is_admin":  true,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createUserResp)

	listUsersResp, err := testutil.GetClient().Get("/open-api/v1/auth/users")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listUsersResp)
	testutil.AssertListSchema(t, listUsersResp, UserSchema)

	oneUserResp, err := testutil.GetClient().Get("/open-api/v1/auth/users/" + userName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneUserResp)
	testutil.AssertSchema(t, oneUserResp, UserSchema)

	sessionResp, err := testutil.GetClient().Post("/open-api/v1/auth/session-keys", map[string]interface{}{
		"user_name": userName,
		"password":  password,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, sessionResp)
	testutil.AssertSchema(t, sessionResp, SessionKeySchema)

	sessionKeyVal, err := testutil.GetDataField(sessionResp, "session_key")
	require.NoError(t, err)

	tokenName := testutil.UniqueTokenName()
	createTokenResp, err := testutil.GetClient().Post("/open-api/v1/auth/tokens", map[string]interface{}{
		"name":  tokenName,
		"scope": "System",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createTokenResp)
	testutil.AssertSchema(t, createTokenResp, CreateTokenResponseSchema)

	listTokensResp, err := testutil.GetClient().Get("/open-api/v1/auth/tokens")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listTokensResp)
	testutil.AssertListSchema(t, listTokensResp, TokenSchema)

	oneTokenResp, err := testutil.GetClient().Get("/open-api/v1/auth/tokens/" + tokenName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneTokenResp)
	testutil.AssertSchema(t, oneTokenResp, TokenSchema)

	metaResp, err := testutil.GetClient().Get("/open-api/v1/meta")
	require.NoError(t, err)
	testutil.AssertSuccess(t, metaResp)
	testutil.AssertSchema(t, metaResp, MetaSchema)

	t.Cleanup(func() {
		testutil.DeleteToken(tokenName)
		testutil.GetClient().Delete("/open-api/v1/auth/session-keys/" + sessionKeyVal.(string))
		testutil.DeleteUser(userName)
	})
}

// ---------- model-prices ----------

func testModelPriceSchema(t *testing.T) {
	schemaProvider := "schema-test-provider"
	schemaProvider2 := "schema-test-provider-2"
	_, err := testutil.CreateProvider(schemaProvider)
	require.NoError(t, err)
	_, err = testutil.CreateProvider(schemaProvider2)
	require.NoError(t, err)

	yamlContent := []byte(`version: v1.0
default_currency: RMB
models:
  - provider: schema-test-provider
    model: schema-test-model
    base_model: schema-test-model
    mode: chat
    capabilities: [chat]
    supported_parameters: [temperature]
    limits:
      context_window: 128000
    prices:
      input_cost_per_token: 0.000002
      output_cost_per_token: 0.000008
    tier_prices:
      peak:
        input_cost_per_token: 0.000004
        output_cost_per_token: 0.000016
    metadata:
      source: test
`)
	importResp, err := testutil.GetClient().PostMultipartFile("/open-api/v1/model-prices/import", "file", "model-list.yaml", yamlContent, map[string]string{
		"mode": "replace",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, importResp)
	testutil.AssertSchema(t, importResp, ModelPriceImportResultSchema)

	createResp, err := testutil.GetClient().Post("/open-api/v1/model-prices", map[string]interface{}{
		"provider":             schemaProvider2,
		"model":                "schema-test-model-2",
		"base_model":           "schema-test-model-2",
		"mode":                 "chat",
		"capabilities":         []string{"chat"},
		"supported_parameters": []string{"temperature"},
		"limits": map[string]interface{}{
			"context_window": 128000,
		},
		"prices": map[string]interface{}{
			"input_cost_per_token":  0.000002,
			"output_cost_per_token": 0.000008,
		},
		"tier_prices": map[string]interface{}{
			"peak": map[string]interface{}{
				"input_cost_per_token":  0.000004,
				"output_cost_per_token": 0.000016,
			},
		},
		"metadata": map[string]interface{}{
			"source": "test",
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createResp)
	testutil.AssertSchema(t, createResp, ModelPriceSchema)

	id, err := testutil.GetDataField(createResp, "id")
	require.NoError(t, err)
	modelPriceID := int64(id.(float64))

	listResp, err := testutil.GetClient().Get("/open-api/v1/model-prices")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertSchema(t, listResp, ModelPriceListResponseSchema)

	oneResp, err := testutil.GetClient().Get(fmt.Sprintf("/open-api/v1/model-prices/%d", modelPriceID))
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, ModelPriceSchema)

	updateResp, err := testutil.GetClient().Put(fmt.Sprintf("/open-api/v1/model-prices/%d", modelPriceID), map[string]interface{}{
		"provider":             schemaProvider2,
		"model":                "schema-test-model-2",
		"base_model":           "schema-test-model-2",
		"mode":                 "chat",
		"capabilities":         []string{"chat", "vision"},
		"supported_parameters": []string{"temperature"},
		"limits": map[string]interface{}{
			"context_window": 128000,
		},
		"prices": map[string]interface{}{
			"input_cost_per_token":  0.000003,
			"output_cost_per_token": 0.000009,
		},
		"tier_prices": map[string]interface{}{
			"peak": map[string]interface{}{
				"input_cost_per_token":  0.000006,
				"output_cost_per_token": 0.000018,
			},
		},
		"metadata": map[string]interface{}{
			"source": "test",
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, updateResp)
	testutil.AssertSchema(t, updateResp, ModelPriceSchema)

	getProvidersResp, err := testutil.GetClient().Get("/open-api/v1/model-prices/actions/get-providers")
	require.NoError(t, err)
	testutil.AssertSuccess(t, getProvidersResp)
	testutil.AssertSchema(t, getProvidersResp, ModelPriceGetProvidersResponseSchema)

	t.Cleanup(func() {
		testutil.DeleteModelPrice(modelPriceID)
		testutil.DeleteModelPriceByQuery(schemaProvider, "schema-test-model", "chat")
		testutil.DeleteProvider(schemaProvider)
		testutil.DeleteProvider(schemaProvider2)
	})
}

// ---------- route-tables ----------

func testRouteTableSchema(t *testing.T) {
	resp, err := testutil.GetClient().Get("/open-api/v1/route-tables")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	testutil.AssertPagedListSchema(t, resp, RouteTableSchema)
}

// ---------- global-route-rules ----------

func testGlobalRouteRulesSchema(t *testing.T) {
	resp, err := testutil.GetClient().Get("/open-api/v1/global-route-rules")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	testutil.AssertSchema(t, resp, GlobalRouteRulesSchema)

	putResp, err := testutil.GetClient().Put("/open-api/v1/global-route-rules", map[string]interface{}{
		"enabled": true,
		"rules":   []interface{}{},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, putResp)
	testutil.AssertSchema(t, putResp, GlobalRouteRulesSchema)
}

// ---------- ai-cache-rules ----------

// testAICacheSchema 覆盖 AI 缓存规则集合（GET/PUT 同构）。
// 集合级资源无 /{id} 端点："GET 单查" 即全量查询的重复拉取（唯一读形状）。
// 另做定向断言：rules 元素键集合精确为合同 8 字段（含 created_at/updated_at），
// 不得含内部 id、无 enabled（ai-cache-rules.md §1）。
func testAICacheSchema(t *testing.T) {
	validCond := `req_path_in("/v1/chat/completions", false)`
	name1 := testutil.UniqueName("schema-ac-1")
	name2 := testutil.UniqueName("schema-ac-2")

	// 建集合（PUT）。
	putResp, err := testutil.GetClient().Put("/open-api/v1/ai-cache-rules", map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{"name": name1, "cond": validCond, "cache_ttl": 3600},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, putResp)
	testutil.AssertSchema(t, putResp, AICacheRulesSchema)

	// GET 列表（全量查询）。
	listResp, err := testutil.GetClient().Get("/open-api/v1/ai-cache-rules")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertSchema(t, listResp, AICacheRulesSchema)

	// GET 重复拉取（唯一读形状，等价单查）。
	oneResp, err := testutil.GetClient().Get("/open-api/v1/ai-cache-rules")
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, AICacheRulesSchema)

	// PUT 修改（全量替换为 2 条）。
	put2Resp, err := testutil.GetClient().Put("/open-api/v1/ai-cache-rules", map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{"name": name1, "cond": validCond, "cache_ttl": 7200},
			map[string]interface{}{"name": name2, "cond": validCond, "cache_key_strategy": "disabled"},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, put2Resp)
	testutil.AssertSchema(t, put2Resp, AICacheRulesSchema)

	// 修改后再 GET。
	get2Resp, err := testutil.GetClient().Get("/open-api/v1/ai-cache-rules")
	require.NoError(t, err)
	testutil.AssertSuccess(t, get2Resp)
	testutil.AssertSchema(t, get2Resp, AICacheRulesSchema)

	// 定向合同锁：rules 元素键集合精确 8 字段，无 id/enabled。
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(get2Resp.Data, &data))
	rules, ok := data["rules"].([]interface{})
	require.True(t, ok, "rules should be array")
	require.Len(t, rules, 2)
	wantStrategies := []string{"lastQuestion", "disabled"}
	for i, item := range rules {
		rule, ok := item.(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		keys := make([]string, 0, len(rule))
		for k := range rule {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t, []string{
			"name", "cond", "cache_key_strategy", "cache_ttl",
			"max_body_bytes", "max_value_bytes", "created_at", "updated_at",
		}, keys, "rules[%d] keys must exactly match contract (no id/enabled)", i)
		assert.Equal(t, wantStrategies[i], rule["cache_key_strategy"])
	}

	t.Cleanup(func() {
		// 恢复空集合，避免影响其他模块。
		_, _ = testutil.GetClient().Put("/open-api/v1/ai-cache-rules", map[string]interface{}{
			"rules": []interface{}{},
		})
	})
}

// ---------- traffic-mirror-rules ----------

// testTrafficMirrorSchema 覆盖流量镜像规则集合（GET/PUT 同构）。
// 集合级资源无 /{id} 端点："GET 单查" 即全量查询的重复拉取（唯一读形状）。
// 另做定向断言：最小提交规则恰为 6 固定键（可选键缺席），全字段提交恰为 10 键，
// 均不得含内部 id、无 enabled（traffic-mirror-rules.md §1，家族1 省略语义 / #201 幻影键）。
func testTrafficMirrorSchema(t *testing.T) {
	clusterName, err := testutil.CreateCluster(testutil.UniqueClusterName())
	require.NoError(t, err)

	fixedKeys := []string{"name", "cond", "mirror_cluster", "percentage", "created_at", "updated_at"}
	fullKeys := []string{
		"name", "cond", "mirror_cluster", "percentage",
		"remove_headers", "set_headers", "body_rewrites", "path_rewrite",
		"created_at", "updated_at",
	}

	// 建集合（PUT）：1 条最小 + 1 条全字段。
	putResp, err := testutil.GetClient().Put("/open-api/v1/traffic-mirror-rules", map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{"name": testutil.UniqueName("schema-tm-1"), "cond": `default_t()`, "mirror_cluster": clusterName},
			map[string]interface{}{
				"name": testutil.UniqueName("schema-tm-2"), "cond": `req_path_prefix_in("/v1/completions", true)`, "mirror_cluster": clusterName,
				"percentage":     10,
				"remove_headers": []interface{}{"Authorization"},
				"set_headers":    map[string]interface{}{"X-A": "1"},
				"body_rewrites":  []interface{}{map[string]interface{}{"path": "model", "value": "m2"}},
				"path_rewrite":   "/v1/internal/x",
			},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, putResp)
	testutil.AssertSchema(t, putResp, TrafficMirrorRulesSchema)

	// GET 列表（全量查询）。
	listResp, err := testutil.GetClient().Get("/open-api/v1/traffic-mirror-rules")
	require.NoError(t, err)
	testutil.AssertSuccess(t, listResp)
	testutil.AssertSchema(t, listResp, TrafficMirrorRulesSchema)

	// 定向合同锁：rules[0] 精确 6 固定键（可选键缺席），rules[1] 精确 10 键。
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(listResp.Data, &data))
	rules, ok := data["rules"].([]interface{})
	require.True(t, ok, "rules should be array")
	require.Len(t, rules, 2)
	for i, wantKeys := range [][]string{fixedKeys, fullKeys} {
		rule, ok := rules[i].(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		keys := make([]string, 0, len(rule))
		for k := range rule {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t, wantKeys, keys,
			"rules[%d] keys must exactly match contract (no id/enabled)", i)
	}

	t.Cleanup(func() {
		// 恢复空集合，避免影响其他模块。
		_, _ = testutil.GetClient().Put("/open-api/v1/traffic-mirror-rules", map[string]interface{}{
			"rules": []interface{}{},
		})
	})
}

// ---------- epp-pool ----------

func testEppPoolSchema(t *testing.T) {
	// EPP 池为单例：本用例 PATCH 自己的组布局后回读。
	patchResp, err := testutil.GetClient().Patch("/open-api/v1/epp-pool", map[string]interface{}{
		"groups": []interface{}{
			map[string]interface{}{
				"name": "g1",
				"instances": []interface{}{
					map[string]interface{}{"id": "epp-a", "host": "10.0.0.1", "port": 9002},
					map[string]interface{}{"id": "epp-b", "host": "10.0.0.2", "port": 9002},
				},
			},
			map[string]interface{}{
				"name": "g2",
				"instances": []interface{}{
					map[string]interface{}{"id": "epp-c", "host": "10.0.0.3", "port": 9002},
				},
			},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, patchResp)
	testutil.AssertSchema(t, patchResp, EppPoolSchema)

	getResp, err := testutil.GetClient().Get("/open-api/v1/epp-pool")
	require.NoError(t, err)
	testutil.AssertSuccess(t, getResp)
	testutil.AssertSchema(t, getResp, EppPoolSchema)

	// 定向断言：name 为配置的单例池名，组与实例字段完整回读。
	nameVal, err := testutil.GetDataField(getResp, "name")
	require.NoError(t, err)
	require.Equal(t, "EPP.pool", nameVal)
	groupsVal, err := testutil.GetDataField(getResp, "groups")
	require.NoError(t, err)
	groups := groupsVal.([]interface{})
	require.Len(t, groups, 2)
	g1 := groups[0].(map[string]interface{})
	require.Equal(t, "g1", g1["name"])
	insts := g1["instances"].([]interface{})
	require.Len(t, insts, 2)
	inst0 := insts[0].(map[string]interface{})
	require.Equal(t, "epp-a", inst0["id"])
	require.Equal(t, "10.0.0.1", inst0["host"])
	require.Equal(t, float64(9002), inst0["port"])
}

// ---------- epp-assignments ----------

func testEppAssignmentsSchema(t *testing.T) {
	// PATCH 自己的组布局（EPP 池单例，不依赖其他用例的池状态）。
	patchResp, err := testutil.GetClient().Patch("/open-api/v1/epp-pool", map[string]interface{}{
		"groups": []interface{}{
			map[string]interface{}{
				"name": "g1",
				"instances": []interface{}{
					map[string]interface{}{"id": "epp-a", "host": "10.0.0.1", "port": 9002},
					map[string]interface{}{"id": "epp-b", "host": "10.0.0.2", "port": 9002},
				},
			},
			map[string]interface{}{
				"name": "g2",
				"instances": []interface{}{
					map[string]interface{}{"id": "epp-c", "host": "10.0.0.3", "port": 9002},
				},
			},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, patchResp)

	providerName := testutil.UniqueProviderName()
	_, err = testutil.CreateProvider(providerName)
	require.NoError(t, err)

	clusterName := testutil.UniqueClusterName()
	createResp, err := testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
		"name":         clusterName,
		"balance_mode": "EPP",
		"epp_config":   map[string]interface{}{"scheduling_profile": "balanced"},
		"llm_config": map[string]interface{}{
			"models":   []string{"deepseek-chat"},
			"provider": providerName,
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, createResp)

	t.Cleanup(func() {
		testutil.DeleteCluster(clusterName)
		testutil.DeleteProvider(providerName)
	})

	// 全量视图：结构 + 定向断言（该 cluster 已自动分配、g2 空闲）。
	viewResp, err := testutil.GetClient().Get("/open-api/v1/epp-assignments")
	require.NoError(t, err)
	testutil.AssertSuccess(t, viewResp)
	testutil.AssertSchema(t, viewResp, EppAssignmentsViewSchema)

	assertAssignmentEntry(t, viewResp.Data, clusterName, false)

	idleVal, err := testutil.GetDataField(viewResp, "idle_groups")
	require.NoError(t, err)
	require.Contains(t, idleVal, "g2")
	unassignedVal, err := testutil.GetDataField(viewResp, "unassigned_clusters")
	require.NoError(t, err)
	require.NotContains(t, unassignedVal, clusterName)

	// 单条过滤查询：同结构 schema。
	oneResp, err := testutil.GetClient().Get("/open-api/v1/epp-assignments", map[string]string{
		"cluster": clusterName,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, oneResp)
	testutil.AssertSchema(t, oneResp, EppAssignmentsViewSchema)
	assertAssignmentEntry(t, oneResp.Data, clusterName, false)

	// 手工覆写后单条响应结构（standby 换为另一实例）。
	entry := findAssignmentEntry(t, oneResp.Data, clusterName)
	standby := entry["standby"].(map[string]interface{})
	putResp, err := testutil.GetClient().Put("/open-api/v1/epp-assignments/"+clusterName, map[string]interface{}{
		"group_name":          "g1",
		"primary_instance_id": standby["id"],
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, putResp)
	testutil.AssertSchema(t, putResp, EppClusterAssignmentOverrideSchema)

	putPrimary, err := testutil.GetDataField(putResp, "primary")
	require.NoError(t, err)
	require.Equal(t, standby["id"], putPrimary.(map[string]interface{})["id"])
}

// findAssignmentEntry 从全量视图中取出指定 cluster 的分配条目。
func findAssignmentEntry(t *testing.T, data []byte, clusterName string) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal assignments data: %v", err)
	}
	clusters := payload["clusters"].([]interface{})
	for _, item := range clusters {
		entry := item.(map[string]interface{})
		if entry["cluster"] == clusterName {
			return entry
		}
	}
	t.Fatalf("cluster %s not found in assignments view", clusterName)
	return nil
}

// assertAssignmentEntry 定向断言指定 cluster 已分配（group/primary/standby 非空）。
func assertAssignmentEntry(t *testing.T, data []byte, clusterName string, unassigned bool) {
	t.Helper()
	entry := findAssignmentEntry(t, data, clusterName)
	require.Equal(t, unassigned, entry["degraded"].(bool))
	if unassigned {
		require.Nil(t, entry["primary"])
		return
	}
	require.NotNil(t, entry["group"])
	primary, ok := entry["primary"].(map[string]interface{})
	require.True(t, ok, "primary should be an object")
	require.NotEmpty(t, primary["id"])
	require.NotEmpty(t, primary["host"])
	require.NotNil(t, entry["standby"])
}

// ---------- intent-config ----------

// testIntentConfigSchema 覆盖意图配置单例（GET/PUT 同构，单例级全量读写）。
// 单例资源无 /{id} 端点："GET 单查" 即重复拉取（唯一读形状）。
// 另做定向断言：顶层键精确为合同 4 字段，不得含内部 version、无 id/enabled
// （单行覆盖式存储，intent-config.md §1）；questions 元素按 type 互斥
// （choice 恰 4 键 / score 恰 5 键），不泄漏内部字段；空 questions 软开关
// （questions: []）PUT/GET 往返合法。
func testIntentConfigSchema(t *testing.T) {
	intentPath := "/open-api/v1/intent-config"

	// 未发布态：资源不存在错误码（Model.NullData → 404）。
	notFoundResp, err := testutil.GetClient().Get(intentPath)
	require.NoError(t, err)
	testutil.AssertErrCode(t, notFoundResp, 404)

	// PUT 完整文档（1 choice + 1 score），含逐问题阈值覆盖。
	putResp, err := testutil.GetClient().Put(intentPath, map[string]interface{}{
		"min_confidence": 0.6,
		"questions": []interface{}{
			map[string]interface{}{
				"name":         testutil.UniqueName("schema-ic-choice"),
				"type":         "choice",
				"instructions": "这条请求属于哪类研发任务？",
				"criteria": map[string]interface{}{
					"coding":       "编写或修改代码、调试、重构、代码审查",
					"test_writing": "编写测试用例、单元测试、集成测试、补充断言",
				},
			},
			map[string]interface{}{
				"name":           testutil.UniqueName("schema-ic-score"),
				"type":           "score",
				"instructions":   "这个任务的复杂度如何？",
				"min_confidence": 0.7,
				"levels": []interface{}{
					map[string]interface{}{"name": "simple", "description": "单步即可完成"},
					map[string]interface{}{"name": "complex", "description": "需要深入推理"},
				},
			},
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, putResp)
	testutil.AssertSchema(t, putResp, IntentConfigSchema)
	assertIntentConfigContract(t, putResp.Data)

	// GET（唯一读形状，与 PUT 响应同构）。
	getResp, err := testutil.GetClient().Get(intentPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, getResp)
	testutil.AssertSchema(t, getResp, IntentConfigSchema)
	assertIntentConfigContract(t, getResp.Data)

	// 非法 PUT：参数错误码（422），配置保持原状。
	invalidBodies := []map[string]interface{}{
		// min_confidence 越界
		{"min_confidence": 1.5, "questions": []interface{}{}},
		// 选项名含 '|'
		{"questions": []interface{}{map[string]interface{}{
			"name": "schema-ic-pipe", "type": "choice", "instructions": "i",
			"criteria": map[string]interface{}{"a|b": "d"},
		}}},
		// criteria/levels 互斥违反（choice 带 levels）
		{"questions": []interface{}{map[string]interface{}{
			"name": "schema-ic-mutex", "type": "choice", "instructions": "i",
			"criteria": map[string]interface{}{"a": "b"},
			"levels":   []interface{}{map[string]interface{}{"name": "l", "description": "d"}},
		}}},
		// questions 超 10 个
		{"questions": make([]interface{}, 11)},
	}
	for _, body := range invalidBodies {
		badResp, err := testutil.GetClient().Put(intentPath, body)
		require.NoError(t, err)
		testutil.AssertErrCode(t, badResp, 422)
	}

	// 拒绝后配置不变（仍是完整文档）。
	unchangedResp, err := testutil.GetClient().Get(intentPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, unchangedResp)
	assertIntentConfigContract(t, unchangedResp.Data)

	// 空 questions 软开关（questions: []）PUT/GET 往返合法。
	emptyPutResp, err := testutil.GetClient().Put(intentPath, map[string]interface{}{"questions": []interface{}{}})
	require.NoError(t, err)
	testutil.AssertSuccess(t, emptyPutResp)
	testutil.AssertSchema(t, emptyPutResp, IntentConfigSchema)

	var emptyData map[string]interface{}
	require.NoError(t, json.Unmarshal(emptyPutResp.Data, &emptyData))
	assert.ElementsMatch(t, []string{"min_confidence", "questions", "created_at", "updated_at"}, keysOfMap(emptyData))
	assert.Empty(t, emptyData["questions"], "questions:[] must round trip as empty array")

	emptyGetResp, err := testutil.GetClient().Get(intentPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, emptyGetResp)
	testutil.AssertSchema(t, emptyGetResp, IntentConfigSchema)
	emptyQuestions, err := testutil.GetDataField(emptyGetResp, "questions")
	require.NoError(t, err)
	assert.Empty(t, emptyQuestions)
}

// assertIntentConfigContract 定向合同锁：顶层键精确 4 字段（无 version/id/enabled），
// questions[0]（choice）恰 4 键、questions[1]（score）恰 5 键（互斥 + 逐问题阈值），
// levels 元素恰 {name,description}，均不泄漏内部字段（intent-config.md §1/§2.2）。
func assertIntentConfigContract(t *testing.T, data []byte) {
	t.Helper()
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &payload))

	assert.ElementsMatch(t,
		[]string{"min_confidence", "questions", "created_at", "updated_at"}, keysOfMap(payload),
		"top-level keys must exactly match contract (no version/id/enabled)")

	questions, ok := payload["questions"].([]interface{})
	require.True(t, ok, "questions should be array")
	require.Len(t, questions, 2)

	choice, ok := questions[0].(map[string]interface{})
	require.True(t, ok, "questions[0] should be object")
	assert.ElementsMatch(t,
		[]string{"name", "type", "instructions", "criteria"}, keysOfMap(choice),
		"choice question keys must exactly match contract (no levels, no internal fields)")
	assert.Equal(t, "choice", choice["type"])

	score, ok := questions[1].(map[string]interface{})
	require.True(t, ok, "questions[1] should be object")
	assert.ElementsMatch(t,
		[]string{"name", "type", "instructions", "min_confidence", "levels"}, keysOfMap(score),
		"score question keys must exactly match contract (no criteria, per-question threshold exported)")
	assert.Equal(t, "score", score["type"])
	assert.InDelta(t, 0.7, score["min_confidence"], 1e-9)

	levels, ok := score["levels"].([]interface{})
	require.True(t, ok, "levels should be array")
	require.NotEmpty(t, levels)
	for i, item := range levels {
		level, ok := item.(map[string]interface{})
		require.True(t, ok, "levels[%d] should be object", i)
		assert.ElementsMatch(t, []string{"name", "description"}, keysOfMap(level),
			"levels[%d] must not leak internal fields", i)
	}

	// 原始 body 不得出现内部 version 键（下发链路内部字段，核心断言）。
	assert.NotContains(t, string(data), `"version"`)
	assert.NotContains(t, string(data), `"id"`)
	assert.NotContains(t, string(data), `"enabled"`)
}

func keysOfMap(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
