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

package traffic_mirror_update_test

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
	trafficMirrorRulesPath       = "/open-api/v1/traffic-mirror-rules"
	trafficMirrorInnerExportPath = "/inner-api/v1/configs/traffic-mirror-rule"
	trafficMirrorAuditResourceID = "traffic_mirror_rules"

	validTMCond      = `req_path_prefix_in("/v1/chat/completions", true)`
	validTMCondModel = `req_path_prefix_in("/v1/chat/completions", true) && req_body_json_in("model", "gpt-4o", false)`
	validTMCondAll   = `default_t()`
	validTMCondB     = `req_path_prefix_in("/v1/completions", true)`
	validTMCondC     = `req_body_json_in("model", "deepseek-chat", false)`

	defaultTMPercentage = 100
)

// fixedRuleKeys 为恒输出的 6 个固定键（可选字段缺省时不输出）。
var fixedRuleKeys = []string{
	"name", "cond", "mirror_cluster", "percentage", "created_at", "updated_at",
}

// fullRuleKeys 为全字段提交时的 10 键（6 固定 + 4 可选）。
var fullRuleKeys = []string{
	"name", "cond", "mirror_cluster", "percentage",
	"remove_headers", "set_headers", "body_rewrites", "path_rewrite",
	"created_at", "updated_at",
}

// defaultSensitiveHeaders 为控制面导出时对"未提交 remove_headers"填的默认黑名单
//（与 model/traffic_mirror.DefaultSensitiveHeaders 合同一致，Inner 导出断言用）。
var defaultSensitiveHeaders = []string{"Authorization", "Cookie", "X-Api-Key"}

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

// mustCreateCluster 创建真实 cluster（级联创建 provider），作为 mirror_cluster 正向 fixture。
func mustCreateCluster(t *testing.T) string {
	t.Helper()
	name, err := testutil.CreateCluster(testutil.UniqueClusterName())
	require.NoError(t, err, "create mirror cluster fixture failed")
	return name
}

// tmRule 构造最小规则（name+cond+mirror_cluster）。
func tmRule(name, cond, cluster string) map[string]interface{} {
	return map[string]interface{}{
		"name":           name,
		"cond":           cond,
		"mirror_cluster": cluster,
	}
}

func putTMRules(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(trafficMirrorRulesPath, body)
	require.NoError(t, err)
	return resp
}

func putTMRulesOK(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp := putTMRules(t, body)
	testutil.AssertSuccess(t, resp)
	return resp
}

func getTMRules(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(trafficMirrorRulesPath)
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

// rulesSnapshot 取当前集合全量快照（含 created_at/updated_at），用于回读零变更断言。
func rulesSnapshot(t *testing.T) map[string]interface{} {
	t.Helper()
	resp := getTMRules(t)
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
	default:
		assert.Equal(t, want, got, "key %s", key)
	}
}

// assertRuleFixedKeys 断言规则恒输出 6 固定键且含时间戳（可选键缺席语义由调用方按需追加）。
func assertRuleFixedKeys(t *testing.T, rule map[string]interface{}) {
	t.Helper()
	assert.NotEmpty(t, rule["created_at"], "created_at should be present")
	assert.NotEmpty(t, rule["updated_at"], "updated_at should be present")
}

// assertRuleKeysExactly 精确锁定规则键集合（禁止 Contains：#201 幻影键不敏感）。
func assertRuleKeysExactly(t *testing.T, rule map[string]interface{}, want []string) {
	t.Helper()
	assert.ElementsMatch(t, want, keysOf(rule), "rule keys must exactly match contract")
	assert.NotContains(t, keysOf(rule), "id")
	assert.NotContains(t, keysOf(rule), "enabled")
}

// assertNoInternalFields 断言规则不含 id/enabled 键。
func assertNoInternalFields(t *testing.T, rule map[string]interface{}) {
	t.Helper()
	assert.NotContains(t, keysOf(rule), "id")
	assert.NotContains(t, keysOf(rule), "enabled")
}

