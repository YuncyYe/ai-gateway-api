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
	"strings"
	"testing"
	"time"

	golibquota "github.com/bfenetworks/go-lib/quota"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/imodel_price"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
)

const testJSONL = `{"id":"r-1","custom_id":"c-1","response":{"model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}},"error":null}
{"id":"r-2","custom_id":"c-2","response":{"model":"gpt-4o","usage":{"prompt_tokens":200,"completion_tokens":100,"total_tokens":300}},"error":null}
{"id":"r-3","custom_id":"c-3","error":{"message":"boom"}}
`

// testJSONLBody 是 OpenAI Batch 契约形态（usage 在 response.body 内，
// issue #254 的复现形态）。
const testJSONLBody = `{"id":"r-1","custom_id":"c-1","response":{"status_code":200,"body":{"model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}},"error":null}
{"id":"r-2","custom_id":"c-2","response":{"status_code":200,"body":{"model":"gpt-4o","usage":{"prompt_tokens":200,"completion_tokens":100,"total_tokens":300}}},"error":null}
{"id":"r-3","custom_id":"c-3","response":null,"error":{"message":"boom"}}
`

// setupSettleFixture 组装兜底结算夹具。
func setupSettleFixture(t *testing.T, tasks []*BatchTask, jsonl string, statusCode int, price *imodel_price.ModelPrice) (*Manager, *fakeBatchStorager, *fakeRedisClient) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/files/file-out/content") {
			t.Errorf("unexpected download path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/jsonl")
		w.WriteHeader(statusCode)
		fmt.Fprint(w, jsonl)
	}))
	t.Cleanup(server.Close)

	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			if filter.Status != nil && *filter.Status == TaskStatusCompleted {
				return tasks, nil
			}
			return nil, nil // release 候选默认无
		},
		updateSettleFn: func(ctx context.Context, batchID, productName, fromStatus string, patch *BatchSettlePatch) (int64, error) {
			return 1, nil
		},
	}
	redis := &fakeRedisClient{}
	addr, port := splitAddrPort(t, server.URL)
	providers := &fakeProviderQuerier{
		fetchFn: func(ctx context.Context, filter *iprovider.ProviderFilter) (*iprovider.Provider, error) {
			return &iprovider.Provider{
				Name:         "openai",
				Keys:         []iprovider.ProviderKey{{Name: "k1", Key: "sk"}},
				InstancePool: []iprovider.ProviderInstance{{Addr: addr, Port: port}},
			}, nil
		},
	}
	prices := &fakePriceQuerier{
		fetchFn: func(ctx context.Context, filter *imodel_price.ModelPriceFilter) (*imodel_price.ModelPrice, error) {
			assert.Equal(t, "batch", *filter.Mode)
			return price, nil
		},
	}
	terminalAt := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	for _, task := range tasks {
		if task.TerminalAt == nil {
			task.TerminalAt = &terminalAt
		}
	}
	m := NewManager(storager, redis,
		&fakeAPIKeyQuerier{}, providers, prices,
		func() *http.Client { return server.Client() },
		&fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)})
	return m, storager, redis
}

func completedTask() *BatchTask {
	return &BatchTask{
		ID: 1, BatchID: "b-1", ProductName: "p1", Provider: "openai", KeyName: "k1",
		Status: TaskStatusCompleted, SettleStatus: SettleStatusReserved,
		OutputFileID: "file-out", ReserveUnits: 100,
		CreatedAt: time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC),
	}
}

