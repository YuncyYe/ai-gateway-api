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
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

func setupOperationLogMock(t *testing.T) (lib.DBContexter, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	oldConfig := stateful.DefaultConfig
	stateful.DefaultConfig = &stateful.Config{
		RunTime: stateful.RunTimeConfig{
			RecordSQL: false,
		},
	}

	dbCtx := lib.NewDBContext(context.Background(), db)
	return dbCtx, mock, func() {
		db.Close()
		stateful.DefaultConfig = oldConfig
	}
}

func operationLogMockRow(id int64) *sqlmock.Rows {
	createdAt := time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)
	return sqlmock.NewRows([]string{
		"id", "log_id", "operator_type", "operator_id", "operator_name",
		"action", "resource_type", "resource_id", "resource_name",
		"resource_parent_id", "status", "error_msg", "change_summary",
		"request_path", "request_method", "client_ip", "user_agent", "created_at",
	}).AddRow(
		id, "log-1", 0, 1, "admin", "create", "entity", "res-1", "name-1",
		"", 1, "", `{"after":{}}`, "/open-api/v1/entities", "POST",
		"127.0.0.1", "agent", createdAt,
	)
}

func TestTOperationLogListDeferred(t *testing.T) {
	dbCtx, mock, cleanup := setupOperationLogMock(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT t\.\* FROM operation_logs t JOIN \(SELECT id FROM operation_logs WHERE \(operator_name=\?\) ORDER BY created_at DESC, id DESC LIMIT \?,\?\) AS x ON t\.id = x\.id ORDER BY t\.created_at DESC, t\.id DESC`).
		WithArgs("admin", 40, 20).
		WillReturnRows(operationLogMockRow(100))

	operatorName := "admin"
	orderBy := "created_at DESC, id DESC"
	list, err := TOperationLogListDeferred(dbCtx, &TOperationLogParam{
		OperatorName: &operatorName,
		OrderBy:      &orderBy,
		Limit:        []uint{40, 20},
	})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int64(100), list[0].ID)
	assert.Equal(t, "admin", list[0].OperatorName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTOperationLogListDeferred_EmptyPage(t *testing.T) {
	dbCtx, mock, cleanup := setupOperationLogMock(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT t\.\* FROM operation_logs t JOIN \(SELECT id FROM operation_logs WHERE \(operator_name=\?\) ORDER BY created_at DESC, id DESC LIMIT \?,\?\) AS x ON t\.id = x\.id ORDER BY t\.created_at DESC, t\.id DESC`).
		WithArgs("nobody", 1000000, 20).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	operatorName := "nobody"
	orderBy := "created_at DESC, id DESC"
	list, err := TOperationLogListDeferred(dbCtx, &TOperationLogParam{
		OperatorName: &operatorName,
		OrderBy:      &orderBy,
		Limit:        []uint{1000000, 20},
	})
	require.NoError(t, err)
	assert.Empty(t, list)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTOperationLogCount(t *testing.T) {
	dbCtx, mock, cleanup := setupOperationLogMock(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM operation_logs WHERE \(operator_name=\?\)`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(123))

	operatorName := "admin"
	total, err := TOperationLogCount(dbCtx, &TOperationLogParam{OperatorName: &operatorName})
	require.NoError(t, err)
	assert.Equal(t, int64(123), total)
	assert.NoError(t, mock.ExpectationsWereMet())
}
