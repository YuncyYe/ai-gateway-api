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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	aiCacheOpenPath             = "/open-api/v1/ai-cache-rules"
	aiCacheSemanticSettingsPath = "/open-api/v1/ai-cache-semantic-settings"
	aiCacheExportPath           = "/inner-api/v1/configs/ai-cache-rule"
	validAICacheCond            = `req_path_in("/v1/chat/completions", false)`

	// productKey 为测试环境 AIRouteInnerProductName 配置值（合同默认 AI_product）。
	productKey = "AI_product"
)

// exportRuleKeys 为 Inner 导出规则的 6 个 tag（大写驼峰，与 Open API 词汇不同；
// 二期新增 enableSemanticCache）。
var exportRuleKeys = []string{
	"cond", "cacheKeyStrategy", "cacheTTL", "maxBodyBytes", "maxValueBytes",
	"enableSemanticCache",
}

// phaseTwoFields 为一期不得导出的二期/预留字段（合同锁定）。
var phaseTwoFields = []string{
	"cacheKeyFrom", "cacheValueFrom", "cacheStreamValueFrom",
	"cacheToolCallsFrom", "responseTemplate", "streamResponseTemplate",
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

type aiCacheExportData struct {
	Config   map[string][]map[string]interface{} `json:"Config"`
	Semantic *exportSemanticConf                 `json:"Semantic"`
	Version  string                              `json:"Version"`
}

// exportSemanticConf 为导出顶层 Semantic 块（camelCase，BFE SemanticConfFile
// 逐字段冻结；恒导出，空设置表时用默认值填充）。
type exportSemanticConf struct {
	TopK              *int     `json:"topK"`
	Threshold         *float64 `json:"threshold"`
	ThresholdRelation *string  `json:"thresholdRelation"`
}

// assertSemanticDefaults 断言 Semantic 块恰含 3 键且取文档默认值。
func assertSemanticDefaults(t *testing.T, semantic *exportSemanticConf) {
	t.Helper()
	require.NotNil(t, semantic, "Semantic block must always be exported")
	assert.Equal(t, 1, *semantic.TopK)
	assert.InDelta(t, 0.15, *semantic.Threshold, 1e-9)
	assert.Equal(t, "lt", *semantic.ThresholdRelation)
}

func putAICacheRules(t *testing.T, rules []interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(aiCacheOpenPath, map[string]interface{}{"rules": rules})
	require.NoError(t, err)
	return resp
}

func putAICacheRulesOK(t *testing.T, rules []interface{}) {
	t.Helper()
	resp := putAICacheRules(t, rules)
	testutil.AssertSuccess(t, resp)
}

// exportAICache 拉取导出；version 为空表示首拉。返回原始响应（Data 可能为 null）。
func exportAICache(t *testing.T, version string) *testutil.APIResponse {
	t.Helper()
	var resp *testutil.APIResponse
	var err error
	if version == "" {
		resp, err = testutil.GetClient().Get(aiCacheExportPath)
	} else {
		resp, err = testutil.GetClient().Get(aiCacheExportPath, map[string]string{"version": version})
	}
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// exportAICacheData 首拉导出（Data 必须非 null）并解析。
func exportAICacheData(t *testing.T) *aiCacheExportData {
	t.Helper()
	resp := exportAICache(t, "")
	testutil.AssertDataNotEmpty(t, resp)
	var data aiCacheExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)
	_, ok := data.Config[productKey]
	require.True(t, ok, "Config must contain product key %s", productKey)
	return &data
}

func productRules(t *testing.T, data *aiCacheExportData) []map[string]interface{} {
	t.Helper()
	rules, ok := data.Config[productKey]
	require.True(t, ok, "product key %s must be present", productKey)
	return rules
}

// assertExportRuleValues 断言导出规则 6 tag 精确集合与取值。
func assertExportRuleValues(t *testing.T, rule map[string]interface{}, want map[string]interface{}) {
	t.Helper()
	keys := make([]string, 0, len(rule))
	for k := range rule {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, exportRuleKeys, keys,
		"exported rule must carry exactly the 6 contract tags")
	for k, v := range want {
		assert.Equal(t, v, rule[k], "export tag %s", k)
	}
}

// aiCacheCondFor 在基准表达式上追加一个只用于区分规则身份的 model 子句：
// 导出的 cond 必须逐条不同，否则整个 ai_cache.data 会被 BFE 拒载（issue #261）。
func aiCacheCondFor(suffix string) string {
	return fmt.Sprintf(`req_path_in("/v1/chat/completions", false) && req_body_json_in("model", %q, false)`, suffix)
}

// ---------- AICE-1-001 首拉（家族8：存在性 + 取值 + 原始 body 文本形态） ----------

func TestInnerAPI_AICache_ExportFirstPull(t *testing.T) {
	n1 := testutil.UniqueName("aice-1-001-defaults")
	n2 := testutil.UniqueName("aice-1-001-explicit")
	putAICacheRulesOK(t, []interface{}{
		// 规则 1：仅 name+cond，依赖默认值回填。
		map[string]interface{}{"name": n1, "cond": aiCacheCondFor(n1)},
		// 规则 2：显式非默认值（含语义开关 true）。
		map[string]interface{}{
			"name": n2, "cond": aiCacheCondFor(n2),
			"cache_key_strategy": "disabled", "cache_ttl": 86400,
			"max_body_bytes": 524288, "max_value_bytes": 262144,
			"enable_semantic_cache": true,
		},
	})

	resp := exportAICache(t, "")
	testutil.AssertSuccess(t, resp)

	var data aiCacheExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version, "Version must be non-empty")

	rules, ok := data.Config[productKey]
	require.True(t, ok, "Config must contain product key %s", productKey)
	require.Len(t, rules, 2, "Config.%s length must equal submitted collection", productKey)

	// 顺序=提交顺序（first-match-wins 优先级）。
	assertExportRuleValues(t, rules[0], map[string]interface{}{
		"cond": aiCacheCondFor(n1), "cacheKeyStrategy": "lastQuestion",
		"cacheTTL": float64(0), "maxBodyBytes": float64(1048576), "maxValueBytes": float64(1048576),
		"enableSemanticCache": false,
	})
	assertExportRuleValues(t, rules[1], map[string]interface{}{
		"cond": aiCacheCondFor(n2), "cacheKeyStrategy": "disabled",
		"cacheTTL": float64(86400), "maxBodyBytes": float64(524288), "maxValueBytes": float64(262144),
		"enableSemanticCache": true,
	})

	// 原始响应 body 文本形态断言（家族8/#102：仅反序列化值比较会静默放过序列化错误）。
	body := string(resp.RawBody)
	assert.Contains(t, body, `"cacheKeyStrategy":"lastQuestion"`)
	assert.Contains(t, body, `"cacheTTL":0`)
	assert.Contains(t, body, `"maxBodyBytes":1048576`)
	assert.Contains(t, body, `"maxValueBytes":1048576`)
	assert.Contains(t, body, `"cacheKeyStrategy":"disabled"`)
	assert.Contains(t, body, `"cacheTTL":86400`)
	assert.Contains(t, body, `"enableSemanticCache":true`)
	assert.Contains(t, body, `"enableSemanticCache":false`)
}

