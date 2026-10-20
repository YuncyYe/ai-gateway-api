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
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

// Defaults applied by the control plane when an optional rule field is omitted.
// They mirror the ai_cache_rules table column defaults (see db_ddl.sql).
const (
	DefaultCacheKeyStrategy = "lastQuestion"
	DefaultCacheTTL         = 0
	DefaultMaxBodyBytes     = 1048576
	DefaultMaxValueBytes    = 1048576
)

// Defaults for the semantic cache settings, applied when the settings table
// is empty (an empty table means "defaults", no default row is pre-inserted).
// These values are frozen in sync with the BFE mod_ai_cache setDefaults
// (1 / 0.15 / lt): changing one side requires changing the other.
const (
	DefaultSemanticTopK              = 1
	DefaultSemanticThreshold         = 0.15
	DefaultSemanticThresholdRelation = "lt"
)

// AICacheRuleParam is the storage-level AI cache rule. The internal ID is the
// ordering/priority key and is never exposed through the Open API.
// Per-rule timestamps are intentionally not carried: the Open API does not
// expose them (full-replace resource, see shared.AICacheRuleParam), the DAO
// fills created_at on insert and the DB default fills updated_at.
type AICacheRuleParam struct {
	ID                  *int64
	Name                *string
	Cond                *string
	CacheKeyStrategy    *string
	CacheTTL            *int
	MaxBodyBytes        *int64
	MaxValueBytes       *int64
	EnableSemanticCache *bool
}

// SemanticSettingsRow is the storage-level singleton semantic settings row.
type SemanticSettingsRow struct {
	ID                *int64
	TopK              *int
	Threshold         *float64
	ThresholdRelation *string
	CreatedAt         *time.Time
	UpdatedAt         *time.Time
}

// SemanticConf is the top-level Semantic block of the exported ai_cache.data,
// consumed by the BFE mod_ai_cache rule loader (SemanticConfFile). The JSON
// tags are frozen contract (see design-docs/sys-design/details/AI缓存规则与导出.md
// section 5.2.1) and must stay verbatim in sync with BFE.
type SemanticConf struct {
	TopK              *int     `json:"topK"`
	Threshold         *float64 `json:"threshold"`
	ThresholdRelation *string  `json:"thresholdRelation"`
}

// AICacheStorager defines storage operations for the AI cache rule collection.
// ReplaceAll is executed inside a transaction provided by itxn.TxnStorager.
type AICacheStorager interface {
	// FetchAll returns all rules ordered by id ascending.
	FetchAll(ctx context.Context) ([]*AICacheRuleParam, error)
	// ReplaceAll atomically replaces the whole rule set with the given rules
	// (delete-all + insert-all, in slice order).
	ReplaceAll(ctx context.Context, rules []*AICacheRuleParam) error
}

// AICacheSemanticSettingsStorager defines storage operations for the
// singleton semantic settings. Upsert is executed inside a transaction
// provided by itxn.TxnStorager.
type AICacheSemanticSettingsStorager interface {
	// Get returns the settings row, or (nil, nil) when the table is empty.
	Get(ctx context.Context) (*SemanticSettingsRow, error)
	// Upsert overwrites the singleton row (delete-all + insert).
	Upsert(ctx context.Context, row *SemanticSettingsRow) error
}

// aiCacheRuleParamFromShared converts an API-level rule into the storage-level
// rule, filling the documented defaults for omitted optional fields so that
// every inserted row carries the full column set. Timestamps are not part of the
// vocabulary: the DAO fills created_at and the DB default fills updated_at, which
// also guarantees a uniform column set for the bulk INSERT (issue #258).
func aiCacheRuleParamFromShared(param *shared.AICacheRuleParam) *AICacheRuleParam {
	if param == nil {
		return nil
	}

	rule := &AICacheRuleParam{
		Name:                param.Name,
		Cond:                param.Cond,
		CacheKeyStrategy:    param.CacheKeyStrategy,
		CacheTTL:            param.CacheTTL,
		MaxBodyBytes:        param.MaxBodyBytes,
		MaxValueBytes:       param.MaxValueBytes,
		EnableSemanticCache: param.EnableSemanticCache,
	}

	if rule.CacheKeyStrategy == nil {
		rule.CacheKeyStrategy = lib.PString(DefaultCacheKeyStrategy)
	}
	if rule.CacheTTL == nil {
		rule.CacheTTL = lib.PInt(DefaultCacheTTL)
	}
	if rule.MaxBodyBytes == nil {
		rule.MaxBodyBytes = lib.PInt64(DefaultMaxBodyBytes)
	}
	if rule.MaxValueBytes == nil {
		rule.MaxValueBytes = lib.PInt64(DefaultMaxValueBytes)
	}
	if rule.EnableSemanticCache == nil {
		rule.EnableSemanticCache = lib.PBool(false)
	}

	return rule
}

// aiCacheRuleParamToShared converts a storage-level rule into the API-level rule.
func aiCacheRuleParamToShared(rule *AICacheRuleParam) *shared.AICacheRuleParam {
	if rule == nil {
		return nil
	}

	return &shared.AICacheRuleParam{
		Name:                rule.Name,
		Cond:                rule.Cond,
		CacheKeyStrategy:    rule.CacheKeyStrategy,
		CacheTTL:            rule.CacheTTL,
		MaxBodyBytes:        rule.MaxBodyBytes,
		MaxValueBytes:       rule.MaxValueBytes,
		EnableSemanticCache: rule.EnableSemanticCache,
	}
}

// defaultSemanticSettingsParam builds the settings param carrying the
// documented defaults (1 / 0.15 / lt), without timestamps (no row exists).
func defaultSemanticSettingsParam() *shared.AICacheSemanticSettingsParam {
	return &shared.AICacheSemanticSettingsParam{
		TopK:              lib.PInt(DefaultSemanticTopK),
		Threshold:         lib.PFloat64(DefaultSemanticThreshold),
		ThresholdRelation: lib.PString(DefaultSemanticThresholdRelation),
	}
}

// semanticSettingsRowFromShared converts the API-level settings into the
// storage-level row, filling the documented defaults for omitted fields so
// the inserted row carries the full column set.
func semanticSettingsRowFromShared(param *shared.AICacheSemanticSettingsParam) *SemanticSettingsRow {
	if param == nil {
		return nil
	}

	row := &SemanticSettingsRow{
		TopK:              param.TopK,
		Threshold:         param.Threshold,
		ThresholdRelation: param.ThresholdRelation,
		CreatedAt:         param.CreatedAt,
		UpdatedAt:         param.UpdatedAt,
	}

	if row.TopK == nil {
		row.TopK = lib.PInt(DefaultSemanticTopK)
	}
	if row.Threshold == nil {
		row.Threshold = lib.PFloat64(DefaultSemanticThreshold)
	}
	if row.ThresholdRelation == nil {
		row.ThresholdRelation = lib.PString(DefaultSemanticThresholdRelation)
	}

	return row
}

// semanticSettingsRowToShared converts a storage-level settings row into the
// API-level settings. A nil row yields the documented defaults.
func semanticSettingsRowToShared(row *SemanticSettingsRow) *shared.AICacheSemanticSettingsParam {
	if row == nil {
		return defaultSemanticSettingsParam()
	}

	return &shared.AICacheSemanticSettingsParam{
		TopK:              row.TopK,
		Threshold:         row.Threshold,
		ThresholdRelation: row.ThresholdRelation,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}
