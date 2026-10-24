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

package batch

import (
	"context"
	"encoding/json"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

// UpsertBatchTask 按 idem_key 幂等 upsert（详见 dao.TBatchTaskUpsertByIdemKey）。
func (s *BatchStorager) UpsertBatchTask(ctx context.Context, task *ibatch.BatchTask) (int64, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return 0, err
	}
	return dao.TBatchTaskUpsertByIdemKey(dbCtx, batchTaskDataToParam(task))
}

// FetchBatchTask 查询单条任务。未命中返回 (nil, nil)。
func (s *BatchStorager) FetchBatchTask(ctx context.Context, filter *ibatch.BatchTaskFilter) (*ibatch.BatchTask, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}
	one, err := dao.TBatchTaskOne(dbCtx, batchTaskFilterToParam(filter))
	if err != nil {
		return nil, err
	}
	if one == nil {
		return nil, nil
	}
	return batchTaskParamToData(one), nil
}

// ListBatchTasks 查询任务列表。游标分页固定 id 升序：
// WHERE id > cursor ORDER BY id ASC LIMIT n。
func (s *BatchStorager) ListBatchTasks(ctx context.Context, filter *ibatch.BatchTaskFilter) ([]*ibatch.BatchTask, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}
	where := batchTaskFilterToParam(filter)
	where.OrderBy = lib.PString("id")
	if filter != nil && filter.Limit > 0 {
		where.Limit = []uint{0, uint(filter.Limit)}
	}
	list, err := dao.TBatchTaskList(dbCtx, where)
	if err != nil {
		return nil, err
	}
	rst := make([]*ibatch.BatchTask, 0, len(list))
	for _, one := range list {
		rst = append(rst, batchTaskParamToData(one))
	}
	return rst, nil
}

// PreemptBatchTaskCancel cancel 抢占（非终态 → cancelling），返回 affected。
//
// 语义：抢占为"状态转换受理权"——仅首个将任务从非终态转为 cancelling
// 的调用受理成功（affected=1，调用方继续出网 cancel）；对已是 cancelling
// 的行重放（本实例重试或他实例并发）返回 0，由上层 model 按任务当前状态
// 判别：终态 → 409；已 cancelling → 200 幂等重放（重发 provider cancel，
// 幂等，见 batches.md §3.3 处理逻辑 1 与 issue #274）。存储层维持"仅首个
// 转换者成功"的互斥语义不变，不在存储层区分终态与受理中。
//
// 双方言一致性：MySQL 默认 CLIENT_FOUND_ROWS=off，同值 UPDATE
//（命中但无实际列变化）天然返回 affected=0；SQLite changes() 按命中行
// 计数会返回 1。故 WHERE 显式排除 cancelling，使两种后端的 affected
// 语义一致（0 = 终态/受理中/无行，1 = 本次完成状态转换）。
func (s *BatchStorager) PreemptBatchTaskCancel(ctx context.Context, batchID, productName string) (int64, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return 0, err
	}
	excluded := make([]string, 0, len(ibatch.TerminalStatuses)+1)
	excluded = append(excluded, ibatch.TerminalStatuses...)
	excluded = append(excluded, ibatch.TaskStatusCancelling)
	return dao.TBatchTaskPreemptCancel(dbCtx, batchID, productName, excluded, *lib.PTimeNow())
}

// UpdateBatchTaskSettleStatus 三态对账条件更新（WHERE settle_status=from），
// 返回 affected。
func (s *BatchStorager) UpdateBatchTaskSettleStatus(ctx context.Context, batchID, productName, fromStatus string, patch *ibatch.BatchSettlePatch) (int64, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return 0, err
	}
	assign := &dao.TBatchTaskParam{
		SettleStatus:      lib.PString(patch.SettleStatus),
		SettleUnits:       patch.SettleUnits,
		UsageInputTokens:  patch.UsageInputTokens,
		UsageOutputTokens: patch.UsageOutputTokens,
		UsageSource:       patch.UsageSource,
		OverReserved:      patch.OverReserved,
		TerminalAt:        patch.TerminalAt,
		UpdatedAt:         lib.PTimeNow(),
	}
	return dao.TBatchTaskUpdateSettleStatus(dbCtx, batchID, productName, fromStatus, assign)
}

// UpdateBatchTaskProgress 按 ID 部分更新状态推进字段（nil 跳过）。
func (s *BatchStorager) UpdateBatchTaskProgress(ctx context.Context, id int64, patch *ibatch.BatchTaskProgressPatch) (int64, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return 0, err
	}
	assign := &dao.TBatchTaskParam{
		Status:       patch.Status,
		OutputFileID: patch.OutputFileID,
		Endpoint:     patch.Endpoint,
		TerminalAt:   patch.TerminalAt,
		UpdatedAt:    lib.PTimeNow(),
	}
	if patch.RequestCounts != nil {
		if raw, err := json.Marshal(*patch.RequestCounts); err == nil {
			assign.RequestCounts = lib.PString(string(raw))
		}
	}
	return dao.TBatchTaskUpdate(dbCtx, assign, &dao.TBatchTaskParam{ID: &id})
}

