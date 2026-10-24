// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ai_context

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storageRule(id int64, cond, mode string) *ContextRuleRow {
	now := time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)
	return &ContextRuleRow{
		ID:               lib.PInt64(id),
		Cond:             lib.PString(cond),
		Mode:             lib.PString(mode),
		MaxContextTokens: lib.PInt(64000),
		ReserveTokens:    lib.PInt(8192),
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}
}

func sharedRule(cond, mode string) *shared.AIContextRuleParam {
	return &shared.AIContextRuleParam{
		Cond: lib.PString(cond),
		Mode: lib.PString(mode),
	}
}

func settingsRow(triggerRatio float64, keepImages, toolMaxChars, charsPerToken, imageEstimate int,
	policy, strength string, survivalRate float64) *SettingsRow {
	now := time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)
	return &SettingsRow{
		ID:                           lib.PInt64(1),
		TriggerRatio:                 lib.PFloat64(triggerRatio),
		KeepLatestImages:             lib.PInt(keepImages),
		ToolResultMaxChars:           lib.PInt(toolMaxChars),
		ThinkingPolicy:               lib.PString(policy),
		CharsPerToken:                lib.PInt(charsPerToken),
		ImageTokenEstimate:           lib.PInt(imageEstimate),
		RewriteStrength:              lib.PString(strength),
		RewriteProtectedSurvivalRate: lib.PFloat64(survivalRate),
		CreatedAt:                    &now,
		UpdatedAt:                    &now,
	}
}

func TestAIContextManager_GetRules(t *testing.T) {
	ctx := context.Background()

	t.Run("returns rules in storage order without timestamps", func(t *testing.T) {
		store := &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				return []*ContextRuleRow{
					storageRule(1, "default_t()", "balanced"),
					storageRule(2, "req_path_in(\"/v1\", false)", "off"),
				}, nil
			},
		}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")

		rules, err := m.GetRules(ctx)
		require.NoError(t, err)
		require.Len(t, rules, 2)
		assert.Equal(t, "default_t()", *rules[0].Cond)
		assert.Equal(t, "balanced", *rules[0].Mode)
		assert.Equal(t, "off", *rules[1].Mode)
	})

	t.Run("empty collection returns empty slice", func(t *testing.T) {
		store := &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				return []*ContextRuleRow{}, nil
			},
		}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")

		rules, err := m.GetRules(ctx)
		require.NoError(t, err)
		assert.NotNil(t, rules)
		assert.Len(t, rules, 0)
	})

	t.Run("storage error propagates", func(t *testing.T) {
		store := &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				return nil, errors.New("db down")
			},
		}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")

		_, err := m.GetRules(ctx)
		require.Error(t, err)
	})
}

