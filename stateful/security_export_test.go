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

package stateful

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
)

func exportTestKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func writeExportTestKeyring(t *testing.T, active int, seeds ...byte) string {
	t.Helper()
	content := "ActiveKeyID = " + strconv.Itoa(active) + "\n[Keys]\n"
	for i, seed := range seeds {
		content += "  " + strconv.Itoa(i+1) + " = \"" +
			base64.StdEncoding.EncodeToString(exportTestKey(seed)) + "\"\n"
	}
	path := filepath.Join(t.TempDir(), "export.keys")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// rawDecryptLikeBFE opens an export envelope with the raw keyring key,
// replicating the BFE data plane (bfe_util/crypto: no HKDF derivation).
func rawDecryptLikeBFE(t *testing.T, envelope string, keys map[uint8][]byte) string {
	t.Helper()
	require.True(t, xcrypto.IsEncrypted(envelope))
	raw, err := base64.StdEncoding.DecodeString(envelope[len(xcrypto.Marker):])
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), 29)

	keyID := raw[0]
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

// setupExportSecurity installs a SecurityConfig into DefaultConfig and loads
// the export ring; cleanup restores the previous DefaultConfig.
func setupExportSecurity(t *testing.T, sec SecurityConfig) {
	t.Helper()
	prev := DefaultConfig
	DefaultConfig = &Config{}
	DefaultConfig.Security = sec
	t.Cleanup(func() {
		DefaultConfig = prev
	})
}

func TestLoadExportSecretRingMatrix(t *testing.T) {
	t.Run("switch on without keyring file", func(t *testing.T) {
		setupExportSecurity(t, SecurityConfig{EncryptExports: true})
		err := LoadExportSecretRing()
		assert.Error(t, err)
		assert.False(t, ExportCryptoEnabled())
	})

	t.Run("switch on with malformed keyring", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.keys")
		require.NoError(t, os.WriteFile(bad, []byte("not a keyring"), 0o600))
		setupExportSecurity(t, SecurityConfig{EncryptExports: true, ExportKeyFile: bad})
		assert.Error(t, LoadExportSecretRing())
		assert.False(t, ExportCryptoEnabled())
	})

	t.Run("switch on with unknown active export keyID", func(t *testing.T) {
		path := writeExportTestKeyring(t, 1, 7)
		setupExportSecurity(t, SecurityConfig{
			EncryptExports: true, ExportKeyFile: path, ActiveExportKeyID: 99,
		})
		assert.Error(t, LoadExportSecretRing())
		assert.False(t, ExportCryptoEnabled())
	})

	t.Run("switch on valid", func(t *testing.T) {
		path := writeExportTestKeyring(t, 1, 7)
		setupExportSecurity(t, SecurityConfig{EncryptExports: true, ExportKeyFile: path})
		require.NoError(t, LoadExportSecretRing())
		assert.True(t, ExportCryptoEnabled())

		enc, err := ExportEncrypt("sk-ctl-plain")
		require.NoError(t, err)
		// cross-vector: the BFE data plane can open it with the raw key
		assert.Equal(t, "sk-ctl-plain", rawDecryptLikeBFE(t, enc, map[uint8][]byte{1: exportTestKey(7)}))
	})

	t.Run("switch on explicit active export keyID", func(t *testing.T) {
		path := writeExportTestKeyring(t, 2, 7, 8)
		setupExportSecurity(t, SecurityConfig{
			EncryptExports: true, ExportKeyFile: path, ActiveExportKeyID: 2,
		})
		require.NoError(t, LoadExportSecretRing())
		enc, err := ExportEncrypt("sk-key2")
		require.NoError(t, err)
		raw, err := base64.StdEncoding.DecodeString(enc[len(xcrypto.Marker):])
		require.NoError(t, err)
		require.NotEmpty(t, raw)
		assert.Equal(t, byte(2), raw[0], "envelope keyID must be ActiveExportKeyID")
	})

	t.Run("switch off with valid keyring stays disabled", func(t *testing.T) {
		path := writeExportTestKeyring(t, 1, 7)
		setupExportSecurity(t, SecurityConfig{EncryptExports: false, ExportKeyFile: path})
		require.NoError(t, LoadExportSecretRing())
		assert.False(t, ExportCryptoEnabled(), "switch off must disable export encryption even with a usable keyring")
		_, err := ExportEncrypt("sk-x")
		assert.Error(t, err)
	})

	t.Run("switch off with malformed keyring only warns", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.keys")
		require.NoError(t, os.WriteFile(bad, []byte("junk"), 0o600))
		setupExportSecurity(t, SecurityConfig{EncryptExports: false, ExportKeyFile: bad})
		assert.NoError(t, LoadExportSecretRing())
		assert.False(t, ExportCryptoEnabled())
	})
}

func TestReloadSecurityPartialFailure(t *testing.T) {
	masterPath := filepath.Join(t.TempDir(), "master.keys")
	require.NoError(t, os.WriteFile(masterPath, []byte(
		"ActiveKeyID = 1\n[Keys]\n  1 = \""+
			base64.StdEncoding.EncodeToString(exportTestKey(3))+"\"\n"), 0o600))
	exportPath := writeExportTestKeyring(t, 1, 7)

	setupExportSecurity(t, SecurityConfig{
		MasterKeyFile: masterPath, ActiveKeyID: 1,
		EncryptExports: true, ExportKeyFile: exportPath,
	})
	require.NoError(t, LoadSecretRing())
	require.NoError(t, LoadExportSecretRing())
	require.True(t, ExportCryptoEnabled())

	// rotate step: export keyring file is replaced by garbage before reload
	require.NoError(t, os.WriteFile(exportPath, []byte("corrupted"), 0o600))

	msg, err := ReloadSecurity(nil)
	assert.Error(t, err, "reload must report the export keyring failure")
	assert.NotEmpty(t, msg)

	// the previous export keyring is kept: encryption still works with the old key
	assert.True(t, ExportCryptoEnabled())
	enc, encErr := ExportEncrypt("sk-still-works")
	require.NoError(t, encErr)
	assert.Equal(t, "sk-still-works",
		rawDecryptLikeBFE(t, enc, map[uint8][]byte{1: exportTestKey(7)}))

	// the master keyring reloaded fine
	require.NotNil(t, SecretRing())
}
