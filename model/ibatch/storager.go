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

	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/imodel_price"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
)

// BatchStorager 定义批量两表的存储接口（batch_tasks / batch_files）。
// 实现位于 storage/rdb/batch；单测使用手写 callback fake（TESTING.md）。
//
// 所有条件更新（抢占 / settle_status 推进）均返回 affected rows，
// 由 Manager 依据 affected 判定冲突或幂等跳过——条件更新本身是并发防线
// （多实例/重跑不双倍结算，见批量任务与对账.md §6.2）。
type BatchStorager interface {
	// UpsertBatchTask 按 idem_key 幂等 upsert（Redis 同步落库 / 补登）。
	// 已存在行刷新可变字段（status/output_file_id/usage_*/settle_* 等），
	// insert 时幂等键冲突定位。返回自增 ID（冲突更新时为 0）。
	UpsertBatchTask(ctx context.Context, task *BatchTask) (int64, error)
	// FetchBatchTask 按过滤条件查询单条（BatchID/ID/IdemKey）。
	// 未命中返回 (nil, nil)。
	FetchBatchTask(ctx context.Context, filter *BatchTaskFilter) (*BatchTask, error)
	// ListBatchTasks 按过滤条件查询列表（游标分页由 filter.CursorID/Limit 表达）。
	ListBatchTasks(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error)
	// PreemptBatchTaskCancel 抢占式条件更新：
	// SET status='cancelling', updated_at=now
	// WHERE batch_id=? AND product_name=? AND status NOT IN (终态, cancelling)。
	// 返回 affected rows（0 = 已终态/已受理中/无行 → 上层 409；
	// 1 = 本次完成非终态 → cancelling 状态转换，调用方继续出网 cancel）。
	// WHERE 显式排除 cancelling：MySQL 同值 UPDATE 天然 affected=0，而
	// SQLite changes() 按命中计数，排除后双方言语义一致（storage/rdb/batch
	// 实现注释有完整说明）。
	PreemptBatchTaskCancel(ctx context.Context, batchID, productName string) (int64, error)
	// UpdateBatchTaskSettleStatus 三态对账条件更新：
	// WHERE batch_id=? AND settle_status=<fromStatus>，写入 patch 非 nil 字段。
	// 返回 affected rows（0 = 已被他路径推进，幂等跳过）。
	UpdateBatchTaskSettleStatus(ctx context.Context, batchID, productName, fromStatus string, patch *BatchSettlePatch) (int64, error)
	// UpdateBatchTaskProgress 按 ID 部分更新状态推进字段（nil 跳过）。
	UpdateBatchTaskProgress(ctx context.Context, id int64, patch *BatchTaskProgressPatch) (int64, error)

	// UpsertBatchFile 按 (file_id, provider) 幂等 upsert。
	UpsertBatchFile(ctx context.Context, file *BatchFile) (int64, error)
	// FetchBatchFile 按 (file_id, provider) 查询单条。未命中返回 (nil, nil)。
	FetchBatchFile(ctx context.Context, filter *BatchFileFilter) (*BatchFile, error)
	// ListBatchFiles 按过滤条件查询列表。
	ListBatchFiles(ctx context.Context, filter *BatchFileFilter) ([]*BatchFile, error)
}

// RedisClient 是 ibatch 对 Redis 的最小抽象。
//
// 底层组件（bfe_util/redis_client.Client）不暴露 HGETALL/SCAN/SETNX 原语，
// 且 Lua 仅支持单 key；因此 ibatch 面向本接口编程，装配时由容器适配
// （同语义 Lua 或直连实现），单元测试使用手写 fake。各方法语义与数据面
// mod_ai_batch / mod_ai_token_auth 的原子脚本一一对应（单 key，Redis
// Cluster 安全）。
type RedisClient interface {
	// HGetAll 读取 hash 全部字段；key 不存在返回 (nil, nil)。
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	// HSetWithTTL 原子 HSET 字段并刷新 TTL（秒）；对应数据面 luaHashSetWithTtl。
	HSetWithTTL(ctx context.Context, key string, ttlSeconds int, fields map[string]string) error
	// SetNXWithTTL SET NX EX；返回是否抢到锁；对应数据面 luaSetNxWithTtl。
	SetNXWithTTL(ctx context.Context, key string, ttlSeconds int) (bool, error)
	// ReserveDecr 原子扣减预留镜像计数（扣减值钳制到当前值，不为负）；
	// 返回实际扣减数量；对应数据面 luaReserveDecr。
	ReserveDecr(ctx context.Context, key string, delta int64) (int64, error)
	// Delete 删除 key；对应数据面 luaHashDel。
	Delete(ctx context.Context, key string) error
	// Scan 游标扫描（MATCH pattern，COUNT count），返回下一游标与本批 keys。
	Scan(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error)
}

// ProviderQuerier 提供 provider 记录查询（cancel / 兜底结算拼地址与取 key）。
// *iprovider.ProviderManager 满足本接口；key 在 storage 层已解密为明文。
type ProviderQuerier interface {
	FetchProvider(ctx context.Context, filter *iprovider.ProviderFilter) (*iprovider.Provider, error)
}

// BatchPriceQuerier 提供 model_prices 查询（mode=batch 批量价）。
// *imodel_price.Manager 满足本接口。
type BatchPriceQuerier interface {
	FetchModelPrice(ctx context.Context, filter *imodel_price.ModelPriceFilter) (*imodel_price.ModelPrice, error)
}

// APIKeyQuerier 提供 api_keys 查询（Redis 同步落库时解析 product_name /
// entity_id，BATCH_* hash 不携带产品线）。*api_key.APIKeyManager 满足本接口。
type APIKeyQuerier interface {
	FetchAPIKeyList(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error)
}
