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
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

// Defaults applied by the control plane when an optional settings field is
// omitted (an empty settings table means "defaults", no default row is
// pre-inserted). These values are frozen in sync with the BFE mod_ai_context
// setDefaults (0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95):
// changing one side requires changing the other.
const (
	DefaultTriggerRatio                 = 0.7
	DefaultKeepLatestImages             = 2
	DefaultToolResultMaxChars           = 2000
	DefaultThinkingPolicy               = "trim-all-but-last"
	DefaultCharsPerToken                = 4
	DefaultImageTokenEstimate           = 1200
	DefaultRewriteStrength              = "lite"
	DefaultRewriteProtectedSurvivalRate = 0.95
)

// Context compression modes (BFE mod_ai_context contract). Mode is required
// on every rule: the BFE loader rejects the whole file when mode is missing
// or not one of these values.
const (
	ModeOff          = "off"
	ModeConservative = "conservative"
	ModeBalanced     = "balanced"
	ModeAggressive   = "aggressive"
)

// ContextRuleRow is the storage-level AI context rule. The internal ID is the
// ordering/priority key and is never exposed through the Open API.
type ContextRuleRow struct {
	ID               *int64
	Cond             *string
	Mode             *string
	MaxContextTokens *int
	ReserveTokens    *int
	CreatedAt        *time.Time
	UpdatedAt        *time.Time
}

// SettingsRow is the storage-level singleton global settings row.
type SettingsRow struct {
	ID                           *int64
	TriggerRatio                 *float64
	KeepLatestImages             *int
	ToolResultMaxChars           *int
	ThinkingPolicy               *string
	CharsPerToken                *int
	ImageTokenEstimate           *int
	RewriteStrength              *string
	RewriteProtectedSurvivalRate *float64
	CreatedAt                    *time.Time
	UpdatedAt                    *time.Time
}

// RewriteConf is the rewrite sub-object of the exported Defaults block,
// consumed by the BFE mod_ai_context rule loader. The JSON tags are frozen
// contract (see design-docs/modifications/2026-10-01-ai-context-compress-control-plane
// design-changes.md section 4.2) and must stay verbatim in sync with BFE.
type RewriteConf struct {
	Strength              *string  `json:"strength"`
	ProtectedSurvivalRate *float64 `json:"protectedSurvivalRate"`
}

// DefaultsConf is the top-level Defaults block of the exported
// context_rule.data, consumed by the BFE mod_ai_context rule loader
// (DefaultsConfFile). It is always exported (defaults when the settings table
// is empty). The JSON tags are frozen contract (design-changes.md section 4.2)
// and must stay verbatim in sync with BFE.
type DefaultsConf struct {
	TriggerRatio       *float64     `json:"triggerRatio"`
	KeepLatestImages   *int         `json:"keepLatestImages"`
	ToolResultMaxChars *int         `json:"toolResultMaxChars"`
	ThinkingPolicy     *string      `json:"thinkingPolicy"`
	CharsPerToken      *int         `json:"charsPerToken"`
	ImageTokenEstimate *int         `json:"imageTokenEstimate"`
	Rewrite            *RewriteConf `json:"rewrite"`
}

// AIContextStorager defines storage operations for the AI context rule
// collection. ReplaceAll is executed inside a transaction provided by
// itxn.TxnStorager.
type AIContextStorager interface {
	// FetchAll returns all rules ordered by id ascending.
	FetchAll(ctx context.Context) ([]*ContextRuleRow, error)
	// ReplaceAll atomically replaces the whole rule set with the given rules
	// (delete-all + insert-all, in slice order).
	ReplaceAll(ctx context.Context, rules []*ContextRuleRow) error
}

// AIContextSettingsStorager defines storage operations for the singleton
// global settings. Upsert is executed inside a transaction provided by
// itxn.TxnStorager.
type AIContextSettingsStorager interface {
	// Get returns the settings row, or (nil, nil) when the table is empty.
	Get(ctx context.Context) (*SettingsRow, error)
	// Upsert overwrites the singleton row (delete-all + insert).
	Upsert(ctx context.Context, row *SettingsRow) error
}

// contextRuleRowFromShared converts an API-level rule into the storage-level
// rule, filling the documented defaults for omitted optional fields so that
// every inserted row carries the full column set.
func contextRuleRowFromShared(param *shared.AIContextRuleParam) *ContextRuleRow {
	if param == nil {
		return nil
	}

	rule := &ContextRuleRow{
		Cond:             param.Cond,
		Mode:             param.Mode,
		MaxContextTokens: param.MaxContextTokens,
		ReserveTokens:    param.ReserveTokens,
	}

	if rule.MaxContextTokens == nil {
		rule.MaxContextTokens = lib.PInt(0)
	}
	if rule.ReserveTokens == nil {
		rule.ReserveTokens = lib.PInt(0)
	}

	return rule
}

