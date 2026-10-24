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

const tAICacheRuleTableName = "ai_cache_rules"

type TAICacheRule struct {
	ID                  int64     `db:"id"`
	Name                string    `db:"name"`
	Cond                string    `db:"cond"`
	CacheKeyStrategy    string    `db:"cache_key_strategy"`
	CacheTTL            int       `db:"cache_ttl"`
	MaxBodyBytes        int64     `db:"max_body_bytes"`
	MaxValueBytes       int64     `db:"max_value_bytes"`
	EnableSemanticCache bool      `db:"enable_semantic_cache"`
	CreatedAt           time.Time `db:"created_at"`
	UpdatedAt           time.Time `db:"updated_at"`
}

// TAICacheRuleOne Query One
// return nil, nil if record not existed
func TAICacheRuleOne(dbCtx lib.DBContexter, where *TAICacheRuleParam) (*TAICacheRule, error) {
	t := &TAICacheRule{}
	err := internal.QueryOne(dbCtx, tAICacheRuleTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TAICacheRuleList Query Multiple
// return nil, nil if record not existed
func TAICacheRuleList(dbCtx lib.DBContexter, where *TAICacheRuleParam) ([]*TAICacheRule, error) {
	t := []*TAICacheRule{}
	err := internal.QueryList(dbCtx, tAICacheRuleTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TAICacheRuleParam struct {
	ID                  *int64     `db:"id"`
	Name                *string    `db:"name"`
	Cond                *string    `db:"cond"`
	CacheKeyStrategy    *string    `db:"cache_key_strategy"`
	CacheTTL            *int       `db:"cache_ttl"`
	MaxBodyBytes        *int64     `db:"max_body_bytes"`
	MaxValueBytes       *int64     `db:"max_value_bytes"`
	EnableSemanticCache *bool      `db:"enable_semantic_cache"`
	CreatedAt           *time.Time `db:"created_at"`
	UpdatedAt           *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TAICacheRuleCreate One/Multiple
func TAICacheRuleCreate(dbCtx lib.DBContexter, data ...*TAICacheRuleParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tAICacheRuleTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tAICacheRuleTableName, list...)
}

// TAICacheRuleUpdate Update One
func TAICacheRuleUpdate(dbCtx lib.DBContexter, val, where *TAICacheRuleParam) (int64, error) {
	return internal.Update(dbCtx, tAICacheRuleTableName, where, val)
}

// TAICacheRuleDelete Delete One/Multiple
func TAICacheRuleDelete(dbCtx lib.DBContexter, where *TAICacheRuleParam) (int64, error) {
	return internal.Delete(dbCtx, tAICacheRuleTableName, where)
}

// TAICacheRuleReplaceAll rebuilds the whole rule set in one transaction
// bound dbCtx: delete-all + insert-all in the given order.
func TAICacheRuleReplaceAll(dbCtx lib.DBContexter, data ...*TAICacheRuleParam) (int64, error) {
	if _, err := internal.Delete(dbCtx, tAICacheRuleTableName, &TAICacheRuleParam{}); err != nil {
		return 0, err
	}

	if len(data) == 0 {
		return 0, nil
	}

	return TAICacheRuleCreate(dbCtx, data...)
}
