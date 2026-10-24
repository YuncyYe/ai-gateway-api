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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func setTestHashCost(t *testing.T, cost int) {
	t.Helper()
	require.NoError(t, SetPasswordHashCost(cost))
	t.Cleanup(func() {
		require.NoError(t, SetPasswordHashCost(DefaultPasswordHashCost))
	})
}

func TestHashPasswordCheckPasswordRoundTrip(t *testing.T) {
	setTestHashCost(t, 4)

	hashed, err := HashPassword("secret123")
	require.NoError(t, err)
	assert.True(t, IsHashedPassword(hashed))

	ok, needMigrate := CheckPassword(hashed, "secret123")
	assert.True(t, ok)
	assert.False(t, needMigrate)

	ok, _ = CheckPassword(hashed, "wrong")
	assert.False(t, ok)

	// The same plaintext hashes to distinct strings (random salt).
	other, err := HashPassword("secret123")
	require.NoError(t, err)
	assert.NotEqual(t, hashed, other)
}

func TestCheckPasswordLegacyPlaintext(t *testing.T) {
	ok, needMigrate := CheckPassword("secret123", "secret123")
	assert.True(t, ok)
	assert.True(t, needMigrate, "legacy plaintext match must trigger lazy migration")

	ok, needMigrate = CheckPassword("secret123", "wrong")
	assert.False(t, ok)
	assert.False(t, needMigrate)

	ok, needMigrate = CheckPassword("", "")
	assert.True(t, ok)
	assert.True(t, needMigrate)
}

func TestCheckPasswordRefusesOverlongPlaintext(t *testing.T) {
	setTestHashCost(t, 4)
	hashed, err := HashPassword(strings.Repeat("a", 72))
	require.NoError(t, err)

	ok, _ := CheckPassword(hashed, strings.Repeat("a", 72))
	assert.True(t, ok)

	// 73 bytes: bcrypt compare would silently truncate to a match; refuse.
	ok, _ = CheckPassword(hashed, strings.Repeat("a", 73))
	assert.False(t, ok)
}

func TestCheckPasswordDummyHash(t *testing.T) {
	ok, _ := CheckPassword(DummyHash(), "whatever")
	assert.False(t, ok)
	assert.True(t, IsHashedPassword(DummyHash()))
}

func TestIsHashedPassword(t *testing.T) {
	assert.True(t, IsHashedPassword("$2a$10$w2oNyh4MO7SB.NHLPSq6kOj1GMiX1fApPYcWJmL8toZXGCQs3AJ0K"))
	assert.True(t, IsHashedPassword("$2b$10$abcdefghijklmnopqrstuuO1G9C1S2d3F4g5H6j7K8l9M"))
	assert.True(t, IsHashedPassword("$2y$10$abcdefghijklmnopqrstuuO1G9C1S2d3F4g5H6j7K8l9M"))
	assert.False(t, IsHashedPassword("admin"))
	assert.False(t, IsHashedPassword(""))
	assert.False(t, IsHashedPassword("$2x$10$abcdefghijklmnopqrstuu"))
}

func TestSetPasswordHashCost(t *testing.T) {
	assert.NoError(t, SetPasswordHashCost(4))
	assert.NoError(t, SetPasswordHashCost(16))
	assert.Error(t, SetPasswordHashCost(3))
	assert.Error(t, SetPasswordHashCost(17))
	assert.Error(t, SetPasswordHashCost(-1))

	// 0 resets to the default and is reflected in new hashes.
	require.NoError(t, SetPasswordHashCost(0))
	hashed, err := HashPassword("x")
	require.NoError(t, err)
	cost, err := bcrypt.Cost([]byte(hashed))
	require.NoError(t, err)
	assert.Equal(t, DefaultPasswordHashCost, cost)
}
