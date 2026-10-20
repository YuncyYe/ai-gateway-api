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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	golibquota "github.com/bfenetworks/go-lib/quota"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/imodel_price"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// 批量价价格键（与数据面 cluster_conf 常量同名字同语义，元/ token）。
const (
	PriceInputCostPerToken  = "input_cost_per_token"
	PriceOutputCostPerToken = "output_cost_per_token"
)

// noSettleReleaseStatuses 是无结果文件、整额释放预留的终态集合
// （expired/failed/cancelled；completed 走实结不释放，cancelling 为
// 中间态等待 provider 确认）。
var noSettleReleaseStatuses = []string{
	TaskStatusExpired,
	TaskStatusFailed,
	TaskStatusCancelled,
}

// ReconcileSettle 对账 job 职责③（兜底结算，需出网 + Redis）：
//
//   - completed 且 settle_status='reserved' 且进入终态超宽限
//     （默认 30min，WithSettleGrace 可配）→ 用 provider key 下载输出
//     文件（GET {base}/files/{output_file_id}/content），流式逐行解析
//     response.usage 汇总，按 mode=batch 批量价计算 settle_units
//     （1e-8 定点，quota.CalcCostUnits 与数据面同式）；
//   - expired/failed/cancelled 且 settle_status='reserved' → 整额释放
//     （released）。
//
// 每条路径均以 SETNX BATCH_SETTLED（结算去重）与 WHERE
// settle_status='reserved' 条件更新推进——多实例/重跑不双倍结算。
// 查不到 batch 价：记 warn、settle_units=0、usage_source='reconcile'
// 照常推进（标记待人工，由对账巡检金额核对发现，不阻塞）。
// redis 为 nil（降级部署）时直接返回零值（设计降级路径：只依赖下载
// 拦截结算 + 预留 TTL 释放）。
func (m *Manager) ReconcileSettle(ctx context.Context) (int, int, error) {
	if m.redis == nil {
		return 0, 0, nil
	}

	settleCandidates, err := m.storager.ListBatchTasks(ctx, &BatchTaskFilter{
		Status:       lib.PString(TaskStatusCompleted),
		SettleStatus: lib.PString(SettleStatusReserved),
		Limit:        m.reconcileLimit,
	})
	if err != nil {
		return 0, 0, err
	}
	releaseCandidates, err := m.storager.ListBatchTasks(ctx, &BatchTaskFilter{
		StatusIn:     noSettleReleaseStatuses,
		SettleStatus: lib.PString(SettleStatusReserved),
		Limit:        m.reconcileLimit,
	})
	if err != nil {
		return 0, 0, err
	}

	// 宽限判定（terminal_at 缺失时回退 updated_at / created_at）在
	// 管理器侧完成：先取候选再在内存中按有效终态时间过滤。
	now := m.clock.Now()
	settleCandidates = filterBySettleGrace(settleCandidates, now, m.settleGrace)

	accessCache := map[string]*providerAccess{}
	settled := 0
	for _, task := range settleCandidates {
		ok, err := m.settleOneTask(ctx, accessCache, task)
		if err != nil {
			stateful.AccessLogger.Warn("ibatch: settle skipped, batch=%s: %v", task.BatchID, err)
			continue
		}
		if ok {
			settled++
		}
	}

	released := 0
	for _, task := range releaseCandidates {
		ok, err := m.releaseOneTask(ctx, task)
		if err != nil {
			stateful.AccessLogger.Warn("ibatch: settle release skipped, batch=%s: %v", task.BatchID, err)
			continue
		}
		if ok {
			released++
		}
	}
	return settled, released, nil
}

// filterBySettleGrace 按有效终态时间（terminal_at → updated_at →
// created_at 回退）过滤出超宽限的 completed 任务。
func filterBySettleGrace(candidates []*BatchTask, now time.Time, grace time.Duration) []*BatchTask {
	rst := make([]*BatchTask, 0, len(candidates))
	for _, task := range candidates {
		effective := task.CreatedAt
		if !task.UpdatedAt.IsZero() {
			effective = task.UpdatedAt
		}
		if task.TerminalAt != nil {
			effective = *task.TerminalAt
		}
		if now.Sub(effective) >= grace {
			rst = append(rst, task)
		}
	}
	return rst
}

