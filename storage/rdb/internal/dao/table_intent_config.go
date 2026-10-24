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
//limitations under the License.

package dao

import (
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao/internal"
)

const tIntentConfigTableName = "intent_config"

// intentConfigFixedID is the fixed primary key of the singleton row
// (single-row overwrite storage; see design-docs/sys-design/details/意图配置与导出.md).
const intentConfigFixedID = int64(1)

const (
	tIntentConfigUpsertMySQL = "INSERT INTO intent_config (id, version, min_confidence, questions) " +
		"VALUES (?, ?, ?, ?) " +
		"ON DUPLICATE KEY UPDATE version = VALUES(version), min_confidence = VALUES(min_confidence), questions = VALUES(questions)"
	tIntentConfigInsertSQLite = "INSERT OR IGNORE INTO intent_config (id, version, min_confidence, questions) VALUES (?, ?, ?, ?)"
	tIntentConfigUpdateSQLite = "UPDATE intent_config SET version = ?, min_confidence = ?, questions = ? WHERE id = ?"
)

// TIntentConfig maps to intent_config table (single-row singleton, fixed id=1).
type TIntentConfig struct {
	Id            int64     `db:"id"`
	Version       string    `db:"version"`
	MinConfidence float64   `db:"min_confidence"`
	Questions     string    `db:"questions"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

// TIntentConfigOne Query One
// return nil, nil if record not existed
func TIntentConfigOne(dbCtx lib.DBContexter, where *TIntentConfigParam) (*TIntentConfig, error) {
	t := &TIntentConfig{}
	err := internal.QueryOne(dbCtx, tIntentConfigTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TIntentConfigParam struct {
	Id            *int64     `db:"id"`
	Version       *string    `db:"version"`
	MinConfidence *float64   `db:"min_confidence"`
	Questions     *string    `db:"questions"`
	CreatedAt     *time.Time `db:"created_at"`
	UpdatedAt     *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TIntentConfigCreate One
func TIntentConfigCreate(dbCtx lib.DBContexter, data *TIntentConfigParam) (int64, error) {
	if data.CreatedAt == nil {
		data.CreatedAt = internal.PTimeNow()
	}
	return internal.Create(dbCtx, tIntentConfigTableName, data)
}

// TIntentConfigUpdate Update One
func TIntentConfigUpdate(dbCtx lib.DBContexter, val, where *TIntentConfigParam) (int64, error) {
	return internal.Update(dbCtx, tIntentConfigTableName, where, val)
}

// TIntentConfigUpsert overwrites the singleton row (fixed id=1): insert when
// absent, overwrite when present. It handles both dialects (see
// allocateAPIKeyIDSeq for the same pattern):
//   - MySQL: INSERT ... ON DUPLICATE KEY UPDATE in one statement; updated_at
//     is maintained by ON UPDATE CURRENT_TIMESTAMP.
//   - SQLite: INSERT OR IGNORE followed by UPDATE (idempotent overwrite); the
//     updated_at trigger maintains the timestamp.
func TIntentConfigUpsert(dbCtx lib.DBContexter, data *TIntentConfigParam) error {
	if isMySQL(dbCtx.Conn()) {
		return upsertIntentConfigMySQL(dbCtx, data)
	}
	return upsertIntentConfigSQLite(dbCtx, data)
}

func upsertIntentConfigMySQL(dbCtx lib.DBContexter, data *TIntentConfigParam) error {
	_, err := dbCtx.Execer().ExecContext(dbCtx,
		tIntentConfigUpsertMySQL,
		intentConfigFixedID, data.Version, data.MinConfidence, data.Questions)
	return err
}

func upsertIntentConfigSQLite(dbCtx lib.DBContexter, data *TIntentConfigParam) error {
	if _, err := dbCtx.Execer().ExecContext(dbCtx,
		tIntentConfigInsertSQLite,
		intentConfigFixedID, data.Version, data.MinConfidence, data.Questions); err != nil {
		return err
	}

	_, err := dbCtx.Execer().ExecContext(dbCtx,
		tIntentConfigUpdateSQLite,
		data.Version, data.MinConfidence, data.Questions, intentConfigFixedID)
	return err
}
