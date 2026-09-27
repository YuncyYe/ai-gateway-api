// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build mysql

package intent_config_update_test

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

// TestIntentConfig_Update_ConcurrentMySQL（IC-1-013，家族10）在 MySQL 后端下并发执行
// 单例覆盖式 PUT。回归目标：单行 upsert 的单事务路径在并发下不得出现 5xx/deadlock/
// 部分写入——所有请求必须 2xx，且最终读回的单例与某一次提交完全相等（min_confidence
// 与该次提交的问题名同属一次提交，无混合状态）。
//
// SQLite 单写者结构性无法测并发，本用例按家族10 要求以 //go:build mysql 落盘。
// 需要真实 MySQL：DSN 取自 AIAPI_MYSQL_DSN（形如 root:pass@tcp(127.0.0.1:3306)/）。
func TestIntentConfig_Update_ConcurrentMySQL(t *testing.T) {
	adminDSN := os.Getenv("AIAPI_MYSQL_DSN")
	if adminDSN == "" {
		t.Skip("AIAPI_MYSQL_DSN not set, skipping MySQL concurrency test")
	}

	sm, err := testutil.StartServerWithMySQL(adminDSN)
	require.NoError(t, err, "start MySQL-backed server")
	defer sm.Shutdown()

	client := &testutil.Client{BaseURL: sm.ServerURL, HTTPClient: &http.Client{Timeout: 60 * time.Second}}

	const workers = 20
	type submission struct {
		minConfidence float64
		questionName  string
	}
	subs := make([]submission, workers)
	for w := 0; w < workers; w++ {
		subs[w] = submission{
			minConfidence: 0.5 + float64(w)*0.01,
			questionName:  fmt.Sprintf("ic-1-013-w%02d-%s", w, testutil.RandomString(8)),
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			body := map[string]interface{}{
				"min_confidence": subs[w].minConfidence,
				"questions": []interface{}{
					map[string]interface{}{
						"name":         subs[w].questionName,
						"type":         "choice",
						"instructions": "i",
						"criteria":     map[string]interface{}{"a": "b"},
					},
				},
			}
			resp, err := client.Put(intentConfigPath, body)
			if err != nil {
				errCh <- fmt.Errorf("worker %d: PUT error: %w", w, err)
				return
			}
			// 家族7/#183：写操作只允许 2xx/4xx，500/555/556 之外的 5xx 即缺陷。
			if resp.ErrNum != 200 {
				errCh <- fmt.Errorf("worker %d: PUT failed: %d %s", w, resp.ErrNum, resp.ErrMsg)
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Errorf("concurrent PUT failed (家族10): %v", e)
	}

	// 终态必须与某一次提交完全相等（无混合状态）。
	getResp, err := client.Get(intentConfigPath)
	require.NoError(t, err)
	require.Equal(t, 200, getResp.ErrNum, getResp.ErrMsg)

	var data struct {
		MinConfidence float64 `json:"min_confidence"`
		Questions     []struct {
			Name string `json:"name"`
		} `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(getResp.Data, &data))
	require.Len(t, data.Questions, 1, "final singleton must equal exactly one submission")

	matched := -1
	for w, s := range subs {
		if s.questionName == data.Questions[0].Name && s.minConfidence == data.MinConfidence {
			matched = w
			break
		}
	}
	assert.NotEqual(t, -1, matched,
		"final singleton (name=%s min_confidence=%v) must equal one whole submission, no mixed state",
		data.Questions[0].Name, data.MinConfidence)
}
