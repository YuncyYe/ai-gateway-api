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

package ai_cache_update_test

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
	aiCacheRulesPath        = "/open-api/v1/ai-cache-rules"
	aiCacheInnerExportPath  = "/inner-api/v1/configs/ai-cache-rule"
	aiCacheAuditResourceID  = "ai_cache_rules"
	validAICacheCond        = `req_path_in("/v1/chat/completions", false)`
	validAICacheCondModel   = `req_path_in("/v1/chat/completions", false) && req_body_json_in("model", "deepseek-chat", false)`
	defaultCacheKeyStrategy = "lastQuestion"
	defaultMaxBodyBytes     = 1048576
	defaultMaxValueBytes    = 1048576
)

// ruleContractKeys 是合同锁定的响应规则元素键集合（7 字段，无内部 id）。
// 逐行 created_at/updated_at 自 issue #258 起不再返回：整组替换语义下无信息价值，
// 且不暴露字段同时消除批量 INSERT 的列集合不一致（见 ai-cache-rules.md §1）。
var ruleContractKeys = []string{
	"name", "cond", "cache_key_strategy", "cache_ttl",
	"max_body_bytes", "max_value_bytes", "enable_semantic_cache",
}

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

func acRule(name, cond string) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		"cond": cond,
	}
}

// aiCacheCondFor 在基准表达式上追加一个只用于区分规则身份的 model 子句：
// 导出的 cond 必须逐条不同，否则整个 ai_cache.data 会被 BFE 拒载（issue #261）。
func aiCacheCondFor(suffix string) string {
	return fmt.Sprintf(`req_path_in("/v1/chat/completions", false) && req_body_json_in("model", %q, false)`, suffix)
}

func putAICacheRules(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(aiCacheRulesPath, body)
	require.NoError(t, err)
	return resp
}

