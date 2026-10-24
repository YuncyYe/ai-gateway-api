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

// 下发配置文件敏感字段加密（导出口加密）集成测试（模块前缀 EFE，场景登记见
// 同目录 design.md）。被测对象为两个 InnerAPI 导出 topic 的敏感字段加密
// （/configs/mod-api-key 的 Tokens 外层键、/configs/tls_conf/server_data_conf 的
// AIConf.Keys[].Key）；核心断言手段是导出报文：按 api-changes.md 合同断言
// enc$v1$ 信封形态、非敏感字段原样、响应全文不含密钥明文，并以数据面视角
// （原始 keyring 密钥 + 信封布局手工 GCM 解包，与 bfe_util/crypto 同算法）验证
// BFE 可解开控制面产出。各用例独立 StartServerWithMonitor 实例注入 [Security]
// 段（keyring 文件写在 t.TempDir()），用例内步骤串行。
package export_encryption_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
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

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pathModAPIKey    = "/inner-api/v1/configs/mod-api-key"
	pathServerData   = "/inner-api/v1/configs/tls_conf/server_data_conf"
	pathClusterTable = "/inner-api/v1/configs/gslb_data/cluster_table"
	pathAiRoute      = "/inner-api/v1/configs/ai-route"

	// provider 固定明文密钥（testutil.CreateProvider 注入），用于明文/密文对照断言
	providerKeyA = "sk-aaaaaaaaaaaa"
	providerKeyB = "sk-bbbbbbbbbbbb"
)

type rawResp struct {
	Status int
	ErrNum int
	ErrMsg string
	Raw    []byte
}

// newRawKey 生成 32 字节随机密钥。
func newRawKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

// writeKeyring 按 keyring TOML 格式覆写 path。
func writeKeyring(t *testing.T, path string, activeID int, keys map[int][]byte) {
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

// exportSecurityTOML 生成 [Security] 导出加密段（路径统一正斜杠）。
func exportSecurityTOML(keyFile string) string {
	return fmt.Sprintf("[Security]\nEncryptExports = true\nExportKeyFile = \"%s\"\n",
		filetip(keyFile))
}

func filetip(p string) string { return filepath.ToSlash(p) }

// startServer 启动带 monitor 端口的独立实例。
func startServer(t *testing.T, extraTOML string) *testutil.ServerManager {
	t.Helper()
	sm, err := testutil.StartServerWithMonitor(extraTOML)
	require.NoError(t, err)
	t.Cleanup(sm.Shutdown)
	testutil.SetServerURL(sm.ServerURL)
	return sm
}

// startServerExpectFail 启动必须失败的场景（fail-fast），返回启动错误。
func startServerExpectFail(t *testing.T, extraTOML string) {
	t.Helper()
	sm, err := testutil.StartServerWithMonitor(extraTOML)
	if err == nil {
		sm.Shutdown()
		t.Fatal("server should refuse to start (fail-fast), but started")
	}
	t.Logf("server refused to start as expected: %v", err)
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

// decryptLikeBFE 以数据面视角解包导出密文：原始 keyring 密钥（无 HKDF 派生）+
// 信封布局 keyID(1B)|nonce(12B)|ct+tag，与 bfe_util/crypto.Decrypt 同算法。
func decryptLikeBFE(t *testing.T, envelope string, keys map[int][]byte) string {
	t.Helper()
	require.True(t, xcrypto.IsEncrypted(envelope))
	raw, err := base64.StdEncoding.DecodeString(envelope[len(xcrypto.Marker):])
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), 29)

	keyID := int(raw[0])
	key, ok := keys[keyID]
	require.True(t, ok, "no key for keyID %d", keyID)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	pt, err := gcm.Open(nil, raw[1:13], raw[13:], nil)
	require.NoError(t, err)
	return string(pt)
}

func envelopeKeyID(t *testing.T, envelope string) int {
	t.Helper()
	require.True(t, xcrypto.IsEncrypted(envelope))
	raw, err := base64.StdEncoding.DecodeString(envelope[len(xcrypto.Marker):])
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	return int(raw[0])
}

