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

package ai_context_update_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	aiContextRulesPath       = "/open-api/v1/ai-context-rules"
	validContextCond         = `req_path_in("/v1/chat/completions", false)`
	aiContextAuditResourceID = "ai_context_rules"
)

// ruleContractKeys 是合同锁定的响应规则元素键集合（4 字段，无 id/name/enabled/timestamps）。
var ruleContractKeys = []string{"cond", "mode", "max_context_tokens", "reserve_tokens"}

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

// ---------- helpers ----------

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

// rulesOf 提取响应 Data.rules（必须为数组；null/缺失即失败）。
func rulesOf(t *testing.T, resp *testutil.APIResponse) []interface{} {
	t.Helper()
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	rules, ok := data["rules"].([]interface{})
	require.True(t, ok, "Data.rules should be an array, got %v", data["rules"])
	return rules
}

// rulesSnapshot 取当前集合全量快照，用于回读零变更断言。
func rulesSnapshot(t *testing.T) map[string]interface{} {
	t.Helper()
	resp := getRules(t)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	return data
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// assertNoTimestamps 锁定响应不携带 created_at/updated_at（marshal 后 NotContains）。
func assertNoTimestamps(t *testing.T, resp *testutil.APIResponse) {
	t.Helper()
	bs, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	assert.NotContains(t, string(bs), "created_at")
	assert.NotContains(t, string(bs), "updated_at")
}

// assertRuleMatches 逐字段断言规则与期望值一致（期望含默认值回填），
// 并锁定响应键集合精确为合同 4 键（无 id/name/enabled/timestamps）。
func assertRuleMatches(t *testing.T, rule map[string]interface{}, want map[string]interface{}) {
	t.Helper()
	for k, v := range want {
		assert.Equal(t, v, rule[k], "rule key %s", k)
	}
	assert.ElementsMatch(t, ruleContractKeys, keysOf(rule),
		"rule keys must exactly match contract (no id/name/enabled/timestamps)")
}

// ---------- CTX-1-001 正常两条规则（家族1：顺序/回填/合同锁定/无时间戳） ----------

func TestAIContextRules_Update_NormalTwoRules(t *testing.T) {
	resp := putRules(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{
			"cond": validContextCond, "mode": "balanced",
			"max_context_tokens": 64000, "reserve_tokens": 8192,
		},
		map[string]interface{}{"cond": "default_t()", "mode": "off"},
	}})
	testutil.AssertSuccess(t, resp)

	rules := rulesOf(t, resp)
	require.Len(t, rules, 2)

	// [0]：显式预算字段原样返回。
	rule0, ok := rules[0].(map[string]interface{})
	require.True(t, ok, "rules[0] should be object")
	assertRuleMatches(t, rule0, map[string]interface{}{
		"cond": validContextCond, "mode": "balanced",
		"max_context_tokens": float64(64000), "reserve_tokens": float64(8192),
	})

	// [1]：default_t() 兜底 off，可空字段回填 0。
	rule1, ok := rules[1].(map[string]interface{})
	require.True(t, ok, "rules[1] should be object")
	assertRuleMatches(t, rule1, map[string]interface{}{
		"cond": "default_t()", "mode": "off",
		"max_context_tokens": float64(0), "reserve_tokens": float64(0),
	})

	assertNoTimestamps(t, resp)

	// GET 回读与 PUT 响应逐字段一致。
	getResp := getRules(t)
	assert.Equal(t, rules, rulesOf(t, getResp), "GET readback must equal PUT response field-by-field")
	assertNoTimestamps(t, getResp)
}

// ---------- CTX-1-002 422 负向矩阵（家族3/4：整体拒绝 + 回读零变更 + 错误归因） ----------