func TestReconcileSettle_FullChain(t *testing.T) {
	price := &imodel_price.ModelPrice{
		Provider: "openai", Model: "gpt-4o", Mode: "batch",
		Prices: imodel_price.PriceMap{
			"input_cost_per_token":  0.00001,
			"output_cost_per_token": 0.00002,
		},
	}
	m, storager, redis := setupSettleFixture(t, []*BatchTask{completedTask()}, testJSONL, 200, price)

	// 预留簿记。
	redis.hGetAllFn = func(ctx context.Context, key string) (map[string]string, error) {
		if key == "BATCH_RESERVE_BATCH:b-1" {
			return map[string]string{"plan-a": "100"}, nil
		}
		return nil, nil
	}

	settled, released, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, settled)
	assert.Equal(t, 0, released)

	// usage 汇总：input 300 / output 150（错误行跳过）。
	expectedUnits := golibquota.CalcCostUnits(300, 0.00001) + golibquota.CalcCostUnits(150, 0.00002)

	// Redis 结算写回。
	require.Len(t, redis.hSets, 1)
	assert.Equal(t, "BATCH_TASK:b-1", redis.hSets[0].key)
	assert.Equal(t, "300", redis.hSets[0].fields["usage_in"])
	assert.Equal(t, "150", redis.hSets[0].fields["usage_out"])
	assert.Equal(t, fmt.Sprintf("%d", expectedUnits), redis.hSets[0].fields["settle_units"])
	assert.Equal(t, SettleStatusSettled, redis.hSets[0].fields["settle_status"])

	// 预留冲正。
	require.Len(t, redis.decrRecords, 1)
	assert.Contains(t, redis.deletedKeys, "BATCH_RESERVE_BATCH:b-1")

	// DB 条件更新。
	require.Len(t, storager.settlePatches, 1)
	patch := storager.settlePatches[0]
	assert.Equal(t, SettleStatusSettled, patch.SettleStatus)
	require.NotNil(t, patch.SettleUnits)
	assert.Equal(t, expectedUnits, *patch.SettleUnits)
	require.NotNil(t, patch.UsageInputTokens)
	assert.Equal(t, int64(300), *patch.UsageInputTokens)
	require.NotNil(t, patch.UsageSource)
	assert.Equal(t, UsageSourceReconcile, *patch.UsageSource)
	// 300*1e-8*1e8=300 units... 预留 100 → 透支。
	require.NotNil(t, patch.OverReserved)
	assert.True(t, *patch.OverReserved, "settle 超出预留应打 over_reserved")
}

func TestReconcileSettle_OverReservedFalseWithinReserve(t *testing.T) {
	price := &imodel_price.ModelPrice{
		Provider: "openai", Model: "gpt-4o", Mode: "batch",
		Prices: imodel_price.PriceMap{"input_cost_per_token": 0.0000001},
	}
	task := completedTask()
	task.ReserveUnits = 1 << 40 // 巨额预留
	m, storager, _ := setupSettleFixture(t, []*BatchTask{task}, testJSONL, 200, price)

	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, settled)
	require.Len(t, storager.settlePatches, 1)
	assert.False(t, *storager.settlePatches[0].OverReserved)
}

func TestReconcileSettle_ClaimAlreadyTaken(t *testing.T) {
	m, storager, redis := setupSettleFixture(t, []*BatchTask{completedTask()}, testJSONL, 200, &imodel_price.ModelPrice{})
	redis.setNXWithTTLFn = func(ctx context.Context, key string, ttlSeconds int) (bool, error) {
		return false, nil // 下载拦截已结算
	}

	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, settled)
	assert.Empty(t, storager.settlePatches)
	assert.Empty(t, redis.hSets)
}

func TestReconcileSettle_ClaimErrorAborts(t *testing.T) {
	m, _, redis := setupSettleFixture(t, []*BatchTask{completedTask()}, testJSONL, 200, &imodel_price.ModelPrice{})
	redis.setNXWithTTLFn = func(ctx context.Context, key string, ttlSeconds int) (bool, error) {
		return false, errors.New("redis down")
	}
	_, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err, "单任务失败仅告警，不中断本轮")
}

func TestReconcileSettle_PriceMissSettlesZero(t *testing.T) {
	m, storager, redis := setupSettleFixture(t, []*BatchTask{completedTask()}, testJSONL, 200, nil)

	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, settled, "查不到 batch 价不阻塞：settle_units=0 记待人工")

	require.Len(t, storager.settlePatches, 1)
	assert.Equal(t, int64(0), *storager.settlePatches[1-1].SettleUnits)
	require.Len(t, redis.hSets, 1)
	assert.Equal(t, "0", redis.hSets[0].fields["settle_units"])
}

func TestReconcileSettle_PriceQueryErrorRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testJSONL)
	}))
	t.Cleanup(server.Close)

	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			if filter.Status != nil && *filter.Status == TaskStatusCompleted {
				return []*BatchTask{completedTask()}, nil
			}
			return nil, nil
		},
		updateSettleFn: func(ctx context.Context, batchID, productName, fromStatus string, patch *BatchSettlePatch) (int64, error) {
			return 1, nil
		},
	}
	redis := &fakeRedisClient{}
	addr, port := splitAddrPort(t, server.URL)
	providers := &fakeProviderQuerier{
		fetchFn: func(ctx context.Context, filter *iprovider.ProviderFilter) (*iprovider.Provider, error) {
			return &iprovider.Provider{
				Name:         "openai",
				Keys:         []iprovider.ProviderKey{{Name: "k1", Key: "sk"}},
				InstancePool: []iprovider.ProviderInstance{{Addr: addr, Port: port}},
			}, nil
		},
	}
	prices := &fakePriceQuerier{
		fetchFn: func(ctx context.Context, filter *imodel_price.ModelPriceFilter) (*imodel_price.ModelPrice, error) {
			return nil, errors.New("db down")
		},
	}
	m := NewManager(storager, redis, &fakeAPIKeyQuerier{}, providers, prices,
		func() *http.Client { return server.Client() },
		&fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)})

	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, settled)
	assert.Empty(t, storager.settlePatches)
	// 结算锁应释放以便下轮重试。
	assert.Contains(t, redis.deletedKeys, "BATCH_SETTLED:b-1")
}

