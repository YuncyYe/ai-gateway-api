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

package dorisreport

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ireport"
)

var (
	testStart = time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	testEnd   = time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC)
)

func fullFilter() *ireport.Filter {
	stream := true
	return &ireport.Filter{
		Start:       testStart,
		End:         testEnd,
		Models:      []string{"gpt-4o", "gpt-4"},
		ApikeyIDs:   []string{"key-1"},
		Providers:   []string{"openai"},
		Hosts:       []string{"gw-01"},
		Stream:      &stream,
		StatusCodes: []int{200, 500},
	}
}

func assertSnapshot(t *testing.T, query string, args []interface{}, err error,
	expectSQL string, expectArgs []interface{}) {
	t.Helper()
	require.NoError(t, err)
	assert.Equal(t, expectSQL, query)
	assert.Equal(t, expectArgs, args)
}

func fullFilterArgs() []interface{} {
	return []interface{}{
		int8(1), "key-1", "openai", "gpt-4o", "gpt-4", "gw-01", 200, 500,
		testStart, testEnd,
	}
}

func TestBuildOverviewMetricsSQL(t *testing.T) {
	query, args, err := buildOverviewMetricsSQL("bfe_ai_metrics_1m", fullFilter())

	require.NoError(t, err)
	assert.Contains(t, query, "IFNULL(MAX(all_time_sum/request_count),0) AS latency_max")
	assert.Equal(t, fullFilterArgs(), args)
}

func TestBuildOverviewCostSQL(t *testing.T) {
	query, args, err := buildOverviewCostSQL("bfe_ai_metrics_1m", fullFilter())

	require.NoError(t, err)
	// Doris filters the empty currency directly (no IFNULL wrapper).
	assert.Contains(t, query, "ai_cost_currency!=?")
	assert.Contains(t, query, " GROUP BY ai_cost_currency")
	assert.Equal(t, append(fullFilterArgs()[:8], append([]interface{}{""}, fullFilterArgs()[8:]...)...), args)
}

func TestBuildOverviewPercentileSQL(t *testing.T) {
	query, args, err := buildOverviewPercentileSQL("bfe_ai_request_log", fullFilter())

	require.NoError(t, err)
	assert.Equal(t, "SELECT PERCENTILE_APPROX(all_time, 0.5) AS p50,"+
		"PERCENTILE_APPROX(all_time, 0.9) AS p90,"+
		"PERCENTILE_APPROX(all_time, 0.99) AS p99"+
		" FROM bfe_ai_request_log"+
		" WHERE (ai_stream=? AND ai_apikey_id IN (?) AND ai_provider IN (?)"+
		" AND ai_target_model IN (?,?) AND hostid IN (?) AND res_status_code IN (?,?)"+
		" AND log_time>=? AND log_time<? AND all_time IS NOT NULL)", query)
	assert.Equal(t, fullFilterArgs(), args)
}

func TestBuildTimeSeriesSQL_Dialect(t *testing.T) {
	f := &ireport.Filter{Start: testStart, End: testEnd}

	query, args, err := buildTimeSeriesSQL("bfe_ai_metrics_1m", ireport.MetricQPS, "", f, 300)
	require.NoError(t, err)
	// Session-timezone neutral bucket: CAST(FLOOR(TIMESTAMPDIFF(SECOND, epoch, ts_min)/300)*300 AS BIGINT)
	assert.Equal(t, "SELECT CAST(FLOOR(TIMESTAMPDIFF(SECOND, '1970-01-01 00:00:00', ts_min)/300)*300 AS BIGINT) AS time,"+
		"SUM(request_count) AS total"+
		" FROM bfe_ai_metrics_1m WHERE (ts_min>=? AND ts_min<?) GROUP BY time ORDER BY time ASC", query)
	assert.Equal(t, []interface{}{testStart, testEnd}, args)

	query, _, err = buildLatencyPercentileSQL("bfe_ai_request_log", f, 60)
	require.NoError(t, err)
	assert.Equal(t, "SELECT CAST(FLOOR(TIMESTAMPDIFF(SECOND, '1970-01-01 00:00:00', log_time)/60)*60 AS BIGINT) AS time,"+
		"PERCENTILE_APPROX(all_time, 0.5) AS p50,"+
		"PERCENTILE_APPROX(all_time, 0.9) AS p90,"+
		"PERCENTILE_APPROX(all_time, 0.99) AS p99"+
		" FROM bfe_ai_request_log"+
		" WHERE (log_time>=? AND log_time<? AND all_time IS NOT NULL) GROUP BY time ORDER BY time ASC", query)
}

