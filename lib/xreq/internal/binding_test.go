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

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bindTarget struct {
	Stream  *bool  `form:"stream"`
	Page    *int   `form:"page"`
	Keyword string `form:"keyword"`
}

func TestMapForm_ConversionErrorIsFieldAware(t *testing.T) {
	cases := []struct {
		name    string
		form    map[string][]string
		wantErr string
	}{
		{"invalid_bool", map[string][]string{"stream": {"not-bool"}}, "invalid stream: not-bool"},
		{"invalid_int", map[string][]string{"page": {"abc"}}, "invalid page: abc"},
		{"invalid_int_overflow", map[string][]string{"page": {"99999999999999999999"}}, "invalid page: 99999999999999999999"},
		{"invalid_bool_uri_style_value", map[string][]string{"stream": {"2"}}, "invalid stream: 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := &bindTarget{}
			err := MapForm(target, tc.form)
			require.Error(t, err)
			// 字段感知文案，且不携带 strconv 内部错误原文
			assert.Equal(t, tc.wantErr, err.Error())
			assert.NotContains(t, err.Error(), "strconv")
		})
	}
}

func TestMapForm_ValidAndAbsentValues(t *testing.T) {
	target := &bindTarget{}
	err := MapForm(target, map[string][]string{
		"stream":  {"true"},
		"page":    {"3"},
		"keyword": {"hello"},
	})
	require.NoError(t, err)
	require.NotNil(t, target.Stream)
	assert.True(t, *target.Stream)
	require.NotNil(t, target.Page)
	assert.Equal(t, 3, *target.Page)
	assert.Equal(t, "hello", target.Keyword)

	// 缺 key：指针保持 nil，不报错
	absent := &bindTarget{}
	require.NoError(t, MapForm(absent, map[string][]string{}))
	assert.Nil(t, absent.Stream)
	assert.Nil(t, absent.Page)

	// 空值：bool 按 false、int 按 0 处理，与修复前语义一致
	empty := &bindTarget{}
	require.NoError(t, MapForm(empty, map[string][]string{"stream": {""}, "page": {""}}))
	require.NotNil(t, empty.Stream)
	assert.False(t, *empty.Stream)
	require.NotNil(t, empty.Page)
	assert.Equal(t, 0, *empty.Page)
}

func TestMapUri_ConversionErrorIsFieldAware(t *testing.T) {
	type uriTarget struct {
		ProductID *int64 `uri:"product_id"`
	}
	target := &uriTarget{}
	err := MapUri(target, map[string][]string{"product_id": {"abc"}})
	require.Error(t, err)
	assert.Equal(t, "invalid product_id: abc", err.Error())

	valid := &uriTarget{}
	require.NoError(t, MapUri(valid, map[string][]string{"product_id": {"42"}}))
	require.NotNil(t, valid.ProductID)
	assert.Equal(t, int64(42), *valid.ProductID)
}