func TestAIContextManager_SetRules(t *testing.T) {
	ctx := context.Background()

	t.Run("replaces collection in slice order with defaults filled", func(t *testing.T) {
		var replaced [][]*ContextRuleRow
		store := &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				return []*ContextRuleRow{}, nil
			},
			replaceAllFn: func(ctx context.Context, rules []*ContextRuleRow) error {
				replaced = append(replaced, rules)
				return nil
			},
		}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")

		updated, err := m.SetRules(ctx, &shared.AIContextRulesParam{
			Rules: []*shared.AIContextRuleParam{
				sharedRule("default_t()", "balanced"),
				{
					Cond:             lib.PString("req_path_in(\"/v1\", false)"),
					Mode:             lib.PString("conservative"),
					MaxContextTokens: lib.PInt(32000),
					ReserveTokens:    lib.PInt(4096),
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, updated)

		// ReplaceAll must be invoked exactly once, in array order.
		require.Len(t, replaced, 1)
		require.Len(t, replaced[0], 2)
		assert.Equal(t, "default_t()", *replaced[0][0].Cond)
		assert.Equal(t, "balanced", *replaced[0][0].Mode)

		// Omitted optional fields are filled with defaults (0 = BFE side semantics).
		assert.Equal(t, 0, *replaced[0][0].MaxContextTokens)
		assert.Equal(t, 0, *replaced[0][0].ReserveTokens)

		// Explicit values are preserved.
		assert.Equal(t, 32000, *replaced[0][1].MaxContextTokens)
		assert.Equal(t, 4096, *replaced[0][1].ReserveTokens)
	})

	t.Run("nil param and nil rules clear the collection", func(t *testing.T) {
		store := &fakeAIContextRuleStorager{}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")

		_, err := m.SetRules(ctx, nil)
		require.NoError(t, err)
		_, err = m.SetRules(ctx, &shared.AIContextRulesParam{})
		require.NoError(t, err)

		require.Len(t, store.replaceAllCalls, 2)
		assert.Len(t, store.replaceAllCalls[0], 0)
		assert.Len(t, store.replaceAllCalls[1], 0)
	})

	t.Run("replace failure rolls back and records failed audit", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		store := &fakeAIContextRuleStorager{
			replaceAllFn: func(ctx context.Context, rules []*ContextRuleRow) error {
				return errors.New("replace error")
			},
		}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		_, err := m.SetRules(ctx, &shared.AIContextRulesParam{
			Rules: []*shared.AIContextRuleParam{sharedRule("default_t()", "off")},
		})
		require.Error(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ActionUpdate), entry.Action)
		assert.Equal(t, string(ioperlog.ResourceTypeAIContextRule), entry.ResourceType)
		assert.Equal(t, aiContextRulesResourceID, entry.ResourceID)
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)
		assert.NotEmpty(t, entry.ErrorMsg)
	})

	t.Run("records successful audit with collection snapshots", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		calls := 0
		store := &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				calls++
				if calls == 1 {
					return []*ContextRuleRow{storageRule(1, "default_t()", "off")}, nil
				}
				return []*ContextRuleRow{storageRule(2, "req_path_in(\"/v1\", false)", "balanced")}, nil
			},
		}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		_, err := m.SetRules(ctx, &shared.AIContextRulesParam{
			Rules: []*shared.AIContextRuleParam{sharedRule("req_path_in(\"/v1\", false)", "balanced")},
		})
		require.NoError(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ResourceTypeAIContextRule), entry.ResourceType)
		assert.Equal(t, ioperlog.StatusSuccess, entry.Status)

		before, ok := entry.ChangeSummary["before"].(map[string]interface{})
		require.True(t, ok)
		beforeRules, ok := before["rules"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, beforeRules, 1)
		assert.Equal(t, "off", beforeRules[0]["mode"])

		after, ok := entry.ChangeSummary["after"].(map[string]interface{})
		require.True(t, ok)
		afterRules, ok := after["rules"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, afterRules, 1)
		assert.Equal(t, "balanced", afterRules[0]["mode"])
	})

	t.Run("records failed audit for validation rejection", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		store := &fakeAIContextRuleStorager{}
		m := NewAIContextManager(&fakeTxn{}, store, &fakeAIContextSettingsStorager{}, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		m.RecordSetRulesFailure(ctx, &shared.AIContextRulesParam{
			Rules: []*shared.AIContextRuleParam{sharedRule("default_t()", "hyper")},
		}, errors.New("mode must be one of off/conservative/balanced/aggressive"))

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ResourceTypeAIContextRule), entry.ResourceType)
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)

		after, ok := entry.ChangeSummary["after"].(map[string]interface{})
		require.True(t, ok)
		afterRules, ok := after["rules"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, afterRules, 1)
		assert.Equal(t, "hyper", afterRules[0]["mode"])
	})
}

func TestContextRuleRow_Conversions(t *testing.T) {
	t.Run("nil inputs stay nil", func(t *testing.T) {
		assert.Nil(t, contextRuleRowFromShared(nil))
		assert.Nil(t, contextRuleRowToShared(nil))
	})

	t.Run("nil optional fields are filled with defaults", func(t *testing.T) {
		rule := contextRuleRowFromShared(sharedRule("default_t()", "off"))
		require.NotNil(t, rule)
		assert.Equal(t, 0, *rule.MaxContextTokens)
		assert.Equal(t, 0, *rule.ReserveTokens)
	})

	t.Run("round trip preserves values", func(t *testing.T) {
		original := &shared.AIContextRuleParam{
			Cond:             lib.PString("req_path_in(\"/v1\", false)"),
			Mode:             lib.PString("aggressive"),
			MaxContextTokens: lib.PInt(64000),
			ReserveTokens:    lib.PInt(8192),
		}

		rule := contextRuleRowFromShared(original)
		rst := contextRuleRowToShared(rule)
		assert.Equal(t, original, rst)
	})
}

