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

// IntentConfigQuestionSchema 单个意图问题 schema（GET/PUT 响应同构）。
// Required 为全部问题共有的 3 字段；criteria/levels 按 type 互斥出现
// （choice 带 criteria、score 带 levels，互斥与数量上下界由定向断言覆盖）；
// min_confidence 为逐问题可选覆盖。questions 元素不得包含内部字段
// （无 id/enabled/version，intent-config.md §1）。
var IntentConfigQuestionSchema = &testutil.ObjectSchema{
	Required: []string{"name", "type", "instructions"},
	Optional: []string{"criteria", "levels", "min_confidence"},
	Fields: map[string]testutil.FieldSpec{
		"name":         {Type: testutil.TypeString},
		"type":         {Type: testutil.TypeString, Enum: []interface{}{"choice", "score"}},
		"instructions": {Type: testutil.TypeString},
		"criteria":     {Type: testutil.TypeObject},
		"levels": {Type: testutil.TypeArray, Elem: &testutil.ObjectSchema{
			Required: []string{"name", "description"},
			Fields: map[string]testutil.FieldSpec{
				"name":        {Type: testutil.TypeString},
				"description": {Type: testutil.TypeString},
			},
		}},
		"min_confidence": {Type: testutil.TypeNumber},
	},
}

// IntentConfigSchema 意图配置单例 schema（GET/PUT 响应同构）。
// Required 恰为合同 4 字段（min_confidence/questions + created_at/updated_at 只读字段）；
// 不得包含内部 version（下发链路内部字段，OpenAPI 不暴露）、无 id/enabled
// （单行覆盖式存储，无历史版本，intent-config.md §1）。
var IntentConfigSchema = &testutil.ObjectSchema{
	Required: []string{"min_confidence", "questions", "created_at", "updated_at"},
	Fields: map[string]testutil.FieldSpec{
		"min_confidence": {Type: testutil.TypeNumber},
		"questions":      {Type: testutil.TypeArray, Elem: IntentConfigQuestionSchema},
		"created_at":     {Type: testutil.TypeString},
		"updated_at":     {Type: testutil.TypeString},
	},
}
