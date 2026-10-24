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
//limitations under the License.

package ireport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReportStorager struct {
	overviewFn     func(ctx context.Context, f *Filter) (*OverviewResult, error)
	timeSeriesFn   func(ctx context.Context, metric, dimension string, f *Filter, bucketSec int) ([]*MetricPoint, error)
	rankingsFn     func(ctx context.Context, dimension string, f *Filter, limit int) ([]*RankingItem, error)
	distributionFn func(ctx context.Context, dimension string, f *Filter) ([]*DistItem, error)
	logsFn         func(ctx context.Context, f *LogFilter) (*LogQueryResult, error)
	caps           *BackendCaps
}

func (s *fakeReportStorager) Overview(ctx context.Context, f *Filter) (*OverviewResult, error) {
	if s.overviewFn != nil {
		return s.overviewFn(ctx, f)
	}
	return &OverviewResult{}, nil
}

func (s *fakeReportStorager) TimeSeries(ctx context.Context, metric, dimension string, f *Filter, bucketSec int) ([]*MetricPoint, error) {
	if s.timeSeriesFn != nil {
		return s.timeSeriesFn(ctx, metric, dimension, f, bucketSec)
	}
	return nil, nil
}

func (s *fakeReportStorager) Rankings(ctx context.Context, dimension string, f *Filter, limit int) ([]*RankingItem, error) {
	if s.rankingsFn != nil {
		return s.rankingsFn(ctx, dimension, f, limit)
	}
	return nil, nil
}

func (s *fakeReportStorager) Distribution(ctx context.Context, dimension string, f *Filter) ([]*DistItem, error) {
	if s.distributionFn != nil {
		return s.distributionFn(ctx, dimension, f)
	}
	return nil, nil
}

func (s *fakeReportStorager) Logs(ctx context.Context, f *LogFilter) (*LogQueryResult, error) {
	if s.logsFn != nil {
		return s.logsFn(ctx, f)
	}
	return &LogQueryResult{}, nil
}

// Capabilities returns the injected caps; nil means "support everything"
// (the capability check is a pass-through then).
func (s *fakeReportStorager) Capabilities() *BackendCaps {
	return s.caps
}

// mysqlLikeCaps mimics the MySQL backend: all dimensions supported.
func mysqlLikeCaps() *BackendCaps {
	return &BackendCaps{Backend: "mysql", SupportedDimensions: []string{
		DimensionModel, DimensionRequestedModel, DimensionProvider, DimensionAPIKey,
		DimensionHost, DimensionStatus, DimensionProtocol, DimensionMode, DimensionStream,
		DimensionCacheStatus, DimensionMirrorHit, DimensionIntentAnswer,
	}}
}

// dorisLikeCaps mimics the Doris backend (phase 1): the three new
// dimensions are not declared.
func dorisLikeCaps() *BackendCaps {
	return &BackendCaps{Backend: "doris", SupportedDimensions: []string{
		DimensionModel, DimensionRequestedModel, DimensionProvider, DimensionAPIKey,
		DimensionHost, DimensionStatus, DimensionProtocol, DimensionMode, DimensionStream,
	}}
}

func testWindow() (time.Time, time.Time) {
	return time.Unix(1782345600, 0), time.Unix(1782349200, 0)
}

