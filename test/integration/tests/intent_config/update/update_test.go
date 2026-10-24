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

package intent_config_update_test

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
	intentConfigPath       = "/open-api/v1/intent-config"
	intentConfigAuditResID = "intent_config"
	defaultMinConfidence   = 0.6
)

// intentConfigContractKeys 是合同锁定的响应键集合（4 字段，无 version/id）。
var intentConfigContractKeys = []string{"min_confidence", "questions", "created_at", "updated_at"}

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

func putIntentConfig(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Put(intentConfigPath, body)
	require.NoError(t, err)
	return resp
}

func putIntentConfigOK(t *testing.T, body map[string]interface{}) *testutil.APIResponse {
	t.Helper()
	resp := putIntentConfig(t, body)
	testutil.AssertSuccess(t, resp)
	return resp
}

func getIntentConfig(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(intentConfigPath)
	require.NoError(t, err)
	return resp
}

// exportIntentConfig 拉取 /inner-api/v1/configs/mod-ai-intent 导出（家族4 防泄漏断言用）。
func exportIntentConfig(t *testing.T) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get("/inner-api/v1/configs/mod-ai-intent")
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	return resp
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func fullQuestions(suffix string) []interface{} {
	return []interface{}{
		map[string]interface{}{
			"name":         "task_type_" + suffix,
			"type":         "choice",
			"instructions": "这条请求属于哪类研发任务？",
			"criteria": map[string]interface{}{
				"coding":       "编写或修改代码、调试、重构、代码审查",
				"test_writing": "编写测试用例、单元测试、集成测试、补充断言",
				"doc_writing":  "编写文档、README、注释、接口说明、使用示例",
			},
		},
		map[string]interface{}{
			"name":           "complexity_" + suffix,
			"type":           "score",
			"instructions":   "这个任务的复杂度如何？",
			"min_confidence": 0.7,
			"levels": []interface{}{
				map[string]interface{}{"name": "simple", "description": "单步即可完成"},
				map[string]interface{}{"name": "medium", "description": "多步但模式常见"},
				map[string]interface{}{"name": "complex", "description": "需要深入推理或跨模块设计"},
			},
		},
	}
}

// ---------- IC-1-001 最小参数（省略 min_confidence 回填 0.6） ----------

func TestIntentConfig_Update_MinimalParams(t *testing.T) {
	resp := putIntentConfigOK(t, map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"name":         testutil.UniqueName("ic-1-001"),
				"type":         "choice",
				"instructions": "i",
				"criteria":     map[string]interface{}{"a": "b"},
			},
		},
	})
	testutil.AssertDataFieldEquals(t, resp, "min_confidence", defaultMinConfidence)
}

// ---------- IC-1-002 完整参数 + 合同锁定（无 version/id） ----------

func TestIntentConfig_Update_FullParamsContract(t *testing.T) {
	resp := putIntentConfigOK(t, map[string]interface{}{
		"min_confidence": 0.6,
		"questions":      fullQuestions("ic1002"),
	})

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.ElementsMatch(t, intentConfigContractKeys, keysOf(data),
		"response must not contain internal version or id")
	assert.NotContains(t, keysOf(data), "version")
	assert.NotContains(t, keysOf(data), "id")

	assert.InDelta(t, 0.6, data["min_confidence"], 1e-9)
	questions, ok := data["questions"].([]interface{})
	require.True(t, ok, "questions should be array")
	require.Len(t, questions, 2)

	choice := questions[0].(map[string]interface{})
	assert.Equal(t, "choice", choice["type"])
	assert.Contains(t, choice, "criteria")
	assert.NotContains(t, choice, "levels")

	score := questions[1].(map[string]interface{})
	assert.Equal(t, "score", score["type"])
	assert.Contains(t, score, "levels")
	assert.NotContains(t, score, "criteria")

	testutil.AssertDataFieldNotEmpty(t, resp, "created_at")
	testutil.AssertDataFieldNotEmpty(t, resp, "updated_at")
}

// ---------- IC-1-003 空 questions 软开关 ----------