// TestBuildTimeSeriesSQL_CacheTokens 验证 Doris 侧 cache_tokens 的 UNION ALL
// 形态（一期实现范围：与 MySQL 同口径）。
func TestBuildTimeSeriesSQL_CacheTokens(t *testing.T) {
	f := &ireport.Filter{Start: testStart, End: testEnd}

	query, args, err := buildTimeSeriesSQL("bfe_ai_metrics_1m", ireport.MetricCacheTokens, "", f, 300)
	require.NoError(t, err)

	bucket := "CAST(FLOOR(TIMESTAMPDIFF(SECOND, '1970-01-01 00:00:00', ts_min)/300)*300 AS BIGINT) AS time"
	expect := "SELECT " + bucket + ",'cache_read' AS kind,SUM(cache_read_tokens) AS value" +
		" FROM bfe_ai_metrics_1m WHERE (ts_min>=? AND ts_min<?) GROUP BY time" +
		" UNION ALL " +
		"SELECT " + bucket + ",'cache_write' AS kind,SUM(cache_write_tokens) AS value" +
		" FROM bfe_ai_metrics_1m WHERE (ts_min>=? AND ts_min<?) GROUP BY time" +
		" ORDER BY time ASC,kind ASC"
	assert.Equal(t, expect, query)
	assert.Equal(t, []interface{}{testStart, testEnd, testStart, testEnd}, args)
}

// TestBuildTimeSeriesSQL_DimensionGated 验证 Doris 一期不实现维度分支：
// 任何非空 dimension 直接报错（manager 门控前的纵深防御）。
func TestBuildTimeSeriesSQL_DimensionGated(t *testing.T) {
	f := &ireport.Filter{Start: testStart, End: testEnd}

	for _, dim := range []string{ireport.DimensionCacheStatus, ireport.DimensionMirrorHit, ireport.DimensionIntentAnswer} {
		_, _, err := buildTimeSeriesSQL("bfe_ai_metrics_1m", ireport.MetricQPS, dim, f, 60)
		require.Error(t, err, dim)
		assert.Contains(t, err.Error(), "not supported by doris backend (mysql only until doris support lands)")
	}
}

// TestCapabilities 验证 Doris 一期能力声明：不声明三个新维度。
func TestCapabilities(t *testing.T) {
	caps := New(nil, "", "doris").Capabilities()
	require.NotNil(t, caps)
	assert.Equal(t, "doris", caps.Backend)
	assert.Len(t, caps.SupportedDimensions, 9)
	for _, dim := range []string{
		ireport.DimensionCacheStatus, ireport.DimensionMirrorHit, ireport.DimensionIntentAnswer,
	} {
		assert.NotContains(t, caps.SupportedDimensions, dim)
	}
}

func TestBuildRankingsSQL_Dialect(t *testing.T) {
	// string dimension: plain != '' predicate (Doris/Grafana style).
	query, args, err := buildRankingsSQL("bfe_ai_metrics_1m", ireport.DimensionProvider, &ireport.Filter{Start: testStart, End: testEnd}, 10)
	require.NoError(t, err)
	assert.Contains(t, query, "SELECT ai_provider AS name,")
	assert.Contains(t, query, "ai_provider!=?")
	assert.Equal(t, []interface{}{"", testStart, testEnd, 0, 10}, args)

	// status dimension: numeric empty marker 0.
	query, _, err = buildRankingsSQL("bfe_ai_metrics_1m", ireport.DimensionStatus, &ireport.Filter{Start: testStart, End: testEnd}, 10)
	require.NoError(t, err)
	assert.Contains(t, query, "SELECT CAST(res_status_code AS CHAR) AS name,")
	assert.Contains(t, query, "res_status_code!=?")

	_, _, err = buildRankingsSQL("bfe_ai_metrics_1m", "bogus", &ireport.Filter{Start: testStart, End: testEnd}, 10)
	require.Error(t, err)
}

func TestBuildDistributionSQL_Dialect(t *testing.T) {
	// nullable status column: NULL normalizes to unknown as well.
	query, _, err := buildDistributionSQL("bfe_ai_metrics_1m", ireport.DimensionStatus, &ireport.Filter{Start: testStart, End: testEnd})
	require.NoError(t, err)
	assert.Contains(t, query, "CASE WHEN IFNULL(res_status_code,0)=0 THEN 'unknown' ELSE CAST(res_status_code AS CHAR) END AS name")

	query, _, err = buildDistributionSQL("bfe_ai_metrics_1m", ireport.DimensionMode, &ireport.Filter{Start: testStart, End: testEnd})
	require.NoError(t, err)
	assert.Contains(t, query, "CASE WHEN IFNULL(ai_mode,'')='' THEN 'unknown' ELSE ai_mode END AS name")

	_, _, err = buildDistributionSQL("bfe_ai_metrics_1m", "model", &ireport.Filter{Start: testStart, End: testEnd})
	require.Error(t, err)
}

