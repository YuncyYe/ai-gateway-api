//go:build mysql

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
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// setupOperationLogMySQLTestDB creates a fresh operation_logs table in the
// local MySQL test instance (see environment/mysql-installation.md) and loads
// 43 rows with same-second created_at batches to exercise the id tiebreaker.
func setupOperationLogMySQLTestDB(t *testing.T) (lib.DBContexter, func()) {
	dsn := mysqlDSN(t)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)

	oldConfig := stateful.DefaultConfig
	stateful.DefaultConfig = &stateful.Config{
		RunTime: stateful.RunTimeConfig{
			RecordSQL: false,
		},
	}

	_, err = db.Exec("DROP TABLE IF EXISTS operation_logs")
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE operation_logs (
		id bigint(20) NOT NULL AUTO_INCREMENT,
		log_id varchar(64) NOT NULL,
		operator_type tinyint(4) NOT NULL DEFAULT '0',
		operator_id bigint(20) NOT NULL DEFAULT '0',
		operator_name varchar(255) NOT NULL DEFAULT '',
		action varchar(32) NOT NULL,
		resource_type varchar(64) NOT NULL,
		resource_id varchar(255) NOT NULL DEFAULT '',
		resource_name varchar(512) NOT NULL DEFAULT '',
		resource_parent_id varchar(255) NOT NULL DEFAULT '',
		status tinyint(4) NOT NULL DEFAULT '1',
		error_msg varchar(1024) NOT NULL DEFAULT '',
		change_summary mediumtext,
		request_path varchar(512) NOT NULL DEFAULT '',
		request_method varchar(16) NOT NULL DEFAULT '',
		client_ip varchar(64) NOT NULL DEFAULT '',
		user_agent varchar(512) NOT NULL DEFAULT '',
		created_at datetime NOT NULL,
		PRIMARY KEY (id),
		KEY idx_created_at (created_at),
		KEY idx_operator_name_created (operator_name, created_at),
		KEY idx_status_created (status, created_at),
		KEY idx_resource_name_created (resource_name, created_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	require.NoError(t, err)

	// 43 rows: 40 by "admin" and 3 by "bob". created_at is quantized to
	// 10-second buckets so every bucket holds ~14 same-second rows, matching
	// the async batch-flush pattern of the real table.
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	for i := 0; i < 43; i++ {
		operator := "admin"
		if i >= 40 {
			operator = "bob"
		}
		createdAt := base.Add(time.Duration(i/14*10) * time.Second)
		_, err = db.Exec(`INSERT INTO operation_logs
			(log_id, operator_type, operator_id, operator_name, action, resource_type,
			 resource_id, resource_name, resource_parent_id, status, error_msg,
			 change_summary, request_path, request_method, client_ip, user_agent, created_at)
			VALUES (?, 0, 1, ?, 'create', 'entity', ?, ?, '', 1, '', '{}', '/p', 'POST', '127.0.0.1', 'agent', ?)`,
			fmt.Sprintf("log-%d", i), operator, fmt.Sprintf("res-%d", i),
			fmt.Sprintf("name-%d", i), createdAt)
		require.NoError(t, err)
	}
	_, err = db.Exec("ANALYZE TABLE operation_logs")
	require.NoError(t, err)

	dbCtx := lib.NewDBContext(context.Background(), db)
	return dbCtx, func() {
		db.Exec("DROP TABLE IF EXISTS operation_logs")
		db.Close()
		stateful.DefaultConfig = oldConfig
	}
}

// pagedParam builds the same paging clause the storager derives from a filter.
func pagedParam(operatorName *string, offset, size uint) *TOperationLogParam {
	orderBy := "created_at DESC, id DESC"
	return &TOperationLogParam{
		OperatorName: operatorName,
		OrderBy:      &orderBy,
		Limit:        []uint{offset, size},
	}
}

func listIDs(list []*TOperationLog) []int64 {
	ids := make([]int64, 0, len(list))
	for _, one := range list {
		ids = append(ids, one.ID)
	}
	return ids
}

// TestTOperationLogListDeferredMatchesLegacyPages compares the deferred-join
// page results against the legacy SELECT * path page by page, including the
// last and beyond-last pages (issue #275 regression anchor).
func TestTOperationLogListDeferredMatchesLegacyPages(t *testing.T) {
	dbCtx, cleanup := setupOperationLogMySQLTestDB(t)
	defer cleanup()

	const pageSize = 7
	for page := 1; page <= 8; page++ {
		offset := uint((page - 1) * pageSize)
		legacy, err := TOperationLogList(dbCtx, pagedParam(nil, offset, pageSize))
		require.NoError(t, err)
		deferred, err := TOperationLogListDeferred(dbCtx, pagedParam(nil, offset, pageSize))
		require.NoError(t, err)
		assert.Equal(t, listIDs(legacy), listIDs(deferred), "page %d", page)
	}
}

func TestTOperationLogListDeferredFilteredPages(t *testing.T) {
	dbCtx, cleanup := setupOperationLogMySQLTestDB(t)
	defer cleanup()

	admin := "admin"
	total, err := TOperationLogCount(dbCtx, pagedParam(&admin, 0, 10))
	require.NoError(t, err)
	assert.Equal(t, int64(40), total)

	const pageSize = 10
	for page := 1; page <= 5; page++ {
		offset := uint((page - 1) * pageSize)
		legacy, err := TOperationLogList(dbCtx, pagedParam(&admin, offset, pageSize))
		require.NoError(t, err)
		deferred, err := TOperationLogListDeferred(dbCtx, pagedParam(&admin, offset, pageSize))
		require.NoError(t, err)
		assert.Equal(t, listIDs(legacy), listIDs(deferred), "page %d", page)
	}

	// Beyond the filtered set: empty page, not an error.
	deferred, err := TOperationLogListDeferred(dbCtx, pagedParam(&admin, 1000, 10))
	require.NoError(t, err)
	assert.Empty(t, deferred)
}

func TestTOperationLogListDeferredOrdering(t *testing.T) {
	dbCtx, cleanup := setupOperationLogMySQLTestDB(t)
	defer cleanup()

	deferred, err := TOperationLogListDeferred(dbCtx, pagedParam(nil, 0, 43))
	require.NoError(t, err)
	require.Len(t, deferred, 43)

	for i := 1; i < len(deferred); i++ {
		prev, cur := deferred[i-1], deferred[i]
		assert.True(t,
			prev.CreatedAt.After(cur.CreatedAt) ||
				(prev.CreatedAt.Equal(cur.CreatedAt) && prev.ID > cur.ID),
			"row %d breaks created_at DESC, id DESC order", i)
	}
}
