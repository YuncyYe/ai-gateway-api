// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.

package ai_cache

import (
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/validate"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

var Endpoints = []*xreq.Endpoint{
	AICacheRulesGetRoute,
	AICacheRulesUpdateRoute,
	AICacheSemanticSettingsGetRoute,
	AICacheSemanticSettingsUpdateRoute,
}

var AICacheRulesGetRoute = &xreq.Endpoint{
	Path:       "/ai-cache-rules",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(AICacheRulesGetAction),
	Authorizer: iauth.FA(iauth.FeatureAICache, iauth.ActionRead),
}

var AICacheRulesUpdateRoute = &xreq.Endpoint{
	Path:       "/ai-cache-rules",
	Method:     http.MethodPut,
	Handler:    xreq.Convert(AICacheRulesUpdateAction),
	Authorizer: iauth.FA(iauth.FeatureAICache, iauth.ActionUpdate),
}

// AICacheRulesGetAction returns the whole rule set in priority order
// (first-match-wins; the internal id is not exposed).
func AICacheRulesGetAction(req *http.Request) (interface{}, error) {
	rules, err := container.AICacheManager.GetAICacheRules(req.Context())
	if err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []*shared.AICacheRuleParam{}
	}

	return &shared.AICacheRulesParam{Rules: rules}, nil
}

// AICacheRulesUpdateAction full-replaces the rule set in a single transaction
// and returns the rebuilt collection (same shape as GET).
func AICacheRulesUpdateAction(req *http.Request) (interface{}, error) {
	param := &shared.AICacheRulesParam{}
	if err := xreq.BindJSON(req, param); err != nil {
		return nil, err
	}

	if err := validate.AICacheRules(param); err != nil {
		// Record the rejected write for audit. Identity and the before
		// snapshot are independent of the request body (issue #155).
		container.AICacheManager.RecordSetAICacheRulesFailure(req.Context(), param, err)
		return nil, err
	}

	if param.Rules == nil {
		param.Rules = []*shared.AICacheRuleParam{}
	}

	updated, err := container.AICacheManager.SetAICacheRules(req.Context(), param)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		updated = &shared.AICacheRulesParam{Rules: []*shared.AICacheRuleParam{}}
	}
	if updated.Rules == nil {
		updated.Rules = []*shared.AICacheRuleParam{}
	}

	return updated, nil
}
