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

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao/internal"
)

const tRateLimitPolicyTableName = "rate_limit_policies"

type TRateLimitPolicy struct {
	ID             int64     `db:"id"`
	Enabled        bool      `db:"enabled"`
	MaxConcurrency int       `db:"max_concurrency"`
	TpmConfigs     string    `db:"tpm_configs"`
	RpmConfigs     string    `db:"rpm_configs"`
	BatchLimits    string    `db:"batch_limits"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}

// TRateLimitPolicyOne Query One
// return nil, nil if record not existed
func TRateLimitPolicyOne(dbCtx lib.DBContexter, where *TRateLimitPolicyParam) (*TRateLimitPolicy, error) {
	t := &TRateLimitPolicy{}
	err := internal.QueryOne(dbCtx, tRateLimitPolicyTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TRateLimitPolicyList Query Multiple
func TRateLimitPolicyList(dbCtx lib.DBContexter, where *TRateLimitPolicyParam) ([]*TRateLimitPolicy, error) {
	t := []*TRateLimitPolicy{}
	err := internal.QueryList(dbCtx, tRateLimitPolicyTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TRateLimitPolicyParam struct {
	ID             *int64     `db:"id"`
	Enabled        *bool      `db:"enabled"`
	MaxConcurrency *int       `db:"max_concurrency"`
	TpmConfigs     *string    `db:"tpm_configs"`
	RpmConfigs     *string    `db:"rpm_configs"`
	BatchLimits    *string    `db:"batch_limits"`
	CreatedAt      *time.Time `db:"created_at"`
	UpdatedAt      *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TRateLimitPolicyCreate One/Multiple
func TRateLimitPolicyCreate(dbCtx lib.DBContexter, data ...*TRateLimitPolicyParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tRateLimitPolicyTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tRateLimitPolicyTableName, list...)
}

// TRateLimitPolicyUpdate Update One
func TRateLimitPolicyUpdate(dbCtx lib.DBContexter, val, where *TRateLimitPolicyParam) (int64, error) {
	return internal.Update(dbCtx, tRateLimitPolicyTableName, where, val)
}

// TRateLimitPolicyDelete Delete One/Multiple
func TRateLimitPolicyDelete(dbCtx lib.DBContexter, where *TRateLimitPolicyParam) (int64, error) {
	return internal.Delete(dbCtx, tRateLimitPolicyTableName, where)
}
