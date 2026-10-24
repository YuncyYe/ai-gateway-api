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

// Package batches 实现批量任务管控 Open API（api-changes.md §4）：
// 任务列表/详情（DB miss 回源 Redis）/取消（控制面直调 provider）与
// 批量文件元数据查询。product_name 由服务端解析（resolveProduct）为
// 默认产品线，本包全部处理器经 resolveProduct 取值，不接受外部传参；
// 对外口径见 batches.md §1/§6（issue #276，2026-10-23 校准）。
package batches

import (
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
)

var Endpoints = []*xreq.Endpoint{
	BatchTaskListRoute,
	BatchTaskOneRoute,
	BatchTaskCancelRoute,
	BatchFileOneRoute,
}

// BatchTaskListRoute GET /open-api/v1/batches
var BatchTaskListRoute = &xreq.Endpoint{
	Path:       "/batches",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(BatchTaskListAction),
	Authorizer: iauth.FA(iauth.FeatureBatch, iauth.ActionRead),
}

// BatchTaskOneRoute GET /open-api/v1/batches/{batch_id}
var BatchTaskOneRoute = &xreq.Endpoint{
	Path:       "/batches/{batch_id}",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(BatchTaskOneAction),
	Authorizer: iauth.FA(iauth.FeatureBatch, iauth.ActionRead),
}

// BatchTaskCancelRoute POST /open-api/v1/batches/{batch_id}/cancel
var BatchTaskCancelRoute = &xreq.Endpoint{
	Path:       "/batches/{batch_id}/cancel",
	Method:     http.MethodPost,
	Handler:    xreq.Convert(BatchTaskCancelAction),
	Authorizer: iauth.FA(iauth.FeatureBatch, iauth.ActionCancel),
}

// BatchFileOneRoute GET /open-api/v1/batch-files/{file_id}
var BatchFileOneRoute = &xreq.Endpoint{
	Path:       "/batch-files/{file_id}",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(BatchFileOneAction),
	Authorizer: iauth.FA(iauth.FeatureBatch, iauth.ActionRead),
}
