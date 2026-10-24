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
	"runtime"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// batchJobLockName 是控制库 MySQL named lock（GET_LOCK）名，多副本 api
// 实例每 tick 抢锁互斥（锁失效双跑时全部写操作幂等收敛，不双倍结算，
// 见批量任务与对账.md §8）。SQLite 模式 named lock 退化为空操作、约定
// 单实例（与现有报表 job 同一约定）。
const batchJobLockName = "batch_reconcile_job"

// ReconcileManager 是 job 面向的管理器抽象（*Manager 满足），便于测试。
type ReconcileManager interface {
	SyncFromRedis(ctx context.Context) (int, int, error)
	AdvanceStatus(ctx context.Context) (int, error)
	BackfillTerminalArtifacts(ctx context.Context) (int, error)
	ReconcileSettle(ctx context.Context) (int, int, error)
}

var _ ReconcileManager = (*Manager)(nil)

// Job 批量对账 job：每分钟 tick（可配），每 tick 抢控制库 named lock 后
// 依次执行 Redis 同步落库 → 状态推进 → 兜底结算。三职责限量、错误与
// panic 隔离（单职责失败/ panic 不影响其余职责与后续 tick，批量任务与
// 异步任务支持一期 change-summary.md 阶段 6）。
type Job struct {
	manager  ReconcileManager
	db       *sql.DB
	interval time.Duration
	useMySQL bool

	stopCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// NewJob 创建对账 job。db 为控制库连接池；interval <= 0 时取默认 1min。
// MySQL 后端启用 named lock 互斥，其余后端（SQLite）退化为单实例空操作锁。
func NewJob(manager ReconcileManager, db *sql.DB, interval time.Duration) *Job {
	if interval <= 0 {
		interval = time.Minute
	}
	j := &Job{
		manager:  manager,
		db:       db,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
	if db != nil {
		_, j.useMySQL = db.Driver().(*mysql.MySQLDriver)
	}
	return j
}

// Start 启动 tick 循环（立即执行一轮后按间隔 tick），进程生命周期跟随
// （与 quota.QuotaResetScheduler / mysqlreport.Job 同一接线惯例）。
func (j *Job) Start() {
	j.startOnce.Do(func() {
		j.wg.Add(1)
		go j.loop()
	})
}

// Stop 停止 tick 循环并等待当前轮退出。
func (j *Job) Stop() {
	j.stopOnce.Do(func() {
		close(j.stopCh)
	})
	j.wg.Wait()
}

func (j *Job) loop() {
	defer j.wg.Done()
	defer recoverJobPanic("batch reconcile job loop")

	j.runCycle()

	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			j.runCycle()
		case <-j.stopCh:
			return
		}
	}
}

// runCycle 执行一轮三职责（各自 recover + 错误隔离）。
func (j *Job) runCycle() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := j.RunOnce(ctx); err != nil {
		jobLogf("batch reconcile cycle failed: %v", err)
	}
}

// RunOnce 抢锁并执行一轮三职责；锁未抢到返回 nil（其余实例在跑）。
// 返回错误仅用于连接/锁获取失败等基础设施问题（调用方告警）。
func (j *Job) RunOnce(ctx context.Context) error {
	if j.manager == nil || j.db == nil {
		return nil
	}

	if j.useMySQL {
		conn, err := j.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()

		locked, err := acquireJobNamedLock(ctx, conn, batchJobLockName)
		if err != nil {
			return err
		}
		if !locked {
			jobLogf("batch reconcile job: lock %s not acquired, skip cycle", batchJobLockName)
			return nil
		}
		defer releaseJobNamedLock(conn, batchJobLockName)
	}

	j.runDuty("sync from redis", func(ctx context.Context) error {
		tasks, files, err := j.manager.SyncFromRedis(ctx)
		if err != nil {
			return err
		}
		jobLogf("batch reconcile: synced tasks=%d files=%d", tasks, files)
		return nil
	})
	j.runDuty("advance status", func(ctx context.Context) error {
		advanced, err := j.manager.AdvanceStatus(ctx)
		if err != nil {
			return err
		}
		jobLogf("batch reconcile: advanced=%d", advanced)
		return nil
	})
	j.runDuty("backfill terminal artifacts", func(ctx context.Context) error {
		backfilled, err := j.manager.BackfillTerminalArtifacts(ctx)
		if err != nil {
			return err
		}
		jobLogf("batch reconcile: backfilled=%d", backfilled)
		return nil
	})
	j.runDuty("reconcile settle", func(ctx context.Context) error {
		settled, released, err := j.manager.ReconcileSettle(ctx)
		if err != nil {
			return err
		}
		jobLogf("batch reconcile: settled=%d released=%d", settled, released)
		return nil
	})
	return nil
}

// runDuty 单职责执行器：panic recover + 错误告警，绝不上传中断循环。
func (j *Job) runDuty(name string, do func(ctx context.Context) error) {
	defer recoverJobPanic("batch reconcile " + name)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := do(ctx); err != nil {
		jobLogf("batch reconcile duty %s failed: %v", name, err)
	}
}

func acquireJobNamedLock(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var res sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", name).Scan(&res); err != nil {
		return false, err
	}
	return res.Valid && res.Int64 == 1, nil
}

func releaseJobNamedLock(conn *sql.Conn, name string) {
	if _, err := conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", name); err != nil {
		jobLogf("batch reconcile job: release lock %s failed: %v", name, err)
	}
}

func recoverJobPanic(name string) {
	if err := recover(); err != nil {
		stack := make([]byte, 1024*8)
		stack = stack[:runtime.Stack(stack, false)]
		jobLogf("PANIC in %s: err=%v\n%s", name, err, string(stack))
	}
}

// jobLogf 容忍单测环境的 nil logger（生产经 stateful.InitLog 初始化）。
func jobLogf(format string, args ...interface{}) {
	if stateful.AccessLogger != nil {
		stateful.AccessLogger.Warn(format, args...)
	}
}
