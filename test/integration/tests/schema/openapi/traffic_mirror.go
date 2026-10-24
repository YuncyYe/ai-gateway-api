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

package openapi

import "github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"

// TrafficMirrorRuleSchema 单个流量镜像规则 schema。
// Required 恰为恒输出的 6 个固定键（4 业务字段 + created_at/updated_at 只读字段），
// Optional 为 4 个可选键（提交非 null 才输出，缺省键缺席——remove_headers 两层默认语义的
// Open API 面）；不得包含内部 id；无 enabled 字段（traffic-mirror-rules.md §1）。
var TrafficMirrorRuleSchema = &testutil.ObjectSchema{
	Required: []string{
		"name", "cond", "mirror_cluster", "percentage", "created_at", "updated_at",
	},
	Optional: []string{"remove_headers", "set_headers", "body_rewrites", "path_rewrite"},
	Fields: map[string]testutil.FieldSpec{
		"name":           {Type: testutil.TypeString},
		"cond":           {Type: testutil.TypeString},
		"mirror_cluster": {Type: testutil.TypeString},
		"percentage":     {Type: testutil.TypeInt},
		"remove_headers": {Type: testutil.TypeArray, Item: &testutil.FieldSpec{Type: testutil.TypeString}},
		"set_headers":    {Type: testutil.TypeObject},
		"body_rewrites": {Type: testutil.TypeArray, Elem: &testutil.ObjectSchema{
			Required: []string{"path", "value"},
			Fields: map[string]testutil.FieldSpec{
				"path":  {Type: testutil.TypeString},
				"value": {Type: testutil.TypeString},
			},
		}},
		"path_rewrite": {Type: testutil.TypeString},
		"created_at":   {Type: testutil.TypeString},
		"updated_at":   {Type: testutil.TypeString},
	},
}

// TrafficMirrorRulesSchema 流量镜像规则集合 schema（GET/PUT 响应同构，顶层键含 rules）。
var TrafficMirrorRulesSchema = &testutil.ObjectSchema{
	Required: []string{"rules"},
	Fields: map[string]testutil.FieldSpec{
		"rules": {Type: testutil.TypeArray, Elem: TrafficMirrorRuleSchema},
	},
}
