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

package epp_assignments_test

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
	sm, err = testutil.StartServer()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	sm.Shutdown()
	os.Exit(code)
}

func patchEppPool(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	resp, err := testutil.GetClient().Patch("/open-api/v1/epp-pool", body)
	if err != nil {
		t.Fatalf("patch epp pool failed: %v", err)
	}
	return resp
}

func twoGroupsBody() map[string]interface{} {
	return map[string]interface{}{
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
	}
}

// createEPPCluster creates an EPP-mode cluster (provider auto-created) and
// returns its name.
func createEPPCluster(t *testing.T, name string) string {
	t.Helper()
	providerName := testutil.UniqueProviderName()
	if _, err := testutil.CreateProvider(providerName); err != nil {
		t.Fatalf("setup provider failed: %v", err)
	}
	resp, err := testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
		"name":         name,
		"balance_mode": "EPP",
		"epp_config":   map[string]interface{}{"load_profile": "balanced"},
		"llm_config": map[string]interface{}{
			"models":   []string{"deepseek-chat"},
			"provider": providerName,
		},
	})
	if err != nil {
		t.Fatalf("create epp cluster failed: %v", err)
	}
	if resp.ErrNum != 200 {
		t.Fatalf("create epp cluster failed: %d %s", resp.ErrNum, resp.ErrMsg)
	}
	return name
}

func getAssignments(t *testing.T, query ...map[string]string) map[string]interface{} {
	t.Helper()
	var resp *testutil.APIResponse
	var err error
	if len(query) > 0 {
		resp, err = testutil.GetClient().Get("/open-api/v1/epp-assignments", query[0])
	} else {
		resp, err = testutil.GetClient().Get("/open-api/v1/epp-assignments")
	}
	if err != nil {
		t.Fatalf("get assignments failed: %v", err)
	}
	testutil.AssertSuccess(t, resp)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal assignments failed: %v", err)
	}
	return data
}

func findClusterEntry(t *testing.T, data map[string]interface{}, cluster string) map[string]interface{} {
	t.Helper()
	clusters := data["clusters"].([]interface{})
	for _, item := range clusters {
		entry := item.(map[string]interface{})
		if entry["cluster"] == cluster {
			return entry
		}
	}
	return nil
}

