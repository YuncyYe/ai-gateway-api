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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints/openapi_v1/internal/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	aiCacheModel "github.com/rainway-ai-gateway/ai-gateway-api/model/ai_cache"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAICacheStorager emulates the DB behavior: ReplaceAll rebuilds the table
// (new ids in slice order), FetchAll reads it back.
type fakeAICacheStorager struct {
	rules  []*aiCacheModel.AICacheRuleParam
	nextID int64
}

func (f *fakeAICacheStorager) FetchAll(ctx context.Context) ([]*aiCacheModel.AICacheRuleParam, error) {
	return f.rules, nil
}

func (f *fakeAICacheStorager) ReplaceAll(ctx context.Context, rules []*aiCacheModel.AICacheRuleParam) error {
	f.rules = make([]*aiCacheModel.AICacheRuleParam, 0, len(rules))
	for _, r := range rules {
		f.nextID++
		f.rules = append(f.rules, &aiCacheModel.AICacheRuleParam{
			ID:                  lib.PInt64(f.nextID),
			Name:                r.Name,
			Cond:                r.Cond,
			CacheKeyStrategy:    r.CacheKeyStrategy,
			CacheTTL:            r.CacheTTL,
			MaxBodyBytes:        r.MaxBodyBytes,
			MaxValueBytes:       r.MaxValueBytes,
			EnableSemanticCache: r.EnableSemanticCache,
		})
	}
	return nil
}

// fakeAICacheSemanticSettingsStorager emulates the singleton settings table:
// Get returns the in-memory row (nil when empty), Upsert overwrites it.
type fakeAICacheSemanticSettingsStorager struct {
	row *aiCacheModel.SemanticSettingsRow
}

func (f *fakeAICacheSemanticSettingsStorager) Get(ctx context.Context) (*aiCacheModel.SemanticSettingsRow, error) {
	return f.row, nil
}

func (f *fakeAICacheSemanticSettingsStorager) Upsert(ctx context.Context, row *aiCacheModel.SemanticSettingsRow) error {
	now := time.Date(2026, 9, 30, 10, 30, 0, 0, time.UTC)
	row.ID = lib.PInt64(1)
	row.CreatedAt = &now
	row.UpdatedAt = &now
	f.row = row
	return nil
}

var _ aiCacheModel.AICacheSemanticSettingsStorager = (*fakeAICacheSemanticSettingsStorager)(nil)

func setupAICacheManager(storager aiCacheModel.AICacheStorager, settingsStorager aiCacheModel.AICacheSemanticSettingsStorager) func() {
	old := container.AICacheManager
	container.AICacheManager = aiCacheModel.NewAICacheManager(&testutil.FakeTxn{}, storager, settingsStorager, nil, "AI_product")
	return func() {
		container.AICacheManager = old
	}
}

