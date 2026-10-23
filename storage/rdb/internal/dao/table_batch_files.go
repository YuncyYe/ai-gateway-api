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
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao/internal"
)

const tBatchFileTableName = "batch_files"

// TBatchFile maps to batch_files table.
type TBatchFile struct {
	ID          int64      `db:"id"`
	FileID      string     `db:"file_id"`
	Provider    string     `db:"provider"`
	KeyName     string     `db:"key_name"`
	APIKeyID    string     `db:"api_key_id"`
	ProductName string     `db:"product_name"`
	Direction   string     `db:"direction"`
	Purpose     string     `db:"purpose"`
	Filename    string     `db:"filename"`
	Lines       int64      `db:"line_count"`
	Bytes       int64      `db:"bytes"`
	FirstSeenAt *time.Time `db:"first_seen_at"`
	LastSeenAt  *time.Time `db:"last_seen_at"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
}

// TBatchFileOne Query One
// return nil, nil if record not existed
func TBatchFileOne(dbCtx lib.DBContexter, where *TBatchFileParam) (*TBatchFile, error) {
	t := &TBatchFile{}
	err := internal.QueryOne(dbCtx, tBatchFileTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TBatchFileList Query Multiple
func TBatchFileList(dbCtx lib.DBContexter, where *TBatchFileParam) ([]*TBatchFile, error) {
	t := []*TBatchFile{}
	err := internal.QueryList(dbCtx, tBatchFileTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TBatchFileParam 查询/写入参数（nil 字段语义同 TBatchTaskParam）。
type TBatchFileParam struct {
	ID          *int64     `db:"id"`
	FileID      *string    `db:"file_id"`
	Provider    *string    `db:"provider"`
	KeyName     *string    `db:"key_name"`
	APIKeyID    *string    `db:"api_key_id"`
	ProductName *string    `db:"product_name"`
	Direction   *string    `db:"direction"`
	Purpose     *string    `db:"purpose"`
	Filename    *string    `db:"filename"`
	Lines       *int64     `db:"line_count"`
	Bytes       *int64     `db:"bytes"`
	FirstSeenAt *time.Time `db:"first_seen_at"`
	LastSeenAt  *time.Time `db:"last_seen_at"`
	CreatedAt   *time.Time `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
	Limit   []uint  `db:"_limit"`
}

// TBatchFileCreate One/Multiple
func TBatchFileCreate(dbCtx lib.DBContexter, data ...*TBatchFileParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tBatchFileTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tBatchFileTableName, list...)
}

// TBatchFileUpsertByFileProvider 按 (file_id, provider) 唯一键幂等 upsert：
// MySQL ON DUPLICATE KEY UPDATE / SQLite ON CONFLICT(file_id, provider)
// DO UPDATE。已存在行刷新可变字段（key_name/api_key_id/product_name/
// direction/purpose/filename/line_count/bytes/last_seen_at/updated_at）；
// filename 冲突更新带 NULLIF+COALESCE 保护：新值为空（NULL 或 ''，
// 参数层空串经 PString 为非 nil）不覆盖已有值；
// first_seen_at 为 insert 时事实不回退。返回自增 ID（冲突更新时为 0）。
func TBatchFileUpsertByFileProvider(dbCtx lib.DBContexter, data *TBatchFileParam) (int64, error) {
	if data.FileID == nil || *data.FileID == "" || data.Provider == nil || *data.Provider == "" {
		return 0, xerror.WrapParamErrorWithMsg("file_id and provider are required for upsert")
	}
	if data.CreatedAt == nil {
		data.CreatedAt = internal.PTimeNow()
	}
	if data.UpdatedAt == nil {
		data.UpdatedAt = internal.PTimeNow()
	}

	args := []interface{}{
		data.FileID, data.Provider, data.KeyName, data.APIKeyID, data.ProductName,
		data.Direction, data.Purpose, data.Filename, data.Lines, data.Bytes,
		data.FirstSeenAt, data.LastSeenAt, data.CreatedAt, data.UpdatedAt,
	}

	var query string
	if isMySQL(dbCtx.Conn()) {
		query = `INSERT INTO batch_files
(file_id, provider, key_name, api_key_id, product_name, direction, purpose, filename, line_count, bytes, first_seen_at, last_seen_at, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE
key_name=VALUES(key_name), api_key_id=VALUES(api_key_id), product_name=VALUES(product_name),
direction=VALUES(direction), purpose=VALUES(purpose), filename=COALESCE(NULLIF(VALUES(filename), ''), filename),
line_count=VALUES(line_count), bytes=VALUES(bytes),
last_seen_at=VALUES(last_seen_at), updated_at=VALUES(updated_at)`
	} else {
		query = `INSERT INTO batch_files
(file_id, provider, key_name, api_key_id, product_name, direction, purpose, filename, line_count, bytes, first_seen_at, last_seen_at, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(file_id, provider) DO UPDATE SET
key_name=excluded.key_name, api_key_id=excluded.api_key_id, product_name=excluded.product_name,
direction=excluded.direction, purpose=excluded.purpose, filename=COALESCE(NULLIF(excluded.filename, ''), batch_files.filename),
line_count=excluded.line_count, bytes=excluded.bytes,
last_seen_at=excluded.last_seen_at, updated_at=excluded.updated_at`
	}

	rst, err := dbCtx.Execer().ExecContext(dbCtx, query, args...)
	if err != nil {
		return 0, xerror.WrapDaoError(err)
	}
	return rst.LastInsertId()
}
