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
	"fmt"
	"strings"
)

// Redis 键布局（与数据面 mod_ai_batch / mod_ai_token_auth 同集群、同 TTL，
// 见批量任务与对账.md §2）。控制面只读写以下键，不持有任务进程内状态。
const (
	// batchStateTTLSeconds 是 BATCH_* 状态键的 TTL（48h，远大于对账 job
	// 1min 同步周期，无丢失窗口）。
	batchStateTTLSeconds = 172800

	batchTaskKeyPrefix         = "BATCH_TASK:"
	batchFileKeyPrefix         = "BATCH_FILE:"
	batchSettledKeyPrefix      = "BATCH_SETTLED:"
	batchReserveBatchKeyPrefix = "BATCH_RESERVE_BATCH:"
	batchReserveMirrorPrefix   = "BATCH_RESERVE:"

	// ScanMatchTasks / ScanMatchFiles 是对账 job SCAN 的 MATCH 模式。
	ScanMatchTasks = batchTaskKeyPrefix + "*"
	ScanMatchFiles = batchFileKeyPrefix + "*"
)

// BatchTaskKey 返回 BATCH_TASK:<batch_id>。
func BatchTaskKey(batchID string) string {
	return batchTaskKeyPrefix + batchID
}

// ParseBatchTaskKey 从 BATCH_TASK:<batch_id> 键解析 batch_id。
// 键不匹配时返回 false。
func ParseBatchTaskKey(key string) (string, bool) {
	if !strings.HasPrefix(key, batchTaskKeyPrefix) {
		return "", false
	}
	batchID := strings.TrimPrefix(key, batchTaskKeyPrefix)
	return batchID, batchID != ""
}

// BatchFileKey 返回 BATCH_FILE:<partition>:<file_id>。partition 为数据面
// 写入的路由 cluster 名（batch_files.provider 列承载同值）。
func BatchFileKey(partition, fileID string) string {
	return batchFileKeyPrefix + partition + ":" + fileID
}

// ParseBatchFileKey 从 BATCH_FILE:<partition>:<file_id> 键解析两段。
// 键不匹配或缺段时返回 false。
func ParseBatchFileKey(key string) (partition, fileID string, ok bool) {
	rest, found := strings.CutPrefix(key, batchFileKeyPrefix)
	if !found {
		return "", "", false
	}
	partition, fileID, found = strings.Cut(rest, ":")
	if !found || partition == "" || fileID == "" {
		return "", "", false
	}
	return partition, fileID, true
}

// BatchSettledKey 返回 BATCH_SETTLED:<batch_id>（结算去重 SETNX 锁）。
func BatchSettledKey(batchID string) string {
	return batchSettledKeyPrefix + batchID
}

// BatchReserveBatchKey 返回 BATCH_RESERVE_BATCH:<batch_id>：数据面预留时
// 写入的 planRedisKey -> units HASH，释放按此簿记逐 plan 冲正，整额释放
// 后删除该键——键不存在即"已清零"，是释放幂等的判据。
func BatchReserveBatchKey(batchID string) string {
	return batchReserveBatchKeyPrefix + batchID
}

// BatchReserveMirrorKey 返回 BATCH_RESERVE:<planRedisKey>：单 plan 的
// 预留镜像计数（可用余额 = QUOTA_* 剩余 − 本计数）。
func BatchReserveMirrorKey(planRedisKey string) string {
	return batchReserveMirrorPrefix + planRedisKey
}

// ReserveBatchKeyPattern 返回 BATCH_RESERVE_BATCH:* 的精确前缀，
// 供日志/测试断言使用。
func ReserveBatchKeyPattern() string {
	return fmt.Sprintf("%s*", batchReserveBatchKeyPrefix)
}
