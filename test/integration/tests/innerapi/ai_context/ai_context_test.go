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
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	aiContextRulesPath    = "/open-api/v1/ai-context-rules"
	aiContextSettingsPath = "/open-api/v1/ai-context-settings"
	aiContextExportPath   = "/inner-api/v1/configs/ai-context-rule"
	validContextCond      = `req_path_in("/v1/chat/completions", false)`

	// productKey 为测试环境 AIRouteInnerProductName 配置值（合同默认 AI_product）。
	productKey = "AI_product"
)

// defaultsKeys 为导出 Defaults 块的 7 个顶层冻结 tag（camelCase，与 Open API
// 小写下划线词汇不同；rewrite 子对象另含 2 个冻结 tag）。
var defaultsKeys = []string{
	"triggerRatio", "keepLatestImages", "toolResultMaxChars",
	"thinkingPolicy", "charsPerToken", "imageTokenEstimate", "rewrite",
}

// exportRuleKeys 为 Inner 导出规则的 4 个冻结 tag（camelCase）。
var exportRuleKeys = []string{"cond", "mode", "maxContextTokens", "reserveTokens"}

// phaseTwoFields 为一期不得导出的二期字段（合同锁定：规则级 override、
// Defaults 内 summary；BFE 未知字段忽略并计数告警，导出侧一期不出现在产物中）。
var phaseTwoFields = []string{"override", "summary"}

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

type contextExportData struct {
	Version  string                              `json:"Version"`
	Defaults map[string]interface{}              `json:"Defaults"`
	Config   map[string][]map[string]interface{} `json:"Config"`
}

// exportDefaultsConf 为导出 Defaults 块的强类型视图（camelCase tag 逐字段冻结）。
type exportDefaultsConf struct {
	TriggerRatio       *float64           `json:"triggerRatio"`
	KeepLatestImages   *int               `json:"keepLatestImages"`
	ToolResultMaxChars *int               `json:"toolResultMaxChars"`
	ThinkingPolicy     *string            `json:"thinkingPolicy"`
	CharsPerToken      *int               `json:"charsPerToken"`
	ImageTokenEstimate *int               `json:"imageTokenEstimate"`
	Rewrite            *exportRewriteConf `json:"rewrite"`
}

type exportRewriteConf struct {
	Strength              *string  `json:"strength"`
	ProtectedSurvivalRate *float64 `json:"protectedSurvivalRate"`
}

// assertDefaultsDefaults 断言 Defaults 块恰含 7 顶层键且取文档默认值
// （0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95）。
func assertDefaultsDefaults(t *testing.T, defaults map[string]interface{}) {
	t.Helper()
	require.NotNil(t, defaults, "Defaults block must always be exported")
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, defaultsKeys, keys, "Defaults block must carry exactly the 7 frozen top-level tags")

	assert.InDelta(t, 0.7, defaults["triggerRatio"], 1e-9)
	assert.Equal(t, float64(2), defaults["keepLatestImages"])
	assert.Equal(t, float64(2000), defaults["toolResultMaxChars"])
	assert.Equal(t, "trim-all-but-last", defaults["thinkingPolicy"])
	assert.Equal(t, float64(4), defaults["charsPerToken"])
	assert.Equal(t, float64(1200), defaults["imageTokenEstimate"])

	rewrite, ok := defaults["rewrite"].(map[string]interface{})
	require.True(t, ok, "rewrite must be an object")
	assert.ElementsMatch(t, []string{"strength", "protectedSurvivalRate"}, keysOf(rewrite))
	assert.Equal(t, "lite", rewrite["strength"])
	assert.InDelta(t, 0.95, rewrite["protectedSurvivalRate"], 1e-9)
}

func putRules(t *testing.T, rules []interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(aiContextRulesPath, map[string]interface{}{"rules": rules})
	require.NoError(t, err)
	return resp
}

func putRulesOK(t *testing.T, rules []interface{}) {
	t.Helper()
	resp := putRules(t, rules)
	testutil.AssertSuccess(t, resp)
}

