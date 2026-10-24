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

package k8s_pool_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

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

func uniquePoolName() string {
	return "pool-" + testutil.RandomString(8)
}

func putInstances(t *testing.T, poolName string, instances []interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put("/inner-api/v1/k8s_pools/"+poolName+"/instances", instances)
	require.NoError(t, err)
	return resp
}

func instanceBody(addr string, port int, weight interface{}) map[string]interface{} {
	inst := map[string]interface{}{"addr": addr, "port": port}
	if weight != nil {
		inst["weight"] = weight
	}
	return inst
}

func fetchProviderData(t *testing.T, providerName string) map[string]interface{} {
	t.Helper()
	resp, err := testutil.GetClient().Get("/open-api/v1/providers/" + providerName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	return data
}

func clusterTableConfig(t *testing.T, resp *testutil.APIResponse) map[string]interface{} {
	t.Helper()
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	config, ok := data["Config"].(map[string]interface{})
	require.True(t, ok, "cluster_table Config should be an object")
	return config
}

func subClusterBackends(t *testing.T, config map[string]interface{}, clusterName string) []map[string]interface{} {
	t.Helper()
	cluster, ok := config[clusterName].(map[string]interface{})
	require.True(t, ok, "cluster %s should exist in cluster_table", clusterName)

	var backends []map[string]interface{}
	for _, v := range cluster {
		subClusterBackends, ok := v.([]interface{})
		if !ok {
			continue
		}
		for _, b := range subClusterBackends {
			backend, ok := b.(map[string]interface{})
			require.True(t, ok, "backend should be an object")
			backends = append(backends, backend)
		}
	}
	return backends
}

func assertBackendExists(t *testing.T, resp *testutil.APIResponse, clusterName, name string) {
	t.Helper()
	backends := subClusterBackends(t, clusterTableConfig(t, resp), clusterName)
	for _, b := range backends {
		if b["Name"] == name {
			return
		}
	}
	t.Fatalf("backend %s not found in cluster %s", name, clusterName)
}

func assertBackendNotExists(t *testing.T, resp *testutil.APIResponse, clusterName, name string) {
	t.Helper()
	backends := subClusterBackends(t, clusterTableConfig(t, resp), clusterName)
	for _, b := range backends {
		if b["Name"] == name {
			t.Fatalf("unexpected backend %s found in cluster %s", name, clusterName)
		}
	}
}

// TestK8sPool_EndToEnd exercises the full P1 contract (k8s-pools.md):
// PUT pool -> create k8s_pool-mode provider -> mirror appears -> cluster
// references provider -> export carries instances -> PUT adds/removes
// instances -> export follows -> DELETE pool -> mirror cleared and export
// becomes an empty entry.
func TestK8sPool_EndToEnd(t *testing.T) {
	poolName := uniquePoolName()
	providerName := testutil.UniqueProviderName()
	clusterName := testutil.UniqueClusterName()
	t.Cleanup(func() {
		testutil.DeleteCluster(clusterName)
		testutil.DeleteProvider(providerName)
		testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	})

	// 1. PUT pool with two instances (no provider references it yet).
	resp := putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, 100),
		instanceBody("10.0.0.2", 8000, nil), // weight omitted -> default 100
	})
	testutil.AssertSuccess(t, resp)
	testutil.AssertDataFieldEquals(t, resp, "name", poolName)
	testutil.AssertDataFieldEquals(t, resp, "instance_count", float64(2))
	testutil.AssertDataFieldNotEmpty(t, resp, "last_sync_time")

	// 2. GET the pool entry.
	resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	testutil.AssertDataFieldEquals(t, resp, "instance_count", float64(2))
	testutil.AssertListFieldLen(t, resp, "instances", 2)

	// 3. Create a k8s_pool-mode provider referencing the pool. The mirror is
	// empty at this point because the provider was created after the PUT.
	_, err = testutil.CreateProvider(providerName, map[string]interface{}{
		"instance_source": "k8s_pool",
		"k8s_pool_name":   poolName,
		"instance_pool":   []interface{}{},
	})
	require.NoError(t, err)
	data := fetchProviderData(t, providerName)
	assert.Equal(t, "k8s_pool", data["instance_source"])
	assert.Equal(t, poolName, data["k8s_pool_name"])
	mirror, _ := data["k8s_instance_pool"].([]interface{})
	assert.Len(t, mirror, 0, "mirror stays empty until the next pool PUT")

	// 4. Re-PUT the same instances: the fan-out fills the provider mirror.
	resp = putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, 100),
		instanceBody("10.0.0.2", 8000, 100),
	})
	testutil.AssertSuccess(t, resp)
	data = fetchProviderData(t, providerName)
	mirror, _ = data["k8s_instance_pool"].([]interface{})
	require.Len(t, mirror, 2, "mirror appears after the pool PUT fan-out")

	// 5. Create a cluster referencing the provider; the export must carry
	// the mirror instances as backends.
	_, err = testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
		"name": clusterName,
		"llm_config": map[string]interface{}{
			"models":   []string{"deepseek-chat"},
			"provider": providerName,
		},
	})
	require.NoError(t, err)
	resp, err = testutil.GetClient().Get("/inner-api/v1/configs/gslb_data/cluster_table")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	assertBackendExists(t, resp, clusterName, "10.0.0.1_8000")
	assertBackendExists(t, resp, clusterName, "10.0.0.2_8000")

	// 6. PUT again with one instance removed and one added: the provider
	// mirror and the export must follow.
	resp = putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.2", 8000, 100),
		instanceBody("10.0.0.3", 9000, 100),
	})
	testutil.AssertSuccess(t, resp)
	testutil.AssertDataFieldEquals(t, resp, "instance_count", float64(2))

	data = fetchProviderData(t, providerName)
	mirror, _ = data["k8s_instance_pool"].([]interface{})
	require.Len(t, mirror, 2)
	resp, err = testutil.GetClient().Get("/inner-api/v1/configs/gslb_data/cluster_table")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	assertBackendNotExists(t, resp, clusterName, "10.0.0.1_8000")
	assertBackendExists(t, resp, clusterName, "10.0.0.2_8000")
	assertBackendExists(t, resp, clusterName, "10.0.0.3_9000")

	// 7. DELETE the pool: the mirror is cleared, the export becomes an
	// empty entry (cluster key present, no backends).
	resp, err = testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	testutil.AssertDataNull(t, resp)

	resp, err = testutil.GetClient().Get("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)

	data = fetchProviderData(t, providerName)
	mirror, _ = data["k8s_instance_pool"].([]interface{})
	assert.Len(t, mirror, 0, "mirror cleared after pool deletion")

	resp, err = testutil.GetClient().Get("/inner-api/v1/configs/gslb_data/cluster_table")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	config := clusterTableConfig(t, resp)
	_, ok := config[clusterName].(map[string]interface{})
	require.True(t, ok, "cluster %s should still be exported as an empty entry", clusterName)
	assert.Empty(t, subClusterBackends(t, config, clusterName))
}

