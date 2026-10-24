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

package mod_api_key

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints/innerapi_v1/internal/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/entity"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iai_route"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/imods"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/quota"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAPIKeyStoragerForRule struct{}

func (f *fakeAPIKeyStoragerForRule) FetchAPIKeyList(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
	return []*api_key.APIKeyParam{
		{
			ID:          lib.PString("key-1"),
			Key:         lib.PString("ak-key-1"),
			ProductName: lib.PString("AI_product"),
			Enable:      lib.PBool(true),
			KeyCreateAt: lib.PTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)),
		},
	}, nil
}

func (f *fakeAPIKeyStoragerForRule) CreateAPIKey(ctx context.Context, param *api_key.APIKeyParam) (int64, error) {
	return 0, nil
}

func (f *fakeAPIKeyStoragerForRule) UpdateAPIKey(ctx context.Context, filter *api_key.APIKeyFilter, param *api_key.APIKeyParam) (int64, error) {
	return 0, nil
}

func (f *fakeAPIKeyStoragerForRule) DeleteAPIKey(ctx context.Context, filter *api_key.APIKeyFilter) error {
	return nil
}

func (f *fakeAPIKeyStoragerForRule) CreateAPIKeyToken(ctx context.Context, param *api_key.APIKeyTokenParam) (int64, error) {
	return 0, nil
}

func (f *fakeAPIKeyStoragerForRule) UpdateAPIKeyToken(ctx context.Context, filter *api_key.APIKeyTokenFilter, param *api_key.APIKeyTokenParam) error {
	return nil
}

func (f *fakeAPIKeyStoragerForRule) FetchAPIKeyTokenList(ctx context.Context, filter *api_key.APIKeyTokenFilter) ([]*api_key.APIKeyTokenParam, error) {
	return nil, nil
}

type fakeAIRouteRuleStoragerForRule struct{}

func (f *fakeAIRouteRuleStoragerForRule) FetchAIRouteRules(ctx context.Context, filter *iai_route.AIRouteFilter) ([]*iai_route.Rule, error) {
	return nil, nil
}

func (f *fakeAIRouteRuleStoragerForRule) CreateAIRouteRules(ctx context.Context, param []*iai_route.Rule) error {
	return nil
}

type fakeQuotaPlanStoragerForRule struct{}

func (f *fakeQuotaPlanStoragerForRule) CreateQuotaPlan(ctx context.Context, param *quota.QuotaPlanParam) (int64, error) {
	return 0, nil
}

func (f *fakeQuotaPlanStoragerForRule) FetchQuotaPlan(ctx context.Context, filter *quota.QuotaPlanFilter) (*quota.QuotaPlanParam, error) {
	return nil, nil
}

func (f *fakeQuotaPlanStoragerForRule) FetchQuotaPlanList(ctx context.Context, filter *quota.QuotaPlanFilter) ([]*quota.QuotaPlanParam, error) {
	return nil, nil
}

func (f *fakeQuotaPlanStoragerForRule) UpdateQuotaPlan(ctx context.Context, filter *quota.QuotaPlanFilter, param *quota.QuotaPlanParam) (int64, error) {
	return 0, nil
}

func (f *fakeQuotaPlanStoragerForRule) DeleteQuotaPlan(ctx context.Context, filter *quota.QuotaPlanFilter) error {
	return nil
}

func (f *fakeQuotaPlanStoragerForRule) ClaimQuotaPlanReset(ctx context.Context, id int64, periodStart time.Time, now time.Time) (int64, error) {
	return 1, nil
}

type fakeEntityStoragerForRule struct{}

func (f *fakeEntityStoragerForRule) CreateEntity(ctx context.Context, param *entity.EntityParam) (int64, error) {
	return 0, nil
}

func (f *fakeEntityStoragerForRule) FetchEntity(ctx context.Context, filter *entity.EntityFilter) (*entity.EntityParam, error) {
	return nil, nil
}

func (f *fakeEntityStoragerForRule) FetchEntityList(ctx context.Context, filter *entity.EntityFilter) ([]*entity.EntityParam, error) {
	return nil, nil
}

func (f *fakeEntityStoragerForRule) UpdateEntity(ctx context.Context, filter *entity.EntityFilter, param *entity.EntityParam) (int64, error) {
	return 0, nil
}

func (f *fakeEntityStoragerForRule) DeleteEntity(ctx context.Context, filter *entity.EntityFilter) error {
	return nil
}

type fakeEntityTypeStoragerForRule struct{}

func (f *fakeEntityTypeStoragerForRule) CreateEntityType(ctx context.Context, param *entity.EntityTypeParam) (int64, error) {
	return 0, nil
}

func (f *fakeEntityTypeStoragerForRule) FetchEntityType(ctx context.Context, filter *entity.EntityTypeFilter) (*entity.EntityTypeParam, error) {
	return nil, nil
}

func (f *fakeEntityTypeStoragerForRule) FetchEntityTypeList(ctx context.Context, filter *entity.EntityTypeFilter) ([]*entity.EntityTypeParam, error) {
	return nil, nil
}

func (f *fakeEntityTypeStoragerForRule) UpdateEntityType(ctx context.Context, filter *entity.EntityTypeFilter, param *entity.EntityTypeParam) (int64, error) {
	return 0, nil
}

func (f *fakeEntityTypeStoragerForRule) DeleteEntityType(ctx context.Context, filter *entity.EntityTypeFilter) error {
	return nil
}

func setupAPIKeyRuleManager(version string) func() {
	origConfig := stateful.DefaultConfig
	stateful.DefaultConfig = &stateful.Config{
		RunTime: stateful.RunTimeConfig{
			AIRouteInnerProductName: "AI_product",
		},
	}

	old := container.APIKeyRuleManager
	container.APIKeyRuleManager = imods.NewAPIKeyRuleManager(
		&testutil.FakeTxn{},
		testutil.NewVersionControlManager(version),
		&fakeAPIKeyStoragerForRule{},
		&fakeAIRouteRuleStoragerForRule{},
		&fakeQuotaPlanStoragerForRule{},
		&fakeEntityStoragerForRule{},
		&fakeEntityTypeStoragerForRule{}, nil)
	return func() {
		container.APIKeyRuleManager = old
		stateful.DefaultConfig = origConfig
	}
}

func TestExportAction(t *testing.T) {
	defer setupAPIKeyRuleManager("v2")()

	req := httptest.NewRequest(http.MethodGet, "/configs/mod-api-key?version=", nil)
	data, err := ExportAction(req)

	require.NoError(t, err)
	require.NotNil(t, data)

	conf, ok := data.(*imods.ModAPIKeyRuleConf)
	require.True(t, ok)
	assert.Equal(t, "v2", *conf.Version)
	assert.Contains(t, conf.Config, "AI_product")
	require.Contains(t, conf.Tokens["AI_product"], "ak-key-1")
	assert.Equal(t, "key-1", conf.Tokens["AI_product"]["ak-key-1"].KeyID)
}

func TestExportAction_VersionNotChanged(t *testing.T) {
	defer setupAPIKeyRuleManager("v1")()

	req := httptest.NewRequest(http.MethodGet, "/configs/mod-api-key?version=v1", nil)
	data, err := ExportAction(req)

	require.NoError(t, err)
	assert.Nil(t, data)
}