func putSettingsOK(t *testing.T, body map[string]interface{}) {
	t.Helper()
	resp, err := testutil.GetClient().Put(aiContextSettingsPath, body)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
}

// exportContext 拉取导出；version 为空表示首拉。返回原始响应（Data 可能为 null）。
func exportContext(t *testing.T, version string) *testutil.APIResponse {
	t.Helper()
	var resp *testutil.APIResponse
	var err error
	if version == "" {
		resp, err = testutil.GetClient().Get(aiContextExportPath)
	} else {
		resp, err = testutil.GetClient().Get(aiContextExportPath, map[string]string{"version": version})
	}
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// exportContextData 首拉导出（Data 必须非 null）并解析。
func exportContextData(t *testing.T) *contextExportData {
	t.Helper()
	resp := exportContext(t, "")
	testutil.AssertDataNotEmpty(t, resp)
	var data contextExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)
	_, ok := data.Config[productKey]
	require.True(t, ok, "Config must contain product key %s", productKey)
	return &data
}

func productRules(t *testing.T, data *contextExportData) []map[string]interface{} {
	t.Helper()
	rules, ok := data.Config[productKey]
	require.True(t, ok, "product key %s must be present", productKey)
	return rules
}

// assertExportRuleValues 断言导出规则 4 tag 精确集合与取值。
func assertExportRuleValues(t *testing.T, rule map[string]interface{}, want map[string]interface{}) {
	t.Helper()
	assert.ElementsMatch(t, exportRuleKeys, keysOf(rule),
		"exported rule must carry exactly the 4 contract tags")
	for k, v := range want {
		assert.Equal(t, v, rule[k], "export tag %s", k)
	}
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ---------- CTXE-1-001 首拉（家族8：Defaults 恒导出 + 冻结 tag 逐字 + 文本形态） ----------

// TestInnerAPI_AIContext_ExportFirstPull 验证初始导出：Version 非空、Defaults 块恒导出
// 且 8 个冻结 tag（7 顶层 + rewrite 2）逐字断言、默认值正确、Config 含产品键。
// 注意：本用例依赖包内源码顺序最先执行（其后用例才写入自定义设置/规则）。
func TestInnerAPI_AIContext_ExportFirstPull(t *testing.T) {
	putRulesOK(t, []interface{}{})

	resp := exportContext(t, "")
	testutil.AssertSuccess(t, resp)

	var data contextExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version, "Version must be non-empty")

	// Defaults 块：7 顶层键精确集合 + 8 项默认值（含 rewrite 子对象）。
	assertDefaultsDefaults(t, data.Defaults)

	// Config 含产品键；初始（规则已清空）为空数组而非 null。
	rules, ok := data.Config[productKey]
	require.True(t, ok, "Config must contain product key %s", productKey)
	require.NotNil(t, rules, "empty rule set must export [] not null")
	assert.Len(t, rules, 0)

	// 原始响应 body 文本形态断言（#102：仅反序列化值比较会静默放过序列化错误）：
	// 8 个冻结 tag 逐字（camelCase）。
	body := string(resp.RawBody)
	assert.Contains(t, body, `"Defaults":{`)
	assert.Contains(t, body, `"triggerRatio":0.7`)
	assert.Contains(t, body, `"keepLatestImages":2`)
	assert.Contains(t, body, `"toolResultMaxChars":2000`)
	assert.Contains(t, body, `"thinkingPolicy":"trim-all-but-last"`)
	assert.Contains(t, body, `"charsPerToken":4`)
	assert.Contains(t, body, `"imageTokenEstimate":1200`)
	assert.Contains(t, body, `"rewrite":{"strength":"lite","protectedSurvivalRate":0.95}`)
	assert.Contains(t, body, `"Version":`)
	assert.Contains(t, body, `"Config":{`)

	// 二期字段缺席（合同锁定）：override/summary 不得出现在导出 JSON 任意位置。
	for _, field := range phaseTwoFields {
		assert.NotContains(t, body, field, "phase-2 field %s must not appear in export (家族8 合同锁定)", field)
	}
}

