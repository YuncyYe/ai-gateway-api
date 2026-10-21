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
//limitations under the License. All rights reserved.

// DB 落盘加密（密钥静态加密）集成测试（模块前缀 SAR，场景登记见同目录 design.md）。
// 被测对象为敏感列（providers.api_keys / api_keys.api_key）的透明加解密与
// keyrotate 收敛任务；核心断言手段是绕过服务进程直读被测库
// （database/sql：SQLite 用 sqlite-strip 驱动，MySQL 用 mysql 驱动）：
// 无 keyring 时两列必须明文、配置 keyring 后必须为 enc$v1$ 信封且 keyID
// 正确、轮换收敛后全部行 keyID 切到 active。各用例独立
// StartServerWithMonitorAuto 实例注入 [Security] 段（keyring 文件写在
// t.TempDir()），用例内步骤串行。
//
// 后端选择：设置 AIAPI_MYSQL_DSN（管理员 DSN，不带库名，形如
// root:pass@tcp(127.0.0.1:3306)/）时以 MySQL 启动（自动建库、执行 db_ddl.sql、
// 用毕 DROP），否则保持 SQLite。MySQL 后端是本模块的关键回归面：
// providers.api_keys 若是 JSON 列，整列 enc$v1$ 信封写入必报
// Error 3140（issue #264），而 SQLite 的 TEXT 列不校验 JSON、永远测不出来。
package secret_at_rest_test

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sweepPath          = "/open-api/v1/security/reencrypt-sweeps"
	innerModAPIKeyPath = "/inner-api/v1/configs/mod-api-key"
	// providerPlainKey 为 testutil.CreateProvider 注入的固定明文密钥，
	// 用于直读 DB 与 OpenAPI 读回的明文一致性断言。
	providerPlainKey = "sk-aaaaaaaaaaaa"
)

// newMasterKey 生成 32 字节随机主密钥。
func newMasterKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

// writeKeyringFile 按 keyring TOML 格式覆写 path：
//
//	ActiveKeyID = <activeID>
//	[Keys]
//	  <id> = "<base64 32-byte master key>"
func writeKeyringFile(t *testing.T, path string, activeID int, keys map[int][]byte) {
	t.Helper()

	var b strings.Builder
	fmt.Fprintf(&b, "ActiveKeyID = %d\n[Keys]\n", activeID)
	ids := make([]int, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "  %d = \"%s\"\n", id, base64.StdEncoding.EncodeToString(keys[id]))
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
}

// securitySectionTOML 生成注入服务端配置的 [Security] 段（路径统一正斜杠）。
func securitySectionTOML(keyFile string) string {
	return fmt.Sprintf("[Security]\nMasterKeyFile = \"%s\"\n", filepath.ToSlash(keyFile))
}

// startSARServer 启动带 monitor 端口的独立实例（本模块全部用例统一入口）。
func startSARServer(t *testing.T, extraTOML string) *testutil.ServerManager {
	t.Helper()

	sm, err := testutil.StartServerWithMonitorAuto(extraTOML)
	require.NoError(t, err)
	t.Cleanup(sm.Shutdown)
	testutil.SetServerURL(sm.ServerURL)
	return sm
}

// openRawDB 直读被测库：MySQL 后端（AIAPI_MYSQL_DSN）用 mysql 驱动连测试库，
// 否则读服务端 SQLite 文件（busy_timeout 与 createTempConfig 同款，避免与后台
// 审计刷盘瞬态争用写锁）。两套后端下断言语义一致，故统一收口到本函数。
func openRawDB(t *testing.T, sm *testutil.ServerManager) *sql.DB {
	t.Helper()

	dsn := sm.MySQLDSN()
	if dsn != "" {
		db, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		return db
	}

	sqliteDSN := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", filepath.ToSlash(sm.DBPath))
	db, err := sql.Open("sqlite-strip", sqliteDSN)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// requireSQLiteBackend 跳过依赖「共享 SQLite 文件」基础设施的用例
// （MySQL 后端没有等价的 StartServerWithSharedInfra 形态）。
func requireSQLiteBackend(t *testing.T, sm *testutil.ServerManager) {
	t.Helper()
	if sm.MySQLDSN() != "" {
		t.Skip("skip: case relies on shared SQLite file infra; run without AIAPI_MYSQL_DSN")
	}
}

// allProviderSecrets 返回 providers.name -> api_keys 列原文（含 seed 行）。
func allProviderSecrets(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()

	rows, err := db.Query("SELECT name, api_keys FROM providers")
	require.NoError(t, err)
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var name, val string
		require.NoError(t, rows.Scan(&name, &val))
		out[name] = val
	}
	require.NoError(t, rows.Err())
	return out
}

