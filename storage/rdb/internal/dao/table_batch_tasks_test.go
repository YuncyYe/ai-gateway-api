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

package dao

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// setupBatchTestDB 建内存 SQLite 库（结构同 db_ddl_sqlite.sql）。
func setupBatchTestDB(t *testing.T) (*sql.DB, lib.DBContexter) {
	// The DAO layer consults stateful.DefaultConfig when recording SQL.
	if stateful.DefaultConfig == nil {
		stateful.DefaultConfig = &stateful.Config{}
	}

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`
CREATE TABLE batch_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  batch_id TEXT NOT NULL,
  api_key_id TEXT NOT NULL,
  product_name TEXT NOT NULL,
  entity_id TEXT,
  provider TEXT NOT NULL,
  key_name TEXT,
  endpoint TEXT,
  input_file_id TEXT,
  output_file_id TEXT,
  status TEXT NOT NULL,
  request_counts TEXT,
  est_lines INTEGER,
  usage_input_tokens INTEGER,
  usage_output_tokens INTEGER,
  usage_source TEXT,
  reserve_units INTEGER,
  settle_units INTEGER,
  settle_status TEXT NOT NULL,
  over_reserved INTEGER DEFAULT 0,
  sync_source TEXT DEFAULT 'redis',
  idem_key TEXT NOT NULL UNIQUE,
  created_at DATETIME,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  terminal_at DATETIME
);
CREATE INDEX batch_tasks_status ON batch_tasks (status);
CREATE INDEX batch_tasks_apikey ON batch_tasks (api_key_id);
CREATE INDEX batch_tasks_terminal ON batch_tasks (terminal_at);

CREATE TABLE batch_files (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  key_name TEXT,
  api_key_id TEXT NOT NULL,
  product_name TEXT NOT NULL,
  direction TEXT NOT NULL,
  purpose TEXT,
  filename TEXT,
  line_count INTEGER,
  bytes INTEGER,
  first_seen_at DATETIME,
  last_seen_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (file_id, provider)
);
CREATE INDEX batch_files_apikey ON batch_files (api_key_id);
`)
	require.NoError(t, err)
	return db, lib.NewDBContext(context.Background(), db)
}

func sampleTaskParam(idem string) *TBatchTaskParam {
	return &TBatchTaskParam{
		BatchID:      lib.PString(idem),
		APIKeyID:     lib.PString("api-key-1"),
		ProductName:  lib.PString("p1"),
		Provider:     lib.PString("openai"),
		KeyName:      lib.PString("k1"),
		InputFileID:  lib.PString("file-in"),
		Status:       lib.PString("in_progress"),
		EstLines:     lib.PInt64(3),
		SettleStatus: lib.PString("reserved"),
		SyncSource:   lib.PString("redis"),
		IdemKey:      lib.PString(idem),
	}
}

func TestTBatchTaskUpsertByIdemKey(t *testing.T) {
	_, dbCtx := setupBatchTestDB(t)

	// 首次插入。
	id, err := TBatchTaskUpsertByIdemKey(dbCtx, sampleTaskParam("b-1"))
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// 幂等冲突更新：状态推进 + usage 原地值，归属列不回退。
	updated := sampleTaskParam("b-1")
	updated.APIKeyID = lib.PString("api-key-EVIL")
	updated.ProductName = lib.PString("other-product")
	updated.Status = lib.PString("completed")
	updated.UsageInputTokens = lib.PInt64(42)
	updated.SettleStatus = lib.PString("settled")
	terminalAt := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	updated.TerminalAt = &terminalAt

	id2, err := TBatchTaskUpsertByIdemKey(dbCtx, updated)
	require.NoError(t, err)
	_ = id2

	one, err := TBatchTaskOne(dbCtx, &TBatchTaskParam{IdemKey: lib.PString("b-1")})
	require.NoError(t, err)
	require.NotNil(t, one)
	assert.Equal(t, id, one.ID, "冲突更新不得产生新行")
	assert.Equal(t, "api-key-1", one.APIKeyID, "归属列不回退")
	assert.Equal(t, "p1", one.ProductName, "product_name 不回退")
	assert.Equal(t, "completed", one.Status)
	assert.Equal(t, int64(42), one.UsageInputTokens)
	assert.Equal(t, "settled", one.SettleStatus)
	require.NotNil(t, one.TerminalAt)

	// 缺 idem_key 参数错误。
	_, err = TBatchTaskUpsertByIdemKey(dbCtx, &TBatchTaskParam{})
	require.Error(t, err)
}