func TestBuildLogsSQL(t *testing.T) {
	f := &ireport.LogFilter{
		Filter:   *fullFilter(),
		ErrOnly:  true,
		Keyword:  "timeout",
		Page:     2,
		PageSize: 20,
	}
	query, args, err := buildLogsSQL("bfe_ai_request_log", f)
	require.NoError(t, err)
	assert.Contains(t, query, " ORDER BY log_time DESC LIMIT ?,?")
	assert.Contains(t, query, "err_msg LIKE ?")
	assert.Equal(t, []interface{}{int8(1), "key-1", "openai", "gpt-4o", "gpt-4", "gw-01", 200, 500, "", testStart, testEnd, "%timeout%", 20, 20}, args)

	countSQL, countArgs, err := buildLogsCountSQL("bfe_ai_request_log", f)
	require.NoError(t, err)
	assert.Equal(t, "SELECT COUNT(*) FROM bfe_ai_request_log", countSQL[:strings.Index(countSQL, " WHERE")])
	assert.Contains(t, countSQL, "IFNULL(err_code,'')!=?")
	assert.Equal(t, args[:len(args)-2], countArgs)
}

// TestBuildLogsSQL_CacheMirrorIntentFilters 验证 Doris 侧五个新过滤参数
// 渲染（一期实现范围，与 MySQL 同口径）。
func TestBuildLogsSQL_CacheMirrorIntentFilters(t *testing.T) {
	cacheStatus := "hit"
	mirrorHit := true
	intentAnswer := "unknown"
	f := &ireport.LogFilter{
		Filter:      ireport.Filter{Start: testStart, End: testEnd},
		CacheStatus: &cacheStatus,
		MirrorHit:   &mirrorHit,
		IntentAnswer: &intentAnswer,
	}
	query, args, err := buildLogsCountSQL("bfe_ai_request_log", f)
	require.NoError(t, err)
	assert.Contains(t, query, "ai_cache_status=?")
	assert.Contains(t, query, "mirror_hit=?")
	assert.Contains(t, query, "ai_intent_answer=?")
	assert.Equal(t, []interface{}{"hit", "unknown", int8(1), testStart, testEnd}, args)
}

func TestTablePrefix(t *testing.T) {
	s := New(nil, "bfe_observability", "doris")
	assert.Equal(t, "bfe_observability.bfe_ai_request_log", s.table(tableDetail))

	s = New(nil, "", "doris")
	assert.Equal(t, "bfe_ai_metrics_1m", s.table(tableMetrics))
}

func TestBuildOverviewDetailCountsSQL(t *testing.T) {
	query, args, err := buildOverviewDetailCountsSQL("bfe_ai_request_log", fullFilter())

	require.NoError(t, err)
	assert.Contains(t, query, "SUM(CASE WHEN ai_cache_status='hit' THEN 1 ELSE 0 END)")
	assert.Contains(t, query, "SUM(CASE WHEN IFNULL(mirror_hit,0)=1 THEN 1 ELSE 0 END)")
	assert.Contains(t, query, "SUM(CASE WHEN ai_intent_answer='unknown' THEN 1 ELSE 0 END)")
	assert.Equal(t, fullFilterArgs(), args)
}

func TestRowToMetricPoint(t *testing.T) {
	qps := rowToMetricPoint(ireport.MetricQPS, 1000, 60, metricRowValues{total: 300})
	require.NotNil(t, qps.Value)
	assert.InDelta(t, 5, *qps.Value, 1e-9)

	latency := rowToMetricPoint(ireport.MetricLatency, 1000, 60, metricRowValues{allTimeSum: 900, requestCount: 3, latencyMax: 450})
	assert.InDelta(t, 300, *latency.Avg, 1e-9)
	assert.InDelta(t, 450, *latency.Max, 1e-9)

	ttft := rowToMetricPoint(ireport.MetricTTFT, 1000, 60, metricRowValues{ttftUsSum: 2_000_000, streamRequests: 4})
	assert.InDelta(t, 500, *ttft.Value, 1e-9)

	cost := rowToMetricPoint(ireport.MetricCost, 1000, 60, metricRowValues{value: 600_000_000, currency: "USD"})
	assert.InDelta(t, 0.1, *cost.Value, 1e-9) // 6e8 定点 / 60s / 1e8 = 0.1 元/秒
	assert.Equal(t, "USD", cost.Currency)

	cacheRead := rowToMetricPoint(ireport.MetricCacheTokens, 1000, 60, metricRowValues{value: 4000, kind: "cache_read"})
	require.NotNil(t, cacheRead.Value)
	assert.InDelta(t, 4000.0/60, *cacheRead.Value, 1e-9)
	assert.Equal(t, "cache_read", cacheRead.Kind)

	_, err := scanMetricPoint(nil, "bogus", "", 60)
	require.Error(t, err)
}

