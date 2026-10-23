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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// providerAbsentGrace 是状态推进中"provider 侧 404"判定 expired 的宽限：
// 任务创建超过该时长且 provider 已查无此批量，判 expired 并记终端时间
// （批量任务与对账.md §5 职责②）。24h 覆盖 provider 侧正常的保留周期。
const providerAbsentGrace = 24 * time.Hour

// providerBatchResponse 是 provider GET /batches/{id} 的最小读视图
// （OpenAI Batch API；未知字段忽略）。
type providerBatchResponse struct {
	ID            string           `json:"id"`
	Status        string           `json:"status"`
	OutputFileID  string           `json:"output_file_id"`
	Endpoint      string           `json:"endpoint"`
	RequestCounts map[string]int64 `json:"request_counts"`
}

// AdvanceStatus 对账 job 职责②（状态推进，需出网）：对非终态任务
// （限量）直调 provider GET {base}/batches/{id}，按返回原样更新
// status/output_file_id/request_counts/endpoint（终态不回退，进入终态记
// terminal_at）；首次发现 output_file_id 补登记输出 batch_files
// （direction=output）。provider 侧已消失（404）且任务创建超
// providerAbsentGrace 判定 expired。
//
// 单任务失败（provider 不可达/解析失败）告警并继续（fail-open，下轮
// 重试）；返回值 advanced 为本轮实际推进条数。providerQuerier 未注入
// （无出网降级部署）时返回零值。
func (m *Manager) AdvanceStatus(ctx context.Context) (int, error) {
	if m.providerQuerier == nil {
		return 0, nil
	}
	tasks, err := m.storager.ListBatchTasks(ctx, &BatchTaskFilter{
		StatusNotIn: TerminalStatuses,
		Limit:       m.advanceLimit,
	})
	if err != nil {
		return 0, err
	}

	advanced := 0
	for _, task := range tasks {
		ok, err := m.advanceOneTask(ctx, task)
		if err != nil {
			stateful.AccessLogger.Warn("ibatch: advance status skipped, batch=%s: %v", task.BatchID, err)
			continue
		}
		if ok {
			advanced++
		}
	}
	return advanced, nil
}

// terminalArtifactWindow 是终态产物补漏的候选时间窗：只补最近该时长内
// 创建的任务，限定每轮的出网范围（更早的缺失行由对账巡检告警，不再重试）。
const terminalArtifactWindow = 24 * time.Hour

// BackfillTerminalArtifacts 对账 job 职责②b（终态产物补漏，需出网）。
//
// 职责① 会把 Redis 里的 completed 直接落库，职责② 只查非终态，于是已终态
// 的行永远补不上 output_file_id/request_counts/endpoint（Redis 键先到期、
// 或历史键缺字段时尤其明显）。本轮对"completed 且缺产物"的任务直调
// provider，只 patch 缺失字段。
//
// 与职责② 的关键差别：provider 404 / 非 2xx / 解析失败一律告警跳过，
// **不改任务状态**——completed 不会因为 provider 侧数据过期被误降级为
// expired。代价是永久缺失的行会按周期重复出网，上限为
// 每轮 advanceLimit 次 GET × 窗口内的缺失行。
func (m *Manager) BackfillTerminalArtifacts(ctx context.Context) (int, error) {
	if m.providerQuerier == nil {
		return 0, nil
	}
	status := TaskStatusCompleted
	windowStart := m.clock.Now().Add(-terminalArtifactWindow)
	tasks, err := m.storager.ListBatchTasks(ctx, &BatchTaskFilter{
		Status:       &status,
		CreatedStart: &windowStart,
		Limit:        m.advanceLimit,
	})
	if err != nil {
		return 0, err
	}

	backfilled := 0
	for _, task := range tasks {
		if task.OutputFileID != "" && task.RequestCounts != nil {
			continue // artifacts already complete
		}
		ok, err := m.backfillOneTask(ctx, task)
		if err != nil {
			stateful.AccessLogger.Warn("ibatch: backfill artifacts skipped, batch=%s: %v", task.BatchID, err)
			continue
		}
		if ok {
			backfilled++
		}
	}
	return backfilled, nil
}

