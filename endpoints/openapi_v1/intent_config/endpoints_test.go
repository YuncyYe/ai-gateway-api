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

package intent_config

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints/openapi_v1/internal/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iintent_config"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIntentConfigStorager emulates the singleton DB behavior: Upsert
// overwrites the only row (fixed id=1), Fetch reads it back.
type fakeIntentConfigStorager struct {
	config *iintent_config.IntentConfig
}

func (f *fakeIntentConfigStorager) Fetch(ctx context.Context) (*iintent_config.IntentConfig, error) {
	return f.config, nil
}

func (f *fakeIntentConfigStorager) Upsert(ctx context.Context, config *iintent_config.IntentConfig) error {
	f.config = config
	return nil
}

func setupIntentConfigManager(storager iintent_config.IntentConfigStorager) func() {
	old := container.IntentConfigManager
	container.IntentConfigManager = iintent_config.NewIntentConfigManager(&testutil.FakeTxn{}, storager, nil)
	return func() {
		container.IntentConfigManager = old
	}
}

var validPutBody = `{
	"min_confidence": 0.6,
	"questions": [
		{"name": "task_type", "type": "choice", "instructions": "这条请求属于哪类研发任务？",
		 "criteria": {"coding": "编写或修改代码", "test_writing": "编写测试用例", "doc_writing": "编写文档"}},
		{"name": "complexity", "type": "score", "instructions": "这个任务的复杂度如何？", "min_confidence": 0.7,
		 "levels": [{"name": "simple", "description": "单步即可完成"},
		            {"name": "medium", "description": "多步但模式常见"},
		            {"name": "complex", "description": "需要深入推理"}]}
	]
}`

func TestIntentConfigGetAction(t *testing.T) {
	t.Run("published config returns 200 shape without version", func(t *testing.T) {
		publishedAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
		store := &fakeIntentConfigStorager{
			config: &iintent_config.IntentConfig{
				Id:            iintent_config.IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.6,
				Questions:     `[{"name":"task_type","type":"choice","instructions":"i","criteria":{"coding":"d"}}]`,
				CreatedAt:     publishedAt,
				UpdatedAt:     publishedAt,
			},
		}
		defer setupIntentConfigManager(store)()

		req := httptest.NewRequest(http.MethodGet, "/intent-config", nil)
		data, err := IntentConfigGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"version"`)
		assert.NotContains(t, string(bs), `"id"`)

		param, ok := data.(*iintent_config.IntentConfigParam)
		require.True(t, ok)
		assert.InDelta(t, 0.6, *param.MinConfidence, 1e-9)
		assert.JSONEq(t, `[{"name":"task_type","type":"choice","instructions":"i","criteria":{"coding":"d"}}]`, string(param.Questions))
		assert.NotNil(t, param.CreatedAt)
		assert.NotNil(t, param.UpdatedAt)
	})

	t.Run("unpublished config returns record not exist", func(t *testing.T) {
		defer setupIntentConfigManager(&fakeIntentConfigStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/intent-config", nil)
		_, err := IntentConfigGetAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Record Not Exist")
	})

	t.Run("published empty questions returns the empty array", func(t *testing.T) {
		store := &fakeIntentConfigStorager{
			config: &iintent_config.IntentConfig{
				Id:            iintent_config.IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.6,
				Questions:     `[]`,
			},
		}
		defer setupIntentConfigManager(store)()

		req := httptest.NewRequest(http.MethodGet, "/intent-config", nil)
		data, err := IntentConfigGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"questions":[]`)
	})
}

