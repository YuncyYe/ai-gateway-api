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
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
)

// Cancel / 查询路径的错误语义（api-changes.md §5 错误码表）。
// endpoints 层按 errors.Is 识别后映射 HTTP 状态与业务错误码；
// 同时尽量保持 xerror 包装以便默认渲染（404/409 直接可用）：
//
//   - ErrBatchNotFound：DB 与 Redis 均 miss → 404 BATCH_NOT_FOUND；
//   - errBatchConflict：cancel 抢占 affected=0（已终态/他实例处理中）
//     → 409 BATCH_TERMINAL_OR_CONFLICT；
//   - ErrUpstreamUnreachable：provider 不可达/超时/非 2xx（除 404）
//     → 502 UPSTREAM_UNREACHABLE；
//   - ErrUpstreamBatchNotFound：provider 返回批量不存在 → 404
//     BATCH_NOT_FOUND（透传 provider 语义）。
var (
	ErrBatchNotFound         = xerror.WrapRecordNotExist("Batch")
	ErrUpstreamUnreachable   = xerror.WrapUpstreamErrorWithMsg("batch upstream provider unreachable")
	ErrUpstreamBatchNotFound = xerror.WrapModelErrorWithMsg("batch not found on upstream provider")
)

// errBatchConflict 构造 cancel 抢占冲突错误（409）。
func errBatchConflict(batchID string) error {
	return xerror.WrapConflictErrorWithMsg("batch task %s is terminal or being processed by another instance", batchID)
}

// errProviderUnavailable 构造 provider 配置缺失错误（provider 记录不存在、
// 无可用实例、配置了 key 但无可用明文）。属控制面数据异常，endpoints 可映射
// 502 或 500。provider keys 为空数组（keyless 合法配置）不视为异常——出网
// 不携带鉴权头（issue #274）。
func errProviderUnavailable(provider string) error {
	return xerror.WrapModelErrorWithMsg("batch provider %s unavailable", provider)
}
