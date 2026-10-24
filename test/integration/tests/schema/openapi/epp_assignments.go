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

// EppClusterAssignmentSchema /epp-assignments §2.1 全量视图 clusters[] 元素 schema。
// 视图只保留 cluster + group（分配粒度为组，实例由 /epp-pool 派生）。
var EppClusterAssignmentSchema = &testutil.ObjectSchema{
	Required: []string{"cluster", "group"},
	Fields: map[string]testutil.FieldSpec{
		"cluster": {Type: testutil.TypeString},
		"group":   {Type: testutil.TypeString},
	},
}

// EppClusterAssignmentOverrideSchema /epp-assignments §2.2 PUT 覆写响应 schema。
var EppClusterAssignmentOverrideSchema = &testutil.ObjectSchema{
	Required: []string{"cluster", "group"},
	Fields: map[string]testutil.FieldSpec{
		"cluster": {Type: testutil.TypeString},
		"group":   {Type: testutil.TypeString},
	},
}

// EppAssignmentsViewSchema /epp-assignments 全量视图 schema
// （GET 全量视图与 GET ?cluster= 过滤响应同结构）。
var EppAssignmentsViewSchema = &testutil.ObjectSchema{
	Required: []string{"clusters", "unassigned_clusters", "idle_groups"},
	Fields: map[string]testutil.FieldSpec{
		"clusters":            {Type: testutil.TypeArray, Elem: EppClusterAssignmentSchema},
		"unassigned_clusters": {Type: testutil.TypeArray, Item: &testutil.FieldSpec{Type: testutil.TypeString}},
		"idle_groups":         {Type: testutil.TypeArray, Item: &testutil.FieldSpec{Type: testutil.TypeString}},
	},
}