func TestAICacheRulesGetAction(t *testing.T) {
	t.Run("returns rule collection without internal id", func(t *testing.T) {
		store := &fakeAICacheStorager{
			nextID: 2,
			rules: []*aiCacheModel.AICacheRuleParam{
				{
					ID:               lib.PInt64(1),
					Name:             lib.PString("rule1"),
					Cond:             lib.PString("default_t()"),
					CacheKeyStrategy: lib.PString("lastQuestion"),
					CacheTTL:         lib.PInt(3600),
					MaxBodyBytes:     lib.PInt64(1048576),
					MaxValueBytes:    lib.PInt64(1048576),
				},
			},
		}
		defer setupAICacheManager(store, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/ai-cache-rules", nil)
		data, err := AICacheRulesGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)
		assert.NotContains(t, string(bs), `"created_at"`, "per-rule timestamps are not part of the contract (issue #258)")
		assert.NotContains(t, string(bs), `"updated_at"`, "per-rule timestamps are not part of the contract (issue #258)")

		result, ok := data.(*shared.AICacheRulesParam)
		require.True(t, ok)
		require.Len(t, result.Rules, 1)
		assert.Equal(t, "rule1", *result.Rules[0].Name)
	})

	t.Run("empty collection returns empty rules array", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/ai-cache-rules", nil)
		data, err := AICacheRulesGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"rules":[]`)
	})
}

func TestAICacheRulesUpdateAction(t *testing.T) {
	validBody := `{
		"rules": [
			{
				"name": "cache-deepseek-chat",
				"cond": "req_path_in(\"/v1/chat/completions\", false) && req_body_json_in(\"model\", \"deepseek-chat\", false)",
				"cache_key_strategy": "lastQuestion",
				"cache_ttl": 3600,
				"max_body_bytes": 1048576,
				"max_value_bytes": 1048576
			},
			{
				"name": "cache-all-models-long-ttl",
				"cond": "req_path_in(\"/v1/chat/completions\", false)",
				"cache_ttl": 86400
			}
		]
	}`

	t.Run("round trip with default backfill and no internal id", func(t *testing.T) {
		store := &fakeAICacheStorager{}
		defer setupAICacheManager(store, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(validBody))
		data, err := AICacheRulesUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)

		result, ok := data.(*shared.AICacheRulesParam)
		require.True(t, ok)
		require.Len(t, result.Rules, 2)

		assert.Equal(t, "cache-deepseek-chat", *result.Rules[0].Name)
		assert.Equal(t, "lastQuestion", *result.Rules[0].CacheKeyStrategy)
		assert.Equal(t, 3600, *result.Rules[0].CacheTTL)

		// Omitted optional fields are backfilled with defaults.
		assert.Equal(t, "cache-all-models-long-ttl", *result.Rules[1].Name)
		assert.Equal(t, "lastQuestion", *result.Rules[1].CacheKeyStrategy)
		assert.Equal(t, 86400, *result.Rules[1].CacheTTL)
		assert.Equal(t, int64(1048576), *result.Rules[1].MaxBodyBytes)
		assert.Equal(t, int64(1048576), *result.Rules[1].MaxValueBytes)
		assert.NotContains(t, string(bs), `"created_at"`)
		assert.NotContains(t, string(bs), `"updated_at"`)
	})

	t.Run("empty rules clear the collection", func(t *testing.T) {
		store := &fakeAICacheStorager{}
		defer setupAICacheManager(store, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(`{"rules": []}`))
		data, err := AICacheRulesUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"rules":[]`)
	})

	t.Run("round trip with enable_semantic_cache", func(t *testing.T) {
		store := &fakeAICacheStorager{}
		defer setupAICacheManager(store, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [
			{"name": "semantic-on", "cond": "default_t()", "enable_semantic_cache": true},
			{"name": "semantic-off-explicit", "cond": "default_t()", "enable_semantic_cache": false}
		]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		data, err := AICacheRulesUpdateAction(req)
		require.NoError(t, err)

		result, ok := data.(*shared.AICacheRulesParam)
		require.True(t, ok)
		require.Len(t, result.Rules, 2)
		assert.Equal(t, true, *result.Rules[0].EnableSemanticCache)
		assert.Equal(t, false, *result.Rules[1].EnableSemanticCache)

		// Omitted flag defaults to false on read-back.
		body = `{"rules": [{"name": "semantic-omitted", "cond": "default_t()"}]}`
		req = httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		data, err = AICacheRulesUpdateAction(req)
		require.NoError(t, err)
		result = data.(*shared.AICacheRulesParam)
		require.Len(t, result.Rules, 1)
		assert.Equal(t, false, *result.Rules[0].EnableSemanticCache)
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader("not-json"))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("422 invalid cond", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [{"name": "bad-cond", "cond": "unknown_func()"}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad-cond")
	})

	t.Run("422 duplicate name in collection", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [
			{"name": "dup", "cond": "default_t()"},
			{"name": "dup", "cond": "req_path_in(\"/v1\", false)"}
		]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("422 invalid cache_key_strategy enum", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [{"name": "bad-enum", "cond": "default_t()", "cache_key_strategy": "firstQuestion"}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cache_key_strategy")
	})

	t.Run("422 negative cache_ttl", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [{"name": "bad-ttl", "cond": "default_t()", "cache_ttl": -1}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cache_ttl")
	})

	t.Run("422 non-positive byte limits", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [{"name": "bad-bytes", "cond": "default_t()", "max_body_bytes": 0}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_body_bytes")

		body = `{"rules": [{"name": "bad-bytes", "cond": "default_t()", "max_value_bytes": -1}]}`
		req = httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err = AICacheRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_value_bytes")
	})

	t.Run("422 null rule element", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [null]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("422 empty name and oversized name", func(t *testing.T) {
		defer setupAICacheManager(&fakeAICacheStorager{}, &fakeAICacheSemanticSettingsStorager{})()

		body := `{"rules": [{"name": "", "cond": "default_t()"}]}`
		req := httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err := AICacheRulesUpdateAction(req)
		require.Error(t, err)

		longName := strings.Repeat("a", 129)
		body = `{"rules": [{"name": "` + longName + `", "cond": "default_t()"}]}`
		req = httptest.NewRequest(http.MethodPut, "/ai-cache-rules", strings.NewReader(body))
		_, err = AICacheRulesUpdateAction(req)
		require.Error(t, err)
	})
}
