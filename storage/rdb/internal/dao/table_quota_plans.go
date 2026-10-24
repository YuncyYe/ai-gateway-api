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
	"time"

	"github.com/shopspring/decimal"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao/internal"
)

const tQuotaPlanTableName = "quota_plans"

type TQuotaPlan struct {
	ID                    int64           `db:"id"`
	Unlimited             bool            `db:"unlimited"`
	PassWhenNoEnoughQuota bool            `db:"pass_when_no_enough_quota"`
	Quota                 decimal.Decimal `db:"quota"`
	Unit                  string          `db:"unit"`
	ResetPeriod           string          `db:"reset_period"`
	LastResetAt           *time.Time      `db:"last_reset_at"`
	CreatedAt             time.Time       `db:"created_at"`
	UpdatedAt             time.Time       `db:"updated_at"`
}

// TQuotaPlanOne Query One
// return nil, nil if record not existed
func TQuotaPlanOne(dbCtx lib.DBContexter, where *TQuotaPlanParam) (*TQuotaPlan, error) {
	t := &TQuotaPlan{}
	err := internal.QueryOne(dbCtx, tQuotaPlanTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TQuotaPlanList Query Multiple
func TQuotaPlanList(dbCtx lib.DBContexter, where *TQuotaPlanParam) ([]*TQuotaPlan, error) {
	t := []*TQuotaPlan{}
	err := internal.QueryList(dbCtx, tQuotaPlanTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TQuotaPlanParam struct {
	ID                    *int64           `db:"id"`
	Unlimited             *bool            `db:"unlimited"`
	PassWhenNoEnoughQuota *bool            `db:"pass_when_no_enough_quota"`
	Quota                 *decimal.Decimal `db:"quota"`
	Unit                  *string          `db:"unit"`
	ResetPeriod           *string          `db:"reset_period"`
	LastResetAt           *time.Time       `db:"last_reset_at"`
	CreatedAt             *time.Time       `db:"created_at"`
	UpdatedAt             *time.Time       `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TQuotaPlanCreate One/Multiple
func TQuotaPlanCreate(dbCtx lib.DBContexter, data ...*TQuotaPlanParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tQuotaPlanTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tQuotaPlanTableName, list...)
}

// TQuotaPlanUpdate Update One
func TQuotaPlanUpdate(dbCtx lib.DBContexter, val, where *TQuotaPlanParam) (int64, error) {
	return internal.Update(dbCtx, tQuotaPlanTableName, where, val)
}

// TQuotaPlanClaimPeriodReset 认领周期重置（issue #228）：仅当计划从未重置
// （last_reset_at IS NULL）或上次重置早于本周期起点时，将 last_reset_at
// 推进到 now。返回影响行数：1 表示认领成功，0 表示本周期已被认领。
// WHERE 需显式覆盖 NULL——三值逻辑下 `last_reset_at < ?` 对 NULL 行恒不成立，
// 曾导致标记永不落库、调度器每分钟重复重置。
func TQuotaPlanClaimPeriodReset(dbCtx lib.DBContexter, id int64, periodStart time.Time, now time.Time) (int64, error) {
	const query = "UPDATE quota_plans SET last_reset_at = ? WHERE id = ? AND (last_reset_at IS NULL OR last_reset_at < ?)"

	start := time.Now()
	rst, err := dbCtx.Execer().ExecContext(dbCtx, query, now, id, periodStart)
	sr := &stateful.SQLRecord{
		SQL:  query,
		Args: []interface{}{now, id, periodStart},
		Err:  err,
		Cost: time.Since(start),
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

// TQuotaPlanDelete Delete One/Multiple
func TQuotaPlanDelete(dbCtx lib.DBContexter, where *TQuotaPlanParam) (int64, error) {
	return internal.Delete(dbCtx, tQuotaPlanTableName, where)
}
