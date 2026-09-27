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

// 本文件覆盖 Backend=doris 的查询层能力门控（一期）：三个新维度
// （ai_intent_answer / ai_cache_status / mirror_hit）在 rankings /
// distribution / timeseries 上返回 422，错误信息指明 "mysql only until
// doris support lands"；overview / logs / timeseries?metric=cache_tokens
// 等明细能力不受门控影响。
//
// 装配说明：数据源仍是 REPORT_MYSQL_DSN 指向的 MySQL 实例（422 在查询层
// 拦截、不触达 SQL），仅 [Report].Backend 装配为 "doris"（与
// testutil.StartReportServerWithBackend 的约定一致）。

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startDorisBackendServer 以 Backend="doris" 装配一个独立 api 进程（独立
// 随机库与端口），REPORT_MYSQL_DSN 未设置时 Skip。
func startDorisBackendServer(t *testing.T) *testutil.ReportServer {
	t.Helper()
	if os.Getenv("REPORT_MYSQL_DSN") == "" {
		t.Skip("REPORT_MYSQL_DSN not set; skipping doris gate integration tests.")
	}
	rs, err := testutil.StartReportServerWithBackend("doris", aggregateSeedSQL, detailSeedSQL)
	if err != nil {
		if errors.Is(err, testutil.ErrReportMySQLDSNNotSet) {
			t.Skip("REPORT_MYSQL_DSN not set; skipping doris gate integration tests.")
		}
		t.Fatalf("setup doris-backend report server failed: %v", err)
	}
	t.Cleanup(rs.Close)
	return rs
}

func TestDorisBackend_NewDimensionGate(t *testing.T) {
	rs := startDorisBackendServer(t)
	client := &testutil.Client{
		BaseURL:    rs.Server.ServerURL,
		HTTPClient: testutil.GetClient().HTTPClient,
		Token:      testutil.GetClient().Token,
	}

	newDims := []string{"ai_intent_answer", "ai_cache_status", "mirror_hit"}
	window := windowQuery()

	t.Run("rankings", func(t *testing.T) {
		for _, dim := range newDims {
			query := with(window, "dimension", dim)
			resp, err := client.Get(reportPrefix+"/rankings", query)
			require.NoError(t, err, dim)
			require.NotNil(t, resp)
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, fmt.Sprintf("dimension %s not supported by doris backend", dim))
			assert.Contains(t, resp.ErrMsg, "mysql only until doris support lands")
		}
	})

	t.Run("distribution", func(t *testing.T) {
		for _, dim := range newDims {
			query := with(window, "dimension", dim)
			resp, err := client.Get(reportPrefix+"/distribution", query)
			require.NoError(t, err, dim)
			require.NotNil(t, resp)
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, fmt.Sprintf("dimension %s not supported by doris backend", dim))
		}
	})

	t.Run("timeseries", func(t *testing.T) {
		for _, dim := range newDims {
			query := with(window, "metric", "qps", "dimension", dim)
			resp, err := client.Get(reportPrefix+"/timeseries", query)
			require.NoError(t, err, dim)
			require.NotNil(t, resp)
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, fmt.Sprintf("dimension %s not supported by doris backend", dim))
		}
	})

	t.Run("legacy_dimension_still_supported", func(t *testing.T) {
		// 既有维度在 doris 后端行为不变。
		resp, err := client.Get(reportPrefix+"/rankings", with(window, "dimension", "model"))
		require.NoError(t, err)
		require.NotNil(t, resp)
		testutil.AssertSuccess(t, resp)
	})
}

// TestDorisBackend_DetailCapabilitiesUngated 验证一期 Doris 明细能力不受
// 维度门控影响：logs 新过滤（两后端方言一致的明细列过滤）在 doris 后端
// 正常返回。overview 的 PERCENTILE_APPROX 与 timeseries 的 Doris 桶表达式
// 属 Doris 方言，本 harness 数据源为 MySQL 实例无法执行（需真实 Doris FE），
// 不在此覆盖（由两侧 storager 单测锁定）。
func TestDorisBackend_DetailCapabilitiesUngated(t *testing.T) {
	rs := startDorisBackendServer(t)
	client := &testutil.Client{
		BaseURL:    rs.Server.ServerURL,
		HTTPClient: testutil.GetClient().HTTPClient,
		Token:      testutil.GetClient().Token,
	}

	window := windowQuery()

	t.Run("logs_filter", func(t *testing.T) {
		resp, err := client.Get(reportPrefix+"/logs", with(window, "cache_status", "hit"))
		require.NoError(t, err)
		require.NotNil(t, resp)
		testutil.AssertSuccess(t, resp)
	})

	t.Run("logs_new_columns_projected", func(t *testing.T) {
		resp, err := client.Get(reportPrefix+"/logs", with(window, "intent_answer", "coding"))
		require.NoError(t, err)
		require.NotNil(t, resp)
		testutil.AssertSuccess(t, resp)
	})
}
