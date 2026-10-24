// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package imods

import (
	"context"
	"sync"
	"time"

	"github.com/bfenetworks/go-lib/log/log4go"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/entity"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iai_route"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/itxn"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/quota"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// fakeTxn is a TxnStorager mock that executes the callback directly.
type fakeTxn struct{}

func (f *fakeTxn) AtomExecute(ctx context.Context, do func(context.Context) error) error {
	return do(ctx)
}

var _ itxn.TxnStorager = (*fakeTxn)(nil)

// fakeAPIKeyStorager implements api_key.APIKeyStorager.
type fakeAPIKeyStorager struct {
	fetchListFn      func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error)
	createFn         func(ctx context.Context, param *api_key.APIKeyParam) (int64, error)
	updateFn         func(ctx context.Context, filter *api_key.APIKeyFilter, param *api_key.APIKeyParam) (int64, error)
	deleteFn         func(ctx context.Context, filter *api_key.APIKeyFilter) error
	createTokenFn    func(ctx context.Context, param *api_key.APIKeyTokenParam) (int64, error)
	updateTokenFn    func(ctx context.Context, filter *api_key.APIKeyTokenFilter, param *api_key.APIKeyTokenParam) error
	fetchTokenListFn func(ctx context.Context, filter *api_key.APIKeyTokenFilter) ([]*api_key.APIKeyTokenParam, error)
}

func (s *fakeAPIKeyStorager) FetchAPIKeyList(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
	if s.fetchListFn != nil {
		return s.fetchListFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeAPIKeyStorager) CreateAPIKey(ctx context.Context, param *api_key.APIKeyParam) (int64, error) {
	if s.createFn != nil {
		return s.createFn(ctx, param)
	}
	return 0, nil
}

func (s *fakeAPIKeyStorager) UpdateAPIKey(ctx context.Context, filter *api_key.APIKeyFilter, param *api_key.APIKeyParam) (int64, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, filter, param)
	}
	return 0, nil
}

func (s *fakeAPIKeyStorager) DeleteAPIKey(ctx context.Context, filter *api_key.APIKeyFilter) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, filter)
	}
	return nil
}

func (s *fakeAPIKeyStorager) CreateAPIKeyToken(ctx context.Context, param *api_key.APIKeyTokenParam) (int64, error) {
	if s.createTokenFn != nil {
		return s.createTokenFn(ctx, param)
	}
	return 0, nil
}

func (s *fakeAPIKeyStorager) UpdateAPIKeyToken(ctx context.Context, filter *api_key.APIKeyTokenFilter, param *api_key.APIKeyTokenParam) error {
	if s.updateTokenFn != nil {
		return s.updateTokenFn(ctx, filter, param)
	}
	return nil
}

func (s *fakeAPIKeyStorager) FetchAPIKeyTokenList(ctx context.Context, filter *api_key.APIKeyTokenFilter) ([]*api_key.APIKeyTokenParam, error) {
	if s.fetchTokenListFn != nil {
		return s.fetchTokenListFn(ctx, filter)
	}
	return nil, nil
}

var _ api_key.APIKeyStorager = (*fakeAPIKeyStorager)(nil)

// fakeAIRouteRuleStorager implements iai_route.AIRouteRuleStorager.
type fakeAIRouteRuleStorager struct {
	fetchFn  func(ctx context.Context, filter *iai_route.AIRouteFilter) ([]*iai_route.Rule, error)
	createFn func(ctx context.Context, param []*iai_route.Rule) error
	mu       sync.Mutex
	created  [][]*iai_route.Rule
	fetched  []*iai_route.AIRouteFilter
}

