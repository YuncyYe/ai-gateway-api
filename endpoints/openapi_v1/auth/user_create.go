// Copyright (c) 2021 The BFE Authors.
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

package auth

import (
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/validate"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// UserCreateParam Request Param
type UserCreateParam struct {
	UserName *string `json:"user_name" validate:"required,min=1"`
	Password *string `json:"password" validate:"required,min=8"`
	Role     *string `json:"role" validate:"required"`
}

// Validate performs centralized business validation on the request parameters.
func (p *UserCreateParam) Validate() error {
	if err := validate.UserName(*p.UserName); err != nil {
		return err
	}
	if err := validate.Password(*p.Password, *p.UserName); err != nil {
		return err
	}
	if p.Role == nil {
		return xerror.WrapParamErrorWithMsg("role is required")
	}
	return validate.Role(*p.Role)
}

// UserCreateRoute route
var UserCreateEndpoint = &xreq.Endpoint{
	Path:       "/auth/users",
	Method:     http.MethodPost,
	Handler:    xreq.Convert(UserCreateAction),
	Authorizer: iauth.FA(iauth.FeatureUser, iauth.ActionCreate),
}

// AUTO GEN BY ctrl, MODIFY AS U NEED
func newUserCreateParam(req *http.Request) (*UserCreateParam, error) {
	param := &UserCreateParam{}
	err := xreq.BindJSON(req, param)
	return param, err
}

func userCreateActionProcess(req *http.Request, param *UserCreateParam) error {
	scopes, err := iauth.RoleToScopes(*param.Role)
	if err != nil {
		return err
	}

	return container.AuthenticateManager.CreateUser(req.Context(), &iauth.UserParam{
		Name:     param.UserName,
		Password: param.Password,
		Scopes:   scopes,
	})
}

var _ xreq.Handler = UserCreateAction

// UserCreateAction action
func UserCreateAction(req *http.Request) (interface{}, error) {
	param, err := newUserCreateParam(req)
	if err != nil {
		return nil, err
	}

	if param.Password == nil || *param.Password == "" {
		return nil, xerror.WrapParamErrorWithMsg("password is required")
	}

	return nil, userCreateActionProcess(req, param)
}