// ---------- AICE-1-002 二期字段缺席（家族8 合同锁定） ----------

func TestInnerAPI_AICache_PhaseTwoFieldsAbsent(t *testing.T) {
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{
			"name": testutil.UniqueName("aice-1-002"), "cond": validAICacheCond,
			"cache_ttl": 3600,
		},
	})

	resp := exportAICache(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	// 断言作用于原始 body 文本，覆盖 JSON 任意嵌套位置。
	body := string(resp.RawBody)
	for _, field := range phaseTwoFields {
		assert.NotContains(t, body, field, "phase-2 field %s must not appear in export (家族8 合同锁定)", field)
	}
}

// ---------- AICE-1-003 增量同步 ----------

func TestInnerAPI_AICache_Incremental(t *testing.T) {
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("aice-1-003"), "cond": validAICacheCond},
	})

	first := exportAICacheData(t)
	v1 := first.Version

	// 携带刚返回的 version 再拉 → Data 为 null（未变化）。
	incrResp := exportAICache(t, v1)
	testutil.AssertDataNull(t, incrResp)

	// 不传 version 再拉 → 有数据且 Version 相同（内容未变则版本不变）。
	again := exportAICacheData(t)
	assert.Equal(t, v1, again.Version, "unchanged content must keep the same version")
	assert.Equal(t, productRules(t, first), productRules(t, again), "export content must be stable")
}

// ---------- AICE-1-004 版本单调（家族8/#142：同秒连续变更版本严格递增且收敛） ----------

