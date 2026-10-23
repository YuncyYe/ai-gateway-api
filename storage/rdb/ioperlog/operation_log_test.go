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
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/bfenetworks/bfe/bfe_util/redis_client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

func setupStoragerMock(t *testing.T) (*OperationLogStorager, sqlmock.Sqlmock, func()) {
	return setupStoragerMockWithRedis(t, nil)
}

func setupStoragerMockWithRedis(t *testing.T, client redis_client.Client) (*OperationLogStorager, sqlmock.Sqlmock, func()) {
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
	storager := NewOperationLogStorager(factory, client)
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

const (
	operlogCountDeferredQuery = `SELECT t\.\* FROM operation_logs t JOIN \(SELECT id FROM operation_logs WHERE \(operator_name=\?\) ORDER BY created_at DESC, id DESC LIMIT \?,\?\) AS x ON t\.id = x\.id ORDER BY t\.created_at DESC, t\.id DESC`
	operlogCountQuery         = `SELECT count\(\*\) FROM operation_logs WHERE \(operator_name=\?\)`
)

func oplogRow(id int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "log_id", "operator_type", "operator_id", "operator_name",
		"action", "resource_type", "resource_id", "resource_name",
		"resource_parent_id", "status", "error_msg", "change_summary",
		"request_path", "request_method", "client_ip", "user_agent", "created_at",
	}).AddRow(
		id, "log-1", 0, 1, "admin", "create", "entity", "res-1", "name-1",
		"", 1, "", `{"after":{}}`, "/p", "POST", "127.0.0.1", "agent",
		time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local),
	)
}

func TestListPagedCountServedFromCache(t *testing.T) {
	storager, mock, cleanup := setupStoragerMockWithRedis(t, stateful.NewMockRedisClient())
	defer cleanup()

	// Page 3: live count, then the deferred-join page query.
	mock.ExpectQuery(operlogCountQuery).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(100))
	mock.ExpectQuery(operlogCountDeferredQuery).
		WithArgs("admin", 40, 20).
		WillReturnRows(oplogRow(100))

	// Page 4, same filter: the count must come from the cache (issue #280
	// acceptance: no repeated count while paging one filtered result set).
	mock.ExpectQuery(operlogCountDeferredQuery).
		WithArgs("admin", 60, 20).
		WillReturnRows(oplogRow(80))

	_, total, err := storager.List(context.Background(), pagedFilter(3, 20, "admin"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), total)

	_, total, err = storager.List(context.Background(), pagedFilter(4, 20, "admin"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), total)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListCountRefreshedAfterBatchCreate(t *testing.T) {
	storager, mock, cleanup := setupStoragerMockWithRedis(t, stateful.NewMockRedisClient())
	defer cleanup()

	mock.ExpectQuery(operlogCountQuery).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(100))
	mock.ExpectQuery(operlogCountDeferredQuery).
		WithArgs("admin", 0, 20).
		WillReturnRows(oplogRow(100))

	insertArgs := make([]driver.Value, 17) // full column set, see entryToParam
	for i := range insertArgs {
		insertArgs[i] = sqlmock.AnyArg()
	}
	mock.ExpectExec(`INSERT INTO operation_logs`).
		WithArgs(insertArgs...).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// A successful flush bumps the generation, so the cached total for the
	// same filter must not be served again.
	mock.ExpectQuery(operlogCountQuery).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(101))
	mock.ExpectQuery(operlogCountDeferredQuery).
		WithArgs("admin", 20, 20).
		WillReturnRows(oplogRow(99))

	_, total, err := storager.List(context.Background(), pagedFilter(1, 20, "admin"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), total)

	entry := &ioperlog.OperationLogEntry{OperatorName: "ops"}
	require.NoError(t, storager.BatchCreate(context.Background(), []*ioperlog.OperationLogEntry{entry}))

	_, total, err = storager.List(context.Background(), pagedFilter(2, 20, "admin"))
	require.NoError(t, err)
	assert.Equal(t, int64(101), total)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListCountFallsBackToLiveCountWhenRedisDown(t *testing.T) {
	redis := &scriptedRedis{
		MockRedisClient: stateful.NewMockRedisClient(),
		getInt64: func(key string) (int64, error) {
			return 0, errors.New("connection refused")
		},
	}
	storager, mock, cleanup := setupStoragerMockWithRedis(t, redis)
	defer cleanup()

	// The cache must degrade to the live count, never fail the request.
	mock.ExpectQuery(operlogCountQuery).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(100))
	mock.ExpectQuery(operlogCountDeferredQuery).
		WithArgs("admin", 0, 20).
		WillReturnRows(oplogRow(100))

	_, total, err := storager.List(context.Background(), pagedFilter(1, 20, "admin"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), total)
	assert.NoError(t, mock.ExpectationsWereMet())
}
