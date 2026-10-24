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

const tAIContextSettingsTableName = "ai_context_settings"

type TAIContextSettings struct {
	ID                           int64     `db:"id"`
	TriggerRatio                 float64   `db:"trigger_ratio"`
	KeepLatestImages             int       `db:"keep_latest_images"`
	ToolResultMaxChars           int       `db:"tool_result_max_chars"`
	ThinkingPolicy               string    `db:"thinking_policy"`
	CharsPerToken                int       `db:"chars_per_token"`
	ImageTokenEstimate           int       `db:"image_token_estimate"`
	RewriteStrength              string    `db:"rewrite_strength"`
	RewriteProtectedSurvivalRate float64   `db:"rewrite_protected_survival_rate"`
	CreatedAt                    time.Time `db:"created_at"`
	UpdatedAt                    time.Time `db:"updated_at"`
}

// TAIContextSettingsOne Query One
// return nil, nil if record not existed
func TAIContextSettingsOne(dbCtx lib.DBContexter, where *TAIContextSettingsParam) (*TAIContextSettings, error) {
	t := &TAIContextSettings{}
	err := internal.QueryOne(dbCtx, tAIContextSettingsTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TAIContextSettingsList Query Multiple
// return nil, nil if record not existed
func TAIContextSettingsList(dbCtx lib.DBContexter, where *TAIContextSettingsParam) ([]*TAIContextSettings, error) {
	t := []*TAIContextSettings{}
	err := internal.QueryList(dbCtx, tAIContextSettingsTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

type TAIContextSettingsParam struct {
	ID                           *int64     `db:"id"`
	TriggerRatio                 *float64   `db:"trigger_ratio"`
	KeepLatestImages             *int       `db:"keep_latest_images"`
	ToolResultMaxChars           *int       `db:"tool_result_max_chars"`
	ThinkingPolicy               *string    `db:"thinking_policy"`
	CharsPerToken                *int       `db:"chars_per_token"`
	ImageTokenEstimate           *int       `db:"image_token_estimate"`
	RewriteStrength              *string    `db:"rewrite_strength"`
	RewriteProtectedSurvivalRate *float64   `db:"rewrite_protected_survival_rate"`
	CreatedAt                    *time.Time `db:"created_at"`
	UpdatedAt                    *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TAIContextSettingsCreate One/Multiple
func TAIContextSettingsCreate(dbCtx lib.DBContexter, data ...*TAIContextSettingsParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		return internal.Create(dbCtx, tAIContextSettingsTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		list[i] = one
	}

	return internal.Create(dbCtx, tAIContextSettingsTableName, list...)
}

// TAIContextSettingsUpdate Update One
func TAIContextSettingsUpdate(dbCtx lib.DBContexter, val, where *TAIContextSettingsParam) (int64, error) {
	return internal.Update(dbCtx, tAIContextSettingsTableName, where, val)
}

// TAIContextSettingsDelete Delete One/Multiple
func TAIContextSettingsDelete(dbCtx lib.DBContexter, where *TAIContextSettingsParam) (int64, error) {
	return internal.Delete(dbCtx, tAIContextSettingsTableName, where)
}

// TAIContextSettingsReplaceAll overwrites the singleton settings row in one
// transaction bound dbCtx: delete-all + insert (the table is physically
// single-row; re-running it still ends with exactly one row).
func TAIContextSettingsReplaceAll(dbCtx lib.DBContexter, data ...*TAIContextSettingsParam) (int64, error) {
	if _, err := internal.Delete(dbCtx, tAIContextSettingsTableName, &TAIContextSettingsParam{}); err != nil {
		return 0, err
	}

	if len(data) == 0 {
		return 0, nil
	}

	return TAIContextSettingsCreate(dbCtx, data...)
}