func TestK8sPool_List(t *testing.T) {
	poolName := uniquePoolName()
	t.Cleanup(func() {
		testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	})

	resp := putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, 100),
		instanceBody("10.0.0.2", 8000, 100),
		instanceBody("10.0.0.3", 8000, 100),
	})
	testutil.AssertSuccess(t, resp)

	resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	list, err := testutil.GetDataListField(resp, "list")
	require.NoError(t, err)
	found := false
	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		require.True(t, ok)
		if entry["name"] != poolName {
			continue
		}
		found = true
		assert.Equal(t, float64(3), entry["instance_count"])
		assert.NotZero(t, entry["last_sync_time"])
	}
	require.True(t, found, "pool %s should appear in the list", poolName)
}

func TestK8sPool_ValidationErrors(t *testing.T) {
	t.Run("PUT with invalid pool name", func(t *testing.T) {
		resp := putInstances(t, "-bad-", []interface{}{
			instanceBody("10.0.0.1", 8000, 100),
		})
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("PUT with duplicate addr/port", func(t *testing.T) {
		resp := putInstances(t, uniquePoolName(), []interface{}{
			instanceBody("10.0.0.1", 8000, 100),
			instanceBody("10.0.0.1", 8000, 100),
		})
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("PUT with out-of-range weight", func(t *testing.T) {
		resp := putInstances(t, uniquePoolName(), []interface{}{
			instanceBody("10.0.0.1", 8000, 101),
		})
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("PUT with out-of-range port", func(t *testing.T) {
		resp := putInstances(t, uniquePoolName(), []interface{}{
			instanceBody("10.0.0.1", 0, 100),
		})
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("PUT with missing addr", func(t *testing.T) {
		resp := putInstances(t, uniquePoolName(), []interface{}{
			map[string]interface{}{"port": 8000, "weight": 100},
		})
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("GET missing pool returns 404", func(t *testing.T) {
		resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools/" + uniquePoolName())
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 404)
	})

	t.Run("DELETE missing pool returns 404", func(t *testing.T) {
		resp, err := testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + uniquePoolName())
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 404)
	})
}

// TestProvider_K8sPoolModeUpdate covers the OpenAPI side of the contract:
// creating and PATCHing providers in k8s_pool mode, the read-only mirror
// rejection, and the legacy default-mode behavior.
func TestProvider_K8sPoolModeUpdate(t *testing.T) {
	t.Run("create rejects k8s_instance_pool as read-only", func(t *testing.T) {
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() { testutil.DeleteProvider(providerName) })
		resp, err := testutil.GetClient().Post("/open-api/v1/providers", map[string]interface{}{
			"name":              providerName,
			"models":            []string{"deepseek-chat"},
			"model_protocols":   []string{"openai"},
			"instance_source":   "k8s_pool",
			"k8s_pool_name":     uniquePoolName(),
			"k8s_instance_pool": []interface{}{instanceBody("10.0.0.1", 8000, 100)},
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("create rejects invalid instance_source", func(t *testing.T) {
		providerName := testutil.UniqueProviderName()
		resp, err := testutil.GetClient().Post("/open-api/v1/providers", map[string]interface{}{
			"name":            providerName,
			"models":          []string{"deepseek-chat"},
			"model_protocols": []string{"openai"},
			"instance_source": "unknown",
			"instance_pool":   []interface{}{instanceBody("10.0.0.1", 8000, 100)},
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("create k8s_pool mode without k8s_pool_name rejected", func(t *testing.T) {
		providerName := testutil.UniqueProviderName()
		resp, err := testutil.GetClient().Post("/open-api/v1/providers", map[string]interface{}{
			"name":            providerName,
			"models":          []string{"deepseek-chat"},
			"model_protocols": []string{"openai"},
			"instance_source": "k8s_pool",
			"instance_pool":   []interface{}{instanceBody("10.0.0.1", 8000, 100)},
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("mode switch back to instance_pool recovers the dormant pool", func(t *testing.T) {
		poolName := uniquePoolName()
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() { testutil.DeleteProvider(providerName) })

		_, err := testutil.CreateProvider(providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
		})
		require.NoError(t, err)

		resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
			"instance_source": "instance_pool",
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
			"model_protocols": []string{"openai"},
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		data := fetchProviderData(t, providerName)
		assert.Equal(t, "instance_pool", data["instance_source"])
		// The k8s pool name is kept dormant for a later switch back.
		assert.Equal(t, poolName, data["k8s_pool_name"])
	})

	t.Run("k8s_pool_name in default mode is dormant but format-checked", func(t *testing.T) {
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() { testutil.DeleteProvider(providerName) })

		_, err := testutil.CreateProvider(providerName, map[string]interface{}{
			"k8s_pool_name": "svc-dormant",
		})
		require.NoError(t, err)
		data := fetchProviderData(t, providerName)
		assert.Equal(t, "instance_pool", data["instance_source"])
		assert.Equal(t, "svc-dormant", data["k8s_pool_name"])

		resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
			"model_protocols": []string{"openai"},
			"k8s_pool_name":   "bad name",
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)
	})
}

// TestK8sPool_DeleteReferenced ensures the no-reference-protection contract:
// a referenced pool can be deleted and referencing providers are reset.
func TestK8sPool_DeleteReferenced(t *testing.T) {
	poolName := uniquePoolName()
	providerName := testutil.UniqueProviderName()
	t.Cleanup(func() {
		testutil.DeleteProvider(providerName)
		testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	})

	resp := putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, 100),
	})
	testutil.AssertSuccess(t, resp)

	_, err := testutil.CreateProvider(providerName, map[string]interface{}{
		"instance_source": "k8s_pool",
		"k8s_pool_name":   poolName,
		"instance_pool":   []interface{}{},
	})
	require.NoError(t, err)

	resp = putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, 100),
	})
	testutil.AssertSuccess(t, resp)

	resp, err = testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	data := fetchProviderData(t, providerName)
	mirror, _ := data["k8s_instance_pool"].([]interface{})
	assert.Len(t, mirror, 0, "referenced provider mirror cleared after pool deletion")
}

// TestK8sPool_ProviderMirrorLinkage covers the /k8s_pools <-> /providers
// linkage beyond the single-provider happy path: N:1 fan-out with isolation,
// PUT-empty clear semantics, and mode-switch detachment.
func TestK8sPool_ProviderMirrorLinkage(t *testing.T) {
	mirrorOf := func(t *testing.T, providerName string) []interface{} {
		t.Helper()
		data := fetchProviderData(t, providerName)
		mirror, _ := data["k8s_instance_pool"].([]interface{})
		return mirror
	}

	t.Run("N:1 fan-out updates every referencing provider and only those", func(t *testing.T) {
		poolA := uniquePoolName()
		poolB := uniquePoolName()
		provA1 := testutil.UniqueProviderName()
		provA2 := testutil.UniqueProviderName()
		provB := testutil.UniqueProviderName()
		t.Cleanup(func() {
			testutil.DeleteProvider(provA1)
			testutil.DeleteProvider(provA2)
			testutil.DeleteProvider(provB)
			testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolA)
			testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolB)
		})

		createK8sProvider := func(name, pool string) {
			_, err := testutil.CreateProvider(name, map[string]interface{}{
				"instance_source": "k8s_pool",
				"k8s_pool_name":   pool,
				"instance_pool":   []interface{}{},
			})
			require.NoError(t, err)
		}
		createK8sProvider(provA1, poolA)
		createK8sProvider(provA2, poolA)
		createK8sProvider(provB, poolB)

		// PUT pool A: both referencing providers get the mirror, provider of
		// pool B stays empty.
		testutil.AssertSuccess(t, putInstances(t, poolA, []interface{}{
			instanceBody("10.0.0.1", 8000, 100),
			instanceBody("10.0.0.2", 8000, nil),
		}))
		mirror := mirrorOf(t, provA1)
		require.Len(t, mirror, 2)
		first := mirror[0].(map[string]interface{})
		assert.Equal(t, "10.0.0.1", first["addr"])
		assert.Equal(t, float64(8000), first["port"])
		assert.Equal(t, float64(100), first["weight"])
		assert.Len(t, mirrorOf(t, provA2), 2)
		assert.Len(t, mirrorOf(t, provB), 0)

		// PUT pool B: only provider B follows.
		testutil.AssertSuccess(t, putInstances(t, poolB, []interface{}{
			instanceBody("10.0.1.1", 9000, 100),
		}))
		assert.Len(t, mirrorOf(t, provB), 1)
		assert.Len(t, mirrorOf(t, provA1), 2, "providers of pool A unaffected by pool B PUT")

		// DELETE pool A: both mirrors cleared, provider B untouched.
		resp, err := testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolA)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		assert.Len(t, mirrorOf(t, provA1), 0)
		assert.Len(t, mirrorOf(t, provA2), 0)
		assert.Len(t, mirrorOf(t, provB), 1, "provider of pool B unaffected by pool A DELETE")
	})

	t.Run("PUT empty list clears mirrors without deleting the pool", func(t *testing.T) {
		poolName := uniquePoolName()
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() {
			testutil.DeleteProvider(providerName)
			testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
		})

		_, err := testutil.CreateProvider(providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"instance_pool":   []interface{}{},
		})
		require.NoError(t, err)

		testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
			instanceBody("10.0.0.1", 8000, 100),
		}))
		require.Len(t, mirrorOf(t, providerName), 1)

		// Zero instances via PUT (pool entry still exists).
		testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{}))
		assert.Len(t, mirrorOf(t, providerName), 0, "mirror cleared by PUT empty list")

		resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools/" + poolName)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		testutil.AssertDataFieldEquals(t, resp, "instance_count", float64(0))
	})

	t.Run("mode switch detaches and re-attaches the linkage", func(t *testing.T) {
		poolName := uniquePoolName()
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() {
			testutil.DeleteProvider(providerName)
			testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
		})

		_, err := testutil.CreateProvider(providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"instance_pool":   []interface{}{},
		})
		require.NoError(t, err)

		testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
			instanceBody("10.0.0.1", 8000, 100),
		}))
		require.Len(t, mirrorOf(t, providerName), 1)

		// Switch to instance_pool mode: the mirror is cleared per contract
		// and subsequent pool PUTs no longer reach this provider.
		resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
			"instance_source": "instance_pool",
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
			"model_protocols": []string{"openai"},
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		assert.Len(t, mirrorOf(t, providerName), 0, "mirror cleared on mode switch")

		testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
			instanceBody("10.0.0.9", 8000, 100),
		}))
		assert.Len(t, mirrorOf(t, providerName), 0, "detached provider ignores pool PUTs")
		data := fetchProviderData(t, providerName)
		assert.Equal(t, "instance_pool", data["instance_source"])
		assert.Equal(t, poolName, data["k8s_pool_name"], "pool name stays dormant for switch back")
		pool, _ := data["instance_pool"].([]interface{})
		require.Len(t, pool, 1)
		assert.Equal(t, "1.2.3.4", pool[0].(map[string]interface{})["addr"])

		// Switch back to k8s_pool mode: the linkage re-attaches after the
		// next pool PUT fan-out.
		resp, err = testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"model_protocols": []string{"openai"},
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
			instanceBody("10.0.0.9", 8000, 100),
			instanceBody("10.0.0.10", 8000, nil),
		}))
		mirror := mirrorOf(t, providerName)
		require.Len(t, mirror, 2, "mirror follows again after re-attach")
		assert.Equal(t, "10.0.0.10", mirror[1].(map[string]interface{})["addr"])
	})
}

