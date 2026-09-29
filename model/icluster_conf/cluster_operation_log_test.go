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

package icluster_conf

import (
	"context"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibasic"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertMapKeys(t *testing.T, m map[string]interface{}, keys ...string) {
	t.Helper()
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	assert.ElementsMatch(t, keys, got)
}

func assertNoUppercaseKeys(t *testing.T, m map[string]interface{}) {
	t.Helper()
	for k := range m {
		require.NotEmpty(t, k)
		first := k[0]
		assert.False(t, first >= 'A' && first <= 'Z',
			"key %q must be API lowercase vocabulary, not a Go field name", k)
	}
}

// TestClusterParamToMap_DropsOmittedFields guards the issue #201 family
// fix: ClusterParam pointer fields omitted from a partial update must not
// materialize as null entries in the audit snapshot (phantom diff_keys).
// The snapshot keys are API lowercase vocabulary (issue #205).
func TestClusterParamToMap_DropsOmittedFields(t *testing.T) {
	m := clusterParamToMap(&ClusterParam{Description: lib.PString("d1")})
	require.NotNil(t, m)
	assert.Equal(t, map[string]interface{}{"description": "d1"}, m)

	assert.Nil(t, clusterParamToMap(nil))
	assert.Nil(t, clusterToMap(nil))
}

// TestClusterParamToMap_APIVocabulary is the issue #205 key-name layer
// anchor: every snapshot key (top level and nested) must be the cluster
// Open API field name, internal bookkeeping fields must be trimmed, and
// value representations must match the API (string enum, decoded
// epp_config object).
func TestClusterParamToMap_APIVocabulary(t *testing.T) {
	m := clusterParamToMap(&ClusterParam{
		Name:        lib.PString("c1"),
		Description: lib.PString("d1"),
		Basic: &ClusterBasicParam{
			Connection: &ClusterBasicConnectionParam{
				MaxIdleConnPerRs:    lib.PInt16(10),
				CancelOnClientClose: lib.PBool(true),
			},
			Retries: &ClusterBasicRetriesParam{
				MaxRetryInSubcluster:    lib.PInt8(2),
				MaxRetryCrossSubcluster: lib.PInt8(7), // trimmed: no API field
			},
			Buffers: &ClusterBasicBuffersParam{
				ReqWriteBufferSize: lib.PInt32(512),
				ReqFlushInterval:   lib.PInt32(100), // trimmed: no API field
				ResFlushInterval:   lib.PInt32(-1),  // trimmed: no API field
			},
			Timeouts: &ClusterBasicTimeoutsParam{
				TimeoutConnServ:        lib.PInt32(50000),
				TimeoutResponseHeader:  lib.PInt32(50000),
				TimeoutReadbodyClient:  lib.PInt32(30000),
				TimeoutReadClientAgain: lib.PInt32(30000),
				TimeoutWriteClient:     lib.PInt32(60000),
			},
			Protocol: lib.PString("http"),
		},
		StickySessions: &ClusterStickySessionsParam{
			SessionSticky: lib.PBool(true),
			HashStrategy:  lib.PInt32(ClusterHashStrategyClientIDOnlyI),
			HashHeader:    lib.PString("x-uid"),
		},
		PassiveHealthCheck: &ClusterPassiveHealthCheckParam{
			Schema:     lib.PString("http"), // trimmed: no API field
			Interval:   lib.PInt32(1000),
			Failnum:    lib.PInt32(3),
			Statuscode: lib.PInt32(200),
			Host:       lib.PString("h"),
			Uri:        lib.PString("/"),
		},
		LLMConfig: &LLMConfig{
			Models:   []string{"m1"},
			Provider: lib.PString("p1"),
		},
		BalanceMode: lib.PString(BalanceModeEPP),
		EppConfig:   lib.PString(`{"load_profile":"balanced"}`),
		// Internal bookkeeping fields: must not leak into the snapshot.
		ID:          lib.PInt64(7),
		ProductID:   lib.PInt64(2),
		SubClusters: []string{"sc1"},
		Scheduler:   map[string]map[string]int{"bfe1": {"sc1": 100}},
	})

	require.NotNil(t, m)
	assertNoUppercaseKeys(t, m)
	assertMapKeys(t, m, "name", "description", "basic", "sticky_sessions",
		"passive_health_check", "llm_config", "balance_mode", "epp_config")
	for _, internal := range []string{"id", "product_id", "sub_clusters", "scheduler", "instance_pool"} {
		assert.NotContains(t, m, internal)
	}

	assert.Equal(t, "c1", m["name"])
	assert.Equal(t, "d1", m["description"])
	assert.Equal(t, BalanceModeEPP, m["balance_mode"])

	basic, ok := m["basic"].(map[string]interface{})
	require.True(t, ok, "basic should be a JSON map")
	assertNoUppercaseKeys(t, basic)
	assertMapKeys(t, basic, "connection", "retries", "buffers", "timeouts", "protocol")

	conn, ok := basic["connection"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, conn, "max_idle_conn_per_rs", "cancel_on_client_close")
	assert.Equal(t, int16(10), conn["max_idle_conn_per_rs"])
	assert.Equal(t, true, conn["cancel_on_client_close"])

	retries, ok := basic["retries"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, retries, "max_retry_in_cluster")
	assert.Equal(t, int8(2), retries["max_retry_in_cluster"])

	buffers, ok := basic["buffers"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, buffers, "req_write_buffer_size")
	assert.Equal(t, int32(512), buffers["req_write_buffer_size"])

	timeouts, ok := basic["timeouts"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, timeouts, "timeout_conn_serv", "timeout_response_header",
		"timeout_readbody_client", "timeout_read_client_again", "timeout_write_client")

	assert.Equal(t, "http", basic["protocol"])

	sticky, ok := m["sticky_sessions"].(map[string]interface{})
	require.True(t, ok, "sticky_sessions should be a JSON map")
	assertMapKeys(t, sticky, "enabled", "hash_strategy", "hash_header")
	assert.Equal(t, true, sticky["enabled"])
	assert.Equal(t, "CLIENT_ID_ONLY", sticky["hash_strategy"])
	assert.Equal(t, "x-uid", sticky["hash_header"])

	phc, ok := m["passive_health_check"].(map[string]interface{})
	require.True(t, ok, "passive_health_check should be a JSON map")
	assertMapKeys(t, phc, "interval", "failnum", "statuscode", "host", "uri")
	assert.Equal(t, int32(1000), phc["interval"])

	llmConfig, ok := m["llm_config"].(map[string]interface{})
	require.True(t, ok, "llm_config should be a JSON map")
	assertNoUppercaseKeys(t, llmConfig)
	assert.Equal(t, "p1", llmConfig["provider"])
	assert.Equal(t, []interface{}{"m1"}, llmConfig["models"])

	epp, ok := m["epp_config"].(map[string]interface{})
	require.True(t, ok, "epp_config should be a decoded JSON object, not an escaped string")
	assert.Equal(t, "balanced", epp["load_profile"])
}

