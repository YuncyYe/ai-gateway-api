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

package innerapi_test

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

func uniquePoolName() string {
	return "inpool-" + testutil.RandomString(8)
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

func fetchPool(t *testing.T, poolName string) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	return resp
}

func decodeEntry(t *testing.T, resp *testutil.APIResponse) map[string]interface{} {
	t.Helper()
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	return data
}

// IN-K8S-001/002/003/004：PUT 幂等 upsert、GET 单个、GET 列表、DELETE 生命周期
func TestInnerAPI_K8sPools_Lifecycle(t *testing.T) {
	poolName := uniquePoolName()

	// 创建：两个实例，第二个缺省 weight 应为 100
	resp := putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, nil),
		instanceBody("10.0.0.2", 8000, 50),
	})
	testutil.AssertSuccess(t, resp)
	entry := decodeEntry(t, resp)
	assert.Equal(t, poolName, entry["name"])
	assert.Equal(t, float64(2), entry["instance_count"])
	assert.Greater(t, entry["last_sync_time"], float64(0))
	instances := entry["instances"].([]interface{})
	require.Len(t, instances, 2)
	assert.Equal(t, float64(100), instances[0].(map[string]interface{})["weight"])
	assert.Equal(t, float64(50), instances[1].(map[string]interface{})["weight"])

	// GET 单个：字段与写入一致
	resp = fetchPool(t, poolName)
	testutil.AssertSuccess(t, resp)
	entry = decodeEntry(t, resp)
	assert.Equal(t, float64(2), entry["instance_count"])
	assert.Equal(t, "10.0.0.1", entry["instances"].([]interface{})[0].(map[string]interface{})["addr"])

	// 全量替换：实例数变化
	resp = putInstances(t, poolName, []interface{}{
		instanceBody("10.0.1.1", 9000, nil),
	})
	testutil.AssertSuccess(t, resp)
	entry = decodeEntry(t, fetchPool(t, poolName))
	assert.Equal(t, float64(1), entry["instance_count"])
	assert.Equal(t, "10.0.1.1", entry["instances"].([]interface{})[0].(map[string]interface{})["addr"])

	// 幂等：重复 PUT 同一列表，内容不变
	same := []interface{}{instanceBody("10.0.1.1", 9000, nil)}
	testutil.AssertSuccess(t, putInstances(t, poolName, same))
	testutil.AssertSuccess(t, putInstances(t, poolName, same))
	entry = decodeEntry(t, fetchPool(t, poolName))
	assert.Equal(t, float64(1), entry["instance_count"])

	// GET 列表：包含该 pool 且计数正确
	resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	var listData map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &listData))
	found := false
	for _, item := range listData["list"].([]interface{}) {
		e := item.(map[string]interface{})
		if e["name"] == poolName {
			found = true
			assert.Equal(t, float64(1), e["instance_count"])
			assert.Greater(t, e["last_sync_time"], float64(0))
		}
	}
	assert.True(t, found, "pool list should contain %s", poolName)

	// DELETE：删除后 GET 404，重复 DELETE 404
	resp, err = testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	resp = fetchPool(t, poolName)
	assert.Equal(t, 404, resp.ErrNum, "deleted pool should return 404, got %d: %s", resp.ErrNum, resp.ErrMsg)

	resp, err = testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.ErrNum, "deleting a missing pool should return 404, got %d: %s", resp.ErrNum, resp.ErrMsg)
}