func (s *fakeAIRouteRuleStorager) FetchAIRouteRules(ctx context.Context, filter *iai_route.AIRouteFilter) ([]*iai_route.Rule, error) {
	s.mu.Lock()
	s.fetched = append(s.fetched, filter)
	s.mu.Unlock()
	if s.fetchFn != nil {
		return s.fetchFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeAIRouteRuleStorager) CreateAIRouteRules(ctx context.Context, param []*iai_route.Rule) error {
	s.mu.Lock()
	s.created = append(s.created, param)
	s.mu.Unlock()
	if s.createFn != nil {
		return s.createFn(ctx, param)
	}
	return nil
}

var _ iai_route.AIRouteRuleStorager = (*fakeAIRouteRuleStorager)(nil)

// fakeQuotaPlanStorager implements quota.QuotaPlanStorager.
type fakeQuotaPlanStorager struct {
	createFn func(ctx context.Context, param *quota.QuotaPlanParam) (int64, error)
	fetchFn  func(ctx context.Context, filter *quota.QuotaPlanFilter) (*quota.QuotaPlanParam, error)
	listFn   func(ctx context.Context, filter *quota.QuotaPlanFilter) ([]*quota.QuotaPlanParam, error)
	updateFn func(ctx context.Context, filter *quota.QuotaPlanFilter, param *quota.QuotaPlanParam) (int64, error)
	deleteFn func(ctx context.Context, filter *quota.QuotaPlanFilter) error

	mu      sync.Mutex
	created []*quota.QuotaPlanParam
	fetched []*quota.QuotaPlanFilter
	listed  []*quota.QuotaPlanFilter
	updated []updateQuotaPlanCall
	deleted []*quota.QuotaPlanFilter
}

type updateQuotaPlanCall struct {
	filter *quota.QuotaPlanFilter
	param  *quota.QuotaPlanParam
}

func (s *fakeQuotaPlanStorager) CreateQuotaPlan(ctx context.Context, param *quota.QuotaPlanParam) (int64, error) {
	s.mu.Lock()
	s.created = append(s.created, param)
	s.mu.Unlock()
	if s.createFn != nil {
		return s.createFn(ctx, param)
	}
	return 0, nil
}

func (s *fakeQuotaPlanStorager) FetchQuotaPlan(ctx context.Context, filter *quota.QuotaPlanFilter) (*quota.QuotaPlanParam, error) {
	s.mu.Lock()
	s.fetched = append(s.fetched, filter)
	s.mu.Unlock()
	if s.fetchFn != nil {
		return s.fetchFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeQuotaPlanStorager) FetchQuotaPlanList(ctx context.Context, filter *quota.QuotaPlanFilter) ([]*quota.QuotaPlanParam, error) {
	s.mu.Lock()
	s.listed = append(s.listed, filter)
	s.mu.Unlock()
	if s.listFn != nil {
		return s.listFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeQuotaPlanStorager) UpdateQuotaPlan(ctx context.Context, filter *quota.QuotaPlanFilter, param *quota.QuotaPlanParam) (int64, error) {
	s.mu.Lock()
	s.updated = append(s.updated, updateQuotaPlanCall{filter: filter, param: param})
	s.mu.Unlock()
	if s.updateFn != nil {
		return s.updateFn(ctx, filter, param)
	}
	return 0, nil
}

func (s *fakeQuotaPlanStorager) DeleteQuotaPlan(ctx context.Context, filter *quota.QuotaPlanFilter) error {
	s.mu.Lock()
	s.deleted = append(s.deleted, filter)
	s.mu.Unlock()
	if s.deleteFn != nil {
		return s.deleteFn(ctx, filter)
	}
	return nil
}

func (s *fakeQuotaPlanStorager) ClaimQuotaPlanReset(ctx context.Context, id int64, periodStart time.Time, now time.Time) (int64, error) {
	return 1, nil
}

var _ quota.QuotaPlanStorager = (*fakeQuotaPlanStorager)(nil)

// fakeEntityStorager implements entity.EntityStorager.
type fakeEntityStorager struct {
	createFn func(ctx context.Context, param *entity.EntityParam) (int64, error)
	fetchFn  func(ctx context.Context, filter *entity.EntityFilter) (*entity.EntityParam, error)
	listFn   func(ctx context.Context, filter *entity.EntityFilter) ([]*entity.EntityParam, error)
	updateFn func(ctx context.Context, filter *entity.EntityFilter, param *entity.EntityParam) (int64, error)
	deleteFn func(ctx context.Context, filter *entity.EntityFilter) error

	mu      sync.Mutex
	created []*entity.EntityParam
	fetched []*entity.EntityFilter
	listed  []*entity.EntityFilter
	updated []updateEntityCall
	deleted []*entity.EntityFilter
}

type updateEntityCall struct {
	filter *entity.EntityFilter
	param  *entity.EntityParam
}

func (s *fakeEntityStorager) CreateEntity(ctx context.Context, param *entity.EntityParam) (int64, error) {
	s.mu.Lock()
	s.created = append(s.created, param)
	s.mu.Unlock()
	if s.createFn != nil {
		return s.createFn(ctx, param)
	}
	return 0, nil
}

func (s *fakeEntityStorager) FetchEntity(ctx context.Context, filter *entity.EntityFilter) (*entity.EntityParam, error) {
	s.mu.Lock()
	s.fetched = append(s.fetched, filter)
	s.mu.Unlock()
	if s.fetchFn != nil {
		return s.fetchFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeEntityStorager) FetchEntityList(ctx context.Context, filter *entity.EntityFilter) ([]*entity.EntityParam, error) {
	s.mu.Lock()
	s.listed = append(s.listed, filter)
	s.mu.Unlock()
	if s.listFn != nil {
		return s.listFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeEntityStorager) UpdateEntity(ctx context.Context, filter *entity.EntityFilter, param *entity.EntityParam) (int64, error) {
	s.mu.Lock()
	s.updated = append(s.updated, updateEntityCall{filter: filter, param: param})
	s.mu.Unlock()
	if s.updateFn != nil {
		return s.updateFn(ctx, filter, param)
	}
	return 0, nil
}

func (s *fakeEntityStorager) DeleteEntity(ctx context.Context, filter *entity.EntityFilter) error {
	s.mu.Lock()
	s.deleted = append(s.deleted, filter)
	s.mu.Unlock()
	if s.deleteFn != nil {
		return s.deleteFn(ctx, filter)
	}
	return nil
}

var _ entity.EntityStorager = (*fakeEntityStorager)(nil)

// fakeEntityTypeStorager implements entity.EntityTypeStorager.
type fakeEntityTypeStorager struct {
	createFn func(ctx context.Context, param *entity.EntityTypeParam) (int64, error)
	fetchFn  func(ctx context.Context, filter *entity.EntityTypeFilter) (*entity.EntityTypeParam, error)
	listFn   func(ctx context.Context, filter *entity.EntityTypeFilter) ([]*entity.EntityTypeParam, error)
	updateFn func(ctx context.Context, filter *entity.EntityTypeFilter, param *entity.EntityTypeParam) (int64, error)
	deleteFn func(ctx context.Context, filter *entity.EntityTypeFilter) error
}

func (s *fakeEntityTypeStorager) CreateEntityType(ctx context.Context, param *entity.EntityTypeParam) (int64, error) {
	if s.createFn != nil {
		return s.createFn(ctx, param)
	}
	return 0, nil
}

func (s *fakeEntityTypeStorager) FetchEntityType(ctx context.Context, filter *entity.EntityTypeFilter) (*entity.EntityTypeParam, error) {
	if s.fetchFn != nil {
		return s.fetchFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeEntityTypeStorager) FetchEntityTypeList(ctx context.Context, filter *entity.EntityTypeFilter) ([]*entity.EntityTypeParam, error) {
	if s.listFn != nil {
		return s.listFn(ctx, filter)
	}
	return nil, nil
}

func (s *fakeEntityTypeStorager) UpdateEntityType(ctx context.Context, filter *entity.EntityTypeFilter, param *entity.EntityTypeParam) (int64, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, filter, param)
	}
	return 0, nil
}

func (s *fakeEntityTypeStorager) DeleteEntityType(ctx context.Context, filter *entity.EntityTypeFilter) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, filter)
	}
	return nil
}

var _ entity.EntityTypeStorager = (*fakeEntityTypeStorager)(nil)

// fakeVersionControlStorager implements iversion_control.VersionControlStorager.
type fakeVersionControlStorager struct {
	upsertFn func(ctx context.Context, css *iversion_control.ExportData) (string, error)
	mu       sync.Mutex
	upserted []*iversion_control.ExportData
}

func (s *fakeVersionControlStorager) UpsertConfigLastExportedVersion(ctx context.Context, css *iversion_control.ExportData) (string, error) {
	s.mu.Lock()
	s.upserted = append(s.upserted, css)
	s.mu.Unlock()
	if s.upsertFn != nil {
		return s.upsertFn(ctx, css)
	}
	return "v1", nil
}

var _ iversion_control.VersionControlStorager = (*fakeVersionControlStorager)(nil)

// fakeRouteRulesStorager implements shared.RouteRulesStorager.
type fakeRouteRulesStorager struct {
	createFn    func(ctx context.Context, ruleType string, owner *string, param *shared.RouteRulesParam) (int64, error)
	fetchFn     func(ctx context.Context, ruleType string, owner *string) (*shared.RouteRulesParam, error)
	listFn      func(ctx context.Context, filter *shared.RouteRulesFilter) ([]*shared.RouteTableParam, int64, error)
	updateFn    func(ctx context.Context, id int64, param *shared.RouteRulesParam) (int64, error)
	deleteFn    func(ctx context.Context, id int64) error
	fetchByIDFn func(ctx context.Context, id int64) (*shared.RouteRulesParam, error)
	allFn       func(ctx context.Context) ([]*shared.RouteRulesParam, error)

	mu          sync.Mutex
	created     []createRouteRulesCall
	fetched     []fetchRouteRulesCall
	listed      []*shared.RouteRulesFilter
	updated     []updateRouteRulesCall
	deleted     []int64
	fetchedByID []int64
	fetchedAll  int
}

type createRouteRulesCall struct {
	ruleType string
	owner    *string
	param    *shared.RouteRulesParam
}

type fetchRouteRulesCall struct {
	ruleType string
	owner    *string
}

type updateRouteRulesCall struct {
	id    int64
	param *shared.RouteRulesParam
}

func (s *fakeRouteRulesStorager) CreateRouteRules(ctx context.Context, ruleType string, owner *string, param *shared.RouteRulesParam) (int64, error) {
	s.mu.Lock()
	s.created = append(s.created, createRouteRulesCall{ruleType: ruleType, owner: owner, param: param})
	s.mu.Unlock()
	if s.createFn != nil {
		return s.createFn(ctx, ruleType, owner, param)
	}
	return 0, nil
}

func (s *fakeRouteRulesStorager) FetchRouteRules(ctx context.Context, ruleType string, owner *string) (*shared.RouteRulesParam, error) {
	s.mu.Lock()
	s.fetched = append(s.fetched, fetchRouteRulesCall{ruleType: ruleType, owner: owner})
	s.mu.Unlock()
	if s.fetchFn != nil {
		return s.fetchFn(ctx, ruleType, owner)
	}
	return nil, nil
}

func (s *fakeRouteRulesStorager) FetchRouteRulesList(ctx context.Context, filter *shared.RouteRulesFilter) ([]*shared.RouteTableParam, int64, error) {
	s.mu.Lock()
	s.listed = append(s.listed, filter)
	s.mu.Unlock()
	if s.listFn != nil {
		return s.listFn(ctx, filter)
	}
	return nil, 0, nil
}

func (s *fakeRouteRulesStorager) UpdateRouteRules(ctx context.Context, id int64, param *shared.RouteRulesParam) (int64, error) {
	s.mu.Lock()
	s.updated = append(s.updated, updateRouteRulesCall{id: id, param: param})
	s.mu.Unlock()
	if s.updateFn != nil {
		return s.updateFn(ctx, id, param)
	}
	return 0, nil
}

func (s *fakeRouteRulesStorager) DeleteRouteRules(ctx context.Context, id int64) error {
	s.mu.Lock()
	s.deleted = append(s.deleted, id)
	s.mu.Unlock()
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id)
	}
	return nil
}