func TestTBatchTaskPreemptCancel(t *testing.T) {
	_, dbCtx := setupBatchTestDB(t)

	_, err := TBatchTaskUpsertByIdemKey(dbCtx, sampleTaskParam("b-1"))
	require.NoError(t, err)

	// 非终态可抢占。
	affected, err := TBatchTaskPreemptCancel(dbCtx, "b-1", "p1", []string{"completed", "expired", "failed", "cancelled"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	one, err := TBatchTaskOne(dbCtx, &TBatchTaskParam{IdemKey: lib.PString("b-1")})
	require.NoError(t, err)
	assert.Equal(t, "cancelling", one.Status)

	// storager 语义：重放抢占时 excluded 含 cancelling（双方言一致的
	// "仅首个转换者受理"），cancelling 行不可再次抢占 → affected=0 → 409。
	excludedWithCancelling := []string{"completed", "expired", "failed", "cancelled", "cancelling"}
	affected, err = TBatchTaskPreemptCancel(dbCtx, "b-1", "p1", excludedWithCancelling, time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected, "cancelling 重放抢占 affected=0 → 409")

	// 终态不可抢占。
	_, err = TBatchTaskUpdate(dbCtx, &TBatchTaskParam{Status: lib.PString("completed")}, &TBatchTaskParam{IdemKey: lib.PString("b-1")})
	require.NoError(t, err)
	affected, err = TBatchTaskPreemptCancel(dbCtx, "b-1", "p1", []string{"completed", "expired", "failed", "cancelled"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected, "终态抢占 affected=0 → 409")

	// product_name 隔离：跨产品线抢占 affected=0。
	affected, err = TBatchTaskPreemptCancel(dbCtx, "b-1", "other-product", []string{"completed", "expired", "failed", "cancelled"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)

	// 不存在的任务 affected=0。
	affected, err = TBatchTaskPreemptCancel(dbCtx, "ghost", "p1", []string{"completed"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)
}

func TestTBatchTaskUpdateSettleStatus(t *testing.T) {
	_, dbCtx := setupBatchTestDB(t)
	_, err := TBatchTaskUpsertByIdemKey(dbCtx, sampleTaskParam("b-1"))
	require.NoError(t, err)

	// reserved → settled 条件推进。
	assign := &TBatchTaskParam{
		SettleStatus:      lib.PString("settled"),
		SettleUnits:       lib.PInt64(777),
		UsageInputTokens:  lib.PInt64(10),
		UsageOutputTokens: lib.PInt64(20),
		UsageSource:       lib.PString("reconcile"),
		OverReserved:      lib.PBool(true),
		UpdatedAt:         lib.PTimeNow(),
	}
	affected, err := TBatchTaskUpdateSettleStatus(dbCtx, "b-1", "p1", "reserved", assign)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	one, err := TBatchTaskOne(dbCtx, &TBatchTaskParam{IdemKey: lib.PString("b-1")})
	require.NoError(t, err)
	assert.Equal(t, "settled", one.SettleStatus)
	assert.Equal(t, int64(777), one.SettleUnits)
	assert.True(t, one.OverReserved)

	// 重复推进 affected=0（防双倍结算第三道防线）。
	affected, err = TBatchTaskUpdateSettleStatus(dbCtx, "b-1", "p1", "reserved", assign)
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)

	// released 语义。
	affected, err = TBatchTaskUpdateSettleStatus(dbCtx, "b-1", "p1", "settled",
		&TBatchTaskParam{SettleStatus: lib.PString("released"), UpdatedAt: lib.PTimeNow()})
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)
}

func TestTBatchTaskListFiltersAndCursor(t *testing.T) {
	_, dbCtx := setupBatchTestDB(t)

	seed := []*TBatchTaskParam{
		{BatchID: lib.PString("b-1"), APIKeyID: lib.PString("ak-1"), ProductName: lib.PString("p1"), Provider: lib.PString("openai"), Status: lib.PString("completed"), SettleStatus: lib.PString("settled"), IdemKey: lib.PString("b-1")},
		{BatchID: lib.PString("b-2"), APIKeyID: lib.PString("ak-2"), ProductName: lib.PString("p1"), Provider: lib.PString("azure"), Status: lib.PString("in_progress"), SettleStatus: lib.PString("reserved"), IdemKey: lib.PString("b-2"), EntityID: lib.PString("e-1")},
		{BatchID: lib.PString("b-3"), APIKeyID: lib.PString("ak-1"), ProductName: lib.PString("p1"), Provider: lib.PString("openai"), Status: lib.PString("failed"), SettleStatus: lib.PString("released"), IdemKey: lib.PString("b-3")},
		{BatchID: lib.PString("b-4"), APIKeyID: lib.PString("ak-9"), ProductName: lib.PString("p2"), Provider: lib.PString("openai"), Status: lib.PString("queued"), SettleStatus: lib.PString("reserved"), IdemKey: lib.PString("b-4")},
	}
	for _, s := range seed {
		_, err := TBatchTaskUpsertByIdemKey(dbCtx, s)
		require.NoError(t, err)
	}

	// 组合过滤：product + provider + status + settle_status。
	list, err := TBatchTaskList(dbCtx, &TBatchTaskParam{
		ProductName:  lib.PString("p1"),
		Provider:     lib.PString("openai"),
		Status:       lib.PString("completed"),
		SettleStatus: lib.PString("settled"),
		OrderBy:      lib.PString("id"),
	})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "b-1", list[0].BatchID)

	// api_key + entity 过滤。
	list, err = TBatchTaskList(dbCtx, &TBatchTaskParam{
		ProductName: lib.PString("p1"),
		APIKeyID:    lib.PString("ak-2"),
		EntityID:    lib.PString("e-1"),
	})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "b-2", list[0].BatchID)

	// StatusNotIn：非终态（job 状态推进候选）。
	list, err = TBatchTaskList(dbCtx, &TBatchTaskParam{
		ProductName: lib.PString("p1"),
		StatusNotIn: []string{"completed", "expired", "failed", "cancelled"},
		OrderBy:     lib.PString("id"),
	})
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "b-2", list[0].BatchID)

	// StatusIn（job 释放候选）。
	list, err = TBatchTaskList(dbCtx, &TBatchTaskParam{
		ProductName:  lib.PString("p1"),
		StatusIn:     []string{"expired", "failed", "cancelled"},
		SettleStatus: lib.PString("released"),
	})
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "b-3", list[0].BatchID)

	// 游标分页：id 升序 + id > cursor + limit。
	page1, err := TBatchTaskList(dbCtx, &TBatchTaskParam{
		ProductName: lib.PString("p1"),
		OrderBy:     lib.PString("id"),
		Limit:       []uint{0, 2},
	})
	require.NoError(t, err)
	require.Len(t, page1, 2)

	page2, err := TBatchTaskList(dbCtx, &TBatchTaskParam{
		ProductName: lib.PString("p1"),
		IDGT:        lib.PInt64(page1[len(page1)-1].ID),
		OrderBy:     lib.PString("id"),
		Limit:       []uint{0, 2},
	})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Greater(t, page2[0].ID, page1[1].ID)
}

