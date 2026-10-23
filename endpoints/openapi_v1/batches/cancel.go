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
	"errors"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// BatchTaskCancelAction POST /open-api/v1/batches/{batch_id}/cancel：
// DB 条件更新抢占（affected=0 → 409）→ 出网 provider cancel → 幂等释放
// 预留 → operation_logs 审计（批量任务与对账.md §6 五步流程）。
//
// 错误映射（api-changes.md §5）：
//   - 任务不存在（DB 与 Redis 均 miss）→ 404（ErrBatchNotFound）；
//   - 已终态 / 他实例处理中（抢占 affected=0）→ 409（xerror Conflict）；
//   - provider 返回批量不存在 → 404（透传 provider 语义）；
//   - provider 不可达 / 超时 / 非 2xx → 502（xerror Upstream Unreachable）。
func BatchTaskCancelAction(req *http.Request) (interface{}, error) {
	product, err := resolveProduct(req.Context())
	if err != nil {
		return nil, err
	}
	batchID := mux.Vars(req)["batch_id"]
	if batchID == "" {
		return nil, xerror.WrapParamErrorWithMsg("batch_id is required")
	}

	task, err := container.BatchManager.Cancel(req.Context(), batchID, product.Name)
	if err != nil {
		if errors.Is(err, ibatch.ErrUpstreamBatchNotFound) {
			// provider 侧批量不存在：透传为 404（与任务不不存在同码）。
			return nil, xerror.WrapRecordNotExist("Batch")
		}
		return nil, err
	}
	return newBatchTaskView(task), nil
}