func TestEppAssignments_Get(t *testing.T) {
	clusterName := testutil.UniqueClusterName()

	t.Cleanup(func() {
		testutil.DeleteCluster(clusterName)
	})

	t.Run("EA-1-001 配置实例池后创建 EPP 集群自动分配", func(t *testing.T) {
		resp := patchEppPool(t, twoGroupsBody())
		testutil.AssertSuccess(t, resp)

		createEPPCluster(t, clusterName)

		data := getAssignments(t)
		entry := findClusterEntry(t, data, clusterName)
		require.NotNil(t, entry, "cluster should appear in assignment view")
		assert.False(t, entry["degraded"].(bool))
		assert.Equal(t, "g1", entry["group"], "greedy allocator picks least-loaded group, tie-break by name")

		primary, ok := entry["primary"].(map[string]interface{})
		require.True(t, ok, "primary should be non-null after auto assignment")
		assert.Contains(t, []string{"epp-a", "epp-b"}, primary["id"])
		assert.NotEmpty(t, primary["host"])
		assert.NotEmpty(t, primary["port"])

		standby, ok := entry["standby"].(map[string]interface{})
		require.True(t, ok, "standby should be expanded from the same group")
		assert.NotEqual(t, primary["id"], standby["id"])
		assert.Contains(t, []string{"epp-a", "epp-b"}, standby["id"])

		assert.Contains(t, data["idle_groups"], "g2")
		assert.NotContains(t, data["unassigned_clusters"], clusterName)
	})

	t.Run("EA-1-002 按 cluster 过滤查询", func(t *testing.T) {
		data := getAssignments(t, map[string]string{"cluster": clusterName})
		clusters := data["clusters"].([]interface{})
		require.Len(t, clusters, 1)
		entry := clusters[0].(map[string]interface{})
		assert.Equal(t, clusterName, entry["cluster"])
		assert.NotNil(t, entry["primary"])
	})

	t.Run("EA-1-003 过滤不存在的 cluster 返回 404", func(t *testing.T) {
		resp, err := testutil.GetClient().Get("/open-api/v1/epp-assignments", map[string]string{
			"cluster": "not_exist_cluster",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 404)
	})

	t.Run("EA-1-004 过滤 WRR 集群返回 404", func(t *testing.T) {
		wrrCluster := testutil.UniqueClusterName()
		if _, err := testutil.CreateCluster(wrrCluster); err != nil {
			t.Fatalf("setup wrr cluster failed: %v", err)
		}
		defer testutil.DeleteCluster(wrrCluster)

		resp, err := testutil.GetClient().Get("/open-api/v1/epp-assignments", map[string]string{
			"cluster": wrrCluster,
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 404)
	})
}

// TestEppAssignments_GroupShrinkRepair 验证统一组规模规则（每组 1~2 实例）下的
// 悬空修复语义：PATCH /epp-pool 同步触发修复，单实例组是合法组。
func TestEppAssignments_GroupShrinkRepair(t *testing.T) {
	t.Run("EA-1-005 组缩容到单实例（保留 primary）后分配保留", func(t *testing.T) {
		resp := patchEppPool(t, twoGroupsBody())
		testutil.AssertSuccess(t, resp)

		cluster := testutil.UniqueClusterName()
		createEPPCluster(t, cluster)
		defer testutil.DeleteCluster(cluster)

		entry := findClusterEntry(t, getAssignments(t), cluster)
		require.NotNil(t, entry)
		group := entry["group"].(string)
		primary := entry["primary"].(map[string]interface{})

		// 全量替换为：组名不变、仅保留该 primary 的单实例组。
		resp = patchEppPool(t, map[string]interface{}{
			"groups": []interface{}{
				map[string]interface{}{
					"name":      group,
					"instances": []interface{}{primary},
				},
			},
		})
		testutil.AssertSuccess(t, resp)

		entry = findClusterEntry(t, getAssignments(t), cluster)
		require.NotNil(t, entry)
		assert.Equal(t, group, entry["group"], "单实例组合法：分配不换组")
		assert.Equal(t, primary["id"], entry["primary"].(map[string]interface{})["id"], "分配不换主")
		assert.Nil(t, entry["standby"])
		assert.False(t, entry["degraded"].(bool))
	})

	t.Run("EA-1-006 primary 被移除后同组重选", func(t *testing.T) {
		resp := patchEppPool(t, twoGroupsBody())
		testutil.AssertSuccess(t, resp)

		cluster := testutil.UniqueClusterName()
		createEPPCluster(t, cluster)
		defer testutil.DeleteCluster(cluster)

		entry := findClusterEntry(t, getAssignments(t), cluster)
		require.NotNil(t, entry)
		group := entry["group"].(string)

		// 同组名替换为全新实例（模拟原 primary 下线、同组换机）。
		replacement := map[string]interface{}{"id": "epp-replacement", "host": "10.0.9.9", "port": 9002}
		resp = patchEppPool(t, map[string]interface{}{
			"groups": []interface{}{
				map[string]interface{}{
					"name":      group,
					"instances": []interface{}{replacement},
				},
			},
		})
		testutil.AssertSuccess(t, resp)

		entry = findClusterEntry(t, getAssignments(t), cluster)
		require.NotNil(t, entry)
		assert.Equal(t, group, entry["group"], "组仍存在：不换组，同组重选 primary")
		assert.Equal(t, replacement["id"], entry["primary"].(map[string]interface{})["id"])
		assert.Nil(t, entry["standby"])
		assert.False(t, entry["degraded"].(bool))
	})
}