func TestReportManager_Overview(t *testing.T) {
	var gotFilter *Filter
	storager := &fakeReportStorager{
		overviewFn: func(ctx context.Context, f *Filter) (*OverviewResult, error) {
			gotFilter = f
			return &OverviewResult{RequestTotal: 100, LogsTotal: 100}, nil
		},
	}
	manager := NewReportManager(storager)

	start, end := testWindow()
	stream := true
	result, err := manager.Overview(context.Background(), &OverviewQuery{
		BaseQuery: BaseQuery{
			Start:       start,
			End:         end,
			Models:      []string{"gpt-4o"},
			ApikeyIDs:   []string{"key-1"},
			Providers:   []string{"openai"},
			Hosts:       []string{"gw-01"},
			Stream:      &stream,
			StatusCodes: []int{200, 500},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(100), result.RequestTotal)

	require.NotNil(t, gotFilter)
	assert.Equal(t, start, gotFilter.Start)
	assert.Equal(t, end, gotFilter.End)
	assert.Equal(t, []string{"gpt-4o"}, gotFilter.Models)
	assert.Equal(t, []string{"key-1"}, gotFilter.ApikeyIDs)
	assert.Equal(t, []string{"openai"}, gotFilter.Providers)
	assert.Equal(t, []string{"gw-01"}, gotFilter.Hosts)
	require.NotNil(t, gotFilter.Stream)
	assert.True(t, *gotFilter.Stream)
	assert.Equal(t, []int{200, 500}, gotFilter.StatusCodes)
}

func TestReportManager_Overview_StoragerError(t *testing.T) {
	storager := &fakeReportStorager{
		overviewFn: func(ctx context.Context, f *Filter) (*OverviewResult, error) {
			return nil, errors.New("dao boom")
		},
	}
	manager := NewReportManager(storager)

	start, end := testWindow()
	_, err := manager.Overview(context.Background(), &OverviewQuery{BaseQuery: BaseQuery{Start: start, End: end}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dao boom")
}

func TestReportManager_TimeSeries_Bucket(t *testing.T) {
	cases := []struct {
		name   string
		window time.Duration
		expect int
	}{
		{"one hour", time.Hour, 60},
		{"six hours", 6 * time.Hour, 60},
		{"six hours and one second", 6*time.Hour + time.Second, 300},
		{"three days", 72 * time.Hour, 300},
		{"three days and one second", 72*time.Hour + time.Second, 1800},
		{"seven days", 7 * 24 * time.Hour, 1800},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBucket int
			storager := &fakeReportStorager{
				timeSeriesFn: func(ctx context.Context, metric, dimension string, f *Filter, bucketSec int) ([]*MetricPoint, error) {
					gotBucket = bucketSec
					return []*MetricPoint{{Time: 1782345600}}, nil
				},
			}
			manager := NewReportManager(storager)

			start := time.Unix(1782345600, 0)
			points, err := manager.TimeSeries(context.Background(), &TimeSeriesQuery{
				BaseQuery: BaseQuery{Start: start, End: start.Add(tc.window)},
				Metric:    MetricQPS,
			})
			require.NoError(t, err)
			require.Len(t, points, 1)
			assert.Equal(t, tc.expect, gotBucket)
		})
	}
}

func TestReportManager_TimeSeries_InvalidMetric(t *testing.T) {
	manager := NewReportManager(&fakeReportStorager{})
	start, end := testWindow()

	_, err := manager.TimeSeries(context.Background(), &TimeSeriesQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Metric:    "not-a-metric",
	})
	require.Error(t, err)

	_, err = manager.TimeSeries(context.Background(), &TimeSeriesQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
	})
	require.Error(t, err)
}

func TestReportManager_Rankings_Limit(t *testing.T) {
	cases := []struct {
		name   string
		limit  int
		expect int
	}{
		{"default", 0, DefaultRankLimit},
		{"negative", -3, DefaultRankLimit},
		{"normal", 25, 25},
		{"over cap", 100, MaxRankLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotLimit int
			storager := &fakeReportStorager{
				rankingsFn: func(ctx context.Context, dimension string, f *Filter, limit int) ([]*RankingItem, error) {
					gotLimit = limit
					return nil, nil
				},
			}
			manager := NewReportManager(storager)
			start, end := testWindow()

			_, err := manager.Rankings(context.Background(), &RankingsQuery{
				BaseQuery: BaseQuery{Start: start, End: end},
				Dimension: DimensionModel,
				Limit:     tc.limit,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.expect, gotLimit)
		})
	}
}

func TestReportManager_Rankings_InvalidDimension(t *testing.T) {
	manager := NewReportManager(&fakeReportStorager{})
	start, end := testWindow()

	_, err := manager.Rankings(context.Background(), &RankingsQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Dimension: "stream",
	})
	require.Error(t, err)

	_, err = manager.Distribution(context.Background(), &DistributionQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Dimension: DimensionModel,
	})
	require.Error(t, err)
}

func TestReportManager_Distribution(t *testing.T) {
	var gotDimension string
	storager := &fakeReportStorager{
		distributionFn: func(ctx context.Context, dimension string, f *Filter) ([]*DistItem, error) {
			gotDimension = dimension
			return []*DistItem{{Name: "200", RequestCount: 10, Ratio: 1}}, nil
		},
	}
	manager := NewReportManager(storager)
	start, end := testWindow()

	items, err := manager.Distribution(context.Background(), &DistributionQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Dimension: DimensionStatus,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, DimensionStatus, gotDimension)
}