// IN-K8S-005：参数校验——路径名非法、缺 addr、addr/port 重复、weight 越界均 422
func TestInnerAPI_K8sPools_Validation(t *testing.T) {
	poolName := uniquePoolName()

	// 路径名非法（首尾连字符）
	resp := putInstances(t, "-bad-", []interface{}{instanceBody("10.0.0.1", 8000, nil)})
	assert.Equal(t, 422, resp.ErrNum, "invalid pool name should return 422, got %d: %s", resp.ErrNum, resp.ErrMsg)

	// 缺少 addr
	resp = putInstances(t, poolName, []interface{}{map[string]interface{}{"port": 8000}})
	assert.Equal(t, 422, resp.ErrNum, "missing addr should return 422, got %d: %s", resp.ErrNum, resp.ErrMsg)

	// addr/port 重复
	resp = putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, nil),
		instanceBody("10.0.0.1", 8000, nil),
	})
	assert.Equal(t, 422, resp.ErrNum, "duplicate addr/port should return 422, got %d: %s", resp.ErrNum, resp.ErrMsg)

	// weight 越界
	resp = putInstances(t, poolName, []interface{}{instanceBody("10.0.0.1", 8000, 101)})
	assert.Equal(t, 422, resp.ErrNum, "weight out of range should return 422, got %d: %s", resp.ErrNum, resp.ErrMsg)

	// 空数组合法（零实例语义），随后清理
	resp = putInstances(t, poolName, []interface{}{})
	testutil.AssertSuccess(t, resp)
	entry := decodeEntry(t, fetchPool(t, poolName))
	assert.Equal(t, float64(0), entry["instance_count"])

	resp, err := testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
}

// IN-K8S-006：N:1 场景——同一 pool 供多个 provider 引用，GET 单个返回完整实例
// （provider 镜像与 cluster 派生池链路由 tests/provider/k8s_pool 覆盖）
func TestInnerAPI_K8sPools_SharedPoolRead(t *testing.T) {
	poolName := uniquePoolName()

	testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
		instanceBody("10.0.2.1", 8000, nil),
		instanceBody("10.0.2.2", 8000, nil),
	}))

	resp := fetchPool(t, poolName)
	testutil.AssertSuccess(t, resp)
	entry := decodeEntry(t, resp)
	assert.Equal(t, float64(2), entry["instance_count"])
	require.Len(t, entry["instances"].([]interface{}), 2)

	// 更新实例列表后 last_sync_time 不应回退
	resp = putInstances(t, poolName, []interface{}{instanceBody("10.0.2.3", 8000, nil)})
	testutil.AssertSuccess(t, resp)
	updated := decodeEntry(t, fetchPool(t, poolName))
	assert.GreaterOrEqual(t, updated["last_sync_time"], entry["last_sync_time"])

	resp, err := testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
}

// IN-K8S-007：拒绝写入零变更——对已存在 pool 的非法 PUT 不得部分写入；
// 非法路径名对 GET/DELETE 同样 422（不被当作"不存在"的 404）
func TestInnerAPI_K8sPools_RejectedWritePreservesState(t *testing.T) {
	poolName := uniquePoolName()
	t.Cleanup(func() {
		testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	})

	testutil.AssertSuccess(t, putInstances(t, poolName, []interface{}{
		instanceBody("10.0.0.1", 8000, 100),
	}))

	// 非法 PUT（重复 addr/port）→ 422，pool 内容逐字段不变
	resp := putInstances(t, poolName, []interface{}{
		instanceBody("10.9.9.9", 9000, 100),
		instanceBody("10.9.9.9", 9000, 100),
	})
	assert.Equal(t, 422, resp.ErrNum, "duplicate addr/port should return 422, got %d: %s", resp.ErrNum, resp.ErrMsg)

	entry := decodeEntry(t, fetchPool(t, poolName))
	assert.Equal(t, float64(1), entry["instance_count"], "rejected PUT must not touch the pool")
	assert.Equal(t, "10.0.0.1", entry["instances"].([]interface{})[0].(map[string]interface{})["addr"])

	// 非法路径名：GET/DELETE 同样 422
	resp, err := testutil.GetClient().Get("/inner-api/v1/k8s_pools/-bad-")
	require.NoError(t, err)
	assert.Equal(t, 422, resp.ErrNum, "invalid pool name on GET should return 422, got %d: %s", resp.ErrNum, resp.ErrMsg)

	resp, err = testutil.GetClient().Delete("/inner-api/v1/k8s_pools/-bad-")
	require.NoError(t, err)
	assert.Equal(t, 422, resp.ErrNum, "invalid pool name on DELETE should return 422, got %d: %s", resp.ErrNum, resp.ErrMsg)

	resp, err = testutil.GetClient().Delete("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
}
