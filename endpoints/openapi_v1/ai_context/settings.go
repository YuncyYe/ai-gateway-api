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

var AIContextSettingsGetRoute = &xreq.Endpoint{
	Path:       "/ai-context-settings",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(AIContextSettingsGetAction),
	Authorizer: iauth.FA(iauth.FeatureAIContext, iauth.ActionRead),
}

var AIContextSettingsUpdateRoute = &xreq.Endpoint{
	Path:       "/ai-context-settings",
	Method:     http.MethodPut,
	Handler:    xreq.Convert(AIContextSettingsUpdateAction),
	Authorizer: iauth.FA(iauth.FeatureAIContext, iauth.ActionUpdate),
}

// AIContextSettingsGetAction returns the singleton global settings. An empty
// settings table returns the documented defaults (0.7 / 2 / 2000 /
// trim-all-but-last / 4 / 1200 / lite / 0.95, without timestamps), never 404.
func AIContextSettingsGetAction(req *http.Request) (interface{}, error) {
	return container.AIContextManager.GetSettings(req.Context())
}

// AIContextSettingsUpdateAction upserts the singleton global settings in a
// single transaction and returns the updated settings (same shape as GET). A
// PUT rejected by validation records a failed audit and leaves the stored
// settings unchanged.
func AIContextSettingsUpdateAction(req *http.Request) (interface{}, error) {
	param := &shared.AIContextSettingsParam{}
	if err := xreq.BindJSON(req, param); err != nil {
		return nil, err
	}

	if err := validate.AIContextSettings(param); err != nil {
		// Record the rejected write for audit. Identity and the before
		// snapshot are independent of the request body (issue #155).
		container.AIContextManager.RecordSetSettingsFailure(req.Context(), param, err)
		return nil, err
	}

	return container.AIContextManager.SetSettings(req.Context(), param)
}