// fetchInnerExport 拉取 Inner 导出（version 为空表示首拉）。
func fetchInnerExport(t *testing.T, version string) *testutil.APIResponse {
	t.Helper()
	var resp *testutil.APIResponse
	var err error
	if version == "" {
		resp, err = testutil.GetClient().Get(trafficMirrorInnerExportPath)
	} else {
		resp, err = testutil.GetClient().Get(trafficMirrorInnerExportPath, map[string]string{"version": version})
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

// exportedRemoveHeaders 取导出集合第一条规则的 removeHeaders（供两层默认语义断言）。
func exportedRemoveHeaders(t *testing.T) []string {
	t.Helper()
	resp := fetchInnerExport(t, "")
	testutil.AssertDataNotEmpty(t, resp)
	var data struct {
		Config map[string][]struct {
			RemoveHeaders []string `json:"removeHeaders"`
		} `json:"Config"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	rules, ok := data.Config["AI_product"]
	require.True(t, ok, "Config.AI_product should be present")
	require.NotEmpty(t, rules, "need at least one exported rule")
	return rules[0].RemoveHeaders
}

// waitForTMAudit 轮询操作日志，返回匹配 match 的首条记录。
// 集合级资源的所有 PUT 审计身份相同（resource_id 恒为 traffic_mirror_rules），
// 因此必须按 change_summary 内容匹配到具体操作，不能取 List[0]。
func waitForTMAudit(t *testing.T, startTs int64, status string, match func(*testutil.OperationLogEntry) bool) *testutil.OperationLogEntry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, _, err := testutil.QueryOperationLogs(map[string]string{
			"resource_type": "traffic_mirror_rule",
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

// ---------- TM-1-001 最小参数（家族1：省略字段默认值语义 + mirror_cluster 正向存在性） ----------

func TestTrafficMirrorRules_Update_MinimalParams(t *testing.T) {
	cluster := mustCreateCluster(t)
	name := testutil.UniqueName("tm-1-001")

	resp := putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{tmRule(name, validTMCondAll, cluster)},
	})

	rules := rulesOf(t, resp)
	require.Len(t, rules, 1)
	rule := rules[0].(map[string]interface{})
	assertRuleFieldEquals(t, rule, "name", name)
	assertRuleFieldEquals(t, rule, "cond", validTMCondAll)
	assertRuleFieldEquals(t, rule, "mirror_cluster", cluster)
	assertRuleFieldEquals(t, rule, "percentage", defaultTMPercentage) // 默认值回填
	assertRuleFixedKeys(t, rule)
	// 可选字段缺省不输出（键缺席而非 null）——家族1 省略语义。
	assertRuleKeysExactly(t, rule, fixedRuleKeys)

	// GET 回读与 PUT 响应逐字段一致（含时间戳）。
	getResp := getTMRules(t)
	assert.Equal(t, rules, rulesOf(t, getResp), "GET readback must equal PUT response field-by-field")
}

// ---------- TM-1-002 完整参数 3 条 ----------

func TestTrafficMirrorRules_Update_FullParams(t *testing.T) {
	c1 := mustCreateCluster(t)
	c2 := mustCreateCluster(t)
	n1 := testutil.UniqueName("tm-1-002-a")
	n2 := testutil.UniqueName("tm-1-002-b")
	n3 := testutil.UniqueName("tm-1-002-c")

	r1 := map[string]interface{}{
		"name": n1, "cond": validTMCondModel, "mirror_cluster": c1,
		"percentage":    10,
		"set_headers":   map[string]interface{}{"X-Shadow-Env": "pre-release"},
		"body_rewrites": []interface{}{map[string]interface{}{"path": "model", "value": "deepseek-v3"}},
	}
	r2 := map[string]interface{}{
		"name": n2, "cond": validTMCond, "mirror_cluster": c2,
		"percentage":    1,
		"remove_headers": []interface{}{"Authorization", "Cookie", "X-Api-Key", "X-Custom-Secret"},
	}
	r3 := map[string]interface{}{
		"name": n3, "cond": validTMCondAll, "mirror_cluster": c1,
		"percentage":  0,
		"path_rewrite": "/v1/internal/chat/completions",
	}
	resp := putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{r1, r2, r3},
	})

	rules := rulesOf(t, resp)
	require.Len(t, rules, 3)
	for i, want := range []map[string]interface{}{r1, r2, r3} {
		rule, ok := rules[i].(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		for k, v := range want {
			assertRuleFieldEquals(t, rule, k, v)
		}
		assertRuleFixedKeys(t, rule)
		assertNoInternalFields(t, rule)
		// 键集合 = 6 固定键 + 本条已提交的可选键（可选字段缺省缺席，家族1）。
		wantKeys := append([]string{}, fixedRuleKeys...)
		for _, opt := range []string{"remove_headers", "set_headers", "body_rewrites", "path_rewrite"} {
			if _, submitted := want[opt]; submitted {
				wantKeys = append(wantKeys, opt)
			}
		}
		assertRuleKeysExactly(t, rule, wantKeys)
	}
}

// ---------- TM-1-003 全量替换（家族1/3） ----------

func TestTrafficMirrorRules_Update_FullReplace(t *testing.T) {
	c1 := mustCreateCluster(t)
	na1 := testutil.UniqueName("tm-1-003-a1")
	na2 := testutil.UniqueName("tm-1-003-a2")
	na3 := testutil.UniqueName("tm-1-003-a3")
	nb1 := testutil.UniqueName("tm-1-003-b1")

	// 集合 A：3 条。
	putTMRulesOK(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"name": na1, "cond": validTMCond, "mirror_cluster": c1, "percentage": 11},
		tmRule(na2, validTMCondB, c1),
		tmRule(na3, validTMCondC, c1),
	}})
	require.Len(t, rulesOf(t, getTMRules(t)), 3, "setup collection A")

	// 集合 B：改 a1（percentage）、增 b1；a2/a3 被删除。
	wantA1 := map[string]interface{}{
		"name": na1, "cond": validTMCond, "mirror_cluster": c1, "percentage": 22,
	}
	wantB1 := map[string]interface{}{
		"name": nb1, "cond": validTMCondB, "mirror_cluster": c1, "percentage": defaultTMPercentage,
	}
	putTMRulesOK(t, map[string]interface{}{"rules": []interface{}{
		map[string]interface{}(wantA1),
		tmRule(nb1, validTMCondB, c1),
	}})

	got := rulesOf(t, getTMRules(t))
	require.Len(t, got, 2)
	for i, want := range []map[string]interface{}{wantA1, wantB1} {
		rule, ok := got[i].(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		for k, v := range want {
			assertRuleFieldEquals(t, rule, k, v)
		}
		assertRuleFixedKeys(t, rule)
		assertRuleKeysExactly(t, rule, fixedRuleKeys)
	}

	gotNames := []string{}
	for _, r := range got {
		gotNames = append(gotNames, r.(map[string]interface{})["name"].(string))
	}
	assert.NotContains(t, gotNames, na2, "deleted rule must disappear")
	assert.NotContains(t, gotNames, na3, "deleted rule must disappear")
}

// ---------- TM-1-004 清空 ----------

func TestTrafficMirrorRules_Update_Clear(t *testing.T) {
	t.Run("rules_empty_array", func(t *testing.T) {
		cluster := mustCreateCluster(t)
		putTMRulesOK(t, map[string]interface{}{
			"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-004-pre"), validTMCond, cluster)},
		})
		resp := putTMRulesOK(t, map[string]interface{}{"rules": []interface{}{}})

		require.Len(t, rulesOf(t, resp), 0)
		assert.Contains(t, string(resp.Data), `"rules":[]`)

		getResp := getTMRules(t)
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(getResp.Data, &data))
		rules, ok := data["rules"].([]interface{})
		require.True(t, ok, "rules must be present and an array (not null)")
		assert.Len(t, rules, 0)
	})

	t.Run("rules_omitted", func(t *testing.T) {
		cluster := mustCreateCluster(t)
		putTMRulesOK(t, map[string]interface{}{
			"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-004-pre2"), validTMCond, cluster)},
		})
		putTMRulesOK(t, map[string]interface{}{})

		getResp := getTMRules(t)
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(getResp.Data, &data))
		rules, ok := data["rules"].([]interface{})
		require.True(t, ok, "omitted rules must clear collection; rules should be [] not null")
		assert.Len(t, rules, 0)
	})
}

// ---------- TM-1-005 省略 vs 显式空对照（家族1 最高危变体，GET + Inner 导出双重断言） ----------

func TestTrafficMirrorRules_Update_RemoveHeadersOmitVsExplicitEmpty(t *testing.T) {
	cluster := mustCreateCluster(t)
	name := testutil.UniqueName("tm-1-005")
	base := func() map[string]interface{} {
		return tmRule(name, validTMCond, cluster)
	}

	// 轮次 1：显式黑名单 → GET 输出 / 导出原样。
	r1 := base()
	r1["remove_headers"] = []interface{}{"X-Custom-Secret"}
	putTMRulesOK(t, map[string]interface{}{"rules": []interface{}{r1}})
	rule := rulesOf(t, getTMRules(t))[0].(map[string]interface{})
	assertRuleFieldEquals(t, rule, "remove_headers", []interface{}{"X-Custom-Secret"})
	assert.ElementsMatch(t, []string{"X-Custom-Secret"}, exportedRemoveHeaders(t),
		"explicit blacklist must be exported as-is")

	// 轮次 2：省略 remove_headers → GET 键缺席；导出填默认黑名单（两层默认语义）。
	putTMRulesOK(t, map[string]interface{}{"rules": []interface{}{base()}})
	rule = rulesOf(t, getTMRules(t))[0].(map[string]interface{})
	assertRuleKeysExactly(t, rule, fixedRuleKeys)
	_, present := rule["remove_headers"]
	assert.False(t, present, "omitted remove_headers must be absent from response (not null, not [])")
	assert.ElementsMatch(t, defaultSensitiveHeaders, exportedRemoveHeaders(t),
		"omitted remove_headers must be filled with default sensitive-header blacklist at export")

	// 轮次 3：显式 [] → GET 输出空数组（键 present）；导出不剔除。
	r3 := base()
	r3["remove_headers"] = []interface{}{}
	putTMRulesOK(t, map[string]interface{}{"rules": []interface{}{r3}})
	resp := getTMRules(t)
	rule = rulesOf(t, resp)[0].(map[string]interface{})
	assertRuleFieldEquals(t, rule, "remove_headers", []interface{}{})
	assert.Contains(t, string(resp.Data), `"remove_headers":[]`,
		"explicit empty blacklist must be present as [] in response body")
	assert.Empty(t, exportedRemoveHeaders(t),
		"explicit empty blacklist must be exported as [] (no stripping)")
}

// ---------- TM-1-006 非法 cond（家族3：回读零变更） ----------

func TestTrafficMirrorRules_Update_InvalidCond(t *testing.T) {
	cluster := mustCreateCluster(t)
	keeper := testutil.UniqueName("tm-1-006-keeper")
	putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{tmRule(keeper, validTMCond, cluster)},
	})

	tests := []struct {
		name string
		cond string
	}{
		{"syntax_error", "not_a_valid_expr("},
		// req_path_prefix_in 必须两参数（合同示例形态），单参数必须 422。
		{"req_path_prefix_in_single_arg", `req_path_prefix_in("/v1/chat/completions")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := rulesSnapshot(t)

			resp := putTMRules(t, map[string]interface{}{
				"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-006-bad"), tt.cond, cluster)},
			})
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, "cond", "error message must be attributable to cond field")

			assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3)")
		})
	}
}

