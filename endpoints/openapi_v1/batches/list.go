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
	"net/http"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

type batchTaskListFilter struct {
	APIKeyID  *string `form:"api_key_id"`
	EntityID  *string `form:"entity_id"`
	Provider  *string `form:"provider"`
	Status    *string `form:"status"`
	StartTime *int64  `form:"start_time"`
	EndTime   *int64  `form:"end_time"`
	Cursor    *int64  `form:"cursor"`
	Limit     *int    `form:"limit"`
}

type batchTaskListResponse struct {
	List       []*batchTaskView `json:"list"`
	NextCursor int64            `json:"next_cursor"`
}

// BatchTaskListAction GET /open-api/v1/batches：过滤 + 主键游标分页。
// 金额字段为 1e-8 定点整数（RMB），直接以 JSON 整数输出（api-changes.md
// §4.1）。product_name 强制取中间件注入值。
func BatchTaskListAction(req *http.Request) (interface{}, error) {
	product, err := resolveProduct(req.Context())
	if err != nil {
		return nil, err
	}

	filter := &batchTaskListFilter{}
	if err := xreq.BindForm(req, filter); err != nil {
		return nil, err
	}
	if filter.Cursor != nil && *filter.Cursor < 0 {
		return nil, xerror.WrapParamErrorWithMsg("cursor must be >= 0")
	}

	managerFilter := &ibatch.BatchTaskFilter{
		APIKeyID:    filter.APIKeyID,
		EntityID:    filter.EntityID,
		Provider:    filter.Provider,
		Status:      filter.Status,
		ProductName: &product.Name,
		CursorID:    filter.Cursor,
	}
	if filter.Limit != nil {
		managerFilter.Limit = *filter.Limit
	}
	if filter.StartTime != nil {
		t := time.Unix(*filter.StartTime, 0)
		managerFilter.CreatedStart = &t
	}
	if filter.EndTime != nil {
		t := time.Unix(*filter.EndTime, 0)
		managerFilter.CreatedEnd = &t
	}

	result, err := container.BatchManager.ListTasks(req.Context(), managerFilter)
	if err != nil {
		return nil, err
	}

	return &batchTaskListResponse{List: newBatchTaskViews(result.Tasks), NextCursor: result.NextCursor}, nil
}