func TestAIContextManager_GetSettings(t *testing.T) {
	ctx := context.Background()

	t.Run("empty table returns documented defaults without timestamps", func(t *testing.T) {
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, &fakeAIContextSettingsStorager{}, nil, "AI_product")

		settings, err := m.GetSettings(ctx)
		require.NoError(t, err)
		require.NotNil(t, settings)
		assert.Equal(t, DefaultTriggerRatio, *settings.TriggerRatio)
		assert.Equal(t, DefaultKeepLatestImages, *settings.KeepLatestImages)
		assert.Equal(t, DefaultToolResultMaxChars, *settings.ToolResultMaxChars)
		assert.Equal(t, DefaultThinkingPolicy, *settings.ThinkingPolicy)
		assert.Equal(t, DefaultCharsPerToken, *settings.CharsPerToken)
		assert.Equal(t, DefaultImageTokenEstimate, *settings.ImageTokenEstimate)
		require.NotNil(t, settings.Rewrite)
		assert.Equal(t, DefaultRewriteStrength, *settings.Rewrite.Strength)
		assert.Equal(t, DefaultRewriteProtectedSurvivalRate, *settings.Rewrite.ProtectedSurvivalRate)
	})

	t.Run("existing row returns stored values", func(t *testing.T) {
		store := &fakeAIContextSettingsStorager{
			getFn: func(ctx context.Context) (*SettingsRow, error) {
				return settingsRow(0.8, 4, 4000, 3, 800, "keep", "full", 0.9), nil
			},
		}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")

		settings, err := m.GetSettings(ctx)
		require.NoError(t, err)
		require.NotNil(t, settings)
		assert.Equal(t, 0.8, *settings.TriggerRatio)
		assert.Equal(t, 4, *settings.KeepLatestImages)
		assert.Equal(t, "keep", *settings.ThinkingPolicy)
		assert.Equal(t, 3, *settings.CharsPerToken)
		require.NotNil(t, settings.Rewrite)
		assert.Equal(t, "full", *settings.Rewrite.Strength)
		assert.Equal(t, 0.9, *settings.Rewrite.ProtectedSurvivalRate)
	})

	t.Run("storage error propagates", func(t *testing.T) {
		store := &fakeAIContextSettingsStorager{
			getFn: func(ctx context.Context) (*SettingsRow, error) {
				return nil, errors.New("db down")
			},
		}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")

		_, err := m.GetSettings(ctx)
		require.Error(t, err)
	})
}

func TestAIContextManager_SetSettings(t *testing.T) {
	ctx := context.Background()

	t.Run("insert on empty table fills defaults for omitted fields", func(t *testing.T) {
		store := &fakeAIContextSettingsStorager{}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")

		updated, err := m.SetSettings(ctx, &shared.AIContextSettingsParam{
			TriggerRatio:  lib.PFloat64(0.8),
			CharsPerToken: lib.PInt(3),
		})
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, 0.8, *updated.TriggerRatio)
		assert.Equal(t, 3, *updated.CharsPerToken)
		assert.Equal(t, DefaultKeepLatestImages, *updated.KeepLatestImages)
		assert.Equal(t, DefaultToolResultMaxChars, *updated.ToolResultMaxChars)
		assert.Equal(t, DefaultThinkingPolicy, *updated.ThinkingPolicy)
		assert.Equal(t, DefaultImageTokenEstimate, *updated.ImageTokenEstimate)
		require.NotNil(t, updated.Rewrite)
		assert.Equal(t, DefaultRewriteStrength, *updated.Rewrite.Strength)
		assert.Equal(t, DefaultRewriteProtectedSurvivalRate, *updated.Rewrite.ProtectedSurvivalRate)

		// Upsert invoked exactly once, with the full row.
		require.Len(t, store.upsertCalls, 1)
		assert.Equal(t, 0.8, *store.upsertCalls[0].TriggerRatio)
		assert.Equal(t, DefaultRewriteStrength, *store.upsertCalls[0].RewriteStrength)
	})

	t.Run("nil rewrite sub-object is filled with defaults", func(t *testing.T) {
		store := &fakeAIContextSettingsStorager{}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")

		updated, err := m.SetSettings(ctx, &shared.AIContextSettingsParam{})
		require.NoError(t, err)
		assert.Equal(t, DefaultTriggerRatio, *updated.TriggerRatio)
		require.NotNil(t, updated.Rewrite)
		assert.Equal(t, DefaultRewriteStrength, *updated.Rewrite.Strength)
	})

	t.Run("partial rewrite sub-object fills only omitted fields", func(t *testing.T) {
		store := &fakeAIContextSettingsStorager{}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")

		updated, err := m.SetSettings(ctx, &shared.AIContextSettingsParam{
			Rewrite: &shared.AIContextRewriteParam{
				Strength: lib.PString("full"),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, updated.Rewrite)
		assert.Equal(t, "full", *updated.Rewrite.Strength)
		assert.Equal(t, DefaultRewriteProtectedSurvivalRate, *updated.Rewrite.ProtectedSurvivalRate)
	})

	t.Run("records successful audit with default before snapshot on empty table", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		store := &fakeAIContextSettingsStorager{}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		_, err := m.SetSettings(ctx, &shared.AIContextSettingsParam{
			KeepLatestImages: lib.PInt(5),
		})
		require.NoError(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ActionUpdate), entry.Action)
		assert.Equal(t, string(ioperlog.ResourceTypeAIContextSettings), entry.ResourceType)
		assert.Equal(t, aiContextSettingsResourceID, entry.ResourceID)
		assert.Equal(t, ioperlog.StatusSuccess, entry.Status)

		before, ok := entry.ChangeSummary["before"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, DefaultKeepLatestImages, before["keep_latest_images"])

		after, ok := entry.ChangeSummary["after"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, 5, after["keep_latest_images"])
		afterRewrite, ok := after["rewrite"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, DefaultRewriteStrength, afterRewrite["strength"])
	})

	t.Run("records failed audit when upsert errors", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		store := &fakeAIContextSettingsStorager{
			upsertFn: func(ctx context.Context, row *SettingsRow) error {
				return errors.New("upsert error")
			},
		}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		_, err := m.SetSettings(ctx, &shared.AIContextSettingsParam{})
		require.Error(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)
		assert.NotEmpty(t, entry.ErrorMsg)
	})

	t.Run("records failed audit for validation rejection", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		store := &fakeAIContextSettingsStorager{}
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, store, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		m.RecordSetSettingsFailure(ctx, &shared.AIContextSettingsParam{
			TriggerRatio: lib.PFloat64(1.5),
		}, errors.New("trigger_ratio out of range"))

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ResourceTypeAIContextSettings), entry.ResourceType)
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)

		after, ok := entry.ChangeSummary["after"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, 1.5, after["trigger_ratio"])
	})
}

