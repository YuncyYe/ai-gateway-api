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

// Copyright (c) 2021 The BFE Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stateful

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/bfenetworks/go-lib/log"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
)

// secretRing is the runtime keyring; nil means encryption is not configured
// (writes stay plaintext until a keyring file is configured and reloaded).
var secretRing atomic.Value // stores *xcrypto.Keyring

// exportCrypto is the runtime state of export-file encryption: the dedicated
// conf-file keyring plus the resolved active export keyID. enabled is fixed
// at load time: the switch AND a usable keyring must both be present.
type exportCrypto struct {
	kr       *xcrypto.Keyring
	activeID uint8
	enabled  bool
}

var exportCryptoState atomic.Value // stores *exportCrypto

// SecurityConfig is the [Security] section of ai_gateway_api.toml.
type SecurityConfig struct {
	// MasterKeyFile points to the keyring file (the ONLY injection way; no
	// env fallback by design). Empty disables encryption (gradual enable).
	MasterKeyFile string `toml:"MasterKeyFile"`
	// ActiveKeyID selects which key in the keyring encrypts new values.
	ActiveKeyID int `toml:"ActiveKeyID"`
	// EncryptExports enables field-level enc$v1$ encryption of sensitive
	// fields in the two key-bearing export topics (mod-api-key Tokens outer
	// keys, server_data_conf AIConf.Keys[].Key). Default false.
	EncryptExports bool `toml:"EncryptExports"`
	// ExportKeyFile is the dedicated conf-file keyring, distributed to all
	// BFE instances (BFE reads the same file via [Security].KeyFile). It is
	// an independent root: fully separated from MasterKeyFile.
	ExportKeyFile string `toml:"ExportKeyFile"`
	// ActiveExportKeyID selects which key in ExportKeyFile encrypts new
	// export ciphertexts. 0 (default) follows the keyring file's
	// ActiveKeyID.
	ActiveExportKeyID int `toml:"ActiveExportKeyID"`
	// PasswordHashCost is the bcrypt cost factor for users.password hashing.
	// 0 (the default) selects xcrypto.DefaultPasswordHashCost (10). Valid
	// range 4-16; out-of-range values fail startup (fail-fast).
	PasswordHashCost int `toml:"PasswordHashCost"`
}

// LoadSecretRing loads (or reloads) the keyring from the current
// DefaultConfig and atomically swaps it in. Failure keeps the previous
// keyring effective.
func LoadSecretRing() error {
	if DefaultConfig == nil {
		return errors.New("security: DefaultConfig not initialized")
	}
	return loadSecretRing(&DefaultConfig.Security)
}

func loadSecretRing(conf *SecurityConfig) error {
	path := conf.MasterKeyFile
	if path == "" {
		secretRing.Store((*xcrypto.Keyring)(nil))
		log.Logger.Info("security: MasterKeyFile not configured, encryption at rest disabled")
		return nil
	}
	kr, err := xcrypto.LoadKeyringFile(path)
	if err != nil {
		return fmt.Errorf("security: load keyring %s: %v", path, err)
	}
	secretRing.Store(kr)
	log.Logger.Info("security: keyring loaded from %s, activeKeyID=%d, keys=%v",
		path, kr.ActiveID(), kr.KeyIDs())
	return nil
}

// SecretRing returns the current keyring; nil when encryption is disabled.
func SecretRing() *xcrypto.Keyring {
	if v := secretRing.Load(); v != nil {
		return v.(*xcrypto.Keyring)
	}
	return nil
}

// EncryptIfEnabled encrypts value with the active key when a keyring is
// configured; otherwise returns value unchanged.
func EncryptIfEnabled(value string) (string, error) {
	kr := SecretRing()
	if kr == nil {
		return value, nil
	}
	return kr.Encrypt(value)
}

// DecryptIfEnabled decrypts envelope values when a keyring is configured;
// plaintext values pass through. Without a keyring values are returned
// as-is (encryption disabled).
func DecryptIfEnabled(value string) (string, error) {
	kr := SecretRing()
	if kr == nil {
		return value, nil
	}
	return kr.Decrypt(value)
}

// LoadExportSecretRing loads (or reloads) the conf-file keyring for export
// encryption and atomically swaps the runtime state. Failure keeps the
// previous state effective.
func LoadExportSecretRing() error {
	if DefaultConfig == nil {
		return errors.New("security: DefaultConfig not initialized")
	}
	return loadExportSecretRing(&DefaultConfig.Security)
}

