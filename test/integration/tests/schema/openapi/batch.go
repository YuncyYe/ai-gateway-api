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

// BatchFileMetaSchema 批量文件元数据对外视图（batches.md §2.2）。
// Required 恰为恒输出键；purpose/filename/bytes 为 omitempty（无值时缺席，
// filename 由 provider File 对象给出，issue #254 起落库）。
// 不得包含内部字段（id/direction/lines/key_name/api_key_id/product_name/
// first_seen_at/last_seen_at/updated_at）——由 detail 用例键集合精确锁兜底。
var BatchFileMetaSchema = &testutil.ObjectSchema{
	Required: []string{"file_id", "provider", "created_at"},
	Optional: []string{"purpose", "filename", "bytes"},
	Fields: map[string]testutil.FieldSpec{
		"file_id":    {Type: testutil.TypeString},
		"provider":   {Type: testutil.TypeString},
		"purpose":    {Type: testutil.TypeString},
		"filename":   {Type: testutil.TypeString},
		"bytes":      {Type: testutil.TypeInt},
		"created_at": {Type: testutil.TypeString},
	},
}

// BatchTaskDetailSchema 批量任务详情响应（batches.md §3.2：Data 即
// BatchTask 对象本身，字段同 §2.1 含 batch_files）。
// Required 为恒输出键；Optional 为 omitempty 键（标识类字段无值时缺席、
// request_counts/usage_source 契约未定义空值、batch_files 仅详情填充）。
// issue #271 起 est_lines/usage_*/reserve_units/settle_units（int64 零值必现）
// 与 output_file_id/terminal_at（空串必现）移入 Required——零值/空值显式输出。
// issue #276 起 product_name 从契约剔除（内部字段，不入参不返回），
// 不得出现在响应中（键缺席由 BT-2-002 定向锁覆盖）。
// 内部字段（id/key_name/idem_key）为已记录的契约超集遗留，不列入 schema
//（未声明键不校验，由 BT-2-002 定向锁覆盖）。
var BatchTaskDetailSchema = &testutil.ObjectSchema{
	Required: []string{
		"batch_id", "api_key_id", "provider", "status",
		"output_file_id", "est_lines",
		"usage_input_tokens", "usage_output_tokens",
		"reserve_units", "settle_units",
		"settle_status", "over_reserved", "sync_source",
		"terminal_at", "created_at", "updated_at",
	},
	Optional: []string{
		"entity_id", "endpoint", "input_file_id",
		"request_counts", "usage_source", "batch_files",
	},
	Fields: map[string]testutil.FieldSpec{
		"batch_id":            {Type: testutil.TypeString},
		"api_key_id":          {Type: testutil.TypeString},
		"entity_id":           {Type: testutil.TypeString},
		"provider":            {Type: testutil.TypeString},
		"endpoint":            {Type: testutil.TypeString},
		"input_file_id":       {Type: testutil.TypeString},
		"output_file_id":      {Type: testutil.TypeString},
		"status":              {Type: testutil.TypeString},
		"request_counts":      {Type: testutil.TypeObject},
		"est_lines":           {Type: testutil.TypeInt},
		"usage_input_tokens":  {Type: testutil.TypeInt},
		"usage_output_tokens": {Type: testutil.TypeInt},
		"usage_source":        {Type: testutil.TypeString},
		"reserve_units":       {Type: testutil.TypeInt},
		"settle_units":        {Type: testutil.TypeInt},
		"settle_status":       {Type: testutil.TypeString},
		"over_reserved":       {Type: testutil.TypeBool},
		"sync_source":         {Type: testutil.TypeString},
		"terminal_at":         {Type: testutil.TypeString},
		"batch_files":         {Type: testutil.TypeArray, Elem: BatchFileMetaSchema},
		"created_at":          {Type: testutil.TypeString},
		"updated_at":          {Type: testutil.TypeString},
	},
}
