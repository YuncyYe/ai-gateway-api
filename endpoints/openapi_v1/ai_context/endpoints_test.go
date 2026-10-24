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

package ai_context

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints/openapi_v1/internal/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	aiContextModel "github.com/rainway-ai-gateway/ai-gateway-api/model/ai_context"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAIContextStorager emulates the DB behavior: ReplaceAll rebuilds the
// table (new ids in slice order, timestamps attached), FetchAll reads it back.
type fakeAIContextStorager struct {
	rules  []*aiContextModel.ContextRuleRow
	nextID int64
}

func (f *fakeAIContextStorager) FetchAll(ctx context.Context) ([]*aiContextModel.ContextRuleRow, error) {
	return f.rules, nil
}

func (f *fakeAIContextStorager) ReplaceAll(ctx context.Context, rules []*aiContextModel.ContextRuleRow) error {
	f.rules = make([]*aiContextModel.ContextRuleRow, 0, len(rules))
	now := time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)
	for _, r := range rules {
		f.nextID++
		f.rules = append(f.rules, &aiContextModel.ContextRuleRow{
			ID:               lib.PInt64(f.nextID),
			Cond:             r.Cond,
			Mode:             r.Mode,
			MaxContextTokens: r.MaxContextTokens,
			ReserveTokens:    r.ReserveTokens,
			CreatedAt:        &now,
			UpdatedAt:        &now,
		})
	}
	return nil
}

// fakeAIContextSettingsStorager emulates the singleton settings table: Get
// returns the in-memory row (nil when empty), Upsert overwrites it.
type fakeAIContextSettingsStorager struct {
	row *aiContextModel.SettingsRow
}

func (f *fakeAIContextSettingsStorager) Get(ctx context.Context) (*aiContextModel.SettingsRow, error) {
	return f.row, nil
}

func (f *fakeAIContextSettingsStorager) Upsert(ctx context.Context, row *aiContextModel.SettingsRow) error {
	now := time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)
	row.ID = lib.PInt64(1)
	row.CreatedAt = &now
	row.UpdatedAt = &now
	f.row = row
	return nil
}

var _ aiContextModel.AIContextSettingsStorager = (*fakeAIContextSettingsStorager)(nil)

func setupAIContextManager(storager aiContextModel.AIContextStorager, settingsStorager aiContextModel.AIContextSettingsStorager) func() {
	old := container.AIContextManager
	container.AIContextManager = aiContextModel.NewAIContextManager(&testutil.FakeTxn{}, storager, settingsStorager, nil, "AI_product")
	return func() {
		container.AIContextManager = old
	}
}

