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

package imods

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

func exportKeyMaterial(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func setupExportCrypto(t *testing.T, enabled bool) map[uint8][]byte {
	t.Helper()
	seed := byte(42)
	raw := exportKeyMaterial(seed)
	content := "ActiveKeyID = 1\n[Keys]\n  1 = \"" +
		base64.StdEncoding.EncodeToString(raw) + "\"\n"
	path := filepath.Join(t.TempDir(), "export.keys")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	prev := stateful.DefaultConfig
	stateful.DefaultConfig = &stateful.Config{}
	stateful.DefaultConfig.Security = stateful.SecurityConfig{
		EncryptExports: enabled,
		ExportKeyFile:  path,
	}
	require.NoError(t, stateful.LoadExportSecretRing())
	t.Cleanup(func() {
		stateful.DefaultConfig = prev
		// reset the export ring to the previous state
		if prev == nil {
			stateful.DefaultConfig = &stateful.Config{}
		}
		_ = stateful.LoadExportSecretRing()
	})
	return map[uint8][]byte{1: raw}
}

func rawDecrypt(t *testing.T, envelope string, keys map[uint8][]byte) string {
	t.Helper()
	require.True(t, xcrypto.IsEncrypted(envelope))
	raw, err := base64.StdEncoding.DecodeString(envelope[len(xcrypto.Marker):])
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), 29)
	key, ok := keys[raw[0]]
	require.True(t, ok)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	pt, err := gcm.Open(nil, raw[1:13], raw[13:], nil)
	require.NoError(t, err)
	return string(pt)
}

func oneKeyExportManager() *APIKeyRuleManager {
	apiKeyStore := &fakeAPIKeyStorager{
		fetchListFn: func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
			return []*api_key.APIKeyParam{
				{
					ID:          lib.PString("key-1"),
					Key:         lib.PString("ak-key-1"),
					ProductName: lib.PString("AI_product"),
					Enable:      lib.PBool(true),
					KeyCreateAt: lib.PTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)),
				},
			}, nil
		},
	}
	versionStore := &fakeVersionControlStorager{
		upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
			return "v-abc", nil
		},
	}
	return NewAPIKeyRuleManager(
		&fakeTxn{},
		iversion_control.NewVersionControllerManager(&fakeTxn{}, versionStore),
		apiKeyStore,
		&fakeAIRouteRuleStorager{},
		&fakeQuotaPlanStorager{},
		&fakeEntityStorager{},
		&fakeEntityTypeStorager{}, nil)
}

func TestConfigExportEncryptedTokens(t *testing.T) {
	setupState()
	keys := setupExportCrypto(t, true)
	m := oneKeyExportManager()

	conf, err := m.ConfigExport(context.Background(), "")
	require.NoError(t, err)
	require.NotNil(t, conf)

	tokens := conf.Tokens["AI_product"]
	require.Len(t, tokens, 1)

	var outer string
	for k := range tokens {
		outer = k
	}
	require.True(t, xcrypto.IsEncrypted(outer), "Tokens outer key must be enc$v1$ ciphertext")
	assert.NotContains(t, outer, "ak-key-1", "no key plaintext in the outer key")

	tok := tokens[outer]
	require.NotNil(t, tok)
	assert.Equal(t, "", tok.Key, "inner key must be cleared")
	assert.Equal(t, "key-1", tok.KeyID, "non-sensitive fields stay intact")

	// BFE can decrypt the outer key back to the plaintext api key
	assert.Equal(t, "ak-key-1", rawDecrypt(t, outer, keys))
}

func TestConfigExportPlaintextWhenSwitchOff(t *testing.T) {
	setupState()
	setupExportCrypto(t, false)
	m := oneKeyExportManager()

	conf, err := m.ConfigExport(context.Background(), "")
	require.NoError(t, err)
	require.NotNil(t, conf)

	tokens := conf.Tokens["AI_product"]
	require.Len(t, tokens, 1)
	tok, ok := tokens["ak-key-1"]
	require.True(t, ok, "switch off: outer key stays plaintext")
	assert.Equal(t, "ak-key-1", tok.Key)
}

func TestConfigExportDeterministicTokens(t *testing.T) {
	setupState()
	keys := setupExportCrypto(t, true)

	// two independent exports of the same plaintext must produce the same
	// ciphertext (deterministic nonce), so data_sign stays stable
	m1 := oneKeyExportManager()
	conf1, err := m1.ConfigExport(context.Background(), "")
	require.NoError(t, err)

	m2 := oneKeyExportManager()
	conf2, err := m2.ConfigExport(context.Background(), "")
	require.NoError(t, err)

	outer1 := onlyKey(t, conf1.Tokens["AI_product"])
	outer2 := onlyKey(t, conf2.Tokens["AI_product"])
	assert.Equal(t, outer1, outer2)
	assert.Equal(t, "ak-key-1", rawDecrypt(t, outer1, keys))
}

func onlyKey(t *testing.T, m map[string]*TokenFile) string {
	t.Helper()
	require.Len(t, m, 1)
	for k := range m {
		return k
	}
	return ""
}
