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
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T, seed byte) []byte {
	t.Helper()
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t, 1)
	ct, err := Encrypt("sk-provider-secret", key, 2)
	require.NoError(t, err)
	assert.True(t, IsEncrypted(ct))

	pt, err := Decrypt(ct, map[uint8][]byte{2: key})
	require.NoError(t, err)
	assert.Equal(t, "sk-provider-secret", pt)
}

func TestEncryptProducesDistinctCiphertexts(t *testing.T) {
	key := testKey(t, 1)
	a, _ := Encrypt("same-plaintext", key, 1)
	b, _ := Encrypt("same-plaintext", key, 1)
	assert.NotEqual(t, a, b, "random nonce must make ciphertexts distinct")
}

func TestDecryptPlaintextPassthroughNotEncrypted(t *testing.T) {
	_, err := Decrypt("plain-value", map[uint8][]byte{1: testKey(t, 1)})
	assert.ErrorIs(t, err, ErrNotEncrypted)
}

func TestDecryptUnknownKeyID(t *testing.T) {
	ct, _ := Encrypt("x", testKey(t, 9), 7)
	_, err := Decrypt(ct, map[uint8][]byte{1: testKey(t, 9)})
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := testKey(t, 1)
	ct, _ := Encrypt("secret", key, 1)
	raw, _ := base64.StdEncoding.DecodeString(ct[len(Marker):])
	raw[len(raw)-1] ^= 0xff
	tampered := Marker + base64.StdEncoding.EncodeToString(raw)
	_, err := Decrypt(tampered, map[uint8][]byte{1: key})
	assert.Error(t, err)
}

func TestKeyIDOf(t *testing.T) {
	ct, _ := Encrypt("x", testKey(t, 1), 42)
	id, ok := KeyIDOf(ct)
	require.True(t, ok)
	assert.Equal(t, uint8(42), id)

	_, ok = KeyIDOf("plain")
	assert.False(t, ok)
	_, ok = KeyIDOf(Marker + "!!!not-base64")
	assert.False(t, ok)
}

func writeKeyringFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "master.keys")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadKeyringFile(t *testing.T) {
	k1 := base64.StdEncoding.EncodeToString(testKey(t, 1))
	k2 := base64.StdEncoding.EncodeToString(testKey(t, 100))
	path := writeKeyringFile(t, `
ActiveKeyID = 2
[Keys]
  1 = "`+k1+`"
  2 = "`+k2+`"
`)
	kr, err := LoadKeyringFile(path)
	require.NoError(t, err)
	assert.Equal(t, uint8(2), kr.ActiveID())
	assert.True(t, kr.HasKey(1))
	assert.True(t, kr.HasKey(2))

	// encrypt uses active; decrypt resolves per-row keyID
	ct, err := kr.Encrypt("hello")
	require.NoError(t, err)
	id, ok := KeyIDOf(ct)
	require.True(t, ok)
	assert.Equal(t, uint8(2), id)
	pt, err := kr.Decrypt(ct)
	require.NoError(t, err)
	assert.Equal(t, "hello", pt)

	// plaintext passthrough
	pt, err = kr.Decrypt("legacy-plain")
	require.NoError(t, err)
	assert.Equal(t, "legacy-plain", pt)
}

func TestLoadKeyringFileActiveNotFound(t *testing.T) {
	k1 := base64.StdEncoding.EncodeToString(testKey(t, 1))
	path := writeKeyringFile(t, `
ActiveKeyID = 9
[Keys]
  1 = "`+k1+`"
`)
	_, err := LoadKeyringFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestLoadKeyringFileBadKeyLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	path := writeKeyringFile(t, `
ActiveKeyID = 1
[Keys]
  1 = "`+short+`"
`)
	_, err := LoadKeyringFile(path)
	require.Error(t, err)
}

func TestKeyHashStableAndDistinct(t *testing.T) {
	k1 := base64.StdEncoding.EncodeToString(testKey(t, 1))
	path := writeKeyringFile(t, `
ActiveKeyID = 1
[Keys]
  1 = "`+k1+`"
`)
	h1 := KeyHash("sk-abc")
	h2 := KeyHash("sk-abc")
	h3 := KeyHash("sk-xyz")
	assert.Equal(t, h1, h2, "same plaintext => same hash")
	assert.NotEqual(t, h1, h3)
	assert.Len(t, h1, 64)

	// hash is independent of the keyring: rotation must not change it
	kr, err := LoadKeyringFile(path)
	require.NoError(t, err)
	assert.Equal(t, h1, kr.KeyHash("sk-abc"))
}

func TestParseKeyID(t *testing.T) {
	id, err := ParseKeyID("255")
	require.NoError(t, err)
	assert.Equal(t, uint8(255), id)

	for _, bad := range []string{"0", "256", "-1", "abc", ""} {
		_, err := ParseKeyID(bad)
		assert.Error(t, err, "input=%q", bad)
	}
}

func TestEnvelopeVersionMarkerShape(t *testing.T) {
	assert.True(t, strings.HasPrefix("enc$v1$xxx", Marker))
	assert.Equal(t, 1, EnvelopeVersion())
}
