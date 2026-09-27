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

package intent_config_export

import (
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints/innerapi_v1/export_util"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// ExportRoute exports the mod_ai_intent data file (intent_questions.data)
// for conf-agent: the file content verbatim with the version embedded
// (Version/MinConfidence/Questions). When the config is unpublished or the
// version is unchanged, Data is null and conf-agent neither rewrites the
// file nor reloads BFE.
var ExportRoute = &xreq.Endpoint{
	Path:       "/configs/mod-ai-intent",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(ExportAction),
	Authorizer: iauth.FA(iauth.FeatureAIIntent, iauth.ActionExport),
}

var _ xreq.Handler = ExportAction

// ExportAction action
func ExportAction(req *http.Request) (interface{}, error) {
	param, err := export_util.NewExportFromReq(req)
	if err != nil {
		return nil, err
	}

	return container.IntentConfigManager.ConfigExport(req.Context(), param.Version)
}