func TestSettingsRow_Conversions(t *testing.T) {
	t.Run("nil row converts to documented defaults", func(t *testing.T) {
		param := settingsRowToShared(nil)
		require.NotNil(t, param)
		assert.Equal(t, DefaultTriggerRatio, *param.TriggerRatio)
		require.NotNil(t, param.Rewrite)
		assert.Equal(t, DefaultRewriteStrength, *param.Rewrite.Strength)
	})

	t.Run("nil param fills a full default row", func(t *testing.T) {
		row := settingsRowFromShared(nil)
		require.NotNil(t, row)
		assert.Equal(t, DefaultTriggerRatio, *row.TriggerRatio)
		assert.Equal(t, DefaultKeepLatestImages, *row.KeepLatestImages)
		assert.Equal(t, DefaultToolResultMaxChars, *row.ToolResultMaxChars)
		assert.Equal(t, DefaultThinkingPolicy, *row.ThinkingPolicy)
		assert.Equal(t, DefaultCharsPerToken, *row.CharsPerToken)
		assert.Equal(t, DefaultImageTokenEstimate, *row.ImageTokenEstimate)
		assert.Equal(t, DefaultRewriteStrength, *row.RewriteStrength)
		assert.Equal(t, DefaultRewriteProtectedSurvivalRate, *row.RewriteProtectedSurvivalRate)
	})

	t.Run("round trip preserves values", func(t *testing.T) {
		original := settingsRow(0.9, 3, 500, 3, 900, "keep", "full", 0.99)
		rst := settingsRowToShared(settingsRowFromShared(settingsRowToShared(original)))
		assert.Equal(t, original.TriggerRatio, rst.TriggerRatio)
		assert.Equal(t, original.KeepLatestImages, rst.KeepLatestImages)
		assert.Equal(t, original.ToolResultMaxChars, rst.ToolResultMaxChars)
		assert.Equal(t, original.ThinkingPolicy, rst.ThinkingPolicy)
		assert.Equal(t, original.CharsPerToken, rst.CharsPerToken)
		assert.Equal(t, original.ImageTokenEstimate, rst.ImageTokenEstimate)
		assert.Equal(t, original.RewriteStrength, rst.Rewrite.Strength)
		assert.Equal(t, original.RewriteProtectedSurvivalRate, rst.Rewrite.ProtectedSurvivalRate)
	})
}

