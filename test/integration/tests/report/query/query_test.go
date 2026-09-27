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

package query_test

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
)

// 本用例组在真实 MySQL 8.x 上验证 /report/* 五个查询端点。
//
// 数据源：环境变量 REPORT_MYSQL_DSN（格式 user:pass@tcp(host:port)/，参照
// log-reader LR03 的 LR_MYSQL_DSN 约定）。未设置时全部用例 Skip。
// 测试自建专用随机名数据库 report_it_<ns>，套用项目 db_ddl_report_mysql.sql
// （${INIT_DATE} 替换为今天+3 天，历史时间戳落入 p_init 分区），灌确定性种子
// 数据后启动带 [Report] 装配的 api 进程，结束后 DROP DATABASE 清理。

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	rs, err := testutil.StartReportServer(aggregateSeedSQL, detailSeedSQL)
	if errors.Is(err, testutil.ErrReportMySQLDSNNotSet) {
		fmt.Println("REPORT_MYSQL_DSN not set; skipping report query integration tests.")
		fmt.Println(`To enable: REPORT_MYSQL_DSN="root:****@tcp(127.0.0.1:3306)/" go test ./tests/report/query/`)
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

func with(base map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(base)+len(kv)/2)
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

// ---------------------------------------------------------------------------
// 响应数据结构（与 model/ireport 的 JSON 形状一致）
// ---------------------------------------------------------------------------

type overviewData struct {
	RequestTotal int64   `json:"request_total"`
	ErrorTotal   int64   `json:"error_total"`
	ErrorRate    float64 `json:"error_rate"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`

	LatencyAvgMs float64  `json:"latency_avg_ms"`
	LatencyMaxMs float64  `json:"latency_max_ms"`
	LatencyP50Ms *float64 `json:"latency_p50_ms"`

	TtftAvgMs float64 `json:"ttft_avg_ms"`
	TpotAvgMs float64 `json:"tpot_avg_ms"`

	Cost []struct {
		Currency string `json:"currency"`
		Value    float64 `json:"value"`
	} `json:"cost"`

	RateLimitHits int64 `json:"rate_limit_hits"`
	AuthRejects   int64 `json:"auth_rejects"`
	LogsTotal     int64 `json:"logs_total"`

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

type metricPoint struct {
	Time     int64    `json:"time"`
	Value    *float64 `json:"value"`
	Input    *float64 `json:"input"`
	Output   *float64 `json:"output"`
	Total    *float64 `json:"total"`
	Currency string   `json:"currency"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
}

type timeseriesData struct {
	BucketSec int           `json:"bucket_sec"`
	Series    []metricPoint `json:"series"`
}

type rankingItem struct {
	Name         string `json:"name"`
	RequestCount int64  `json:"request_count"`
	ErrorCount   int64  `json:"error_count"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

type rankingsData struct {
	Items []rankingItem `json:"items"`
}

type distItem struct {
	Name         string  `json:"name"`
	RequestCount int64   `json:"request_count"`
	Ratio        float64 `json:"ratio"`
}

type distributionData struct {
	Items []distItem `json:"items"`
}

type logItem struct {
	LogID          *int64  `json:"logid"`
	LogTime        int64   `json:"log_time"`
	Hostid         *string `json:"hostid"`
	Product        *string `json:"product"`
	APIKeyID       *string `json:"ai_apikey_id"`
	RequestedModel *string `json:"ai_requested_model"`
	TargetModel    *string `json:"ai_target_model"`
	Provider       *string `json:"ai_provider"`
	Protocol       *string `json:"ai_protocol"`
	Mode           *string `json:"ai_mode"`
	Stream         *int16  `json:"ai_stream"`
	StatusCode     *int16  `json:"res_status_code"`
	ErrCode        *string `json:"err_code"`
	ErrMsg         *string `json:"err_msg"`

	CostValue    *float64 `json:"ai_cost_value"`
	CostCurrency *string  `json:"ai_cost_currency"`

	RateLimitHits        *string `json:"ai_rate_limit_hits"`
	AuthRejectQuotaPlans *string `json:"ai_auth_reject_quota_plans"`
	Level1Name           *string `json:"level1Name"`
	Level1               *string `json:"level1"`
	ClientIP             *string `json:"client_ip"`
	HeaderHost           *string `json:"header_host"`
	OriginURI            *string `json:"origin_uri"`
	ReqHeaders           *string `json:"req_headers"`
	ResHeaders           *string `json:"res_headers"`

	AICacheStatus        *string  `json:"ai_cache_status"`
	MirrorHit            *bool    `json:"mirror_hit"`
	MirrorCluster        *string  `json:"mirror_cluster"`
	AIIntentQuestion     *string  `json:"ai_intent_question"`
	AIIntentAnswer       *string  `json:"ai_intent_answer"`
	AIIntentConfidence   *float64 `json:"ai_intent_confidence"`
	AIIntentSource       *string  `json:"ai_intent_source"`
	AIIntentLatencyUs    *int64   `json:"ai_intent_latency_us"`
	AIIntentCacheHit     *bool    `json:"ai_intent_cache_hit"`
	AIIntentQuestionsVer *string  `json:"ai_intent_questions_version"`
}

type logsData struct {
	Total    int64     `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
	Items    []logItem `json:"items"`
}
