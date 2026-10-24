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

package ai_context_get_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	aiContextRulesPath = "/open-api/v1/ai-context-rules"
	validContextCond   = `req_path_in("/v1/chat/completions", false)`
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

func putRules(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(aiContextRulesPath, body)
	require.NoError(t, err)
	return resp
}

func getRules(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(aiContextRulesPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// ---------- CTX-2-001 空集合形状（家族11：顶层键含 rules 且为 [] 非 null） ----------

func TestAIContextRules_Get_EmptyShape(t *testing.T) {
	resp := putRules(t, map[string]interface{}{"rules": []interface{}{}})
	testutil.AssertSuccess(t, resp)

	getResp := getRules(t)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(getResp.Data, &data))

	rulesRaw, ok := data["rules"]
	require.True(t, ok, "top-level key rules must be present (家族11)")
	rules, ok := rulesRaw.([]interface{})
	require.True(t, ok, "rules must be an array (not null)")
	assert.Len(t, rules, 0)
	assert.Contains(t, string(getResp.Data), `"rules":[]`)

	// 合同锁定：空集合响应同样不得携带 created_at/updated_at。
	bs, err := json.Marshal(getResp.Data)
	require.NoError(t, err)
	assert.NotContains(t, string(bs), "created_at")
	assert.NotContains(t, string(bs), "updated_at")
}

// ---------- CTX-2-002 顺序：GET 顺序=提交顺序 ----------

func TestAIContextRules_Get_Order(t *testing.T) {
	cond1 := `req_path_in("/v1/alpha", false)`
	cond2 := `req_path_in("/v1/beta", false)`

	resp := putRules(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"cond": cond1, "mode": "balanced", "max_context_tokens": 1000},
		map[string]interface{}{"cond": cond2, "mode": "aggressive", "reserve_tokens": 500},
	}})
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(getRules(t).Data, &data))
	rules, ok := data["rules"].([]interface{})
	require.True(t, ok)
	require.Len(t, rules, 2)

	gotConds := make([]string, 0, 2)
	for i, item := range rules {
		rule, ok := item.(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		gotConds = append(gotConds, rule["cond"].(string))
	}
	assert.Equal(t, []string{cond1, cond2}, gotConds, "GET order must equal submission order (first-match-wins)")
}

// ---------- CTX-2-003 幂等：连续两次 GET 逐字段一致 ----------

func TestAIContextRules_Get_Idempotent(t *testing.T) {
	resp := putRules(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"cond": validContextCond, "mode": "conservative"},
	}})
	testutil.AssertSuccess(t, resp)

	first := getRules(t)
	second := getRules(t)

	var firstData, secondData map[string]interface{}
	require.NoError(t, json.Unmarshal(first.Data, &firstData))
	require.NoError(t, json.Unmarshal(second.Data, &secondData))
	assert.Equal(t, firstData, secondData, "two consecutive GETs must be field-by-field identical")
}
