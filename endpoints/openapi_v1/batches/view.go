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
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
)

// batchTaskView 是 batches.md §2.1 的对外响应形状（issue #271）：
// int64 零值与空串必须显式出现（"字段必现规则"），因此不复用
// ibatch.BatchTask 的 omitempty 规则——后者同时承担 Redis 回源合并的
// 读模型语义（未携带 = 未知），与对外契约无关。terminal_at 以 string
// 输出：未终结时为空串（§3.2/§3.3 示例），不出现 null。
type batchTaskView struct {
	ID                int64                 `json:"id"`
	BatchID           string                `json:"batch_id"`
	APIKeyID          string                `json:"api_key_id"`
	ProductName       string                `json:"product_name"`
	EntityID          string                `json:"entity_id,omitempty"`
	Provider          string                `json:"provider"`
	KeyName           string                `json:"key_name,omitempty"`
	Endpoint          string                `json:"endpoint,omitempty"`
	InputFileID       string                `json:"input_file_id,omitempty"`
	OutputFileID      string                `json:"output_file_id"`
	Status            string                `json:"status"`
	RequestCounts     map[string]int64      `json:"request_counts,omitempty"`
	EstLines          int64                 `json:"est_lines"`
	UsageInputTokens  int64                 `json:"usage_input_tokens"`
	UsageOutputTokens int64                 `json:"usage_output_tokens"`
	UsageSource       string                `json:"usage_source,omitempty"`
	ReserveUnits      int64                 `json:"reserve_units"`
	SettleUnits       int64                 `json:"settle_units"`
	SettleStatus      string                `json:"settle_status"`
	OverReserved      bool                  `json:"over_reserved"`
	SyncSource        string                `json:"sync_source"`
	IdemKey           string                `json:"idem_key"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	TerminalAt        string                `json:"terminal_at"`
	BatchFiles        []*ibatch.BatchFileMeta `json:"batch_files,omitempty"`
}

// newBatchTaskView 将任务实体投影为对外视图；nil 入参返回 nil。
// terminal_at 未终结（nil）输出空串，非 nil 输出 RFC3339（与
// created_at / updated_at 同格式）。
func newBatchTaskView(task *ibatch.BatchTask) *batchTaskView {
	if task == nil {
		return nil
	}
	view := &batchTaskView{
		ID:                task.ID,
		BatchID:           task.BatchID,
		APIKeyID:          task.APIKeyID,
		ProductName:       task.ProductName,
		EntityID:          task.EntityID,
		Provider:          task.Provider,
		KeyName:           task.KeyName,
		Endpoint:          task.Endpoint,
		InputFileID:       task.InputFileID,
		OutputFileID:      task.OutputFileID,
		Status:            task.Status,
		RequestCounts:     task.RequestCounts,
		EstLines:          task.EstLines,
		UsageInputTokens:  task.UsageInputTokens,
		UsageOutputTokens: task.UsageOutputTokens,
		UsageSource:       task.UsageSource,
		ReserveUnits:      task.ReserveUnits,
		SettleUnits:       task.SettleUnits,
		SettleStatus:      task.SettleStatus,
		OverReserved:      task.OverReserved,
		SyncSource:        task.SyncSource,
		IdemKey:           task.IdemKey,
		CreatedAt:         task.CreatedAt,
		UpdatedAt:         task.UpdatedAt,
		TerminalAt:        "",
		BatchFiles:        task.BatchFiles,
	}
	if task.TerminalAt != nil {
		view.TerminalAt = task.TerminalAt.Format(time.RFC3339)
	}
	return view
}

// newBatchTaskViews 批量投影；nil 入参归一为非 nil 空切片，保证列表
// 空结果序列化为 [] 而非 null（沿用 list.go 既有约定）。
func newBatchTaskViews(tasks []*ibatch.BatchTask) []*batchTaskView {
	views := make([]*batchTaskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, newBatchTaskView(task))
	}
	return views
}