func TestReconcileSettle_DownloadNotFoundRetries(t *testing.T) {
	m, storager, redis := setupSettleFixture(t, []*BatchTask{completedTask()}, "", 404, &imodel_price.ModelPrice{})

	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, settled)
	assert.Empty(t, storager.settlePatches)
	assert.Contains(t, redis.deletedKeys, "BATCH_SETTLED:b-1", "404 应释放锁下轮重试")
}

func TestReconcileSettle_NoOutputFileReleases(t *testing.T) {
	task := completedTask()
	task.OutputFileID = ""
	m, storager, redis := setupSettleFixture(t, []*BatchTask{task}, "", 200, &imodel_price.ModelPrice{})

	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, settled)

	// 走释放路径：BATCH_SETTLED 让出 + settle_status → released。
	assert.Contains(t, redis.deletedKeys, "BATCH_SETTLED:b-1")
	require.Len(t, storager.settlePatches, 1)
	assert.Equal(t, SettleStatusReleased, storager.settlePatches[0].SettleStatus)
}

func TestReconcileSettle_ReleaseCandidates(t *testing.T) {
	expiredTask := &BatchTask{
		ID: 2, BatchID: "b-2", ProductName: "p1", Provider: "openai",
		Status: TaskStatusExpired, SettleStatus: SettleStatusReserved,
		CreatedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
	}
	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			if len(filter.StatusIn) > 0 {
				return []*BatchTask{expiredTask}, nil
			}
			return nil, nil
		},
		updateSettleFn: func(ctx context.Context, batchID, productName, fromStatus string, patch *BatchSettlePatch) (int64, error) {
			return 1, nil
		},
	}
	redis := &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			return map[string]string{"plan-x": "42"}, nil
		},
	}
	m := NewManager(storager, redis, &fakeAPIKeyQuerier{}, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})

	settled, released, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, settled)
	assert.Equal(t, 1, released)

	require.Len(t, redis.decrRecords, 1)
	assert.Equal(t, "BATCH_RESERVE:plan-x", redis.decrRecords[0].key)
	assert.Equal(t, int64(42), redis.decrRecords[0].delta)
	require.Len(t, storager.settlePatches, 1)
	assert.Equal(t, SettleStatusReleased, storager.settlePatches[0].SettleStatus)
}

func TestReconcileSettle_ReleaseReserveError(t *testing.T) {
	storager := &fakeBatchStorager{
		listTasksFn: func(ctx context.Context, filter *BatchTaskFilter) ([]*BatchTask, error) {
			if len(filter.StatusIn) > 0 {
				return []*BatchTask{{
					ID: 3, BatchID: "b-3", ProductName: "p1", Provider: "openai",
					Status: TaskStatusFailed, SettleStatus: SettleStatusReserved,
				}}, nil
			}
			return nil, nil
		},
	}
	redis := &fakeRedisClient{
		hGetAllFn: func(ctx context.Context, key string) (map[string]string, error) {
			return nil, errors.New("redis down")
		},
	}
	m := NewManager(storager, redis, &fakeAPIKeyQuerier{}, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})
	_, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Empty(t, storager.settlePatches)
}

func TestReconcileSettle_WithinGraceSkipped(t *testing.T) {
	task := completedTask()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	terminalAt := now.Add(-10 * time.Minute) // 宽限 30min 内
	task.TerminalAt = &terminalAt
	m, storager, _ := setupSettleFixture(t, []*BatchTask{task}, testJSONL, 200, &imodel_price.ModelPrice{})

	// setupSettleFixture 使用同一时钟；宽限内不触发。
	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, settled)
	assert.Empty(t, storager.settlePatches)
}

func TestReconcileSettle_NilRedis(t *testing.T) {
	m := NewManager(&fakeBatchStorager{}, nil, &fakeAPIKeyQuerier{}, &fakeProviderQuerier{}, &fakePriceQuerier{}, nil, &fakeClock{})
	s, r, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, s)
	assert.Equal(t, 0, r)
}