// ---------- CTXE-1-002 PUT rules → 导出规则数组顺序与 4 冻结 tag ----------

func TestInnerAPI_AIContext_ExportRules(t *testing.T) {
	putRulesOK(t, []interface{}{
		map[string]interface{}{
			"cond": validContextCond, "mode": "balanced",
			"max_context_tokens": 64000, "reserve_tokens": 8192,
		},
		map[string]interface{}{"cond": "default_t()", "mode": "off"},
	})

	resp := exportContext(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	var data contextExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))

	rules := productRules(t, &data)
	require.Len(t, rules, 2, "Config.%s length must equal submitted collection", productKey)

	// 顺序=提交顺序（first-match-wins 优先级）。
	assertExportRuleValues(t, rules[0], map[string]interface{}{
		"cond": validContextCond, "mode": "balanced",
		"maxContextTokens": float64(64000), "reserveTokens": float64(8192),
	})
	assertExportRuleValues(t, rules[1], map[string]interface{}{
		"cond": "default_t()", "mode": "off",
		"maxContextTokens": float64(0), "reserveTokens": float64(0),
	})

	// 原始 body 文本形态：4 个规则冻结 tag（camelCase）逐字。
	// 注意 cond 值在 JSON 中带转义引号（\"），按原始字节断言。
	body := string(resp.RawBody)
	assert.Contains(t, body, `"cond":"req_path_in(\"/v1/chat/completions\", false)"`)
	assert.Contains(t, body, `"mode":"balanced"`)
	assert.Contains(t, body, `"maxContextTokens":64000`)
	assert.Contains(t, body, `"reserveTokens":8192`)
	assert.Contains(t, body, `"mode":"off"`)
}

// ---------- CTXE-1-003 PUT settings 改值 → 版本流推进 + Defaults 反映新值 ----------

func TestInnerAPI_AIContext_ExportSettingsChangeBumpsVersion(t *testing.T) {
	putRulesOK(t, []interface{}{
		map[string]interface{}{"cond": validContextCond, "mode": "balanced"},
	})
	v1 := exportContextData(t).Version
	beforeRules := productRules(t, exportContextData(t))

	// PUT 全局设置（不动 rules）→ 全量生成内容签名变化 → 导出版本推进。
	putSettingsOK(t, map[string]interface{}{
		"trigger_ratio":         0.8,
		"keep_latest_images":    4,
		"tool_result_max_chars": 4000,
		"thinking_policy":       "keep",
		"chars_per_token":       3,
		"image_token_estimate":  800,
		"rewrite":               map[string]interface{}{"strength": "full", "protected_survival_rate": 0.9},
	})

	// 轮询导出（间隔 500ms，最多 10 次）直到版本严格大于 v1（同秒碰撞防护，#142 模式）。
	var converged *contextExportData
	for i := 0; i < 10; i++ {
		data := exportContextData(t)
		if data.Version > v1 {
			converged = data
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotNil(t, converged, "settings change must bump export version within bounded polls")

	// Defaults 块反映新值（camelCase），规则内容不受影响（两者独立维护）。
	var defaults exportDefaultsConf
	raw, err := json.Marshal(converged.Defaults)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &defaults))
	assert.InDelta(t, 0.8, *defaults.TriggerRatio, 1e-9)
	assert.Equal(t, 4, *defaults.KeepLatestImages)
	assert.Equal(t, 4000, *defaults.ToolResultMaxChars)
	assert.Equal(t, "keep", *defaults.ThinkingPolicy)
	assert.Equal(t, 3, *defaults.CharsPerToken)
	assert.Equal(t, 800, *defaults.ImageTokenEstimate)
	require.NotNil(t, defaults.Rewrite)
	assert.Equal(t, "full", *defaults.Rewrite.Strength)
	assert.InDelta(t, 0.9, *defaults.Rewrite.ProtectedSurvivalRate, 1e-9)

	assert.Equal(t, beforeRules, productRules(t, converged), "rules must be untouched by settings PUT")

	// 原始 body 文本形态锁定新值序列化（#102）。
	again := exportContext(t, "")
	var againData contextExportData
	require.NoError(t, json.Unmarshal(again.Data, &againData))
	assert.Equal(t, converged.Version, againData.Version, "unchanged content must keep the converged version")
	body := string(again.RawBody)
	assert.Contains(t, body, `"triggerRatio":0.8`)
	assert.Contains(t, body, `"keepLatestImages":4`)
	assert.Contains(t, body, `"thinkingPolicy":"keep"`)
	assert.Contains(t, body, `"rewrite":{"strength":"full","protectedSurvivalRate":0.9}`)
}

