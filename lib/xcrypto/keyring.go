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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/hkdf"
)

// apiKeyHashPepper is the stable HMAC key for api_key_hash. It is an
// application-level constant (NOT the master key, NOT rotated): the hash is
// an equality-lookup index over high-entropy random api keys, not a password
// store, so pepper secrecy provides no practical benefit and stability across
// keyring rotations is required for lookups to keep working.
var apiKeyHashPepper = func() []byte {
	h := sha256.Sum256([]byte("rainway-ai-gateway:api-key-hash-pepper:v1"))
	return h[:]
}()

// KeyHash computes the stable lookup hash for an api key value:
// hex(HMAC-SHA256(pepper, plaintext)).
func KeyHash(plaintext string) string {
	mac := hmac.New(sha256.New, apiKeyHashPepper)
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}

// Keyring holds all in-service master keys plus the active one used for new
// ciphertext. Encrypt uses the active key; Decrypt picks per-ciphertext by
// embedded keyID; plaintext values pass through unchanged.
type Keyring struct {
	keys     map[uint8][]byte
	encKeys  map[uint8][]byte
	active   uint8
	filePath string
}

// ActiveID returns the keyID used for new encryptions.
func (k *Keyring) ActiveID() uint8 {
	return k.active
}

// HasKey reports whether keyID is available for decryption.
func (k *Keyring) HasKey(id uint8) bool {
	_, ok := k.keys[id]
	return ok
}

// KeyIDs returns all loaded keyIDs.
func (k *Keyring) KeyIDs() []uint8 {
	ids := make([]uint8, 0, len(k.keys))
	for id := range k.keys {
		ids = append(ids, id)
	}
	return ids
}

// FilePath returns the keyring file path.
func (k *Keyring) FilePath() string {
	return k.filePath
}

// Encrypt encrypts plaintext with the active key.
func (k *Keyring) Encrypt(plaintext string) (string, error) {
	key, ok := k.encKeys[k.active]
	if !ok {
		return "", fmt.Errorf("%w: active keyID=%d", ErrKeyNotFound, k.active)
	}
	return Encrypt(plaintext, key, k.active)
}

// Decrypt decrypts an envelope with the key identified by its embedded keyID.
// Plaintext values (no marker) pass through, which keeps legacy rows readable
// before the migration sweep runs.
func (k *Keyring) Decrypt(value string) (string, error) {
	if !IsEncrypted(value) {
		return value, nil
	}
	keyID, ok := envelopeKeyID(value)
	if !ok {
		return "", ErrInvalidEnvelope
	}
	key, ok := k.encKeys[keyID]
	if !ok {
		return "", fmt.Errorf("%w: keyID=%d", ErrKeyNotFound, keyID)
	}
	return Decrypt(value, map[uint8][]byte{keyID: key})
}

// KeyHash computes the stable lookup hash for an api key value. Delegates to
// the package-level KeyHash (pepper is rotation-independent).
func (k *Keyring) KeyHash(plaintext string) string {
	return KeyHash(plaintext)
}

// EncryptWithRawKey encrypts plaintext with the RAW 32-byte key material of
// the given keyID (NOT the HKDF-derived encKeys used for DB at-rest
// encryption), producing a deterministic envelope. This is the export-file
// encryption primitive: the data plane (BFE bfe_util/crypto) decrypts with
// the raw keyring key directly, so derivation here would be incompatible.
// keyID==0 means the keyring's active ID.
func (k *Keyring) EncryptWithRawKey(plaintext string, keyID uint8) (string, error) {
	if keyID == 0 {
		keyID = k.active
	}
	key, ok := k.keys[keyID]
	if !ok {
		return "", fmt.Errorf("%w: keyID=%d", ErrKeyNotFound, keyID)
	}
	return EncryptDeterministic(plaintext, key, keyID)
}

// keyringFile is the TOML layout of MasterKeyFile:
//
//	ActiveKeyID = 2
//	[Keys]
//	  1 = "base64-master-key"
//	  2 = "base64-master-key"
type keyringFile struct {
	ActiveKeyID int
	Keys        map[string]string
}

// LoadKeyringFile parses and validates the keyring file, deriving per-key
// encryption keys via HKDF-SHA256. An empty path returns ErrNoKeyring.
func LoadKeyringFile(path string) (*Keyring, error) {
	if path == "" {
		return nil, errors.New("xcrypto: keyring file path empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f keyringFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("xcrypto: parse keyring file: %w", err)
	}
	if len(f.Keys) == 0 {
		return nil, errors.New("xcrypto: keyring file has no [Keys] entries")
	}

	k := &Keyring{
		keys:     make(map[uint8][]byte, len(f.Keys)),
		encKeys:  make(map[uint8][]byte, len(f.Keys)),
		filePath: path,
	}
	for s, b64 := range f.Keys {
		id, err := ParseKeyID(s)
		if err != nil {
			return nil, err
		}
		master, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(master) != keyLen {
			return nil, fmt.Errorf("xcrypto: key %d: invalid base64 or length != %d", id, keyLen)
		}
		encKey, err := hkdfBytes(master, "db-enc-key")
		if err != nil {
			return nil, err
		}
		k.keys[id] = master
		k.encKeys[id] = encKey
	}

	if f.ActiveKeyID < 1 || f.ActiveKeyID > 255 {
		return nil, fmt.Errorf("xcrypto: ActiveKeyID %d out of range", f.ActiveKeyID)
	}
	k.active = uint8(f.ActiveKeyID)
	if _, ok := k.keys[k.active]; !ok {
		return nil, fmt.Errorf("xcrypto: ActiveKeyID %d not found in [Keys]", k.active)
	}
	return k, nil
}

func hkdfBytes(master []byte, info string) ([]byte, error) {
	r := hkdf.New(sha256.New, master, nil, []byte(info))
	out := make([]byte, keyLen)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}