func TestTBatchFileUpsertByFileProvider(t *testing.T) {
	_, dbCtx := setupBatchTestDB(t)

	data := &TBatchFileParam{
		FileID:      lib.PString("f-1"),
		Provider:    lib.PString("openai"),
		KeyName:     lib.PString("k1"),
		APIKeyID:    lib.PString("ak-1"),
		ProductName: lib.PString("p1"),
		Direction:   lib.PString("input"),
		Purpose:     lib.PString("batch"),
		Filename:    lib.PString("input.jsonl"),
		Lines:       lib.PInt64(5),
		Bytes:       lib.PInt64(100),
	}
	id, err := TBatchFileUpsertByFileProvider(dbCtx, data)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	one, err := TBatchFileOne(dbCtx, &TBatchFileParam{FileID: lib.PString("f-1"), Provider: lib.PString("openai")})
	require.NoError(t, err)
	require.NotNil(t, one)
	assert.Equal(t, "input.jsonl", one.Filename, "首次 insert 必须落库 filename")

	// 幂等刷新。
	data.Lines = lib.PInt64(9)
	first := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	last := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	data.FirstSeenAt = &first
	data.LastSeenAt = &last
	_, err = TBatchFileUpsertByFileProvider(dbCtx, data)
	require.NoError(t, err)

	one, err = TBatchFileOne(dbCtx, &TBatchFileParam{FileID: lib.PString("f-1"), Provider: lib.PString("openai")})
	require.NoError(t, err)
	require.NotNil(t, one)
	assert.Equal(t, int64(9), one.Lines)
	assert.Equal(t, int64(100), one.Bytes)
	assert.Equal(t, "input.jsonl", one.Filename)

	// 二次 upsert 传空串 filename（参数层 PString 空串为非 nil，
	// 生产路径实际形态）：NULLIF+COALESCE 保护，DB 值不回退为空。
	data.Filename = lib.PString("")
	_, err = TBatchFileUpsertByFileProvider(dbCtx, data)
	require.NoError(t, err)

	one, err = TBatchFileOne(dbCtx, &TBatchFileParam{FileID: lib.PString("f-1"), Provider: lib.PString("openai")})
	require.NoError(t, err)
	require.NotNil(t, one)
	assert.Equal(t, "input.jsonl", one.Filename, "空 filename 不得把已有值刷空")

	// (file_id, provider) 复合唯一：同 file 不同 provider 两条。
	_, err = TBatchFileUpsertByFileProvider(dbCtx, &TBatchFileParam{
		FileID: lib.PString("f-1"), Provider: lib.PString("azure"),
		APIKeyID: lib.PString("ak-1"), ProductName: lib.PString("p1"), Direction: lib.PString("output"),
	})
	require.NoError(t, err)
	list, err := TBatchFileList(dbCtx, &TBatchFileParam{FileID: lib.PString("f-1")})
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// 缺参数。
	_, err = TBatchFileUpsertByFileProvider(dbCtx, &TBatchFileParam{})
	require.Error(t, err)

	// 按 api_key 过滤。
	list, err = TBatchFileList(dbCtx, &TBatchFileParam{APIKeyID: lib.PString("ak-1")})
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestTBatchTaskCreateAndOne(t *testing.T) {
	_, dbCtx := setupBatchTestDB(t)

	id, err := TBatchTaskCreate(dbCtx, sampleTaskParam("b-create"))
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	one, err := TBatchTaskOne(dbCtx, &TBatchTaskParam{BatchID: lib.PString("b-create")})
	require.NoError(t, err)
	require.NotNil(t, one)
	assert.Equal(t, "b-create", one.BatchID)

	// 未命中返回 (nil, nil)。
	none, err := TBatchTaskOne(dbCtx, &TBatchTaskParam{BatchID: lib.PString("ghost")})
	require.NoError(t, err)
	assert.Nil(t, none)
}