func getAICacheRules(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(aiCacheRulesPath)
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

// rulesSnapshot 取当前集合全量快照（业务字段；逐行时间戳自 issue #258 起不在契约内），
// 用于回读零变更断言。
func rulesSnapshot(t *testing.T) map[string]interface{} {
	t.Helper()
	resp := getAICacheRules(t)
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

// assertRuleFieldEquals 断言单个规则字段（数值做 float64 规整）。
func assertRuleFieldEquals(t *testing.T, rule map[string]interface{}, key string, want interface{}) {
	t.Helper()
	got, ok := rule[key]
	require.True(t, ok, "rule missing key %s (rule=%v)", key, rule)
	switch w := want.(type) {
	case int:
		gf, ok := got.(float64)
		require.True(t, ok, "key %s should be number, got %T", key, got)
		assert.Equal(t, float64(w), gf, "key %s", key)
	case int64:
		gf, ok := got.(float64)
		require.True(t, ok, "key %s should be number, got %T", key, got)
		assert.Equal(t, float64(w), gf, "key %s", key)
	default:
		assert.Equal(t, want, got, "key %s", key)
	}
}

// assertRuleMatches 逐字段断言规则与期望值一致（期望含默认值回填），
// 并锁定响应键集合精确为合同 7 键（无内部 id、无逐行时间戳；家族11/12 / #201 / #258）。
func assertRuleMatches(t *testing.T, rule map[string]interface{}, want map[string]interface{}) {
	t.Helper()
	for k, v := range want {
		assertRuleFieldEquals(t, rule, k, v)
	}
	assert.ElementsMatch(t, ruleContractKeys, keysOf(rule),
		"rule keys must exactly match contract (no id/enabled/timestamps)")
}

// assertRulesMatchWants 按数组下标逐条断言（顺序即优先级）。
func assertRulesMatchWants(t *testing.T, resp *testutil.APIResponse, wants []map[string]interface{}) {
	t.Helper()
	rules := rulesOf(t, resp)
	require.Len(t, rules, len(wants))
	for i, want := range wants {
		rule, ok := rules[i].(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		assertRuleMatches(t, rule, want)
	}
}

// putAndAssert200 执行 PUT 并断言 200，返回响应。
func putAndAssert200(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp := putAICacheRules(t, body)
	testutil.AssertSuccess(t, resp)
	return resp
}

// fetchInnerExport 拉取 Inner 导出（version 为空表示首拉）。
func fetchInnerExport(t *testing.T, version string) *testutil.APIResponse {
	t.Helper()
	var resp *testutil.APIResponse
	var err error
	if version == "" {
		resp, err = testutil.GetClient().Get(aiCacheInnerExportPath)
	} else {
		resp, err = testutil.GetClient().Get(aiCacheInnerExportPath, map[string]string{"version": version})
	}
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// innerExportVersion 首拉导出并返回 Version。
func innerExportVersion(t *testing.T) string {
	t.Helper()
	resp := fetchInnerExport(t, "")
	var data struct {
		Version string `json:"Version"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)
	return data.Version
}

// waitForAICacheAudit 轮询操作日志，返回匹配 match 的首条记录。
// 集合级资源的所有 PUT 审计身份相同（resource_id 恒为 ai_cache_rules），
// 因此必须按 change_summary 内容匹配到具体操作，不能取 List[0]。
func waitForAICacheAudit(t *testing.T, startTs int64, status string, match func(*testutil.OperationLogEntry) bool) *testutil.OperationLogEntry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, _, err := testutil.QueryOperationLogs(map[string]string{
			"resource_type": "ai_cache_rule",
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

// ---------- AC-1-001 最小参数（家族1：省略字段默认值语义） ----------

func TestAICacheRules_Update_MinimalParams(t *testing.T) {
	name := testutil.UniqueName("ac-1-001")
	resp := putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{acRule(name, validAICacheCondModel)},
	})

	rules := rulesOf(t, resp)
	require.Len(t, rules, 1)
	assertRuleMatches(t, rules[0].(map[string]interface{}), map[string]interface{}{
		"name":                  name,
		"cond":                  validAICacheCondModel,
		"cache_key_strategy":    defaultCacheKeyStrategy,
		"cache_ttl":             0,
		"max_body_bytes":        defaultMaxBodyBytes,
		"max_value_bytes":       defaultMaxValueBytes,
		"enable_semantic_cache": false,
	})

	// GET 回读与 PUT 响应逐字段一致（含时间戳）。
	getResp := getAICacheRules(t)
	assert.Equal(t, rules, rulesOf(t, getResp), "GET readback must equal PUT response field-by-field")
}

// ---------- AC-1-002 完整参数 3 条 ----------

func TestAICacheRules_Update_FullParams(t *testing.T) {
	n1 := testutil.UniqueName("ac-1-002-a")
	n2 := testutil.UniqueName("ac-1-002-b")
	n3 := testutil.UniqueName("ac-1-002-c")

	wants := []map[string]interface{}{
		{
			"name": n1, "cond": validAICacheCondModel,
			"cache_key_strategy": "allQuestions", "cache_ttl": 3600,
			"max_body_bytes": 2097152, "max_value_bytes": 2097152,
			"enable_semantic_cache": true,
		},
		{
			"name": n2, "cond": aiCacheCondFor(n2),
			"cache_key_strategy": "disabled", "cache_ttl": 86400,
			"max_body_bytes": 1048576, "max_value_bytes": 524288,
			"enable_semantic_cache": false,
		},
		{
			"name": n3, "cond": aiCacheCondFor(n3),
			"cache_key_strategy": "lastQuestion", "cache_ttl": 60,
			"max_body_bytes": 1048576, "max_value_bytes": 1048576,
			"enable_semantic_cache": true,
		},
	}
	body := map[string]interface{}{"rules": []interface{}{
		map[string]interface{}(wants[0]),
		map[string]interface{}(wants[1]),
		map[string]interface{}(wants[2]),
	}}
	resp := putAndAssert200(t, body)

	// 响应顺序=提交顺序，逐字段精确等于显式提交值。
	assertRulesMatchWants(t, resp, wants)
}

// ---------- AC-1-003 全量替换（家族1/3） ----------

func TestAICacheRules_Update_FullReplace(t *testing.T) {
	na1 := testutil.UniqueName("ac-1-003-a1")
	na2 := testutil.UniqueName("ac-1-003-a2")
	na3 := testutil.UniqueName("ac-1-003-a3")
	nb1 := testutil.UniqueName("ac-1-003-b1")

	// 集合 A：3 条（cond 逐条不同，见 issue #261）。
	putAndAssert200(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"name": na1, "cond": aiCacheCondFor(na1), "cache_ttl": 111},
		acRule(na2, aiCacheCondFor(na2)),
		acRule(na3, aiCacheCondFor(na3)),
	}})
	require.Len(t, rulesOf(t, getAICacheRules(t)), 3, "setup collection A")

	// 集合 B：改 a1（ttl/strategy）、增 b1；a2/a3 被删除。
	wantA1 := map[string]interface{}{
		"name": na1, "cond": aiCacheCondFor(na1),
		"cache_key_strategy": "allQuestions", "cache_ttl": 222,
		"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
		"enable_semantic_cache": true,
	}
	wantB1 := map[string]interface{}{
		"name": nb1, "cond": aiCacheCondFor(nb1),
		"cache_key_strategy": defaultCacheKeyStrategy, "cache_ttl": 0,
		"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
		"enable_semantic_cache": false,
	}
	resp := putAndAssert200(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}(wantA1),
		acRule(nb1, aiCacheCondFor(nb1)),
	}})

	// GET 逐字段精确等于新提交集合（含默认值回填）。
	assertRulesMatchWants(t, getAICacheRules(t), []map[string]interface{}{wantA1, wantB1})
	assertRulesMatchWants(t, resp, []map[string]interface{}{wantA1, wantB1})

	// 被删规则消失。
	gotNames := []string{}
	for _, r := range rulesOf(t, getAICacheRules(t)) {
		gotNames = append(gotNames, r.(map[string]interface{})["name"].(string))
	}
	assert.NotContains(t, gotNames, na2, "deleted rule must disappear")
	assert.NotContains(t, gotNames, na3, "deleted rule must disappear")
}

// ---------- AC-1-004 清空 ----------

func TestAICacheRules_Update_Clear(t *testing.T) {
	t.Run("rules_empty_array", func(t *testing.T) {
		putAndAssert200(t, map[string]interface{}{
			"rules": []interface{}{acRule(testutil.UniqueName("ac-1-004-pre"), validAICacheCond)},
		})
		resp := putAndAssert200(t, map[string]interface{}{"rules": []interface{}{}})

		// 响应 rules 为 []（非 null）。
		require.Len(t, rulesOf(t, resp), 0)
		assert.Contains(t, string(resp.Data), `"rules":[]`)

		// GET rules==[] 且顶层键含 rules（家族11 形状锁）。
		getResp := getAICacheRules(t)
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(getResp.Data, &data))
		rules, ok := data["rules"].([]interface{})
		require.True(t, ok, "rules must be present and an array (not null)")
		assert.Len(t, rules, 0)
	})

	t.Run("rules_omitted", func(t *testing.T) {
		putAndAssert200(t, map[string]interface{}{
			"rules": []interface{}{acRule(testutil.UniqueName("ac-1-004-pre2"), validAICacheCond)},
		})
		putAndAssert200(t, map[string]interface{}{})

		getResp := getAICacheRules(t)
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(getResp.Data, &data))
		rules, ok := data["rules"].([]interface{})
		require.True(t, ok, "omitted rules must clear collection; rules should be [] not null")
		assert.Len(t, rules, 0)
	})
}

// ---------- AC-1-005 非法 cond（家族3：回读零变更） ----------

func TestAICacheRules_Update_InvalidCond(t *testing.T) {
	// 先落一个合法集合，使回读零变更有判别力。
	keeper := testutil.UniqueName("ac-1-005-keeper")
	putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{acRule(keeper, validAICacheCond)},
	})

	tests := []struct {
		name string
		cond string
	}{
		{"syntax_error", "not_a_valid_expr("},
		// req_path_in 必须两参数（合同示例形态），单参数必须 422。
		{"req_path_in_single_arg", `req_path_in("/v1/chat/completions")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := rulesSnapshot(t)

			resp := putAICacheRules(t, map[string]interface{}{
				"rules": []interface{}{acRule(testutil.UniqueName("ac-1-005-bad"), tt.cond)},
			})
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, "cond", "error message must be attributable to cond field")

			assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3)")
		})
	}
}

