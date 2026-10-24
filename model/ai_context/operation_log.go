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

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

// aiContextRulesResourceID identifies the whole collection in operation logs
// (the collection is the only addressable resource; per-rule id is internal).
const aiContextRulesResourceID = "ai_context_rules"

// RecordSetRulesFailure records a failed update audit for a PUT rejected by
// parameter validation (4xx). It is called from the endpoint layer, which
// validation never passes through SetRules. Identity is the fixed collection
// id and the before snapshot is read from storage, so the audit never
// depends on the request body (issue #155 discipline).
func (m *AIContextManager) RecordSetRulesFailure(ctx context.Context, param *shared.AIContextRulesParam, validateErr error) {
	before, _ := m.storager.FetchAll(ctx)

	var after map[string]interface{}
	if param != nil {
		after = aiContextRulesParamToMap(param.Rules)
	}

	m.recordRulesOperation(ctx, string(ioperlog.ActionUpdate), aiContextRulesSnapshotToMap(before), after, validateErr)
}

func (m *AIContextManager) recordRulesOperation(ctx context.Context, action string, before, after map[string]interface{}, err error) {
	if m.operationLogManager == nil {
		return
	}

	status := ioperlog.StatusSuccess
	errorMsg := ""
	if err != nil {
		status = ioperlog.StatusFailed
		errorMsg = ioperlog.TruncateErrorMessageDefault(err)
	}

	entry := &ioperlog.OperationLogEntry{
		Action:       action,
		ResourceType: string(ioperlog.ResourceTypeAIContextRule),
		ResourceID:   aiContextRulesResourceID,
		ResourceName: aiContextRulesResourceID,
		Status:       status,
		ErrorMsg:     errorMsg,
		CreatedAt:    time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(action, before, after)

	m.operationLogManager.Record(ctx, entry)
}

// aiContextSettingsResourceID identifies the singleton in operation logs (the
// settings have no addressing field beyond the fixed resource id).
const aiContextSettingsResourceID = "ai_context_settings"

// RecordSetSettingsFailure records a failed update audit for a PUT rejected by
// parameter validation (4xx). It is called from the endpoint layer, which
// validation never passes through SetSettings. The before snapshot is read
// from storage (defaults when the table is empty), so the audit never depends
// on the request body (issue #155 discipline).
func (m *AIContextManager) RecordSetSettingsFailure(ctx context.Context, param *shared.AIContextSettingsParam, validateErr error) {
	before, _ := m.settingsStorager.Get(ctx)

	var after map[string]interface{}
	if param != nil {
		after = aiContextSettingsToMap(settingsRowToShared(settingsRowFromShared(param)))
	}

	m.recordSettingsOperation(ctx, string(ioperlog.ActionUpdate), aiContextSettingsToMap(settingsRowToShared(before)), after, validateErr)
}

func (m *AIContextManager) recordSettingsOperation(ctx context.Context, action string, before, after map[string]interface{}, err error) {
	if m.operationLogManager == nil {
		return
	}

	status := ioperlog.StatusSuccess
	errorMsg := ""
	if err != nil {
		status = ioperlog.StatusFailed
		errorMsg = ioperlog.TruncateErrorMessageDefault(err)
	}

	entry := &ioperlog.OperationLogEntry{
		Action:       action,
		ResourceType: string(ioperlog.ResourceTypeAIContextSettings),
		ResourceID:   aiContextSettingsResourceID,
		ResourceName: aiContextSettingsResourceID,
		Status:       status,
		ErrorMsg:     errorMsg,
		CreatedAt:    time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(action, before, after)

	m.operationLogManager.Record(ctx, entry)
}

// aiContextSettingsToMap builds the audit snapshot of the settings using the
// Open API lowercase vocabulary; nil fields are omitted (nil-guard).
func aiContextSettingsToMap(param *shared.AIContextSettingsParam) map[string]interface{} {
	if param == nil {
		return nil
	}

	m := map[string]interface{}{}
	if param.TriggerRatio != nil {
		m["trigger_ratio"] = *param.TriggerRatio
	}
	if param.KeepLatestImages != nil {
		m["keep_latest_images"] = *param.KeepLatestImages
	}
	if param.ToolResultMaxChars != nil {
		m["tool_result_max_chars"] = *param.ToolResultMaxChars
	}
	if param.ThinkingPolicy != nil {
		m["thinking_policy"] = *param.ThinkingPolicy
	}
	if param.CharsPerToken != nil {
		m["chars_per_token"] = *param.CharsPerToken
	}
	if param.ImageTokenEstimate != nil {
		m["image_token_estimate"] = *param.ImageTokenEstimate
	}
	if param.Rewrite != nil {
		rewrite := map[string]interface{}{}
		if param.Rewrite.Strength != nil {
			rewrite["strength"] = *param.Rewrite.Strength
		}
		if param.Rewrite.ProtectedSurvivalRate != nil {
			rewrite["protected_survival_rate"] = *param.Rewrite.ProtectedSurvivalRate
		}
		m["rewrite"] = rewrite
	}

	return m
}

// aiContextRuleParamToMap builds the audit snapshot of a single rule using
// the Open API lowercase vocabulary; nil fields are omitted (nil-guard).
func aiContextRuleParamToMap(rule *shared.AIContextRuleParam) map[string]interface{} {
	if rule == nil {
		return nil
	}

	m := map[string]interface{}{}
	if rule.Cond != nil {
		m["cond"] = *rule.Cond
	}
	if rule.Mode != nil {
		m["mode"] = *rule.Mode
	}
	if rule.MaxContextTokens != nil {
		m["max_context_tokens"] = *rule.MaxContextTokens
	}
	if rule.ReserveTokens != nil {
		m["reserve_tokens"] = *rule.ReserveTokens
	}

	return m
}

// aiContextRulesParamToMap builds the audit snapshot of a submitted rule list.
func aiContextRulesParamToMap(rules []*shared.AIContextRuleParam) map[string]interface{} {
	list := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		if one := aiContextRuleParamToMap(rule); one != nil {
			list = append(list, one)
		}
	}

	return map[string]interface{}{"rules": list}
}

// aiContextRulesSnapshotToMap builds the audit snapshot of a storage-level
// rule list (converted to the API vocabulary, timestamps included when present).
func aiContextRulesSnapshotToMap(rules []*ContextRuleRow) map[string]interface{} {
	list := make([]*shared.AIContextRuleParam, 0, len(rules))
	for _, rule := range rules {
		list = append(list, contextRuleRowToShared(rule))
	}

	return aiContextRulesParamToMap(list)
}
