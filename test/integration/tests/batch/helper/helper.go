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

// Package batchhelper 提供 batch 模块各场景用例共享的探测守卫与
// SQLite / MySQL 双后端种子直写工具。
//
// 后端选择：测试包 TestMain 使用 testutil.StartServerAuto——设置
// AIAPI_MYSQL_DSN 时为 MySQL 后端（库自动建删），否则 SQLite。
// OpenDB 依 ServerManager.MySQLDSN() 是否为空自动选择驱动；
// 种子 SQL 不含任何方言函数（时间值由 Go 侧传入），同一实现两用。
package batchhelper

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/require"
)

// TestProductName 是测试环境注入的产品线名称：产品线上下文优先取
// McProductProbe 注入值；本组路由无产品路径变量，处理器回退到 conf 中
// AIRouteInnerProductName = "AI_product"（种子数据已预置该产品）。
const TestProductName = "AI_product"

var probeOnce sync.Once
var probeReachable bool

// batchAPIReachable 探测 /batches 端点是否已可正常进入业务逻辑
// （true = 产品线解析已可用）。
func batchAPIReachable(t *testing.T) bool {
	t.Helper()
	probeOnce.Do(func() {
		resp, err := testutil.GetClient().Get("/open-api/v1/batches")
		if err != nil {
			probeReachable = false
			return
		}
		probeReachable = resp.ErrNum == 200
	})
	return probeReachable
}

// RequireBatchAPI 在批量管控 API 不可达时跳过用例（产品线解析回归时
// 的防御性守卫；当前处理器已实现默认产品线回退，正常始终通过）。
func RequireBatchAPI(t *testing.T) {
	t.Helper()
	if !batchAPIReachable(t) {
		t.Skip("skip: /batches API unreachable (product context unresolved). " +
			"See tests/batch/design.md §2.3")
	}
}

// OpenDB 打开本测试进程的数据库：委托 testutil.OpenDB（MySQL 后端用 mysql
// 驱动连被测库；否则 sqlite-strip 打开 sm.DBPath），同一断言代码双后端零改动。
func OpenDB(t *testing.T, sm *testutil.ServerManager) *sql.DB {
	t.Helper()
	return testutil.OpenDB(t, sm)
}

// BatchTaskRow 描述 batch_tasks 直写种子（仅含测试常用列）。
type BatchTaskRow struct {
	BatchID      string
	APIKeyID     string
	ProductName  string
	EntityID     string
	Provider     string
	KeyName      string
	Endpoint     string
	InputFileID  string
	OutputFileID string
	Status       string
	SettleStatus string
	ReserveUnits int64
	OverReserved int
	SyncSource   string
	IdemKey      string
}

// InsertBatchTask 向 batch_tasks 直写一行，返回自增 id。
// 时间列由 Go 侧 time.Now() 传入，SQL 双方言通用。
func InsertBatchTask(t *testing.T, db *sql.DB, row BatchTaskRow) int64 {
	t.Helper()
	if row.SettleStatus == "" {
		row.SettleStatus = "reserved"
	}
	if row.SyncSource == "" {
		row.SyncSource = "log"
	}
	now := time.Now()
	res, err := db.Exec(`
		INSERT INTO batch_tasks (
			batch_id, api_key_id, product_name, entity_id, provider, key_name,
			endpoint, input_file_id, output_file_id, status, request_counts,
			est_lines, usage_input_tokens, usage_output_tokens, usage_source,
			reserve_units, settle_units, settle_status, over_reserved,
			sync_source, idem_key, created_at, updated_at, terminal_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', 0, 0, 0, '', ?, 0, ?, ?, ?, ?, ?, ?, NULL)
	`, row.BatchID, row.APIKeyID, row.ProductName, row.EntityID, row.Provider,
		row.KeyName, row.Endpoint, row.InputFileID, row.OutputFileID, row.Status,
		row.ReserveUnits, row.SettleStatus, row.OverReserved, row.SyncSource, row.IdemKey,
		now, now)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// DeleteBatchTask 清理 batch_tasks 行。
func DeleteBatchTask(t *testing.T, db *sql.DB, batchID string) {
	t.Helper()
	_, err := db.Exec("DELETE FROM batch_tasks WHERE batch_id = ?", batchID)
	require.NoError(t, err)
}

// BatchFileRow 描述 batch_files 直写种子。
type BatchFileRow struct {
	FileID      string
	Provider    string
	APIKeyID    string
	ProductName string
	Direction   string
	Purpose     string
	Filename    string
	Lines       int64
	Bytes       int64
}

// InsertBatchFile 向 batch_files 直写一行。行数列名为 line_count
// （原 lines 为 MySQL 8 保留字，特性内已改名；JSON 字段仍为 lines）。
func InsertBatchFile(t *testing.T, db *sql.DB, row BatchFileRow) {
	t.Helper()
	now := time.Now()
	_, err := db.Exec(`
		INSERT INTO batch_files (
			file_id, provider, key_name, api_key_id, product_name, direction,
			purpose, filename, line_count, bytes, first_seen_at, last_seen_at, created_at, updated_at
		) VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, row.FileID, row.Provider, row.APIKeyID, row.ProductName, row.Direction,
		row.Purpose, row.Filename, row.Lines, row.Bytes, now, now, now, now)
	require.NoError(t, err)
}

// DeleteBatchFile 清理 batch_files 行。
func DeleteBatchFile(t *testing.T, db *sql.DB, fileID, provider string) {
	t.Helper()
	_, err := db.Exec("DELETE FROM batch_files WHERE file_id = ? AND provider = ?", fileID, provider)
	require.NoError(t, err)
}

// InsertProvider 向 providers 直写一行最小可用 provider 记录
// （cancel 出网解析需要 instance_pool 与 api_keys 非空）。
// instancePool / apiKeys 为 JSON 文本。
func InsertProvider(t *testing.T, db *sql.DB, name, instancePool, apiKeys string) {
	t.Helper()
	now := time.Now()
	_, err := db.Exec(`
		INSERT INTO providers (
			name, description, model_endpoint, models, api_keys, instance_pool,
			model_protocols, protocol_paths, created_at, updated_at
		) VALUES (?, 'batch test provider', '{"schema":"https","uri":"/v1/models"}',
			'["deepseek-chat"]', ?, ?, '["openai"]', '{"openai":"/v1"}', ?, ?)
	`, name, apiKeys, instancePool, now, now)
	require.NoError(t, err)
}

// DeleteProvider 清理 providers 行。
func DeleteProvider(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	_, err := db.Exec("DELETE FROM providers WHERE name = ?", name)
	require.NoError(t, err)
}

// Unique 生成带前缀的唯一标识（避免同模块多用例相互污染）。
func Unique(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, testutil.RandomString(8))
}