// ---------- AC-1-006 name 重名（家族3） ----------

func TestAICacheRules_Update_DuplicateName(t *testing.T) {
	keeper := testutil.UniqueName("ac-1-006-keeper")
	putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{acRule(keeper, validAICacheCond)},
	})
	before := rulesSnapshot(t)

	dup := testutil.UniqueName("ac-1-006-dup")
	// 两条规则 cond 互不相同，保证失败原因唯一归因到重名（issue #261 判重不会抢先拦截）。
	resp := putAICacheRules(t, map[string]interface{}{
		"rules": []interface{}{
			acRule(dup, aiCacheCondFor(dup+"-a")),
			acRule(dup, aiCacheCondFor(dup+"-b")),
		},
	})
	testutil.AssertErrCode(t, resp, 422)
	assert.Contains(t, resp.ErrMsg, dup, "error message should identify the duplicated name")

	assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3)")
}

// ---------- AC-1-007 非法值矩阵（家族3/4，表驱动） ----------

func TestAICacheRules_Update_InvalidValueMatrix(t *testing.T) {
	keeper := testutil.UniqueName("ac-1-007-keeper")
	putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{acRule(keeper, validAICacheCond)},
	})

	longName := "n" + strings.Repeat("x", 128) // 恰好 129 字符（name 上限 128）

	tests := []struct {
		name string
		rule map[string]interface{}
	}{
		{"name_empty", map[string]interface{}{"name": "", "cond": validAICacheCond}},
		{"name_too_long_129", map[string]interface{}{"name": longName, "cond": validAICacheCond}},
		{"strategy_wrong_case", map[string]interface{}{
			"name": testutil.UniqueName("ac-1-007-case"), "cond": validAICacheCond,
			"cache_key_strategy": "LastQuestion",
		}},
		{"cache_ttl_negative", map[string]interface{}{
			"name": testutil.UniqueName("ac-1-007-ttl"), "cond": validAICacheCond,
			"cache_ttl": -1,
		}},
		{"max_body_bytes_zero", map[string]interface{}{
			"name": testutil.UniqueName("ac-1-007-mbb"), "cond": validAICacheCond,
			"max_body_bytes": 0,
		}},
		{"max_value_bytes_negative", map[string]interface{}{
			"name": testutil.UniqueName("ac-1-007-mvb"), "cond": validAICacheCond,
			"max_value_bytes": -1,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := rulesSnapshot(t)
			resp := putAICacheRules(t, map[string]interface{}{
				"rules": []interface{}{tt.rule},
			})
			testutil.AssertErrCode(t, resp, 422)
			assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3/4)")
		})
	}

	// rules 含 null 元素 → 422 + 回读零变更。
	t.Run("rules_null_element", func(t *testing.T) {
		before := rulesSnapshot(t)
		resp := putAICacheRules(t, map[string]interface{}{
			"rules": []interface{}{acRule(testutil.UniqueName("ac-1-007-null"), validAICacheCond), nil},
		})
		testutil.AssertErrCode(t, resp, 422)
		assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3/4)")
	})
}

