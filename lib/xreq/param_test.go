// Copyright (c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
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

package xreq

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
)

type formTarget struct {
	Stream *bool `form:"stream"`
	Page   *int  `form:"page"`
}

// TestBindForm_ConversionErrorResolvesTo422 回归 issue #233：form 类型转换
// 失败必须包装为 PARAM 参数错误（HTTP 422），不得裸抛为 500 Unknown Exception。
func TestBindForm_ConversionErrorResolvesTo422(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/report/overview?stream=not-bool", nil)
	target := &formTarget{}
	err := BindForm(req, target)
	require.Error(t, err)

	rr := xerror.Resolve(err)
	assert.Equal(t, 422, rr.ErrNo)
	assert.Equal(t, "Param Illegal", rr.Type)
	assert.Equal(t, "invalid stream: not-bool", rr.Msg)
	assert.NotContains(t, rr.Msg, "strconv")
}

func TestBindForm_ValidValuesUnchanged(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/report/overview?stream=true&page=2", nil)
	target := &formTarget{}
	require.NoError(t, BindForm(req, target))
	require.NotNil(t, target.Stream)
	assert.True(t, *target.Stream)
	require.NotNil(t, target.Page)
	assert.Equal(t, 2, *target.Page)
}

type uriTarget struct {
	ProductID *int64 `uri:"product_id"`
}

// TestBindURI_ConversionErrorResolvesTo422 覆盖 MapUri 共用路径
// （endpoints/middleware/product_probe.go 的 product_id）。
func TestBindURI_ConversionErrorResolvesTo422(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/products/abc/info", nil)
	req = mux.SetURLVars(req, map[string]string{"product_id": "abc"})
	target := &uriTarget{}
	err := BindURI(req, target)
	require.Error(t, err)

	rr := xerror.Resolve(err)
	assert.Equal(t, 422, rr.ErrNo)
	assert.Equal(t, "Param Illegal", rr.Type)
	assert.Equal(t, "invalid product_id: abc", rr.Msg)
}

func TestBindURI_ValidValuesUnchanged(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/products/42/info", nil)
	req = mux.SetURLVars(req, map[string]string{"product_id": "42"})
	target := &uriTarget{}
	require.NoError(t, BindURI(req, target))
	require.NotNil(t, target.ProductID)
	assert.Equal(t, int64(42), *target.ProductID)
}

// TestWrapParamErrorIdempotent 已包装为 PARAM 的错误再次包装不叠加前缀，
// 保证绑定层与业务层重复包装时文案稳定。
func TestWrapParamErrorIdempotent(t *testing.T) {
	once := xerror.WrapParamError(fmt.Errorf("invalid stream: not-bool"))
	twice := xerror.WrapParamError(once)
	assert.Equal(t, once.Error(), twice.Error())
	assert.Equal(t, "PARAM: invalid stream: not-bool", twice.Error())
}
