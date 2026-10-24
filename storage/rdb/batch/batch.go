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

// Package batch 实现 model/ibatch.BatchStorager：batch_tasks /
// batch_files 两表的 MySQL/SQLite 双方言存储（幂等 upsert、条件更新
// 抢占、游标分页列表）。SQL 经 storage/rdb/internal/dao 层组装。
package batch

import (
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
)

// BatchStorager 批量两表存储实现。
type BatchStorager struct {
	dbCtxFactory lib.DBContextFactory
}

// NewBatchStorager 创建批量存储。dbCtxFactory 惯例为 stateful.NewBFEDBContext。
func NewBatchStorager(dbCtxFactory lib.DBContextFactory) *BatchStorager {
	return &BatchStorager{
		dbCtxFactory: dbCtxFactory,
	}
}

var _ ibatch.BatchStorager = &BatchStorager{}