// ---------- TM-1-007 cond 缺省/空串（家族12 合同锁定：必填，防静默全匹配） ----------

func TestTrafficMirrorRules_Update_CondRequired(t *testing.T) {
	cluster := mustCreateCluster(t)
	keeper := testutil.UniqueName("tm-1-007-keeper")
	putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{tmRule(keeper, validTMCond, cluster)},
	})

	t.Run("cond_omitted", func(t *testing.T) {
		before := rulesSnapshot(t)
		rule := map[string]interface{}{
			"name":           testutil.UniqueName("tm-1-007-omit"),
			"mirror_cluster": cluster,
		}
		resp := putTMRules(t, map[string]interface{}{"rules": []interface{}{rule}})
		testutil.AssertErrCode(t, resp, 422)
		assert.Contains(t, resp.ErrMsg, "cond", "error message must be attributable to cond field")
		assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3)")
	})

	t.Run("cond_empty_string", func(t *testing.T) {
		before := rulesSnapshot(t)
		resp := putTMRules(t, map[string]interface{}{
			"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-007-empty"), "", cluster)},
		})
		testutil.AssertErrCode(t, resp, 422)
		assert.Contains(t, resp.ErrMsg, "cond")
		assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3)")
	})
}

// ---------- TM-1-008 cond 集合内重复（家族3/4） ----------

