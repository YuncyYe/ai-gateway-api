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

package iintent_config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateIntentConfig(t *testing.T) {
	t.Run("nil document is rejected", func(t *testing.T) {
		err := ValidateIntentConfig(nil)
		require.Error(t, err)
	})

	t.Run("questions is required", func(t *testing.T) {
		err := ValidateIntentConfig(&IntentConfigParam{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "questions")
	})

	t.Run("questions null literal is rejected", func(t *testing.T) {
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(`null`)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "questions")
	})

	t.Run("questions must be a JSON array", func(t *testing.T) {
		for _, body := range []string{`{"a":1}`, `"str"`, `123`, `true`} {
			err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
			require.Error(t, err, "body: %s", body)
			assert.Contains(t, err.Error(), "JSON array", "body: %s", body)
		}
	})

	t.Run("empty questions array is the valid soft switch", func(t *testing.T) {
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(`[]`)})
		assert.NoError(t, err)
	})

	t.Run("null question element is rejected", func(t *testing.T) {
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(`[null]`)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "null")
	})

	t.Run("non object question element is rejected", func(t *testing.T) {
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(`["x"]`)})
		require.Error(t, err)
	})

	t.Run("more than 10 questions is rejected", func(t *testing.T) {
		questions := make([]string, 0, 11)
		for i := 0; i < 11; i++ {
			questions = append(questions, fmt.Sprintf(
				`{"name":"q%d","type":"choice","instructions":"i","criteria":{"a":"b"}}`, i))
		}
		body := `[` + strings.Join(questions, ",") + `]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "questions count")
	})

	t.Run("empty or duplicated question name is rejected", func(t *testing.T) {
		emptyName := `[{"name":"","type":"choice","instructions":"i","criteria":{"a":"b"}}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(emptyName)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")

		duplicate := `[
			{"name":"dup","type":"choice","instructions":"i","criteria":{"a":"b"}},
			{"name":"dup","type":"score","instructions":"i","levels":[{"name":"l","description":"d"}]}
		]`
		err = ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(duplicate)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("invalid question type is rejected", func(t *testing.T) {
		body := `[{"name":"q","type":"boolean","instructions":"i","criteria":{"a":"b"}}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "type")
	})

	t.Run("empty instructions is rejected", func(t *testing.T) {
		body := `[{"name":"q","type":"choice","instructions":"","criteria":{"a":"b"}}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instructions")
	})

	t.Run("choice requires criteria 1-10 entries", func(t *testing.T) {
		withoutCriteria := `[{"name":"q","type":"choice","instructions":"i"}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(withoutCriteria)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "criteria count")

		criteria := make([]string, 0, 11)
		for i := 0; i < 11; i++ {
			criteria = append(criteria, fmt.Sprintf(`"opt%d":"d%d"`, i, i))
		}
		body := `[{"name":"q","type":"choice","instructions":"i","criteria":{` + strings.Join(criteria, ",") + `}}]`
		err = ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "criteria count")
	})

	t.Run("choice rejects empty option name and pipe in option name", func(t *testing.T) {
		emptyOption := `[{"name":"q","type":"choice","instructions":"i","criteria":{"":"d"}}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(emptyOption)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty option name")

		pipeOption := `[{"name":"q","type":"choice","instructions":"i","criteria":{"a|b":"d"}}]`
		err = ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(pipeOption)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "contains '|'")
	})

	t.Run("choice rejects levels", func(t *testing.T) {
		body := `[{"name":"q","type":"choice","instructions":"i","criteria":{"a":"b"},
			"levels":[{"name":"l","description":"d"}]}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "levels not allowed")
	})

	t.Run("score requires levels 1-10 entries", func(t *testing.T) {
		withoutLevels := `[{"name":"q","type":"score","instructions":"i"}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(withoutLevels)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "levels count")

		levels := make([]string, 0, 11)
		for i := 0; i < 11; i++ {
			levels = append(levels, fmt.Sprintf(`{"name":"l%d","description":"d"}`, i))
		}
		body := `[{"name":"q","type":"score","instructions":"i","levels":[` + strings.Join(levels, ",") + `]}]`
		err = ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "levels count")
	})

	t.Run("score rejects empty or duplicated level name", func(t *testing.T) {
		emptyLevel := `[{"name":"q","type":"score","instructions":"i","levels":[{"name":"","description":"d"}]}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(emptyLevel)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "level #0 name")

		duplicate := `[{"name":"q","type":"score","instructions":"i","levels":[
			{"name":"l","description":"d1"},{"name":"l","description":"d2"}]}]`
		err = ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(duplicate)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate level name")
	})

	t.Run("score rejects criteria", func(t *testing.T) {
		body := `[{"name":"q","type":"score","instructions":"i","criteria":{"a":"b"},
			"levels":[{"name":"l","description":"d"}]}]`
		err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "criteria not allowed")
	})

	t.Run("per-question min_confidence out of range is rejected", func(t *testing.T) {
		for _, v := range []string{`-0.001`, `1.001`} {
			body := `[{"name":"q","type":"choice","instructions":"i","min_confidence":` + v + `,"criteria":{"a":"b"}}]`
			err := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(body)})
			require.Error(t, err, "min_confidence: %s", v)
			assert.Contains(t, err.Error(), "out of range [0,1]", "min_confidence: %s", v)
		}
	})

	t.Run("global min_confidence out of range is rejected", func(t *testing.T) {
		for _, v := range []float64{-0.001, 1.001} {
			param := &IntentConfigParam{
				MinConfidence: float64Ptr(v),
				Questions:     json.RawMessage(`[]`),
			}
			err := ValidateIntentConfig(param)
			require.Error(t, err, "min_confidence: %f", v)
			assert.Contains(t, err.Error(), "min_confidence", "min_confidence: %f", v)
		}
	})

	t.Run("boundary values are accepted", func(t *testing.T) {
		body := `[
			{"name":"q1","type":"choice","instructions":"i","min_confidence":0,"criteria":{"a":"b"}},
			{"name":"q2","type":"score","instructions":"i","min_confidence":1,"levels":[{"name":"l","description":"d"}]}
		]`
		err := ValidateIntentConfig(&IntentConfigParam{
			MinConfidence: float64Ptr(0),
			Questions:     json.RawMessage(body),
		})
		assert.NoError(t, err)

		err = ValidateIntentConfig(&IntentConfigParam{
			MinConfidence: float64Ptr(1),
			Questions:     json.RawMessage(body),
		})
		assert.NoError(t, err)
	})
}

func TestParseIntentQuestions(t *testing.T) {
	t.Run("whitespace-padded array is accepted", func(t *testing.T) {
		questions, err := parseIntentQuestions(json.RawMessage("  [ ]  "))
		require.NoError(t, err)
		assert.Empty(t, questions)
	})

	t.Run("empty raw message is rejected", func(t *testing.T) {
		_, err := parseIntentQuestions(json.RawMessage(""))
		require.Error(t, err)
	})
}

func TestIntentConfigParamToStorage(t *testing.T) {
	t.Run("storage row carries the fixed id, given version and raw questions", func(t *testing.T) {
		row := intentConfigParamToStorage(validPutParam(), "20260926120000")
		assert.Equal(t, IntentConfigFixedID, row.Id)
		assert.Equal(t, "20260926120000", row.Version)
		assert.InDelta(t, 0.6, row.MinConfidence, 1e-9)
		assert.Equal(t, string(validQuestionsJSON), row.Questions)
	})

	t.Run("omitted min_confidence falls back to the default", func(t *testing.T) {
		param := validPutParam()
		param.MinConfidence = nil
		row := intentConfigParamToStorage(param, "20260926120000")
		assert.InDelta(t, DefaultMinConfidence, row.MinConfidence, 1e-9)
	})
}

func TestIntentConfigToParam(t *testing.T) {
	assert.Nil(t, intentConfigToParam(nil))

	publishedAt := time.Now()
	param := intentConfigToParam(&IntentConfig{
		Id:            IntentConfigFixedID,
		Version:       "20260926120000",
		MinConfidence: 0.6,
		Questions:     `[]`,
		CreatedAt:     publishedAt,
		UpdatedAt:     publishedAt,
	})
	require.NotNil(t, param)
	assert.InDelta(t, 0.6, *param.MinConfidence, 1e-9)
	assert.Equal(t, "[]", string(param.Questions))
	assert.Equal(t, publishedAt, *param.CreatedAt)
	assert.Equal(t, publishedAt, *param.UpdatedAt)
}