// ---------- AC-1-008 边界正值（家族4：边界±1） ----------

func TestAICacheRules_Update_BoundaryValues(t *testing.T) {
	// name 恰好 128 字符（上界）。
	name128 := "ac-1-008-" + strings.Repeat("b", 119) // 9 + 119 = 128
	require.Len(t, name128, 128)

	want := map[string]interface{}{
		"name": name128, "cond": validAICacheCond,
		"cache_key_strategy": defaultCacheKeyStrategy, "cache_ttl": 0, // ttl 下界 ≥0
		"max_body_bytes":        1, // >0 下界
		"max_value_bytes":       1,
		"enable_semantic_cache": false,
	}
	resp := putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{map[string]interface{}(want)},
	})
	assertRulesMatchWants(t, resp, []map[string]interface{}{want})
}

// ---------- AC-1-009 合同锁定：响应不得含 id/enabled（家族11/12 / #201） ----------

func TestAICacheRules_Update_ContractLockNoInternalFields(t *testing.T) {
	na := testutil.UniqueName("ac-1-009-a")
	nb := testutil.UniqueName("ac-1-009-b")

	resp := putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{
			acRule(na, aiCacheCondFor(na)),
			map[string]interface{}{
				"name": nb, "cond": aiCacheCondFor(nb),
				"cache_key_strategy": "disabled",
			},
		},
	})

	rules := rulesOf(t, resp)
	require.Len(t, rules, 2)
	for i, item := range rules {
		rule, ok := item.(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		// 精确键集合匹配（禁止 Contains：幻影键永不导致失败）。
		assert.ElementsMatch(t, ruleContractKeys, keysOf(rule),
			"rules[%d] must not contain internal id or enabled (家族11/12)", i)
		assert.NotContains(t, keysOf(rule), "id")
		assert.NotContains(t, keysOf(rule), "enabled")
	}
}

