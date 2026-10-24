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

package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 本用例组在真实 MySQL 8.x 上校验 /report/* 三个暴露成本字段的端点
// （overview / timeseries?metric=cost / logs）的返回形状与金额口径：
// 种子中的定点成本刻意取非整数金额（RMB 66900 → 0.000669 元，USD 15230000 →
// 0.1523 美元），形状校验（value 为 number）叠加 0<value<1 语义断言直接锁死
// "忘记 ÷1e8" 的口径回归（ai-gateway-web #115 同类缺陷）。
//
// 数据源：环境变量 REPORT_MYSQL_DSN（格式 user:pass@tcp(host:port)/）。
// 未设置时全部用例 Skip；装配逻辑与 tests/report/query 共用 testutil.StartReportServer。

const reportPrefix = "/open-api/v1/report"

// 聚合表种子：窗口 [2026-09-15 09:59, 10:05)（≤6h → 60s 桶），两个币种各一行，
// 定点成本 RMB=66900（明细 2001）、USD=15230000（明细 2002）；
// cache token 计量：read=6000（行1）、write=700（行1），行2 全 0。
const aggregateSeedSQL = `INSERT INTO bfe_ai_metrics_1m
(ts_min, ai_apikey_id, ai_requested_model, ai_target_model, ai_stream,
 res_status_code, err_code, ai_provider, ai_protocol, ai_cost_currency,
 request_count, error_count, input_tokens, output_tokens, total_tokens,
 ttft_us_sum, tpot_us_sum, all_time_sum, rate_limit_hits, auth_reject_count,
 ai_cost_value_sum, cache_read_tokens, cache_write_tokens)
VALUES
('2026-09-15 10:00:00', 'key-1', 'gpt-4', 'gpt-4o', 1, 200, '', 'openai', 'openai', 'RMB', 2, 0, 100, 20, 120, 1000, 100, 200, 0, 0, 66900, 6000, 700),
('2026-09-15 10:00:00', 'key-2', 'gpt-4', 'gpt-4', 0, 200, '', 'azure', 'openai', 'USD', 1, 0, 50, 10, 60, 0, 0, 100, 0, 0, 15230000, 0, 0)`

// 明细表种子 3 行（log_time 倒序 2003..2001）：2003 为零成本且 currency 为 NULL
// 的未认证行，锁定 ai_cost_value=0 / ai_cost_currency=null 的序列化行为；
// 缓存/镜像/意图 10 列：2001 全量字段（classifier 源）、2002 cache 源、
// 2003 全默认（未启用缓存/未求值意图）。
const detailSeedSQL = `INSERT INTO bfe_ai_request_log
(logid, log_time, hostid, product, ai_apikey_id, ai_requested_model,
 ai_target_model, ai_provider, ai_protocol, ai_mode, ai_stream,
 res_status_code, err_code, err_msg,
 ai_input_tokens, ai_output_tokens, ai_total_tokens, all_time,
 ai_ttft_us, ai_tpot_us, ai_cost_value, ai_cost_currency,
 ai_rate_limit_hits, ai_auth_reject_quota_plans,
 level1Name, level1, client_ip, header_host, origin_uri, req_headers, res_headers,
 ai_cache_status, mirror_hit, mirror_cluster,
 ai_intent_question, ai_intent_answer, ai_intent_confidence, ai_intent_source,
 ai_intent_latency_us, ai_intent_cache_hit, ai_intent_questions_version)
VALUES
(2001, '2026-09-15 10:00:10', 'gw-01', 'BFE', 'key-1', 'gpt-4', 'gpt-4o', 'openai', 'openai', 'chat', 1, 200, NULL, NULL, 100, 20, 120, 100, 500, 50, 66900, 'RMB', NULL, NULL, NULL, NULL, '10.0.0.1', 'api.example.org', '/v1/chat', NULL, NULL, 'hit', 1, 'mirror-bj', 'task_type', 'coding', 0.95, 'classifier', 1200, 0, 'v3'),
(2002, '2026-09-15 10:00:20', 'gw-01', 'BFE', 'key-2', 'gpt-4', 'gpt-4', 'azure', 'openai', 'chat', 0, 200, NULL, NULL, 50, 10, 60, 100, NULL, NULL, 15230000, 'USD', NULL, NULL, NULL, NULL, '10.0.0.2', 'api.example.org', '/v1/chat', NULL, NULL, 'miss', 0, '', 'task_type', 'coding', 0.88, 'cache', NULL, 1, 'v3'),
(2003, '2026-09-15 10:00:30', 'gw-01', 'BFE', NULL, 'gpt-4', 'gpt-4o', 'openai', 'openai', 'chat', 0, 401, 'E401', 'invalid api key', 10, 2, 12, 20, NULL, NULL, 0, NULL, NULL, '["plan-a"]', NULL, NULL, '10.0.0.3', 'api.example.org', '/v1/chat', NULL, NULL, '', 0, '', '', '', NULL, '', NULL, NULL, '')`

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	rs, err := testutil.StartReportServer(aggregateSeedSQL, detailSeedSQL)
	if errors.Is(err, testutil.ErrReportMySQLDSNNotSet) {
		fmt.Println("REPORT_MYSQL_DSN not set; skipping report schema integration tests.")
		fmt.Println(`To enable: REPORT_MYSQL_DSN="root:****@tcp(127.0.0.1:3306)/" go test ./tests/schema/report/`)
		os.Exit(0)
	}
	if err != nil {
		fmt.Println("setup report integration environment failed:", err)
		os.Exit(1)
	}
	sm = rs.Server

	code := m.Run()
	rs.Close()
	os.Exit(code)
}

func TestReportSchema(t *testing.T) {
	t.Run("overview", testOverviewSchema)
	t.Run("timeseries_cost", testTimeSeriesCostSchema)
	t.Run("timeseries_cache_tokens", testTimeSeriesCacheTokensSchema)
	t.Run("logs", testLogsSchema)
}

func getData(t *testing.T, path string, query map[string]string, out interface{}) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Get(reportPrefix+path, query)
	require.NoError(t, err)
	require.NotNil(t, resp)
	testutil.AssertSuccess(t, resp)
	if out != nil {
		require.NoError(t, json.Unmarshal(resp.Data, out), "data: %s", string(resp.Data))
	}
	return resp
}

func windowQuery() map[string]string {
	return map[string]string{
		"start": fmt.Sprintf("%d", epoch("2026-09-15 09:59:00")),
		"end":   fmt.Sprintf("%d", epoch("2026-09-15 10:05:00")),
	}
}

// epoch 把 UTC 民用时刻转为 Unix 秒（请求参数；驱动按 UTC 格式化回 DATETIME）。
func epoch(datetime string) int64 {
	tt, err := time.ParseInLocation("2006-01-02 15:04:05", datetime, time.UTC)
	if err != nil {
		panic(err)
	}
	return tt.Unix()
}

// ---------------------------------------------------------------------------
// 响应数据结构（与 model/ireport 的 JSON 形状一致，仅本用例组断言涉及的字段）
// ---------------------------------------------------------------------------

type overviewData struct {
	RequestTotal int64 `json:"request_total"`
	TotalTokens  int64 `json:"total_tokens"`
	LogsTotal    int64 `json:"logs_total"`
	Cost         []struct {
		Currency string  `json:"currency"`
		Value    float64 `json:"value"`
	} `json:"cost"`
	Cache struct {
		HitCount    int64   `json:"hit_count"`
		MissCount   int64   `json:"miss_count"`
		SkipCount   int64   `json:"skip_count"`
		HitRate     float64 `json:"hit_rate"`
		ReadTokens  int64   `json:"read_tokens"`
		WriteTokens int64   `json:"write_tokens"`
	} `json:"cache"`
	Mirror struct {
		HitCount int64 `json:"hit_count"`
	} `json:"mirror"`
	Intent struct {
		ClassifiedCount int64   `json:"classified_count"`
		UnknownCount    int64   `json:"unknown_count"`
		UnknownRate     float64 `json:"unknown_rate"`
	} `json:"intent"`
}

type timeseriesCostData struct {
	BucketSec int `json:"bucket_sec"`
	Series    []struct {
		Time     int64    `json:"time"`
		Value    *float64 `json:"value"`
		Currency string   `json:"currency"`
	} `json:"series"`
}

type timeseriesCacheTokensData struct {
	BucketSec int `json:"bucket_sec"`
	Series    []struct {
		Time  int64    `json:"time"`
		Value *float64 `json:"value"`
		Kind  string   `json:"kind"`
	} `json:"series"`
}

type logItem struct {
	LogID        *int64   `json:"logid"`
	CostValue    *float64 `json:"ai_cost_value"`
	CostCurrency *string  `json:"ai_cost_currency"`

	AICacheStatus      *string  `json:"ai_cache_status"`
	MirrorHit          *bool    `json:"mirror_hit"`
	MirrorCluster      *string  `json:"mirror_cluster"`
	AIIntentQuestion   *string  `json:"ai_intent_question"`
	AIIntentAnswer     *string  `json:"ai_intent_answer"`
	AIIntentConfidence *float64 `json:"ai_intent_confidence"`
	AIIntentSource     *string  `json:"ai_intent_source"`
	AIIntentLatencyUs  *int64   `json:"ai_intent_latency_us"`
	AIIntentCacheHit   *bool    `json:"ai_intent_cache_hit"`
}

type logsData struct {
	Total    int64     `json:"total"`
	Items    []logItem `json:"items"`
}

// testOverviewSchema 校验 overview 形状（cost[].value 为 number）与金额口径：
// 定点种子 RMB=66900 / USD=15230000，接口出口 ÷1e8 后为 0.000669 / 0.1523；
// 缓存/镜像/意图组锁定明细 count 口径与聚合表 cache token SUM。
func testOverviewSchema(t *testing.T) {
	var data overviewData
	resp := getData(t, "/overview", windowQuery(), &data)
	testutil.AssertSchema(t, resp, OverviewResultSchema)

	assert.Equal(t, int64(3), data.RequestTotal)
	assert.Equal(t, int64(3), data.LogsTotal)

	require.Len(t, data.Cost, 2)
	byCurrency := map[string]float64{}
	for _, one := range data.Cost {
		byCurrency[one.Currency] = one.Value
	}
	assert.InDelta(t, 0.000669, byCurrency["RMB"], 1e-12)
	assert.InDelta(t, 0.1523, byCurrency["USD"], 1e-12)
	// 口径回归锁：0<value<1（旧定点口径下同种子必为 ≥1 的整数形态）。
	for currency, value := range byCurrency {
		assert.Greater(t, value, 0.0, "currency %s", currency)
		assert.Less(t, value, 1.0, "currency %s", currency)
	}

	// 明细 2001=hit、2002=miss、2003=''（未启用）：hit_rate=hit/(hit+miss)=0.5。
	assert.Equal(t, int64(1), data.Cache.HitCount)
	assert.Equal(t, int64(1), data.Cache.MissCount)
	assert.Equal(t, int64(0), data.Cache.SkipCount)
	assert.InDelta(t, 0.5, data.Cache.HitRate, 1e-9)
	// 聚合表 SUM（种子行1：read=6000、write=700；行2 全 0）。
	assert.Equal(t, int64(6000), data.Cache.ReadTokens)
	assert.Equal(t, int64(700), data.Cache.WriteTokens)
	assert.Equal(t, int64(1), data.Mirror.HitCount) // 2001
	// 意图：2001/2002 均 coding（classifier/cache 源）→ classified=2、unknown=0。
	assert.Equal(t, int64(2), data.Intent.ClassifiedCount)
	assert.Equal(t, int64(0), data.Intent.UnknownCount)
	assert.Equal(t, float64(0), data.Intent.UnknownRate)
}

// testTimeSeriesCostSchema 校验成本时序形状（金额/秒，按 currency 分序列）与口径。
func testTimeSeriesCostSchema(t *testing.T) {
	query := windowQuery()
	query["metric"] = "cost"

	var data timeseriesCostData
	resp := getData(t, "/timeseries", query, &data)
	testutil.AssertSchema(t, resp, TimeSeriesCostDataSchema)

	assert.Equal(t, 60, data.BucketSec)
	require.Len(t, data.Series, 2)

	byCurrency := map[string]float64{}
	for _, one := range data.Series {
		require.NotNil(t, one.Value)
		assert.Equal(t, epoch("2026-09-15 10:00:00"), one.Time)
		byCurrency[one.Currency] = *one.Value
	}
	// 金额/秒 = 定点值 / 60s / 1e8。
	assert.InDelta(t, 66900.0/60/1e8, byCurrency["RMB"], 1e-15)
	assert.InDelta(t, 15230000.0/60/1e8, byCurrency["USD"], 1e-15)
}

// testTimeSeriesCacheTokensSchema 校验 cache_tokens 时序形状（kind 区分
// cache_read/cache_write）与口径：原始 token 计数 ÷ 60s（不做 ÷1e8）。
func testTimeSeriesCacheTokensSchema(t *testing.T) {
	query := windowQuery()
	query["metric"] = "cache_tokens"

	var data timeseriesCacheTokensData
	resp := getData(t, "/timeseries", query, &data)
	testutil.AssertSchema(t, resp, TimeSeriesCacheTokensDataSchema)

	assert.Equal(t, 60, data.BucketSec)
	require.Len(t, data.Series, 2)

	byKind := map[string]float64{}
	for _, one := range data.Series {
		require.NotNil(t, one.Value)
		assert.Equal(t, epoch("2026-09-15 10:00:00"), one.Time)
		byKind[one.Kind] = *one.Value
	}
	// 种子行1：read=6000、write=700；行2 全 0。
	assert.InDelta(t, 6000.0/60, byKind["cache_read"], 1e-9)
	assert.InDelta(t, 700.0/60, byKind["cache_write"], 1e-9)
}

// testLogsSchema 校验日志明细形状（ai_cost_value 为 number 且允许 null）与口径：
// 定点成本换算为金额，零成本且 currency 为 NULL 的行返回 0/null；
// 缓存/镜像/意图新列锁定 2001 全量取值与 2003 全默认/NULL 语义。
func testLogsSchema(t *testing.T) {
	var data logsData
	resp := getData(t, "/logs", windowQuery(), &data)
	testutil.AssertSchema(t, resp, LogQueryResultSchema)

	assert.Equal(t, int64(3), data.Total)
	require.Len(t, data.Items, 3)
	byID := map[int64]logItem{}
	for _, one := range data.Items {
		byID[*one.LogID] = one
	}

	withCost := byID[2001]
	require.NotNil(t, withCost.CostValue)
	assert.InDelta(t, 0.000669, *withCost.CostValue, 1e-12)
	require.NotNil(t, withCost.CostCurrency)
	assert.Equal(t, "RMB", *withCost.CostCurrency)

	withCostUSD := byID[2002]
	require.NotNil(t, withCostUSD.CostValue)
	assert.InDelta(t, 0.1523, *withCostUSD.CostValue, 1e-12)
	require.NotNil(t, withCostUSD.CostCurrency)
	assert.Equal(t, "USD", *withCostUSD.CostCurrency)

	noCost := byID[2003]
	require.NotNil(t, noCost.CostValue)
	assert.Equal(t, 0.0, *noCost.CostValue)
	assert.Nil(t, noCost.CostCurrency)

	// 新列：2001 classifier 源全量字段。
	full := byID[2001]
	require.NotNil(t, full.AICacheStatus)
	assert.Equal(t, "hit", *full.AICacheStatus)
	require.NotNil(t, full.MirrorHit)
	assert.True(t, *full.MirrorHit)
	require.NotNil(t, full.MirrorCluster)
	assert.Equal(t, "mirror-bj", *full.MirrorCluster)
	require.NotNil(t, full.AIIntentQuestion)
	assert.Equal(t, "task_type", *full.AIIntentQuestion)
	require.NotNil(t, full.AIIntentAnswer)
	assert.Equal(t, "coding", *full.AIIntentAnswer)
	require.NotNil(t, full.AIIntentConfidence)
	assert.InDelta(t, 0.95, *full.AIIntentConfidence, 1e-12)
	require.NotNil(t, full.AIIntentSource)
	assert.Equal(t, "classifier", *full.AIIntentSource)
	require.NotNil(t, full.AIIntentLatencyUs)
	assert.Equal(t, int64(1200), *full.AIIntentLatencyUs)
	require.NotNil(t, full.AIIntentCacheHit)
	assert.False(t, *full.AIIntentCacheHit)

	// 2002 cache 源：latency_us 为 NULL -> nil；cache_hit=true。
	fromCache := byID[2002]
	require.NotNil(t, fromCache.AIIntentSource)
	assert.Equal(t, "cache", *fromCache.AIIntentSource)
	assert.Nil(t, fromCache.AIIntentLatencyUs)
	require.NotNil(t, fromCache.AIIntentCacheHit)
	assert.True(t, *fromCache.AIIntentCacheHit)

	// 2003 全默认：缓存未启用/意图未求值，mirror_hit 为 false（非 NULL 列）。
	none := byID[2003]
	require.NotNil(t, none.AICacheStatus)
	assert.Equal(t, "", *none.AICacheStatus)
	require.NotNil(t, none.MirrorHit)
	assert.False(t, *none.MirrorHit)
	require.NotNil(t, none.MirrorCluster)
	assert.Equal(t, "", *none.MirrorCluster)
	require.NotNil(t, none.AIIntentAnswer)
	assert.Equal(t, "", *none.AIIntentAnswer)
	assert.Nil(t, none.AIIntentConfidence)
	assert.Nil(t, none.AIIntentLatencyUs)
	assert.Nil(t, none.AIIntentCacheHit)
}
