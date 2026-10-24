//Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
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

const tAIContextRuleTableName = "ai_context_rules"

type TAIContextRule struct {
	ID               int64     `db:"id"`
	Cond             string    `db:"cond"`
	Mode             string    `db:"mode"`
	MaxContextTokens int       `db:"max_context_tokens"`
	ReserveTokens    int       `db:"reserve_tokens"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

// TAIContextRuleOne Query One
// return nil, nil if record not existed
func TAIContextRuleOne(dbCtx lib.DBContexter, where *TAIContextRuleParam) (*TAIContextRule, error) {
	t := &TAIContextRule{}
	err := internal.QueryOne(dbCtx, tAIContextRuleTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TAIContextRuleList Query Multiple
// return nil, nil if record not existed
func TAIContextRuleList(dbCtx lib.DBContexter, where *TAIContextRuleParam) ([]*TAIContextRule, error) {
	t := []*TAIContextRule{}
	err := internal.QueryList(dbCtx, tAIContextRuleTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TAIContextRuleParam struct {
	ID               *int64     `db:"id"`
	Cond             *string    `db:"cond"`
	Mode             *string    `db:"mode"`
	MaxContextTokens *int       `db:"max_context_tokens"`
	ReserveTokens    *int       `db:"reserve_tokens"`
	CreatedAt        *time.Time `db:"created_at"`
	UpdatedAt        *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TAIContextRuleCreate One/Multiple
func TAIContextRuleCreate(dbCtx lib.DBContexter, data ...*TAIContextRuleParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tAIContextRuleTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tAIContextRuleTableName, list...)
}

// TAIContextRuleUpdate Update One
func TAIContextRuleUpdate(dbCtx lib.DBContexter, val, where *TAIContextRuleParam) (int64, error) {
	return internal.Update(dbCtx, tAIContextRuleTableName, where, val)
}

// TAIContextRuleDelete Delete One/Multiple
func TAIContextRuleDelete(dbCtx lib.DBContexter, where *TAIContextRuleParam) (int64, error) {
	return internal.Delete(dbCtx, tAIContextRuleTableName, where)
}

// TAIContextRuleReplaceAll rebuilds the whole rule set in one transaction
// bound dbCtx: delete-all + insert-all in the given order.
func TAIContextRuleReplaceAll(dbCtx lib.DBContexter, data ...*TAIContextRuleParam) (int64, error) {
	if _, err := internal.Delete(dbCtx, tAIContextRuleTableName, &TAIContextRuleParam{}); err != nil {
		return 0, err
	}

	if len(data) == 0 {
		return 0, nil
	}

	return TAIContextRuleCreate(dbCtx, data...)
}
