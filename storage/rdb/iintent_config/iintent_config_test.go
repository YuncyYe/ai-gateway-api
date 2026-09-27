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

package iintent_config

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iintent_config"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStorager(t *testing.T) (*IntentConfigStorager, *sql.DB) {
	// The DAO layer consults stateful.DefaultConfig when recording SQL.
	if stateful.DefaultConfig == nil {
		stateful.DefaultConfig = &stateful.Config{}
	}

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	// Same shape as db_ddl_sqlite.sql.
	_, err = db.Exec(`
CREATE TABLE intent_config (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  version TEXT NOT NULL,
  min_confidence REAL NOT NULL DEFAULT 0.6,
  questions TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TRIGGER intent_config_updated_at AFTER UPDATE ON intent_config
  FOR EACH ROW BEGIN UPDATE intent_config SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id; END;`)
	require.NoError(t, err)

	factory := lib.DBContextFactory(func(ctx context.Context, ops ...*lib.Op) (*lib.DBContext, error) {
		return lib.NewDBContext(ctx, db), nil
	})

	return NewIntentConfigStorager(factory), db
}

func TestIntentConfigStorager_Fetch(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStorager(t)

	t.Run("no record returns empty instead of error", func(t *testing.T) {
		config, err := s.Fetch(ctx)
		require.NoError(t, err)
		assert.Nil(t, config)
	})
}

func TestIntentConfigStorager_Upsert(t *testing.T) {
	ctx := context.Background()
	s, db := setupTestStorager(t)

	t.Run("inserts the singleton when absent", func(t *testing.T) {
		err := s.Upsert(ctx, &iintent_config.IntentConfig{
			Id:            iintent_config.IntentConfigFixedID,
			Version:       "20260926120000",
			MinConfidence: 0.6,
			Questions:     `[{"name":"q1","type":"choice","instructions":"i","criteria":{"a":"b"}}]`,
		})
		require.NoError(t, err)

		config, err := s.Fetch(ctx)
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Equal(t, iintent_config.IntentConfigFixedID, config.Id)
		assert.Equal(t, "20260926120000", config.Version)
		assert.InDelta(t, 0.6, config.MinConfidence, 1e-9)
		assert.Equal(t, `[{"name":"q1","type":"choice","instructions":"i","criteria":{"a":"b"}}]`, config.Questions)
		assert.False(t, config.CreatedAt.IsZero())
		assert.False(t, config.UpdatedAt.IsZero())

		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM intent_config").Scan(&count))
		assert.Equal(t, 1, count)
	})

	t.Run("overwrites the same row on the next upsert", func(t *testing.T) {
		err := s.Upsert(ctx, &iintent_config.IntentConfig{
			Id:            iintent_config.IntentConfigFixedID,
			Version:       "20260926130000",
			MinConfidence: 0.85,
			Questions:     `[]`,
		})
		require.NoError(t, err)

		config, err := s.Fetch(ctx)
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Equal(t, "20260926130000", config.Version)
		assert.InDelta(t, 0.85, config.MinConfidence, 1e-9)
		assert.Equal(t, `[]`, config.Questions)
		// The fixed id stays and no history row accumulates.
		assert.Equal(t, iintent_config.IntentConfigFixedID, config.Id)

		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM intent_config").Scan(&count))
		assert.Equal(t, 1, count)
	})

	t.Run("nil config is a no-op", func(t *testing.T) {
		require.NoError(t, s.Upsert(ctx, nil))
	})

	t.Run("questions JSON is stored verbatim", func(t *testing.T) {
		questions := `[{"name":"complexity","type":"score","instructions":"i","min_confidence":0.7,"levels":[{"name":"simple","description":"d"}]}]`
		err := s.Upsert(ctx, &iintent_config.IntentConfig{
			Id:            iintent_config.IntentConfigFixedID,
			Version:       "20260926140000",
			MinConfidence: 0.65,
			Questions:     questions,
		})
		require.NoError(t, err)

		config, err := s.Fetch(ctx)
		require.NoError(t, err)
		assert.Equal(t, questions, config.Questions)
		assert.InDelta(t, 0.65, config.MinConfidence, 1e-9)
	})
}