// createFixtures 建 provider（固定明文双 key）+ cluster（llm_config 引用双 key）
// + api-key，返回 (providerName, clusterName, apiKeyID, apiKeyVal)。
func createFixtures(t *testing.T) (string, string, string, string) {
	t.Helper()

	providerName, err := testutil.CreateProvider(testutil.UniqueProviderName())
	require.NoError(t, err)

	clusterName := testutil.UniqueClusterName()
	resp, err := testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
		"name": clusterName,
		"llm_config": map[string]interface{}{
			"models":   []string{"deepseek-chat"},
			"provider": providerName,
			"keys": []interface{}{
				map[string]interface{}{"name": "key-primary", "weight": 70},
				map[string]interface{}{"name": "key-secondary", "weight": 30},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 200, resp.ErrNum, "create cluster: %s", resp.ErrMsg)

	apiKeyID, apiKeyVal, err := testutil.CreateAPIKeyWithKey(testutil.UniqueAPIKeyDesc(), "")
	require.NoError(t, err)
	return providerName, clusterName, apiKeyID, apiKeyVal
}

// exportTokens 拉取 mod-api-key 导出并返回 tokens 段原始 JSON 文本与解析结果。
func exportTokens(t *testing.T, baseURL string) (string, map[string]interface{}) {
	t.Helper()
	r := doRaw(t, http.MethodGet, baseURL+pathModAPIKey, nil)
	require.Equal(t, http.StatusOK, r.Status)
	require.Equal(t, 200, r.ErrNum, r.ErrMsg)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(r.Raw, &data))
	tokens, ok := data["Data"].(map[string]interface{})["tokens"].(map[string]interface{})
	require.True(t, ok, "tokens missing in export")
	return string(r.Raw), tokens
}

// exportServerData 拉取 server_data_conf 导出原始 JSON 文本。
func exportServerData(t *testing.T, baseURL string) (string, map[string]interface{}) {
	t.Helper()
	r := doRaw(t, http.MethodGet, baseURL+pathServerData, nil)
	require.Equal(t, http.StatusOK, r.Status)
	require.Equal(t, 200, r.ErrNum, r.ErrMsg)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(r.Raw, &data))
	exported, ok := data["Data"].(map[string]interface{})
	require.True(t, ok, "Data missing in server_data_conf export")
	return string(r.Raw), exported
}

// clusterAIConfKeys 从 server_data_conf 导出中取指定 cluster 的 AIConf.Keys
// （必须按 cluster 名精确定位：导出可能含其他带空 Keys 的 AIConf cluster）。
func clusterAIConfKeys(t *testing.T, exported map[string]interface{}, clusterName string) []interface{} {
	t.Helper()
	conf, ok := exported["ClusterConf"].(map[string]interface{})
	require.True(t, ok, "ClusterConf missing")
	config, ok := conf["Config"].(map[string]interface{})
	require.True(t, ok, "ClusterConf.Config missing")
	clusterConf, ok := config[clusterName].(map[string]interface{})
	require.True(t, ok, "cluster %s missing in ClusterConf.Config", clusterName)
	aiConf, ok := clusterConf["AIConf"].(map[string]interface{})
	require.True(t, ok, "AIConf missing in cluster %s", clusterName)
	keys, ok := aiConf["Keys"].([]interface{})
	require.True(t, ok, "AIConf.Keys missing")
	return keys
}

// onlyTokenEntry 取 tokens.<product> 唯一条目（外层键, 内层对象）。
func onlyTokenEntry(t *testing.T, tokens map[string]interface{}, product string) (string, map[string]interface{}) {
	t.Helper()
	productTokens, ok := tokens[product].(map[string]interface{})
	require.True(t, ok, "tokens.%s missing", product)
	require.Len(t, productTokens, 1, "expect exactly one token in this scenario")
	for k, v := range productTokens {
		entry, ok := v.(map[string]interface{})
		require.True(t, ok)
		return k, entry
	}
	return "", nil
}

// EFE-1-001 开关关闭：两 topic 明文导出，全文无 enc$v1$。
func TestEFE1_001_PlaintextBaseline(t *testing.T) {
	sm := startServer(t, "")
	_, clusterName, apiKeyID, apiKeyVal := createFixtures(t)

	body, tokens := exportTokens(t, sm.ServerURL)
	assert.NotContains(t, body, xcrypto.Marker)

	outer, entry := onlyTokenEntry(t, tokens, "AI_product")
	assert.Equal(t, apiKeyVal, outer, "plaintext export: outer key is the api key")
	assert.Equal(t, apiKeyVal, entry["key"])
	assert.Equal(t, apiKeyID, entry["key_id"])

	sdBody, exported := exportServerData(t, sm.ServerURL)
	assert.NotContains(t, sdBody, xcrypto.Marker)
	keys := clusterAIConfKeys(t, exported, clusterName)
	require.Len(t, keys, 2)
	assert.Equal(t, providerKeyA, keys[0].(map[string]interface{})["Key"])
	assert.Equal(t, providerKeyB, keys[1].(map[string]interface{})["Key"])
}