func TestInnerAPI_AICache_VersionMonotonic(t *testing.T) {
	nA := testutil.UniqueName("aice-1-004-a")
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": nA, "cond": validAICacheCond, "cache_ttl": 111},
	})
	v1 := exportAICacheData(t).Version

	// 同一秒内第二次变更。
	nB1 := testutil.UniqueName("aice-1-004-b1")
	nB2 := testutil.UniqueName("aice-1-004-b2")
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": nB1, "cond": aiCacheCondFor(nB1), "cache_ttl": 222},
		map[string]interface{}{"name": nB2, "cond": aiCacheCondFor(nB2), "cache_ttl": 333},
	})

	// 轮询导出（间隔 500ms，最多 10 次）直到版本严格大于 v1（版本串为 14 位定宽，字典序即时间序）。
	var converged *aiCacheExportData
	for i := 0; i < 10; i++ {
		data := exportAICacheData(t)
		if data.Version > v1 {
			converged = data
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotNil(t, converged, "version must strictly increase within bounded polls (家族8/#142)")

	// 内容收敛为最新集合（导出无 name 字段，以逐条唯一的 cond 锁定顺序）。
	rules := productRules(t, converged)
	require.Len(t, rules, 2)
	assertExportRuleValues(t, rules[0], map[string]interface{}{
		"cond": aiCacheCondFor(nB1), "cacheKeyStrategy": "lastQuestion",
		"cacheTTL": float64(222), "maxBodyBytes": float64(1048576), "maxValueBytes": float64(1048576),
		"enableSemanticCache": false,
	})
	assertExportRuleValues(t, rules[1], map[string]interface{}{
		"cond": aiCacheCondFor(nB2), "cacheKeyStrategy": "lastQuestion",
		"cacheTTL": float64(333), "maxBodyBytes": float64(1048576), "maxValueBytes": float64(1048576),
		"enableSemanticCache": false,
	})
}

// ---------- AICE-1-005 清空导出（家族8：空数组非 null，product 键 present） ----------

func TestInnerAPI_AICache_ExportEmpty(t *testing.T) {
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("aice-1-005-a"), "cond": aiCacheCondFor("aice-1-005-a")},
		map[string]interface{}{"name": testutil.UniqueName("aice-1-005-b"), "cond": aiCacheCondFor("aice-1-005-b")},
	})
	putAICacheRulesOK(t, []interface{}{})

	resp := exportAICache(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	var data aiCacheExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)

	// product 键 present 且为 []（非 null）。
	rules, ok := data.Config[productKey]
	require.True(t, ok, "product key %s must stay present for empty collection", productKey)
	require.NotNil(t, rules, "empty collection must export [] not null")
	assert.Len(t, rules, 0)
	assert.Contains(t, string(resp.RawBody), `"`+productKey+`":[]`)
}

// ---------- AICE-1-006 422 防泄漏（家族4：拒绝后导出完全一致、Version 不变） ----------

func TestInnerAPI_AICache_RejectedPutNoLeak(t *testing.T) {
	keeper := testutil.UniqueName("aice-1-006-keeper")
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": keeper, "cond": validAICacheCond, "cache_ttl": 777},
	})
	before := exportAICacheData(t)
	v1 := before.Version
	beforeRules := productRules(t, before)

	// PUT 被拒（非法 cond）。
	resp := putAICacheRules(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("aice-1-006-bad"), "cond": "not_a_valid_expr("},
	})
	testutil.AssertErrCode(t, resp, 422)

	// 带 v1 增量拉取 → Data null（版本未推进）。
	incrResp := exportAICache(t, v1)
	testutil.AssertDataNull(t, incrResp)

	// 首拉 → Version 不变且内容与拒绝前完全一致。
	after := exportAICacheData(t)
	assert.Equal(t, v1, after.Version, "rejected PUT must not bump export version (家族4)")
	assert.Equal(t, beforeRules, productRules(t, after),
		"rejected PUT must not leak into export content (家族4)")
}

// ---------- helpers（二期：语义全局设置） ----------

// putSemanticSettings 提交语义设置；wantCode 非 200 时返回原始响应不做成功断言。
func putSemanticSettings(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(aiCacheSemanticSettingsPath, body)
	require.NoError(t, err)
	return resp
}

func putSemanticSettingsOK(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp := putSemanticSettings(t, body)
	testutil.AssertSuccess(t, resp)
	return resp
}

// ---------- AICE-1-007 顶层 Semantic 块默认值（家族8：恒导出 + 空表默认） ----------

// TestInnerAPI_AICache_ExportSemanticDefaults 验证设置表为空时导出仍恒含顶层
// Semantic 块，且取文档默认值（topK=1 / threshold=0.15 / thresholdRelation=lt，
// camelCase tag 逐字冻结）。
// 注意：本用例依赖包内源码顺序先于 AICE-1-008 执行（该用例才写入自定义设置）。
func TestInnerAPI_AICache_ExportSemanticDefaults(t *testing.T) {
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("aice-1-007"), "cond": validAICacheCond},
	})

	resp := exportAICache(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	var data aiCacheExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assertSemanticDefaults(t, data.Semantic)

	// 原始 body 文本形态：顶层 Semantic 键 + camelCase tag 逐字锁定（#102）。
	body := string(resp.RawBody)
	assert.Contains(t, body, `"Semantic":{`)
	assert.Contains(t, body, `"topK":1`)
	assert.Contains(t, body, `"threshold":0.15`)
	assert.Contains(t, body, `"thresholdRelation":"lt"`)
}

