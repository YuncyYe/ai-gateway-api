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

// AICacheRuleSchema 单个 AI 缓存规则 schema。
// Required 恰为合同 7 字段（业务字段），不得包含内部 id 与逐行 created_at/updated_at
// （整组替换语义下不返回，issue #258；ai-cache-rules.md §1）；无 enabled 字段。
// enable_semantic_cache 为二期字段：响应恒携带，缺省填 false。
var AICacheRuleSchema = &testutil.ObjectSchema{
	Required: []string{
		"name", "cond", "cache_key_strategy", "cache_ttl",
		"max_body_bytes", "max_value_bytes", "enable_semantic_cache",
	},
	Fields: map[string]testutil.FieldSpec{
		"name":                  {Type: testutil.TypeString},
		"cond":                  {Type: testutil.TypeString},
		"cache_key_strategy":    {Type: testutil.TypeString, Enum: []interface{}{"lastQuestion", "allQuestions", "disabled"}},
		"cache_ttl":             {Type: testutil.TypeInt},
		"max_body_bytes":        {Type: testutil.TypeInt},
		"max_value_bytes":       {Type: testutil.TypeInt},
		"enable_semantic_cache": {Type: testutil.TypeBool},
	},
}

// AICacheRulesSchema AI 缓存规则集合 schema（GET/PUT 响应同构，顶层键含 rules）。
var AICacheRulesSchema = &testutil.ObjectSchema{
	Required: []string{"rules"},
	Fields: map[string]testutil.FieldSpec{
		"rules": {Type: testutil.TypeArray, Elem: AICacheRuleSchema},
	},
}
