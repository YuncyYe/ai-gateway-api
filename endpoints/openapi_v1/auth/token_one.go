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
	"context"
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xmask"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// InnerUserOneRoute route
// AUTO GEN BY ctrl, MODIFY AS U NEED
var TokenOneEndpoint = &xreq.Endpoint{
	Path:       "/auth/tokens/{token_name}",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(TokenOneAction),
	Authorizer: iauth.FA(iauth.FeatureToken, iauth.ActionReadAll),
}

func tokenOneActionProcess(req *http.Request) (*TokenData, error) {
	param, err := newTokenNameParam(req)
	if err != nil {
		return nil, err
	}

	list, err := container.AuthenticateManager.FetchTokens(req.Context(), &iauth.TokenFilter{
		Name: param.TokenName,
	})
	if err != nil {
		return nil, err
	}

	if len(list) == 0 {
		return nil, xerror.WrapRecordNotExist("Token")
	}

	data := newTokenData(list[0], true)
	if err := maskTokensIfReadonly(req.Context(), data); err != nil {
		return nil, err
	}
	return data, nil
}

// maskTokensIfReadonly masks the token value for the read-only system admin
// (系统管理员（只读）). Full admins are never masked; Support tokens cannot
// reach these read endpoints (403 at authorization).
func maskTokensIfReadonly(ctx context.Context, tokens ...*TokenData) error {
	v, err := iauth.MustGetVisitor(ctx)
	if err != nil {
		return err
	}
	if !v.IsSystemReadonly() {
		return nil
	}
	for _, t := range tokens {
		t.Token = xmask.Secret(t.Token)
	}
	return nil
}

var _ xreq.Handler = TokenOneAction

// TokenOneAction action
// AUTO GEN BY ctrl, MODIFY AS U NEED
func TokenOneAction(req *http.Request) (interface{}, error) {
	return tokenOneActionProcess(req)
}

type TokenData struct {
	Name  string `json:"name"`
	Token string `json:"token,omitempty"`
	Scope string `json:"scope"`
}

func newTokenData(token *iauth.Token, withToken bool) *TokenData {
	t := token.Token
	if !withToken {
		t = ""
	}

	return &TokenData{
		Name:  token.Name,
		Token: t,
		Scope: token.Scope,
	}
}