func TestReportManager_Los_PageDefaults(t *testing.T) {
	cases := []struct {
		name       string
		page       int
		pageSize   int
		expectPage int
		expectSize int
	}{
		{"default", 0, 0, DefaultPage, DefaultPageSize},
		{"negative", -1, -2, DefaultPage, DefaultPageSize},
		{"normal", 3, 50, 3, 50},
		{"over cap", 1, 500, 1, MaxPageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotFilter *LogFilter
			storager := &fakeReportStorager{
				logsFn: func(ctx context.Context, f *LogFilter) (*LogQueryResult, error) {
					gotFilter = f
					return &LogQueryResult{}, nil
				},
			}
			manager := NewReportManager(storager)
			start, end := testWindow()

			_, err := manager.Logs(context.Background(), &LogsQuery{
				BaseQuery: BaseQuery{Start: start, End: end},
				Page:      tc.page,
				PageSize:  tc.pageSize,
			})
			require.NoError(t, err)
			require.NotNil(t, gotFilter)
			assert.Equal(t, tc.expectPage, gotFilter.Page)
			assert.Equal(t, tc.expectSize, gotFilter.PageSize)
		})
	}
}

func TestReportManager_Logs_Filters(t *testing.T) {
	var gotFilter *LogFilter
	storager := &fakeReportStorager{
		logsFn: func(ctx context.Context, f *LogFilter) (*LogQueryResult, error) {
			gotFilter = f
			return &LogQueryResult{}, nil
		},
	}
	manager := NewReportManager(storager)
	start, end := testWindow()

	_, err := manager.Logs(context.Background(), &LogsQuery{
		BaseQuery:       BaseQuery{Start: start, End: end},
		RequestedModels: []string{"gpt-4"},
		ErrOnly:         true,
		Keyword:         "timeout",
	})
	require.NoError(t, err)
	require.NotNil(t, gotFilter)
	assert.Equal(t, []string{"gpt-4"}, gotFilter.RequestedModels)
	assert.True(t, gotFilter.ErrOnly)
	assert.Equal(t, "timeout", gotFilter.Keyword)
	assert.Equal(t, start, gotFilter.Start)
	assert.Equal(t, end, gotFilter.End)
}

// TestReportManager_Logs_CacheMirrorIntentFilters 验证五个新过滤参数透传到 LogFilter。
func TestReportManager_Logs_CacheMirrorIntentFilters(t *testing.T) {
	var gotFilter *LogFilter
	storager := &fakeReportStorager{
		logsFn: func(ctx context.Context, f *LogFilter) (*LogQueryResult, error) {
			gotFilter = f
			return &LogQueryResult{}, nil
		},
	}
	manager := NewReportManager(storager)
	start, end := testWindow()

	cacheStatus := "hit"
	mirrorHit := true
	intentQuestion := "task_type"
	intentAnswer := "unknown"
	intentSource := "classifier"
	_, err := manager.Logs(context.Background(), &LogsQuery{
		BaseQuery:      BaseQuery{Start: start, End: end},
		CacheStatus:    &cacheStatus,
		MirrorHit:      &mirrorHit,
		IntentQuestion: &intentQuestion,
		IntentAnswer:   &intentAnswer,
		IntentSource:   &intentSource,
	})
	require.NoError(t, err)
	require.NotNil(t, gotFilter)
	require.NotNil(t, gotFilter.CacheStatus)
	assert.Equal(t, "hit", *gotFilter.CacheStatus)
	require.NotNil(t, gotFilter.MirrorHit)
	assert.True(t, *gotFilter.MirrorHit)
	require.NotNil(t, gotFilter.IntentQuestion)
	assert.Equal(t, "task_type", *gotFilter.IntentQuestion)
	require.NotNil(t, gotFilter.IntentAnswer)
	assert.Equal(t, "unknown", *gotFilter.IntentAnswer)
	require.NotNil(t, gotFilter.IntentSource)
	assert.Equal(t, "classifier", *gotFilter.IntentSource)

	// 缺省时保持 nil（不过滤）。
	var emptyFilter *LogFilter
	storager.logsFn = func(ctx context.Context, f *LogFilter) (*LogQueryResult, error) {
		emptyFilter = f
		return &LogQueryResult{}, nil
	}
	_, err = manager.Logs(context.Background(), &LogsQuery{BaseQuery: BaseQuery{Start: start, End: end}})
	require.NoError(t, err)
	require.NotNil(t, emptyFilter)
	assert.Nil(t, emptyFilter.CacheStatus)
	assert.Nil(t, emptyFilter.MirrorHit)
	assert.Nil(t, emptyFilter.IntentQuestion)
	assert.Nil(t, emptyFilter.IntentAnswer)
	assert.Nil(t, emptyFilter.IntentSource)
}

