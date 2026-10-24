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

// AICacheSemanticSettingsSchema 语义全局设置单例 schema（GET/PUT 响应同构）。
// Required 恰为 {top_k, threshold, threshold_relation}（ai-cache-semantic-settings.md §1）；
// created_at/updated_at 为只读字段，放 Optional 而不进 Required——空设置表时 GET 返回
// 默认值对象（无时间戳键），写入后响应携带时间戳，两种形态同构兼容。
var AICacheSemanticSettingsSchema = &testutil.ObjectSchema{
	Required: []string{"top_k", "threshold", "threshold_relation"},
	Optional: []string{"created_at", "updated_at"},
	Fields: map[string]testutil.FieldSpec{
		"top_k":              {Type: testutil.TypeInt},
		"threshold":          {Type: testutil.TypeNumber},
		"threshold_relation": {Type: testutil.TypeString, Enum: []interface{}{"lt", "lte", "gt", "gte"}},
		"created_at":         {Type: testutil.TypeString},
		"updated_at":         {Type: testutil.TypeString},
	},
}
