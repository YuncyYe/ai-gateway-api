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

const tTrafficMirrorRuleTableName = "traffic_mirror_rules"

// TTrafficMirrorRule is the record representation of traffic_mirror_rules.
// The nullable JSON/text columns are pointers so that a NULL remove_headers
// stays distinguishable from an explicit '[]' (default-blacklist semantics).
type TTrafficMirrorRule struct {
	ID            int64     `db:"id"`
	Name          string    `db:"name"`
	Cond          string    `db:"cond"`
	MirrorCluster string    `db:"mirror_cluster"`
	Percentage    int       `db:"percentage"`
	RemoveHeaders *string   `db:"remove_headers"`
	SetHeaders    *string   `db:"set_headers"`
	BodyRewrites  *string   `db:"body_rewrites"`
	PathRewrite   *string   `db:"path_rewrite"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

// TTrafficMirrorRuleOne Query One
// return nil, nil if record not existed
func TTrafficMirrorRuleOne(dbCtx lib.DBContexter, where *TTrafficMirrorRuleParam) (*TTrafficMirrorRule, error) {
	t := &TTrafficMirrorRule{}
	err := internal.QueryOne(dbCtx, tTrafficMirrorRuleTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TTrafficMirrorRuleList Query Multiple
// return nil, nil if record not existed
func TTrafficMirrorRuleList(dbCtx lib.DBContexter, where *TTrafficMirrorRuleParam) ([]*TTrafficMirrorRule, error) {
	t := []*TTrafficMirrorRule{}
	err := internal.QueryList(dbCtx, tTrafficMirrorRuleTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TTrafficMirrorRuleParam struct {
	ID            *int64     `db:"id"`
	Name          *string    `db:"name"`
	Cond          *string    `db:"cond"`
	MirrorCluster *string    `db:"mirror_cluster"`
	Percentage    *int       `db:"percentage"`
	RemoveHeaders *string    `db:"remove_headers"`
	SetHeaders    *string    `db:"set_headers"`
	BodyRewrites  *string    `db:"body_rewrites"`
	PathRewrite   *string    `db:"path_rewrite"`
	CreatedAt     *time.Time `db:"created_at"`
	UpdatedAt     *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TTrafficMirrorRuleCreate One/Multiple
func TTrafficMirrorRuleCreate(dbCtx lib.DBContexter, data ...*TTrafficMirrorRuleParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tTrafficMirrorRuleTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tTrafficMirrorRuleTableName, list...)
}

// TTrafficMirrorRuleUpdate Update One
func TTrafficMirrorRuleUpdate(dbCtx lib.DBContexter, val, where *TTrafficMirrorRuleParam) (int64, error) {
	return internal.Update(dbCtx, tTrafficMirrorRuleTableName, where, val)
}

// TTrafficMirrorRuleDelete Delete One/Multiple
func TTrafficMirrorRuleDelete(dbCtx lib.DBContexter, where *TTrafficMirrorRuleParam) (int64, error) {
	return internal.Delete(dbCtx, tTrafficMirrorRuleTableName, where)
}

// TTrafficMirrorRuleReplaceAll rebuilds the whole rule set in one transaction
// bound dbCtx: delete-all + insert-all in the given order.
//
// Records are inserted one by one (not a single bulk Insert): gendry's bulk
// insert requires every record to carry the same column set, but the nullable
// JSON/text columns (remove_headers/set_headers/body_rewrites/path_rewrite)
// legitimately differ across rules (one rule may leave them NULL while
// another sets them) — a bulk insert then fails with "insert data not match".
// Per-record inserts keep the NULL semantics intact.
func TTrafficMirrorRuleReplaceAll(dbCtx lib.DBContexter, data ...*TTrafficMirrorRuleParam) (int64, error) {
	if _, err := internal.Delete(dbCtx, tTrafficMirrorRuleTableName, &TTrafficMirrorRuleParam{}); err != nil {
		return 0, err
	}

	var affected int64
	for _, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		n, err := internal.Create(dbCtx, tTrafficMirrorRuleTableName, one)
		if err != nil {
			return 0, err
		}
		affected += n
	}
	return affected, nil
}