// TestClusterToMap_APIVocabulary is the issue #205 before-snapshot anchor:
// the stored Cluster model leaks Go field names (ID/Ready/ProductID/
// Scheduler/SubClusters...) and internal nested fields (phc.Schema, cross
// subcluster retry, flush intervals); none of them may surface, and the
// remaining keys/values must match the GET /clusters representation.
func TestClusterToMap_APIVocabulary(t *testing.T) {
	c := newTestClusterBase() // fills ID/SubClusters/Scheduler/phc.Schema and friends
	c.Ready = true
	c.ProductID = 2
	c.Description = "old desc"
	c.LLMConfig = &LLMConfig{Models: []string{"m1"}, Provider: lib.PString("p1")}
	c.EppConfig = `{"load_profile":"balanced"}`

	m := clusterToMap(c)
	require.NotNil(t, m)
	assertNoUppercaseKeys(t, m)
	assertMapKeys(t, m, "name", "description", "basic", "sticky_sessions",
		"passive_health_check", "llm_config", "balance_mode", "epp_config")
	for _, internal := range []string{"id", "ready", "product_id", "scheduler", "sub_clusters", "instance_pool"} {
		assert.NotContains(t, m, internal)
	}

	assert.Equal(t, "c1", m["name"])
	assert.Equal(t, "old desc", m["description"])
	// Empty storage value normalizes to the API default, like GET /clusters.
	assert.Equal(t, BalanceModeWRR, m["balance_mode"])

	basic, ok := m["basic"].(map[string]interface{})
	require.True(t, ok)
	retries, ok := basic["retries"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, retries, "max_retry_in_cluster")
	assert.Equal(t, int8(1), retries["max_retry_in_cluster"])

	buffers, ok := basic["buffers"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, buffers, "req_write_buffer_size")
	assert.Equal(t, int32(1024), buffers["req_write_buffer_size"])

	sticky, ok := m["sticky_sessions"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, sticky, "enabled", "hash_strategy", "hash_header")
	assert.Equal(t, false, sticky["enabled"])
	assert.Equal(t, "CLIENT_ID_ONLY", sticky["hash_strategy"])

	phc, ok := m["passive_health_check"].(map[string]interface{})
	require.True(t, ok)
	assertMapKeys(t, phc, "interval", "failnum", "statuscode", "host", "uri")

	llmConfig, ok := m["llm_config"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "p1", llmConfig["provider"])

	epp, ok := m["epp_config"].(map[string]interface{})
	require.True(t, ok, "epp_config should be a decoded JSON object")
	assert.Equal(t, "balanced", epp["load_profile"])
}

