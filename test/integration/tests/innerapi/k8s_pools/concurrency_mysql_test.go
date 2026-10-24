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

package innerapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInnerAPI_K8sPools_ConcurrentPutMySQL 在 MySQL 后端下并发对同一 pool
// 执行全量替换 PUT（skill 家族10：并发用例必须落盘，SQLite 结构性无法测
// 并发）。回归目标：幂等 upsert 的 upsert 路径在唯一键并发下不得出现
// duplicate-key 500、事务死锁或部分写入——所有请求必须 2xx，且最终读回
// 的实例列表与某一提交的合法 payload 完全一致（last-write-wins 语义）。
//
// 需要真实 MySQL：DSN 取自 AIAPI_MYSQL_DSN（形如 root:pass@tcp(127.0.0.1:3306)/）。
func TestInnerAPI_K8sPools_ConcurrentPutMySQL(t *testing.T) {
	adminDSN := os.Getenv("AIAPI_MYSQL_DSN")
	if adminDSN == "" {
		t.Skip("AIAPI_MYSQL_DSN not set, skipping MySQL concurrency test")
	}

	sm, err := testutil.StartServerWithMySQL(adminDSN)
	require.NoError(t, err, "start MySQL-backed server")
	defer sm.Shutdown()

	client := &testutil.Client{BaseURL: sm.ServerURL, HTTPClient: &http.Client{Timeout: 60 * time.Second}}

	poolName := uniquePoolName()
	t.Cleanup(func() {
		resp, err := client.Delete("/inner-api/v1/k8s_pools/" + poolName)
		if err == nil && resp.ErrNum != 200 && resp.ErrNum != 404 {
			t.Logf("cleanup delete pool %s: ErrNum=%d %s", poolName, resp.ErrNum, resp.ErrMsg)
		}
	})

	const workers = 20
	payloads := make([]map[string]interface{}, workers)
	for w := 0; w < workers; w++ {
		payloads[w] = map[string]interface{}{
			"addr":   fmt.Sprintf("10.8.%d.%d", w/256, w%256),
			"port":   8000,
			"weight": 100,
		}
	}

	errs := make(chan error, workers)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			resp, err := client.Put("/inner-api/v1/k8s_pools/"+poolName+"/instances",
				[]interface{}{payloads[w]})
			if err != nil {
				errs <- fmt.Errorf("request error: %w", err)
				return
			}
			if resp.ErrNum != 200 {
				errs <- fmt.Errorf("worker %d: ErrNum=%d %s", w, resp.ErrNum, resp.ErrMsg)
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	elapsed := time.Since(start)

	for e := range errs {
		t.Errorf("concurrent PUT failed: %v", e)
	}

	// 最终一致性：读回内容必须与某一提交的 payload 完全一致。
	resp, err := client.Get("/inner-api/v1/k8s_pools/" + poolName)
	require.NoError(t, err)
	require.Equal(t, 200, resp.ErrNum, "final read failed: %s", resp.ErrMsg)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	instances, ok := data["instances"].([]interface{})
	require.True(t, ok, "instances should be an array")
	require.Len(t, instances, 1, "last-write-wins: exactly one submitted payload survives")
	got, ok := instances[0].(map[string]interface{})
	require.True(t, ok)

	matched := false
	for _, p := range payloads {
		if got["addr"] == p["addr"] && got["port"] == float64(p["port"].(int)) && got["weight"] == float64(p["weight"].(int)) {
			matched = true
			break
		}
	}
	assert.True(t, matched, "surviving payload %v must be one of the submitted payloads", got)
	assert.GreaterOrEqual(t, data["last_sync_time"], float64(start.Unix()))
	t.Logf("concurrent PUT: %d workers, elapsed=%s", workers, elapsed)
}