// ---------- AC-1-010 操作审计（家族7 / #201 / #155） ----------

func TestAICacheRules_Update_OperationLog(t *testing.T) {
	t.Run("success_audit", func(t *testing.T) {
		name := testutil.UniqueName("ac-1-010-ok")
		startTs := time.Now().Add(-2 * time.Second).Unix()

		putAndAssert200(t, map[string]interface{}{
			"rules": []interface{}{acRule(name, validAICacheCond)},
		})

		// 等到一条 after 快照包含本用例规则名的成功审计。
		entry := waitForAICacheAudit(t, startTs, "1", func(e *testutil.OperationLogEntry) bool {
			after, ok := e.ChangeSummary["after"].(map[string]interface{})
			if !ok {
				return false
			}
			raw, _ := json.Marshal(after)
			return strings.Contains(string(raw), name)
		})
		require.NotNil(t, entry)

		assert.Equal(t, "update", entry.Action)
		assert.Equal(t, "ai_cache_rule", entry.ResourceType)
		// 集合级资源身份固定，不依赖请求体。
		assert.Equal(t, aiCacheAuditResourceID, entry.ResourceID)
		assert.Equal(t, aiCacheAuditResourceID, entry.ResourceName)
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

		// before/after 为整个集合快照，键名小写 API 词汇，含 rules。
		for _, key := range []string{"before", "after"} {
			snap, ok := cs[key].(map[string]interface{})
			require.True(t, ok, "change_summary.%s should be object", key)
			_, ok = snap["rules"].([]interface{})
			assert.True(t, ok, "change_summary.%s.rules should be array", key)
		}
		afterRules := cs["after"].(map[string]interface{})["rules"].([]interface{})
		rule0 := afterRules[0].(map[string]interface{})
		assert.Equal(t, name, rule0["name"])
		assert.ElementsMatch(t, ruleContractKeys, keysOf(rule0),
			"audit snapshot keys must use lowercase API vocabulary without id")

		// 快照 JSON 序列化后不含内部 "id" 键。
		raw, err := json.Marshal(cs)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"id"`, "audit snapshot must not leak internal id")
	})

	t.Run("failure_audit", func(t *testing.T) {
		startTs := time.Now().Unix()

		// PUT 422（非法 cond）：合同要求同样记录失败审计（家族7/#155）。
		resp := putAICacheRules(t, map[string]interface{}{
			"rules": []interface{}{acRule(testutil.UniqueName("ac-1-010-bad"), "not_a_valid_expr(")},
		})
		testutil.AssertErrCode(t, resp, 422)

		entry := waitForAICacheAudit(t, startTs, "2", func(e *testutil.OperationLogEntry) bool {
			return e.ResourceType == "ai_cache_rule" && e.Action == "update"
		})
		require.NotNil(t, entry)

		assert.Equal(t, float64(2), entry.Status, "failed PUT must be audited with failed status")
		// 身份取自服务端固定资源标识，不依赖请求体（#155）。
		assert.Equal(t, aiCacheAuditResourceID, entry.ResourceID)
		assert.Equal(t, aiCacheAuditResourceID, entry.ResourceName)
		assert.NotEmpty(t, entry.ErrorMsg, "failed audit should carry error reason")
	})
}

// ---------- AC-1-011 混合集合假事务（家族3 假事务 + 家族4 防泄漏） ----------

func TestAICacheRules_Update_MixedCollection(t *testing.T) {
	// 落一个合法集合并取其 Inner 导出版本号。
	keeper := testutil.UniqueName("ac-1-011-keeper")
	putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{acRule(keeper, validAICacheCond)},
	})
	before := rulesSnapshot(t)
	versionBefore := innerExportVersion(t)

	// 同一 PUT：2 条合法 + 1 条非法（cond 编译失败）。
	// 两条合法规则的 cond 必须互不相同，否则会被"重复 cond"抢先拒绝（issue #261），
	// 用例的失败原因就不再是"第 3 条编译失败"。
	ok1 := testutil.UniqueName("ac-1-011-ok1")
	ok2 := testutil.UniqueName("ac-1-011-ok2")
	resp := putAICacheRules(t, map[string]interface{}{
		"rules": []interface{}{
			acRule(ok1, aiCacheCondFor(ok1)),
			acRule(ok2, aiCacheCondFor(ok2)),
			acRule(testutil.UniqueName("ac-1-011-bad"), "not_a_valid_expr("),
		},
	})
	testutil.AssertErrCode(t, resp, 422)

	// 断言 1（家族3 假事务）：三条都未生效，GET 回读零变更。
	assert.Equal(t, before, rulesSnapshot(t),
		"mixed valid+invalid PUT must not persist any rule (家族3 假事务)")

	// 断言 2（家族4 防泄漏）：被拒配置不得出现在 Inner 导出。
	// 带旧 version 增量拉取必须返回 Data=null（版本未推进即内容未变）。
	incrResp := fetchInnerExport(t, versionBefore)
	testutil.AssertDataNull(t, incrResp)
}

// ---------- AC-1-013 语义缓存开关往返（二期：enable_semantic_cache） ----------

// TestAICacheRules_Update_SemanticCacheFlag 验证 rules 元素新增 enable_semantic_cache：
// 显式 true/false 原样落库回读，省略时响应回填 false（GET 回读与 PUT 响应逐字段一致）。
func TestAICacheRules_Update_SemanticCacheFlag(t *testing.T) {
	nTrue := testutil.UniqueName("ac-1-013-true")
	nFalse := testutil.UniqueName("ac-1-013-false")
	nOmit := testutil.UniqueName("ac-1-013-omit")

	wantTrue := map[string]interface{}{
		"name": nTrue, "cond": validAICacheCondModel,
		"cache_key_strategy": "allQuestions", "cache_ttl": 600,
		"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
		"enable_semantic_cache": true,
	}
	wantFalse := map[string]interface{}{
		"name": nFalse, "cond": aiCacheCondFor(nFalse),
		"cache_key_strategy": defaultCacheKeyStrategy, "cache_ttl": 0,
		"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
		"enable_semantic_cache": false,
	}

	// 规则 1 显式 true；规则 2 显式 false；规则 3 省略（应回填 false）。
	resp := putAndAssert200(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}(wantTrue),
		map[string]interface{}(wantFalse),
		acRule(nOmit, aiCacheCondFor(nOmit)),
	}})

	wantOmit := map[string]interface{}{
		"name": nOmit, "cond": aiCacheCondFor(nOmit),
		"cache_key_strategy": defaultCacheKeyStrategy, "cache_ttl": 0,
		"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
		"enable_semantic_cache": false,
	}
	assertRulesMatchWants(t, resp, []map[string]interface{}{wantTrue, wantFalse, wantOmit})

	// GET 回读与 PUT 响应逐字段一致（家族1 往返）。
	assertRulesMatchWants(t, getAICacheRules(t), []map[string]interface{}{wantTrue, wantFalse, wantOmit})
}
// ---------- AC-1-014 回读原样 PUT（幂等编辑，家族1/11） ----------

// TestAICacheRules_Update_ReadbackRoundTrip 覆盖"读取—修改—写回"的最自然形态：
// GET 回读的 rules 原样作为 PUT body 提交（issue #258 的编辑路径）。
func TestAICacheRules_Update_ReadbackRoundTrip(t *testing.T) {
	n1 := testutil.UniqueName("ac-1-014-a")
	n2 := testutil.UniqueName("ac-1-014-b")

	putAndAssert200(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"name": n1, "cond": aiCacheCondFor(n1), "cache_ttl": 120},
		acRule(n2, aiCacheCondFor(n2)),
	}})

	getResp := getAICacheRules(t)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(getResp.Data, &data))
	readback, ok := data["rules"].([]interface{})
	require.True(t, ok, "GET Data.rules should be an array")
	require.Len(t, readback, 2)

	resp := putAndAssert200(t, map[string]interface{}{"rules": readback})

	got := rulesOf(t, resp)
	require.Len(t, got, 2)
	assert.Equal(t, readback, got, "原样回写的集合必须逐字段一致")
	for i, item := range got {
		rule, ok := item.(map[string]interface{})
		require.True(t, ok)
		assert.NotContains(t, rule, "created_at", "rules[%d] must not carry per-rule timestamps", i)
		assert.NotContains(t, rule, "updated_at", "rules[%d] must not carry per-rule timestamps", i)
	}
}

// ---------- AC-1-015 旧客户端携带只读时间戳（issue #258 复现原文） ----------

// TestAICacheRules_Update_LegacyTimestampOrderInsensitive 复现 issue 的两种排列：
// 带 created_at/updated_at 的旧规则在前（修复前 500）、在后（修复前静默丢 updated_at）。
// 修复后两种排列均 200，响应顺序等于各自提交顺序。
func TestAICacheRules_Update_LegacyTimestampOrderInsensitive(t *testing.T) {
	cases := []struct {
		name        string
		legacyFirst bool
	}{
		{name: "legacy_rule_first", legacyFirst: true},
		{name: "legacy_rule_last", legacyFirst: false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			oldName := testutil.UniqueName("ac-1-015-old")
			putAndAssert200(t, map[string]interface{}{"rules": []interface{}{acRule(oldName, validAICacheCond)}})

			// 旧客户端按 issue 的 curl 形态提交：旧规则带时间戳、新规则不带。
			legacy := map[string]interface{}{
				"name": oldName, "cond": aiCacheCondFor(oldName),
				"cache_key_strategy": defaultCacheKeyStrategy, "cache_ttl": 300,
				"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
				"enable_semantic_cache": false,
				"created_at":            "2026-10-19T04:27:56Z",
				"updated_at":            "2026-10-19T12:27:56Z",
			}
			freshName := testutil.UniqueName("ac-1-015-new")
			fresh := map[string]interface{}{"name": freshName, "cond": aiCacheCondFor(freshName), "cache_ttl": 300}

			rules := []interface{}{legacy, fresh}
			wantNames := []string{oldName, freshName}
			if !tt.legacyFirst {
				rules = []interface{}{fresh, legacy}
				wantNames = []string{freshName, oldName}
			}

			resp := putAndAssert200(t, map[string]interface{}{"rules": rules})
			got := rulesOf(t, resp)
			require.Len(t, got, 2, "both rules must be written (issue #258 regression)")

			gotNames := make([]string, 0, len(got))
			for _, item := range got {
				rule, ok := item.(map[string]interface{})
				require.True(t, ok)
				gotNames = append(gotNames, rule["name"].(string))
				assert.NotContains(t, rule, "created_at")
				assert.NotContains(t, rule, "updated_at")
			}
			assert.Equal(t, wantNames, gotNames, "响应顺序必须等于提交顺序")
		})
	}
}

// ---------- AC-1-016 只读时间戳被忽略（不回显、不落库） ----------

func TestAICacheRules_Update_TimestampsIgnored(t *testing.T) {
	name := testutil.UniqueName("ac-1-016")

	resp := putAndAssert200(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{
			"name": name, "cond": validAICacheCond,
			// 伪造的只读时间戳（2000 年）：必须被忽略而非落库/回显。
			"created_at": "2000-01-01T00:00:00Z",
			"updated_at": "2000-01-01T00:00:00Z",
		},
	}})

	rule := rulesOf(t, resp)[0].(map[string]interface{})
	assert.NotContains(t, rule, "created_at", "submitted created_at must not be echoed")
	assert.NotContains(t, rule, "updated_at", "submitted updated_at must not be echoed")

	readback := rulesOf(t, getAICacheRules(t))
	require.Len(t, readback, 1)
	got := readback[0].(map[string]interface{})
	assert.Equal(t, name, got["name"])
	assert.NotContains(t, got, "created_at")
	assert.NotContains(t, got, "updated_at")
}

// ---------- AC-1-017 集合内重复 cond（异常参数，issue #261） ----------

// TestAICacheRules_Update_DuplicateCond 验证同一次 PUT 的 rules 数组内 cond 逐字节
// 重复必须 422：BFE mod_ai_cache 以 cond 作为 product 内的规则键，重复会让整份
// ai_cache.data 被拒载（conf-agent 每 5s 重试同一文件、symlink 不推进），因此控制面
// 必须在入门前拦截，且拒绝即零变更 + 记一条失败审计。
func TestAICacheRules_Update_DuplicateCond(t *testing.T) {
	keeper := testutil.UniqueName("ac-1-017-keeper")
	putAndAssert200(t, map[string]interface{}{
		"rules": []interface{}{acRule(keeper, validAICacheCond)},
	})
	before := rulesSnapshot(t)

	// 两条规则仅 name 与 cache_ttl 不同、cond 逐字节相同：判重只看 cond。
	startTs := time.Now().Unix()
	resp := putAICacheRules(t, map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"name": testutil.UniqueName("ac-1-017-dup1"), "cond": validAICacheCond,
				"cache_ttl": 60,
			},
			map[string]interface{}{
				"name": testutil.UniqueName("ac-1-017-dup2"), "cond": validAICacheCond,
				"cache_ttl": 600,
			},
		},
	})
	testutil.AssertErrCode(t, resp, 422)
	assert.Contains(t, resp.ErrMsg, "duplicate", "error message must attribute the duplicated cond")
	assert.Contains(t, resp.ErrMsg, validAICacheCond, "error message must carry the duplicated cond verbatim")

	// 拒绝即零变更（家族3）。
	assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (issue #261)")

	// 拒绝同样记失败审计（家族7/#155）：before 快照取自库中现状，与请求体无关。
	entry := waitForAICacheAudit(t, startTs, "2", func(e *testutil.OperationLogEntry) bool {
		return e.ResourceType == "ai_cache_rule" && e.Action == "update" &&
			strings.Contains(e.ErrorMsg, validAICacheCond)
	})
	require.NotNil(t, entry)

	assert.Equal(t, float64(2), entry.Status, "duplicate cond must be audited as a failed write")
	assert.Equal(t, aiCacheAuditResourceID, entry.ResourceID)
	assert.Equal(t, aiCacheAuditResourceID, entry.ResourceName)

	beforeSnap, ok := entry.ChangeSummary["before"].(map[string]interface{})
	require.True(t, ok, "failure audit must carry a before snapshot")
	beforeRules, ok := beforeSnap["rules"].([]interface{})
	require.True(t, ok, "failure audit before.rules should be an array")
	require.Len(t, beforeRules, 1, "before snapshot must be the stored collection")
	assert.Equal(t, keeper, beforeRules[0].(map[string]interface{})["name"],
		"before snapshot must come from storage, not from the rejected request body")
}

// ---------- AC-1-018 唯一 cond 集合的幂等编辑（正常参数） ----------

// TestAICacheRules_Update_UniqueCondIdempotentEdit 验证新增的 cond 唯一性校验不干扰
// 正常编辑路径：cond 互不相同的集合可写入，且 GET 回读的 rules 原样作为 PUT body
// 回写仍 200、响应与回读逐字段一致，规则元素仍是合同 7 键。
func TestAICacheRules_Update_UniqueCondIdempotentEdit(t *testing.T) {
	n1 := testutil.UniqueName("ac-1-018-a")
	n2 := testutil.UniqueName("ac-1-018-b")

	wants := []map[string]interface{}{
		{
			"name": n1, "cond": aiCacheCondFor(n1),
			"cache_key_strategy": defaultCacheKeyStrategy, "cache_ttl": 120,
			"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
			"enable_semantic_cache": false,
		},
		{
			"name": n2, "cond": aiCacheCondFor(n2),
			"cache_key_strategy": "allQuestions", "cache_ttl": 0,
			"max_body_bytes": defaultMaxBodyBytes, "max_value_bytes": defaultMaxValueBytes,
			"enable_semantic_cache": false,
		},
	}

	resp := putAndAssert200(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"name": n1, "cond": aiCacheCondFor(n1), "cache_ttl": 120},
		map[string]interface{}{"name": n2, "cond": aiCacheCondFor(n2), "cache_key_strategy": "allQuestions"},
	}})
	assertRulesMatchWants(t, resp, wants)

	readback := rulesOf(t, getAICacheRules(t))
	require.Len(t, readback, 2, "GET readback must contain both rules")
	assert.Equal(t, rulesOf(t, resp), readback, "GET 回读与 PUT 响应逐字段一致")

	// 原样回写：唯一 cond 集合不得被判重拦截。
	resp2 := putAndAssert200(t, map[string]interface{}{"rules": readback})
	assertRulesMatchWants(t, resp2, wants)
	assert.Equal(t, readback, rulesOf(t, resp2), "原样回写的集合必须逐字段一致")
}
