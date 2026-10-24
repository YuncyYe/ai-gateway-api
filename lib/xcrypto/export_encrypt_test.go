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

package xcrypto

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
)

// rawTestKey builds deterministic 32-byte test key material.
func rawTestKey(seed byte) []byte {
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

// writeTestKeyring writes a TOML keyring file with the given active ID.
func writeTestKeyring(t *testing.T, active int, seeds ...byte) string {
	t.Helper()
	content := "ActiveKeyID = " + strconv.Itoa(active) + "\n[Keys]\n"
	for i, seed := range seeds {
		content += "  " + strconv.Itoa(i+1) + " = \"" +
			base64.StdEncoding.EncodeToString(rawTestKey(seed)) + "\"\n"
	}
	path := filepath.Join(t.TempDir(), "export.keys")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// decryptLikeBFE replicates the BFE data plane's bfe_util/crypto.Decrypt:
// the RAW keyring key (no HKDF derivation), envelope layout
// keyID(1B)|nonce(12B)|ct+tag. It is the cross-repo compatibility vector:
// whatever this function opens, BFE can open.
func decryptLikeBFE(t *testing.T, envelope string, keys map[uint8][]byte) string {
	t.Helper()
	require.True(t, IsEncrypted(envelope))
	raw, err := base64.StdEncoding.DecodeString(envelope[len(Marker):])
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), keyIDLen+nonceLen+16)

	keyID := raw[0]
	key, ok := keys[keyID]
	require.True(t, ok, "no key for keyID %d", keyID)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	pt, err := gcm.Open(nil, raw[keyIDLen:keyIDLen+nonceLen], raw[keyIDLen+nonceLen:], nil)
	require.NoError(t, err)
	return string(pt)
}

func envelopeKeyIDByte(t *testing.T, envelope string) byte {
	t.Helper()
	require.True(t, IsEncrypted(envelope))
	raw, err := base64.StdEncoding.DecodeString(envelope[len(Marker):])
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	return raw[0]
}

func TestEncryptDeterministic(t *testing.T) {
	key := rawTestKey(1)

	e1, err := EncryptDeterministic("sk-det", key, 1)
	require.NoError(t, err)
	e2, err := EncryptDeterministic("sk-det", key, 1)
	require.NoError(t, err)
	assert.Equal(t, e1, e2, "same (keyID, plaintext) must produce identical ciphertext")

	e3, err := EncryptDeterministic("sk-other", key, 1)
	require.NoError(t, err)
	assert.NotEqual(t, e1, e3)

	// BFE layout cross-vector: the data plane must be able to open it
	assert.Equal(t, "sk-det", decryptLikeBFE(t, e1, map[uint8][]byte{1: key}))
}

func TestEncryptWithRawKey(t *testing.T) {
	path := writeTestKeyring(t, 2, 11, 22)
	kr, err := LoadKeyringFile(path)
	require.NoError(t, err)

	raw1, raw2 := rawTestKey(11), rawTestKey(22)

	enc, err := kr.EncryptWithRawKey("sk-raw", 2)
	require.NoError(t, err)
	assert.Equal(t, byte(2), envelopeKeyIDByte(t, enc))
	assert.Equal(t, "sk-raw", decryptLikeBFE(t, enc, map[uint8][]byte{2: raw2}))

	// keyID 0 follows the keyring's active ID
	enc0, err := kr.EncryptWithRawKey("sk-raw0", 0)
	require.NoError(t, err)
	assert.Equal(t, byte(2), envelopeKeyIDByte(t, enc0))
	assert.Equal(t, "sk-raw0", decryptLikeBFE(t, enc0, map[uint8][]byte{2: raw2}))

	// non-active explicit keyID works too
	enc1, err := kr.EncryptWithRawKey("sk-raw1", 1)
	require.NoError(t, err)
	assert.Equal(t, byte(1), envelopeKeyIDByte(t, enc1))
	assert.Equal(t, "sk-raw1", decryptLikeBFE(t, enc1, map[uint8][]byte{1: raw1}))

	// unknown keyID
	_, err = kr.EncryptWithRawKey("sk-x", 9)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestEncryptWithRawKeyNotDerived locks the cross-repo contract: export
// ciphertext must open with the RAW keyring key and must NOT open with the
// HKDF-derived encKeys used for DB at-rest encryption (BFE has no derivation).
func TestEncryptWithRawKeyNotDerived(t *testing.T) {
	path := writeTestKeyring(t, 1, 42)
	kr, err := LoadKeyringFile(path)
	require.NoError(t, err)

	raw := rawTestKey(42)
	derived := encKeyForTest(t, raw)

	enc, err := kr.EncryptWithRawKey("sk-cross", 1)
	require.NoError(t, err)

	// raw key opens it (this is exactly what bfe_util/crypto does)
	pt, err := Decrypt(enc, map[uint8][]byte{1: raw})
	require.NoError(t, err)
	assert.Equal(t, "sk-cross", pt)

	// the derived encKey must NOT open it (GCM authentication failure)
	_, err = Decrypt(enc, map[uint8][]byte{1: derived})
	assert.Error(t, err)
}

// encKeyForTest derives the db-enc-key HKDF output, matching xcrypto's
// derivation in LoadKeyringFile.
func encKeyForTest(t *testing.T, master []byte) []byte {
	t.Helper()
	derived, err := hkdfBytes(master, "db-enc-key")
	require.NoError(t, err)
	return derived
}
