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

	"github.com/gorilla/mux"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// BatchFileOneAction GET /open-api/v1/batch-files/{file_id}：文件元数据
// 查询。file_id + provider 唯一定位（api-changes.md §4.4：provider 必填
// query 参数）；服务端内部按默认产品线过滤（resolveProduct），DB miss
// 回源 Redis。响应经 batchFileView 投影，product_name 不出现在返回值中
//（issue #276）。
func BatchFileOneAction(req *http.Request) (interface{}, error) {
	product, err := resolveProduct(req.Context())
	if err != nil {
		return nil, err
	}
	fileID := mux.Vars(req)["file_id"]
	if fileID == "" {
		return nil, xerror.WrapParamErrorWithMsg("file_id is required")
	}
	provider := req.URL.Query().Get("provider")
	if provider == "" {
		return nil, xerror.WrapParamErrorWithMsg("provider is required")
	}

	file, err := container.BatchManager.FetchFile(req.Context(), fileID, provider, product.Name, true)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, xerror.WrapRecordNotExist("BatchFile")
	}
	return newBatchFileView(file), nil
}