// TestK8sPool_RejectedWritesLeaveStateUnchanged pins the read-back-zero-change
// contract (skill: 4xx 拒绝路径必须回读): every rejected write here must leave
// the provider, the pool, and the exported cluster_table bit-identical.
func TestK8sPool_RejectedWritesLeaveStateUnchanged(t *testing.T) {
	t.Run("PATCH invalid k8s_pool_name → 422 → provider unchanged", func(t *testing.T) {
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() { testutil.DeleteProvider(providerName) })

		_, err := testutil.CreateProvider(providerName, map[string]interface{}{
			"k8s_pool_name": "svc-dormant-x",
		})
		require.NoError(t, err)

		resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
			"instance_pool":   []interface{}{instanceBody("10.0.0.2", 8081, 100)},
			"model_protocols": []string{"openai"},
			"k8s_pool_name":   "bad name",
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)

		data := fetchProviderData(t, providerName)
		assert.Equal(t, "svc-dormant-x", data["k8s_pool_name"], "rejected PATCH must not change stored k8s_pool_name")
		pool, _ := data["instance_pool"].([]interface{})
		require.Len(t, pool, 1)
		assert.Equal(t, "10.0.0.1", pool[0].(map[string]interface{})["addr"],
			"rejected PATCH must not persist the submitted instance_pool")
	})

	t.Run("create with read-only k8s_instance_pool → 422 → provider not created", func(t *testing.T) {
		providerName := testutil.UniqueProviderName()
		resp, err := testutil.GetClient().Post("/open-api/v1/providers", map[string]interface{}{
			"name":              providerName,
			"models":            []string{"deepseek-chat"},
			"model_protocols":   []string{"openai"},
			"instance_source":   "k8s_pool",
			"k8s_pool_name":     uniquePoolName(),
			"instance_pool":     []interface{}{},
			"k8s_instance_pool": []interface{}{instanceBody("10.0.0.1", 8000, 100)},
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)

		resp, err = testutil.GetClient().Get("/open-api/v1/providers/" + providerName)
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 404)
	})

	t.Run("PATCH switch to k8s_pool without name → 422 → provider unchanged", func(t *testing.T) {
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() { testutil.DeleteProvider(providerName) })

		_, err := testutil.CreateProvider(providerName, nil)
		require.NoError(t, err)

		resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"instance_pool":   []interface{}{instanceBody("10.0.0.1", 8080, 100)},
			"model_protocols": []string{"openai"},
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)

		data := fetchProviderData(t, providerName)
		assert.Equal(t, "instance_pool", data["instance_source"], "rejected mode switch must not change stored source")
		assert.Empty(t, data["k8s_instance_pool"])
	})

	t.Run("failed pool PUT leaves pool, mirror and export unchanged", func(t *testing.T) {
		poolName := uniquePoolName()
		providerName := testutil.UniqueProviderName()
		clusterName := testutil.UniqueClusterName()
		t.Cleanup(func() {
			testutil.DeleteCluster(clusterName)
			testutil.DeleteProvider(providerName)
			testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
		})

		testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
			instanceBody("10.0.0.1", 8000, 100),
		}))
		_, err := testutil.CreateProvider(providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"instance_pool":   []interface{}{},
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
			instanceBody("10.0.0.1", 8000, 100),
		}))
		_, err = testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
			"name": clusterName,
			"llm_config": map[string]interface{}{
				"models":   []string{"deepseek-chat"},
				"provider": providerName,
			},
		})
		require.NoError(t, err)

		// Rejected PUT: duplicate addr/port inside the new list.
		resp := putInstances(t, poolName, []interface{}{
			instanceBody("10.9.9.9", 9000, 100),
			instanceBody("10.9.9.9", 9000, 100),
		})
		testutil.AssertErrCode(t, resp, 422)

		entry := decodePoolEntry(t, fetchPoolEntry(t, poolName))
		assert.Equal(t, float64(1), entry["instance_count"], "rejected PUT must not touch the pool")
		assert.Equal(t, "10.0.0.1", entry["instances"].([]interface{})[0].(map[string]interface{})["addr"])

		data := fetchProviderData(t, providerName)
		mirror, _ := data["k8s_instance_pool"].([]interface{})
		require.Len(t, mirror, 1, "rejected PUT must not touch the mirror")
		assert.Equal(t, "10.0.0.1", mirror[0].(map[string]interface{})["addr"])

		// 防泄漏: the rejected instances must not reach the exported config.
		resp, err = testutil.GetClient().Get("/inner-api/v1/configs/gslb_data/cluster_table")
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		assertBackendExists(t, resp, clusterName, "10.0.0.1_8000")
		assertBackendNotExists(t, resp, clusterName, "10.9.9.9_9000")
	})
}