func TestOverviewResultFromRow(t *testing.T) {
	row := &overviewMetricsRow{
		requestTotal:     sql.NullInt64{Int64: 200, Valid: true},
		errorTotal:       sql.NullInt64{Int64: 10, Valid: true},
		allTimeSum:       sql.NullInt64{Int64: 400000, Valid: true},
		latencyMax:       sql.NullFloat64{Float64: 5000, Valid: true},
		ttftUsSum:        sql.NullInt64{Int64: 10_000_000, Valid: true},
		streamRequests:   sql.NullInt64{Int64: 40, Valid: true},
		tpotUsSum:        sql.NullInt64{Int64: 1_000_000, Valid: true},
		cacheReadTokens:  sql.NullInt64{Int64: 4500, Valid: true},
		cacheWriteTokens: sql.NullInt64{Int64: 700, Valid: true},
	}
	detailCounts := &overviewDetailCounts{
		cacheHitCount:         sql.NullInt64{Int64: 2, Valid: true},
		cacheMissCount:        sql.NullInt64{Int64: 2, Valid: true},
		cacheSkipCount:        sql.NullInt64{Int64: 1, Valid: true},
		mirrorHitCount:        sql.NullInt64{Int64: 2, Valid: true},
		intentClassifiedCount: sql.NullInt64{Int64: 3, Valid: true},
		intentUnknownCount:    sql.NullInt64{Int64: 1, Valid: true},
	}
	result := overviewResultFromRow(row, nil, 5, detailCounts)
	assert.Equal(t, int64(200), result.RequestTotal)
	assert.InDelta(t, 0.05, result.ErrorRate, 1e-9)
	assert.InDelta(t, 2000, result.LatencyAvgMs, 1e-9)
	assert.InDelta(t, 250, result.TtftAvgMs, 1e-9)
	assert.Nil(t, result.LatencyP50Ms)
	// 与 MySQL 后端同口径：hit_rate=hit/(hit+miss)，skip 不计分母。
	assert.InDelta(t, 0.5, result.Cache.HitRate, 1e-9)
	assert.Equal(t, int64(4500), result.Cache.ReadTokens)
	assert.Equal(t, int64(700), result.Cache.WriteTokens)
	assert.Equal(t, int64(2), result.Mirror.HitCount)
	assert.Equal(t, int64(3), result.Intent.ClassifiedCount)
	assert.Equal(t, int64(1), result.Intent.UnknownCount)
	assert.InDelta(t, 0.25, result.Intent.UnknownRate, 1e-9)
}

func TestDistributionRatios(t *testing.T) {
	items := []*ireport.DistItem{{Name: "chat", RequestCount: 90}, {Name: "unknown", RequestCount: 10}}
	distributionRatios(items)
	assert.InDelta(t, 0.9, items[0].Ratio, 1e-9)
	assert.InDelta(t, 0.1, items[1].Ratio, 1e-9)
}

func TestNullPtrHelpers(t *testing.T) {
	assert.Nil(t, nullInt64Ptr(sql.NullInt64{}))
	assert.Nil(t, nullInt16Ptr(sql.NullInt64{}))
	assert.Nil(t, nullStringPtr(sql.NullString{}))
	assert.Nil(t, nullCostAmountPtr(sql.NullInt64{}))
	assert.Nil(t, nullBoolPtr(sql.NullInt64{}))
	assert.Nil(t, nullFloat64Ptr(sql.NullFloat64{}))

	v := sql.NullInt64{Int64: 7, Valid: true}
	require.NotNil(t, nullInt64Ptr(v))
	assert.Equal(t, int64(7), *nullInt64Ptr(v))
	require.NotNil(t, nullInt16Ptr(v))
	assert.Equal(t, int16(7), *nullInt16Ptr(v))

	one := sql.NullInt64{Int64: 1, Valid: true}
	require.NotNil(t, nullBoolPtr(one))
	assert.True(t, *nullBoolPtr(one))

	f := sql.NullFloat64{Float64: 0.95, Valid: true}
	require.NotNil(t, nullFloat64Ptr(f))
	assert.InDelta(t, 0.95, *nullFloat64Ptr(f), 1e-12)

	c := sql.NullInt64{Int64: 66900, Valid: true}
	require.NotNil(t, nullCostAmountPtr(c))
	assert.InDelta(t, 0.000669, *nullCostAmountPtr(c), 1e-12)
}
