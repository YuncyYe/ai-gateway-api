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

package icluster_conf

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

func exportCryptoKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func setupClusterExportCrypto(t *testing.T, enabled bool) map[uint8][]byte {
	t.Helper()
	raw := exportCryptoKey(42)
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
		if prev == nil {
			stateful.DefaultConfig = &stateful.Config{}
		}
		_ = stateful.LoadExportSecretRing()
	})
	return map[uint8][]byte{1: raw}
}

func llmProviderKeyTable() map[string][]iprovider.ProviderKey {
	return map[string][]iprovider.ProviderKey{
		"openai": {
			{Name: "key-primary", Key: "sk-aaaaaaaaaaaa"},
			{Name: "key-secondary", Key: "sk-bbbbbbbbbbbb"},
		},
	}
}

func rawKeyDecrypt(t *testing.T, envelope string, keys map[uint8][]byte) string {
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

func TestNewBfeClusterConfEncryptedKeys(t *testing.T) {
	keys := setupClusterExportCrypto(t, true)

	conf, err := NewBfeClusterConf(context.Background(), "v1", []*Cluster{newTestClusterLLM()},
		nil, llmProviderKeyTable(), map[string][]string{"openai": {"openai"}},
		nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, conf)

	cConf := (*conf.Config)["c1"]
	require.NotNil(t, cConf.AIConf)
	require.Len(t, cConf.AIConf.Keys, 2)

	for i, want := range []string{"sk-aaaaaaaaaaaa", "sk-bbbbbbbbbbbb"} {
		k := cConf.AIConf.Keys[i]
		require.True(t, xcrypto.IsEncrypted(k.Key), "Keys[%d].Key must be ciphertext", i)
		assert.NotContains(t, k.Key, want)
		assert.Equal(t, want, rawKeyDecrypt(t, k.Key, keys),
			"BFE must decrypt Keys[%d].Key back to the upstream key", i)
	}
	// non-sensitive fields stay intact
	assert.Equal(t, "key-primary", cConf.AIConf.Keys[0].Name)
	assert.Equal(t, 70, cConf.AIConf.Keys[0].Weight)
	assert.Equal(t, "key-secondary", cConf.AIConf.Keys[1].Name)
	assert.Equal(t, 30, cConf.AIConf.Keys[1].Weight)
	require.NotNil(t, cConf.AIConf.KeyPolicy)
}

func TestNewBfeClusterConfPlaintextKeysWhenSwitchOff(t *testing.T) {
	setupClusterExportCrypto(t, false)

	conf, err := NewBfeClusterConf(context.Background(), "v1", []*Cluster{newTestClusterLLM()},
		nil, llmProviderKeyTable(), map[string][]string{"openai": {"openai"}},
		nil, nil, nil)
	require.NoError(t, err)

	cConf := (*conf.Config)["c1"]
	require.NotNil(t, cConf.AIConf)
	assert.Equal(t, "sk-aaaaaaaaaaaa", cConf.AIConf.Keys[0].Key)
	assert.Equal(t, "sk-bbbbbbbbbbbb", cConf.AIConf.Keys[1].Key)
}