// TestProvider_K8sPoolOmissionMatrix pins the omission semantics (skill: PATCH
// 省略字段保留原值矩阵) for the three new fields.
func TestProvider_K8sPoolOmissionMatrix(t *testing.T) {
	t.Run("k8s mode PATCH keeps every unsubmitted field", func(t *testing.T) {
		poolName := uniquePoolName()
		providerName := testutil.UniqueProviderName()
		t.Cleanup(func() {
			testutil.DeleteProvider(providerName)
			testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
		})

		_, err := testutil.CreateProvider(providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"description":     "orig-desc",
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
		})
		require.NoError(t, err)

		resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"model_protocols": []string{"openai"},
			"description":     "new-desc",
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		data := fetchProviderData(t, providerName)
		assert.Equal(t, "new-desc", data["description"])
		assert.Equal(t, []interface{}{"deepseek-chat"}, data["models"])
		assert.Len(t, data["keys"], 2)
		pool, _ := data["instance_pool"].([]interface{})
		require.Len(t, pool, 1)
		assert.Equal(t, "1.2.3.4", pool[0].(map[string]interface{})["addr"],
			"dormant instance_pool must survive a PATCH that omits it")
		assert.Equal(t, poolName, data["k8s_pool_name"])
		assert.Empty(t, data["k8s_instance_pool"])
	})

	t.Run("explicit empty pool rejected at switch, dormant pool recovers when resubmitted", func(t *testing.T) {
		poolName := uniquePoolName()
		t.Cleanup(func() {
			testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
		})

		// KP-7-006a: switching to instance_pool with an explicit empty pool is
		// rejected at the switch point and the dormant pool stays intact.
		p1 := testutil.UniqueProviderName()
		t.Cleanup(func() { testutil.DeleteProvider(p1) })
		_, err := testutil.CreateProvider(p1, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
		})
		require.NoError(t, err)

		resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+p1, map[string]interface{}{
			"instance_source": "instance_pool",
			"instance_pool":   []interface{}{},
			"model_protocols": []string{"openai"},
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)

		data := fetchProviderData(t, p1)
		assert.Equal(t, "k8s_pool", data["instance_source"], "rejected switch must keep the stored mode")
		pool, _ := data["instance_pool"].([]interface{})
		require.Len(t, pool, 1, "dormant pool must stay intact after a rejected switch")
		assert.Equal(t, "1.2.3.4", pool[0].(map[string]interface{})["addr"])

		// KP-7-006b: resubmitting the dormant pool value at switch time makes
		// it effective again, while k8s_pool_name stays dormant.
		p2 := testutil.UniqueProviderName()
		t.Cleanup(func() { testutil.DeleteProvider(p2) })
		_, err = testutil.CreateProvider(p2, map[string]interface{}{
			"instance_source": "k8s_pool",
			"k8s_pool_name":   poolName,
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
		})
		require.NoError(t, err)

		resp, err = testutil.GetClient().Patch("/open-api/v1/providers/"+p2, map[string]interface{}{
			"instance_source": "instance_pool",
			"instance_pool":   []interface{}{instanceBody("1.2.3.4", 443, 100)},
			"model_protocols": []string{"openai"},
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		data = fetchProviderData(t, p2)
		assert.Equal(t, "instance_pool", data["instance_source"])
		assert.Equal(t, poolName, data["k8s_pool_name"], "pool name stays dormant for a later switch back")
		pool, _ = data["instance_pool"].([]interface{})
		require.Len(t, pool, 1)
		assert.Equal(t, "1.2.3.4", pool[0].(map[string]interface{})["addr"])
		assert.Empty(t, data["k8s_instance_pool"])
	})
}

