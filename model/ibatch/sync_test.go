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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
)

// fakeRedisData 构建按 key 应答的 Redis fake（SCAN 两轮游标）。
func fakeRedisData(tasks map[string]map[string]string, files map[string]map[string]string) *fakeRedisClient {
	return &fakeRedisClient{
		scanFn: func(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error) {
			if match == ScanMatchTasks {
				if cursor == 0 {
					keys := make([]string, 0, len(tasks))
					for k := range tasks {
						keys = append(keys, BatchTaskKey(k))
					}
					return 7, keys, nil // 第二轮回车
				}
				return 0, nil, nil
			}
			if cursor == 0 {
				keys := make([]string, 0, len(files))
				for k := range files {
					p, f, _ := splitCompositeKey(k)
					keys = append(keys, BatchFileKey(p, f))
				}
				return 9, keys, nil
			}
			return 0, nil, nil
		},
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			batchID, ok := ParseBatchTaskKey(key)
			if ok {
				return tasks[batchID], nil
			}
			partition, fileID, ok := ParseBatchFileKey(key)
			if ok {
				return files[partition+":"+fileID], nil
			}
			return nil, nil
		},
	}
}

func splitCompositeKey(composite string) (string, string, bool) {
	for i := 0; i < len(composite); i++ {
		if composite[i] == ':' {
			return composite[:i], composite[i+1:], true
		}
	}
	return "", "", false
}

func apiKeyQuerierWithProduct(product string) *fakeAPIKeyQuerier {
	return &fakeAPIKeyQuerier{
		fetchFn: func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
			p := product
			return []*api_key.APIKeyParam{{ID: filter.ID, ProductName: &p, EntityID: filter.ID}}, nil
		},
	}
}

func TestSyncFromRedis(t *testing.T) {
	tasks := map[string]map[string]string{
		"b-1": {"api_key_id": "ak", "provider": "openai", "status": "in_progress", "est_lines": "4"},
		"b-2": {"api_key_id": "ak", "cluster": "c-9", "status": "completed", "usage_in": "9", "usage_out": "1", "settle_status": "settled"},
	}
	files := map[string]map[string]string{
		"openai:f-1": {"api_key_id": "ak", "lines": "3", "bytes": "10", "dir": "input"},
	}

	t.Run("SCAN 同步幂等 upsert", func(t *testing.T) {
		storager := &fakeBatchStorager{}
		redis := fakeRedisData(tasks, files)
		m := NewManager(storager, redis, apiKeyQuerierWithProduct("p1"),
			&fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})

		nTasks, nFiles, err := m.SyncFromRedis(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 2, nTasks)
		assert.Equal(t, 1, nFiles)

		require.Len(t, storager.upsertedTasks, 2)
		byID := map[string]*BatchTask{}
		for _, task := range storager.upsertedTasks {
			byID[task.BatchID] = task
		}
		assert.Equal(t, "openai", byID["b-1"].Provider)
		assert.Equal(t, "c-9", byID["b-2"].Provider, "cluster 字段回退")
		assert.Equal(t, TaskStatusCompleted, byID["b-2"].Status)
		assert.Equal(t, SettleStatusSettled, byID["b-2"].SettleStatus)
		require.NotNil(t, byID["b-2"].TerminalAt, "同步即终态应记 terminal_at")
		assert.Equal(t, SyncSourceRedis, byID["b-1"].SyncSource)

		require.Len(t, storager.upsertedFiles, 1)
		assert.Equal(t, "f-1", storager.upsertedFiles[0].FileID)
		assert.Equal(t, "openai", storager.upsertedFiles[0].Provider)
		require.NotNil(t, storager.upsertedFiles[0].LastSeenAt)
	})

	t.Run("终态不回退 + 可变字段刷新", func(t *testing.T) {
		existingTerminalAt := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
		storager := &fakeBatchStorager{
			fetchTaskFn: func(ctx context.Context, filter *BatchTaskFilter) (*BatchTask, error) {
				if *filter.IdemKey == "b-2" {
					return &BatchTask{
						ID: 5, BatchID: "b-2", IdemKey: "b-2", ProductName: "p1",
						Status: TaskStatusCompleted, TerminalAt: &existingTerminalAt,
						SettleStatus: SettleStatusSettled, UsageInputTokens: 9,
					}, nil
				}
				return nil, nil
			},
		}
		redis := fakeRedisData(tasks, files)
		m := NewManager(storager, redis, apiKeyQuerierWithProduct("p1"),
			&fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})

		_, _, err := m.SyncFromRedis(context.Background())
		require.NoError(t, err)

		var b2 *BatchTask
		for _, task := range storager.upsertedTasks {
			if task.BatchID == "b-2" {
				b2 = task
			}
		}
		require.NotNil(t, b2)
		assert.Equal(t, TaskStatusCompleted, b2.Status)
		assert.Equal(t, existingTerminalAt, *b2.TerminalAt, "终态 terminal_at 不回退")
		assert.Equal(t, int64(5), b2.ID)
	})

	t.Run("api_key 缺失跳过", func(t *testing.T) {
		storager := &fakeBatchStorager{}
		redis := fakeRedisData(tasks, files)
		apiKeys := &fakeAPIKeyQuerier{
			fetchFn: func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
				return nil, nil // 全部找不到
			},
		}
		m := NewManager(storager, redis, apiKeys,
			&fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})

		nTasks, nFiles, err := m.SyncFromRedis(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 0, nTasks)
		assert.Equal(t, 0, nFiles)
		assert.Empty(t, storager.upsertedTasks)
	})

	t.Run("脏数据键跳过不影响其他键", func(t *testing.T) {
		dirtyTasks := map[string]map[string]string{
			"b-bad": {"api_key_id": "ak"}, // 无 status
			"b-ok":  {"api_key_id": "ak", "status": "queued"},
		}
		storager := &fakeBatchStorager{}
		redis := fakeRedisData(dirtyTasks, nil)
		m := NewManager(storager, redis, apiKeyQuerierWithProduct("p1"),
			&fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})

		nTasks, _, err := m.SyncFromRedis(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 1, nTasks)
		require.Len(t, storager.upsertedTasks, 1)
		assert.Equal(t, "b-ok", storager.upsertedTasks[0].BatchID)
	})

	t.Run("SCAN 错误中止本轮", func(t *testing.T) {
		redis := &fakeRedisClient{
			scanFn: func(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error) {
				return 0, nil, errors.New("redis scan down")
			},
		}
		m := NewManager(&fakeBatchStorager{}, redis, apiKeyQuerierWithProduct("p1"),
			&fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})
		_, _, err := m.SyncFromRedis(context.Background())
		require.Error(t, err)
	})

	t.Run("nil redis 降级", func(t *testing.T) {
		m := NewManager(&fakeBatchStorager{}, nil, apiKeyQuerierWithProduct("p1"),
			&fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})
		n, f, err := m.SyncFromRedis(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.Equal(t, 0, f)
	})
}