func TestTrafficMirrorRules_Update_DuplicateCond(t *testing.T) {
	cluster := mustCreateCluster(t)
	keeper := testutil.UniqueName("tm-1-008-keeper")
	putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{tmRule(keeper, validTMCond, cluster)},
	})
	before := rulesSnapshot(t)

	// 两条 default_t()（全匹配显式写法）cond 重复 → 422。
	resp := putTMRules(t, map[string]interface{}{
		"rules": []interface{}{
			tmRule(testutil.UniqueName("tm-1-008-dup1"), validTMCondAll, cluster),
			tmRule(testutil.UniqueName("tm-1-008-dup2"), validTMCondAll, cluster),
		},
	})
	testutil.AssertErrCode(t, resp, 422)
	assert.Contains(t, resp.ErrMsg, "duplicate", "error message should identify duplicate cond")
	assert.Contains(t, resp.ErrMsg, validTMCondAll)

	assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3)")
}

// ---------- TM-1-009 name 重名（家族3） ----------

func TestTrafficMirrorRules_Update_DuplicateName(t *testing.T) {
	cluster := mustCreateCluster(t)
	keeper := testutil.UniqueName("tm-1-009-keeper")
	putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{tmRule(keeper, validTMCond, cluster)},
	})
	before := rulesSnapshot(t)

	dup := testutil.UniqueName("tm-1-009-dup")
	resp := putTMRules(t, map[string]interface{}{
		"rules": []interface{}{
			tmRule(dup, validTMCond, cluster),
			tmRule(dup, validTMCondAll, cluster),
		},
	})
	testutil.AssertErrCode(t, resp, 422)
	assert.Contains(t, resp.ErrMsg, dup, "error message should identify the duplicated name")

	assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3)")
}