// TestProvider_K8sPoolModeSwitchAuditLog pins the audit contract (skill:
// 更新用例必须断言审计日志): a mode-switch PATCH must produce a provider
// update log whose diff_keys exactly matches the fields that actually changed
// — ElementsMatch, not Contains (skill anti-pattern #4).
func TestProvider_K8sPoolModeSwitchAuditLog(t *testing.T) {
	poolName := uniquePoolName()
	providerName := testutil.UniqueProviderName()
	t.Cleanup(func() {
		testutil.DeleteProvider(providerName)
		testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	})

	// CreateProvider seeds instance_pool [10.0.0.1:8080] and
	// model_protocols [openai]; resubmitting identical values below keeps
	// them out of the expected diff.
	_, err := testutil.CreateProvider(providerName, nil)
	require.NoError(t, err)

	resp, err := testutil.GetClient().Patch("/open-api/v1/providers/"+providerName, map[string]interface{}{
		"instance_source": "k8s_pool",
		"k8s_pool_name":   poolName,
		"instance_pool":   []interface{}{instanceBody("10.0.0.1", 8080, 100)},
		"model_protocols": []string{"openai"},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	// The operation-log recorder flushes asynchronously; under a full-suite
	// run the backlog stretches the visible latency well past the 10s default,
	// so allow a generous window.
	entry, err := testutil.WaitForOperationLog(map[string]string{
		"resource_type": "provider",
		"action":        "update",
		"resource_name": providerName,
		"status":        "1",
	}, 30*time.Second)
	require.NoError(t, err, "mode-switch PATCH must produce a provider update audit log")

	require.NotNil(t, entry.ChangeSummary)
	diffKeys, ok := entry.ChangeSummary["diff_keys"].([]interface{})
	require.True(t, ok, "diff_keys should be an array")
	assert.ElementsMatch(t, []interface{}{"instance_source", "k8s_pool_name"}, diffKeys)

	after, ok := entry.ChangeSummary["after"].(map[string]interface{})
	require.True(t, ok, "after should be an object")
	assert.Equal(t, "k8s_pool", after["instance_source"])
	assert.Equal(t, poolName, after["k8s_pool_name"])
}

// fetchPoolEntry GETs one pool entry for read-back assertions.
func fetchPoolEntry(t *testing.T, poolName string) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// decodePoolEntry unmarshals a pool entry response into a field map.
func decodePoolEntry(t *testing.T, resp *testutil.APIResponse) map[string]interface{} {
	t.Helper()
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	return data
}