func TestReportManager_Logs_KeywordTooLong(t *testing.T) {
	manager := NewReportManager(&fakeReportStorager{})
	start, end := testWindow()

	keyword := make([]byte, MaxKeywordLength+1)
	for i := range keyword {
		keyword[i] = 'a'
	}
	_, err := manager.Logs(context.Background(), &LogsQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Keyword:   string(keyword),
	})
	require.Error(t, err)
}

func TestReportManager_WindowValidation(t *testing.T) {
	cases := []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{"end before start", time.Unix(1782349200, 0), time.Unix(1782345600, 0)},
		{"end equals start", time.Unix(1782345600, 0), time.Unix(1782345600, 0)},
		{"window over 7 days", time.Unix(1782345600, 0), time.Unix(1782345600, 0).Add(7*24*time.Hour + time.Second)},
		{"zero start", time.Time{}, time.Unix(1782345600, 0)},
		{"zero end", time.Unix(1782345600, 0), time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewReportManager(&fakeReportStorager{})
			query := &OverviewQuery{BaseQuery: BaseQuery{Start: tc.start, End: tc.end}}
			_, err := manager.Overview(context.Background(), query)
			require.Error(t, err)
		})
	}
}

func TestReportManager_NilQuery(t *testing.T) {
	manager := NewReportManager(&fakeReportStorager{})
	ctx := context.Background()

	_, err := manager.Overview(ctx, nil)
	require.Error(t, err)
	_, err = manager.TimeSeries(ctx, nil)
	require.Error(t, err)
	_, err = manager.Rankings(ctx, nil)
	require.Error(t, err)
	_, err = manager.Distribution(ctx, nil)
	require.Error(t, err)
	_, err = manager.Logs(ctx, nil)
	require.Error(t, err)
}

func TestBucketSeconds(t *testing.T) {
	assert.Equal(t, 60, BucketSeconds(0))
	assert.Equal(t, 60, BucketSeconds(6*time.Hour))
	assert.Equal(t, 300, BucketSeconds(6*time.Hour+time.Nanosecond))
	assert.Equal(t, 300, BucketSeconds(72*time.Hour))
	assert.Equal(t, 1800, BucketSeconds(72*time.Hour+time.Nanosecond))
	assert.Equal(t, 1800, BucketSeconds(7*24*time.Hour))
}

func TestDimensionColumns(t *testing.T) {
	// Every accepted dimension must map to a column, and the mapping must be
	// stable for both backends.
	for dimension := range RankingDimensions {
		assert.NotEmpty(t, DimensionColumns[dimension], dimension)
	}
	for dimension := range DistributionDimensions {
		assert.NotEmpty(t, DimensionColumns[dimension], dimension)
	}
	for dimension := range TimeSeriesDimensions {
		assert.NotEmpty(t, DimensionColumns[dimension], dimension)
	}
}