func TestAIContextManager_ContextRuleGenerator(t *testing.T) {
	ctx := context.Background()

	generate := func(t *testing.T, ruleStore *fakeAIContextRuleStorager, settingsStore *fakeAIContextSettingsStorager) *ExportContextRuleConfig {
		m := NewAIContextManager(&fakeTxn{}, ruleStore, settingsStore, nil, "AI_product")
		data, err := m.ContextRuleGenerator(ctx)
		require.NoError(t, err)
		assert.Equal(t, ConfigTopicProductAIContext, data.Topic)
		conf, ok := data.DataWithoutVersion.(*ExportContextRuleConfig)
		require.True(t, ok)
		return conf
	}

	t.Run("empty settings table exports documented defaults and empty rule array", func(t *testing.T) {
		conf := generate(t, &fakeAIContextRuleStorager{}, &fakeAIContextSettingsStorager{})

		require.NotNil(t, conf.Defaults)
		assert.Equal(t, DefaultTriggerRatio, *conf.Defaults.TriggerRatio)
		assert.Equal(t, DefaultKeepLatestImages, *conf.Defaults.KeepLatestImages)
		assert.Equal(t, DefaultToolResultMaxChars, *conf.Defaults.ToolResultMaxChars)
		assert.Equal(t, DefaultThinkingPolicy, *conf.Defaults.ThinkingPolicy)
		assert.Equal(t, DefaultCharsPerToken, *conf.Defaults.CharsPerToken)
		assert.Equal(t, DefaultImageTokenEstimate, *conf.Defaults.ImageTokenEstimate)
		require.NotNil(t, conf.Defaults.Rewrite)
		assert.Equal(t, DefaultRewriteStrength, *conf.Defaults.Rewrite.Strength)
		assert.Equal(t, DefaultRewriteProtectedSurvivalRate, *conf.Defaults.Rewrite.ProtectedSurvivalRate)

		rules, ok := conf.Config["AI_product"]
		require.True(t, ok)
		assert.NotNil(t, rules)
		assert.Len(t, rules, 0)
	})

	t.Run("existing settings row and rules export stored values", func(t *testing.T) {
		ruleStore := &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				return []*ContextRuleRow{storageRule(1, "default_t()", "balanced")}, nil
			},
		}
		settingsStore := &fakeAIContextSettingsStorager{
			getFn: func(ctx context.Context) (*SettingsRow, error) {
				return settingsRow(0.8, 4, 4000, 3, 800, "keep", "full", 0.9), nil
			},
		}
		conf := generate(t, ruleStore, settingsStore)

		assert.Equal(t, 0.8, *conf.Defaults.TriggerRatio)
		assert.Equal(t, "keep", *conf.Defaults.ThinkingPolicy)
		assert.Equal(t, "full", *conf.Defaults.Rewrite.Strength)

		rules := conf.Config["AI_product"]
		require.Len(t, rules, 1)
		assert.Equal(t, "default_t()", rules[0].Cond)
		assert.Equal(t, "balanced", rules[0].Mode)
		assert.Equal(t, 64000, *rules[0].MaxContextTokens)
		assert.Equal(t, 8192, *rules[0].ReserveTokens)
	})

	t.Run("marshal uses the frozen export tags verbatim", func(t *testing.T) {
		ruleStore := &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				return []*ContextRuleRow{storageRule(1, "default_t()", "balanced")}, nil
			},
		}
		conf := generate(t, ruleStore, &fakeAIContextSettingsStorager{})
		conf.UpdateVersion("20261001120000")

		raw, err := json.Marshal(conf)
		require.NoError(t, err)

		var top map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &top))
		assert.ElementsMatch(t, []string{"Version", "Defaults", "Config"}, keysOfRaw(top))

		var defaults map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(top["Defaults"], &defaults))
		assert.ElementsMatch(t, []string{
			"triggerRatio", "keepLatestImages", "toolResultMaxChars",
			"thinkingPolicy", "charsPerToken", "imageTokenEstimate", "rewrite",
		}, keysOfRaw(defaults))

		var rewrite map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(defaults["rewrite"], &rewrite))
		assert.ElementsMatch(t, []string{"strength", "protectedSurvivalRate"}, keysOfRaw(rewrite))

		var config map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(top["Config"], &config))
		var rules []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(config["AI_product"], &rules))
		require.Len(t, rules, 1)
		assert.ElementsMatch(t, []string{"cond", "mode", "maxContextTokens", "reserveTokens"}, keysOfRaw(rules[0]))
	})

	t.Run("rules fetch error propagates", func(t *testing.T) {
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*ContextRuleRow, error) {
				return nil, errors.New("db down")
			},
		}, &fakeAIContextSettingsStorager{}, nil, "AI_product")

		_, err := m.ContextRuleGenerator(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch ai context rules error")
	})

	t.Run("settings fetch error propagates", func(t *testing.T) {
		m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, &fakeAIContextSettingsStorager{
			getFn: func(ctx context.Context) (*SettingsRow, error) {
				return nil, errors.New("db down")
			},
		}, nil, "AI_product")

		_, err := m.ContextRuleGenerator(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch ai context settings error")
	})
}

