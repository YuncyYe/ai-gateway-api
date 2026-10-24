//Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
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

package ai_context

import (
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/validate"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

var Endpoints = []*xreq.Endpoint{
	AIContextRulesGetRoute,
	AIContextRulesUpdateRoute,
	AIContextSettingsGetRoute,
	AIContextSettingsUpdateRoute,
}

var AIContextRulesGetRoute = &xreq.Endpoint{
	Path:       "/ai-context-rules",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(AIContextRulesGetAction),
	Authorizer: iauth.FA(iauth.FeatureAIContext, iauth.ActionRead),
}

var AIContextRulesUpdateRoute = &xreq.Endpoint{
	Path:       "/ai-context-rules",
	Method:     http.MethodPut,
	Handler:    xreq.Convert(AIContextRulesUpdateAction),
	Authorizer: iauth.FA(iauth.FeatureAIContext, iauth.ActionUpdate),
}

// AIContextRulesGetAction returns the whole rule set in priority order
// (first-match-wins; the internal id is not exposed).
func AIContextRulesGetAction(req *http.Request) (interface{}, error) {
	rules, err := container.AIContextManager.GetRules(req.Context())
	if err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []*shared.AIContextRuleParam{}
	}

	return &shared.AIContextRulesParam{Rules: rules}, nil
}

// AIContextRulesUpdateAction full-replaces the rule set in a single
// transaction and returns the rebuilt collection (same shape as GET).
func AIContextRulesUpdateAction(req *http.Request) (interface{}, error) {
	param := &shared.AIContextRulesParam{}
	if err := xreq.BindJSON(req, param); err != nil {
		return nil, err
	}

	if err := validate.AIContextRules(param); err != nil {
		// Record the rejected write for audit. Identity and the before
		// snapshot are independent of the request body (issue #155).
		container.AIContextManager.RecordSetRulesFailure(req.Context(), param, err)
		return nil, err
	}

	if param.Rules == nil {
		param.Rules = []*shared.AIContextRuleParam{}
	}

	updated, err := container.AIContextManager.SetRules(req.Context(), param)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		updated = &shared.AIContextRulesParam{Rules: []*shared.AIContextRuleParam{}}
	}
	if updated.Rules == nil {
		updated.Rules = []*shared.AIContextRuleParam{}
	}

	return updated, nil
}