// EFE-1-002 开关开启：敏感字段 enc$v1$ 密文、非敏感原样、无明文泄漏、BFE 可解密。
func TestEFE1_002_EncryptedExport(t *testing.T) {
	key1 := newRawKey(t)
	keyFile := filepath.Join(t.TempDir(), "export.keys")
	writeKeyring(t, keyFile, 1, map[int][]byte{1: key1})
	sm := startServer(t, exportSecurityTOML(keyFile))

	_, clusterName, apiKeyID, apiKeyVal := createFixtures(t)

	body, tokens := exportTokens(t, sm.ServerURL)
	outer, entry := onlyTokenEntry(t, tokens, "AI_product")

	require.True(t, xcrypto.IsEncrypted(outer), "outer key must be enc$v1$ ciphertext")
	assert.Equal(t, 1, envelopeKeyID(t, outer))
	assert.NotContains(t, body, apiKeyVal, "no api key plaintext in mod-api-key body")
	assert.Equal(t, "", entry["key"], "inner key cleared")
	assert.Equal(t, apiKeyID, entry["key_id"], "non-sensitive fields intact")
	assert.Contains(t, entry, "enabled")

	// BFE data plane can decrypt the outer key back to the api key
	assert.Equal(t, apiKeyVal, decryptLikeBFE(t, outer, map[int][]byte{1: key1}))

	sdBody, exported := exportServerData(t, sm.ServerURL)
	assert.NotContains(t, sdBody, providerKeyA)
	assert.NotContains(t, sdBody, providerKeyB)

	keys := clusterAIConfKeys(t, exported, clusterName)
	require.Len(t, keys, 2)
	k0 := keys[0].(map[string]interface{})
	require.True(t, xcrypto.IsEncrypted(k0["Key"].(string)))
	assert.Equal(t, 1, envelopeKeyID(t, k0["Key"].(string)))
	assert.Equal(t, "key-primary", k0["Name"], "non-sensitive Name intact")
	assert.Equal(t, float64(70), k0["Weight"])
	assert.Equal(t, providerKeyA, decryptLikeBFE(t, k0["Key"].(string), map[int][]byte{1: key1}))

	k1 := keys[1].(map[string]interface{})
	assert.Equal(t, providerKeyB, decryptLikeBFE(t, k1["Key"].(string), map[int][]byte{1: key1}))
}

// EFE-1-003 确定性密文：同版本增量返回 Data null；重复导出密文字节一致（data_sign 稳定）。
func TestEFE1_003_DeterministicCiphertext(t *testing.T) {
	key1 := newRawKey(t)
	keyFile := filepath.Join(t.TempDir(), "export.keys")
	writeKeyring(t, keyFile, 1, map[int][]byte{1: key1})
	sm := startServer(t, exportSecurityTOML(keyFile))

	createFixtures(t)

	_, tokens1 := exportTokens(t, sm.ServerURL)
	outer1, _ := onlyTokenEntry(t, tokens1, "AI_product")

	var data1 map[string]interface{}
	r1 := doRaw(t, http.MethodGet, sm.ServerURL+pathModAPIKey, nil)
	require.NoError(t, json.Unmarshal(r1.Raw, &data1))
	version := data1["Data"].(map[string]interface{})["version"].(string)
	require.NotEmpty(t, version)

	// unchanged config: incremental pull with the same version returns Data null
	r2 := doRaw(t, http.MethodGet, sm.ServerURL+pathModAPIKey+"?version="+version, nil)
	require.Equal(t, http.StatusOK, r2.Status)
	var data2 map[string]interface{}
	require.NoError(t, json.Unmarshal(r2.Raw, &data2))
	assert.Nil(t, data2["Data"], "same version with unchanged config must return Data null (data_sign stable)")

	// full pull again: deterministic ciphertext, byte-identical
	_, tokens3 := exportTokens(t, sm.ServerURL)
	outer3, _ := onlyTokenEntry(t, tokens3, "AI_product")
	assert.Equal(t, outer1, outer3, "deterministic export ciphertext must be byte-identical")
}

// EFE-1-004 门控：开关开启时无关导出 topic 不含密文。
func TestEFE1_004_GateOtherTopicsPlain(t *testing.T) {
	key1 := newRawKey(t)
	keyFile := filepath.Join(t.TempDir(), "export.keys")
	writeKeyring(t, keyFile, 1, map[int][]byte{1: key1})
	sm := startServer(t, exportSecurityTOML(keyFile))

	createFixtures(t)

	for _, p := range []string{pathClusterTable, pathAiRoute} {
		r := doRaw(t, http.MethodGet, sm.ServerURL+p, nil)
		require.Equal(t, http.StatusOK, r.Status, p)
		assert.NotContains(t, string(r.Raw), xcrypto.Marker, "%s must not contain ciphertext", p)
	}
}