func TestIntentConfig_Update_EmptyQuestionsSoftSwitch(t *testing.T) {
	resp := putIntentConfigOK(t, map[string]interface{}{"questions": []interface{}{}})
	testutil.AssertDataFieldEquals(t, resp, "min_confidence", defaultMinConfidence)

	get := getIntentConfig(t)
	testutil.AssertSuccess(t, get)
	testutil.AssertDataFieldEquals(t, get, "min_confidence", defaultMinConfidence)
	raw, err := json.Marshal(get.Data)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"questions":[]`)
}

// ---------- IC-1-004 全量覆盖 ----------

func TestIntentConfig_Update_FullReplace(t *testing.T) {
	putIntentConfigOK(t, map[string]interface{}{
		"min_confidence": 0.6,
		"questions":      fullQuestions("ic1004a"),
	})
	putIntentConfigOK(t, map[string]interface{}{
		"min_confidence": 0.9,
		"questions": []interface{}{
			map[string]interface{}{
				"name":         testutil.UniqueName("ic-1-004"),
				"type":         "score",
				"instructions": "i",
				"levels":       []interface{}{map[string]interface{}{"name": "l", "description": "d"}},
			},
		},
	})

	get := getIntentConfig(t)
	testutil.AssertSuccess(t, get)
	testutil.AssertDataFieldEquals(t, get, "min_confidence", 0.9)
	var getData map[string]interface{}
	require.NoError(t, json.Unmarshal(get.Data, &getData))
	questions, ok := getData["questions"].([]interface{})
	require.True(t, ok)
	require.Len(t, questions, 1, "full replace keeps only the second PUT content")
	assert.Equal(t, "score", questions[0].(map[string]interface{})["type"])
}

// ---------- IC-1-005 校验失败族（422 且配置不变） ----------

func TestIntentConfig_Update_ValidationFailures(t *testing.T) {
	validChoice := func(name string) map[string]interface{} {
		return map[string]interface{}{
			"name": name, "type": "choice", "instructions": "i",
			"criteria": map[string]interface{}{"a": "b"},
		}
	}

	keeper := validChoice(testutil.UniqueName("ic-1-005-keeper"))
	putIntentConfigOK(t, map[string]interface{}{"questions": []interface{}{keeper}})
	before := getIntentConfig(t)
	testutil.AssertSuccess(t, before)
	exportBefore := exportIntentConfig(t)

	cases := []struct {
		name string
		body map[string]interface{}
		want string
	}{
		{"missing questions", map[string]interface{}{"min_confidence": 0.6}, "questions"},
		{"questions not array", map[string]interface{}{"questions": map[string]interface{}{"a": 1}}, "JSON array"},
		{"more than 10 questions", map[string]interface{}{"questions": make([]interface{}, 11)}, "questions count"},
		{"invalid type", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "boolean", "instructions": "i", "criteria": map[string]interface{}{"a": "b"}},
		}}, "type"},
		{"empty question name", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "", "type": "choice", "instructions": "i", "criteria": map[string]interface{}{"a": "b"}},
		}}, "name"},
		{"empty instructions", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "choice", "instructions": "", "criteria": map[string]interface{}{"a": "b"}},
		}}, "instructions"},
		{"choice missing criteria", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "choice", "instructions": "i"},
		}}, "criteria count"},
		{"choice criteria over 10", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "choice", "instructions": "i", "criteria": manyOptions("c", 11)},
		}}, "criteria count"},
		{"choice with levels", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "choice", "instructions": "i",
				"criteria": map[string]interface{}{"a": "b"},
				"levels":   []interface{}{map[string]interface{}{"name": "l", "description": "d"}}},
		}}, "levels not allowed"},
		{"option name with pipe", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "choice", "instructions": "i", "criteria": map[string]interface{}{"a|b": "d"}},
		}}, "contains '|'"},
		{"duplicate question name", map[string]interface{}{"questions": []interface{}{
			validChoice("dup-ic-1-005"), validChoice("dup-ic-1-005"),
		}}, "duplicate"},
		{"score missing levels", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "score", "instructions": "i"},
		}}, "levels count"},
		{"score levels over 10", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "score", "instructions": "i", "levels": manyLevels(11)},
		}}, "levels count"},
		{"score with criteria", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "score", "instructions": "i",
				"criteria": map[string]interface{}{"a": "b"},
				"levels":   []interface{}{map[string]interface{}{"name": "l", "description": "d"}}},
		}}, "criteria not allowed"},
		{"duplicate level name", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "score", "instructions": "i", "levels": []interface{}{
				map[string]interface{}{"name": "l", "description": "d1"},
				map[string]interface{}{"name": "l", "description": "d2"},
			}},
		}}, "duplicate level name"},
		{"min_confidence out of range", map[string]interface{}{"min_confidence": 1.5, "questions": []interface{}{}}, "out of range [0,1]"},
		{"question min_confidence out of range", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "choice", "instructions": "i",
				"min_confidence": 1.2, "criteria": map[string]interface{}{"a": "b"}},
		}}, "out of range [0,1]"},
		{"question min_confidence negative", map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": "q", "type": "choice", "instructions": "i",
				"min_confidence": -0.001, "criteria": map[string]interface{}{"a": "b"}},
		}}, "out of range [0,1]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := putIntentConfig(t, tc.body)
			// 错误码语义（家族7/#183）：管理写操作只允许 2xx/4xx，500 即缺陷。
			require.NotEqual(t, 500, resp.ErrNum, "write op must never return 500")
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, tc.want, "error must be attributable to the offending field")

			after := getIntentConfig(t)
			testutil.AssertSuccess(t, after)
			var beforeData, afterData map[string]interface{}
			require.NoError(t, json.Unmarshal(before.Data, &beforeData))
			require.NoError(t, json.Unmarshal(after.Data, &afterData))
			assert.Equal(t, beforeData, afterData, "rejected PUT must leave the published config unchanged")

			// 防泄漏（家族4/#172）：被拒绝的配置不得进入 InnerAPI 导出产物。
			exportAfter := exportIntentConfig(t)
			assert.JSONEq(t, string(exportBefore.Data), string(exportAfter.Data),
				"rejected PUT must not leak into the mod-ai-intent export")
		})
	}
}

// manyOptions 构造 n 个选项的 criteria（name 前缀 + 序号，键唯一）。
func manyOptions(prefix string, n int) map[string]interface{} {
	criteria := map[string]interface{}{}
	for i := 0; i < n; i++ {
		criteria[fmt.Sprintf("%s-%s-%d", prefix, testutil.RandomString(4), i)] = "d"
	}
	return criteria
}

// manyLevels 构造 n 个档位的 levels（Name 唯一）。
func manyLevels(n int) []interface{} {
	levels := make([]interface{}, 0, n)
	for i := 0; i < n; i++ {
		levels = append(levels, map[string]interface{}{
			"name": fmt.Sprintf("l-%s-%d", testutil.RandomString(4), i), "description": "d",
		})
	}
	return levels
}

// ---------- IC-1-006/IC-1-007 操作审计 ----------

func waitForIntentConfigAudit(t *testing.T, startTs int64, status string) *testutil.OperationLogEntry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, _, err := testutil.QueryOperationLogs(map[string]string{
			"resource_type": "intent_config",
			"action":        "update",
			"status":        status,
			"start_time":    fmt.Sprintf("%d", startTs),
			"page_size":     "100",
		})
		require.NoError(t, err)
		for i := range result.List {
			if result.List[i].ResourceType == "intent_config" {
				return &result.List[i]
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("operation log not found after 15s (status=%s, since=%d)", status, startTs)
	return nil
}

func TestIntentConfig_Update_OperationLog(t *testing.T) {
	t.Run("success_audit", func(t *testing.T) {
		startTs := time.Now().Add(-2 * time.Second).Unix()
		putIntentConfigOK(t, map[string]interface{}{
			"questions": []interface{}{
				map[string]interface{}{
					"name":         testutil.UniqueName("ic-1-006"),
					"type":         "choice",
					"instructions": "i",
					"criteria":     map[string]interface{}{"a": "b"},
				},
			},
		})

		entry := waitForIntentConfigAudit(t, startTs, "1")
		require.NotNil(t, entry)
		assert.Equal(t, "update", entry.Action)
		assert.Equal(t, "intent_config", entry.ResourceType)
		assert.Equal(t, intentConfigAuditResID, entry.ResourceID, "singleton identity is fixed")
		assert.Equal(t, intentConfigAuditResID, entry.ResourceName)
		assert.Equal(t, float64(1), entry.Status)

		cs := entry.ChangeSummary
		require.NotNil(t, cs)
		assert.ElementsMatch(t, []string{"before", "after", "diff_keys"}, keysOf(cs))
	})

	t.Run("failure_audit", func(t *testing.T) {
		startTs := time.Now().Unix()
		resp := putIntentConfig(t, map[string]interface{}{"questions": "bad"})
		testutil.AssertErrCode(t, resp, 422)

		entry := waitForIntentConfigAudit(t, startTs, "2")
		require.NotNil(t, entry)
		assert.Equal(t, float64(2), entry.Status, "rejected PUT must be audited with failed status")
		assert.Equal(t, intentConfigAuditResID, entry.ResourceID, "identity never depends on the request body")
		assert.NotEmpty(t, entry.ErrorMsg, "failed audit should carry error reason")
	})
}

// ---------- IC-1-008 非法 JSON ----------

func TestIntentConfig_Update_InvalidJSON(t *testing.T) {
	resp, err := testutil.GetClient().RawBody("PUT", intentConfigPath, "not-json", "application/json")
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 422)
}

// ---------- IC-1-009 min_confidence 边界与缺省回落（家族4 边界±1 / #1 默认回落） ----------

// TestIntentConfig_Update_MinConfidenceBoundaries 覆盖全局门控阈值的合法边界（0、1）、
// 非法边界（-0.001、1.001，边界±1 外侧）与缺省回落（省略字段 → 0.6，GET 回读锁定）。
func TestIntentConfig_Update_MinConfidenceBoundaries(t *testing.T) {
	t.Run("zero is a valid boundary", func(t *testing.T) {
		putIntentConfigOK(t, map[string]interface{}{"min_confidence": 0, "questions": []interface{}{}})
		get := getIntentConfig(t)
		testutil.AssertSuccess(t, get)
		testutil.AssertDataFieldEquals(t, get, "min_confidence", 0.0)
	})

	t.Run("one is a valid boundary", func(t *testing.T) {
		putIntentConfigOK(t, map[string]interface{}{"min_confidence": 1, "questions": []interface{}{}})
		get := getIntentConfig(t)
		testutil.AssertSuccess(t, get)
		testutil.AssertDataFieldEquals(t, get, "min_confidence", 1.0)
	})

	t.Run("negative epsilon is rejected with attribution", func(t *testing.T) {
		exportBefore := exportIntentConfig(t)
		resp := putIntentConfig(t, map[string]interface{}{"min_confidence": -0.001, "questions": []interface{}{}})
		require.NotEqual(t, 500, resp.ErrNum, "write op must never return 500")
		testutil.AssertErrCode(t, resp, 422)
		assert.Contains(t, resp.ErrMsg, "min_confidence")
		assert.JSONEq(t, string(exportBefore.Data), string(exportIntentConfig(t).Data),
			"rejected PUT must not leak into the mod-ai-intent export")
	})

	t.Run("above one epsilon is rejected with attribution", func(t *testing.T) {
		exportBefore := exportIntentConfig(t)
		resp := putIntentConfig(t, map[string]interface{}{"min_confidence": 1.001, "questions": []interface{}{}})
		require.NotEqual(t, 500, resp.ErrNum, "write op must never return 500")
		testutil.AssertErrCode(t, resp, 422)
		assert.Contains(t, resp.ErrMsg, "min_confidence")
		assert.JSONEq(t, string(exportBefore.Data), string(exportIntentConfig(t).Data),
			"rejected PUT must not leak into the mod-ai-intent export")
	})

	t.Run("omitted field falls back to the 0.6 default on read-back", func(t *testing.T) {
		resp := putIntentConfigOK(t, map[string]interface{}{"questions": []interface{}{}})
		testutil.AssertDataFieldEquals(t, resp, "min_confidence", defaultMinConfidence)

		get := getIntentConfig(t)
		testutil.AssertSuccess(t, get)
		testutil.AssertDataFieldEquals(t, get, "min_confidence", defaultMinConfidence)
	})
}

// ---------- IC-1-010 questions 上界与字段必填（家族4 边界±1） ----------

// TestIntentConfig_Update_QuestionBoundaries 覆盖 questions 数组上界（10 合法 / 已有 11 拒绝）、
// name/instructions 必填、逐问题 min_confidence 边界。
func TestIntentConfig_Update_QuestionBoundaries(t *testing.T) {
	t.Run("exactly 10 questions is a valid boundary", func(t *testing.T) {
		questions := make([]interface{}, 0, 10)
		for i := 0; i < 10; i++ {
			questions = append(questions, map[string]interface{}{
				"name":         testutil.UniqueName(fmt.Sprintf("ic-1-010-q%02d", i)),
				"type":         "choice",
				"instructions": "i",
				"criteria":     map[string]interface{}{"a": "b"},
			})
		}
		resp := putIntentConfigOK(t, map[string]interface{}{"questions": questions})
		testutil.AssertSuccess(t, resp)

		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		assert.Len(t, data["questions"], 10, "exactly 10 questions must be accepted")

		export := exportIntentConfig(t)
		var exportData map[string]interface{}
		require.NoError(t, json.Unmarshal(export.Data, &exportData))
		assert.Len(t, exportData["Questions"], 10, "export must carry all 10 questions")
	})

	t.Run("score accepts exactly 10 levels", func(t *testing.T) {
		resp := putIntentConfigOK(t, map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": testutil.UniqueName("ic-1-010-l10"), "type": "score", "instructions": "i", "levels": manyLevels(10)},
		}})
		testutil.AssertSuccess(t, resp)
	})

	t.Run("score accepts exactly 10 criteria options", func(t *testing.T) {
		resp := putIntentConfigOK(t, map[string]interface{}{"questions": []interface{}{
			map[string]interface{}{"name": testutil.UniqueName("ic-1-010-c10"), "type": "choice", "instructions": "i", "criteria": manyOptions("c", 10)},
		}})
		testutil.AssertSuccess(t, resp)
	})

	t.Run("per-question min_confidence boundaries are enforced", func(t *testing.T) {
		for _, v := range []float64{1.001, -0.001} {
			resp := putIntentConfig(t, map[string]interface{}{"questions": []interface{}{
				map[string]interface{}{"name": "q", "type": "choice", "instructions": "i",
					"min_confidence": v, "criteria": map[string]interface{}{"a": "b"}},
			}})
			require.NotEqual(t, 500, resp.ErrNum, "write op must never return 500")
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, "out of range [0,1]")
		}
	})
}

// ---------- IC-1-012 审计 diff_keys 精确匹配（家族7 / #201 幻影键） ----------

// waitForIntentConfigAuditMatch 在 15s 内轮询操作日志，返回首个满足 match 的条目。
func waitForIntentConfigAuditMatch(t *testing.T, startTs int64, status string, match func(*testutil.OperationLogEntry) bool) *testutil.OperationLogEntry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, _, err := testutil.QueryOperationLogs(map[string]string{
			"resource_type": "intent_config",
			"action":        "update",
			"status":        status,
			"start_time":    fmt.Sprintf("%d", startTs),
			"page_size":     "100",
		})
		require.NoError(t, err)
		for i := range result.List {
			if result.List[i].ResourceType == "intent_config" && match(&result.List[i]) {
				return &result.List[i]
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("operation log not found after 15s (status=%s, since=%d)", status, startTs)
	return nil
}

// TestIntentConfig_Update_OperationLogDiffKeys 锁定成功审计的 diff_keys 为精确字段集合：
// 第二次 PUT 仅内容字段（min_confidence/questions）变化，created_at 单行保留不变、
// updated_at 推进——diff_keys 必须恰为 [min_confidence, questions, updated_at]，
// 多一个幻影键或少一个都失败（ElementsMatch，禁止 Contains）。
func TestIntentConfig_Update_OperationLogDiffKeys(t *testing.T) {
	markerA := testutil.UniqueName("ic-1-012-a")
	putIntentConfigOK(t, map[string]interface{}{
		"min_confidence": 0.6,
		"questions": []interface{}{map[string]interface{}{
			"name": markerA, "type": "choice", "instructions": "i", "criteria": map[string]interface{}{"a": "b"},
		}},
	})

	// 确保跨越秒边界：updated_at 单调推进后必入 diff_keys，断言才确定性。
	time.Sleep(1100 * time.Millisecond)
	startTs := time.Now().Add(-2 * time.Second).Unix()

	markerB := testutil.UniqueName("ic-1-012-b")
	putIntentConfigOK(t, map[string]interface{}{
		"min_confidence": 0.8,
		"questions": []interface{}{map[string]interface{}{
			"name": markerB, "type": "choice", "instructions": "i", "criteria": map[string]interface{}{"a": "b"},
		}},
	})

	entry := waitForIntentConfigAuditMatch(t, startTs, "1", func(e *testutil.OperationLogEntry) bool {
		after, ok := e.ChangeSummary["after"].(map[string]interface{})
		if !ok {
			return false
		}
		raw, _ := json.Marshal(after)
		return strings.Contains(string(raw), markerB)
	})
	require.NotNil(t, entry)

	assert.Equal(t, "update", entry.Action)
	assert.Equal(t, "intent_config", entry.ResourceType)
	assert.Equal(t, intentConfigAuditResID, entry.ResourceID)
	assert.Equal(t, float64(1), entry.Status)

	diffKeys, ok := entry.ChangeSummary["diff_keys"].([]interface{})
	require.True(t, ok, "diff_keys should be an array, got %v", entry.ChangeSummary["diff_keys"])
	assert.ElementsMatch(t, []interface{}{"min_confidence", "questions", "updated_at"}, diffKeys,
		"diff_keys must match exactly (issue #201 phantom keys)")

	// before 快照仍为第一次发布内容（created_at 不变、单行覆盖无历史）。
	before, ok := entry.ChangeSummary["before"].(map[string]interface{})
	require.True(t, ok, "before snapshot should be object")
	assert.Equal(t, 0.6, before["min_confidence"])
}
