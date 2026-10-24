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

package ibatch

import (
	"context"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
)

// ResourceTypeBatchTask 是批量任务在 operation_logs 中的资源类型。
const ResourceTypeBatchTask = ioperlog.ResourceTypeBatchTask

// recordCancelOperation 记录 cancel 审计（批量任务与对账.md §6.1 第 5
// 步：change_summary 带 batch_id 与前后状态）。err 非 nil 时记失败。
func (m *Manager) recordCancelOperation(ctx context.Context, batchID, prevStatus string, err error) {
	if m.operationLogManager == nil {
		return
	}

	status := ioperlog.StatusSuccess
	errorMsg := ""
	if err != nil {
		status = ioperlog.StatusFailed
		errorMsg = ioperlog.TruncateErrorMessageDefault(err)
	}

	before := map[string]interface{}{"batch_id": batchID}
	if prevStatus != "" {
		before["status"] = prevStatus
	}
	after := map[string]interface{}{
		"batch_id": batchID,
		"status":   TaskStatusCancelling,
	}

	entry := &ioperlog.OperationLogEntry{
		Action:        string(ioperlog.ActionUpdate),
		ResourceType:  string(ResourceTypeBatchTask),
		ResourceID:    batchID,
		ResourceName:  batchID,
		Status:        status,
		ErrorMsg:      errorMsg,
		ChangeSummary: ioperlog.BuildChangeSummary(string(ioperlog.ActionUpdate), before, after),
		CreatedAt:     time.Now(),
	}
	m.operationLogManager.Record(ctx, entry)
}
