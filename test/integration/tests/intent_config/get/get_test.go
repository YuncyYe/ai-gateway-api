// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package intent_config_get_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const intentConfigPath = "/open-api/v1/intent-config"

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

// ---------- IC-2-001 未发布返回 404 ----------

func TestIntentConfig_Get_NotPublished(t *testing.T) {
	resp, err := testutil.GetClient().Get(intentConfigPath)
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- IC-2-002 发布后回读一致 ----------

func TestIntentConfig_Get_Published(t *testing.T) {
	questions := []interface{}{
		map[string]interface{}{
			"name":         testutil.UniqueName("ic-2-002"),
			"type":         "choice",
			"instructions": "这条请求属于哪类研发任务？",
			"criteria": map[string]interface{}{
				"coding":       "编写或修改代码",
				"test_writing": "编写测试用例",
			},
		},
	}
	put, err := testutil.GetClient().Put(intentConfigPath, map[string]interface{}{
		"min_confidence": 0.65,
		"questions":      questions,
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, put)

	get, err := testutil.GetClient().Get(intentConfigPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, get)
	testutil.AssertDataFieldEquals(t, get, "min_confidence", 0.65)

	var getData map[string]interface{}
	require.NoError(t, json.Unmarshal(get.Data, &getData))
	got, ok := getData["questions"].([]interface{})
	require.True(t, ok)
	require.Len(t, got, 1)
	assert.Equal(t, questions[0].(map[string]interface{})["name"], got[0].(map[string]interface{})["name"])
	assert.Equal(t, questions[0].(map[string]interface{})["criteria"], got[0].(map[string]interface{})["criteria"])
	testutil.AssertDataFieldNotEmpty(t, get, "created_at")
	testutil.AssertDataFieldNotEmpty(t, get, "updated_at")
}

// ---------- IC-2-003 空 questions 软开关回读（200 而非 404） ----------

func TestIntentConfig_Get_EmptyQuestions(t *testing.T) {
	put, err := testutil.GetClient().Put(intentConfigPath, map[string]interface{}{"questions": []interface{}{}})
	require.NoError(t, err)
	testutil.AssertSuccess(t, put)

	get, err := testutil.GetClient().Get(intentConfigPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, get)

	raw, err := json.Marshal(get.Data)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"questions":[]`)
}