func (s *fakeRouteRulesStorager) FetchRouteRulesByID(ctx context.Context, id int64) (*shared.RouteRulesParam, error) {
	s.mu.Lock()
	s.fetchedByID = append(s.fetchedByID, id)
	s.mu.Unlock()
	if s.fetchByIDFn != nil {
		return s.fetchByIDFn(ctx, id)
	}
	return nil, nil
}

func (s *fakeRouteRulesStorager) FetchAllRouteRules(ctx context.Context) ([]*shared.RouteRulesParam, error) {
	s.mu.Lock()
	s.fetchedAll++
	s.mu.Unlock()
	if s.allFn != nil {
		return s.allFn(ctx)
	}
	return nil, nil
}

var _ shared.RouteRulesStorager = (*fakeRouteRulesStorager)(nil)

// setupState initializes package-global state required by tests.
func setupState() {
	if stateful.DefaultConfig == nil {
		stateful.DefaultConfig = &stateful.Config{
			RunTime: stateful.RunTimeConfig{
				AIRouteInnerProductName: "AI_product",
			},
		}
	}
	if stateful.AccessLogger == nil {
		stateful.AccessLogger = make(log4go.Logger)
	}
}

func strPtr(s string) *string {
	return &s
}

func int64Ptr(i int64) *int64 {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func timePtr(t time.Time) *time.Time {
	return &t
}