func batchTaskFilterToParam(filter *ibatch.BatchTaskFilter) *dao.TBatchTaskParam {
	if filter == nil {
		return nil
	}
	where := &dao.TBatchTaskParam{
		ID:           filter.ID,
		BatchID:      filter.BatchID,
		IdemKey:      filter.IdemKey,
		APIKeyID:     filter.APIKeyID,
		ProductName:  filter.ProductName,
		EntityID:     filter.EntityID,
		Provider:     filter.Provider,
		Status:       filter.Status,
		SettleStatus: filter.SettleStatus,
		CreatedAtGTE: filter.CreatedStart,
		CreatedAtLTE: filter.CreatedEnd,
	}
	if len(filter.StatusIn) > 0 {
		where.StatusIn = filter.StatusIn
	}
	if len(filter.StatusNotIn) > 0 {
		where.StatusNotIn = filter.StatusNotIn
	}
	if filter.CursorID != nil {
		where.IDGT = filter.CursorID
	}
	return where
}

func batchTaskDataToParam(task *ibatch.BatchTask) *dao.TBatchTaskParam {
	data := &dao.TBatchTaskParam{
		BatchID:           lib.PString(task.BatchID),
		APIKeyID:          lib.PString(task.APIKeyID),
		ProductName:       lib.PString(task.ProductName),
		EntityID:          lib.PString(task.EntityID),
		Provider:          lib.PString(task.Provider),
		KeyName:           lib.PString(task.KeyName),
		Endpoint:          lib.PString(task.Endpoint),
		InputFileID:       lib.PString(task.InputFileID),
		OutputFileID:      lib.PString(task.OutputFileID),
		Status:            lib.PString(task.Status),
		EstLines:          lib.PInt64(task.EstLines),
		UsageInputTokens:  lib.PInt64(task.UsageInputTokens),
		UsageOutputTokens: lib.PInt64(task.UsageOutputTokens),
		UsageSource:       lib.PString(task.UsageSource),
		ReserveUnits:      lib.PInt64(task.ReserveUnits),
		SettleUnits:       lib.PInt64(task.SettleUnits),
		SettleStatus:      lib.PString(task.SettleStatus),
		OverReserved:      lib.PBool(task.OverReserved),
		SyncSource:        lib.PString(task.SyncSource),
		IdemKey:           lib.PString(task.IdemKey),
		TerminalAt:        task.TerminalAt,
	}
	// 零值时间留给 DAO 填当前时间（Redis 同步组装的实体无创建时间）。
	if !task.CreatedAt.IsZero() {
		data.CreatedAt = lib.PTime(task.CreatedAt)
	}
	if !task.UpdatedAt.IsZero() {
		data.UpdatedAt = lib.PTime(task.UpdatedAt)
	}
	if len(task.RequestCounts) > 0 {
		if raw, err := json.Marshal(task.RequestCounts); err == nil {
			data.RequestCounts = lib.PString(string(raw))
		}
	}
	return data
}

func batchTaskParamToData(one *dao.TBatchTask) *ibatch.BatchTask {
	task := &ibatch.BatchTask{
		ID:                one.ID,
		BatchID:           one.BatchID,
		APIKeyID:          one.APIKeyID,
		ProductName:       one.ProductName,
		EntityID:          one.EntityID,
		Provider:          one.Provider,
		KeyName:           one.KeyName,
		Endpoint:          one.Endpoint,
		InputFileID:       one.InputFileID,
		OutputFileID:      one.OutputFileID,
		Status:            one.Status,
		EstLines:          one.EstLines,
		UsageInputTokens:  one.UsageInputTokens,
		UsageOutputTokens: one.UsageOutputTokens,
		UsageSource:       one.UsageSource,
		ReserveUnits:      one.ReserveUnits,
		SettleUnits:       one.SettleUnits,
		SettleStatus:      one.SettleStatus,
		OverReserved:      one.OverReserved,
		SyncSource:        one.SyncSource,
		IdemKey:           one.IdemKey,
		CreatedAt:         one.CreatedAt,
		UpdatedAt:         one.UpdatedAt,
		TerminalAt:        one.TerminalAt,
	}
	if one.RequestCounts != "" {
		var counts map[string]int64
		if err := json.Unmarshal([]byte(one.RequestCounts), &counts); err == nil {
			task.RequestCounts = counts
		}
	}
	return task
}