// TestReportManager_DimensionCapabilityGate 验证后端能力门控：维度白名单
// 校验通过但后端不支持时返回 422，错误信息指明后端名（xerror 约定）。
// 二期起真实 dorisreport 声明全量维度，门控仅作为未来维度的纵深防御，
// 这里用 fake caps（不含新维度）锁定机制本身。
func TestReportManager_DimensionCapabilityGate(t *testing.T) {
	start, end := testWindow()
	newDims := []string{DimensionCacheStatus, DimensionMirrorHit, DimensionIntentAnswer}

	t.Run("rankings_doris_422", func(t *testing.T) {
		manager := NewReportManager(&fakeReportStorager{caps: dorisLikeCaps()})
		for _, dim := range newDims {
			_, err := manager.Rankings(context.Background(), &RankingsQuery{
				BaseQuery: BaseQuery{Start: start, End: end},
				Dimension: dim,
			})
			require.Error(t, err, dim)
			assert.Contains(t, err.Error(), "not supported by doris backend")
		}
	})

	t.Run("distribution_doris_422", func(t *testing.T) {
		manager := NewReportManager(&fakeReportStorager{caps: dorisLikeCaps()})
		for _, dim := range newDims {
			_, err := manager.Distribution(context.Background(), &DistributionQuery{
				BaseQuery: BaseQuery{Start: start, End: end},
				Dimension: dim,
			})
			require.Error(t, err, dim)
			assert.Contains(t, err.Error(), "not supported by doris backend")
		}
	})

	t.Run("timeseries_doris_422", func(t *testing.T) {
		manager := NewReportManager(&fakeReportStorager{caps: dorisLikeCaps()})
		for _, dim := range newDims {
			_, err := manager.TimeSeries(context.Background(), &TimeSeriesQuery{
				BaseQuery: BaseQuery{Start: start, End: end},
				Metric:    MetricQPS,
				Dimension: dim,
			})
			require.Error(t, err, dim)
			assert.Contains(t, err.Error(), "not supported by doris backend")
		}
	})

	t.Run("legacy_dimensions_still_supported_on_doris", func(t *testing.T) {
		var gotDimension string
		storager := &fakeReportStorager{
			caps: dorisLikeCaps(),
			rankingsFn: func(ctx context.Context, dimension string, f *Filter, limit int) ([]*RankingItem, error) {
				gotDimension = dimension
				return nil, nil
			},
		}
		manager := NewReportManager(storager)
		_, err := manager.Rankings(context.Background(), &RankingsQuery{
			BaseQuery: BaseQuery{Start: start, End: end},
			Dimension: DimensionModel,
		})
		require.NoError(t, err)
		assert.Equal(t, DimensionModel, gotDimension)
	})

	t.Run("mysql_supports_new_dimensions", func(t *testing.T) {
		var gotDimension string
		storager := &fakeReportStorager{
			caps: mysqlLikeCaps(),
			rankingsFn: func(ctx context.Context, dimension string, f *Filter, limit int) ([]*RankingItem, error) {
				gotDimension = dimension
				return nil, nil
			},
		}
		manager := NewReportManager(storager)
		for _, dim := range newDims {
			_, err := manager.Rankings(context.Background(), &RankingsQuery{
				BaseQuery: BaseQuery{Start: start, End: end},
				Dimension: dim,
			})
			require.NoError(t, err, dim)
			assert.Equal(t, dim, gotDimension)
		}
	})
}

// TestReportManager_TimeSeries_Dimension 验证 timeseries 的 dimension 参数：
// 仅接受三个新维度；白名单外 400（invalid dimension），doris 422。
func TestReportManager_TimeSeries_Dimension(t *testing.T) {
	start, end := testWindow()

	var gotDimension string
	storager := &fakeReportStorager{
		caps: mysqlLikeCaps(),
		timeSeriesFn: func(ctx context.Context, metric, dimension string, f *Filter, bucketSec int) ([]*MetricPoint, error) {
			gotDimension = dimension
			return nil, nil
		},
	}
	manager := NewReportManager(storager)

	_, err := manager.TimeSeries(context.Background(), &TimeSeriesQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Metric:    MetricQPS,
		Dimension: DimensionCacheStatus,
	})
	require.NoError(t, err)
	assert.Equal(t, DimensionCacheStatus, gotDimension)

	// 缺省 dimension 保持空串（行为不变）。
	_, err = manager.TimeSeries(context.Background(), &TimeSeriesQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Metric:    MetricQPS,
	})
	require.NoError(t, err)
	assert.Equal(t, "", gotDimension)

	// 既有维度（如 model）不在 timeseries 维度白名单内。
	_, err = manager.TimeSeries(context.Background(), &TimeSeriesQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Metric:    MetricQPS,
		Dimension: DimensionModel,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid dimension")
}

// TestReportManager_CacheTokensMetric 验证 cache_tokens 指标进入时序白名单。
func TestReportManager_CacheTokensMetric(t *testing.T) {
	var gotMetric string
	storager := &fakeReportStorager{
		caps: mysqlLikeCaps(),
		timeSeriesFn: func(ctx context.Context, metric, dimension string, f *Filter, bucketSec int) ([]*MetricPoint, error) {
			gotMetric = metric
			return nil, nil
		},
	}
	manager := NewReportManager(storager)
	start, end := testWindow()

	_, err := manager.TimeSeries(context.Background(), &TimeSeriesQuery{
		BaseQuery: BaseQuery{Start: start, End: end},
		Metric:    MetricCacheTokens,
	})
	require.NoError(t, err)
	assert.Equal(t, MetricCacheTokens, gotMetric)
}
