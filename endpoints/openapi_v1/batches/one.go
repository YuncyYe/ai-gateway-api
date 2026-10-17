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
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// BatchTaskOneAction GET /open-api/v1/batches/{batch_id}：DB 优先，miss
// 回源 Redis BATCH_TASK（覆盖对账 job 落库前窗口），再 miss 404。
// 返回 *ibatch.BatchTask：Data 顶层即任务字段，关联文件填充 batch_files
//（batches.md §3.2，元素形状 §2.2）。
func BatchTaskOneAction(req *http.Request) (interface{}, error) {
	product, err := resolveProduct(req.Context())
	if err != nil {
		return nil, err
	}
	batchID := mux.Vars(req)["batch_id"]
	if batchID == "" {
		return nil, xerror.WrapParamErrorWithMsg("batch_id is required")
	}

	task, err := container.BatchManager.FetchTask(req.Context(), batchID, product.Name, true)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ibatch.ErrBatchNotFound
	}

	// 关联 batch_files（输入/输出），best-effort：缺失不阻塞详情。
	task.BatchFiles = make([]*ibatch.BatchFileMeta, 0, 2)
	for _, fileID := range []string{task.InputFileID, task.OutputFileID} {
		if fileID == "" {
			continue
		}
		file, err := container.BatchManager.FetchFile(req.Context(), fileID, task.Provider, product.Name, true)
		if err != nil || file == nil {
			continue
		}
		task.BatchFiles = append(task.BatchFiles, file.ToMeta())
	}

	return task, nil
}
