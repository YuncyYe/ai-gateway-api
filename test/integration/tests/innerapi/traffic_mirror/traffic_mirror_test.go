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
	tmOpenPath   = "/open-api/v1/traffic-mirror-rules"
	tmExportPath = "/inner-api/v1/configs/traffic-mirror-rule"
	validTMCond  = `req_path_prefix_in("/v1/chat/completions", true)`
	validTMCondB = `default_t()`
	validTMCondC = `req_path_prefix_in("/v1/completions", true)`

	// productKey 为测试环境 AIRouteInnerProductName 配置值（合同默认 AI_product）。
	productKey = "AI_product"
)

// exportRuleKeys 为 Inner 导出规则的 7 个 tag（大写驼峰，与 Open API 词汇不同），
// 规则全字段恒输出（含空值零值 {} / [] / ""），合同锁定。
var exportRuleKeys = []string{
	"cond", "mirrorCluster", "percentage", "removeHeaders", "setHeaders", "bodyRewrites", "pathRewrite",
}

// defaultSensitiveHeaders 为"未提交 remove_headers"时控制面导出的默认黑名单
//（两层默认语义：BFE 自身缺省是空列表，默认黑名单是控制面导出行为）。
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

type tmExportRule struct {
	Cond          string                       `json:"cond"`
	MirrorCluster string                       `json:"mirrorCluster"`
	Percentage    int                          `json:"percentage"`
	RemoveHeaders []string                     `json:"removeHeaders"`
	SetHeaders    map[string]string            `json:"setHeaders"`
	BodyRewrites  []struct {
		Path  string `json:"path"`
		Value string `json:"value"`
	} `json:"bodyRewrites"`
	PathRewrite string `json:"pathRewrite"`
}

type tmExportData struct {
	Config  map[string][]tmExportRule `json:"Config"`
	Version string                    `json:"Version"`
}

// mustCreateCluster 创建真实 cluster（级联创建 provider），作为 mirror_cluster fixture。
func mustCreateCluster(t *testing.T) string {
	t.Helper()
	name, err := testutil.CreateCluster(testutil.UniqueClusterName())
	require.NoError(t, err, "create mirror cluster fixture failed")
	return name
}

func putTMRules(t *testing.T, rules []interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(tmOpenPath, map[string]interface{}{"rules": rules})
	require.NoError(t, err)
	return resp
}

func putTMRulesOK(t *testing.T, rules []interface{}) {
	t.Helper()
	resp := putTMRules(t, rules)
	testutil.AssertSuccess(t, resp)
}

// exportTM 拉取导出；version 为空表示首拉。返回原始响应（Data 可能为 null）。
func exportTM(t *testing.T, version string) *testutil.APIResponse {
	t.Helper()
	var resp *testutil.APIResponse
	var err error
	if version == "" {
		resp, err = testutil.GetClient().Get(tmExportPath)
	} else {
		resp, err = testutil.GetClient().Get(tmExportPath, map[string]string{"version": version})
	}
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// exportTMData 首拉导出（Data 必须非 null）并解析。
func exportTMData(t *testing.T) *tmExportData {
	t.Helper()
	resp := exportTM(t, "")
	testutil.AssertDataNotEmpty(t, resp)
	var data tmExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)
	_, ok := data.Config[productKey]
	require.True(t, ok, "Config must contain product key %s", productKey)
	return &data
}

func productRules(t *testing.T, data *tmExportData) []tmExportRule {
	t.Helper()
	rules, ok := data.Config[productKey]
	require.True(t, ok, "product key %s must be present", productKey)
	return rules
}

// ---------- TMIE-1-001 首拉与全字段文本形态（家族8：存在性 + 取值 + 原始 body 文本形态） ----------

