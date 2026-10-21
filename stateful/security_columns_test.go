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
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
)

// writeColumnTestKeyring writes a single-key keyring file and returns its path.
func writeColumnTestKeyring(t *testing.T) string {
	t.Helper()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	content := "ActiveKeyID = 1\n[Keys]\n  1 = \"" + base64.StdEncoding.EncodeToString(key) + "\"\n"
	path := filepath.Join(t.TempDir(), "master.keys")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// setupColumnTypeCheck builds an in-memory control-plane DB whose envelope
// columns carry the given declared types, swaps it in as the bfe_db
// datasource, and (optionally) loads a keyring.
func setupColumnTypeCheck(t *testing.T, withKeyring bool, providerKeyType, apiKeyType string) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(fmt.Sprintf(
		`CREATE TABLE providers (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, api_keys %s)`, providerKeyType))
	require.NoError(t, err)
	_, err = db.Exec(fmt.Sprintf(
		`CREATE TABLE api_keys (inner_id INTEGER PRIMARY KEY AUTOINCREMENT, api_key %s)`, apiKeyType))
	require.NoError(t, err)

	prevDBs, prevConf, prevRing := DBs, DefaultConfig, SecretRing()
	DBs = map[string]*sql.DB{bfeDBName: db}
	DefaultConfig = &Config{Databases: map[string]*DbConfig{bfeDBName: {Driver: DriverSQLite}}}
	t.Cleanup(func() {
		DBs, DefaultConfig = prevDBs, prevConf
		secretRing.Store(prevRing)
	})

	if withKeyring {
		require.NoError(t, loadSecretRing(&SecurityConfig{MasterKeyFile: writeColumnTestKeyring(t)}))
	} else {
		secretRing.Store((*xcrypto.Keyring)(nil))
	}
}

// A JSON envelope column plus a keyring can only fail at write time on MySQL
// (Error 3140), so startup must refuse with an actionable message (issue #264).
func TestCheckEncryptedColumnTypes_RejectsJSONColumnWithKeyring(t *testing.T) {
	setupColumnTypeCheck(t, true, "JSON", "TEXT")

	err := CheckEncryptedColumnTypes()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "providers.api_keys")
	assert.Contains(t, err.Error(), "1.6")
	assert.NotContains(t, err.Error(), "api_keys.api_key")
}

func TestCheckEncryptedColumnTypes_RejectsJSONAPIKeyColumn(t *testing.T) {
	setupColumnTypeCheck(t, true, "TEXT", "JSON")

	err := CheckEncryptedColumnTypes()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_keys.api_key")
	assert.NotContains(t, err.Error(), "providers.api_keys")
}

func TestCheckEncryptedColumnTypes_AcceptsTextColumns(t *testing.T) {
	setupColumnTypeCheck(t, true, "TEXT", "TEXT")

	require.NoError(t, CheckEncryptedColumnTypes())
}

// Without a keyring the DAO writes plaintext JSON, which a JSON column
// accepts: gradual enable must not be blocked.
func TestCheckEncryptedColumnTypes_SkippedWithoutKeyring(t *testing.T) {
	setupColumnTypeCheck(t, false, "JSON", "JSON")

	require.NoError(t, CheckEncryptedColumnTypes())
}

// An unconfigured/unknown backend must not invent a startup failure.
func TestCheckEncryptedColumnTypes_SkippedWithoutDriver(t *testing.T) {
	setupColumnTypeCheck(t, true, "JSON", "JSON")

	DefaultConfig = &Config{}
	require.NoError(t, CheckEncryptedColumnTypes())
}