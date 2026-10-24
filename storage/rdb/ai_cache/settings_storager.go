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

package ai_cache

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ai_cache"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

// AICacheSemanticSettingsStorager implements the singleton semantic settings
// storage. The table is physically single-row: Get returns (nil, nil) when
// the table is empty and Upsert overwrites via delete-all + insert inside the
// caller-provided transaction.
type AICacheSemanticSettingsStorager struct {
	dbCtxFactory lib.DBContextFactory
}

func NewAICacheSemanticSettingsStorager(dbCtxFactory lib.DBContextFactory) *AICacheSemanticSettingsStorager {
	return &AICacheSemanticSettingsStorager{
		dbCtxFactory: dbCtxFactory,
	}
}

var _ ai_cache.AICacheSemanticSettingsStorager = &AICacheSemanticSettingsStorager{}

// Get returns the settings row with the smallest id, or (nil, nil) when the
// table is empty (empty table means "use the documented defaults").
func (s *AICacheSemanticSettingsStorager) Get(ctx context.Context) (*ai_cache.SemanticSettingsRow, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}

	where := &dao.TAICacheSemanticSettingsParam{
		OrderBy: lib.PString("id ASC"),
	}
	list, err := dao.TAICacheSemanticSettingsList(dbCtx, where)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}

	return aiCacheSemanticSettingsDataToRow(list[0]), nil
}

// Upsert overwrites the singleton row (delete-all + insert). It must be
// called inside a transaction (itxn.TxnStorager).
func (s *AICacheSemanticSettingsStorager) Upsert(ctx context.Context, row *ai_cache.SemanticSettingsRow) error {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return err
	}

	_, err = dao.TAICacheSemanticSettingsReplaceAll(dbCtx, aiCacheSemanticSettingsRowToData(row))
	return err
}

func aiCacheSemanticSettingsRowToData(row *ai_cache.SemanticSettingsRow) *dao.TAICacheSemanticSettingsParam {
	if row == nil {
		return nil
	}

	return &dao.TAICacheSemanticSettingsParam{
		ID:                row.ID,
		TopK:              row.TopK,
		Threshold:         row.Threshold,
		ThresholdRelation: row.ThresholdRelation,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

func aiCacheSemanticSettingsDataToRow(one *dao.TAICacheSemanticSettings) *ai_cache.SemanticSettingsRow {
	if one == nil {
		return nil
	}

	return &ai_cache.SemanticSettingsRow{
		ID:                &one.ID,
		TopK:              &one.TopK,
		Threshold:         &one.Threshold,
		ThresholdRelation: &one.ThresholdRelation,
		CreatedAt:         &one.CreatedAt,
		UpdatedAt:         &one.UpdatedAt,
	}
}