func TestIntentConfigUpdateAction(t *testing.T) {
	t.Run("round trip publishes the singleton without exposing version", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(validPutBody))
		data, err := IntentConfigUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"version"`)
		assert.NotContains(t, string(bs), `"id"`)

		param, ok := data.(*iintent_config.IntentConfigParam)
		require.True(t, ok)
		assert.InDelta(t, 0.6, *param.MinConfidence, 1e-9)
		assert.Contains(t, string(param.Questions), `"task_type"`)

		// The singleton was persisted with the fixed id and a generated version.
		require.NotNil(t, store.config)
		assert.Equal(t, iintent_config.IntentConfigFixedID, store.config.Id)
		assert.NotEmpty(t, store.config.Version)
	})

	t.Run("omitted min_confidence backfills the 0.6 default", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		body := `{"questions": [{"name": "q", "type": "choice", "instructions": "i", "criteria": {"a": "b"}}]}`
		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(body))
		data, err := IntentConfigUpdateAction(req)
		require.NoError(t, err)

		param := data.(*iintent_config.IntentConfigParam)
		assert.InDelta(t, 0.6, *param.MinConfidence, 1e-9)
		assert.InDelta(t, 0.6, store.config.MinConfidence, 1e-9)
	})

	t.Run("empty questions array is accepted as the soft switch", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(`{"questions": []}`))
		data, err := IntentConfigUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"questions":[]`)
		assert.Equal(t, `[]`, store.config.Questions)
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		defer setupIntentConfigManager(&fakeIntentConfigStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader("not-json"))
		_, err := IntentConfigUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("invalid document is rejected and nothing is written", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		body := `{"questions": [{"name": "q", "type": "choice", "instructions": "i"}]}`
		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(body))
		_, err := IntentConfigUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "criteria count")
		assert.Nil(t, store.config)
	})

	t.Run("missing questions is rejected", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(`{"min_confidence": 0.6}`))
		_, err := IntentConfigUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "questions")
		assert.Nil(t, store.config)
	})

	t.Run("min_confidence out of range is rejected", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		body := `{"min_confidence": 1.5, "questions": []}`
		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(body))
		_, err := IntentConfigUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "out of range [0,1]")
		assert.Nil(t, store.config)
	})

	t.Run("duplicate question name is rejected", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		body := `{"questions": [
			{"name": "dup", "type": "choice", "instructions": "i", "criteria": {"a": "b"}},
			{"name": "dup", "type": "score", "instructions": "i", "levels": [{"name": "l", "description": "d"}]}
		]}`
		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(body))
		_, err := IntentConfigUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
		assert.Nil(t, store.config)
	})

	t.Run("option name containing pipe is rejected", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		defer setupIntentConfigManager(store)()

		body := `{"questions": [{"name": "q", "type": "choice", "instructions": "i", "criteria": {"a|b": "d"}}]}`
		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(body))
		_, err := IntentConfigUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "contains '|'")
		assert.Nil(t, store.config)
	})

	t.Run("storage failure is passed through", func(t *testing.T) {
		failing := &failingIntentConfigStorager{}
		defer setupIntentConfigManager(failing)()

		req := httptest.NewRequest(http.MethodPut, "/intent-config", strings.NewReader(validPutBody))
		_, err := IntentConfigUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}

type failingIntentConfigStorager struct{}

func (f *failingIntentConfigStorager) Fetch(ctx context.Context) (*iintent_config.IntentConfig, error) {
	return nil, errors.New("db down")
}

func (f *failingIntentConfigStorager) Upsert(ctx context.Context, config *iintent_config.IntentConfig) error {
	return errors.New("db down")
}

func TestIntentConfigRoutes(t *testing.T) {
	t.Run("routes carry the AIIntent permission points", func(t *testing.T) {
		require.Len(t, Endpoints, 2)

		assert.Equal(t, "/intent-config", IntentConfigGetRoute.Path)
		assert.Equal(t, http.MethodGet, IntentConfigGetRoute.Method)
		require.NotNil(t, IntentConfigGetRoute.Authorizer)
		require.NotNil(t, IntentConfigGetRoute.Authorizer.FeatureAuthorizer)
		assert.Equal(t, "AIIntent", string(IntentConfigGetRoute.Authorizer.FeatureAuthorizer.Feature))

		assert.Equal(t, "/intent-config", IntentConfigUpdateRoute.Path)
		assert.Equal(t, http.MethodPut, IntentConfigUpdateRoute.Method)
		require.NotNil(t, IntentConfigUpdateRoute.Authorizer)
		require.NotNil(t, IntentConfigUpdateRoute.Authorizer.FeatureAuthorizer)
		assert.Equal(t, "AIIntent", string(IntentConfigUpdateRoute.Authorizer.FeatureAuthorizer.Feature))
	})
}
