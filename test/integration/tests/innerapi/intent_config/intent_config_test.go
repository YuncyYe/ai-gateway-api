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

package innerapi_test

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	intentConfigOpenPath   = "/open-api/v1/intent-config"
	intentConfigExportPath = "/inner-api/v1/configs/mod-ai-intent"
)

// intentConfigExportKeys 为 Inner 导出顶层键（PascalCase，BFE 文件合同；
// Version 内嵌文件原样，同 ai-route 形态，非 ai-cache 的 Version+Config 包装）。
var intentConfigExportKeys = []string{"Version", "MinConfidence", "Questions"}

// exportQuestionKeys 为问题元素的 PascalCase 键集合（透传 BFE 合同）。
var exportQuestionKeys = []string{"Name", "Type", "Instructions", "Criteria"}

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

type intentConfigExportData struct {
	Version       string                   `json:"Version"`
	MinConfidence float64                  `json:"MinConfidence"`
	Questions     []map[string]interface{} `json:"Questions"`
}

func putIntentConfig(t *testing.T, body map[string]interface{}) {
	t.Helper()
	resp, err := testutil.GetClient().Put(intentConfigOpenPath, body)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
}

// exportIntentConfig 拉取导出；version 为空表示首拉。返回原始响应（Data 可能为 null）。
func exportIntentConfig(t *testing.T, version string) *testutil.APIResponse {
	t.Helper()
	var resp *testutil.APIResponse
	var err error
	if version == "" {
		resp, err = testutil.GetClient().Get(intentConfigExportPath)
	} else {
		resp, err = testutil.GetClient().Get(intentConfigExportPath, map[string]string{"version": version})
	}
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

// exportIntentConfigData 首拉导出（Data 必须非 null）并解析。
func exportIntentConfigData(t *testing.T) *intentConfigExportData {
	t.Helper()
	resp := exportIntentConfig(t, "")
	testutil.AssertDataNotEmpty(t, resp)
	var data intentConfigExportData
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	return &data
}

func fullQuestions(name string) []interface{} {
	return []interface{}{
		map[string]interface{}{
			"name":         name,
			"type":         "choice",
			"instructions": "这条请求属于哪类研发任务？",
			"criteria": map[string]interface{}{
				"coding":       "编写或修改代码、调试、重构、代码审查",
				"test_writing": "编写测试用例、单元测试、集成测试、补充断言",
				"doc_writing":  "编写文档、README、注释、接口说明、使用示例",
			},
		},
	}
}

// ---------- IC-E-001 未发布首拉：Data null ----------

func TestIntentConfigExport_NotPublished(t *testing.T) {
	resp := exportIntentConfig(t, "")
	testutil.AssertDataNull(t, resp)
}

// ---------- IC-E-002 发布后首拉：PascalCase 合同 + 内容透传 ----------

func TestIntentConfigExport_FirstPullContract(t *testing.T) {
	putIntentConfig(t, map[string]interface{}{
		"min_confidence": 0.6,
		"questions":      fullQuestions(testutil.UniqueName("ic-e-002")),
	})

	data := exportIntentConfigData(t)
	require.NotEmpty(t, data.Version)
	assert.InDelta(t, 0.6, data.MinConfidence, 1e-9)
	require.Len(t, data.Questions, 1)

	// 顶层键精确匹配（禁止幻影键；非 Version+Config 包装形态）。
	var top map[string]interface{}
	resp := exportIntentConfig(t, "")
	require.NoError(t, json.Unmarshal(resp.Data, &top))
	assert.ElementsMatch(t, intentConfigExportKeys, keysOfMap(top), "export must be the file content verbatim (Version embedded, no Config wrapper)")

	// 问题元素键精确匹配（Name/Type/Instructions/Criteria，子字段大写驼峰）。
	assert.ElementsMatch(t, exportQuestionKeys, keysOfMap(data.Questions[0]))

	criteria, ok := data.Questions[0]["Criteria"].(map[string]interface{})
	require.True(t, ok, "Criteria should be an object")
	assert.Contains(t, criteria, "coding")
	assert.Equal(t, "choice", data.Questions[0]["Type"])
}

// ---------- IC-E-003 版本未变化：Data null ----------

func TestIntentConfigExport_UnchangedVersion(t *testing.T) {
	putIntentConfig(t, map[string]interface{}{
		"questions": fullQuestions(testutil.UniqueName("ic-e-003")),
	})
	first := exportIntentConfigData(t)

	resp := exportIntentConfig(t, first.Version)
	testutil.AssertDataNull(t, resp)
}

// ---------- IC-E-004 内容变更：新版本 + 内容更新 ----------

func TestIntentConfigExport_ContentChange(t *testing.T) {
	putIntentConfig(t, map[string]interface{}{
		"min_confidence": 0.6,
		"questions":      fullQuestions(testutil.UniqueName("ic-e-004a")),
	})
	first := exportIntentConfigData(t)

	putIntentConfig(t, map[string]interface{}{
		"min_confidence": 0.8,
		"questions":      fullQuestions(testutil.UniqueName("ic-e-004b")),
	})
	second := exportIntentConfigData(t)

	assert.NotEqual(t, first.Version, second.Version, "content change must produce a new export version")
	assert.InDelta(t, 0.8, second.MinConfidence, 1e-9)

	// 旧版本号再拉：增量语义（当前版本已推进，旧 version 视为变更）。
	resp := exportIntentConfig(t, first.Version)
	testutil.AssertDataNotEmpty(t, resp)
}

// ---------- IC-E-005 空 questions 软开关正常下发 ----------

func TestIntentConfigExport_EmptyQuestionsSoftSwitch(t *testing.T) {
	putIntentConfig(t, map[string]interface{}{"questions": []interface{}{}})

	data := exportIntentConfigData(t)
	require.NotEmpty(t, data.Version)
	assert.NotNil(t, data.Questions, "questions must be present")
	assert.Len(t, data.Questions, 0)

	raw, err := json.Marshal(data)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"Questions":[]`)
}

func keysOfMap(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ---------- IC-E-006 版本单调（家族8 / #142） ----------

// TestIntentConfigExport_VersionMonotonic 同一秒内连续两次 PUT（内容不同），
// 两次导出的 Version 必须严格递增（第二版 = 第一版 +1s 顺延或更晚，有限轮次收敛）。
func TestIntentConfigExport_VersionMonotonic(t *testing.T) {
	putIntentConfig(t, map[string]interface{}{
		"questions": fullQuestions(testutil.UniqueName("ic-e-006a")),
	})
	first := exportIntentConfigData(t)

	// 不做 sleep：与第一次 PUT 落在同一秒内，迫使版本冲突顺延路径生效。
	putIntentConfig(t, map[string]interface{}{
		"questions": fullQuestions(testutil.UniqueName("ic-e-006b")),
	})
	second := exportIntentConfigData(t)

	assert.True(t, second.Version > first.Version,
		"export version must strictly increase on same-second re-PUT: first=%s second=%s",
		first.Version, second.Version)

	// 带第一版 version 增量拉取视为已过期（返回新数据而非 null）。
	resp := exportIntentConfig(t, first.Version)
	testutil.AssertDataNotEmpty(t, resp)
}

// ---------- IC-E-007 数值文本形态（家族8/#9 / #102） ----------

// TestIntentConfigExport_MinConfidenceTextForm 对原始 HTTP body 文本断言
// `"MinConfidence":0.655`：定点数不得序列化为科学计数法或丢精度
// （仅反序列化值比较会静默放过 0.6550000000000001 / 6.55e-01 形态）。
func TestIntentConfigExport_MinConfidenceTextForm(t *testing.T) {
	putIntentConfig(t, map[string]interface{}{
		"min_confidence": 0.655,
		"questions":      fullQuestions(testutil.UniqueName("ic-e-007")),
	})

	resp := exportIntentConfig(t, "")
	testutil.AssertDataNotEmpty(t, resp)
	body := string(resp.RawBody)
	assert.Contains(t, body, `"MinConfidence":0.655`)

	// 科学计数法/精度守护：抽取 MinConfidence 的原始文本 token
	//（问题名等其余字段可能含 "e-"，不能对整段 body 做朴素 NotContains）。
	token := minConfidenceToken(t, body)
	assert.NotContains(t, token, "e", "min_confidence must not serialize in scientific notation (#102), token=%s", token)
	assert.NotContains(t, token, "0.6550000000000001", "min_confidence must not lose precision")
}

// minConfidenceTokenPattern 捕获 "MinConfidence":<token> 的 token（到 , 或 } 为止）。
var minConfidenceTokenPattern = regexp.MustCompile(`"MinConfidence":([^,}]+)`)

// minConfidenceToken 从原始 body 抽取 "MinConfidence":<token> 的 token 文本。
// 问题名等其余字段可能含 "e-"，科学计数法守护只针对该 token（家族8/#102）。
func minConfidenceToken(t *testing.T, body string) string {
	t.Helper()
	loc := minConfidenceTokenPattern.FindStringSubmatchIndex(body)
	require.NotNil(t, loc, "MinConfidence token not found in export body")
	return body[loc[2]:loc[3]]
}