func TestFilterBySettleGrace(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	grace := 30 * time.Minute

	withTerminal := completedTask()
	withTerminal.TerminalAt = &[]time.Time{now.Add(-31 * time.Minute)}[0]
	withoutTerminal := completedTask() // terminal_at nil → 回退 updated_at(11:00) → 超宽限
	fresh := completedTask()
	fresh.TerminalAt = &[]time.Time{now.Add(-5 * time.Minute)}[0]

	rst := filterBySettleGrace([]*BatchTask{withTerminal, withoutTerminal, fresh}, now, grace)
	assert.Len(t, rst, 2)
}

func TestParseJSONLUsage(t *testing.T) {
	t.Run("多行汇总与错误行跳过", func(t *testing.T) {
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader(testJSONL), 1<<20, totals)
		require.NoError(t, err)
		assert.Equal(t, int64(300), totals.inputTokens)
		assert.Equal(t, int64(150), totals.outputTokens)
		assert.Equal(t, "gpt-4o", totals.model)
		assert.Equal(t, 2, totals.parsedLines)
	})

	t.Run("无尾换行末行也解析", func(t *testing.T) {
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader(`{"response":{"model":"m","usage":{"prompt_tokens":5,"completion_tokens":6}}}`), 1<<20, totals)
		require.NoError(t, err)
		assert.Equal(t, int64(5), totals.inputTokens)
		assert.Equal(t, int64(6), totals.outputTokens)
	})

	t.Run("超长行丢弃并恢复", func(t *testing.T) {
		shortLine := `{"response":{"model":"m","usage":{"prompt_tokens":2,"completion_tokens":2}}}`
		longLine := `{"response":{"model":"m","usage":{"prompt_tokens":1,"completion_tokens":1}}}` + strings.Repeat(" ", 100)
		jsonl := longLine + "\n" + shortLine + "\n"
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader(jsonl), int64(len(shortLine)+2), totals)
		require.NoError(t, err)
		assert.Equal(t, int64(2), totals.inputTokens, "超长行跳过、后续行恢复解析")
		assert.Equal(t, 1, totals.parsedLines)
	})

	t.Run("超长末行无换行", func(t *testing.T) {
		jsonl := `{"response":{"usage":{"prompt_tokens":9,"completion_tokens":9}}}` + strings.Repeat(" ", 100)
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader(jsonl), 64, totals)
		require.NoError(t, err)
		assert.Equal(t, 0, totals.parsedLines)
	})

	t.Run("response.body.usage 契约形态（issue #254）", func(t *testing.T) {
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader(testJSONLBody), 1<<20, totals)
		require.NoError(t, err)
		assert.Equal(t, int64(300), totals.inputTokens)
		assert.Equal(t, int64(150), totals.outputTokens)
		assert.Equal(t, "gpt-4o", totals.model)
		assert.Equal(t, 2, totals.parsedLines)
		assert.Equal(t, 0, totals.noUsageLines, "错误行不计 noUsage")
		assert.Equal(t, 0, totals.badLines)
	})

	t.Run("两种形态混合：body 优先，扁平兼容", func(t *testing.T) {
		jsonl := `{"response":{"body":{"model":"m-body","usage":{"prompt_tokens":1,"completion_tokens":2}}}}` + "\n" +
			`{"response":{"model":"m-flat","usage":{"prompt_tokens":3,"completion_tokens":4}}}` + "\n" +
			`{"response":{"body":{"model":"m-body","usage":{"prompt_tokens":5,"completion_tokens":6}}},"response_extra":null}` + "\n"
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader(jsonl), 1<<20, totals)
		require.NoError(t, err)
		assert.Equal(t, int64(9), totals.inputTokens)
		assert.Equal(t, int64(12), totals.outputTokens)
		assert.Equal(t, "m-body", totals.model, "首个可计费行的 model（body 优先）")
		assert.Equal(t, 3, totals.parsedLines)
	})

	t.Run("有 response 无 usage 计 noUsageLines", func(t *testing.T) {
		jsonl := `{"response":{"body":{"model":"m"}}}` + "\n" +
			`{"response":{"model":"m"}}` + "\n"
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader(jsonl), 1<<20, totals)
		require.NoError(t, err)
		assert.Equal(t, 0, totals.parsedLines)
		assert.Equal(t, 2, totals.noUsageLines)
	})

	t.Run("非法 JSON 计 badLines", func(t *testing.T) {
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader("not-a-json-line\n"), 1<<20, totals)
		require.NoError(t, err)
		assert.Equal(t, 1, totals.badLines)
		assert.Equal(t, 0, totals.noUsageLines)
	})

	t.Run("空行与空白跳过", func(t *testing.T) {
		totals := &usageTotals{}
		err := parseJSONLUsage(strings.NewReader("\n   \n"), 1<<20, totals)
		require.NoError(t, err)
		assert.Equal(t, 0, totals.parsedLines)
	})
}

