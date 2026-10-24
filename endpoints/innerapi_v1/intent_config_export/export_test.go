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

package intent_config_export

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints/innerapi_v1/internal/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iintent_config"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func setupIntentConfigManager(version string) func() {
	old := container.IntentConfigManager
	vcm := testutil.NewVersionControlManager(version)
	container.IntentConfigManager = iintent_config.NewIntentConfigManager(&testutil.FakeTxn{}, &fakeIntentConfigStorager{
		config: &iintent_config.IntentConfig{
			Id:            iintent_config.IntentConfigFixedID,
			Version:       "20260926120000",
			MinConfidence: 0.6,
			Questions:     `[{"name":"task_type","type":"choice","instructions":"i","criteria":{"coding":"d"}}]`,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		},
	}, vcm)
	return func() {
		container.IntentConfigManager = old
	}
}

func TestExportAction(t *testing.T) {
	t.Run("exports the PascalCase file content", func(t *testing.T) {
		defer setupIntentConfigManager("20260926130000")()

		req := httptest.NewRequest(http.MethodGet, "/configs/mod-ai-intent?version=", nil)
		data, err := ExportAction(req)
		require.NoError(t, err)
		require.NotNil(t, data)

		conf, ok := data.(*iintent_config.IntentConfigDataExport)
		require.True(t, ok)
		assert.Equal(t, "20260926130000", conf.Version)
		assert.InDelta(t, 0.6, conf.MinConfidence, 1e-9)
		require.Len(t, conf.Questions, 1)
		assert.Equal(t, "task_type", conf.Questions[0].Name)
		assert.Equal(t, map[string]string{"coding": "d"}, conf.Questions[0].Criteria)
	})

	t.Run("version not changed exports nil", func(t *testing.T) {
		defer setupIntentConfigManager("20260926120000")()

		req := httptest.NewRequest(http.MethodGet, "/configs/mod-ai-intent?version=20260926120000", nil)
		data, err := ExportAction(req)
		require.NoError(t, err)
		assert.Nil(t, data)
	})

	t.Run("unpublished config exports nil", func(t *testing.T) {
		old := container.IntentConfigManager
		vcm := testutil.NewVersionControlManager("20260926130000")
		container.IntentConfigManager = iintent_config.NewIntentConfigManager(&testutil.FakeTxn{}, &fakeIntentConfigStorager{}, vcm)
		defer func() { container.IntentConfigManager = old }()

		req := httptest.NewRequest(http.MethodGet, "/configs/mod-ai-intent?version=", nil)
		data, err := ExportAction(req)
		require.NoError(t, err)
		assert.Nil(t, data)
	})
}
