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

package entity_type

import (
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/validate"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/entity"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

var EntityTypeCreateRoute = &xreq.Endpoint{
	Path:       "/entity-types",
	Method:     http.MethodPost,
	Handler:    xreq.Convert(EntityTypeCreateAction),
	Authorizer: iauth.FA(iauth.FeatureEntityType, iauth.ActionCreate),
}

func EntityTypeCreateAction(req *http.Request) (interface{}, error) {
	param := &entity.EntityTypeParam{}
	if err := xreq.BindJSON(req, param); err != nil {
		return nil, err
	}

	if param.TypeName == nil || *param.TypeName == "" {
		return nil, xerror.WrapParamErrorWithMsg("type_name is required")
	}
	if err := validate.EntityTypeName(*param.TypeName); err != nil {
		return nil, err
	}

	if param.Level == nil {
		return nil, xerror.WrapParamErrorWithMsg("level is required")
	}
	if *param.Level < 1 || *param.Level > 5 {
		return nil, xerror.WrapParamErrorWithMsg("level must be between 1 and 5")
	}

	if param.Description != nil {
		if err := validate.Description(*param.Description, validate.MaxEntityTypeDescriptionLength, "description"); err != nil {
			return nil, err
		}
	}

	existing, err := container.EntityTypeManager.FetchEntityType(req.Context(), &entity.EntityTypeFilter{
		TypeName: param.TypeName,
	})
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, xerror.WrapDuplicateData("entity type")
	}

	if _, err := container.EntityTypeManager.CreateEntityType(req.Context(), param); err != nil {
		return nil, err
	}

	return container.EntityTypeManager.FetchEntityType(req.Context(), &entity.EntityTypeFilter{
		TypeName: param.TypeName,
	})
}