func TestCalcSettleUnits(t *testing.T) {
	m := newTestManager(&fakeBatchStorager{}, nil)

	t.Run("价格缺失记 miss", func(t *testing.T) {
		units, miss, err := m.calcSettleUnits(context.Background(), &BatchTask{Provider: "openai"}, &usageTotals{model: "m", inputTokens: 10})
		require.NoError(t, err)
		assert.True(t, miss)
		assert.Equal(t, int64(0), units)
	})

	t.Run("价格键全缺记 miss", func(t *testing.T) {
		m2 := NewManager(&fakeBatchStorager{}, nil, &fakeAPIKeyQuerier{}, &fakeProviderQuerier{},
			&fakePriceQuerier{fetchFn: func(ctx context.Context, filter *imodel_price.ModelPriceFilter) (*imodel_price.ModelPrice, error) {
				return &imodel_price.ModelPrice{Prices: imodel_price.PriceMap{"other_key": 1}}, nil
			}}, nil, &fakeClock{})
		units, miss, err := m2.calcSettleUnits(context.Background(), &BatchTask{Provider: "openai"}, &usageTotals{model: "m", inputTokens: 10})
		require.NoError(t, err)
		assert.True(t, miss)
		assert.Equal(t, int64(0), units)
	})

	t.Run("查询错误透传", func(t *testing.T) {
		m2 := NewManager(&fakeBatchStorager{}, nil, &fakeAPIKeyQuerier{}, &fakeProviderQuerier{},
			&fakePriceQuerier{fetchFn: func(ctx context.Context, filter *imodel_price.ModelPriceFilter) (*imodel_price.ModelPrice, error) {
				return nil, errors.New("db down")
			}}, nil, &fakeClock{})
		_, _, err := m2.calcSettleUnits(context.Background(), &BatchTask{Provider: "openai"}, &usageTotals{model: "m"})
		require.Error(t, err)
	})
}

func TestNoSettleReleaseStatuses(t *testing.T) {
	assert.Equal(t, []string{TaskStatusExpired, TaskStatusFailed, TaskStatusCancelled}, noSettleReleaseStatuses)
	// completed 不在整额释放集合（先实结，预留随结算冲正）。
	assert.NotContains(t, noSettleReleaseStatuses, TaskStatusCompleted)
	assert.NotContains(t, noSettleReleaseStatuses, TaskStatusCancelling)
}

// issue #254 安全阀：结果文件里的行有 response 却一行 usage 都取不到时，
// 不得把批次推进为 settled（修复前扁平解析正是这种形态，会造成 0 元结算）。
func TestReconcileSettle_NoUsageNotSettled(t *testing.T) {
	noUsageJSONL := `{"custom_id":"c-1","response":{"status_code":200,"body":{"model":"gpt-4o"}},"error":null}` + "\n" +
		`{"custom_id":"c-2","response":null,"error":{"message":"boom"}}` + "\n"
	price := &imodel_price.ModelPrice{
		Provider: "openai", Model: "gpt-4o", Mode: "batch",
		Prices: imodel_price.PriceMap{"input_cost_per_token": 0.00001},
	}
	m, storager, redis := setupSettleFixture(t, []*BatchTask{completedTask()}, noUsageJSONL, 200, price)

	settled, released, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, settled, "有 response 无 usage 时不得判 settled")
	assert.Equal(t, 0, released)
	assert.Empty(t, storager.settlePatches, "不得推进 settle_status")
	assert.Empty(t, redis.hSets, "不得写回 Redis 结算字段")
}

// 纯错误行（response 全为 null）是合法形态：照常按 0 元结算。
func TestReconcileSettle_OnlyErrorRowsSettlesZero(t *testing.T) {
	errorOnly := `{"custom_id":"c-1","response":null,"error":{"message":"boom"}}` + "\n"
	price := &imodel_price.ModelPrice{
		Provider: "openai", Model: "gpt-4o", Mode: "batch",
		Prices: imodel_price.PriceMap{"input_cost_per_token": 0.00001},
	}
	m, storager, redis := setupSettleFixture(t, []*BatchTask{completedTask()}, errorOnly, 200, price)

	settled, _, err := m.ReconcileSettle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, settled, "全部请求失败的结果文件应正常结算 0")
	require.Len(t, redis.hSets, 1)
	assert.Equal(t, "0", redis.hSets[0].fields["usage_in"])
	assert.Equal(t, SettleStatusSettled, redis.hSets[0].fields["settle_status"])
	require.Len(t, storager.settlePatches, 1)
}
