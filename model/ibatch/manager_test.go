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

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
)

func TestFetchTask_DBHit(t *testing.T) {
	storager := &fakeBatchStorager{
		fetchTaskFn: func(ctx context.Context, filter *BatchTaskFilter) (*BatchTask, error) {
			require.NotNil(t, filter.ProductName)
			assert.Equal(t, "b-1", *filter.BatchID)
			return &BatchTask{ID: 7, BatchID: "b-1", ProductName: "p1", Status: TaskStatusInProgress}, nil
		},
	}
	m := newTestManager(storager, &fakeRedisClient{})

	task, err := m.FetchTask(context.Background(), "b-1", "p1", true)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, int64(7), task.ID)
}

func TestFetchTask_RedisFallback(t *testing.T) {
	storager := &fakeBatchStorager{}
	redis := &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			assert.Equal(t, "BATCH_TASK:b-9", key)
			return map[string]string{
				"api_key_id":    "api-key-1",
				"provider":      "openai",
				"key_name":      "k1",
				"input_file_id": "file-in",
				"est_lines":     "3",
				"status":        "in_progress",
				"usage_in":      "10",
				"usage_out":     "20",
			}, nil
		},
	}
	apiKeys := &fakeAPIKeyQuerier{
		fetchFn: func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
			product := "p1"
			entity := "e-1"
			return []*api_key.APIKeyParam{{ID: filter.ID, ProductName: &product, EntityID: &entity}}, nil
		},
	}
	m := NewManager(storager, redis, apiKeys, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})

	task, err := m.FetchTask(context.Background(), "b-9", "p1", true)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "b-9", task.BatchID)
	assert.Equal(t, "p1", task.ProductName)
	assert.Equal(t, "e-1", task.EntityID)
	assert.Equal(t, "openai", task.Provider)
	assert.Equal(t, int64(3), task.EstLines)
	assert.Equal(t, int64(10), task.UsageInputTokens)
	assert.Equal(t, SettleStatusReserved, task.SettleStatus)
}

func TestFetchTask_RedisFallbackProductMismatch(t *testing.T) {
	storager := &fakeBatchStorager{}
	redis := &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			return map[string]string{"api_key_id": "api-key-1", "provider": "openai", "status": "queued"}, nil
		},
	}
	apiKeys := &fakeAPIKeyQuerier{
		fetchFn: func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
			product := "other-product"
			return []*api_key.APIKeyParam{{ID: filter.ID, ProductName: &product}}, nil
		},
	}
	m := NewManager(storager, redis, apiKeys, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})

	task, err := m.FetchTask(context.Background(), "b-9", "p1", true)
	require.NoError(t, err)
	assert.Nil(t, task, "跨产品线任务必须按 miss 处理")
}

func TestFetchTask_BothMiss(t *testing.T) {
	m := newTestManager(&fakeBatchStorager{}, &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			return nil, nil
		},
	})

	task, err := m.FetchTask(context.Background(), "b-x", "p1", true)
	require.NoError(t, err)
	assert.Nil(t, task)

	// 不允许回源时不应触碰 Redis。
	called := false
	m2 := newTestManager(&fakeBatchStorager{}, &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			called = true
			return nil, nil
		},
	})
	task, err = m2.FetchTask(context.Background(), "b-x", "p1", false)
	require.NoError(t, err)
	assert.Nil(t, task)
	assert.False(t, called)
}

func TestFetchTask_DirtyRedisRecordMiss(t *testing.T) {
	m := newTestManager(&fakeBatchStorager{}, &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			return map[string]string{"api_key_id": "api-key-1"}, nil // 无 status
		},
	})
	task, err := m.FetchTask(context.Background(), "b-x", "p1", true)
	require.NoError(t, err)
	assert.Nil(t, task)
}

func TestFetchFile_DBHitAndRedisFallback(t *testing.T) {
	storager := &fakeBatchStorager{
		fetchFileFn: func(ctx context.Context, filter *BatchFileFilter) (*BatchFile, error) {
			return &BatchFile{ID: 3, FileID: "f-1", Provider: "openai", ProductName: "p1"}, nil
		},
	}
	m := newTestManager(storager, &fakeRedisClient{})
	file, err := m.FetchFile(context.Background(), "f-1", "openai", "p1", true)
	require.NoError(t, err)
	require.NotNil(t, file)
	assert.Equal(t, int64(3), file.ID)

	// Redis 回源。
	storager2 := &fakeBatchStorager{}
	redis := &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			assert.Equal(t, "BATCH_FILE:openai:f-2", key)
			return map[string]string{
				"api_key_id": "api-key-1",
				"key_name":   "k1",
				"lines":      "5",
				"bytes":      "1024",
				"purpose":    "batch",
				"dir":        "input",
			}, nil
		},
	}
	apiKeys := &fakeAPIKeyQuerier{
		fetchFn: func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
			product := "p1"
			return []*api_key.APIKeyParam{{ID: filter.ID, ProductName: &product}}, nil
		},
	}
	m2 := NewManager(storager2, redis, apiKeys, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})
	file, err = m2.FetchFile(context.Background(), "f-2", "openai", "p1", true)
	require.NoError(t, err)
	require.NotNil(t, file)
	assert.Equal(t, "f-2", file.FileID)
	assert.Equal(t, "openai", file.Provider)
	assert.Equal(t, int64(5), file.Lines)
	assert.Equal(t, FileDirectionInput, file.Direction)
}