// ---------- TM-1-010 mirror_cluster 不存在（家族5：跨对象引用存在性） ----------

func TestTrafficMirrorRules_Update_MirrorClusterNotFound(t *testing.T) {
	cluster := mustCreateCluster(t)
	keeper := testutil.UniqueName("tm-1-010-keeper")
	putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{tmRule(keeper, validTMCond, cluster)},
	})
	before := rulesSnapshot(t)

	missing := testutil.UniqueClusterName()
	resp := putTMRules(t, map[string]interface{}{
		"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-010-bad"), validTMCond, missing)},
	})
	testutil.AssertErrCode(t, resp, 422)
	assert.Contains(t, resp.ErrMsg, missing,
		"error message must identify the missing mirror_cluster (家族5 可归因)")

	assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3/5)")
}

// ---------- TM-1-011 非法值矩阵（家族3/4，表驱动 + 混合集合假事务） ----------

func TestTrafficMirrorRules_Update_InvalidValueMatrix(t *testing.T) {
	cluster := mustCreateCluster(t)
	keeper := testutil.UniqueName("tm-1-011-keeper")
	putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{tmRule(keeper, validTMCond, cluster)},
	})

	longName := "n" + strings.Repeat("x", 128) // 恰好 129 字符（name 上限 128）

	tests := []struct {
		name string
		rule map[string]interface{}
	}{
		{"name_empty", map[string]interface{}{"name": "", "cond": validTMCond, "mirror_cluster": cluster}},
		{"name_too_long_129", map[string]interface{}{"name": longName, "cond": validTMCond, "mirror_cluster": cluster}},
		{"percentage_negative", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-pneg"), "cond": validTMCond,
			"mirror_cluster": cluster, "percentage": -1,
		}},
		{"percentage_over_100", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-p101"), "cond": validTMCond,
			"mirror_cluster": cluster, "percentage": 101,
		}},
		{"body_rewrite_path_not_model", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-brpath"), "cond": validTMCond,
			"mirror_cluster": cluster,
			"body_rewrites": []interface{}{map[string]interface{}{"path": "temperature", "value": "0.7"}},
		}},
		{"body_rewrite_value_empty", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-brval"), "cond": validTMCond,
			"mirror_cluster": cluster,
			"body_rewrites": []interface{}{map[string]interface{}{"path": "model", "value": ""}},
		}},
		{"body_rewrite_null_element", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-brnull"), "cond": validTMCond,
			"mirror_cluster": cluster,
			"body_rewrites": []interface{}{nil},
		}},
		{"path_rewrite_no_leading_slash", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-prw"), "cond": validTMCond,
			"mirror_cluster": cluster, "path_rewrite": "v1/internal/chat/completions",
		}},
		{"set_headers_empty_value", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-sh"), "cond": validTMCond,
			"mirror_cluster": cluster,
			"set_headers": map[string]interface{}{"X-A": ""},
		}},
		{"remove_headers_empty_element", map[string]interface{}{
			"name": testutil.UniqueName("tm-1-011-rh"), "cond": validTMCond,
			"mirror_cluster": cluster,
			"remove_headers": []interface{}{"Authorization", ""},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := rulesSnapshot(t)
			resp := putTMRules(t, map[string]interface{}{
				"rules": []interface{}{tt.rule},
			})
			testutil.AssertErrCode(t, resp, 422)
			assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3/4)")
		})
	}

	// rules 含 null 元素 → 422 + 回读零变更。
	t.Run("rules_null_element", func(t *testing.T) {
		before := rulesSnapshot(t)
		resp := putTMRules(t, map[string]interface{}{
			"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-011-null"), validTMCond, cluster), nil},
		})
		testutil.AssertErrCode(t, resp, 422)
		assert.Equal(t, before, rulesSnapshot(t), "rejected PUT must leave collection unchanged (家族3/4)")
	})

	// 混合集合假事务：2 合法 + 1 非法（cond 编译失败）→ 422 且全部未生效（家族3）。
	t.Run("mixed_valid_invalid", func(t *testing.T) {
		before := rulesSnapshot(t)
		resp := putTMRules(t, map[string]interface{}{
			"rules": []interface{}{
				tmRule(testutil.UniqueName("tm-1-011-ok1"), validTMCond, cluster),
				tmRule(testutil.UniqueName("tm-1-011-ok2"), validTMCond, cluster),
				tmRule(testutil.UniqueName("tm-1-011-bad"), "not_a_valid_expr(", cluster),
			},
		})
		testutil.AssertErrCode(t, resp, 422)
		assert.Equal(t, before, rulesSnapshot(t),
			"mixed valid+invalid PUT must not persist any rule (家族3 假事务)")
	})
}

