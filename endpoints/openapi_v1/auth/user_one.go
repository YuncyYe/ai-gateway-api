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

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// UserOneRoute route
// AUTO GEN BY ctrl, MODIFY AS U NEED
var UserOneEndpoint = &xreq.Endpoint{
	Path:       "/auth/users/{user_name}",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(UserOneAction),
	Authorizer: iauth.FA(iauth.FeatureUser, iauth.ActionReadAll),
}

func userOneActionProcess(req *http.Request, param *UserNameParam) (*UserData, error) {
	user, err := container.AuthenticateManager.FetchUser(req.Context(), &iauth.UserFilter{
		Name: param.UserName,
	})
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, xerror.WrapRecordNotExist("User")
	}

	return newUserData(user), nil
}

var _ xreq.Handler = UserOneAction

// UserOneAction action
// AUTO GEN BY ctrl, MODIFY AS U NEED
func UserOneAction(req *http.Request) (interface{}, error) {
	param, err := newUserNameParam(req)
	if err != nil {
		return nil, err
	}

	return userOneActionProcess(req, param)
}

// UserData Request Param
// AUTO GEN BY ctrl, MODIFY AS U NEED
type UserData struct {
	UserName string `json:"user_name,omitempty"`
	Role     string `json:"role"`

	// SessionKey is only ever populated for the login response of the user
	// themselves; user list/detail responses must leave it empty so session
	// keys never leak through user read APIs.
	SessionKey string `json:"session_key,omitempty"`
}

func newUserData(user *iauth.User) *UserData {
	return &UserData{
		UserName: user.Name,
		Role:     iauth.ScopesToRole(user.Scopes, user.Admin),
	}
}
