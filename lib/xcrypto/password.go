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
	"crypto/subtle"
	"fmt"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultPasswordHashCost is the bcrypt cost factor used when
	// [Security].PasswordHashCost is unset (0).
	DefaultPasswordHashCost = 10

	minPasswordHashCost = 4
	maxPasswordHashCost = 16

	// passwordMaxBytes is the bcrypt input limit. CompareHashAndPassword
	// silently truncates longer inputs, so CheckPassword refuses them.
	passwordMaxBytes = 72
)

var passwordHashCost int32 = DefaultPasswordHashCost

// dummyPasswordHash is compared against on the user-not-found login path so
// that response timing does not reveal whether the account exists.
var dummyPasswordHash = func() string {
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-password"), DefaultPasswordHashCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}()

// SetPasswordHashCost sets the bcrypt cost factor for password hashing and
// verification. A cost of 0 selects DefaultPasswordHashCost. Values outside
// [minPasswordHashCost, maxPasswordHashCost] are rejected. It is injected
// once at startup (see stateful.LoadPasswordHashCost); tests may adjust it.
func SetPasswordHashCost(cost int) error {
	if cost == 0 {
		cost = DefaultPasswordHashCost
	}
	if cost < minPasswordHashCost || cost > maxPasswordHashCost {
		return fmt.Errorf("xcrypto: password hash cost %d out of range [%d, %d]",
			cost, minPasswordHashCost, maxPasswordHashCost)
	}
	atomic.StoreInt32(&passwordHashCost, int32(cost))
	return nil
}

// HashPassword returns the bcrypt hash of a plaintext password as a standard
// bcrypt string ready to store in users.password.
func HashPassword(plaintext string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plaintext), int(atomic.LoadInt32(&passwordHashCost)))
	if err != nil {
		return "", fmt.Errorf("xcrypto: hash password: %w", err)
	}
	return string(h), nil
}

// CheckPassword verifies plaintext against a stored users.password value in
// constant time. It reports ok and needMigrate:
//
//   - Stored bcrypt hash: verified via bcrypt.CompareHashAndPassword (itself
//     constant-time in the password); needMigrate is always false.
//   - Legacy plaintext row (pre-hashing): compared with
//     subtle.ConstantTimeCompare; ok=true implies needMigrate=true, telling
//     the caller to re-hash and persist the password (lazy migration).
//
// Plaintext longer than 72 bytes is refused outright: bcrypt would silently
// truncate it on compare, which must not become an acceptance path.
func CheckPassword(hashed, plaintext string) (ok bool, needMigrate bool) {
	if len(plaintext) > passwordMaxBytes {
		return false, false
	}
	if IsHashedPassword(hashed) {
		return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plaintext)) == nil, false
	}
	if subtle.ConstantTimeCompare([]byte(hashed), []byte(plaintext)) == 1 {
		return true, true
	}
	return false, false
}

// IsHashedPassword reports whether s looks like a bcrypt hash string
// ($2a$/$2b$/$2y$ prefix). Anything else is treated as a legacy plaintext
// password still waiting for lazy migration.
func IsHashedPassword(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

// DummyHash returns a precomputed bcrypt hash for timing equalization on the
// user-not-found login path (see CheckPassword).
func DummyHash() string {
	return dummyPasswordHash
}
