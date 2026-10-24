// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
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

package dao

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertIntentConfigMySQLStatement(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mock.ExpectExec(tIntentConfigUpsertMySQL).
		WithArgs(intentConfigFixedID, "20260926120000", 0.6, `[{"name":"q"}]`).
		WillReturnResult(sqlmock.NewResult(1, 2))

	dbCtx := lib.NewDBContext(context.Background(), db)
	err = upsertIntentConfigMySQL(dbCtx, &TIntentConfigParam{
		Version:       lib.PString("20260926120000"),
		MinConfidence: lib.PFloat64(0.6),
		Questions:     lib.PString(`[{"name":"q"}]`),
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())

	// A single ON DUPLICATE KEY UPDATE statement must carry the full row, so
	// the overwrite is atomic in MySQL.
	assert.Contains(t, tIntentConfigUpsertMySQL, "ON DUPLICATE KEY UPDATE")
}

func TestUpsertIntentConfigMySQLStatementError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mock.ExpectExec(tIntentConfigUpsertMySQL).
		WillReturnError(errors.New("connection lost"))

	dbCtx := lib.NewDBContext(context.Background(), db)
	err = upsertIntentConfigMySQL(dbCtx, &TIntentConfigParam{
		Version:       lib.PString("20260926120000"),
		MinConfidence: lib.PFloat64(0.6),
		Questions:     lib.PString(`[]`),
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertIntentConfigSQLiteStatements(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mock.ExpectExec(tIntentConfigInsertSQLite).
		WithArgs(intentConfigFixedID, "20260926120000", 0.6, `[{"name":"q"}]`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(tIntentConfigUpdateSQLite).
		WithArgs("20260926120000", 0.6, `[{"name":"q"}]`, intentConfigFixedID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	dbCtx := lib.NewDBContext(context.Background(), db)
	err = upsertIntentConfigSQLite(dbCtx, &TIntentConfigParam{
		Version:       lib.PString("20260926120000"),
		MinConfidence: lib.PFloat64(0.6),
		Questions:     lib.PString(`[{"name":"q"}]`),
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())

	// INSERT OR IGNORE makes the pair idempotent: re-running on an existing
	// row still ends with the overwrite applied.
	assert.Contains(t, tIntentConfigInsertSQLite, "INSERT OR IGNORE")
}

func TestUpsertIntentConfigSQLiteInsertError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mock.ExpectExec(tIntentConfigInsertSQLite).
		WillReturnError(errors.New("disk full"))

	dbCtx := lib.NewDBContext(context.Background(), db)
	err = upsertIntentConfigSQLite(dbCtx, &TIntentConfigParam{
		Version:       lib.PString("20260926120000"),
		MinConfidence: lib.PFloat64(0.6),
		Questions:     lib.PString(`[]`),
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
