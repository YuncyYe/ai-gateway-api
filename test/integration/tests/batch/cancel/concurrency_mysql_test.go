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

//go:build mysql

package cancel_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	helper "github.com/rainway-ai-gateway/ai-gateway-api/integration/tests/batch/helper"
	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBatchCancel_ConcurrentPreemptMySQL（BT-3-007）在 MySQL 后端下并发
// cancel 同一 in_progress 任务，验证 DB 条件更新抢占
// （UPDATE ... WHERE batch_id=? AND product_name=? AND status NOT IN (终态)）
// 在真实 MySQL 行锁/原子语义下的受理互斥，以及 issue #274 后的幂等重放契约：
//
//   - 恰好一个调用抢占受理（affected=1，主路径出网）；
//   - 其余调用 affected=0 且读回状态为 cancelling → 走 200 幂等重放
//     （重发 provider cancel，幂等；释放为幂等 no-op）；
//   - 因此全部调用返回 200，upstream 收到 20 次幂等 cancel，全部审计
//     为成功（status=1）。
//
// 需要真实 MySQL：DSN 取自 AIAPI_MYSQL_DSN（形如
// root:pass@tcp(127.0.0.1:3306)/）。SQLite 单写者串行无法暴露该行锁
// 竞态，本用例以 //go:build mysql 落盘（家族10，仿 traffic_mirror）。
//
// 修复前契约为"1×200 + 19×409 + upstream 恰好 1 次出网"（防惊群）；
// issue #274 裁定 cancelling 重放为 409 假冲突（误导排障，且抢占残留
// 可能是首次出网未落地的崩溃空洞），改为幂等 200 并重发 cancel 恢复
// 意图落地——并发重放出网均幂等，见 batches.md §3.3 处理逻辑 1 与
// design.md §4.3。
func TestBatchCancel_ConcurrentPreemptMySQL(t *testing.T) {
	adminDSN := os.Getenv("AIAPI_MYSQL_DSN")
	if adminDSN == "" {
		t.Skip("AIAPI_MYSQL_DSN not set, skipping MySQL concurrency test")
	}

	sm, err := testutil.StartServerWithMySQL(adminDSN)
	require.NoError(t, err, "start MySQL-backed server")
	defer sm.Shutdown()

	client := &testutil.Client{BaseURL: sm.ServerURL, HTTPClient: &http.Client{Timeout: 60 * time.Second}}
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-007")
	provider := tag + "-provider"

	// 本地假 provider：受理方与重放方的出网都打到本测试进程
	// （每次 cancel 均为幂等 POST，共期望 workers 次）。
	var upstreamHits int
	var upstreamMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamMu.Lock()
		upstreamHits++
		upstreamMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	_, upstreamPort, err := net.SplitHostPort(upstream.URL[len("http://"):])
	require.NoError(t, err)

	helper.InsertProvider(t, db, provider,
		`[{"addr":"127.0.0.1","port":`+upstreamPort+`,"weight":100}]`,
		`[{"name":"key-primary","key":"sk-test"}]`)
	defer helper.DeleteProvider(t, db, provider)

	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: provider, KeyName: "key-primary", Status: "in_progress", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	const workers = 20
	var wg sync.WaitGroup
	results := make(chan int, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Post("/open-api/v1/batches/"+tag+"/cancel", map[string]interface{}{})
			if err != nil {
				results <- -1
				return
			}
			results <- resp.ErrNum
		}()
	}
	wg.Wait()
	close(results)

	okCount, conflictCount, other := 0, 0, map[int]int{}
	for code := range results {
		switch code {
		case 200:
			okCount++
		case 409:
			conflictCount++
		default:
			other[code]++
		}
	}
	// issue #274 契约：1 个抢占受理 + 19 个幂等重放，全部 200；
	// 409 仅属于终态任务（本场景不存在）。
	assert.Equal(t, workers, okCount, "1 个抢占受理 + workers-1 个幂等重放，全部 200")
	assert.Equal(t, 0, conflictCount, "并发重放不得再返回 409 假冲突")
	assert.Empty(t, other, "no unexpected error codes")

	// 每次调用各出网一次幂等 cancel（1 主路径 + workers-1 重放）。
	upstreamMu.Lock()
	assert.Equal(t, workers, upstreamHits, "upstream must receive one idempotent cancel POST per call")
	upstreamMu.Unlock()

	// 审计：每次尝试各记一条成功审计（status=1）。
	var successAudits, totalAudits int
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := db.QueryRow(`
			SELECT
				COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END), 0),
				COUNT(*)
			FROM operation_logs WHERE resource_type = 'batch_task' AND resource_id = ?
		`, tag).Scan(&successAudits, &totalAudits)
		require.NoError(t, err)
		if totalAudits >= workers || time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	assert.Equal(t, workers, successAudits, "per-attempt audits all success after issue #274")
	assert.Equal(t, workers, totalAudits,
		"implementation records one audit per cancel attempt")

	// 终态校验：任务保持 cancelling（受理中），由状态推进对账收敛。
	var status string
	require.NoError(t, db.QueryRow("SELECT status FROM batch_tasks WHERE batch_id = ?", tag).Scan(&status))
	assert.Equal(t, "cancelling", status)
}