// apiKeySecret 返回 api_keys 表中指定 id 的 api_key 列原文。
func apiKeySecret(t *testing.T, db *sql.DB, id string) string {
	t.Helper()

	var val string
	require.NoError(t, db.QueryRow("SELECT api_key FROM api_keys WHERE id = ?", id).Scan(&val))
	return val
}

// requireEnvelope 断言值为 enc$v1$ 信封且内嵌 keyID 等于 wantKeyID；ctx 为定位信息。
func requireEnvelope(t *testing.T, value string, wantKeyID int, ctx string) {
	t.Helper()

	if !xcrypto.IsEncrypted(value) {
		t.Fatalf("%s: value must be enc$v1$ envelope, got %q", ctx, truncateForLog(value))
	}
	id, ok := xcrypto.KeyIDOf(value)
	if !ok {
		t.Fatalf("%s: envelope keyID decode failed: %q", ctx, truncateForLog(value))
	}
	if uint8(wantKeyID) != id {
		t.Fatalf("%s: envelope keyID=%d, want %d", ctx, id, wantKeyID)
	}
}

// requireAllSecretsEncrypted 断言两张敏感表全部行均为 enc$v1$ 且 keyID=wantKeyID
// （含 seed 的 deepseek 明文 '[]' 行——reencrypt 模式也会把它收敛为密文）。
func requireAllSecretsEncrypted(t *testing.T, db *sql.DB, wantKeyID int) {
	t.Helper()

	for name, val := range allProviderSecrets(t, db) {
		requireEnvelope(t, val, wantKeyID, fmt.Sprintf("providers.api_keys name=%s", name))
	}
	rows, err := db.Query("SELECT id, api_key FROM api_keys")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id, val string
		require.NoError(t, rows.Scan(&id, &val))
		requireEnvelope(t, val, wantKeyID, fmt.Sprintf("api_keys.api_key id=%s", id))
	}
	require.NoError(t, rows.Err())
}

// requireAllSecretsPlaintext 断言两张敏感表全部行均无 enc$v1$ 前缀。
func requireAllSecretsPlaintext(t *testing.T, db *sql.DB) {
	t.Helper()

	for name, val := range allProviderSecrets(t, db) {
		require.NotContains(t, val, xcrypto.Marker, "providers.api_keys name=%s must be plaintext", name)
	}
	rows, err := db.Query("SELECT id, api_key FROM api_keys")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id, val string
		require.NoError(t, rows.Scan(&id, &val))
		require.NotContains(t, val, xcrypto.Marker, "api_keys.api_key id=%s must be plaintext", id)
	}
	require.NoError(t, rows.Err())
}

