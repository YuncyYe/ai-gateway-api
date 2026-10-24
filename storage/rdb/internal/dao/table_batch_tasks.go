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
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao/internal"
)

const tBatchTaskTableName = "batch_tasks"

// TBatchTask maps to batch_tasks table.
type TBatchTask struct {
	ID                int64      `db:"id"`
	BatchID           string     `db:"batch_id"`
	APIKeyID          string     `db:"api_key_id"`
	ProductName       string     `db:"product_name"`
	EntityID          string     `db:"entity_id"`
	Provider          string     `db:"provider"`
	KeyName           string     `db:"key_name"`
	Endpoint          string     `db:"endpoint"`
	InputFileID       string     `db:"input_file_id"`
	OutputFileID      string     `db:"output_file_id"`
	Status            string     `db:"status"`
	RequestCounts     string     `db:"request_counts"`
	EstLines          int64      `db:"est_lines"`
	UsageInputTokens  int64      `db:"usage_input_tokens"`
	UsageOutputTokens int64      `db:"usage_output_tokens"`
	UsageSource       string     `db:"usage_source"`
	ReserveUnits      int64      `db:"reserve_units"`
	SettleUnits       int64      `db:"settle_units"`
	SettleStatus      string     `db:"settle_status"`
	OverReserved      bool       `db:"over_reserved"`
	SyncSource        string     `db:"sync_source"`
	IdemKey           string     `db:"idem_key"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
	TerminalAt        *time.Time `db:"terminal_at"`
}

// TBatchTaskOne Query One
// return nil, nil if record not existed
func TBatchTaskOne(dbCtx lib.DBContexter, where *TBatchTaskParam) (*TBatchTask, error) {
	t := &TBatchTask{}
	err := internal.QueryOne(dbCtx, tBatchTaskTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TBatchTaskList Query Multiple
func TBatchTaskList(dbCtx lib.DBContexter, where *TBatchTaskParam) ([]*TBatchTask, error) {
	t := []*TBatchTask{}
	err := internal.QueryList(dbCtx, tBatchTaskTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TBatchTaskParam 查询/写入参数。nil 指针字段在 WHERE 中忽略、在 SET 中
// 跳过（部分更新语义，见 details/部分更新语义与DAO-nil-skip约定.md）。
type TBatchTaskParam struct {
	ID                *int64     `db:"id"`
	IDGT              *int64     `db:"id,>"`
	IDLT              *int64     `db:"id,<"`
	BatchID           *string    `db:"batch_id"`
	APIKeyID          *string    `db:"api_key_id"`
	ProductName       *string    `db:"product_name"`
	EntityID          *string    `db:"entity_id"`
	Provider          *string    `db:"provider"`
	KeyName           *string    `db:"key_name"`
	Endpoint          *string    `db:"endpoint"`
	InputFileID       *string    `db:"input_file_id"`
	OutputFileID      *string    `db:"output_file_id"`
	Status            *string    `db:"status"`
	StatusIn          []string   `db:"status,in"`
	StatusNotIn       []string   `db:"status,not in"`
	RequestCounts     *string    `db:"request_counts"`
	EstLines          *int64     `db:"est_lines"`
	UsageInputTokens  *int64     `db:"usage_input_tokens"`
	UsageOutputTokens *int64     `db:"usage_output_tokens"`
	UsageSource       *string    `db:"usage_source"`
	ReserveUnits      *int64     `db:"reserve_units"`
	SettleUnits       *int64     `db:"settle_units"`
	SettleStatus      *string    `db:"settle_status"`
	OverReserved      *bool      `db:"over_reserved"`
	SyncSource        *string    `db:"sync_source"`
	IdemKey           *string    `db:"idem_key"`
	CreatedAt         *time.Time `db:"created_at"`
	CreatedAtGTE      *time.Time `db:"created_at,>="`
	CreatedAtLTE      *time.Time `db:"created_at,<="`
	UpdatedAt         *time.Time `db:"updated_at"`
	TerminalAt        *time.Time `db:"terminal_at"`

	OrderBy *string `db:"_orderby"`
	Limit   []uint  `db:"_limit"`
}

// TBatchTaskCreate One/Multiple
func TBatchTaskCreate(dbCtx lib.DBContexter, data ...*TBatchTaskParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tBatchTaskTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tBatchTaskTableName, list...)
}

// TBatchTaskUpdate Update matched rows
func TBatchTaskUpdate(dbCtx lib.DBContexter, val, where *TBatchTaskParam) (int64, error) {
	return internal.Update(dbCtx, tBatchTaskTableName, where, val)
}

// TBatchTaskUpsertByIdemKey 按 idem_key（= batch_id，唯一键）幂等 upsert：
// MySQL ON DUPLICATE KEY UPDATE / SQLite ON CONFLICT(idem_key) DO UPDATE。
// 已存在行只刷新可变同步字段（status/output_file_id/request_counts/
// usage_*/reserve_units/settle_units/terminal_at/updated_at）；归属类
// 列（api_key_id/product_name/provider/key_name/endpoint/input_file_id/
// sync_source/usage_source/created_at）为 insert 时事实，不回退。
// 返回自增 ID（冲突更新时为 0）。
func TBatchTaskUpsertByIdemKey(dbCtx lib.DBContexter, data *TBatchTaskParam) (int64, error) {
	if data.IdemKey == nil || *data.IdemKey == "" {
		return 0, xerror.WrapParamErrorWithMsg("idem_key is required for upsert")
	}
	if data.CreatedAt == nil {
		data.CreatedAt = internal.PTimeNow()
	}
	if data.UpdatedAt == nil {
		data.UpdatedAt = internal.PTimeNow()
	}

	args := []interface{}{
		data.BatchID, data.APIKeyID, data.ProductName, data.EntityID, data.Provider,
		data.KeyName, data.Endpoint, data.InputFileID, data.OutputFileID, data.Status,
		data.RequestCounts, data.EstLines, data.UsageInputTokens, data.UsageOutputTokens,
		data.UsageSource, data.ReserveUnits, data.SettleUnits, data.SettleStatus,
		data.OverReserved, data.SyncSource, data.IdemKey,
		data.CreatedAt, data.UpdatedAt, data.TerminalAt,
	}

	var query string
	if isMySQL(dbCtx.Conn()) {
		query = `INSERT INTO batch_tasks
(batch_id, api_key_id, product_name, entity_id, provider, key_name, endpoint, input_file_id, output_file_id, status, request_counts, est_lines, usage_input_tokens, usage_output_tokens, usage_source, reserve_units, settle_units, settle_status, over_reserved, sync_source, idem_key, created_at, updated_at, terminal_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE
status=VALUES(status), output_file_id=VALUES(output_file_id), request_counts=VALUES(request_counts),
est_lines=VALUES(est_lines), usage_input_tokens=VALUES(usage_input_tokens), usage_output_tokens=VALUES(usage_output_tokens),
reserve_units=VALUES(reserve_units), settle_units=VALUES(settle_units), settle_status=VALUES(settle_status),
terminal_at=VALUES(terminal_at), updated_at=VALUES(updated_at)`
	} else {
		query = `INSERT INTO batch_tasks
(batch_id, api_key_id, product_name, entity_id, provider, key_name, endpoint, input_file_id, output_file_id, status, request_counts, est_lines, usage_input_tokens, usage_output_tokens, usage_source, reserve_units, settle_units, settle_status, over_reserved, sync_source, idem_key, created_at, updated_at, terminal_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(idem_key) DO UPDATE SET
status=excluded.status, output_file_id=excluded.output_file_id, request_counts=excluded.request_counts,
est_lines=excluded.est_lines, usage_input_tokens=excluded.usage_input_tokens, usage_output_tokens=excluded.usage_output_tokens,
reserve_units=excluded.reserve_units, settle_units=excluded.settle_units, settle_status=excluded.settle_status,
terminal_at=excluded.terminal_at, updated_at=excluded.updated_at`
	}

	rst, err := dbCtx.Execer().ExecContext(dbCtx, query, args...)
	if err != nil {
		return 0, xerror.WrapDaoError(err)
	}
	return rst.LastInsertId()
}

// TBatchTaskPreemptCancel cancel 抢占式条件更新：
// SET status='cancelling', updated_at=now
// WHERE batch_id=? AND product_name=? AND status NOT IN (excluded...)。
// excluded 由调用方组装（终态 + cancelling，见 storage/rdb/batch），
// 保证双方言下"仅首个转换者受理成功"的语义（MySQL 同值 UPDATE
// affected=0 vs SQLite changes() 按命中计数的差异被 WHERE 消除）。
// 返回 affected rows（0 = 已终态/受理中/无行 → 409）。
func TBatchTaskPreemptCancel(dbCtx lib.DBContexter, batchID, productName string, terminal []string, now time.Time) (int64, error) {
	notIn := make([]interface{}, 0, len(terminal))
	for _, s := range terminal {
		notIn = append(notIn, s)
	}
	where := map[string]interface{}{
		"batch_id":      batchID,
		"product_name":  productName,
		"status not in": notIn,
	}
	assign := map[string]interface{}{
		"status":     "cancelling",
		"updated_at": now,
	}
	return execUpdate(dbCtx, tBatchTaskTableName, where, assign)
}

// TBatchTaskUpdateSettleStatus 三态对账条件更新：
// WHERE batch_id=? AND product_name=? AND settle_status=<fromStatus>，
// SET 为 assign 非 nil 字段（调用方组装）。返回 affected rows。
func TBatchTaskUpdateSettleStatus(dbCtx lib.DBContexter, batchID, productName, fromStatus string, assign *TBatchTaskParam) (int64, error) {
	where := map[string]interface{}{
		"batch_id":      batchID,
		"product_name":  productName,
		"settle_status": fromStatus,
	}
	return execUpdate(dbCtx, tBatchTaskTableName, where, internal.Struct2Assign(assign))
}

// execUpdate 以字面 where/assign map 执行 UPDATE（gendry builder），
// SQL 记录与 internal.Update 一致。供无法用语义化 where 结构表达的条件
// 更新使用（status NOT IN 抢占、settle_status 条件推进）。
func execUpdate(dbCtx lib.DBContexter, table string, where, assign map[string]interface{}) (int64, error) {
	build := internal.NewUpdateBuilder(table, where, assign)
	sqlText, args, err := build.Compile()
	if err != nil {
		return 0, xerror.WrapDaoError(err)
	}

	now := time.Now()
	rst, err := dbCtx.Execer().ExecContext(dbCtx, sqlText, args...)
	sr := &stateful.SQLRecord{
		SQL:  sqlText,
		Args: args,
		Err:  err,
		Cost: time.Since(now),
	}
	defer sr.Print(dbCtx)
	if err != nil {
		return 0, xerror.WrapDaoError(err)
	}
	rows, err := rst.RowsAffected()
	if err != nil {
		return 0, xerror.WrapDaoError(err)
	}
	return rows, nil
}
