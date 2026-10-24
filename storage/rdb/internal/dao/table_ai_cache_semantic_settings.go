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

const tAICacheSemanticSettingsTableName = "ai_cache_semantic_settings"

type TAICacheSemanticSettings struct {
	ID                int64     `db:"id"`
	TopK              int       `db:"top_k"`
	Threshold         float64   `db:"threshold"`
	ThresholdRelation string    `db:"threshold_relation"`
	CreatedAt         time.Time `db:"created_at"`
	UpdatedAt         time.Time `db:"updated_at"`
}

// TAICacheSemanticSettingsOne Query One
// return nil, nil if record not existed
func TAICacheSemanticSettingsOne(dbCtx lib.DBContexter, where *TAICacheSemanticSettingsParam) (*TAICacheSemanticSettings, error) {
	t := &TAICacheSemanticSettings{}
	err := internal.QueryOne(dbCtx, tAICacheSemanticSettingsTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TAICacheSemanticSettingsList Query Multiple
// return nil, nil if record not existed
func TAICacheSemanticSettingsList(dbCtx lib.DBContexter, where *TAICacheSemanticSettingsParam) ([]*TAICacheSemanticSettings, error) {
	t := []*TAICacheSemanticSettings{}
	err := internal.QueryList(dbCtx, tAICacheSemanticSettingsTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TAICacheSemanticSettingsParam struct {
	ID                *int64     `db:"id"`
	TopK              *int       `db:"top_k"`
	Threshold         *float64   `db:"threshold"`
	ThresholdRelation *string    `db:"threshold_relation"`
	CreatedAt         *time.Time `db:"created_at"`
	UpdatedAt         *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TAICacheSemanticSettingsCreate One/Multiple
func TAICacheSemanticSettingsCreate(dbCtx lib.DBContexter, data ...*TAICacheSemanticSettingsParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tAICacheSemanticSettingsTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tAICacheSemanticSettingsTableName, list...)
}

// TAICacheSemanticSettingsUpdate Update One
func TAICacheSemanticSettingsUpdate(dbCtx lib.DBContexter, val, where *TAICacheSemanticSettingsParam) (int64, error) {
	return internal.Update(dbCtx, tAICacheSemanticSettingsTableName, where, val)
}

// TAICacheSemanticSettingsDelete Delete One/Multiple
func TAICacheSemanticSettingsDelete(dbCtx lib.DBContexter, where *TAICacheSemanticSettingsParam) (int64, error) {
	return internal.Delete(dbCtx, tAICacheSemanticSettingsTableName, where)
}

// TAICacheSemanticSettingsReplaceAll overwrites the singleton settings row in
// one transaction bound dbCtx: delete-all + insert (the table is physically
// single-row; re-running it still ends with exactly one row).
func TAICacheSemanticSettingsReplaceAll(dbCtx lib.DBContexter, data ...*TAICacheSemanticSettingsParam) (int64, error) {
	if _, err := internal.Delete(dbCtx, tAICacheSemanticSettingsTableName, &TAICacheSemanticSettingsParam{}); err != nil {
		return 0, err
	}

	if len(data) == 0 {
		return 0, nil
	}

	return TAICacheSemanticSettingsCreate(dbCtx, data...)
}