// settleOneTask 单任务兜底结算。返回是否由本调用完成结算推进。
func (m *Manager) settleOneTask(ctx context.Context, accessCache map[string]*providerAccess, task *BatchTask) (bool, error) {
	// ① 结算去重锁：下载拦截结算、job 兜底、cancel 后跑完再结算共用。
	claimed, err := m.redis.SetNXWithTTL(ctx, BatchSettledKey(task.BatchID), batchStateTTLSeconds)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}
	// 后续失败路径统一释放锁，下轮重试（绝不持锁失败）。
	releaseClaim := func() {
		if err := m.redis.Delete(ctx, BatchSettledKey(task.BatchID)); err != nil {
			stateful.AccessLogger.Warn("ibatch: release settle claim failed, batch=%s: %v", task.BatchID, err)
		}
	}

	access, err := m.resolveProviderAccessCached(ctx, accessCache, task.Provider, task.KeyName)
	if err != nil {
		releaseClaim()
		return false, err
	}

	if task.OutputFileID == "" {
		// completed 但无输出文件（provider 异常）：无结果可结算，退化为
		// 释放并告警（对账巡检金额核对以日志后端兜底）。释放计数归入
		// released 路径，这里不计 settled。
		stateful.AccessLogger.Warn("ibatch: completed task without output file, release reserve, batch=%s", task.BatchID)
		releaseClaim()
		if _, err := m.releaseOneTask(ctx, task); err != nil {
			return false, err
		}
		return false, nil
	}

	// ② 下载输出文件并流式解析 usage。
	downloadURL := access.baseURL + access.path + "/files/" + task.OutputFileID + "/content"
	usage, statusCode, err := m.downloadAndParseUsage(ctx, access, downloadURL)
	if err != nil {
		releaseClaim()
		return false, err
	}
	if statusCode == http.StatusNotFound {
		stateful.AccessLogger.Warn("ibatch: output file not found on provider, retry later, batch=%s file=%s",
			task.BatchID, task.OutputFileID)
		releaseClaim()
		return false, nil
	}
	if statusCode < 200 || statusCode >= 300 {
		stateful.AccessLogger.Warn("ibatch: download output file returned %d, retry later, batch=%s", statusCode, task.BatchID)
		releaseClaim()
		return false, nil
	}

	// 安全阀（issue #254）：整份文件一个可计费行都没有、却存在"有
	// response 但没有 usage"的行——内容存在而一行 usage 都取不到，属解析
	// 口径可疑（修复前扁平解析正是这种形态），此时不推进 settled，告警并
	// 释放占位、下轮重试。纯错误行/空文件（noUsageLines=0）照常按 0 元结算。
	if usage.parsedLines == 0 && usage.noUsageLines > 0 {
		stateful.AccessLogger.Warn(
			"ibatch: result file carries responses without usage, settlement skipped, batch=%s file=%s no_usage_lines=%d bad_lines=%d",
			task.BatchID, task.OutputFileID, usage.noUsageLines, usage.badLines)
		releaseClaim()
		return false, nil
	}

	// ③ 批量价：model 取结果文件首个非空 response.body.model / response.model
	// （BATCH_TASK 无 model 字段）；查不到 batch 价 → warn + settle_units=0 记待人工。
	settleUnits, priceMiss, err := m.calcSettleUnits(ctx, task, usage)
	if err != nil {
		releaseClaim()
		return false, err
	}
	if priceMiss {
		stateful.AccessLogger.Warn("ibatch: batch price miss, settle_units=0 pending manual, batch=%s provider=%s model=%q",
			task.BatchID, task.Provider, usage.model)
	}

	// ④ 写回 Redis 结算字段（BFE 原地更新同位置，同步落库取用）。
	fields := map[string]string{
		"usage_in":      strconv.FormatInt(usage.inputTokens, 10),
		"usage_out":     strconv.FormatInt(usage.outputTokens, 10),
		"settle_units":  strconv.FormatInt(settleUnits, 10),
		"settle_status": SettleStatusSettled,
	}
	if err := m.redis.HSetWithTTL(ctx, BatchTaskKey(task.BatchID), batchStateTTLSeconds, fields); err != nil {
		releaseClaim()
		return false, err
	}

	// ⑤ 预留随结算冲正（幂等；失败仅告警，对账巡检核对）。
	if err := m.releaseReserve(ctx, task.BatchID); err != nil {
		stateful.AccessLogger.Warn("ibatch: settle release reserve failed, batch=%s: %v", task.BatchID, err)
	}

	// ⑥ DB 条件更新推进（WHERE settle_status='reserved' 防双倍）。
	usageSource := UsageSourceReconcile
	overReserved := task.ReserveUnits > 0 && settleUnits > task.ReserveUnits
	terminalAt := task.TerminalAt
	if terminalAt == nil {
		now := m.clock.Now()
		terminalAt = &now
	}
	patch := &BatchSettlePatch{
		SettleStatus:      SettleStatusSettled,
		SettleUnits:       &settleUnits,
		UsageInputTokens:  &usage.inputTokens,
		UsageOutputTokens: &usage.outputTokens,
		UsageSource:       &usageSource,
		OverReserved:      &overReserved,
		TerminalAt:        terminalAt,
	}
	affected, err := m.storager.UpdateBatchTaskSettleStatus(ctx, task.BatchID, task.ProductName, SettleStatusReserved, patch)
	if err != nil {
		releaseClaim()
		return false, err
	}
	return affected > 0, nil
}