func keysOfRaw(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestAIContextManager_ConfigExport(t *testing.T) {
	ctx := context.Background()

	buildManager := func(versionStore *fakeVersionControlStorager, product string) *AIContextManager {
		var vcm *iversion_control.VersionControlManager
		if versionStore != nil {
			vcm = iversion_control.NewVersionControllerManager(&fakeTxn{}, versionStore)
		}
		return NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, &fakeAIContextSettingsStorager{}, vcm, product)
	}

	t.Run("returns nil when version unchanged", func(t *testing.T) {
		versionStore := &fakeVersionControlStorager{
			upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
				return iversion_control.ZeroVersion, nil
			},
		}
		m := buildManager(versionStore, "AI_product")

		conf, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
		require.NoError(t, err)
		assert.Nil(t, conf)
	})

	t.Run("returns config with new version", func(t *testing.T) {
		versionStore := &fakeVersionControlStorager{
			upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
				return "20261001120000", nil
			},
		}
		m := buildManager(versionStore, "AI_product")

		conf, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
		require.NoError(t, err)
		require.NotNil(t, conf)
		assert.Equal(t, "20261001120000", conf.Version)
		assert.Contains(t, conf.Config, "AI_product")
		require.NotNil(t, conf.Defaults)
	})

	t.Run("settings change drives the export signature", func(t *testing.T) {
		signs := map[string]string{}
		for name, triggerRatio := range map[string]float64{"defaults": DefaultTriggerRatio, "custom": 0.9} {
			var captured *iversion_control.ExportData
			versionStore := &fakeVersionControlStorager{
				upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
					captured = css
					return "20261001103000", nil
				},
			}
			vcm := iversion_control.NewVersionControllerManager(&fakeTxn{}, versionStore)

			settingsStore := &fakeAIContextSettingsStorager{
				getFn: func(ctx context.Context) (*SettingsRow, error) {
					return settingsRow(triggerRatio, DefaultKeepLatestImages, DefaultToolResultMaxChars,
						DefaultCharsPerToken, DefaultImageTokenEstimate, DefaultThinkingPolicy,
						DefaultRewriteStrength, DefaultRewriteProtectedSurvivalRate), nil
				},
			}
			m := NewAIContextManager(&fakeTxn{}, &fakeAIContextRuleStorager{}, settingsStore, vcm, "AI_product")

			_, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
			require.NoError(t, err)
			require.NotNil(t, captured)
			signs[name] = captured.DataSignWithoutVersion
		}

		assert.NotEqual(t, signs["defaults"], signs["custom"])
	})

	t.Run("rules change drives the export signature", func(t *testing.T) {
		signs := map[string]string{}
		for name, withRule := range map[string]bool{"empty": false, "one": true} {
			var captured *iversion_control.ExportData
			versionStore := &fakeVersionControlStorager{
				upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
					captured = css
					return "20261001103000", nil
				},
			}
			vcm := iversion_control.NewVersionControllerManager(&fakeTxn{}, versionStore)

			ruleStore := &fakeAIContextRuleStorager{}
			if withRule {
				ruleStore.fetchAllFn = func(ctx context.Context) ([]*ContextRuleRow, error) {
					return []*ContextRuleRow{storageRule(1, "default_t()", "balanced")}, nil
				}
			}
			m := NewAIContextManager(&fakeTxn{}, ruleStore, &fakeAIContextSettingsStorager{}, vcm, "AI_product")

			_, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
			require.NoError(t, err)
			require.NotNil(t, captured)
			signs[name] = captured.DataSignWithoutVersion
		}

		assert.NotEqual(t, signs["empty"], signs["one"])
	})

	t.Run("version control error propagates", func(t *testing.T) {
		versionStore := &fakeVersionControlStorager{
			upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
				return "", errors.New("version db error")
			},
		}
		m := buildManager(versionStore, "AI_product")

		_, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version db error")
	})
}
