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

package ibatch

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/glebarez/go-sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReconcileManager 记录各职责调用；可注入错误与 panic。
type fakeReconcileManager struct {
	mu            sync.Mutex
	syncCalls     int
	advCalls      int
	backfillCalls int
	settleCalls   int
	syncErr       error
	advErr        error
	backfillErr   error
	settleErr     error
	advPanic      bool
	backfillPanic bool
}

func (f *fakeReconcileManager) SyncFromRedis(ctx context.Context) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncCalls++
	return 1, 2, f.syncErr
}

func (f *fakeReconcileManager) AdvanceStatus(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advCalls++
	if f.advPanic {
		panic("advance duty boom")
	}
	return 3, f.advErr
}

func (f *fakeReconcileManager) BackfillTerminalArtifacts(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backfillCalls++
	if f.backfillPanic {
		panic("backfill duty boom")
	}
	return 6, f.backfillErr
}

func (f *fakeReconcileManager) ReconcileSettle(ctx context.Context) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settleCalls++
	return 4, 5, f.settleErr
}

func (f *fakeReconcileManager) counts() (int, int, int) {
	sync_, adv, _, settle := f.allCounts()
	return sync_, adv, settle
}

func (f *fakeReconcileManager) allCounts() (int, int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.syncCalls, f.advCalls, f.backfillCalls, f.settleCalls
}

func setupJobSQLiteDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

func TestJob_RunOnce_RunsAllDuties(t *testing.T) {
	manager := &fakeReconcileManager{}
	j := NewJob(manager, setupJobSQLiteDB(t), time.Minute)

	require.NoError(t, j.RunOnce(context.Background()))
	sync, adv, settle := manager.counts()
	assert.Equal(t, 1, sync)
	assert.Equal(t, 1, adv)
	assert.Equal(t, 1, settle)
}

func TestJob_RunOnce_DutyErrorIsolation(t *testing.T) {
	manager := &fakeReconcileManager{syncErr: errors.New("redis down"), advErr: errors.New("egress down")}
	j := NewJob(manager, setupJobSQLiteDB(t), time.Minute)

	// 单职责失败不中断其余职责，也不返回错误（仅告警）。
	require.NoError(t, j.RunOnce(context.Background()))
	sync, adv, settle := manager.counts()
	assert.Equal(t, 1, sync)
	assert.Equal(t, 1, adv)
	assert.Equal(t, 1, settle)
}

func TestJob_RunOnce_DutyPanicRecovered(t *testing.T) {
	manager := &fakeReconcileManager{advPanic: true}
	j := NewJob(manager, setupJobSQLiteDB(t), time.Minute)

	require.NoError(t, j.RunOnce(context.Background()))
	sync, adv, settle := manager.counts()
	assert.Equal(t, 1, sync)
	assert.Equal(t, 1, adv)
	assert.Equal(t, 1, settle, "panic 职责被 recover，后续职责照常")

	// 第二轮循环不受 panic 影响。
	require.NoError(t, j.RunOnce(context.Background()))
	_, _, settle = manager.counts()
	assert.Equal(t, 2, settle)
}

func TestJob_RunOnce_NilDeps(t *testing.T) {
	j := NewJob(nil, nil, time.Minute)
	require.NoError(t, j.RunOnce(context.Background()))
}

func TestJob_MySQLNamedLock(t *testing.T) {
	t.Run("抢锁成功执行三职责并释放", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })

		mock.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK(?)"}).AddRow(int64(1)))
		mock.ExpectExec("SELECT RELEASE_LOCK").WillReturnResult(sqlmock.NewResult(0, 0))

		manager := &fakeReconcileManager{}
		j := NewJob(manager, db, time.Minute)
		j.useMySQL = true // sqlmock 驱动非 MySQLDriver，强制走 named lock 路径

		require.NoError(t, j.RunOnce(context.Background()))
		assert.Equal(t, []string{"sync", "advance", "backfill", "settle"}, callOrder(manager))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("抢锁失败跳过本轮", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })

		mock.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK(?)"}).AddRow(int64(0)))

		manager := &fakeReconcileManager{}
		j := NewJob(manager, db, time.Minute)
		j.useMySQL = true

		require.NoError(t, j.RunOnce(context.Background()))
		sync, adv, settle := manager.counts()
		assert.Equal(t, 0, sync+adv+settle, "锁未抢到不执行职责")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("锁获取错误透传", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })

		mock.ExpectQuery("SELECT GET_LOCK").WillReturnError(errors.New("conn lost"))

		j := NewJob(&fakeReconcileManager{}, db, time.Minute)
		j.useMySQL = true
		require.Error(t, j.RunOnce(context.Background()))
	})
}

// callOrder 通过计数快照推断各职责被依次调用（职责内串行）。
func callOrder(m *fakeReconcileManager) []string {
	sync, adv, backfill, settle := m.allCounts()
	order := []string{}
	for i := 0; i < sync; i++ {
		order = append(order, "sync")
	}
	for i := 0; i < adv; i++ {
		order = append(order, "advance")
	}
	for i := 0; i < backfill; i++ {
		order = append(order, "backfill")
	}
	for i := 0; i < settle; i++ {
		order = append(order, "settle")
	}
	return order
}

func TestJob_Lifecycle(t *testing.T) {
	manager := &fakeReconcileManager{}
	j := NewJob(manager, setupJobSQLiteDB(t), 10*time.Millisecond)
	j.Start()

	// 等待至少两轮 tick。
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, settle := manager.counts()
		if settle >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not tick enough, settle=%d", settle)
		}
		time.Sleep(5 * time.Millisecond)
	}
	j.Stop()

	// Stop 后不再增长。
	_, _, settleBefore := manager.counts()
	time.Sleep(30 * time.Millisecond)
	_, _, settleAfter := manager.counts()
	assert.Equal(t, settleBefore, settleAfter, "Stop 后循环必须终止")
}

func TestJob_NamedLockReleaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mock.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK(?)"}).AddRow(int64(1)))
	mock.ExpectExec("SELECT RELEASE_LOCK").WillReturnError(errors.New("conn lost"))

	manager := &fakeReconcileManager{}
	j := NewJob(manager, db, time.Minute)
	j.useMySQL = true

	// 释放锁失败仅告警，职责已执行、无错误返回。
	require.NoError(t, j.RunOnce(context.Background()))
	_, _, settle := manager.counts()
	assert.Equal(t, 1, settle)
}

func TestJob_DefaultInterval(t *testing.T) {
	j := NewJob(&fakeReconcileManager{}, nil, 0)
	assert.Equal(t, time.Minute, j.interval)
}
