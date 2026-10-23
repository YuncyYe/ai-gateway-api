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

package list_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	helper "github.com/rainway-ai-gateway/ai-gateway-api/integration/tests/batch/helper"
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

// ---------- BT-1-000 可达性冒烟：产品线回退后端点可进入业务逻辑 ----------

// TestBatchList_Reachable 冒烟验证产品线解析（默认产品线回退）可用，
// 端点返回业务语义而非 422 Fail To Get Product。
func TestBatchList_Reachable(t *testing.T) {
	resp, err := testutil.GetClient().Get("/open-api/v1/batches")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
}

// ---------- BT-1-001 空表列表（规格用例，注入缺陷修复后生效） ----------

func TestBatchList_Empty(t *testing.T) {
	helper.RequireBatchAPI(t)

	resp, err := testutil.GetClient().Get("/open-api/v1/batches")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	// 空表必须返回空数组（而非 null）+ 分页字段
	list, err := testutil.GetDataListField(resp, "list")
	require.NoError(t, err)
	assert.Equal(t, 0, len(list))

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	cursor, ok := data["next_cursor"]
	require.True(t, ok, "next_cursor must exist")
	assert.Equal(t, float64(0), cursor, "empty table next_cursor should be 0")
}

// ---------- BT-1-002 过滤（status / provider / api_key_id / 非默认产品线不可见） ----------

func TestBatchList_Filters(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-1-002")
	ak1, ak2 := helper.Unique("ak"), helper.Unique("ak")
	rows := []helper.BatchTaskRow{
		{BatchID: tag + "-a", APIKeyID: ak1, ProductName: helper.TestProductName, Provider: "deepseek", Status: "completed"},
		{BatchID: tag + "-b", APIKeyID: ak1, ProductName: helper.TestProductName, Provider: "deepseek", Status: "in_progress"},
		{BatchID: tag + "-c", APIKeyID: ak2, ProductName: helper.TestProductName, Provider: "openai", Status: "completed"},
		// 其它产品线（非默认产品线）的行不可见，任何过滤组合都不出现
		{BatchID: tag + "-x", APIKeyID: ak1, ProductName: "OTHER_product", Provider: "deepseek", Status: "completed"},
	}
	for i := range rows {
		rows[i].IdemKey = rows[i].BatchID + "-idem"
		defer helper.DeleteBatchTask(t, db, rows[i].BatchID)
	}
	for _, r := range rows {
		helper.InsertBatchTask(t, db, r)
	}

	batchIDs := func(resp *testutil.APIResponse) map[string]bool {
		list, err := testutil.GetDataListField(resp, "list")
		require.NoError(t, err)
		ids := map[string]bool{}
		for _, item := range list {
			entry, ok := item.(map[string]interface{})
			require.True(t, ok)
			ids[entry["batch_id"].(string)] = true
		}
		return ids
	}

	// status 过滤
	resp, err := testutil.GetClient().Get("/open-api/v1/batches", map[string]string{"status": "completed"})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	ids := batchIDs(resp)
	assert.True(t, ids[tag+"-a"], "completed row a visible")
	assert.True(t, ids[tag+"-c"], "completed row c visible")
	assert.False(t, ids[tag+"-b"], "in_progress row b filtered out")
	assert.False(t, ids[tag+"-x"], "other-product row x isolated")

	// provider 过滤
	resp, err = testutil.GetClient().Get("/open-api/v1/batches", map[string]string{"provider": "openai"})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	ids = batchIDs(resp)
	assert.True(t, ids[tag+"-c"], "openai row c visible")
	assert.False(t, ids[tag+"-a"], "deepseek row a filtered out")

	// api_key_id 过滤
	resp, err = testutil.GetClient().Get("/open-api/v1/batches", map[string]string{"api_key_id": ak2})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	ids = batchIDs(resp)
	assert.True(t, ids[tag+"-c"], "ak2 row c visible")
	assert.False(t, ids[tag+"-a"], "ak1 row a filtered out")
}

// ---------- BT-1-003 limit 上限钳制（>200 按 200） ----------