func TestAIContextRulesGetAction(t *testing.T) {
	t.Run("returns rule collection without internal id", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)
		store := &fakeAIContextStorager{
			nextID: 2,
			rules: []*aiContextModel.ContextRuleRow{
				{
					ID:               lib.PInt64(1),
					Cond:             lib.PString("req_path_in(\"/v1/chat/completions\", false)"),
					Mode:             lib.PString("balanced"),
					MaxContextTokens: lib.PInt(64000),
					ReserveTokens:    lib.PInt(8192),
					CreatedAt:        &now,
					UpdatedAt:        &now,
				},
			},
		}
		defer setupAIContextManager(store, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/ai-context-rules", nil)
		data, err := AIContextRulesGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)

		result, ok := data.(*shared.AIContextRulesParam)
		require.True(t, ok)
		require.Len(t, result.Rules, 1)
		assert.Equal(t, "balanced", *result.Rules[0].Mode)
		assert.Equal(t, 64000, *result.Rules[0].MaxContextTokens)
	})

	t.Run("empty collection returns empty rules array", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/ai-context-rules", nil)
		data, err := AIContextRulesGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"rules":[]`)
	})
}

func TestAIContextRulesUpdateAction(t *testing.T) {
	validBody := `{
		"rules": [
			{
				"cond": "req_path_in(\"/v1/chat/completions\", false)",
				"mode": "balanced",
				"max_context_tokens": 64000,
				"reserve_tokens": 8192
			},
			{
				"cond": "default_t()",
				"mode": "off"
			}
		]
	}`

	t.Run("round trip with default backfill and no internal id", func(t *testing.T) {
		store := &fakeAIContextStorager{}
		defer setupAIContextManager(store, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(validBody))
		data, err := AIContextRulesUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)

		result, ok := data.(*shared.AIContextRulesParam)
		require.True(t, ok)
		require.Len(t, result.Rules, 2)

		assert.Equal(t, "balanced", *result.Rules[0].Mode)
		assert.Equal(t, 64000, *result.Rules[0].MaxContextTokens)
		assert.Equal(t, 8192, *result.Rules[0].ReserveTokens)

		// Omitted optional fields are backfilled with defaults (0).
		assert.Equal(t, "off", *result.Rules[1].Mode)
		assert.Equal(t, 0, *result.Rules[1].MaxContextTokens)
		assert.Equal(t, 0, *result.Rules[1].ReserveTokens)
	})

	t.Run("empty rules clear the collection", func(t *testing.T) {
		store := &fakeAIContextStorager{}
		defer setupAIContextManager(store, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(`{"rules": []}`))
		data, err := AIContextRulesUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"rules":[]`)
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader("not-json"))
		_, err := AIContextRulesUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("422 missing mode", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{"rules": [{"cond": "default_t()"}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(body))
		_, err := AIContextRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mode is required")
	})

	t.Run("422 invalid mode enum", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{"rules": [{"cond": "default_t()", "mode": "hyper"}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(body))
		_, err := AIContextRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mode must be one of")
	})

	t.Run("422 missing cond", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{"rules": [{"mode": "off"}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(body))
		_, err := AIContextRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cond is required")
	})

	t.Run("422 duplicate cond in collection", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{"rules": [
			{"cond": "default_t()", "mode": "off"},
			{"cond": "default_t()", "mode": "balanced"}
		]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(body))
		_, err := AIContextRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("422 negative budget fields", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{"rules": [{"cond": "default_t()", "mode": "off", "max_context_tokens": -1}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(body))
		_, err := AIContextRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_context_tokens")

		body = `{"rules": [{"cond": "default_t()", "mode": "off", "reserve_tokens": -1}]}`
		req = httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(body))
		_, err = AIContextRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reserve_tokens")
	})

	t.Run("422 null rule element", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{"rules": [null]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-rules", strings.NewReader(body))
		_, err := AIContextRulesUpdateAction(req)
		require.Error(t, err)
	})
}

func TestAIContextSettingsGetAction(t *testing.T) {
	t.Run("empty table returns documented defaults without timestamps", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/ai-context-settings", nil)
		data, err := AIContextSettingsGetAction(req)
		require.NoError(t, err)

		result, ok := data.(*shared.AIContextSettingsParam)
		require.True(t, ok)
		assert.Equal(t, aiContextModel.DefaultTriggerRatio, *result.TriggerRatio)
		assert.Equal(t, aiContextModel.DefaultKeepLatestImages, *result.KeepLatestImages)
		assert.Equal(t, aiContextModel.DefaultToolResultMaxChars, *result.ToolResultMaxChars)
		assert.Equal(t, aiContextModel.DefaultThinkingPolicy, *result.ThinkingPolicy)
		assert.Equal(t, aiContextModel.DefaultCharsPerToken, *result.CharsPerToken)
		assert.Equal(t, aiContextModel.DefaultImageTokenEstimate, *result.ImageTokenEstimate)
		require.NotNil(t, result.Rewrite)
		assert.Equal(t, aiContextModel.DefaultRewriteStrength, *result.Rewrite.Strength)
		assert.Equal(t, aiContextModel.DefaultRewriteProtectedSurvivalRate, *result.Rewrite.ProtectedSurvivalRate)
	})

	t.Run("existing row returns stored values with timestamps", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)
		settings := &aiContextModel.SettingsRow{
			ID:                           lib.PInt64(1),
			TriggerRatio:                 lib.PFloat64(0.8),
			KeepLatestImages:             lib.PInt(4),
			ToolResultMaxChars:           lib.PInt(4000),
			ThinkingPolicy:               lib.PString("keep"),
			CharsPerToken:                lib.PInt(3),
			ImageTokenEstimate:           lib.PInt(800),
			RewriteStrength:              lib.PString("full"),
			RewriteProtectedSurvivalRate: lib.PFloat64(0.9),
			CreatedAt:                    &now,
			UpdatedAt:                    &now,
		}
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{row: settings})()

		req := httptest.NewRequest(http.MethodGet, "/ai-context-settings", nil)
		data, err := AIContextSettingsGetAction(req)
		require.NoError(t, err)

		result, ok := data.(*shared.AIContextSettingsParam)
		require.True(t, ok)
		assert.Equal(t, 0.8, *result.TriggerRatio)
		assert.Equal(t, "keep", *result.ThinkingPolicy)
		require.NotNil(t, result.Rewrite)
		assert.Equal(t, "full", *result.Rewrite.Strength)
	})
}

