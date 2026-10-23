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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
)

// setupAdvanceFixture 组装状态推进夹具：provider 侧由 handler 模拟，
// DB 非终态任务列表由 tasks 给出。
func setupAdvanceFixture(t *testing.T, tasks []*BatchTask, handler http.HandlerFunc) (*Manager, *fakeBatchStorager, *httptest.Server) {
	return setupAdvanceFixtureWithKeys(t, tasks, handler,
		[]iprovider.ProviderKey{{Name: "k1", Key: "sk"}})
}

// setupAdvanceFixtureWithKeys 与 setupAdvanceFixture 相同，但允许指定
// provider keys（nil/空切片 = keyless provider，issue #274）。
func setupAdvanceFixtureWithKeys(t *testing.T, tasks []*BatchTask, handler http.HandlerFunc, keys []iprovider.ProviderKey) (*Manager, *fakeBatchStorager, *httptest.Server) {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			assert.Equal(t, TerminalStatuses, filter.StatusNotIn)
			return tasks, nil
		},
		updateProgressFn: func(ctx context.Context, id int64, patch *BatchTaskProgressPatch) (int64, error) {
			return 1, nil
		},
		upsertFileFn: func(ctx context.Context, file *BatchFile) (int64, error) {
			return 1, nil
		},
	}
	addr, port := splitAddrPort(t, server.URL)
	providers := &fakeProviderQuerier{
		fetchFn: func(ctx context.Context, filter *iprovider.ProviderFilter) (*iprovider.Provider, error) {
			return &iprovider.Provider{
				Name:          "openai",
				Keys:          keys,
				InstancePool:  []iprovider.ProviderInstance{{Addr: addr, Port: port}},
				ProtocolPaths: map[string]string{"openai": "/v1"},
			}, nil
		},
	}
	m := NewManager(storager, &fakeRedisClient{},
		&fakeAPIKeyQuerier{}, providers, &fakePriceQuerier{},
		func() *http.Client { return server.Client() },
		&fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)})
	return m, storager, server
}

func splitAddrPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	addr := rawURL[len("http://"):]
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], atoiOrZero(addr[i+1:])
		}
	}
	return addr, 80
}

func TestAdvanceStatus_StatusForward(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai", KeyName: "k1",
		Status: TaskStatusInProgress, CreatedAt: time.Now().Add(-time.Hour),
	}}

	var gotPath, gotAuth string
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"b-1","status":"completed","output_file_id":"file-out","request_counts":{"completed":10,"failed":1,"total":11}}`)
	})

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, advanced)
	assert.Equal(t, "/v1/batches/b-1", gotPath)
	assert.Equal(t, "Bearer sk", gotAuth)

	require.Len(t, storager.progressPatches, 1)
	patch := storager.progressPatches[0]
	require.NotNil(t, patch.Status)
	assert.Equal(t, TaskStatusCompleted, *patch.Status)
	require.NotNil(t, patch.OutputFileID)
	assert.Equal(t, "file-out", *patch.OutputFileID)
	require.NotNil(t, patch.RequestCounts)
	assert.Equal(t, int64(10), (*patch.RequestCounts)["completed"])
	require.NotNil(t, patch.TerminalAt, "进入终态记 terminal_at")

	// 输出文件补登记。
	require.Len(t, storager.upsertedFiles, 1)
	assert.Equal(t, "file-out", storager.upsertedFiles[0].FileID)
	assert.Equal(t, FileDirectionOutput, storager.upsertedFiles[0].Direction)
	assert.Equal(t, "p1", storager.upsertedFiles[0].ProductName)
}

func TestAdvanceStatus_NoChange(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai",
		Status:        TaskStatusInProgress,
		RequestCounts: map[string]int64{"completed": 1},
	}}
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"b-1","status":"in_progress","request_counts":{"completed":1}}`)
	})

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, advanced)
	assert.Empty(t, storager.progressPatches)
}

func TestAdvanceStatus_ProviderAbsentExpired(t *testing.T) {
	old := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) // > 24h 前
	tasks := []*BatchTask{{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai",
		Status: TaskStatusQueued, CreatedAt: old,
	}}
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, advanced)
	require.Len(t, storager.progressPatches, 1)
	assert.Equal(t, TaskStatusExpired, *storager.progressPatches[0].Status)
	require.NotNil(t, storager.progressPatches[0].TerminalAt)
}

func TestAdvanceStatus_ProviderAbsentWithinGrace(t *testing.T) {
	recent := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC) // 1h 前
	tasks := []*BatchTask{{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai",
		Status: TaskStatusQueued, CreatedAt: recent,
	}}
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, advanced)
	assert.Empty(t, storager.progressPatches)
}