func TestBatchList_LimitClamped(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-1-003")
	const total = 205
	for i := 0; i < total; i++ {
		batchID := fmt.Sprintf("%s-%03d", tag, i)
		helper.InsertBatchTask(t, db, helper.BatchTaskRow{
			BatchID: batchID, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
			Provider: "deepseek", Status: "queued", IdemKey: batchID + "-idem",
		})
		defer helper.DeleteBatchTask(t, db, batchID)
	}

	resp, err := testutil.GetClient().Get("/open-api/v1/batches", map[string]string{"limit": "250"})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	list, err := testutil.GetDataListField(resp, "list")
	require.NoError(t, err)
	assert.Equal(t, 200, len(list), "limit>200 must be clamped to 200")

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.NotEqual(t, float64(0), data["next_cursor"], "205 rows with limit 200 must have next page")
}

// ---------- BT-1-004 cursor 翻页不重复不遗漏 ----------

func TestBatchList_CursorPagination(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-1-004")
	const total = 7
	for i := 0; i < total; i++ {
		batchID := fmt.Sprintf("%s-%03d", tag, i)
		helper.InsertBatchTask(t, db, helper.BatchTaskRow{
			BatchID: batchID, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
			Provider: "deepseek", Status: "queued", IdemKey: batchID + "-idem",
		})
		defer helper.DeleteBatchTask(t, db, batchID)
	}

	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 10; page++ {
		query := map[string]string{"limit": "3"}
		if cursor != "" {
			query["cursor"] = cursor
		}
		resp, err := testutil.GetClient().Get("/open-api/v1/batches", query)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		list, err := testutil.GetDataListField(resp, "list")
		require.NoError(t, err)

		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		next, _ := data["next_cursor"].(float64)

		for _, item := range list {
			entry := item.(map[string]interface{})
			bid := entry["batch_id"].(string)
			if len(bid) >= len(tag) && bid[:len(tag)] == tag {
				seen[bid]++
			}
		}
		if next == 0 {
			break
		}
		cursor = fmt.Sprintf("%d", int64(next))
	}

	assert.Equal(t, total, len(seen), "cursor pagination must not miss rows")
	for bid, n := range seen {
		assert.Equal(t, 1, n, "row %s must not repeat across pages", bid)
	}
}

// ---------- BT-1-005 列表元素零值键必现（issue #271） ----------

// TestBatchList_ZeroValuesExplicit 锁定列表元素与详情同规则：零值 int64
// 与空串必须显式出现（batches.md §2.1"字段必现规则"）。防"只修详情、
// 列表残留"——one.go 与 list.go 必须同批改。当前 HEAD 上本用例必须失败。
func TestBatchList_ZeroValuesExplicit(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-1-005")
	ak := helper.Unique("ak")
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: ak, ProductName: helper.TestProductName,
		Provider: "deepseek", Status: "completed", SettleStatus: "settled",
		ReserveUnits: 0, SettleUnits: 0, IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Get("/open-api/v1/batches", map[string]string{"api_key_id": ak})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	list, err := testutil.GetDataListField(resp, "list")
	require.NoError(t, err)
	require.Len(t, list, 1)
	entry, ok := list[0].(map[string]interface{})
	require.True(t, ok)

	for _, k := range []string{"est_lines", "usage_input_tokens", "usage_output_tokens",
		"reserve_units", "settle_units"} {
		v, ok := entry[k]
		require.True(t, ok, "列表元素键 %s 必须显式出现", k)
		assert.Equal(t, float64(0), v, "列表元素键 %s 的值必须为 0", k)
	}
	for _, k := range []string{"output_file_id", "terminal_at"} {
		v, ok := entry[k]
		require.True(t, ok, "列表元素键 %s 必须显式出现", k)
		assert.Equal(t, "", v, "列表元素键 %s 的值必须为空串", k)
	}

	body := string(resp.RawBody)
	for _, token := range []string{`"reserve_units":0`, `"settle_units":0`, `"terminal_at":""`} {
		assert.Contains(t, body, token, "原始报文必须含 %s", token)
	}
}