// releaseOneTask 终态无结果文件任务的整额释放。
func (m *Manager) releaseOneTask(ctx context.Context, task *BatchTask) (bool, error) {
	if err := m.releaseReserve(ctx, task.BatchID); err != nil {
		return false, err
	}
	patch := &BatchSettlePatch{SettleStatus: SettleStatusReleased}
	affected, err := m.storager.UpdateBatchTaskSettleStatus(ctx, task.BatchID, task.ProductName, SettleStatusReserved, patch)
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// resolveProviderAccessCached 带每轮缓存的 provider 访问解析。
func (m *Manager) resolveProviderAccessCached(ctx context.Context, cache map[string]*providerAccess, providerName, keyName string) (*providerAccess, error) {
	if access, ok := cache[providerName]; ok {
		return access, nil
	}
	access, err := m.resolveProviderAccess(ctx, providerName, keyName)
	if err != nil {
		return nil, err
	}
	cache[providerName] = access
	return access, nil
}

// usageTotals 是输出文件按行解析的 usage 汇总。
type usageTotals struct {
	inputTokens  int64
	outputTokens int64
	model        string
	parsedLines  int // lines that yielded usage
	noUsageLines int // rows with a response object but no usage in either shape
	badLines     int // lines that are not valid JSON
}

// downloadAndParseUsage 下载输出文件并流式逐行解析（内存 O(1)，单行
// 缓冲上限 maxOutputLineBytes，超长行丢弃并告警——与数据面 jsonl 扫描
// 器同策略）。非 2xx 时不读体，直接返回状态码。
func (m *Manager) downloadAndParseUsage(ctx context.Context, access *providerAccess, url string) (*usageTotals, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, m.providerTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set(access.authHeader, access.authValue)

	resp, err := m.httpClientFactory().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	totals := &usageTotals{}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return totals, resp.StatusCode, nil
	}
	if err := parseJSONLUsage(resp.Body, m.maxOutputLineBytes, totals); err != nil {
		return nil, resp.StatusCode, err
	}
	return totals, resp.StatusCode, nil
}