// ---------- CTXE-1-004 清空 rules → Defaults 块仍在（设置独立于规则生命周期） ----------

func TestInnerAPI_AIContext_ExportSurvivesRuleClear(t *testing.T) {
	putRulesOK(t, []interface{}{
		map[string]interface{}{"cond": validContextCond, "mode": "balanced"},
		map[string]interface{}{"cond": "default_t()", "mode": "off"},
	})
	putRulesOK(t, []interface{}{})

	resp := exportContext(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	var data contextExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)

	// Config 为空数组（product 键 present，非 null）。
	rules, ok := data.Config[productKey]
	require.True(t, ok, "product key %s must stay present for empty collection", productKey)
	require.NotNil(t, rules, "empty collection must export [] not null")
	assert.Len(t, rules, 0)

	// Defaults 块仍导出，且与当前 Open API 设置读数一致（规则清空不影响设置）。
	settingsResp, err := testutil.GetClient().Get(aiContextSettingsPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, settingsResp)
	var settings map[string]interface{}
	require.NoError(t, json.Unmarshal(settingsResp.Data, &settings))

	assert.InDelta(t, settings["trigger_ratio"].(float64), data.Defaults["triggerRatio"], 1e-9)
	assert.Equal(t, settings["keep_latest_images"].(float64), data.Defaults["keepLatestImages"])
	assert.Equal(t, settings["tool_result_max_chars"].(float64), data.Defaults["toolResultMaxChars"])
	assert.Equal(t, settings["thinking_policy"].(string), data.Defaults["thinkingPolicy"])
	assert.Equal(t, settings["chars_per_token"].(float64), data.Defaults["charsPerToken"])
	assert.Equal(t, settings["image_token_estimate"].(float64), data.Defaults["imageTokenEstimate"])
	settingsRewrite := settings["rewrite"].(map[string]interface{})
	defaultsRewrite := data.Defaults["rewrite"].(map[string]interface{})
	assert.Equal(t, settingsRewrite["strength"], defaultsRewrite["strength"])
	assert.InDelta(t, settingsRewrite["protected_survival_rate"].(float64), defaultsRewrite["protectedSurvivalRate"], 1e-9)

	// 原始 body 文本：Defaults 键在、Config 为空数组。
	body := string(resp.RawBody)
	assert.Contains(t, body, `"Defaults":{`)
	assert.Contains(t, body, `"`+productKey+`":[]`)
}

// ---------- CTXE-1-005 增量同步 ----------

func TestInnerAPI_AIContext_Incremental(t *testing.T) {
	putRulesOK(t, []interface{}{
		map[string]interface{}{"cond": validContextCond, "mode": "conservative"},
	})

	first := exportContextData(t)
	v1 := first.Version

	// 携带刚返回的 version 再拉 → Data 为 null（未变化）。
	incrResp := exportContext(t, v1)
	testutil.AssertDataNull(t, incrResp)

	// 不传 version 再拉 → 有数据且 Version 相同（内容未变则版本不变）。
	again := exportContextData(t)
	assert.Equal(t, v1, again.Version, "unchanged content must keep the same version")
	assert.Equal(t, productRules(t, first), productRules(t, again), "export content must be stable")
}

