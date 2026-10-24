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

// AIContextRewriteSchema 改写层子对象 schema（rewrite 嵌套对象）。
// Required 恰为 {strength, protected_survival_rate}（ai-context-settings.md §1）。
var AIContextRewriteSchema = &testutil.ObjectSchema{
	Required: []string{"strength", "protected_survival_rate"},
	Fields: map[string]testutil.FieldSpec{
		"strength":                {Type: testutil.TypeString, Enum: []interface{}{"lite", "full"}},
		"protected_survival_rate": {Type: testutil.TypeNumber},
	},
}

// AIContextRuleSchema 单个 AI 上下文压缩规则 schema。
// Required 恰为合同 4 字段（ai-context-rules.md §1）；不得包含 id/name/enabled，
// 与 ai-cache 不同：本资源为整组替换语义，响应不携带 created_at/updated_at。
// mode 必填四枚举（BFE 对缺失/非法 mode 整文件拒载，控制面 422 前置拦截）。
var AIContextRuleSchema = &testutil.ObjectSchema{
	Required: []string{"cond", "mode", "max_context_tokens", "reserve_tokens"},
	Fields: map[string]testutil.FieldSpec{
		"cond":               {Type: testutil.TypeString},
		"mode":               {Type: testutil.TypeString, Enum: []interface{}{"off", "conservative", "balanced", "aggressive"}},
		"max_context_tokens": {Type: testutil.TypeInt},
		"reserve_tokens":     {Type: testutil.TypeInt},
	},
}

// AIContextRulesSchema AI 上下文压缩规则集合 schema（GET/PUT 响应同构，顶层键含 rules）。
var AIContextRulesSchema = &testutil.ObjectSchema{
	Required: []string{"rules"},
	Fields: map[string]testutil.FieldSpec{
		"rules": {Type: testutil.TypeArray, Elem: AIContextRuleSchema},
	},
}

// AIContextSettingsSchema AI 上下文压缩全局设置单例 schema（GET/PUT 响应同构）。
// Required 恰为 7 顶层业务字段（ai-context-settings.md §1），rewrite 为嵌套对象
// （FieldSpec.Nested，TypeObject 专用嵌套 schema）；不含 created_at/updated_at
// （全行覆盖语义，时间戳无信息价值）。空表 GET 默认值对象与写入后响应同构（同 7 键）。
var AIContextSettingsSchema = &testutil.ObjectSchema{
	Required: []string{
		"trigger_ratio", "keep_latest_images", "tool_result_max_chars",
		"thinking_policy", "chars_per_token", "image_token_estimate", "rewrite",
	},
	Fields: map[string]testutil.FieldSpec{
		"trigger_ratio":         {Type: testutil.TypeNumber},
		"keep_latest_images":    {Type: testutil.TypeInt},
		"tool_result_max_chars": {Type: testutil.TypeInt},
		"thinking_policy":       {Type: testutil.TypeString, Enum: []interface{}{"trim-all-but-last", "keep"}},
		"chars_per_token":       {Type: testutil.TypeInt},
		"image_token_estimate":  {Type: testutil.TypeInt},
		"rewrite":               {Type: testutil.TypeObject, Nested: AIContextRewriteSchema},
	},
}