func TestAdvanceStatus_ProviderErrorSkipped(t *testing.T) {
	tasks := []*BatchTask{
		{ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai", Status: TaskStatusQueued, CreatedAt: time.Now()},
		{ID: 2, BatchID: "b-2", ProductName: "p1", Provider: "openai", Status: TaskStatusQueued, CreatedAt: time.Now()},
	}
	calls := 0
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"id":"b-2","status":"failed"}`)
	})

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, advanced, "单任务失败不影响其他任务")
	require.Len(t, storager.progressPatches, 1)
	assert.Equal(t, TaskStatusFailed, *storager.progressPatches[0].Status)
}

func TestAdvanceStatus_BadJSONSkipped(t *testing.T) {
	tasks := []*BatchTask{{ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai", Status: TaskStatusQueued}}
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not-json`)
	})
	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, advanced)
	assert.Empty(t, storager.progressPatches)
}

func TestAdvanceStatus_ListError(t *testing.T) {
	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			return nil, errors.New("db down")
		},
	}
	m := NewManager(storager, nil, &fakeAPIKeyQuerier{}, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})
	_, err := m.AdvanceStatus(context.Background())
	require.Error(t, err)
}

func TestAdvanceStatus_NoProviderQuerier(t *testing.T) {
	m := NewManager(&fakeBatchStorager{}, nil, &fakeAPIKeyQuerier{}, nil, &fakePriceQuerier{}, nil, &fakeClock{})
	n, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestAdvanceStatus_MismatchedID(t *testing.T) {
	tasks := []*BatchTask{{ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai", Status: TaskStatusQueued}}
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"other","status":"completed"}`)
	})
	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, advanced)
	assert.Empty(t, storager.progressPatches)
}

// setupBackfillFixture 组装终态产物补漏夹具：DB 侧返回给定的 completed
// 任务，并记录每次 ListBatchTasks 的过滤条件。
func setupBackfillFixture(t *testing.T, tasks []*BatchTask, handler http.HandlerFunc) (*Manager, *fakeBatchStorager, *[]*BatchTaskFilter) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	filters := make([]*BatchTaskFilter, 0, 1)
	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			filters = append(filters, filter)
			return tasks, nil
		},
		updateProgressFn: func(ctx context.Context, id int64, patch *BatchTaskProgressPatch) (int64, error) {
			return 1, nil
		},
		upsertFileFn: func(ctx context.Context, file *BatchFile) (int64, error) {
			return 1, nil
		},
	}
	addr, port := splitAddrPort(t, server.URL)
	providers := &fakeProviderQuerier{
		fetchFn: func(ctx context.Context, filter *iprovider.ProviderFilter) (*iprovider.Provider, error) {
			return &iprovider.Provider{
				Name:          "openai",
				Keys:          []iprovider.ProviderKey{{Name: "k1", Key: "sk"}},
				InstancePool:  []iprovider.ProviderInstance{{Addr: addr, Port: port}},
				ProtocolPaths: map[string]string{"openai": "/v1"},
			}, nil
		},
	}
	m := NewManager(storager, &fakeRedisClient{},
		&fakeAPIKeyQuerier{}, providers, &fakePriceQuerier{},
		func() *http.Client { return server.Client() },
		&fakeClock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)})
	return m, storager, &filters
}

// issue #254 职责②b：completed 且缺产物的任务由 provider 回读补齐，不改状态。
func TestBackfillTerminalArtifacts_FillsMissingFields(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 7, BatchID: "b-1", ProductName: "p1", Provider: "openai", KeyName: "k1",
		Status:    TaskStatusCompleted,
		CreatedAt: time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC),
	}}
	m, storager, filters := setupBackfillFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/batches/b-1", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"b-1","status":"completed","output_file_id":"file-out",`+
			`"endpoint":"/v1/chat/completions","request_counts":{"completed":2,"failed":0,"total":2}}`)
	})

	backfilled, err := m.BackfillTerminalArtifacts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, backfilled)

	// 候选过滤：completed + 24h 时间窗 + 限量
	require.Len(t, *filters, 1)
	f := (*filters)[0]
	require.NotNil(t, f.Status)
	assert.Equal(t, TaskStatusCompleted, *f.Status)
	require.NotNil(t, f.CreatedStart)
	assert.Equal(t, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), *f.CreatedStart)
	assert.Equal(t, defaultAdvanceLimit, f.Limit)

	require.Len(t, storager.progressPatches, 1)
	patch := storager.progressPatches[0]
	assert.Nil(t, patch.Status, "补漏不得改状态")
	require.NotNil(t, patch.OutputFileID)
	assert.Equal(t, "file-out", *patch.OutputFileID)
	require.NotNil(t, patch.Endpoint)
	assert.Equal(t, "/v1/chat/completions", *patch.Endpoint)
	require.NotNil(t, patch.RequestCounts)
	assert.Equal(t, int64(2), (*patch.RequestCounts)["completed"])

	require.Len(t, storager.upsertedFiles, 1)
	assert.Equal(t, "file-out", storager.upsertedFiles[0].FileID)
}

// 产物已齐备的任务不出网。
func TestBackfillTerminalArtifacts_CompleteTaskSkipped(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 7, BatchID: "b-1", Provider: "openai", KeyName: "k1",
		Status:        TaskStatusCompleted,
		OutputFileID:  "file-out",
		RequestCounts: map[string]int64{"completed": 2},
	}}
	hit := 0
	m, storager, _ := setupBackfillFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		hit++
		fmt.Fprint(w, `{"id":"b-1","status":"completed"}`)
	})

	backfilled, err := m.BackfillTerminalArtifacts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, backfilled)
	assert.Equal(t, 0, hit, "产物齐全时不得出网")
	assert.Empty(t, storager.progressPatches)
}