// EFE-2-001 开关开但 ExportKeyFile 未配置：拒绝启动。
func TestEFE2_001_RefuseStartWithoutKeyFile(t *testing.T) {
	startServerExpectFail(t, "[Security]\nEncryptExports = true\n")
}

// EFE-2-002 开关开但 keyring 文件损坏：拒绝启动。
func TestEFE2_002_RefuseStartWithCorruptKeyring(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "export.keys")
	require.NoError(t, os.WriteFile(keyFile, []byte("corrupted"), 0o600))
	startServerExpectFail(t, exportSecurityTOML(keyFile))
}

// EFE-2-003 开关开但 ActiveExportKeyID 不在 [Keys]：拒绝启动。
func TestEFE2_003_RefuseStartWithUnknownActiveKeyID(t *testing.T) {
	key1 := newRawKey(t)
	keyFile := filepath.Join(t.TempDir(), "export.keys")
	writeKeyring(t, keyFile, 1, map[int][]byte{1: key1})
	startServerExpectFail(t, exportSecurityTOML(keyFile)+"\nActiveExportKeyID = 99\n")
}

// EFE-2-004 开关关但 keyring 文件损坏：仅告警，启动成功且明文导出。
func TestEFE2_004_CorruptKeyringWarnsWhenSwitchOff(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "export.keys")
	require.NoError(t, os.WriteFile(keyFile, []byte("corrupted"), 0o600))
	sm := startServer(t, fmt.Sprintf("[Security]\nEncryptExports = false\nExportKeyFile = \"%s\"\n", filetip(keyFile)))

	_, _, _, apiKeyVal := createFixtures(t)

	body, tokens := exportTokens(t, sm.ServerURL)
	assert.NotContains(t, body, xcrypto.Marker)
	outer, _ := onlyTokenEntry(t, tokens, "AI_product")
	assert.Equal(t, apiKeyVal, outer)
}

// EFE-3-001 热加载轮换：覆写 keyring + /reload/security + 配置变更触发新导出，
// 新密文 keyID=2，旧 keyID=1 密文仍可解（双钥并存）。
func TestEFE3_001_ReloadRotation(t *testing.T) {
	key1 := newRawKey(t)
	keyFile := filepath.Join(t.TempDir(), "export.keys")
	writeKeyring(t, keyFile, 1, map[int][]byte{1: key1})
	sm := startServer(t, exportSecurityTOML(keyFile))

	_, _, apiKeyID, apiKeyVal := createFixtures(t)

	_, tokensV1 := exportTokens(t, sm.ServerURL)
	outerV1, _ := onlyTokenEntry(t, tokensV1, "AI_product")
	require.Equal(t, 1, envelopeKeyID(t, outerV1))

	// rotation: keyring gains key 2 with active=2
	key2 := newRawKey(t)
	writeKeyring(t, keyFile, 2, map[int][]byte{1: key1, 2: key2})

	r := doRaw(t, http.MethodPost, sm.MonitorURL+"/reload/security", nil)
	require.Equal(t, http.StatusOK, r.Status, "reload security: %s", string(r.Raw))
	assert.Contains(t, string(r.Raw), "export keys 1 -> 2")

	// a config change is required to bump the export version: deterministic
	// ciphertext means a pure key rotation produces the same sign
	resp, err := testutil.GetClient().Patch("/open-api/v1/api-keys/"+apiKeyID,
		map[string]interface{}{"allow_models": "deepseek-chat"})
	require.NoError(t, err)
	require.Equal(t, 200, resp.ErrNum, "patch api-key: %s", resp.ErrMsg)

	_, tokensV2 := exportTokens(t, sm.ServerURL)
	outerV2, _ := onlyTokenEntry(t, tokensV2, "AI_product")
	require.Equal(t, 2, envelopeKeyID(t, outerV2), "new export ciphertext must use the new active keyID")
	assert.Equal(t, apiKeyVal, decryptLikeBFE(t, outerV2, map[int][]byte{2: key2}))

	// legacy ciphertext from before the rotation is still readable (coexistence)
	assert.Equal(t, apiKeyVal, decryptLikeBFE(t, outerV1, map[int][]byte{1: key1}))
}