func TestAIContextSettingsUpdateAction(t *testing.T) {
	t.Run("upsert with default backfill and rewrite round trip", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{
			"trigger_ratio": 0.8,
			"chars_per_token": 3,
			"rewrite": {"strength": "full"}
		}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-settings", strings.NewReader(body))
		data, err := AIContextSettingsUpdateAction(req)
		require.NoError(t, err)

		result, ok := data.(*shared.AIContextSettingsParam)
		require.True(t, ok)
		assert.Equal(t, 0.8, *result.TriggerRatio)
		assert.Equal(t, 3, *result.CharsPerToken)

		// Omitted fields are backfilled with defaults; the rewrite
		// sub-object merges field by field.
		assert.Equal(t, aiContextModel.DefaultKeepLatestImages, *result.KeepLatestImages)
		assert.Equal(t, aiContextModel.DefaultToolResultMaxChars, *result.ToolResultMaxChars)
		assert.Equal(t, aiContextModel.DefaultThinkingPolicy, *result.ThinkingPolicy)
		assert.Equal(t, aiContextModel.DefaultImageTokenEstimate, *result.ImageTokenEstimate)
		require.NotNil(t, result.Rewrite)
		assert.Equal(t, "full", *result.Rewrite.Strength)
		assert.Equal(t, aiContextModel.DefaultRewriteProtectedSurvivalRate, *result.Rewrite.ProtectedSurvivalRate)
	})

	t.Run("empty body upserts the documented defaults", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-context-settings", strings.NewReader(`{}`))
		data, err := AIContextSettingsUpdateAction(req)
		require.NoError(t, err)

		result, ok := data.(*shared.AIContextSettingsParam)
		require.True(t, ok)
		assert.Equal(t, aiContextModel.DefaultTriggerRatio, *result.TriggerRatio)
		require.NotNil(t, result.Rewrite)
		assert.Equal(t, aiContextModel.DefaultRewriteStrength, *result.Rewrite.Strength)
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-context-settings", strings.NewReader("not-json"))
		_, err := AIContextSettingsUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("422 trigger_ratio out of range", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		body := `{"trigger_ratio": 1.5}`
		req := httptest.NewRequest(http.MethodPut, "/ai-context-settings", strings.NewReader(body))
		_, err := AIContextSettingsUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trigger_ratio")

		body = `{"trigger_ratio": 0}`
		req = httptest.NewRequest(http.MethodPut, "/ai-context-settings", strings.NewReader(body))
		_, err = AIContextSettingsUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("422 invalid enums and negative values", func(t *testing.T) {
		defer setupAIContextManager(&fakeAIContextStorager{}, &fakeAIContextSettingsStorager{})()

		cases := []string{
			`{"thinking_policy": "drop"}`,
			`{"rewrite": {"strength": "max"}}`,
			`{"keep_latest_images": -1}`,
			`{"tool_result_max_chars": -1}`,
			`{"chars_per_token": 0}`,
			`{"image_token_estimate": -1}`,
			`{"rewrite": {"protected_survival_rate": 0}}`,
		}
		for _, body := range cases {
			req := httptest.NewRequest(http.MethodPut, "/ai-context-settings", strings.NewReader(body))
			_, err := AIContextSettingsUpdateAction(req)
			require.Error(t, err, "body: %s", body)
		}
	})
}