func TestAIContextRules_Update_RejectionMatrix(t *testing.T) {
	keeper := map[string]interface{}{
		"cond": "default_t()",
		"mode": "conservative",
	}
	putResp := putRules(t, map[string]interface{}{"rules": []interface{}{keeper}})
	testutil.AssertSuccess(t, putResp)
	before := rulesSnapshot(t)

	validRule := map[string]interface{}{"cond": validContextCond, "mode": "balanced"}

	tests := []struct {
		name  string
		rules []interface{}
		// wantErrField 非空时断言错误消息可归因到该字段（家族4；照 ai_cache
		// AC-1-005/006 的 ErrMsg 归因先例）。
		wantErrField string
	}{
		{"mode 缺失", []interface{}{map[string]interface{}{"cond": validContextCond}}, "mode"},
		{"mode 空串", []interface{}{map[string]interface{}{"cond": validContextCond, "mode": ""}}, "mode"},
		{"mode 非法枚举", []interface{}{map[string]interface{}{"cond": validContextCond, "mode": "hyper"}}, "mode"},
		{"mode 大小写敏感", []interface{}{map[string]interface{}{"cond": validContextCond, "mode": "Balanced"}}, "mode"},
		{"cond 缺失", []interface{}{map[string]interface{}{"mode": "off"}}, "cond"},
		{"cond 空串", []interface{}{map[string]interface{}{"cond": "", "mode": "off"}}, "cond"},
		{"cond 语法错 default_t( ", []interface{}{map[string]interface{}{"cond": "default_t(", "mode": "off"}}, "cond"},
		{"cond 未知原语 unknown_func()", []interface{}{map[string]interface{}{"cond": "unknown_func()", "mode": "off"}}, "cond"},
		{"max_context_tokens=-1", []interface{}{map[string]interface{}{"cond": validContextCond, "mode": "off", "max_context_tokens": -1}}, ""},
		{"reserve_tokens=-1", []interface{}{map[string]interface{}{"cond": validContextCond, "mode": "off", "reserve_tokens": -1}}, ""},
		{"重复 cond", []interface{}{
			map[string]interface{}{"cond": validContextCond, "mode": "balanced"},
			map[string]interface{}{"cond": validContextCond, "mode": "off"},
		}, ""},
		{"null 元素", []interface{}{validRule, nil}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := putRules(t, map[string]interface{}{"rules": tt.rules})
			testutil.AssertErrCode(t, resp, 422)
			if tt.wantErrField != "" {
				assert.Contains(t, resp.ErrMsg, tt.wantErrField,
					"error message must be attributable to field %q (家族4)", tt.wantErrField)
			}

			after := rulesSnapshot(t)
			assert.Equal(t, before, after, "rejected PUT must leave the collection unchanged (家族3)")
		})
	}
}