// contextRuleRowToShared converts a storage-level rule into the API-level rule.
func contextRuleRowToShared(rule *ContextRuleRow) *shared.AIContextRuleParam {
	if rule == nil {
		return nil
	}

	return &shared.AIContextRuleParam{
		Cond:             rule.Cond,
		Mode:             rule.Mode,
		MaxContextTokens: rule.MaxContextTokens,
		ReserveTokens:    rule.ReserveTokens,
	}
}

// defaultSettingsParam builds the settings param carrying the documented
// defaults (0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95).
func defaultSettingsParam() *shared.AIContextSettingsParam {
	return &shared.AIContextSettingsParam{
		TriggerRatio:       lib.PFloat64(DefaultTriggerRatio),
		KeepLatestImages:   lib.PInt(DefaultKeepLatestImages),
		ToolResultMaxChars: lib.PInt(DefaultToolResultMaxChars),
		ThinkingPolicy:     lib.PString(DefaultThinkingPolicy),
		CharsPerToken:      lib.PInt(DefaultCharsPerToken),
		ImageTokenEstimate: lib.PInt(DefaultImageTokenEstimate),
		Rewrite: &shared.AIContextRewriteParam{
			Strength:              lib.PString(DefaultRewriteStrength),
			ProtectedSurvivalRate: lib.PFloat64(DefaultRewriteProtectedSurvivalRate),
		},
	}
}

// settingsRowFromShared converts the API-level settings into the storage-level
// row, filling the documented defaults for omitted fields (including a nil
// rewrite sub-object or its nil fields) so the inserted row carries the full
// column set.
func settingsRowFromShared(param *shared.AIContextSettingsParam) *SettingsRow {
	if param == nil {
		param = &shared.AIContextSettingsParam{}
	}

	row := &SettingsRow{
		TriggerRatio:       param.TriggerRatio,
		KeepLatestImages:   param.KeepLatestImages,
		ToolResultMaxChars: param.ToolResultMaxChars,
		ThinkingPolicy:     param.ThinkingPolicy,
		CharsPerToken:      param.CharsPerToken,
		ImageTokenEstimate: param.ImageTokenEstimate,
	}
	if param.Rewrite != nil {
		row.RewriteStrength = param.Rewrite.Strength
		row.RewriteProtectedSurvivalRate = param.Rewrite.ProtectedSurvivalRate
	}

	if row.TriggerRatio == nil {
		row.TriggerRatio = lib.PFloat64(DefaultTriggerRatio)
	}
	if row.KeepLatestImages == nil {
		row.KeepLatestImages = lib.PInt(DefaultKeepLatestImages)
	}
	if row.ToolResultMaxChars == nil {
		row.ToolResultMaxChars = lib.PInt(DefaultToolResultMaxChars)
	}
	if row.ThinkingPolicy == nil {
		row.ThinkingPolicy = lib.PString(DefaultThinkingPolicy)
	}
	if row.CharsPerToken == nil {
		row.CharsPerToken = lib.PInt(DefaultCharsPerToken)
	}
	if row.ImageTokenEstimate == nil {
		row.ImageTokenEstimate = lib.PInt(DefaultImageTokenEstimate)
	}
	if row.RewriteStrength == nil {
		row.RewriteStrength = lib.PString(DefaultRewriteStrength)
	}
	if row.RewriteProtectedSurvivalRate == nil {
		row.RewriteProtectedSurvivalRate = lib.PFloat64(DefaultRewriteProtectedSurvivalRate)
	}

	return row
}

// settingsRowToShared converts a storage-level settings row into the
// API-level settings. A nil row yields the documented defaults.
func settingsRowToShared(row *SettingsRow) *shared.AIContextSettingsParam {
	if row == nil {
		return defaultSettingsParam()
	}

	return &shared.AIContextSettingsParam{
		TriggerRatio:       row.TriggerRatio,
		KeepLatestImages:   row.KeepLatestImages,
		ToolResultMaxChars: row.ToolResultMaxChars,
		ThinkingPolicy:     row.ThinkingPolicy,
		CharsPerToken:      row.CharsPerToken,
		ImageTokenEstimate: row.ImageTokenEstimate,
		Rewrite: &shared.AIContextRewriteParam{
			Strength:              row.RewriteStrength,
			ProtectedSurvivalRate: row.RewriteProtectedSurvivalRate,
		},
	}
}

// defaultsConfFromSettingsRow builds the exported Defaults block from a
// settings row (nil row yields the documented defaults). The block is always
// exported.
func defaultsConfFromSettingsRow(row *SettingsRow) *DefaultsConf {
	settings := settingsRowToShared(row)

	return &DefaultsConf{
		TriggerRatio:       settings.TriggerRatio,
		KeepLatestImages:   settings.KeepLatestImages,
		ToolResultMaxChars: settings.ToolResultMaxChars,
		ThinkingPolicy:     settings.ThinkingPolicy,
		CharsPerToken:      settings.CharsPerToken,
		ImageTokenEstimate: settings.ImageTokenEstimate,
		Rewrite: &RewriteConf{
			Strength:              settings.Rewrite.Strength,
			ProtectedSurvivalRate: settings.Rewrite.ProtectedSurvivalRate,
		},
	}
}
