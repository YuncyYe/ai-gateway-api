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

// 本用例组锁定明细表幂等键的请求级判别能力（bfenetworks/bfe#1417）。
//
// 背景：`uk_dedup` 原为四列 (hostid, log_time, ai_apikey_id, ai_requested_model)，
// 而 `log_time` 是秒级 DATETIME、写入侧又是 `ON DUPLICATE KEY UPDATE` 全列覆盖——
// 同一秒内同 API Key、同模型的并发请求互相覆盖，n 个请求只留 1 行（生产实测丢率
// 恰为 (n-1)/n、窗口 13.5%），被覆盖行的 tokens/cost 从求和中整体消失。
// 修复：`logid` 入键（并收紧为 NOT NULL DEFAULT 0），键才真正标识一次请求。
//
// 三层断言：
//  1. detail_key_keeps_concurrent_rows —— 3 条"同秒 + 同 Key + 同模型、仅 logid
//     不同"的明细必须全部保留（/report/overview 的 logs_total、/report/logs 的
//     total/items），即缺陷的报表侧回归锁；
//  2. legacy_four_column_key_collapses —— 把同一批数据写进"旧四列键"的镜像表，
//     只留 1 行且幸存的是最后写入行，构成"唯一区别就是 logid"的因果对照；
//  3. replay_still_idempotent —— 同 logid 再写一次（值变更）仍 3 行且值被覆盖，
//     确认覆盖语义只作用于同一请求的重放，未把"丢行"换成"重复行"。
//
// 数据源：环境变量 REPORT_MYSQL_DSN（格式 user:pass@tcp(host:port)/）；
// 未设置时全部用例 Skip，装配逻辑与 tests/report/query、tests/schema/report
// 共用 testutil.StartReportServer。

package dedup_key

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

const reportPrefix = "/open-api/v1/report"

// detailSeedSQL 3 行共享 (hostid, log_time, ai_apikey_id, ai_requested_model)
// ——即旧四列键的完整键值，只有 logid 与 token/cost 不同：
// 这正是并发请求的判重前提，修复前它们会被折叠成 1 行。
//
// 写入方式与生产同构（多值 INSERT + 全列覆盖更新），因此修复前的表现是
// "只剩 1 行"（断言失败），而不是 SQL 报重复键——与线上事故形态一致。
const detailSeedSQL = `INSERT INTO bfe_ai_request_log
(logid, log_time, hostid, product, ai_apikey_id, ai_requested_model,
 ai_input_tokens, ai_output_tokens, ai_total_tokens,
 ai_cost_value, ai_cost_currency)
VALUES
(3001, '2026-09-15 10:00:00', 'gw-01', 'BFE', 'key-1', 'gpt-4', 10, 5, 15, 1500, 'RMB'),
(3002, '2026-09-15 10:00:00', 'gw-01', 'BFE', 'key-1', 'gpt-4', 20, 5, 25, 2500, 'RMB'),
(3003, '2026-09-15 10:00:00', 'gw-01', 'BFE', 'key-1', 'gpt-4', 30, 5, 35, 3500, 'RMB')
ON DUPLICATE KEY UPDATE
logid=VALUES(logid), product=VALUES(product),
ai_input_tokens=VALUES(ai_input_tokens), ai_output_tokens=VALUES(ai_output_tokens),
ai_total_tokens=VALUES(ai_total_tokens),
ai_cost_value=VALUES(ai_cost_value), ai_cost_currency=VALUES(ai_cost_currency)`

// aggregateSeedSQL 与上面 3 行明细一致的分钟桶（JOB 关闭时聚合表由测试预置）：
// request_count=3、total_tokens=15+25+35=75、ai_cost_value_sum=1500+2500+3500=7500。
// 聚合表本身没有唯一键、口径由 JOB 的"DELETE 窗口 + INSERT SELECT"保证；本用例
// 断言的是明细侧行数（聚合不低估的前提），故这里只做一致性预置。
const aggregateSeedSQL = `INSERT INTO bfe_ai_metrics_1m
(ts_min, ai_apikey_id, ai_requested_model, ai_cost_currency, request_count,
 input_tokens, output_tokens, total_tokens, ai_cost_value_sum)
VALUES
('2026-09-15 10:00:00', 'key-1', 'gpt-4', 'RMB', 3, 60, 15, 75, 7500)`