func truncateForLog(s string) string {
	const max = 64
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// rawResp 为 raw HTTP 响应的归一化形态（全局 Client 不暴露 HTTP 状态码）。
type rawResp struct {
	Status int
	ErrNum int
	ErrMsg string
	Raw    []byte
}

// doRaw 发送 JSON 请求（body 为 nil 时不带报文体）并返回状态码与原始报文。
func doRaw(t *testing.T, method, url string, body interface{}) rawResp {
	t.Helper()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	r := rawResp{Status: resp.StatusCode, Raw: raw}
	var parsed struct {
		ErrNum int    `json:"ErrNum"`
		ErrMsg string `json:"ErrMsg"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		r.ErrNum = parsed.ErrNum
		r.ErrMsg = parsed.ErrMsg
	}
	return r
}

type sweepTriggerData struct {
	TaskID      string `json:"task_id"`
	Status      string `json:"status"`
	Mode        string `json:"mode"`
	DryRun      bool   `json:"dry_run"`
	Scope       string `json:"scope"`
	ActiveKeyID int    `json:"active_key_id"`
}

type tableCount struct {
	Scanned   int64 `json:"scanned"`
	Rewritten int64 `json:"rewritten"`
	Skipped   int64 `json:"skipped"`
}

type sweepTaskData struct {
	TaskID      string                 `json:"task_id"`
	Status      string                 `json:"status"`
	Mode        string                 `json:"mode"`
	DryRun      bool                   `json:"dry_run"`
	Scope       string                 `json:"scope"`
	ActiveKeyID int                    `json:"active_key_id"`
	Summary     map[string]*tableCount `json:"summary"`
	Error       string                 `json:"error"`
}

// triggerSweep 触发收敛任务并解析 Data。
func triggerSweep(t *testing.T, baseURL string, body map[string]interface{}) (rawResp, sweepTriggerData) {
	t.Helper()

	r := doRaw(t, http.MethodPost, baseURL+sweepPath, body)
	var data sweepTriggerData
	if r.Status == http.StatusOK {
		require.NoError(t, json.Unmarshal(r.Raw, &struct {
			Data *sweepTriggerData `json:"Data"`
		}{Data: &data}))
	}
	return r, data
}

// getSweepTask 查询任务并解析 Data。
func getSweepTask(t *testing.T, baseURL, taskID string) (rawResp, sweepTaskData) {
	t.Helper()

	r := doRaw(t, http.MethodGet, baseURL+sweepPath+"/"+taskID, nil)
	var data sweepTaskData
	require.NoError(t, json.Unmarshal(r.Raw, &struct {
		Data *sweepTaskData `json:"Data"`
	}{Data: &data}))
	return r, data
}

// waitSweepSucceeded 轮询任务至终态，要求 succeeded（failed 时带 error 归因失败）。
func waitSweepSucceeded(t *testing.T, baseURL, taskID string, timeout time.Duration) sweepTaskData {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		r, data := getSweepTask(t, baseURL, taskID)
		require.Equal(t, http.StatusOK, r.Status, "query task raw=%s", r.Raw)
		if data.Status != "running" {
			require.Equal(t, "succeeded", data.Status, "task %s failed: %s", taskID, data.Error)
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s not finished in %v", taskID, timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// reloadSecurity 触发 monitor 端口 /reload/security，返回响应体（调用方断言内容）。
func reloadSecurity(t *testing.T, monitorURL string) string {
	t.Helper()
	require.NotEmpty(t, monitorURL, "monitor port not enabled")

	resp, err := http.Post(monitorURL+"/reload/security", "text/plain", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "raw=%s", body)
	return string(body)
}

// assertProviderKeyPlaintext 断言 OpenAPI 读回的 provider keys 为明文且与创建一致。
func assertProviderKeyPlaintext(t *testing.T, providerName string) {
	t.Helper()

	resp, err := testutil.GetClient().Get("/open-api/v1/providers/" + providerName)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	require.Contains(t, string(resp.RawBody), providerPlainKey, "provider key must be plaintext")
	require.NotContains(t, string(resp.RawBody), xcrypto.Marker, "no ciphertext leakage in OpenAPI")
}

// assertAPIKeyPlaintext 断言 OpenAPI 读回的 api-key 明文与创建时一致。
func assertAPIKeyPlaintext(t *testing.T, apiKeyID, want string) {
	t.Helper()

	resp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	keyField, err := testutil.GetDataField(resp, "key")
	require.NoError(t, err)
	require.Equal(t, want, keyField.(string))
	require.NotContains(t, string(resp.RawBody), xcrypto.Marker, "no ciphertext leakage in OpenAPI")
}

// createSecretFixtures 建 provider + api-key，返回 (providerName, apiKeyID, apiKeyValue)。
func createSecretFixtures(t *testing.T) (string, string, string) {
	t.Helper()

	providerName, err := testutil.CreateProvider(testutil.UniqueProviderName())
	require.NoError(t, err)
	apiKeyID, apiKeyVal, err := testutil.CreateAPIKeyWithKey(testutil.UniqueAPIKeyDesc(), "")
	require.NoError(t, err)
	return providerName, apiKeyID, apiKeyVal
}

// SAR-1-001 未配置 keyring：两敏感列明文落库（向后兼容基线），OpenAPI 读回明文一致。
func TestSAR1_001_PlaintextWithoutKeyring(t *testing.T) {
	sm := startSARServer(t, "")
	providerName, apiKeyID, apiKeyVal := createSecretFixtures(t)

	db := openRawDB(t, sm)

	// 直读 SQLite：providers.api_keys 明文且不含 enc$v1$（seed deepseek 的 '[]' 亦断言）
	provSecrets := allProviderSecrets(t, db)
	require.Contains(t, provSecrets, providerName)
	require.NotContains(t, provSecrets[providerName], xcrypto.Marker)
	require.Contains(t, provSecrets[providerName], providerPlainKey)
	for name, val := range provSecrets {
		require.NotContains(t, val, xcrypto.Marker, "providers.api_keys name=%s must be plaintext", name)
	}

	// api_keys.api_key 明文且等于创建时返回值
	rawVal := apiKeySecret(t, db, apiKeyID)
	require.Equal(t, apiKeyVal, rawVal)
	require.NotContains(t, rawVal, xcrypto.Marker)

	// OpenAPI 读回明文一致
	assertProviderKeyPlaintext(t, providerName)
	assertAPIKeyPlaintext(t, apiKeyID, apiKeyVal)
}

// SAR-1-002 配置 keyring：两敏感列 enc$v1$ 落盘（keyID=1），
// OpenAPI 透明解密读回一致，InnerAPI 导出口为明文（无密文泄漏）。
func TestSAR1_002_EncryptedAtRestTransparentDecrypt(t *testing.T) {
	krPath := filepath.Join(t.TempDir(), "master.keys")
	writeKeyringFile(t, krPath, 1, map[int][]byte{1: newMasterKey(t)})
	sm := startSARServer(t, securitySectionTOML(krPath))

	providerName, apiKeyID, apiKeyVal := createSecretFixtures(t)

	db := openRawDB(t, sm)

	// 直读 DB：两列均 enc$v1$ 信封且内嵌 keyID=1
	requireEnvelope(t, allProviderSecrets(t, db)[providerName], 1, "providers.api_keys")
	requireEnvelope(t, apiKeySecret(t, db, apiKeyID), 1, "api_keys.api_key")

	// OpenAPI 透明解密：读回明文一致
	assertProviderKeyPlaintext(t, providerName)
	assertAPIKeyPlaintext(t, apiKeyID, apiKeyVal)

	// InnerAPI 导出口：200 且含明文 key、不含 enc$v1$（导出走 model 明文）
	r := doRaw(t, http.MethodGet, sm.ServerURL+innerModAPIKeyPath, nil)
	require.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	require.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
	require.Contains(t, string(r.Raw), apiKeyVal, "export must contain plaintext api key")
	require.NotContains(t, string(r.Raw), xcrypto.Marker, "no ciphertext leakage in export")
}

// SAR-1-003 库有密文但无 keyring：第二个实例 fail-fast 拒启动。
// 实例 A（keyring-A）建密文后保持运行；实例 B 经 StartServerWithSharedInfra
// 共享同一 DB 文件与 Redis、但不配置 [Security]——CheckSecretAtRest 发现密文
// 却无 keyring，进程 fail-fast 退出，waitForReady 等满 10s 超时返回 error。
// 注：这是全模块唯一接受 10s 超时的用例（进程退出后端口永不就绪，见 design.md）。
// 仅 SQLite 后端可执行：共享 DB 文件的基础设施（StartServerWithSharedInfra）
// 在 MySQL 后端没有等价形态。
func TestSAR1_003_FailFastNoKeyringOnEncryptedDB(t *testing.T) {
	krPath := filepath.Join(t.TempDir(), "master.keys")
	writeKeyringFile(t, krPath, 1, map[int][]byte{1: newMasterKey(t)})
	sm := startSARServer(t, securitySectionTOML(krPath))
	requireSQLiteBackend(t, sm)

	providerName, _, _ := createSecretFixtures(t)
	db := openRawDB(t, sm)
	requireEnvelope(t, allProviderSecrets(t, db)[providerName], 1, "providers.api_keys")

	_, err := testutil.StartServerWithSharedInfra(sm.Redis, sm.DBPath)
	require.Error(t, err, "instance without keyring must refuse to start on encrypted DB")
}

// SAR-2-001 轮换 + 收敛全链路：keyring v1 建数 → 覆写 keyring v2 + 热加载 →
// dry-run（核对 active_key_id=2 且不落库）→ 正式 sweep 收敛 → 全部行 keyID=2
// → OpenAPI 读回明文一致。
func TestSAR2_001_RotationConvergeFullChain(t *testing.T) {
	krPath := filepath.Join(t.TempDir(), "master.keys")
	key1 := newMasterKey(t)
	key2 := newMasterKey(t)
	writeKeyringFile(t, krPath, 1, map[int][]byte{1: key1})
	sm := startSARServer(t, securitySectionTOML(krPath))

	providerName, apiKeyID, apiKeyVal := createSecretFixtures(t)

	db := openRawDB(t, sm)
	requireEnvelope(t, allProviderSecrets(t, db)[providerName], 1, "providers.api_keys(before)")
	requireEnvelope(t, apiKeySecret(t, db, apiKeyID), 1, "api_keys.api_key(before)")

	// 轮换：覆写 keyring 文件（追加 key 2、active=2）→ 热加载
	writeKeyringFile(t, krPath, 2, map[int][]byte{1: key1, 2: key2})
	msg := reloadSecurity(t, sm.MonitorURL)
	assert.Contains(t, msg, "reloaded")

	// dry-run：触发即核对 active_key_id=2（防"改文件漏热加载"型静默失败）。
	// 合同为 HTTP 202；实现 Render 统一归一为 200（design.md §4 记录偏差）。
	r, trig := triggerSweep(t, sm.ServerURL, map[string]interface{}{"dry_run": true})
	require.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	require.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
	require.Equal(t, "reencrypt", trig.Mode)
	require.True(t, trig.DryRun)
	require.Equal(t, 2, trig.ActiveKeyID)
	require.Equal(t, "running", trig.Status)
	require.NotEmpty(t, trig.TaskID)

	dry := waitSweepSucceeded(t, sm.ServerURL, trig.TaskID, 30*time.Second)
	require.NotNil(t, dry.Summary["providers"], "dry summary raw=%s", r.Raw)
	require.NotNil(t, dry.Summary["api_keys"])
	require.GreaterOrEqual(t, dry.Summary["providers"].Rewritten, int64(1))
	require.GreaterOrEqual(t, dry.Summary["api_keys"].Rewritten, int64(1))

	// dry-run 不写库：新建行仍为 keyID=1（seed deepseek 为 DDL 直插明文，
	// 尚未收敛，不在此断言；正式 sweep 后再断言全表收敛）
	requireEnvelope(t, allProviderSecrets(t, db)[providerName], 1, "providers.api_keys(after dry-run)")
	requireEnvelope(t, apiKeySecret(t, db, apiKeyID), 1, "api_keys.api_key(after dry-run)")

	// 正式 sweep：收敛到 active=2
	r, trig = triggerSweep(t, sm.ServerURL, map[string]interface{}{"dry_run": false})
	require.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	require.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
	require.Equal(t, 2, trig.ActiveKeyID)

	done := waitSweepSucceeded(t, sm.ServerURL, trig.TaskID, 30*time.Second)
	require.NotNil(t, done.Summary["providers"])
	require.NotNil(t, done.Summary["api_keys"])
	// seed deepseek（明文 '[]'）+ 新建 provider 均被重写；api_keys 至少 1 行
	require.GreaterOrEqual(t, done.Summary["providers"].Rewritten, int64(2))
	require.GreaterOrEqual(t, done.Summary["api_keys"].Rewritten, int64(1))

	// 直读 DB：两表全部行 enc$v1$ 且 keyID=2
	requireAllSecretsEncrypted(t, db, 2)

	// OpenAPI 透明解密读回明文一致
	assertProviderKeyPlaintext(t, providerName)
	assertAPIKeyPlaintext(t, apiKeyID, apiKeyVal)
}

// SAR-2-002 decrypt 回滚模式：密文原地解密回明文（剥 marker），
// 直读 DB 无 enc$v1$ 残留，OpenAPI 读回明文一致。
func TestSAR2_002_DecryptRollback(t *testing.T) {
	krPath := filepath.Join(t.TempDir(), "master.keys")
	writeKeyringFile(t, krPath, 1, map[int][]byte{1: newMasterKey(t)})
	sm := startSARServer(t, securitySectionTOML(krPath))

	providerName, apiKeyID, apiKeyVal := createSecretFixtures(t)

	db := openRawDB(t, sm)
	requireEnvelope(t, allProviderSecrets(t, db)[providerName], 1, "providers.api_keys(before)")
	requireEnvelope(t, apiKeySecret(t, db, apiKeyID), 1, "api_keys.api_key(before)")

	r, trig := triggerSweep(t, sm.ServerURL, map[string]interface{}{"mode": "decrypt"})
	require.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	require.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
	require.Equal(t, "decrypt", trig.Mode)
	// 注：合同称 decrypt 模式 active_key_id 返 0，实现统一返当前 active（design.md §4），此处不断言。

	waitSweepSucceeded(t, sm.ServerURL, trig.TaskID, 30*time.Second)

	// 直读 DB：两表全部行均无 enc$v1$ 前缀，且明文值与创建时一致
	requireAllSecretsPlaintext(t, db)
	provVal := allProviderSecrets(t, db)[providerName]
	require.Contains(t, provVal, providerPlainKey)
	require.Equal(t, apiKeyVal, apiKeySecret(t, db, apiKeyID))

	// OpenAPI 读回明文一致
	assertProviderKeyPlaintext(t, providerName)
	assertAPIKeyPlaintext(t, apiKeyID, apiKeyVal)
}

// SAR-2-003 sweep 接口语义：运行中重复触发 409（ErrMsg 含 holder task_id）、
// 不存在 task_id 查询 404、非法 mode/scope 触发 422。
// 小数据量任务 running 窗口仅几十毫秒，先直插 300 行明文 provider 把窗口
// 拉到百毫秒级，再循环触发直至命中 409（上限 500 次防御，见 design.md）。
func TestSAR2_003_SweepAPISemantics(t *testing.T) {
	krPath := filepath.Join(t.TempDir(), "master.keys")
	writeKeyringFile(t, krPath, 1, map[int][]byte{1: newMasterKey(t)})
	sm := startSARServer(t, securitySectionTOML(krPath))

	_, _, _ = createSecretFixtures(t)

	db := openRawDB(t, sm)
	seedBulkProviders(t, db, 300)

	// T1：scope=all 正式收敛（300+ 行明文待重写，任务持续时间最长化）
	r, t1 := triggerSweep(t, sm.ServerURL, map[string]interface{}{"scope": "all"})
	require.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	require.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
	require.NotEmpty(t, t1.TaskID)

	// 运行中重复触发：409 + ErrMsg 携带持有者 task_id
	var conflict rawResp
	hit := false
	for i := 0; i < 500; i++ {
		rr, _ := triggerSweep(t, sm.ServerURL, map[string]interface{}{"scope": "all"})
		if rr.Status == http.StatusConflict {
			conflict = rr
			hit = true
			break
		}
	}
	require.True(t, hit, "expected 409 while sweep %s is running", t1.TaskID)
	require.Equal(t, 409, conflict.ErrNum, "raw=%s", conflict.Raw)
	require.Contains(t, conflict.ErrMsg, t1.TaskID)

	// T1 终态收敛成功（幂等：完成后锁已释放，不影响后续断言）
	waitSweepSucceeded(t, sm.ServerURL, t1.TaskID, 30*time.Second)

	// 不存在的 task_id → 404
	g := doRaw(t, http.MethodGet, sm.ServerURL+sweepPath+"/rsp-no-such-task", nil)
	require.Equal(t, http.StatusNotFound, g.Status, "raw=%s", g.Raw)
	require.Equal(t, 404, g.ErrNum, "raw=%s", g.Raw)

	// 非法 mode → 422（字段级归因）
	b := doRaw(t, http.MethodPost, sm.ServerURL+sweepPath, map[string]interface{}{"mode": "rot13"})
	require.Equal(t, http.StatusUnprocessableEntity, b.Status, "raw=%s", b.Raw)
	require.Equal(t, 422, b.ErrNum, "raw=%s", b.Raw)
	require.Contains(t, b.ErrMsg, "mode")

	// 非法 scope → 422
	b = doRaw(t, http.MethodPost, sm.ServerURL+sweepPath, map[string]interface{}{"scope": "everything"})
	require.Equal(t, http.StatusUnprocessableEntity, b.Status, "raw=%s", b.Raw)
	require.Equal(t, 422, b.ErrNum, "raw=%s", b.Raw)
	require.Contains(t, b.ErrMsg, "scope")
}

// seedBulkProviders 直插 n 行明文 provider（name 唯一、api_keys 为明文 JSON），
// 用于拉长 sweep 任务 running 窗口以稳定覆盖 409 互斥语义。
func seedBulkProviders(t *testing.T, db *sql.DB, n int) {
	t.Helper()

	tx, err := db.Begin()
	require.NoError(t, err)
	stmt, err := tx.Prepare(`
		INSERT INTO providers (name, description, api_keys, instance_pool, model_protocols)
		VALUES (?, 'sar bulk row', '[{"name":"bulk","key":"bulk-key"}]', '[]', '[]')`)
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		_, err := stmt.Exec(fmt.Sprintf("sar-bulk-%s-%04d", testutil.RandomString(6), i))
		require.NoError(t, err)
	}
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Commit())
}

// SAR-4-001 MySQL 严格模式下的 Provider 写路径（issue #264）：
// providers.api_keys 承载「整列一条 enc$v1$ 信封」，列类型必须为文本类；
// 若仍为 JSON，MySQL 严格模式写入报 Error 3140、POST/PATCH 整条 500。
// 断言链：建 → 列类型 → 改（keys）→ sweep 收敛存量明文 → 读回明文。
// SQLite 后端同样执行（其 TEXT 列天然通过），MySQL 后端额外锁定列类型。
func TestSAR4_001_ProviderWritePathMySQLStrict(t *testing.T) {
	krPath := filepath.Join(t.TempDir(), "master.keys")
	writeKeyringFile(t, krPath, 1, map[int][]byte{1: newMasterKey(t)})
	sm := startSARServer(t, securitySectionTOML(krPath))

	// 1) 创建（含 keys）：修复前 MySQL 上必然 HTTP 500 + Error 3140
	providerName, _, _ := createSecretFixtures(t)

	db := openRawDB(t, sm)
	requireEnvelope(t, allProviderSecrets(t, db)[providerName], 1, "providers.api_keys(create)")

	// 2) 列类型断言（MySQL 专属）：不允许 json
	if sm.MySQLDSN() != "" {
		var dataType string
		err := db.QueryRow(`SELECT DATA_TYPE FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'providers' AND COLUMN_NAME = 'api_keys'`).
			Scan(&dataType)
		require.NoError(t, err)
		require.NotEqual(t, "json", strings.ToLower(dataType),
			"providers.api_keys must be a text type (see db_ddl_upgrade_to_0.0.11.sql §1.6)")
	}

	// 3) PATCH 携带 keys：与创建同一条 DAO 写入路径（provider.go toDAOParamForUpdate）。
	//    契约上 instance_pool / model_protocols 在 instance_pool 模式下必填，故一并携带。
	r := doRaw(t, http.MethodPatch, sm.ServerURL+"/open-api/v1/providers/"+providerName,
		map[string]interface{}{
			"keys": []interface{}{map[string]interface{}{"name": "key-rotated", "key": providerPlainKey}},
			"instance_pool": []interface{}{
				map[string]interface{}{"addr": "10.0.0.1", "weight": 100, "port": 8080},
			},
			"model_protocols": []string{"openai"},
		})
	require.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	require.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
	requireEnvelope(t, allProviderSecrets(t, db)[providerName], 1, "providers.api_keys(patch)")

	// 4) reencrypt sweep（scope=providers）：存量明文行（seed deepseek 的 '[]'）
	//    在 MySQL 上走同一条 UPDATE，修复前同样 3140 导致任务 failed
	r, trig := triggerSweep(t, sm.ServerURL, map[string]interface{}{"scope": "providers", "mode": "reencrypt"})
	require.Equal(t, http.StatusOK, r.Status, "raw=%s", r.Raw)
	require.Equal(t, 200, r.ErrNum, "raw=%s", r.Raw)
	waitSweepSucceeded(t, sm.ServerURL, trig.TaskID, 30*time.Second)
	for name, val := range allProviderSecrets(t, db) {
		requireEnvelope(t, val, 1, "providers.api_keys(sweep, "+name+")")
	}

	// 5) 读回业务明文（透明解密），报文不含信封
	assertProviderKeyPlaintext(t, providerName)
}