// backfillOneTask 补齐单个 completed 任务的产物字段（不动 status）。
func (m *Manager) backfillOneTask(ctx context.Context, task *BatchTask) (bool, error) {
	access, err := m.resolveProviderAccess(ctx, task.Provider, task.KeyName)
	if err != nil {
		return false, err
	}

	queryURL := access.baseURL + access.path + "/batches/" + task.BatchID
	respBody, statusCode, err := m.getProvider(ctx, access, queryURL)
	if err != nil {
		return false, err
	}
	if statusCode == http.StatusNotFound {
		// provider 侧已不可查：保持 completed，不降级为 expired（与职责②不同）
		stateful.AccessLogger.Warn("ibatch: backfill artifacts not found on provider, keep status, batch=%s",
			task.BatchID)
		return false, nil
	}
	if statusCode < 200 || statusCode >= 300 {
		return false, fmt.Errorf("%w: provider GET %s returned %d", ErrUpstreamUnreachable, queryURL, statusCode)
	}

	var batchResp providerBatchResponse
	if err := json.Unmarshal(respBody, &batchResp); err != nil {
		return false, fmt.Errorf("parse provider batch response: %v", err)
	}
	if batchResp.ID != "" && batchResp.ID != task.BatchID {
		return false, fmt.Errorf("provider returned mismatched batch id %q, expect %q", batchResp.ID, task.BatchID)
	}

	now := m.clock.Now()
	patch := &BatchTaskProgressPatch{}
	missing := make([]string, 0, 3)
	if task.OutputFileID == "" && batchResp.OutputFileID != "" {
		patch.OutputFileID = &batchResp.OutputFileID
		missing = append(missing, "output_file_id")
		if err := m.registerOutputFile(ctx, task, batchResp.OutputFileID, now); err != nil {
			stateful.AccessLogger.Warn("ibatch: register output file failed, batch=%s file=%s: %v",
				task.BatchID, batchResp.OutputFileID, err)
		}
	}
	if task.RequestCounts == nil && batchResp.RequestCounts != nil {
		patch.RequestCounts = &batchResp.RequestCounts
		missing = append(missing, "request_counts")
	}
	if task.Endpoint == "" && batchResp.Endpoint != "" {
		patch.Endpoint = &batchResp.Endpoint
		missing = append(missing, "endpoint")
	}
	if len(missing) == 0 {
		return false, nil
	}
	if _, err := m.storager.UpdateBatchTaskProgress(ctx, task.ID, patch); err != nil {
		return false, err
	}
	stateful.AccessLogger.Warn("ibatch: backfilled terminal artifacts, batch=%s fields=%v",
		task.BatchID, strings.Join(missing, ","))
	return true, nil
}