// legacyTableDDL 复刻修复前的四列唯一键，仅用于对照臂。
const legacyTableDDL = `CREATE TABLE bfe_ai_request_log_legacy_uk (
hostid VARCHAR(256) NOT NULL DEFAULT '',
log_time DATETIME NOT NULL,
ai_apikey_id VARCHAR(256) DEFAULT NULL,
ai_requested_model VARCHAR(128) DEFAULT NULL,
logid BIGINT NOT NULL DEFAULT 0,
ai_total_tokens BIGINT DEFAULT NULL,
UNIQUE KEY uk_dedup (hostid, log_time, ai_apikey_id, ai_requested_model)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

// legacyBatchSQL 与生产写入路径同构：多值 INSERT + 全列覆盖更新。
const legacyBatchSQL = `INSERT INTO bfe_ai_request_log_legacy_uk
(hostid, log_time, ai_apikey_id, ai_requested_model, logid, ai_total_tokens)
VALUES
('gw-01','2026-09-15 10:00:00','key-1','gpt-4',3001,15),
('gw-01','2026-09-15 10:00:00','key-1','gpt-4',3002,25),
('gw-01','2026-09-15 10:00:00','key-1','gpt-4',3003,35)
ON DUPLICATE KEY UPDATE logid=VALUES(logid), ai_total_tokens=VALUES(ai_total_tokens)`

// replayBatchSQL 对真实表重放同一批（同 logid、值变更）。
const replayBatchSQL = `INSERT INTO bfe_ai_request_log
(logid, log_time, hostid, product, ai_apikey_id, ai_requested_model,
 ai_total_tokens, ai_cost_value, ai_cost_currency)