// calcSettleUnits 按 mode=batch 批量价计算 settle_units（1e-8 定点，
// 与数据面 quota.CalcCostUnits 同式；不回退 chat 价）。
// priceMiss=true 表示查不到价或价格键缺失（调用方记 warn 计 0）。
func (m *Manager) calcSettleUnits(ctx context.Context, task *BatchTask, usage *usageTotals) (settleUnits int64, priceMiss bool, err error) {
	if m.priceQuerier == nil || usage.model == "" {
		return 0, true, nil
	}
	price, err := m.priceQuerier.FetchModelPrice(ctx, &imodel_price.ModelPriceFilter{
		Provider: &task.Provider,
		Model:    &usage.model,
		Mode:     lib.PString("batch"),
	})
	if err != nil {
		return 0, false, err
	}
	if price == nil {
		return 0, true, nil
	}
	inputPrice := price.Prices[PriceInputCostPerToken]
	outputPrice := price.Prices[PriceOutputCostPerToken]
	if inputPrice <= 0 && outputPrice <= 0 {
		return 0, true, nil
	}
	settleUnits = golibquota.CalcCostUnits(usage.inputTokens, inputPrice) +
		golibquota.CalcCostUnits(usage.outputTokens, outputPrice)
	return settleUnits, false, nil
}

// parseJSONLUsage 流式解析批量结果文件：逐行 JSON 解析
// response.usage（prompt_tokens/completion_tokens）与 response.model
// （首个非空）；无 usage 的行（错误行）跳过。
func parseJSONLUsage(r io.Reader, maxLineBytes int64, totals *usageTotals) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var pending []byte
	dirty := false

	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			complete := chunk[len(chunk)-1] == '\n'
			seg := chunk
			if complete {
				seg = chunk[:len(chunk)-1]
			}
			switch {
			case dirty:
				if complete {
					// 超长行的剩余部分丢弃至行尾。
					dirty = false
					pending = pending[:0]
				}
			case int64(len(pending)+len(seg)) > maxLineBytes:
				stateful.AccessLogger.Warn("ibatch: jsonl line exceeds limit %d, dropped", maxLineBytes)
				dirty = !complete
				pending = pending[:0]
			default:
				pending = append(pending, seg...)
				if complete {
					handleUsageLine(pending, totals)
					pending = pending[:0]
				}
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			if !dirty && len(bytes.TrimSpace(pending)) > 0 {
				handleUsageLine(pending, totals)
			}
			return nil
		}
		return err
	}
}

// handleUsageLine 解析单行 jsonl 并累加 usage。
//
// usage 位于 response.body.usage（OpenAI Batch 结果文件契约），顶层
// response.usage 作为兼容回退（非标准上游与历史夹具）。行内既有 response
// 对象却两条路径都取不到 usage 时计 noUsageLines（安全阀依据）；整行非法
// JSON 计 badLines；response 为 null/缺省的错误行两者都不计。
func handleUsageLine(line []byte, totals *usageTotals) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}
	var row batchResultRow
	if err := json.Unmarshal(trimmed, &row); err != nil {
		totals.badLines++
		return
	}
	if row.Response == nil {
		return // error rows ({"error":{...}}) carry no usage at all
	}
	usage, model := row.usageAndModel()
	if usage == nil {
		totals.noUsageLines++
		return
	}
	if totals.model == "" && model != "" {
		totals.model = model
	}
	totals.inputTokens += usage.PromptTokens
	totals.outputTokens += usage.CompletionTokens
	totals.parsedLines++
}

// batchResultRow 是批量结果文件的一行：OpenAI 形态把 chat completion 嵌在
// response.body，故 model/usage 位于 response.body.model / .usage，顶层
// response.model / .usage 作为兼容回退。
type batchResultRow struct {
	Response *struct {
		Model string        `json:"model"`
		Usage *usageTokens  `json:"usage"`
		Body  *responseBody `json:"body"`
	} `json:"response"`
}

// responseBody 是结果行里 chat completion 的最小读视图。
type responseBody struct {
	Model string       `json:"model"`
	Usage *usageTokens `json:"usage"`
}

// usageTokens 是结果行里的 token 用量对象。
type usageTokens struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

// usageAndModel 返回该行的 usage 与 model（response.body 优先）。
func (r *batchResultRow) usageAndModel() (*usageTokens, string) {
	if r == nil || r.Response == nil {
		return nil, ""
	}
	if body := r.Response.Body; body != nil && body.Usage != nil {
		return body.Usage, body.Model
	}
	if r.Response.Usage != nil {
		return r.Response.Usage, r.Response.Model
	}
	return nil, ""
}
