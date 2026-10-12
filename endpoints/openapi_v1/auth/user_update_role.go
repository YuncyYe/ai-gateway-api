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

// UserUpdateRoleRoute route
// AUTO GEN BY ctrl, MODIFY AS U NEED
var UserUpdateRoleEndpoint = &xreq.Endpoint{
	Path:       "/auth/users/{user_name}/role",
	Method:     http.MethodPatch,
	Handler:    xreq.Convert(UserUpdateRoleAction),
	Authorizer: iauth.FA(iauth.FeatureUser, iauth.ActionUpdate),
}

// UserUpdateRoleParam Request Param
// AUTO GEN BY ctrl, MODIFY AS U NEED
type UserUpdateRoleParam struct {
	UserName *string `uri:"user_name" validate:"required,min=1"`
	Role     *string `json:"role" validate:"required"`
}

// Validate performs centralized business validation on the request parameters.
func (p *UserUpdateRoleParam) Validate() error {
	if err := validate.UserNameRef(*p.UserName); err != nil {
		return err
	}
	if p.Role == nil {
		return xerror.WrapParamErrorWithMsg("role is required")
	}
	return validate.Role(*p.Role)
}

// AUTO GEN BY ctrl, MODIFY AS U NEED
func newUserUpdateRoleParam(req *http.Request) (*UserUpdateRoleParam, error) {
	param := &UserUpdateRoleParam{}
	err := xreq.Bind(req, param)
	return param, err
}

func userUpdateRoleActionProcess(req *http.Request, param *UserUpdateRoleParam) error {
	user, err := container.AuthenticateManager.FetchUser(req.Context(), &iauth.UserFilter{
		Name: param.UserName,
	})
	if err != nil {
		return err
	}
	if user == nil {
		return xerror.WrapRecordNotExist("User")
	}
	return container.AuthorizeManager.UpdateUserRole(req.Context(), user, *param.Role)
}

var _ xreq.Handler = UserUpdateRoleAction

// UserUpdateRoleAction action
// Update a user's role (system_admin / system_admin_readonly).
func UserUpdateRoleAction(req *http.Request) (interface{}, error) {
	param, err := newUserUpdateRoleParam(req)
	if err != nil {
		return nil, err
	}

	return nil, userUpdateRoleActionProcess(req, param)
}