// TestClusterToMap_EPPVocabulary covers the EPP balance mode and the
// hash_strategy enum translation on the stored-model path.
func TestClusterToMap_EPPVocabulary(t *testing.T) {
	c := newTestClusterEPP()
	c.StickySessions.HashStrategy = ClusterHashStrategyClientIPOnlyI

	m := clusterToMap(c)
	require.NotNil(t, m)
	assert.Equal(t, BalanceModeEPP, m["balance_mode"])

	sticky, ok := m["sticky_sessions"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "CLIENT_IP_ONLY", sticky["hash_strategy"])
}

func TestClusterHashStrategySnapshotVocabulary(t *testing.T) {
	assert.Equal(t, "CLIENT_ID_ONLY", clusterHashStrategyI2S[ClusterHashStrategyClientIDOnlyI])
	assert.Equal(t, "CLIENT_IP_ONLY", clusterHashStrategyI2S[ClusterHashStrategyClientIPOnlyI])
	assert.Equal(t, "CLIENT_ID_PREFERED", clusterHashStrategyI2S[ClusterHashStrategyClientIDPreferedI])
	// Unknown values fall back to "", mirroring the API read path
	// (one.go clusterModel2Control inline map lookup).
	assert.Equal(t, "", clusterHashStrategyI2S[999])
}

func TestEppConfigSnapshotValue(t *testing.T) {
	assert.Nil(t, eppConfigSnapshotValue(""))

	v := eppConfigSnapshotValue(`{"a":1}`)
	m, ok := v.(map[string]interface{})
	require.True(t, ok, "valid epp_config should decode to a JSON object")
	assert.Equal(t, float64(1), m["a"])

	// Defensive: undecodable input falls back to the raw string.
	assert.Equal(t, "not-json", eppConfigSnapshotValue("not-json"))
}

// TestClusterManager_UpdateCluster_RecordsAPIVocabularyAuditLog is the
// issue #205 regression anchor for the SC2101-TC046 assert-update-logs
// cluster target: PATCH {description} must produce diff_keys
// ["description"] and lowercase API-vocabulary before/after snapshots on
// both the success and the pre-txn validation failure paths.
func TestClusterManager_UpdateCluster_RecordsAPIVocabularyAuditLog(t *testing.T) {
	ctx := context.Background()
	product := &ibasic.Product{ID: 2, Name: "test"}

	newManager := func(recorder *fakeOperationLogRecorder) *ClusterManager {
		clusterStore := &fakeClusterStorager{
			clusterUpdateFn: func(ctx context.Context, product *ibasic.Product, old *Cluster, param *ClusterParam) error {
				return nil
			},
		}
		m := NewClusterManager(&fakeTxn{}, clusterStore, &fakeSubClusterStorager{},
			&fakeBFEClusterStorager{}, &fakePoolStorager{}, nil, nil, nil, nil)
		m.SetOperationLogManager(recorder)
		return m
	}

	t.Run("partial update success", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		m := newManager(recorder)

		// The endpoints layer binds the URI cluster_name into param.Name, so a
		// real PATCH carries it; the snapshot mirrors that shape (issue
		// evidence: after={"Description": ..., "Name": ...} lowercased).
		err := m.UpdateCluster(ctx, product, newTestClusterBase(),
			&ClusterParam{Name: lib.PString("c1"), Description: lib.PString("updated cluster")})
		require.NoError(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ActionUpdate), entry.Action)
		assert.Equal(t, string(ioperlog.ResourceTypeCluster), entry.ResourceType)
		assert.Equal(t, ioperlog.StatusSuccess, entry.Status)

		summary := entry.ChangeSummary
		require.NotNil(t, summary)
		after, ok := summary["after"].(map[string]interface{})
		require.True(t, ok, "after should be present")
		assert.Equal(t, map[string]interface{}{"description": "updated cluster", "name": "c1"}, after)

		// name is unchanged (same value as before), so only description diffs.
		assert.Equal(t, []string{"description"}, summary["diff_keys"])

		before, ok := summary["before"].(map[string]interface{})
		require.True(t, ok, "before should be present")
		assertNoUppercaseKeys(t, before)
		assertMapKeys(t, before, "name", "description", "basic", "sticky_sessions",
			"passive_health_check", "balance_mode")
		assert.Equal(t, "c1", before["name"])
		assert.Equal(t, BalanceModeWRR, before["balance_mode"])
	})

	t.Run("validate failure keeps vocabulary", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		m := newManager(recorder)

		err := m.UpdateCluster(ctx, product, newTestClusterBase(),
			&ClusterParam{BalanceMode: lib.PString("ROUND_ROBIN")})
		require.Error(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)

		summary := entry.ChangeSummary
		require.NotNil(t, summary)
		after, ok := summary["after"].(map[string]interface{})
		require.True(t, ok, "after should be present")
		assert.Equal(t, map[string]interface{}{"balance_mode": "ROUND_ROBIN"}, after)
		assert.Equal(t, []string{"balance_mode"}, summary["diff_keys"])

		before, ok := summary["before"].(map[string]interface{})
		require.True(t, ok, "before should be present")
		assertNoUppercaseKeys(t, before)
	})
}
