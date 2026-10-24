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

package traffic_mirror_get_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	trafficMirrorRulesPath = "/open-api/v1/traffic-mirror-rules"
	validTMCond            = `req_path_prefix_in("/v1/chat/completions", true)`
	validTMCondAll         = `default_t()`
	validTMCondB           = `req_path_prefix_in("/v1/completions", true)`
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

// mustCreateCluster 创建真实 cluster（级联创建 provider），作为 mirror_cluster fixture。
func mustCreateCluster(t *testing.T) string {
	t.Helper()
	name, err := testutil.CreateCluster(testutil.UniqueClusterName())
	require.NoError(t, err, "create mirror cluster fixture failed")
	return name
}

func putTMRulesOK(t *testing.T, rules []interface{}) {
	t.Helper()
	resp, err := testutil.GetClient().Put(trafficMirrorRulesPath, map[string]interface{}{"rules": rules})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
}

// ---------- TM-2-001 空集合形状（边界值，家族11） ----------

func TestTrafficMirrorRules_Get_EmptyCollectionShape(t *testing.T) {
	putTMRulesOK(t, []interface{}{})

	resp, err := testutil.GetClient().Get(trafficMirrorRulesPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	rules, ok := data["rules"].([]interface{})
	require.True(t, ok, "Data.rules must be present and an array (not null), got %v", data["rules"])
	assert.Len(t, rules, 0)
	assert.Contains(t, string(resp.Data), `"rules":[]`)
}

// ---------- TM-2-002 顺序（返回数据） ----------

func TestTrafficMirrorRules_Get_OrderMatchesSubmission(t *testing.T) {
	cluster := mustCreateCluster(t)
	names := []string{
		testutil.UniqueName("tm-2-002-a"),
		testutil.UniqueName("tm-2-002-b"),
		testutil.UniqueName("tm-2-002-c"),
	}
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{"name": names[0], "cond": validTMCond, "mirror_cluster": cluster, "percentage": 10},
		map[string]interface{}{"name": names[1], "cond": validTMCondAll, "mirror_cluster": cluster, "percentage": 20},
		map[string]interface{}{"name": names[2], "cond": validTMCondB, "mirror_cluster": cluster, "percentage": 30},
	})

	resp, err := testutil.GetClient().Get(trafficMirrorRulesPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data struct {
		Rules []struct {
			Name       string  `json:"name"`
			Percentage float64 `json:"percentage"`
		} `json:"rules"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.Len(t, data.Rules, 3)
	for i, name := range names {
		assert.Equal(t, name, data.Rules[i].Name, "rules[%d] order must equal submission order", i)
	}
}

// ---------- TM-2-003 幂等（返回数据） ----------

func TestTrafficMirrorRules_Get_Idempotent(t *testing.T) {
	cluster := mustCreateCluster(t)
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{
			"name":           testutil.UniqueName("tm-2-003"),
			"cond":           validTMCond,
			"mirror_cluster": cluster,
			"percentage":     5,
			"set_headers":    map[string]interface{}{"X-Env": "shadow"},
		},
	})

	resp1, err := testutil.GetClient().Get(trafficMirrorRulesPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp1)
	resp2, err := testutil.GetClient().Get(trafficMirrorRulesPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp2)

	assert.Equal(t, string(resp1.Data), string(resp2.Data),
		"two consecutive GETs must return byte-identical Data (no random fields)")
}

// ---------- TM-2-004 可选键缺席/在场形状（合同一致性，家族11） ----------

func TestTrafficMirrorRules_Get_OptionalKeyShape(t *testing.T) {
	cluster := mustCreateCluster(t)

	// 同集合内混合：r1 缺省全部可选字段（6 键），r2 显式全部可选字段（10 键）。
	r1 := map[string]interface{}{
		"name": testutil.UniqueName("tm-2-004-min"), "cond": validTMCond, "mirror_cluster": cluster,
	}
	r2 := map[string]interface{}{
		"name": testutil.UniqueName("tm-2-004-full"), "cond": validTMCondAll, "mirror_cluster": cluster,
		"percentage":    10,
		"remove_headers": []interface{}{"Authorization"},
		"set_headers":    map[string]interface{}{"X-A": "1"},
		"body_rewrites":  []interface{}{map[string]interface{}{"path": "model", "value": "m2"}},
		"path_rewrite":   "/v1/internal/x",
	}
	putTMRulesOK(t, []interface{}{r1, r2})

	resp, err := testutil.GetClient().Get(trafficMirrorRulesPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data struct {
		Rules []map[string]interface{} `json:"rules"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.Len(t, data.Rules, 2)

	fixed := []string{"name", "cond", "mirror_cluster", "percentage", "created_at", "updated_at"}
	optional := []string{"remove_headers", "set_headers", "body_rewrites", "path_rewrite"}

	// r1：精确 6 固定键，可选键全部缺席。
	keys1 := make([]string, 0, len(data.Rules[0]))
	for k := range data.Rules[0] {
		keys1 = append(keys1, k)
	}
	assert.ElementsMatch(t, fixed, keys1, "minimal rule must carry exactly the 6 fixed keys")

	// r2：精确 10 键（固定 + 可选）。
	keys2 := make([]string, 0, len(data.Rules[1]))
	for k := range data.Rules[1] {
		keys2 = append(keys2, k)
	}
	assert.ElementsMatch(t, append(append([]string{}, fixed...), optional...), keys2,
		"full rule must carry exactly the 10 contract keys")

	// 双规则均不得含 id/enabled。
	for i, rule := range data.Rules {
		assert.NotContains(t, rule, "id", "rules[%d] must not contain internal id", i)
		assert.NotContains(t, rule, "enabled", "rules[%d] must not contain enabled", i)
	}
}
