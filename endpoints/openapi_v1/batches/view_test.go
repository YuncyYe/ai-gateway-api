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
//limitations under the License. All rights reserved.

package batches

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
)

// zeroStateTask 构造除状态外全零值的任务实体（est_lines/usage/金额
// 均为 0，output_file_id/terminal_at 为空），与 issue #271 复现形态一致。
func zeroStateTask(batchID, status, settleStatus string) *ibatch.BatchTask {
	return &ibatch.BatchTask{
		ID:           1,
		BatchID:      batchID,
		APIKeyID:     "ak-1",
		ProductName:  "AI_product",
		Provider:     "deepseek",
		Status:       status,
		SettleStatus: settleStatus,
		SyncSource:   "log",
		IdemKey:      batchID + "-idem",
		CreatedAt:    time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 10, 6, 10, 10, 0, 0, time.UTC),
	}
}

// TestBatchTaskView_ZeroValuesExplicit 锁定 batches.md §2.1"字段必现规则"
//（issue #271）：int64 零值与空串必须显式出现，不得被 omitempty 吞掉。
func TestBatchTaskView_ZeroValuesExplicit(t *testing.T) {
	cases := []struct {
		name        string
		status      string
		settle      string
	}{
		{"reserved 未结算", "in_progress", "reserved"},
		{"settled 金额为零", "completed", "settled"},
		{"released 全额释放", "failed", "released"},
	}
	zeroTokens := []string{
		`"est_lines":0`,
		`"usage_input_tokens":0`,
		`"usage_output_tokens":0`,
		`"reserve_units":0`,
		`"settle_units":0`,
		`"output_file_id":""`,
		`"terminal_at":""`,
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(newBatchTaskView(zeroStateTask("b-zero", tc.status, tc.settle)))
			require.NoError(t, err)
			for _, token := range zeroTokens {
				assert.Contains(t, string(raw), token, "零值/空值键必须显式出现")
			}
			assert.NotContains(t, string(raw), `"terminal_at":null`, "terminal_at 只允许空串，不允许 null")
		})
	}
}

// TestBatchTaskView_NonZero 非零回归：金额按 1e-8 定点整数原样输出，
// terminal_at 非空时为 RFC3339 字符串——防"无条件输出 0/空串"的过修。
func TestBatchTaskView_NonZero(t *testing.T) {
	terminalAt := time.Date(2026, 10, 6, 10, 20, 0, 0, time.UTC)
	task := zeroStateTask("b-nonzero", "completed", "settled")
	task.EstLines = 100
	task.UsageInputTokens = 50000
	task.UsageOutputTokens = 12000
	task.ReserveUnits = 250000000
	task.SettleUnits = 180000000
	task.OutputFileID = "f-out"
	task.TerminalAt = &terminalAt

	raw, err := json.Marshal(newBatchTaskView(task))
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"reserve_units":250000000`)
	assert.Contains(t, body, `"settle_units":180000000`)
	assert.Contains(t, body, `"est_lines":100`)
	assert.Contains(t, body, `"output_file_id":"f-out"`)
	assert.Contains(t, body, `"terminal_at":"2026-10-06T10:20:00Z"`)
}

// TestBatchTaskView_KeySet 键集合锁定：字段集合与修复前一致——五个
// int64 + output_file_id/terminal_at 转为必现，契约超集字段
// id/key_name/idem_key 保留，未标注"为空"语义的字段维持省略。
// issue #276 起 product_name 从契约剔除，不得出现在键集合中。
func TestBatchTaskView_KeySet(t *testing.T) {
	raw, err := json.Marshal(newBatchTaskView(zeroStateTask("b-keys", "completed", "settled")))
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{
		"id", "batch_id", "api_key_id", "provider",
		"output_file_id", "status", "est_lines",
		"usage_input_tokens", "usage_output_tokens",
		"reserve_units", "settle_units", "settle_status", "over_reserved",
		"sync_source", "idem_key", "created_at", "updated_at", "terminal_at",
	}, keys)
}

// TestNewBatchTaskViews_Normalize nil 归一为非 nil 空切片，序列化为
// [] 而非 null（沿用 list.go 既有约定）。
func TestNewBatchTaskViews_Normalize(t *testing.T) {
	views := newBatchTaskViews(nil)
	require.NotNil(t, views)
	assert.Equal(t, 0, len(views))
	raw, err := json.Marshal(views)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(raw))

	views = newBatchTaskViews([]*ibatch.BatchTask{zeroStateTask("b-1", "queued", "reserved"), nil})
	require.Len(t, views, 2)
	assert.Equal(t, "b-1", views[0].BatchID)
	assert.Nil(t, views[1], "nil 任务元素映射为 nil 视图")
}