func TestListTasks(t *testing.T) {
	t.Run("product_name 强制", func(t *testing.T) {
		m := newTestManager(&fakeBatchStorager{}, nil)
		_, err := m.ListTasks(context.Background(), &BatchTaskFilter{})
		require.Error(t, err)
		rr := xerror.Resolve(err)
		assert.Equal(t, 422, rr.ErrNo)
	})

	t.Run("limit 默认值与上限", func(t *testing.T) {
		var gotFilter *BatchTaskFilter
		storager := &fakeBatchStorager{
			listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
				gotFilter = filter
				return nil, nil
			},
		}
		m := newTestManager(storager, nil)
		product := "p1"

		_, err := m.ListTasks(context.Background(), &BatchTaskFilter{ProductName: &product})
		require.NoError(t, err)
		assert.Equal(t, DefaultListLimit, gotFilter.Limit)

		_, err = m.ListTasks(context.Background(), &BatchTaskFilter{ProductName: &product, Limit: 9999})
		require.NoError(t, err)
		assert.Equal(t, MaxListLimit, gotFilter.Limit)
	})

	t.Run("游标分页", func(t *testing.T) {
		storager := &fakeBatchStorager{
			listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
				assert.Equal(t, 2, filter.Limit)
				return []*BatchTask{{ID: 11}, {ID: 12}}, nil
			},
		}
		m := newTestManager(storager, nil)
		product := "p1"
		result, err := m.ListTasks(context.Background(), &BatchTaskFilter{ProductName: &product, Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, 2, len(result.Tasks))
		assert.Equal(t, int64(12), result.NextCursor)
	})

	t.Run("末页 next_cursor 为 0", func(t *testing.T) {
		storager := &fakeBatchStorager{
			listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
				return []*BatchTask{{ID: 11}}, nil
			},
		}
		m := newTestManager(storager, nil)
		product := "p1"
		result, err := m.ListTasks(context.Background(), &BatchTaskFilter{ProductName: &product, Limit: 50})
		require.NoError(t, err)
		assert.Equal(t, int64(0), result.NextCursor)
	})
}

func TestListTasks_StoragerError(t *testing.T) {
	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			return nil, errors.New("db down")
		},
	}
	m := newTestManager(storager, nil)
	product := "p1"
	_, err := m.ListTasks(context.Background(), &BatchTaskFilter{ProductName: &product})
	require.Error(t, err)
}

func TestCancel_ErrorSentinelIdentity(t *testing.T) {
	assert.Equal(t, 404, xerror.Resolve(ErrBatchNotFound).ErrNo)
	assert.Equal(t, 409, xerror.Resolve(errBatchConflict("b-1")).ErrNo)
	assert.True(t, errors.Is(ErrUpstreamUnreachable, ErrUpstreamUnreachable))
	assert.True(t, errors.Is(ErrUpstreamBatchNotFound, ErrUpstreamBatchNotFound))
}

func TestIsTerminalStatus(t *testing.T) {
	assert.True(t, IsTerminalStatus(TaskStatusCompleted))
	assert.True(t, IsTerminalStatus(TaskStatusExpired))
	assert.True(t, IsTerminalStatus(TaskStatusFailed))
	assert.True(t, IsTerminalStatus(TaskStatusCancelled))
	assert.False(t, IsTerminalStatus(TaskStatusCancelling))
	assert.False(t, IsTerminalStatus(TaskStatusQueued))
	assert.False(t, IsTerminalStatus("ended")) // 二期方言暂按非终态处理
}

func TestNewManagerDefaults(t *testing.T) {
	m := NewManager(&fakeBatchStorager{}, nil,
		&fakeAPIKeyQuerier{}, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, nil)
	require.NotNil(t, m)
	assert.Equal(t, defaultProviderTimeout, m.providerTimeout)
	assert.Equal(t, defaultSettleGrace, m.settleGrace)
	assert.Equal(t, defaultAdvanceLimit, m.advanceLimit)
	assert.Equal(t, defaultReconcileLimit, m.reconcileLimit)
	assert.Equal(t, int64(defaultScanCount), m.scanCount)
	assert.Equal(t, int64(defaultMaxOutputLineBytes), m.maxOutputLineBytes)

	// 选项覆盖。
	m = NewManager(&fakeBatchStorager{}, nil,
		&fakeAPIKeyQuerier{}, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, nil,
		WithProviderTimeout(time.Second), WithSettleGrace(time.Minute),
		WithAdvanceLimit(3), WithReconcileLimit(4), WithScanCount(5), WithMaxOutputLineBytes(6))
	assert.Equal(t, time.Second, m.providerTimeout)
	assert.Equal(t, time.Minute, m.settleGrace)
	assert.Equal(t, 3, m.advanceLimit)
	assert.Equal(t, 4, m.reconcileLimit)
	assert.Equal(t, int64(5), m.scanCount)
	assert.Equal(t, int64(6), m.maxOutputLineBytes)

	// 非正值选项不覆盖默认值。
	m = NewManager(&fakeBatchStorager{}, nil,
		&fakeAPIKeyQuerier{}, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, nil,
		WithProviderTimeout(0))
	assert.Equal(t, defaultProviderTimeout, m.providerTimeout)
}