func loadExportSecretRing(conf *SecurityConfig) error {
	path := conf.ExportKeyFile
	if path == "" {
		if conf.EncryptExports {
			return errors.New("security: EncryptExports=true but [Security].ExportKeyFile is not configured; refuse to start")
		}
		exportCryptoState.Store(&exportCrypto{})
		log.Logger.Info("security: ExportKeyFile not configured, export encryption disabled")
		return nil
	}

	kr, err := xcrypto.LoadKeyringFile(path)
	if err != nil {
		if conf.EncryptExports {
			return fmt.Errorf("security: EncryptExports=true but export keyring %s is unusable: %v", path, err)
		}
		// Gradual enable: a bad ExportKeyFile with the switch off is a
		// warning, not a startup failure.
		log.Logger.Warn("security: ExportKeyFile %s not usable yet (EncryptExports=false): %v", path, err)
		exportCryptoState.Store(&exportCrypto{})
		return nil
	}

	activeID := uint8(conf.ActiveExportKeyID)
	if activeID == 0 {
		activeID = kr.ActiveID()
	}
	if !kr.HasKey(activeID) {
		return fmt.Errorf("security: ActiveExportKeyID %d not found in export keyring %s", activeID, path)
	}

	exportCryptoState.Store(&exportCrypto{kr: kr, activeID: activeID, enabled: conf.EncryptExports})
	log.Logger.Info("security: export keyring loaded from %s, activeExportKeyID=%d, keys=%v, encryptExports=%v",
		path, activeID, kr.KeyIDs(), conf.EncryptExports)
	return nil
}

// ExportCryptoEnabled reports whether export-file encryption is active
// (switch on AND a usable keyring loaded, both fixed at load/reload time).
func ExportCryptoEnabled() bool {
	ec := currentExportCrypto()
	return ec != nil && ec.enabled
}

// ExportEncrypt encrypts one sensitive export field with the raw key material
// of the active export keyID, producing a deterministic enc$v1$ envelope
// decryptable by the BFE data plane.
func ExportEncrypt(plaintext string) (string, error) {
	ec := currentExportCrypto()
	if ec == nil || !ec.enabled {
		return "", errors.New("security: export encryption not enabled ([Security].EncryptExports/ExportKeyFile)")
	}
	return ec.kr.EncryptWithRawKey(plaintext, ec.activeID)
}

func currentExportCrypto() *exportCrypto {
	if v := exportCryptoState.Load(); v != nil {
		return v.(*exportCrypto)
	}
	return nil
}

// ReloadSecurity is the monitor-port /reload/security handler: re-read both
// keyring files (MasterKeyFile for DB at-rest, ExportKeyFile for export
// encryption) and swap them atomically. Each ring is validated and replaced
// independently: a failure keeps that ring's previous value; any failure
// makes the handler return an error so callers know a partial reload
// happened.
func ReloadSecurity(query url.Values) (string, error) {
	old := SecretRing()
	var oldRules = -1
	if old != nil {
		oldRules = len(old.KeyIDs())
	}
	if err := LoadSecretRing(); err != nil {
		return "", err
	}
	now := SecretRing()
	newKeys := 0
	if now != nil {
		newKeys = len(now.KeyIDs())
	}

	oldExp := currentExportCrypto()
	oldExpKeys := -1
	if oldExp != nil && oldExp.kr != nil {
		oldExpKeys = len(oldExp.kr.KeyIDs())
	}
	expErr := LoadExportSecretRing()
	nowExp := currentExportCrypto()
	newExpKeys := 0
	if nowExp != nil && nowExp.kr != nil {
		newExpKeys = len(nowExp.kr.KeyIDs())
	}

	log.Logger.Info("security: keyring reloaded: keys %d -> %d; export keys %d -> %d",
		oldRules, newKeys, oldExpKeys, newExpKeys)
	msg := fmt.Sprintf("security keyring reloaded: keys %d -> %d; export keys %d -> %d",
		oldRules, newKeys, oldExpKeys, newExpKeys)
	if expErr != nil {
		return msg, fmt.Errorf("security: export keyring reload failed (previous export keyring kept): %v", expErr)
	}
	return msg, nil
}

// CheckSecretAtRest enforces the fail-fast discipline AFTER the DB is up:
// if any sensitive column holds ciphertext, a usable keyring is mandatory;
// without ciphertext a missing keyring is allowed (gradual enable).
func CheckSecretAtRest() error {
	db, err := BFEDB()
	if err != nil {
		return err
	}
	has, err := anyCiphertextPresent(db)
	if err != nil {
		return fmt.Errorf("security: check ciphertext presence: %v", err)
	}
	kr := SecretRing()
	switch {
	case has && kr == nil:
		return errors.New("security: database holds encrypted secrets but no keyring is configured ([Security].MasterKeyFile); refuse to start")
	case has && kr != nil:
		log.Logger.Info("security: encrypted secrets present, keyring ready (activeKeyID=%d)", kr.ActiveID())
	case !has:
		log.Logger.Info("security: no encrypted secrets found in database")
	}
	return nil
}