// provider 404：保持 completed，不降级为 expired（与职责② 的语义差异）。
func TestBackfillTerminalArtifacts_Provider404KeepsStatus(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 7, BatchID: "b-1", Provider: "openai", KeyName: "k1",
		Status:    TaskStatusCompleted,
		CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), // 远超 24h
	}}
	m, storager, _ := setupBackfillFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"message":"not found"}}`)
	})

	backfilled, err := m.BackfillTerminalArtifacts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, backfilled)
	assert.Empty(t, storager.progressPatches, "404 不得改写终态任务")
}

// provider 非 2xx / 非法 JSON：告警跳过，不影响其他任务。
func TestBackfillTerminalArtifacts_ProviderErrorsSkipped(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 7, BatchID: "b-1", Provider: "openai", KeyName: "k1",
		Status: TaskStatusCompleted,
	}}
	m, storager, _ := setupBackfillFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `boom`)
	})
	backfilled, err := m.BackfillTerminalArtifacts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, backfilled)
	assert.Empty(t, storager.progressPatches)

	m2, storager2, _ := setupBackfillFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not-json`)
	})
	backfilled, err = m2.BackfillTerminalArtifacts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, backfilled)
	assert.Empty(t, storager2.progressPatches)
}

// provider 返回的 id 与任务不一致：判为脏数据跳过。
func TestBackfillTerminalArtifacts_MismatchedID(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 7, BatchID: "b-1", Provider: "openai", KeyName: "k1",
		Status: TaskStatusCompleted,
	}}
	m, storager, _ := setupBackfillFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"other","status":"completed","output_file_id":"file-out"}`)
	})
	backfilled, err := m.BackfillTerminalArtifacts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, backfilled)
	assert.Empty(t, storager.progressPatches)
}

// 未注入 providerQuerier（无出网降级）时直接返回零值。
func TestBackfillTerminalArtifacts_NoProviderQuerier(t *testing.T) {
	m := NewManager(&fakeBatchStorager{}, &fakeRedisClient{}, &fakeAPIKeyQuerier{}, nil,
		&fakePriceQuerier{}, nil, &fakeClock{t: time.Now()})
	backfilled, err := m.BackfillTerminalArtifacts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, backfilled)
}

// issue #274：keyless provider 的状态推进 GET 不携带鉴权头。
func TestAdvanceStatus_KeylessProviderNoAuthHeader(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai",
		Status: TaskStatusInProgress, CreatedAt: time.Now().Add(-time.Hour),
	}}
	var authPresent bool
	m, storager, _ := setupAdvanceFixtureWithKeys(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		_, authPresent = r.Header["Authorization"]
		fmt.Fprint(w, `{"id":"b-1","status":"completed"}`)
	}, nil)

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, advanced)
	assert.False(t, authPresent, "keyless provider 的状态推进 GET 不得携带鉴权头")
	require.Len(t, storager.progressPatches, 1)
	assert.Equal(t, TaskStatusCompleted, *storager.progressPatches[0].Status)
}

// issue #274：取消意图不回退——本地 cancelling 且 provider 滞后仍报
// in_progress 时，对账不得降级（验收判据 3），并补发一次幂等 cancel。
// 修复前此用例失败（状态被降回 in_progress）。
func TestAdvanceStatus_CancellingIntentHold(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai", KeyName: "k1",
		Status: TaskStatusCancelling, CreatedAt: time.Now().Add(-time.Hour),
	}}
	cancelReissues := 0
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			cancelReissues++
			w.WriteHeader(http.StatusOK)
			return
		}
		fmt.Fprint(w, `{"id":"b-1","status":"in_progress"}`)
	})

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, advanced)
	assert.Empty(t, storager.progressPatches, "cancelling 不得被对账降回 in_progress")
	assert.Equal(t, 1, cancelReissues, "对账应补发一次幂等 cancel 保证意图落地")
}

// provider 回报终态 cancelled：正常收敛记 terminal_at，且不补发 cancel。
func TestAdvanceStatus_CancellingConvergesToCancelled(t *testing.T) {
	tasks := []*BatchTask{{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai", KeyName: "k1",
		Status: TaskStatusCancelling, CreatedAt: time.Now().Add(-time.Hour),
	}}
	cancelReissues := 0
	m, storager, _ := setupAdvanceFixture(t, tasks, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			cancelReissues++
			w.WriteHeader(http.StatusOK)
			return
		}
		fmt.Fprint(w, `{"id":"b-1","status":"cancelled"}`)
	})

	advanced, err := m.AdvanceStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, advanced)
	assert.Equal(t, 0, cancelReissues, "provider 已回报终态时无需补发 cancel")
	require.Len(t, storager.progressPatches, 1)
	patch := storager.progressPatches[0]
	require.NotNil(t, patch.Status)
	assert.Equal(t, TaskStatusCancelled, *patch.Status)
	require.NotNil(t, patch.TerminalAt)
}
