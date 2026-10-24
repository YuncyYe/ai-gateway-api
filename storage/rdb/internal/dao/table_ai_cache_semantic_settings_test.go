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
	"database/sql"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAICacheSemanticSettingsTestDB(t *testing.T) (*sql.DB, lib.DBContexter) {
	// The DAO layer consults stateful.DefaultConfig when recording SQL.
	if stateful.DefaultConfig == nil {
		stateful.DefaultConfig = &stateful.Config{}
	}

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	db.SetMaxOpenConns(1)

	_, err = db.Exec(`
CREATE TABLE ai_cache_semantic_settings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  top_k INTEGER NOT NULL DEFAULT 1,
  threshold REAL NOT NULL DEFAULT 0.15,
  threshold_relation TEXT NOT NULL DEFAULT 'lt',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`)
	require.NoError(t, err)

	return db, lib.NewDBContext(context.Background(), db)
}

func TestTAICacheSemanticSettingsEmptyTable(t *testing.T) {
	_, dbCtx := setupAICacheSemanticSettingsTestDB(t)

	one, err := TAICacheSemanticSettingsOne(dbCtx, &TAICacheSemanticSettingsParam{})
	require.NoError(t, err)
	assert.Nil(t, one)

	list, err := TAICacheSemanticSettingsList(dbCtx, &TAICacheSemanticSettingsParam{OrderBy: lib.PString("id ASC")})
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestTAICacheSemanticSettingsReplaceAllKeepsSingleRow(t *testing.T) {
	_, dbCtx := setupAICacheSemanticSettingsTestDB(t)

	row := func(topK int, threshold float64, relation string) *TAICacheSemanticSettingsParam {
		return &TAICacheSemanticSettingsParam{
			TopK:              lib.PInt(topK),
			Threshold:         lib.PFloat64(threshold),
			ThresholdRelation: lib.PString(relation),
		}
	}

	// First overwrite on the empty table inserts the row.
	_, err := TAICacheSemanticSettingsReplaceAll(dbCtx, row(1, 0.15, "lt"))
	require.NoError(t, err)

	// Second overwrite still leaves exactly one row with the new values
	// (delete-all + insert, regardless of the auto-increment id).
	_, err = TAICacheSemanticSettingsReplaceAll(dbCtx, row(8, 1.2, "gte"))
	require.NoError(t, err)

	list, err := TAICacheSemanticSettingsList(dbCtx, &TAICacheSemanticSettingsParam{OrderBy: lib.PString("id ASC")})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, 8, list[0].TopK)
	assert.Equal(t, 1.2, list[0].Threshold)
	assert.Equal(t, "gte", list[0].ThresholdRelation)
	assert.False(t, list[0].CreatedAt.IsZero())
	assert.False(t, list[0].UpdatedAt.IsZero())
}
