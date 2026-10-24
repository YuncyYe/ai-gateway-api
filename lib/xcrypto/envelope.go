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

// Package xcrypto provides AES-256-GCM envelope encryption for secrets at
// rest (provider keys / api keys in DB, export fields). The envelope is
// self-describing: enc$v1$<base64(keyID|nonce|ciphertext)>. Decryption picks
// the key by the keyID embedded in each ciphertext, so rows with different
// keyIDs coexist without any external mapping. This is an anti-leakage
// mechanism: it protects storage media (DB files/backups), not tampering.
package xcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Marker prefixes every ciphertext; values without it are treated as
	// legacy plaintext and passed through on read.
	Marker    = "enc$v1$"
	keyIDLen  = 1
	nonceLen  = 12
	keyLen    = 32
	versionV1 = 1
)

var (
	ErrNotEncrypted    = errors.New("value is not an encrypted envelope")
	ErrInvalidEnvelope = errors.New("invalid encrypted envelope")
	ErrKeyNotFound     = errors.New("key not found in keyring")
)

// IsEncrypted reports whether value carries the enc$v1$ marker.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, Marker)
}

// Encrypt encrypts plaintext with key (32 bytes) and returns the envelope
// string tagged with keyID.
func Encrypt(plaintext string, key []byte, keyID uint8) (string, error) {
	if len(key) != keyLen {
		return "", fmt.Errorf("xcrypto: key length %d, want %d", len(key), keyLen)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	raw := make([]byte, 0, keyIDLen+nonceLen+len(ct))
	raw = append(raw, keyID)
	raw = append(raw, nonce...)
	raw = append(raw, ct...)
	return Marker + base64.StdEncoding.EncodeToString(raw), nil
}

// EncryptDeterministic encrypts like Encrypt but derives the nonce from the
// key and plaintext (HMAC-SHA256(key, plaintext)[0:12]) instead of drawing a
// random one. The same (keyID, plaintext) therefore always produces the same
// ciphertext bytes. This is REQUIRED for export-file encryption: export
// content feeds config_versions' data_sign (content MD5), and random nonce
// would make every export look like a new version to conf-agent.
//
// Security note: deterministic encryption leaks plaintext equality. Export
// secrets (api keys / upstream provider keys) are high-entropy random
// strings, so equality carries no exploitable information; do not use this
// for low-entropy secrets.
func EncryptDeterministic(plaintext string, key []byte, keyID uint8) (string, error) {
	if len(key) != keyLen {
		return "", fmt.Errorf("xcrypto: key length %d, want %d", len(key), keyLen)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(plaintext))
	nonce := mac.Sum(nil)[:nonceLen]

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	raw := make([]byte, 0, keyIDLen+nonceLen+len(ct))
	raw = append(raw, keyID)
	raw = append(raw, nonce...)
	raw = append(raw, ct...)
	return Marker + base64.StdEncoding.EncodeToString(raw), nil
}

// envelopeKeyID extracts the keyID from an envelope. ok=false when value is
// not an encrypted envelope.
func envelopeKeyID(value string) (uint8, bool) {
	if !IsEncrypted(value) {
		return 0, false
	}
	raw, err := base64.StdEncoding.DecodeString(value[len(Marker):])
	if err != nil || len(raw) < keyIDLen+nonceLen+1 {
		return 0, false
	}
	return raw[0], true
}

// Decrypt decrypts an envelope value. keys maps keyID to the 32-byte master
// key material (already HKDF-derived by the caller, or raw for this package's
// direct use). Non-envelope values return ErrNotEncrypted.
func Decrypt(value string, keys map[uint8][]byte) (string, error) {
	keyID, ok := envelopeKeyID(value)
	if !ok {
		return "", ErrNotEncrypted
	}
	key, ok := keys[keyID]
	if !ok {
		return "", fmt.Errorf("%w: keyID=%d", ErrKeyNotFound, keyID)
	}
	raw, err := base64.StdEncoding.DecodeString(value[len(Marker):])
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	if len(raw) < keyIDLen+nonceLen+1 {
		return "", ErrInvalidEnvelope
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, raw[keyIDLen:keyIDLen+nonceLen], raw[keyIDLen+nonceLen:], nil)
	if err != nil {
		return "", fmt.Errorf("xcrypto: decrypt: %w", err)
	}
	return string(pt), nil
}

// KeyIDOf returns the embedded keyID for observability (failure logs, sweep
// classification). ok=false for plaintext values.
func KeyIDOf(value string) (uint8, bool) {
	return envelopeKeyID(value)
}

// EnvelopeVersion returns the format version embedded in the marker.
func EnvelopeVersion() int {
	return versionV1
}

// ParseKeyID decodes a decimal keyID string ("1"-"255").
func ParseKeyID(s string) (uint8, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 255 {
		return 0, fmt.Errorf("xcrypto: invalid keyID %q", s)
	}
	return uint8(n), nil
}