func TestInnerAPI_TrafficMirror_ExportFirstPull(t *testing.T) {
	c1 := mustCreateCluster(t)
	c2 := mustCreateCluster(t)

	n1 := testutil.UniqueName("tmie-1-001-defaults")
	n2 := testutil.UniqueName("tmie-1-001-explicit")
	putTMRulesOK(t, []interface{}{
		// 规则 1：仅必填字段——removeHeaders 应填默认黑名单，setHeaders/bodyRewrites/pathRewrite 零值。
		map[string]interface{}{"name": n1, "cond": `default_t()`, "mirror_cluster": c1},
		// 规则 2：全字段显式非默认值。
		map[string]interface{}{
			"name": n2, "cond": validTMCond, "mirror_cluster": c2,
			"percentage":    10,
			"remove_headers": []interface{}{"X-Custom-Secret"},
			"set_headers":    map[string]interface{}{"X-Shadow-Env": "pre-release"},
			"body_rewrites":  []interface{}{map[string]interface{}{"path": "model", "value": "deepseek-v3"}},
			"path_rewrite":   "/v1/internal/chat/completions",
		},
	})

	resp := exportTM(t, "")
	testutil.AssertSuccess(t, resp)

	var data tmExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version, "Version must be non-empty")

	rules, ok := data.Config[productKey]
	require.True(t, ok, "Config must contain product key %s", productKey)
	require.Len(t, rules, 2, "Config.%s length must equal submitted collection", productKey)

	// 顺序=提交顺序（first-match-wins 优先级）。
	r0, r1 := rules[0], rules[1]
	assert.Equal(t, `default_t()`, r0.Cond)
	assert.Equal(t, c1, r0.MirrorCluster)
	assert.Equal(t, 100, r0.Percentage, "omitted percentage must export as 100")
	assert.ElementsMatch(t, defaultSensitiveHeaders, r0.RemoveHeaders,
		"omitted remove_headers must export default sensitive-header blacklist (两层默认语义)")
	assert.NotNil(t, r0.SetHeaders)
	assert.Empty(t, r0.SetHeaders, "omitted set_headers must export {}")
	assert.Empty(t, r0.BodyRewrites, "omitted body_rewrites must export []")
	assert.Equal(t, "", r0.PathRewrite, "omitted path_rewrite must export \"\"")

	assert.Equal(t, validTMCond, r1.Cond)
	assert.Equal(t, c2, r1.MirrorCluster)
	assert.Equal(t, 10, r1.Percentage)
	assert.Equal(t, []string{"X-Custom-Secret"}, r1.RemoveHeaders)
	assert.Equal(t, map[string]string{"X-Shadow-Env": "pre-release"}, r1.SetHeaders)
	require.Len(t, r1.BodyRewrites, 1)
	assert.Equal(t, "model", r1.BodyRewrites[0].Path)
	assert.Equal(t, "deepseek-v3", r1.BodyRewrites[0].Value)
	assert.Equal(t, "/v1/internal/chat/completions", r1.PathRewrite)

	// 每条规则恰含 7 个导出 tag（原始 body 逐规则键集合精确断言，防幻影键）。
	var raw struct {
		Config map[string][]map[string]interface{} `json:"Config"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &raw))
	for i, item := range raw.Config[productKey] {
		keys := make([]string, 0, len(item))
		for k := range item {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t, exportRuleKeys, keys, "exported rule[%d] must carry exactly the 7 tags", i)
	}

	// 原始响应 body 文本形态断言（家族8/#102：仅反序列化值比较会静默放过序列化错误）。
	body := string(resp.RawBody)
	assert.Contains(t, body, `"removeHeaders":["Authorization","Cookie","X-Api-Key"]`)
	assert.Contains(t, body, `"removeHeaders":["X-Custom-Secret"]`)
	assert.Contains(t, body, `"setHeaders":{}`)
	assert.Contains(t, body, `"bodyRewrites":[{"path":"model","value":"deepseek-v3"}]`)
	assert.Contains(t, body, `"pathRewrite":""`)
	assert.Contains(t, body, `"pathRewrite":"/v1/internal/chat/completions"`)
	assert.Contains(t, body, `"mirrorCluster":"`+c1+`"`)
}

// ---------- TMIE-1-002 显式空黑名单导出（家族1/8：两层默认语义对照） ----------

func TestInnerAPI_TrafficMirror_ExplicitEmptyBlacklist(t *testing.T) {
	c1 := mustCreateCluster(t)
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{
			"name":           testutil.UniqueName("tmie-1-002"),
			"cond":           validTMCond,
			"mirror_cluster": c1,
			"remove_headers": []interface{}{},
		},
	})

	resp := exportTM(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	// 显式 [] 必须导出为 []（不剔除），与缺省填默认黑名单严格区分。
	assert.Contains(t, string(resp.RawBody), `"removeHeaders":[]`,
		"explicit empty blacklist must export as [] (家族1 两层默认语义)")

	var data tmExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	rules := productRules(t, &data)
	require.Len(t, rules, 1)
	assert.Empty(t, rules[0].RemoveHeaders)
}

// ---------- TMIE-1-003 增量同步 ----------

func TestInnerAPI_TrafficMirror_Incremental(t *testing.T) {
	c1 := mustCreateCluster(t)
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("tmie-1-003"), "cond": validTMCond, "mirror_cluster": c1},
	})

	first := exportTMData(t)
	v1 := first.Version

	// 携带刚返回的 version 再拉 → Data 为 null（未变化）。
	incrResp := exportTM(t, v1)
	testutil.AssertDataNull(t, incrResp)

	// 不传 version 再拉 → 有数据且 Version 相同（内容未变则版本不变）。
	again := exportTMData(t)
	assert.Equal(t, v1, again.Version, "unchanged content must keep the same version")
	assert.Equal(t, productRules(t, first), productRules(t, again), "export content must be stable")
}

// ---------- TMIE-1-004 版本单调（家族8/#142：同秒连续变更版本严格递增且收敛） ----------

func TestInnerAPI_TrafficMirror_VersionMonotonic(t *testing.T) {
	c1 := mustCreateCluster(t)
	nA := testutil.UniqueName("tmie-1-004-a")
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{"name": nA, "cond": validTMCond, "mirror_cluster": c1, "percentage": 11},
	})
	v1 := exportTMData(t).Version

	// 同一秒内第二次变更。
	nB1 := testutil.UniqueName("tmie-1-004-b1")
	nB2 := testutil.UniqueName("tmie-1-004-b2")
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{"name": nB1, "cond": validTMCondB, "mirror_cluster": c1, "percentage": 22},
		map[string]interface{}{"name": nB2, "cond": validTMCondC, "mirror_cluster": c1, "percentage": 33},
	})

	// 轮询导出（间隔 500ms，最多 10 次）直到版本严格大于 v1（版本串为 14 位定宽，字典序即时间序）。
	var converged *tmExportData
	for i := 0; i < 10; i++ {
		data := exportTMData(t)
		if data.Version > v1 {
			converged = data
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotNil(t, converged, "version must strictly increase within bounded polls (家族8/#142)")

	// 内容收敛为最新集合（导出无 name 字段，以各不相同的 percentage 锁定顺序）。
	rules := productRules(t, converged)
	require.Len(t, rules, 2)
	assert.Equal(t, 22, rules[0].Percentage)
	assert.Equal(t, 33, rules[1].Percentage)
}

// ---------- TMIE-1-005 清空导出（家族8：空数组非 null，product 键 present） ----------

func TestInnerAPI_TrafficMirror_ExportEmpty(t *testing.T) {
	c1 := mustCreateCluster(t)
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("tmie-1-005-a"), "cond": validTMCond, "mirror_cluster": c1},
		map[string]interface{}{"name": testutil.UniqueName("tmie-1-005-b"), "cond": validTMCondB, "mirror_cluster": c1},
	})
	putTMRulesOK(t, []interface{}{})

	resp := exportTM(t, "")
	testutil.AssertDataNotEmpty(t, resp)

	var data tmExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data.Version)

	// product 键 present 且为 []（非 null）。
	rules, ok := data.Config[productKey]
	require.True(t, ok, "product key %s must stay present for empty collection", productKey)
	require.NotNil(t, rules, "empty collection must export [] not null")
	assert.Len(t, rules, 0)
	assert.Contains(t, string(resp.RawBody), `"`+productKey+`":[]`)
}

// ---------- TMIE-1-006 422 防泄漏（家族4：拒绝后导出完全一致、Version 不变） ----------

func TestInnerAPI_TrafficMirror_RejectedPutNoLeak(t *testing.T) {
	c1 := mustCreateCluster(t)
	keeper := testutil.UniqueName("tmie-1-006-keeper")
	putTMRulesOK(t, []interface{}{
		map[string]interface{}{"name": keeper, "cond": validTMCond, "mirror_cluster": c1, "percentage": 77},
	})
	before := exportTMData(t)
	v1 := before.Version
	beforeRules := productRules(t, before)

	// PUT 被拒（非法 cond）。
	resp := putTMRules(t, []interface{}{
		map[string]interface{}{"name": testutil.UniqueName("tmie-1-006-bad"), "cond": "not_a_valid_expr(", "mirror_cluster": c1},
	})
	testutil.AssertErrCode(t, resp, 422)

	// 带 v1 增量拉取 → Data null（版本未推进）。
	incrResp := exportTM(t, v1)
	testutil.AssertDataNull(t, incrResp)

	// 首拉 → Version 不变且内容与拒绝前完全一致。
	after := exportTMData(t)
	assert.Equal(t, v1, after.Version, "rejected PUT must not bump export version (家族4)")
	assert.Equal(t, beforeRules, productRules(t, after),
		"rejected PUT must not leak into export content (家族4)")
}