// ---------- AICE-1-008 设置变更驱动版本流（MD5 变化 → 新版本号） ----------

func TestInnerAPI_AICache_ExportSemanticChangeBumpsVersion(t *testing.T) {
	n1 := testutil.UniqueName("aice-1-008-rule")
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": n1, "cond": validAICacheCond, "cache_ttl": 1234},
	})
	v1 := exportAICacheData(t).Version
	beforeRules := productRules(t, exportAICacheData(t))

	// PUT 语义设置（不动 rules）→ 全量生成内容签名变化 → 导出版本推进。
	putSemanticSettingsOK(t, map[string]interface{}{
		"top_k": 7, "threshold": 0.85, "threshold_relation": "gte",
	})

	// 轮询导出（间隔 500ms，最多 10 次）直到版本严格大于 v1（同秒碰撞防护，#142 模式）。
	var converged *aiCacheExportData
	for i := 0; i < 10; i++ {
		data := exportAICacheData(t)
		if data.Version > v1 {
			converged = data
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotNil(t, converged, "semantic settings change must bump export version within bounded polls")

	// Semantic 块反映新值（camelCase），规则内容不受影响（两者独立维护）。
	semantic := converged.Semantic
	require.NotNil(t, semantic)
	assert.Equal(t, 7, *semantic.TopK)
	assert.InDelta(t, 0.85, *semantic.Threshold, 1e-9)
	assert.Equal(t, "gte", *semantic.ThresholdRelation)
	assert.Equal(t, beforeRules, productRules(t, converged), "rules must be untouched by settings PUT")

	// 内容未再变更：首拉版本保持收敛值，且原始 body 文本形态锁定新值序列化（#102）。
	again := exportAICache(t, "")
	var againData aiCacheExportData
	require.NoError(t, json.Unmarshal(again.Data, &againData))
	assert.Equal(t, converged.Version, againData.Version, "unchanged content must keep the converged version")
	body := string(again.RawBody)
	assert.Contains(t, body, `"topK":7`)
	assert.Contains(t, body, `"threshold":0.85`)
	assert.Contains(t, body, `"thresholdRelation":"gte"`)

	// AICE-1-009 增量语义不回归：携带新 version 再拉 → Data 为 null。
	incrResp := exportAICache(t, converged.Version)
	testutil.AssertDataNull(t, incrResp)
}

// ---------- AICE-1-010 清空 rules 后 Semantic 块仍导出（设置独立于规则生命周期） ----------

func TestInnerAPI_AICache_ExportSemanticSurvivesRuleClear(t *testing.T) {
	putAICacheRulesOK(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("aice-1-010-a"), "cond": aiCacheCondFor("aice-1-010-a")},
		map[string]interface{}{"name": testutil.UniqueName("aice-1-010-b"), "cond": aiCacheCondFor("aice-1-010-b")},
	})
	putAICacheRulesOK(t, []interface{}{})

	resp := exportAICache(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	var data aiCacheExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)

	// Config 为空数组（product 键 present，非 null）。
	rules, ok := data.Config[productKey]
	require.True(t, ok, "product key %s must stay present for empty collection", productKey)
	require.NotNil(t, rules, "empty collection must export [] not null")
	assert.Len(t, rules, 0)

	// Semantic 块仍在，且与当前 Open API 设置读数一致（规则清空不影响设置）。
	settingsResp, err := testutil.GetClient().Get(aiCacheSemanticSettingsPath)
	require.NoError(t, err)
	testutil.AssertSuccess(t, settingsResp)
	var settings map[string]interface{}
	require.NoError(t, json.Unmarshal(settingsResp.Data, &settings))

	semantic := data.Semantic
	require.NotNil(t, semantic, "Semantic block must survive rule clear (设置独立于规则生命周期)")
	assert.Equal(t, int(settings["top_k"].(float64)), *semantic.TopK)
	assert.InDelta(t, settings["threshold"].(float64), *semantic.Threshold, 1e-9)
	assert.Equal(t, settings["threshold_relation"].(string), *semantic.ThresholdRelation)

	// 原始 body 文本：Semantic 键在、Config 为空数组。
	body := string(resp.RawBody)
	assert.Contains(t, body, `"Semantic":{`)
	assert.Contains(t, body, `"`+productKey+`":[]`)
}
