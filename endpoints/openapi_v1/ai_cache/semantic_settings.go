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

var AICacheSemanticSettingsGetRoute = &xreq.Endpoint{
	Path:       "/ai-cache-semantic-settings",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(AICacheSemanticSettingsGetAction),
	Authorizer: iauth.FA(iauth.FeatureAICache, iauth.ActionRead),
}

var AICacheSemanticSettingsUpdateRoute = &xreq.Endpoint{
	Path:       "/ai-cache-semantic-settings",
	Method:     http.MethodPut,
	Handler:    xreq.Convert(AICacheSemanticSettingsUpdateAction),
	Authorizer: iauth.FA(iauth.FeatureAICache, iauth.ActionUpdate),
}

// AICacheSemanticSettingsGetAction returns the singleton semantic settings.
// An empty settings table returns the documented defaults (1 / 0.15 / lt,
// without timestamps), never 404.
func AICacheSemanticSettingsGetAction(req *http.Request) (interface{}, error) {
	return container.AICacheManager.GetSemanticSettings(req.Context())
}

// AICacheSemanticSettingsUpdateAction upserts the singleton semantic settings
// in a single transaction and returns the updated settings (same shape as
// GET). A PUT rejected by validation records a failed audit and leaves the
// stored settings unchanged.
func AICacheSemanticSettingsUpdateAction(req *http.Request) (interface{}, error) {
	param := &shared.AICacheSemanticSettingsParam{}
	if err := xreq.BindJSON(req, param); err != nil {
		return nil, err
	}

	if err := validate.AICacheSemanticSettings(param); err != nil {
		// Record the rejected write for audit. Identity and the before
		// snapshot are independent of the request body (issue #155).
		container.AICacheManager.RecordSetSemanticSettingsFailure(req.Context(), param, err)
		return nil, err
	}

	return container.AICacheManager.SetSemanticSettings(req.Context(), param)
}