VALUES
(3001, '2026-09-15 10:00:00', 'gw-01', 'BFE', 'key-1', 'gpt-4', 100, 1500, 'RMB'),
(3002, '2026-09-15 10:00:00', 'gw-01', 'BFE', 'key-1', 'gpt-4', 200, 2500, 'RMB'),
(3003, '2026-09-15 10:00:00', 'gw-01', 'BFE', 'key-1', 'gpt-4', 300, 3500, 'RMB')
ON DUPLICATE KEY UPDATE logid=VALUES(logid), ai_total_tokens=VALUES(ai_total_tokens)`

var rs *testutil.ReportServer

func TestMain(m *testing.M) {
	server, err := testutil.StartReportServer(aggregateSeedSQL, detailSeedSQL)
	if errors.Is(err, testutil.ErrReportMySQLDSNNotSet) {
		fmt.Println("REPORT_MYSQL_DSN not set; skipping report dedup-key integration tests.")
		fmt.Println(`To enable: REPORT_MYSQL_DSN="root:****@tcp(127.0.0.1:3306)/" go test ./tests/report/dedup_key/`)
		os.Exit(0)
	}
	if err != nil {
		fmt.Println("setup report integration environment failed:", err)
		os.Exit(1)
	}
	rs = server

	code := m.Run()
	rs.Close()
	os.Exit(code)
}

// TestReportDedupKey 按"明细保留 → 旧键对照 → 重放幂等"的顺序执行；
// 三个子用例共享同一份数据，顺序不可调换（第三个会改写前两个断言过的值）。
func TestReportDedupKey(t *testing.T) {
	t.Run("detail_key_keeps_concurrent_rows", testDetailKeyKeepsConcurrentRows)
	t.Run("legacy_four_column_key_collapses", testLegacyFourColumnKeyCollapses)
	t.Run("replay_still_idempotent", testReplayStillIdempotent)
}

// ---------------------------------------------------------------------------
// 响应结构（仅本用例组断言涉及的字段，JSON 形状与 model/ireport 一致）
// ---------------------------------------------------------------------------

type overviewData struct {
	RequestTotal int64 `json:"request_total"`
	TotalTokens  int64 `json:"total_tokens"`
	LogsTotal    int64 `json:"logs_total"`
	Cost         []struct {
		Currency string  `json:"currency"`
		Value    float64 `json:"value"`
	} `json:"cost"`
}

type logItem struct {
	LogID          *int64  `json:"logid"`
	LogTime        int64   `json:"log_time"`
	Hostid         *string `json:"hostid"`
	APIKeyID       *string `json:"ai_apikey_id"`
	RequestedModel *string `json:"ai_requested_model"`
	TotalTokens    *int64  `json:"ai_total_tokens"`
}

type logsData struct {
	Total int64     `json:"total"`
	Items []logItem `json:"items"`
}

func getData(t *testing.T, path string, query map[string]string, out interface{}) {
	t.Helper()

	resp, err := testutil.GetClient().Get(reportPrefix+path, query)
	require.NoError(t, err)
	require.NotNil(t, resp)
	testutil.AssertSuccess(t, resp)
	if out != nil {
		require.NoError(t, json.Unmarshal(resp.Data, out), "data: %s", string(resp.Data))
	}
}

func windowQuery() map[string]string {
	return map[string]string{
		"start": fmt.Sprintf("%d", epoch("2026-09-15 09:59:00")),
		"end":   fmt.Sprintf("%d", epoch("2026-09-15 10:05:00")),
	}
}

// epoch 把 UTC 民用时刻转为 Unix 秒（查询参数；驱动按 UTC 格式化回 DATETIME）。
func epoch(datetime string) int64 {
	tt, err := time.ParseInLocation("2006-01-02 15:04:05", datetime, time.UTC)
	if err != nil {
		panic(err)
	}
	return tt.Unix()
}

// ---------------------------------------------------------------------------
// 子用例 1：修复后的键把 3 条并发请求全部保留下来
// ---------------------------------------------------------------------------

func testDetailKeyKeepsConcurrentRows(t *testing.T) {
	var overview overviewData
	getData(t, "/overview", windowQuery(), &overview)

	// 聚合表口径（预置为与 3 行明细一致）。
	assert.Equal(t, int64(3), overview.RequestTotal)
	assert.Equal(t, int64(75), overview.TotalTokens)

	// 明细侧：修复前这里会是 1（同秒同 Key 同模型互相覆盖）。
	assert.Equal(t, int64(3), overview.LogsTotal, "logs_total must count every concurrent row (bfenetworks/bfe#1417)")
	require.Len(t, overview.Cost, 1)
	assert.InDelta(t, 7500.0/1e8, overview.Cost[0].Value, 1e-15)

	var logs logsData
	getData(t, "/logs", windowQuery(), &logs)
	assert.Equal(t, int64(3), logs.Total, "the detail list must return every concurrent row")
	require.Len(t, logs.Items, 3)

	// 键前提：3 行的 (log_time, hostid, ai_apikey_id, ai_requested_model) 全同，
	// 唯一差别是 logid —— 旧四列键下它们必然互相覆盖。
	seen := map[int64]bool{}
	var tokenSum int64
	for _, item := range logs.Items {
		require.NotNil(t, item.LogID)
		require.NotNil(t, item.Hostid)
		require.NotNil(t, item.APIKeyID)
		require.NotNil(t, item.RequestedModel)
		require.NotNil(t, item.TotalTokens)

		assert.Equal(t, epoch("2026-09-15 10:00:00"), item.LogTime, "rows must share one log_time second")
		assert.Equal(t, "gw-01", *item.Hostid)
		assert.Equal(t, "key-1", *item.APIKeyID)
		assert.Equal(t, "gpt-4", *item.RequestedModel)

		assert.False(t, seen[*item.LogID], "logid %d returned twice", *item.LogID)
		seen[*item.LogID] = true
		tokenSum += *item.TotalTokens
	}
	assert.Len(t, seen, 3, "the three rows must carry three distinct logids")
	assert.Equal(t, int64(75), tokenSum)
}

// ---------------------------------------------------------------------------
// 子用例 2：对照臂——旧四列键把同一批数据折叠成 1 行
// ---------------------------------------------------------------------------

func testLegacyFourColumnKeyCollapses(t *testing.T) {
	require.NotNil(t, rs, "report server must be assembled")
	require.NotNil(t, rs.DB)

	if _, err := rs.DB.Exec(legacyTableDDL); err != nil {
		t.Fatalf("create legacy mirror table failed: %v", err)
	}
	if _, err := rs.DB.Exec(legacyBatchSQL); err != nil {
		t.Fatalf("legacy batch insert failed: %v", err)
	}

	// 旧键下 3 个并发请求只剩 1 行，且幸存的是最后写入行（与 issue 现场一致）。
	var rows int
	var survivorLogID, survivorTokens int64
	row := rs.DB.QueryRow(`SELECT COUNT(*), MAX(logid), MAX(ai_total_tokens)
  FROM bfe_ai_request_log_legacy_uk`)
	if err := row.Scan(&rows, &survivorLogID, &survivorTokens); err != nil {
		t.Fatalf("query legacy mirror table failed: %v", err)
	}
	assert.Equal(t, 1, rows, "the legacy 4-column key collapses the same 3 rows into 1 (the defect)")
	assert.Equal(t, int64(3003), survivorLogID, "the last written row survives, earlier rows are gone")
	assert.Equal(t, int64(35), survivorTokens)
}

// ---------------------------------------------------------------------------
// 子用例 3：重放仍幂等——同 logid 覆盖更新，不产生重复行
// ---------------------------------------------------------------------------

func testReplayStillIdempotent(t *testing.T) {
	require.NotNil(t, rs, "report server must be assembled")
	require.NotNil(t, rs.DB)

	if _, err := rs.DB.Exec(replayBatchSQL); err != nil {
		t.Fatalf("replay batch insert failed: %v", err)
	}

	var logs logsData
	getData(t, "/logs", windowQuery(), &logs)
	assert.Equal(t, int64(3), logs.Total, "replaying the same logids must not add rows")
	require.Len(t, logs.Items, 3)

	var tokenSum int64
	seen := map[int64]bool{}
	for _, item := range logs.Items {
		require.NotNil(t, item.LogID)
		require.NotNil(t, item.TotalTokens)
		seen[*item.LogID] = true
		tokenSum += *item.TotalTokens
	}
	assert.Len(t, seen, 3)
	// 值被覆盖更新：100+200+300（修复前也是覆盖语义，此处锁定"重放仍收敛"）。
	assert.Equal(t, int64(600), tokenSum, "replayed rows must be overwritten, not appended")
}
