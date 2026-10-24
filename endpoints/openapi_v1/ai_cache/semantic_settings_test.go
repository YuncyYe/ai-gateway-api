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

package ai_cache

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	aiCacheModel "github.com/rainway-ai-gateway/ai-gateway-api/model/ai_cache"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAICacheSemanticSettingsGetAction(t *testing.T) {
	t.Run("empty table returns documented defaults without internal id", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/ai-cache-semantic-settings", nil)
		data, err := AICacheSemanticSettingsGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)
		assert.NotContains(t, string(bs), `"created_at"`)
		assert.NotContains(t, string(bs), `"updated_at"`)

		result, ok := data.(*shared.AICacheSemanticSettingsParam)
		require.True(t, ok)
		assert.Equal(t, aiCacheModel.DefaultSemanticTopK, *result.TopK)
		assert.Equal(t, aiCacheModel.DefaultSemanticThreshold, *result.Threshold)
		assert.Equal(t, aiCacheModel.DefaultSemanticThresholdRelation, *result.ThresholdRelation)
	})

	t.Run("existing row returns stored values with timestamps", func(t *testing.T) {
		now := time.Date(2026, 9, 30, 10, 30, 0, 0, time.UTC)
		settingsStore := &fakeAICacheSemanticSettingsStorager{
			row: &aiCacheModel.SemanticSettingsRow{
				ID:                lib.PInt64(1),
				TopK:              lib.PInt(6),
				Threshold:         lib.PFloat64(0.4),
				ThresholdRelation: lib.PString("lte"),
				CreatedAt:         &now,
				UpdatedAt:         &now,
			},
		}
		defer setupAICacheManager(&fakeAICacheStorager{}, settingsStore)()

		req := httptest.NewRequest(http.MethodGet, "/ai-cache-semantic-settings", nil)
		data, err := AICacheSemanticSettingsGetAction(req)
		require.NoError(t, err)

		result, ok := data.(*shared.AICacheSemanticSettingsParam)
		require.True(t, ok)
		assert.Equal(t, 6, *result.TopK)
		assert.Equal(t, 0.4, *result.Threshold)
		assert.Equal(t, "lte", *result.ThresholdRelation)
		assert.NotNil(t, result.CreatedAt)
		assert.NotNil(t, result.UpdatedAt)
	})
}

func TestAICacheSemanticSettingsUpdateAction(t *testing.T) {
	t.Run("put then get round trip", func(t *testing.T) {
		settingsStore := &fakeAICacheSemanticSettingsStorager{}
		defer setupAICacheManager(&fakeAICacheStorager{}, settingsStore)()

		body := `{"top_k": 5, "threshold": 1.5, "threshold_relation": "gt"}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-semantic-settings", strings.NewReader(body))
		data, err := AICacheSemanticSettingsUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)

		result, ok := data.(*shared.AICacheSemanticSettingsParam)
		require.True(t, ok)
		assert.Equal(t, 5, *result.TopK)
		assert.Equal(t, 1.5, *result.Threshold)
		assert.Equal(t, "gt", *result.ThresholdRelation)
		assert.NotNil(t, result.CreatedAt)
		assert.NotNil(t, result.UpdatedAt)

		// GET returns the same values.
		req = httptest.NewRequest(http.MethodGet, "/ai-cache-semantic-settings", nil)
		data, err = AICacheSemanticSettingsGetAction(req)
		require.NoError(t, err)
		result = data.(*shared.AICacheSemanticSettingsParam)
		assert.Equal(t, 5, *result.TopK)
		assert.Equal(t, 1.5, *result.Threshold)
		assert.Equal(t, "gt", *result.ThresholdRelation)
	})

	t.Run("omitted fields are filled with defaults", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-cache-semantic-settings", strings.NewReader(`{}`))
		data, err := AICacheSemanticSettingsUpdateAction(req)
		require.NoError(t, err)

		result, ok := data.(*shared.AICacheSemanticSettingsParam)
		require.True(t, ok)
		assert.Equal(t, aiCacheModel.DefaultSemanticTopK, *result.TopK)
		assert.Equal(t, aiCacheModel.DefaultSemanticThreshold, *result.Threshold)
		assert.Equal(t, aiCacheModel.DefaultSemanticThresholdRelation, *result.ThresholdRelation)
	})

	t.Run("422 top_k out of range", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		for _, body := range []string{
			`{"top_k": 0}`,
			`{"top_k": 11}`,
			`{"top_k": -1}`,
		} {
			req := httptest.NewRequest(http.MethodPut, "/ai-cache-semantic-settings", strings.NewReader(body))
			_, err := AICacheSemanticSettingsUpdateAction(req)
			require.Error(t, err, body)
			assert.Contains(t, err.Error(), "top_k")
		}
	})

	t.Run("422 threshold out of range", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		for _, body := range []string{
			`{"threshold": -0.1}`,
			`{"threshold": 2.1}`,
		} {
			req := httptest.NewRequest(http.MethodPut, "/ai-cache-semantic-settings", strings.NewReader(body))
			_, err := AICacheSemanticSettingsUpdateAction(req)
			require.Error(t, err, body)
			assert.Contains(t, err.Error(), "threshold")
		}
	})

	t.Run("422 illegal threshold_relation enum", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		for _, body := range []string{
			`{"threshold_relation": "LT"}`,
			`{"threshold_relation": "between"}`,
			`{"threshold_relation": ""}`,
		} {
			req := httptest.NewRequest(http.MethodPut, "/ai-cache-semantic-settings", strings.NewReader(body))
			_, err := AICacheSemanticSettingsUpdateAction(req)
			require.Error(t, err, body)
			assert.Contains(t, err.Error(), "threshold_relation")
		}
	})

	t.Run("boundary values are accepted", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		for _, body := range []string{
			`{"top_k": 1, "threshold": 0, "threshold_relation": "lt"}`,
			`{"top_k": 10, "threshold": 2, "threshold_relation": "lte"}`,
			`{"threshold_relation": "gte"}`,
		} {
			req := httptest.NewRequest(http.MethodPut, "/ai-cache-semantic-settings", strings.NewReader(body))
			_, err := AICacheSemanticSettingsUpdateAction(req)
			require.NoError(t, err, body)
		}
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-cache-semantic-settings", strings.NewReader("not-json"))
		_, err := AICacheSemanticSettingsUpdateAction(req)
		require.Error(t, err)
	})
}