// ---------- TM-1-012 边界正值（家族4：边界±1） ----------

func TestTrafficMirrorRules_Update_BoundaryValues(t *testing.T) {
	cluster := mustCreateCluster(t)

	// name 恰好 128 字符（上界）。
	name128 := "tm-1-012-" + strings.Repeat("b", 119) // 10 + 118 = 128
	require.Len(t, name128, 128)

	wants := []map[string]interface{}{
		{"name": name128, "cond": validTMCond, "mirror_cluster": cluster, "percentage": 0},
		{"name": testutil.UniqueName("tm-1-012-p100"), "cond": validTMCondAll, "mirror_cluster": cluster, "percentage": 100},
		{
			"name": testutil.UniqueName("tm-1-012-rewrite"), "cond": validTMCondB, "mirror_cluster": cluster,
			"percentage":    defaultTMPercentage,
			"path_rewrite":  "/v1/chat/completions",
			"body_rewrites": []interface{}{map[string]interface{}{"path": "model", "value": "shadow-v3"}},
		},
	}
	resp := putTMRulesOK(t, map[string]interface{}{
		"rules": []interface{}{wants[0], wants[1], wants[2]},
	})

	rules := rulesOf(t, resp)
	require.Len(t, rules, 3)
	for i, want := range wants {
		rule, ok := rules[i].(map[string]interface{})
		require.True(t, ok, "rules[%d] should be object", i)
		for k, v := range want {
			assertRuleFieldEquals(t, rule, k, v)
		}
		assertRuleFixedKeys(t, rule)
	}
}

