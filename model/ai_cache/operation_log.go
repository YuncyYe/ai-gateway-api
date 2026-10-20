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

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

// aiCacheRulesResourceID identifies the whole collection in operation logs
// (the collection is the only addressable resource; per-rule id is internal).
const aiCacheRulesResourceID = "ai_cache_rules"

// RecordSetAICacheRulesFailure records a failed update audit for a PUT
// rejected by parameter validation (4xx). It is called from the endpoint
// layer, which validation never passes through SetAICacheRules. Identity is
// the fixed collection id and the before snapshot is read from storage, so
// the audit never depends on the request body (issue #155 discipline).
func (m *AICacheManager) RecordSetAICacheRulesFailure(ctx context.Context, param *shared.AICacheRulesParam, validateErr error) {
	before, _ := m.storager.FetchAll(ctx)

	var after map[string]interface{}
	if param != nil {
		after = aiCacheRulesParamToMap(param.Rules)
	}

	m.recordAICacheRulesOperation(ctx, string(ioperlog.ActionUpdate), aiCacheRulesSnapshotToMap(before), after, validateErr)
}

func (m *AICacheManager) recordAICacheRulesOperation(ctx context.Context, action string, before, after map[string]interface{}, err error) {
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
		ResourceType: string(ioperlog.ResourceTypeAICacheRule),
		ResourceID:   aiCacheRulesResourceID,
		ResourceName: aiCacheRulesResourceID,
		Status:       status,
		ErrorMsg:     errorMsg,
		CreatedAt:    time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(action, before, after)

	m.operationLogManager.Record(ctx, entry)
}

// aiCacheSemanticSettingsResourceID identifies the singleton in operation
// logs (the settings have no addressing field beyond the fixed resource id).
const aiCacheSemanticSettingsResourceID = "ai_cache_semantic_settings"

// RecordSetSemanticSettingsFailure records a failed update audit for a PUT
// rejected by parameter validation (4xx). It is called from the endpoint
// layer, which validation never passes through SetSemanticSettings. The
// before snapshot is read from storage (defaults when the table is empty),
// so the audit never depends on the request body (issue #155 discipline).
func (m *AICacheManager) RecordSetSemanticSettingsFailure(ctx context.Context, param *shared.AICacheSemanticSettingsParam, validateErr error) {
	before, _ := m.settingsStorager.Get(ctx)

	var after map[string]interface{}
	if param != nil {
		after = aiCacheSemanticSettingsToMap(semanticSettingsRowToShared(semanticSettingsRowFromShared(param)))
	}

	m.recordSemanticSettingsOperation(ctx, string(ioperlog.ActionUpdate), aiCacheSemanticSettingsToMap(semanticSettingsRowToShared(before)), after, validateErr)
}

func (m *AICacheManager) recordSemanticSettingsOperation(ctx context.Context, action string, before, after map[string]interface{}, err error) {
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
		ResourceType: string(ioperlog.ResourceTypeAICacheSemanticSettings),
		ResourceID:   aiCacheSemanticSettingsResourceID,
		ResourceName: aiCacheSemanticSettingsResourceID,
		Status:       status,
		ErrorMsg:     errorMsg,
		CreatedAt:    time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(action, before, after)

	m.operationLogManager.Record(ctx, entry)
}

// aiCacheSemanticSettingsToMap builds the audit snapshot of the settings
// using the Open API lowercase vocabulary; nil fields are omitted (nil-guard).
func aiCacheSemanticSettingsToMap(param *shared.AICacheSemanticSettingsParam) map[string]interface{} {
	if param == nil {
		return nil
	}

	m := map[string]interface{}{}
	if param.TopK != nil {
		m["top_k"] = *param.TopK
	}
	if param.Threshold != nil {
		m["threshold"] = *param.Threshold
	}
	if param.ThresholdRelation != nil {
		m["threshold_relation"] = *param.ThresholdRelation
	}
	if param.CreatedAt != nil {
		m["created_at"] = *param.CreatedAt
	}
	if param.UpdatedAt != nil {
		m["updated_at"] = *param.UpdatedAt
	}

	return m
}

// aiCacheRuleParamToMap builds the audit snapshot of a single rule using the
// Open API lowercase vocabulary; nil fields are omitted (nil-guard).
func aiCacheRuleParamToMap(rule *shared.AICacheRuleParam) map[string]interface{} {
	if rule == nil {
		return nil
	}

	m := map[string]interface{}{}
	if rule.Name != nil {
		m["name"] = *rule.Name
	}
	if rule.Cond != nil {
		m["cond"] = *rule.Cond
	}
	if rule.CacheKeyStrategy != nil {
		m["cache_key_strategy"] = *rule.CacheKeyStrategy
	}
	if rule.CacheTTL != nil {
		m["cache_ttl"] = *rule.CacheTTL
	}
	if rule.MaxBodyBytes != nil {
		m["max_body_bytes"] = *rule.MaxBodyBytes
	}
	if rule.MaxValueBytes != nil {
		m["max_value_bytes"] = *rule.MaxValueBytes
	}
	if rule.EnableSemanticCache != nil {
		m["enable_semantic_cache"] = *rule.EnableSemanticCache
	}

	return m
}

// aiCacheRulesParamToMap builds the audit snapshot of a submitted rule list.
func aiCacheRulesParamToMap(rules []*shared.AICacheRuleParam) map[string]interface{} {
	list := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		if one := aiCacheRuleParamToMap(rule); one != nil {
			list = append(list, one)
		}
	}

	return map[string]interface{}{"rules": list}
}

// aiCacheRulesSnapshotToMap builds the audit snapshot of a storage-level rule
// list (converted to the API vocabulary; per-rule timestamps are not part of the
// vocabulary since issue #258).
func aiCacheRulesSnapshotToMap(rules []*AICacheRuleParam) map[string]interface{} {
	list := make([]*shared.AICacheRuleParam, 0, len(rules))
	for _, rule := range rules {
		list = append(list, aiCacheRuleParamToShared(rule))
	}

	return aiCacheRulesParamToMap(list)
}
