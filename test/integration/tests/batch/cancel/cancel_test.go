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

package cancel_test

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	helper "github.com/rainway-ai-gateway/ai-gateway-api/integration/tests/batch/helper"
	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	var err error
	sm, err = testutil.StartServerAuto()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	sm.Shutdown()
	os.Exit(code)
}

func cancelPath(batchID string) string {
	return "/open-api/v1/batches/" + batchID + "/cancel"
}

// waitCancelAudit 轮询 SQLite operation_logs 等待 cancel 审计落库
// （审计后台批量刷盘，瞬态延迟）。
func waitCancelAudit(t *testing.T, db *sql.DB, batchID string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var action, resourceType, resourceID, summary string
		var status int64
		err := db.QueryRow(`
			SELECT action, resource_type, resource_id, status, change_summary
			FROM operation_logs WHERE resource_type = 'batch_task' AND resource_id = ?
			ORDER BY id DESC LIMIT 1
		`, batchID).Scan(&action, &resourceType, &resourceID, &status, &summary)
		if err == nil {
			return map[string]interface{}{
				"action": action, "resource_type": resourceType,
				"resource_id": resourceID, "status": status, "change_summary": summary,
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancel audit log for %s not found in operation_logs: %v", batchID, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ---------- BT-3-000 可达性冒烟：产品线回退后可进入业务逻辑（ghost → 404） ----------

func TestBatchCancel_Reachable(t *testing.T) {
	resp, err := testutil.GetClient().Post(cancelPath("ghost-batch"), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- BT-3-001 任务不存在 → 404 ----------

func TestBatchCancel_NotFound(t *testing.T) {
	helper.RequireBatchAPI(t)

	resp, err := testutil.GetClient().Post(cancelPath(helper.Unique("ghost")), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- BT-3-002 已终态（completed）→ 409 ----------

func TestBatchCancel_TerminalCompleted(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-002")
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: "deepseek", Status: "completed", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Post(cancelPath(tag), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 409)
}

// ---------- BT-3-003 cancelling（受理中中间态）重放 → 200 幂等（issue #274） ----------

// 语义（issue #274 修复，batches.md §3.3 处理逻辑 1）：抢占 affected=0 时
// 按任务当前状态判别——已终态 → 409；已 cancelling → 200 幂等重放：重发
// provider cancel 保证取消意图落地（provider cancel 幂等），释放为幂等
// no-op，不新增 release/charge/settlement transition。修复前本用例断言 409
//（假冲突：无并发实例在处理，是上个请求的残留受理态），design.md §4.3 记录。
func TestBatchCancel_CancellingReplay(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-003")
	provider := tag + "-provider"

	// 本地假 provider：重放出网打回本测试进程，验证重发确实发生。
	cancelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelCalls++
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
		Provider: provider, KeyName: "key-primary", Status: "cancelling", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Post(cancelPath(tag), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var task map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &task))
	assert.Equal(t, "cancelling", task["status"])
	assert.Equal(t, 1, cancelCalls, "幂等重放应重发一次 provider cancel")

	audit := waitCancelAudit(t, db, tag)
	assert.Equal(t, int64(1), audit["status"], "重放成功审计 status must be success(1)")
}

// ---------- BT-3-004 非终态但 provider 不可达 → 502 ----------

func TestBatchCancel_UpstreamUnreachable(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-004")
	provider := tag + "-provider"
	// 实例指向未监听的回环端口：连接被拒 → ErrUpstreamUnreachable。
	helper.InsertProvider(t, db, provider,
		`[{"addr":"127.0.0.1","port":9,"weight":100}]`,
		`[{"name":"key-primary","key":"sk-test"}]`)
	defer helper.DeleteProvider(t, db, provider)

	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: provider, KeyName: "key-primary", Status: "in_progress", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Post(cancelPath(tag), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 502)
}

// ---------- BT-3-005 provider 记录缺失 → 500（模型错误，design.md 记录实现语义） ----------

func TestBatchCancel_ProviderRecordMissing(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-005")
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: "no-such-provider", Status: "in_progress", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Post(cancelPath(tag), map[string]interface{}{})
	require.NoError(t, err)
	// errProviderUnavailable 为模型错误（WrapModelErrorWithMsg），当前实现
	// 映射 500 而非 502；钉死实现语义，design.md §4.3 有记录。
	assert.Equal(t, 500, resp.ErrNum)
}

// ---------- BT-3-006 cancel 成功 → 200 cancelling + operation_logs 审计 ----------

func TestBatchCancel_SuccessWithAudit(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-006")
	provider := tag + "-provider"

	// 本地假 provider：cancel 出网打回本测试进程，验证请求路径并返回 200。
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	var upstreamPort string
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

	resp, err := testutil.GetClient().Post(cancelPath(tag), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var task map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &task))
	assert.Equal(t, tag, task["batch_id"])
	assert.Equal(t, "cancelling", task["status"])

	// 出网 cancel 路径：protocol_paths.openai(/v1) + /batches/{id}/cancel
	assert.Equal(t, "/v1/batches/"+tag+"/cancel", gotPath)

	// 审计：operation_logs 直查（后台刷盘，轮询等待）
	audit := waitCancelAudit(t, db, tag)
	assert.Equal(t, "update", audit["action"])
	assert.Equal(t, "batch_task", audit["resource_type"])
	assert.Equal(t, tag, audit["resource_id"])
	assert.Equal(t, int64(1), audit["status"], "cancel success audit status must be success(1)")
	assert.Contains(t, audit["change_summary"], tag)
	assert.Contains(t, audit["change_summary"], "cancelling")
}

// ---------- BT-3-008 取消响应零值键必现（issue #271） ----------

// TestBatchCancel_ZeroValuesExplicit 锁定取消接口与详情/列表同视图规则：
// 零值 int64 与空串必须显式出现（batches.md §2.1"字段必现规则"）。防
// "只修详情、取消残留"——cancel.go 必须同批改。当前 HEAD 上本用例必须失败。
func TestBatchCancel_ZeroValuesExplicit(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-008")
	provider := tag + "-provider"

	// 本地假 provider：cancel 出网打回本测试进程并返回 200（同 BT-3-006）。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	_, upstreamPort, err := net.SplitHostPort(upstream.URL[len("http://"):])
	require.NoError(t, err)

	helper.InsertProvider(t, db, provider,
		`[{"addr":"127.0.0.1","port":`+upstreamPort+`,"weight":100}]`,
		`[{"name":"key-primary","key":"sk-test"}]`)
	defer helper.DeleteProvider(t, db, provider)

	// 零值种子：未预留金额（reserve_units=0）、未结算（settle_units=0）、
	// 未终结（terminal_at=NULL）——与 issue 复现数据同形。
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: provider, KeyName: "key-primary", Status: "in_progress",
		ReserveUnits: 0, SettleUnits: 0, IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Post(cancelPath(tag), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.Equal(t, "cancelling", data["status"])

	for _, k := range []string{"est_lines", "usage_input_tokens", "usage_output_tokens",
		"reserve_units", "settle_units"} {
		v, ok := data[k]
		require.True(t, ok, "取消响应键 %s 必须显式出现", k)
		assert.Equal(t, float64(0), v, "取消响应键 %s 的值必须为 0", k)
	}
	for _, k := range []string{"output_file_id", "terminal_at"} {
		v, ok := data[k]
		require.True(t, ok, "取消响应键 %s 必须显式出现", k)
		assert.Equal(t, "", v, "取消响应键 %s 的值必须为空串", k)
	}

	body := string(resp.RawBody)
	for _, token := range []string{`"reserve_units":0`, `"settle_units":0`, `"terminal_at":""`} {
		assert.Contains(t, body, token, "原始报文必须含 %s", token)
	}
}

// ---------- BT-3-009 keyless provider（keys: []）cancel → 200 且无鉴权头（issue #274） ----------

// keyless 是合法配置（providers.md keys 条款：非必填、默认 []；clusters.md：
// 空数组 = 不配置 API-Key），取消请求不携带鉴权头出网，与数据面 keyless
// cluster 转发语义一致。修复前本用例失败（500 batch provider unavailable，
// 且抢占残留导致后续 409 假冲突与三视图分裂）。
func TestBatchCancel_KeylessProvider(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-3-009")
	provider := tag + "-provider"

	var gotPath string
	var authPresent bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, authPresent = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	_, upstreamPort, err := net.SplitHostPort(upstream.URL[len("http://"):])
	require.NoError(t, err)

	// keys 为空数组 = 不配置 API-Key（keyless provider）。
	helper.InsertProvider(t, db, provider,
		`[{"addr":"127.0.0.1","port":`+upstreamPort+`,"weight":100}]`,
		`[]`)
	defer helper.DeleteProvider(t, db, provider)

	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: provider, Status: "in_progress", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Post(cancelPath(tag), map[string]interface{}{})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var task map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &task))
	assert.Equal(t, "cancelling", task["status"])
	assert.Equal(t, "/v1/batches/"+tag+"/cancel", gotPath)
	assert.False(t, authPresent, "keyless provider 不得携带 Authorization 头")
}