func anyCiphertextPresent(db *sql.DB) (bool, error) {
	ctx := context.Background()
	var one int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM providers WHERE api_keys LIKE ? LIMIT 1`, xcrypto.Marker+"%").Scan(&one)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	err = db.QueryRowContext(ctx,
		`SELECT 1 FROM api_keys WHERE api_key LIKE ? LIMIT 1`, xcrypto.Marker+"%").Scan(&one)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// encryptedColumns lists the columns that receive whole-value enc$v1$
// envelopes at the DAO boundary. A JSON column can never hold one: MySQL
// validates JSON syntax on write, so the DAO's "one envelope per column" write
// fails with Error 3140 and the whole INSERT/UPDATE is rejected (issue #264).
var encryptedColumns = []struct{ Table, Column string }{
	{Table: "providers", Column: "api_keys"},
	{Table: "api_keys", Column: "api_key"},
}

// CheckEncryptedColumnTypes refuses to start when an envelope-carrying column
// is still declared as JSON while a keyring is loaded — that combination can
// only produce run-time write failures. Without a keyring the DAO writes
// plaintext JSON, which a JSON column accepts, so the check is skipped there
// and gradual enable keeps working.
func CheckEncryptedColumnTypes() error {
	if SecretRing() == nil {
		return nil
	}
	db, err := BFEDB()
	if err != nil {
		return err
	}

	var inspect func(*sql.DB) ([]string, error)
	switch dbDriver() {
	case DriverMySQL:
		inspect = jsonTypedColumnsMySQL
	case DriverSQLite, "sqlite-strip":
		inspect = jsonTypedColumnsSQLite
	default:
		// Unknown backend: do not invent a startup failure.
		return nil
	}
	bad, err := inspect(db)
	if err != nil {
		return fmt.Errorf("security: inspect encrypted column types: %v", err)
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("security: column(s) %s declared as JSON but hold enc$v1$ envelopes; "+
		"MySQL rejects the envelope on write (Error 3140) and every provider write fails. "+
		"Run the schema change in database/db_ddl_upgrade_to_0.0.11.sql §1.6 "+
		"(ALTER TABLE providers MODIFY COLUMN api_keys MEDIUMTEXT ..., ALGORITHM=COPY, LOCK=SHARED) "+
		"and restart", strings.Join(bad, ", "))
}

// dbDriver returns the configured driver of the bfe_db datasource.
func dbDriver() string {
	if DefaultConfig == nil {
		return ""
	}
	if conf := DefaultConfig.Databases[bfeDBName]; conf != nil {
		return conf.Driver
	}
	return ""
}

// jsonTypedColumnsMySQL returns the envelope columns declared as JSON, read
// from information_schema. Absent tables/columns are skipped: other startup
// checks own that failure.
func jsonTypedColumnsMySQL(db *sql.DB) ([]string, error) {
	ctx := context.Background()
	var bad []string
	for _, col := range encryptedColumns {
		var dataType string
		err := db.QueryRowContext(ctx,
			`SELECT DATA_TYPE FROM information_schema.COLUMNS
			 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			col.Table, col.Column).Scan(&dataType)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(dataType, "json") {
			bad = append(bad, col.Table+"."+col.Column)
		}
	}
	return bad, nil
}

// jsonTypedColumnsSQLite is the same check for SQLite deployments (the
// shipped schema uses TEXT; a hand-migrated JSON column is still caught).
func jsonTypedColumnsSQLite(db *sql.DB) ([]string, error) {
	ctx := context.Background()
	var bad []string
	for _, col := range encryptedColumns {
		rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", col.Table))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var (
				cid       int
				name      string
				declType  string
				notNull   int
				dfltValue sql.NullString
				pk        int
			)
			if err := rows.Scan(&cid, &name, &declType, &notNull, &dfltValue, &pk); err != nil {
				rows.Close()
				return nil, err
			}
			if name == col.Column && strings.EqualFold(strings.TrimSpace(declType), "json") {
				bad = append(bad, col.Table+"."+col.Column)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return bad, nil
}

// LoadPasswordHashCost validates [Security].PasswordHashCost and injects it
// into xcrypto (see SetPasswordHashCost). Failure is fatal (fail-fast).
func LoadPasswordHashCost() error {
	if DefaultConfig == nil {
		return errors.New("security: DefaultConfig not initialized")
	}
	return loadPasswordHashCost(&DefaultConfig.Security)
}

func loadPasswordHashCost(conf *SecurityConfig) error {
	cost := conf.PasswordHashCost
	if err := xcrypto.SetPasswordHashCost(cost); err != nil {
		return fmt.Errorf("security: invalid [Security].PasswordHashCost %d: %v", conf.PasswordHashCost, err)
	}
	if cost == 0 {
		cost = xcrypto.DefaultPasswordHashCost
	}
	log.Logger.Info("security: password hash cost = %d", cost)
	return nil
}
