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

package ioperlog

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

func setupStoragerMock(t *testing.T) (*OperationLogStorager, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	oldConfig := stateful.DefaultConfig
	stateful.DefaultConfig = &stateful.Config{
		RunTime: stateful.RunTimeConfig{
			RecordSQL: false,
		},
	}

	factory := lib.DBContextFactory(func(ctx context.Context, ops ...*lib.Op) (*lib.DBContext, error) {
		return lib.NewDBContext(ctx, db), nil
	})
	storager := NewOperationLogStorager(factory)
	return storager, mock, func() {
		db.Close()
		stateful.DefaultConfig = oldConfig
	}
}

func pagedFilter(page, pageSize int, operatorName string) *ioperlog.OperationLogFilter {
	name := operatorName
	return &ioperlog.OperationLogFilter{
		OperatorName: &name,
		Page:         &page,
		PageSize:     &pageSize,
	}
}

func TestListPagedUsesDeferredJoin(t *testing.T) {
	storager, mock, cleanup := setupStoragerMock(t)
	defer cleanup()

	// count(*) first, then the deferred-join page query.
	mock.ExpectQuery(`SELECT count\(\*\) FROM operation_logs WHERE \(operator_name=\?\)`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(100))
	mock.ExpectQuery(`SELECT t\.\* FROM operation_logs t JOIN \(SELECT id FROM operation_logs WHERE \(operator_name=\?\) ORDER BY created_at DESC, id DESC LIMIT \?,\?\) AS x ON t\.id = x\.id ORDER BY t\.created_at DESC, t\.id DESC`).
		WithArgs("admin", 40, 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "log_id", "operator_type", "operator_id", "operator_name",
			"action", "resource_type", "resource_id", "resource_name",
			"resource_parent_id", "status", "error_msg", "change_summary",
			"request_path", "request_method", "client_ip", "user_agent", "created_at",
		}).AddRow(
			100, "log-1", 0, 1, "admin", "create", "entity", "res-1", "name-1",
			"", 1, "", `{"after":{}}`, "/p", "POST", "127.0.0.1", "agent",
			time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local),
		))

	list, total, err := storager.List(context.Background(), pagedFilter(3, 20, "admin"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), total)
	require.Len(t, list, 1)
	assert.Equal(t, int64(100), list[0].ID)
	assert.Equal(t, map[string]interface{}{"after": map[string]interface{}{}}, list[0].ChangeSummary)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListBeyondLastPageSkipsListQuery(t *testing.T) {
	storager, mock, cleanup := setupStoragerMock(t)
	defer cleanup()

	// Only count(*) is expected: an offset beyond total must not touch the table.
	mock.ExpectQuery(`SELECT count\(\*\) FROM operation_logs WHERE \(operator_name=\?\)`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(10))

	list, total, err := storager.List(context.Background(), pagedFilter(100, 20, "admin"))
	require.NoError(t, err)
	assert.Equal(t, int64(10), total)
	assert.Empty(t, list)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListUnpagedUsesLegacyQuery(t *testing.T) {
	storager, mock, cleanup := setupStoragerMock(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM operation_logs WHERE \(operator_name=\?\)`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(1))
	mock.ExpectQuery(`SELECT \* FROM operation_logs WHERE \(operator_name=\?\)`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "log_id", "operator_type", "operator_id", "operator_name",
			"action", "resource_type", "resource_id", "resource_name",
			"resource_parent_id", "status", "error_msg", "change_summary",
			"request_path", "request_method", "client_ip", "user_agent", "created_at",
		}).AddRow(
			7, "log-7", 0, 1, "admin", "delete", "entity", "res-7", "name-7",
			"", 2, "boom", `{}`, "/p", "DELETE", "127.0.0.1", "agent",
			time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local),
		))

	name := "admin"
	filter := &ioperlog.OperationLogFilter{OperatorName: &name}
	list, total, err := storager.List(context.Background(), filter)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, int64(7), list[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
