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

package file_test

import (
	"encoding/json"
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

// ---------- BT-4-000 可达性冒烟：产品线回退后可进入业务逻辑（ghost → 404） ----------

func TestBatchFile_Reachable(t *testing.T) {
	resp, err := testutil.GetClient().Get("/open-api/v1/batch-files/"+helper.Unique("ghost"), map[string]string{
		"provider": "deepseek",
	})
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- BT-4-001 直写 batch_files → 200 字段正确 ----------

func TestBatchFile_Found(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-4-001")
	row := helper.BatchFileRow{
		FileID:      tag,
		Provider:    "deepseek",
		APIKeyID:    tag + "-ak",
		ProductName: helper.TestProductName,
		Direction:   "input",
		Purpose:     "batch_input",
		Filename:    "input.jsonl",
		Lines:       128,
		Bytes:       65536,
	}
	helper.InsertBatchFile(t, db, row)
	defer helper.DeleteBatchFile(t, db, tag, "deepseek")

	resp, err := testutil.GetClient().Get("/open-api/v1/batch-files/"+tag, map[string]string{
		"provider": "deepseek",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.Equal(t, tag, data["file_id"])
	assert.Equal(t, "deepseek", data["provider"])
	assert.Equal(t, tag+"-ak", data["api_key_id"])
	assert.Equal(t, helper.TestProductName, data["product_name"])
	assert.Equal(t, "input", data["direction"])
	assert.Equal(t, "batch_input", data["purpose"])
	// issue #254：filename 由 provider File 对象落库后透出
	assert.Equal(t, "input.jsonl", data["filename"])
	assert.Equal(t, float64(128), data["lines"])
	assert.Equal(t, float64(65536), data["bytes"])
}

// ---------- BT-4-002 文件不存在 → 404 ----------

func TestBatchFile_NotFound(t *testing.T) {
	helper.RequireBatchAPI(t)

	resp, err := testutil.GetClient().Get("/open-api/v1/batch-files/"+helper.Unique("ghost"), map[string]string{
		"provider": "deepseek",
	})
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- BT-4-003 provider 必填 ----------

func TestBatchFile_ProviderRequired(t *testing.T) {
	helper.RequireBatchAPI(t)

	resp, err := testutil.GetClient().Get("/open-api/v1/batch-files/" + helper.Unique("bt-4-003"))
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 422)
}

// ---------- BT-4-004 产品线隔离：其它产品线的文件按不存在处理 ----------

func TestBatchFile_ProductIsolation(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-4-004")
	helper.InsertBatchFile(t, db, helper.BatchFileRow{
		FileID: tag, Provider: "deepseek", APIKeyID: tag + "-ak",
		ProductName: "OTHER_product", Direction: "input", Purpose: "batch_input",
	})
	defer helper.DeleteBatchFile(t, db, tag, "deepseek")

	resp, err := testutil.GetClient().Get("/open-api/v1/batch-files/"+tag, map[string]string{
		"provider": "deepseek",
	})
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}