// waitForAIContextAudit 轮询操作日志，返回匹配 match 的首条记录。
// 集合级资源的所有 PUT 审计身份相同（resource_id 恒为 ai_context_rules），
// 因此必须按 change_summary 内容匹配到具体操作，不能取 List[0]。
func waitForAIContextAudit(t *testing.T, startTs int64, status string, match func(*testutil.OperationLogEntry) bool) *testutil.OperationLogEntry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, _, err := testutil.QueryOperationLogs(map[string]string{
			"resource_type": "ai_context_rule",
			"action":        "update",
			"status":        status,
			"start_time":    fmt.Sprintf("%d", startTs),
			"page_size":     "100",
		})
		require.NoError(t, err)
		for i := range result.List {
			if match(&result.List[i]) {
				return &result.List[i]
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("operation log not found after 15s (status=%s, since=%d)", status, startTs)
	return nil
}

// ---------- CTX-1-004 操作审计（家族7 / #201 / #155） ----------

func TestAIContextRules_Update_OperationLog(t *testing.T) {
	t.Run("success_audit", func(t *testing.T) {
		// cond 特征串（匹配时用不含引号的子串：快照 JSON 序列化后引号被转义）。
		probe := `req_path_in("/v1/audit-ok", false)`
		marker := "/v1/audit-ok"
		startTs := time.Now().Add(-2 * time.Second).Unix()

		putResp := putRules(t, map[string]interface{}{"rules": []interface{}{
			map[string]interface{}{"cond": probe, "mode": "balanced"},
		}})
		testutil.AssertSuccess(t, putResp)

		// 等到一条 after 快照包含本用例 cond 特征串的成功审计。
		entry := waitForAIContextAudit(t, startTs, "1", func(e *testutil.OperationLogEntry) bool {
			after, ok := e.ChangeSummary["after"].(map[string]interface{})
			if !ok {
				return false
			}
			raw, _ := json.Marshal(after)
			return strings.Contains(string(raw), marker)
		})
		require.NotNil(t, entry)

		assert.Equal(t, "update", entry.Action)
		assert.Equal(t, "ai_context_rule", entry.ResourceType)
		// 集合级资源身份固定，不依赖请求体。
		assert.Equal(t, aiContextAuditResourceID, entry.ResourceID)
		assert.Equal(t, aiContextAuditResourceID, entry.ResourceName)
		assert.Equal(t, float64(1), entry.Status)

		// change_summary 键集合精确为 before/after/diff_keys（#201：禁止 Contains）。
		cs := entry.ChangeSummary
		require.NotNil(t, cs)
		assert.ElementsMatch(t, []string{"before", "after", "diff_keys"}, keysOf(cs))

		// diff_keys 精确匹配（集合快照顶层键级 diff）。
		diffKeys, ok := cs["diff_keys"].([]interface{})
		require.True(t, ok, "diff_keys should be an array, got %v", cs["diff_keys"])
		gotDiff := make([]string, 0, len(diffKeys))
		for _, d := range diffKeys {
			gotDiff = append(gotDiff, d.(string))
		}
		assert.ElementsMatch(t, []string{"rules"}, gotDiff, "diff_keys must match exactly (家族7/#201)")

		// before/after 为整个集合快照，键名小写 API 词汇，含 rules；
		// 快照规则元素恰 4 合同键（无 id/name/enabled/timestamps）。
		for _, key := range []string{"before", "after"} {
			snap, ok := cs[key].(map[string]interface{})
			require.True(t, ok, "change_summary.%s should be object", key)
			_, ok = snap["rules"].([]interface{})
			assert.True(t, ok, "change_summary.%s.rules should be array", key)
		}
		afterRules := cs["after"].(map[string]interface{})["rules"].([]interface{})
		rule0 := afterRules[0].(map[string]interface{})
		assert.Equal(t, probe, rule0["cond"])
		assert.ElementsMatch(t, ruleContractKeys, keysOf(rule0),
			"audit snapshot keys must use lowercase API vocabulary without id/timestamps")

		// 快照 JSON 序列化后不含内部 "id" 键。
		raw, err := json.Marshal(cs)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"id"`, "audit snapshot must not leak internal id")
	})

	t.Run("failure_audit", func(t *testing.T) {
		startTs := time.Now().Unix()

		// PUT 422（非法 mode）：合同要求同样记录失败审计（家族7/#155）。
		resp := putRules(t, map[string]interface{}{"rules": []interface{}{
			map[string]interface{}{"cond": validContextCond, "mode": "hyper"},
		}})
		testutil.AssertErrCode(t, resp, 422)
		assert.Contains(t, resp.ErrMsg, "mode", "error message must be attributable to mode field (家族4)")

		entry := waitForAIContextAudit(t, startTs, "2", func(e *testutil.OperationLogEntry) bool {
			return e.ResourceType == "ai_context_rule" && e.Action == "update"
		})
		require.NotNil(t, entry)

		assert.Equal(t, float64(2), entry.Status, "failed PUT must be audited with failed status")
		// 身份取自服务端固定资源标识，不依赖请求体（#155）。
		assert.Equal(t, aiContextAuditResourceID, entry.ResourceID)
		assert.Equal(t, aiContextAuditResourceID, entry.ResourceName)
		assert.NotEmpty(t, entry.ErrorMsg, "failed audit should carry error reason")
	})
}

// ---------- CTX-1-003 清空（边界值：rules==[] 非 null） ----------

func TestAIContextRules_Update_Clear(t *testing.T) {
	putResp := putRules(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"cond": validContextCond, "mode": "balanced"},
	}})
	testutil.AssertSuccess(t, putResp)

	resp := putRules(t, map[string]interface{}{"rules": []interface{}{}})
	testutil.AssertSuccess(t, resp)

	getResp := getRules(t)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(getResp.Data, &data))
	rulesRaw, ok := data["rules"]
	require.True(t, ok, "top-level key rules must be present")
	rules, ok := rulesRaw.([]interface{})
	require.True(t, ok, "rules must be an array (not null)")
	assert.Len(t, rules, 0)
	assert.Contains(t, string(getResp.Data), `"rules":[]`)
	assertNoTimestamps(t, getResp)
}