// ---------- CTXE-1-006 422 防泄漏（家族4/#172：被拒配置不得出现在导出产物） ----------

func TestInnerAPI_AIContext_RejectedPutNoLeak(t *testing.T) {
	putRulesOK(t, []interface{}{
		map[string]interface{}{"cond": validContextCond, "mode": "balanced"},
	})
	before := exportContextData(t)
	v1 := before.Version

	// 同一 PUT：1 条携带特征串的合法规则 + 1 条 mode 非法 → 整体 422。
	probe := `req_path_in("/v1/leak_probe_ctx_forbidden", false)`
	resp := putRules(t, []interface{}{
		map[string]interface{}{"cond": probe, "mode": "balanced"},
		map[string]interface{}{"cond": "default_t()", "mode": "hyper"},
	})
	testutil.AssertErrCode(t, resp, 422)

	// 断言 1：被拒集合（含特征串规则）不得出现在导出产物原始 body 中（#172）。
	leakResp := exportContext(t, "")
	testutil.AssertDataNotEmpty(t, leakResp)
	assert.NotContains(t, string(leakResp.RawBody), "leak_probe_ctx_forbidden",
		"rejected rules must not leak into the export payload (家族4/#172)")
	assert.NotContains(t, string(leakResp.RawBody), probe,
		"rejected rules must not leak into the export payload (家族4/#172)")

	// 断言 2：版本未推进（带 v1 增量拉取返回 Data=null）且内容与拒绝前一致。
	incrResp := exportContext(t, v1)
	testutil.AssertDataNull(t, incrResp)
	after := exportContextData(t)
	assert.Equal(t, v1, after.Version, "rejected PUT must not bump export version (家族4)")
	assert.Equal(t, productRules(t, before), productRules(t, after),
		"rejected PUT must not leak into export content (家族4)")
}

// ---------- CTXE-1-007 rules PUT 版本流（家族8/#142：同秒碰撞防护 + Defaults 不受影响） ----------

func TestInnerAPI_AIContext_RulesChangeBumpsVersion(t *testing.T) {
	putRulesOK(t, []interface{}{
		map[string]interface{}{"cond": "default_t()", "mode": "off"},
	})
	v1 := exportContextData(t).Version
	beforeDefaults := exportContextData(t).Defaults

	// 同一秒内 PUT rules（cond 特征值变更，不动 settings）→ 全量生成内容签名变化。
	probeCond := `req_path_in("/v1/version_flow_probe", false)`
	putRulesOK(t, []interface{}{
		map[string]interface{}{"cond": probeCond, "mode": "balanced"},
	})

	// 轮询导出（间隔 500ms，最多 10 次）直到版本严格大于 v1（同秒碰撞防护，#142 模式）。
	var converged *contextExportData
	for i := 0; i < 10; i++ {
		data := exportContextData(t)
		if data.Version > v1 {
			converged = data
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotNil(t, converged, "rules change must bump export version within bounded polls (家族8/#142)")

	// 内容收敛为最新集合。
	rules := productRules(t, converged)
	require.Len(t, rules, 1)
	assertExportRuleValues(t, rules[0], map[string]interface{}{
		"cond": probeCond, "mode": "balanced",
		"maxContextTokens": float64(0), "reserveTokens": float64(0),
	})

	// Defaults 块不受 rules 变更影响（两者独立维护，共享同一条版本流）。
	assert.Equal(t, beforeDefaults, converged.Defaults, "Defaults must be untouched by rules PUT")

	// 收敛后版本稳定；携带新 version 增量拉取 → Data=null（CTXE-1-005 语义不回归）。
	again := exportContextData(t)
	assert.Equal(t, converged.Version, again.Version, "unchanged content must keep the converged version")
	incrResp := exportContext(t, converged.Version)
	testutil.AssertDataNull(t, incrResp)
}