// advanceOneTask 推进单个任务。返回是否发生状态/产物更新。
func (m *Manager) advanceOneTask(ctx context.Context, task *BatchTask) (bool, error) {
	access, err := m.resolveProviderAccess(ctx, task.Provider, task.KeyName)
	if err != nil {
		return false, err
	}

	queryURL := access.baseURL + access.path + "/batches/" + task.BatchID
	respBody, statusCode, err := m.getProvider(ctx, access, queryURL)
	if err != nil {
		return false, err
	}
	if statusCode == http.StatusNotFound {
		return m.handleProviderAbsent(ctx, task)
	}
	if statusCode < 200 || statusCode >= 300 {
		return false, fmt.Errorf("%w: provider GET %s returned %d", ErrUpstreamUnreachable, queryURL, statusCode)
	}

	var batchResp providerBatchResponse
	if err := json.Unmarshal(respBody, &batchResp); err != nil {
		return false, fmt.Errorf("parse provider batch response: %v", err)
	}
	if batchResp.ID != "" && batchResp.ID != task.BatchID {
		return false, fmt.Errorf("provider returned mismatched batch id %q, expect %q", batchResp.ID, task.BatchID)
	}
	if batchResp.Status == "" {
		return false, fmt.Errorf("provider response missing status")
	}

	now := m.clock.Now()
	patch := &BatchTaskProgressPatch{
		Status: &batchResp.Status,
	}
	statusHeld := false
	if task.Status == TaskStatusCancelling &&
		!IsTerminalStatus(batchResp.Status) && batchResp.Status != TaskStatusCancelling {
		// 取消意图不回退（issue #274）：cancelling 是控制面断言状态，provider
		// 滞后仍报 in_progress/queued 等非终态时本地保持；并补发一次 provider
		// cancel（幂等）覆盖"首次出网未落地"空洞，失败 fail-open 下轮重试。
		if err := m.postProviderCancel(ctx, access,
			access.baseURL+access.path+"/batches/"+task.BatchID+"/cancel"); err != nil {
			stateful.AccessLogger.Warn("ibatch: reissue cancel failed, batch=%s: %v", task.BatchID, err)
		}
		patch.Status = nil
		statusHeld = true
	}
	changed := (!statusHeld && batchResp.Status != task.Status) ||
		!equalCounts(batchResp.RequestCounts, task.RequestCounts)
	if batchResp.RequestCounts != nil {
		patch.RequestCounts = &batchResp.RequestCounts
	}

	if batchResp.OutputFileID != "" && batchResp.OutputFileID != task.OutputFileID {
		patch.OutputFileID = &batchResp.OutputFileID
		changed = true
		if err := m.registerOutputFile(ctx, task, batchResp.OutputFileID, now); err != nil {
			stateful.AccessLogger.Warn("ibatch: register output file failed, batch=%s file=%s: %v",
				task.BatchID, batchResp.OutputFileID, err)
		}
	}
	if batchResp.Endpoint != "" && batchResp.Endpoint != task.Endpoint {
		patch.Endpoint = &batchResp.Endpoint
		changed = true
	}

	if IsTerminalStatus(task.Status) {
		// 终态不回退：刷新 request_counts 等附属字段但不改状态。
		// （列表已过滤终态，双重防御。）
		patch.Status = nil
	}
	if IsTerminalStatus(batchResp.Status) && patch.Status != nil {
		t := now
		patch.TerminalAt = &t
	}

	if !changed {
		return false, nil
	}
	if _, err := m.storager.UpdateBatchTaskProgress(ctx, task.ID, patch); err != nil {
		return false, err
	}
	return true, nil
}

// handleProviderAbsent 处理 provider 404：创建超宽限判 expired（记终端
// 时间），宽限内保持现状下轮再看（provider 数据面可能滞后）。
func (m *Manager) handleProviderAbsent(ctx context.Context, task *BatchTask) (bool, error) {
	if m.clock.Now().Sub(task.CreatedAt) <= providerAbsentGrace {
		return false, nil
	}
	status := TaskStatusExpired
	now := m.clock.Now()
	patch := &BatchTaskProgressPatch{
		Status:     &status,
		TerminalAt: &now,
	}
	affected, err := m.storager.UpdateBatchTaskProgress(ctx, task.ID, patch)
	if err != nil || affected == 0 {
		return false, err
	}
	return true, nil
}

// registerOutputFile 首次发现 output_file_id 时补登记输出 batch_files
// （幂等 upsert；已存在则刷新 last_seen_at）。
func (m *Manager) registerOutputFile(ctx context.Context, task *BatchTask, outputFileID string, now time.Time) error {
	existing, err := m.storager.FetchBatchFile(ctx, &BatchFileFilter{
		FileID:   &outputFileID,
		Provider: &task.Provider,
	})
	if err != nil {
		return err
	}
	file := &BatchFile{
		FileID:      outputFileID,
		Provider:    task.Provider,
		KeyName:     task.KeyName,
		APIKeyID:    task.APIKeyID,
		ProductName: task.ProductName,
		Direction:   FileDirectionOutput,
	}
	merged := mergeFileForSync(existing, file, &redisFileView{
		partition: task.Provider,
		fileID:    outputFileID,
	}, now)
	_, err = m.storager.UpsertBatchFile(ctx, merged)
	return err
}

// getProvider 出网 GET provider 端点（超时控制），返回响应体与状态码。
func (m *Manager) getProvider(ctx context.Context, access *providerAccess, url string) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, m.providerTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if access.authHeader != "" {
		req.Header.Set(access.authHeader, access.authValue)
	}

	resp, err := m.httpClientFactory().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUpstreamUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func equalCounts(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
