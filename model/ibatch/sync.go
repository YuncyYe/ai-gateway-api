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
	"errors"

	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// errSyncSkip 表示单键同步被跳过（api_key 已删除等正常清理场景），
// 调用方不计入同步数也不告警（对账巡检的日志补登兜底覆盖）。
var errSyncSkip = errors.New("ibatch: sync skipped")

// SyncFromRedis 对账 job 职责①（Redis 是任务同步的主事件源）：游标
// SCAN BATCH_TASK:* / BATCH_FILE:*（每批限量），逐键 HGETALL 后按
// idem_key / (file_id, provider) 幂等 upsert 落库（sync_source=redis）。
// 已存在行刷新可变字段并遵守"终态不回退"；BFE 结算/释放时已把
// usage_in/usage_out/settle_units/settle_status 原地更新进 BATCH_TASK，
// 本路径直接取用。
//
// 失败策略：单键解析/归属解析失败跳过并告警（fail-open，由对账巡检
// 日志补登兜底）；SCAN 级错误终止本轮并返回错误。返回值是 (tasks, files)
// 本轮成功同步条数。redis 为 nil（降级部署）时直接返回零值。
func (m *Manager) SyncFromRedis(ctx context.Context) (int, int, error) {
	if m.redis == nil {
		return 0, 0, nil
	}
	tasks, err := m.scanSyncTasks(ctx)
	if err != nil {
		return 0, 0, err
	}
	files, err := m.scanSyncFiles(ctx)
	if err != nil {
		return tasks, 0, err
	}
	return tasks, files, nil
}

func (m *Manager) scanSyncTasks(ctx context.Context) (int, error) {
	var synced int
	apiKeys := newAPIKeyResolveCache()
	cursor := uint64(0)
	for {
		next, keys, err := m.redis.Scan(ctx, cursor, ScanMatchTasks, m.scanCount)
		if err != nil {
			return synced, err
		}
		for _, key := range keys {
			batchID, ok := ParseBatchTaskKey(key)
			if !ok {
				continue
			}
			if err := m.syncOneTask(ctx, apiKeys, batchID); err != nil {
				if errors.Is(err, errSyncSkip) {
					continue
				}
				stateful.AccessLogger.Warn("ibatch: sync task %s skipped: %v", key, err)
				continue
			}
			synced++
		}
		if next == 0 {
			return synced, nil
		}
		cursor = next
	}
}

func (m *Manager) scanSyncFiles(ctx context.Context) (int, error) {
	var synced int
	apiKeys := newAPIKeyResolveCache()
	cursor := uint64(0)
	for {
		next, keys, err := m.redis.Scan(ctx, cursor, ScanMatchFiles, m.scanCount)
		if err != nil {
			return synced, err
		}
		for _, key := range keys {
			partition, fileID, ok := ParseBatchFileKey(key)
			if !ok {
				continue
			}
			if err := m.syncOneFile(ctx, apiKeys, partition, fileID); err != nil {
				if errors.Is(err, errSyncSkip) {
					continue
				}
				stateful.AccessLogger.Warn("ibatch: sync file %s skipped: %v", key, err)
				continue
			}
			synced++
		}
		if next == 0 {
			return synced, nil
		}
		cursor = next
	}
}

// syncOneTask 同步单个 BATCH_TASK：读 hash → 解析 → api_key 归属解析 →
// 与 DB 已有行合并（终态不回退）→ 幂等 upsert。
func (m *Manager) syncOneTask(ctx context.Context, apiKeys *apiKeyResolveCache, batchID string) error {
	vals, err := m.redis.HGetAll(ctx, BatchTaskKey(batchID))
	if err != nil {
		return err
	}
	if len(vals) == 0 {
		return nil // 键在 SCAN 与读取间消亡，TTL 自然过期
	}
	view, err := ParseRedisTaskView(batchID, vals)
	if err != nil {
		return err
	}
	incoming, err := m.applyTaskView(ctx, apiKeys, view)
	if err != nil {
		return err
	}
	if incoming == nil {
		return errSyncSkip // api_key 不存在（已删除），由巡检补登兜底
	}

	existing, err := m.storager.FetchBatchTask(ctx, &BatchTaskFilter{IdemKey: &batchID})
	if err != nil {
		return err
	}
	merged := mergeTaskForSync(existing, incoming, view, m.clock.Now())
	_, err = m.storager.UpsertBatchTask(ctx, merged)
	return err
}

// syncOneFile 同步单个 BATCH_FILE（同 syncOneTask 语义）。
func (m *Manager) syncOneFile(ctx context.Context, apiKeys *apiKeyResolveCache, partition, fileID string) error {
	vals, err := m.redis.HGetAll(ctx, BatchFileKey(partition, fileID))
	if err != nil {
		return err
	}
	if len(vals) == 0 {
		return nil
	}
	view := ParseRedisFileView(partition, fileID, vals)
	incoming, err := m.applyFileView(ctx, apiKeys, view)
	if err != nil {
		return err
	}
	if incoming == nil {
		return errSyncSkip
	}

	existing, err := m.storager.FetchBatchFile(ctx, &BatchFileFilter{
		FileID:   &fileID,
		Provider: &partition,
	})
	if err != nil {
		return err
	}
	merged := mergeFileForSync(existing, incoming, view, m.clock.Now())
	_, err = m.storager.UpsertBatchFile(ctx, merged)
	return err
}