// ---------- TM-1-013 合同锁定：响应键集合精确（家族11/12 / #201） ----------

func TestTrafficMirrorRules_Update_ContractLockKeySets(t *testing.T) {
	cluster := mustCreateCluster(t)

	t.Run("full_params_exactly_10_keys", func(t *testing.T) {
		rule := tmRule(testutil.UniqueName("tm-1-013-full"), validTMCondModel, cluster)
		rule["percentage"] = 10
		rule["remove_headers"] = []interface{}{"Authorization"}
		rule["set_headers"] = map[string]interface{}{"X-A": "1"}
		rule["body_rewrites"] = []interface{}{map[string]interface{}{"path": "model", "value": "m2"}}
		rule["path_rewrite"] = "/v1/internal/x"
		resp := putTMRulesOK(t, map[string]interface{}{"rules": []interface{}{rule}})

		rules := rulesOf(t, resp)
		require.Len(t, rules, 1)
		assertRuleKeysExactly(t, rules[0].(map[string]interface{}), fullRuleKeys)
	})

	t.Run("minimal_params_exactly_6_keys", func(t *testing.T) {
		resp := putTMRulesOK(t, map[string]interface{}{
			"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-013-min"), validTMCond, cluster)},
		})
		rules := rulesOf(t, resp)
		require.Len(t, rules, 1)
		assertRuleKeysExactly(t, rules[0].(map[string]interface{}), fixedRuleKeys)
	})
}

// ---------- TM-1-014 操作审计（家族7 / #201 / #155） ----------

func TestTrafficMirrorRules_Update_OperationLog(t *testing.T) {
	t.Run("success_audit", func(t *testing.T) {
		cluster := mustCreateCluster(t)
		name := testutil.UniqueName("tm-1-010-ok")
		startTs := time.Now().Add(-2 * time.Second).Unix()

		putTMRulesOK(t, map[string]interface{}{
			"rules": []interface{}{tmRule(name, validTMCond, cluster)},
		})

		// 等到一条 after 快照包含本用例规则名的成功审计。
		entry := waitForTMAudit(t, startTs, "1", func(e *testutil.OperationLogEntry) bool {
			after, ok := e.ChangeSummary["after"].(map[string]interface{})
			if !ok {
				return false
			}
			raw, _ := json.Marshal(after)
			return strings.Contains(string(raw), name)
		})
		require.NotNil(t, entry)

		assert.Equal(t, "update", entry.Action)
		assert.Equal(t, "traffic_mirror_rule", entry.ResourceType)
		// 集合级资源身份固定，不依赖请求体。
		assert.Equal(t, trafficMirrorAuditResourceID, entry.ResourceID)
		assert.Equal(t, trafficMirrorAuditResourceID, entry.ResourceName)
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
		assert.ElementsMatch(t, fixedRuleKeys, keysOf(rule0),
			"audit snapshot keys must use lowercase API vocabulary without id")

		// 快照 JSON 序列化后不含内部 "id" 键。
		raw, err := json.Marshal(cs)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"id"`, "audit snapshot must not leak internal id")
	})

	t.Run("failure_audit", func(t *testing.T) {
		cluster := mustCreateCluster(t)
		startTs := time.Now().Unix()

		// PUT 422（非法 cond）：合同要求同样记录失败审计（家族7/#155）。
		resp := putTMRules(t, map[string]interface{}{
			"rules": []interface{}{tmRule(testutil.UniqueName("tm-1-010-bad"), "not_a_valid_expr(", cluster)},
		})
		testutil.AssertErrCode(t, resp, 422)

		entry := waitForTMAudit(t, startTs, "2", func(e *testutil.OperationLogEntry) bool {
			return e.ResourceType == "traffic_mirror_rule" && e.Action == "update"
		})
		require.NotNil(t, entry)

		assert.Equal(t, float64(2), entry.Status, "failed PUT must be audited with failed status")
		// 身份取自服务端固定资源标识，不依赖请求体（#155）。
		assert.Equal(t, trafficMirrorAuditResourceID, entry.ResourceID)
		assert.Equal(t, trafficMirrorAuditResourceID, entry.ResourceName)
		assert.NotEmpty(t, entry.ErrorMsg, "failed audit should carry error reason")
	})
}
