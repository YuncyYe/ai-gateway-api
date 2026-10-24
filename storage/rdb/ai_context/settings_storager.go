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

package ai_context

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ai_context"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

// AIContextSettingsStorager implements the singleton global settings storage.
// The table is physically single-row: Get returns (nil, nil) when the table is
// empty and Upsert overwrites via delete-all + insert inside the
// caller-provided transaction.
type AIContextSettingsStorager struct {
	dbCtxFactory lib.DBContextFactory
}

func NewAIContextSettingsStorager(dbCtxFactory lib.DBContextFactory) *AIContextSettingsStorager {
	return &AIContextSettingsStorager{
		dbCtxFactory: dbCtxFactory,
	}
}

var _ ai_context.AIContextSettingsStorager = &AIContextSettingsStorager{}

// Get returns the settings row with the smallest id, or (nil, nil) when the
// table is empty (empty table means "use the documented defaults").
func (s *AIContextSettingsStorager) Get(ctx context.Context) (*ai_context.SettingsRow, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}

	where := &dao.TAIContextSettingsParam{
		OrderBy: lib.PString("id ASC"),
	}
	list, err := dao.TAIContextSettingsList(dbCtx, where)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}

	return aiContextSettingsDataToRow(list[0]), nil
}

// Upsert overwrites the singleton row (delete-all + insert). It must be
// called inside a transaction (itxn.TxnStorager).
func (s *AIContextSettingsStorager) Upsert(ctx context.Context, row *ai_context.SettingsRow) error {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return err
	}

	_, err = dao.TAIContextSettingsReplaceAll(dbCtx, aiContextSettingsRowToData(row))
	return err
}

func aiContextSettingsRowToData(row *ai_context.SettingsRow) *dao.TAIContextSettingsParam {
	if row == nil {
		return nil
	}

	return &dao.TAIContextSettingsParam{
		ID:                           row.ID,
		TriggerRatio:                 row.TriggerRatio,
		KeepLatestImages:             row.KeepLatestImages,
		ToolResultMaxChars:           row.ToolResultMaxChars,
		ThinkingPolicy:               row.ThinkingPolicy,
		CharsPerToken:                row.CharsPerToken,
		ImageTokenEstimate:           row.ImageTokenEstimate,
		RewriteStrength:              row.RewriteStrength,
		RewriteProtectedSurvivalRate: row.RewriteProtectedSurvivalRate,
		CreatedAt:                    row.CreatedAt,
		UpdatedAt:                    row.UpdatedAt,
	}
}

func aiContextSettingsDataToRow(one *dao.TAIContextSettings) *ai_context.SettingsRow {
	if one == nil {
		return nil
	}

	return &ai_context.SettingsRow{
		ID:                           &one.ID,
		TriggerRatio:                 &one.TriggerRatio,
		KeepLatestImages:             &one.KeepLatestImages,
		ToolResultMaxChars:           &one.ToolResultMaxChars,
		ThinkingPolicy:               &one.ThinkingPolicy,
		CharsPerToken:                &one.CharsPerToken,
		ImageTokenEstimate:           &one.ImageTokenEstimate,
		RewriteStrength:              &one.RewriteStrength,
		RewriteProtectedSurvivalRate: &one.RewriteProtectedSurvivalRate,
		CreatedAt:                    &one.CreatedAt,
		UpdatedAt:                    &one.UpdatedAt,
	}
}
