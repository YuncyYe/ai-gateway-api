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

package traffic_mirror

import (
	"context"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

// trafficMirrorRulesResourceID identifies the whole collection in operation
// logs (the collection is the only addressable resource; per-rule id is internal).
const trafficMirrorRulesResourceID = "traffic_mirror_rules"

// RecordSetTrafficMirrorRulesFailure records a failed update audit for a PUT
// rejected by parameter validation (4xx). It is called from the endpoint layer,
// which validation never passes through SetTrafficMirrorRules. Identity is the
// fixed collection id and the before snapshot is read from storage, so the
// audit never depends on the request body (issue #155 discipline).
func (m *TrafficMirrorManager) RecordSetTrafficMirrorRulesFailure(ctx context.Context, param *shared.TrafficMirrorRulesParam, validateErr error) {
	before, _ := m.storager.FetchAll(ctx)

	var after map[string]interface{}
	if param != nil {
		after = trafficMirrorRulesParamToMap(param.Rules)
	}

	m.recordTrafficMirrorRulesOperation(ctx, string(ioperlog.ActionUpdate), trafficMirrorRulesSnapshotToMap(before), after, validateErr)
}

func (m *TrafficMirrorManager) recordTrafficMirrorRulesOperation(ctx context.Context, action string, before, after map[string]interface{}, err error) {
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
		ResourceType: string(ioperlog.ResourceTypeTrafficMirrorRule),
		ResourceID:   trafficMirrorRulesResourceID,
		ResourceName: trafficMirrorRulesResourceID,
		Status:       status,
		ErrorMsg:     errorMsg,
		CreatedAt:    time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(action, before, after)

	m.operationLogManager.Record(ctx, entry)
}

// trafficMirrorRuleParamToMap builds the audit snapshot of a single rule using
// the Open API lowercase vocabulary; nil fields are omitted (nil-guard).
func trafficMirrorRuleParamToMap(rule *shared.TrafficMirrorRuleParam) map[string]interface{} {
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
	if rule.MirrorCluster != nil {
		m["mirror_cluster"] = *rule.MirrorCluster
	}
	if rule.Percentage != nil {
		m["percentage"] = *rule.Percentage
	}
	if rule.RemoveHeaders != nil {
		m["remove_headers"] = *rule.RemoveHeaders
	}
	if rule.SetHeaders != nil {
		m["set_headers"] = rule.SetHeaders
	}
	if len(rule.BodyRewrites) > 0 {
		list := make([]map[string]interface{}, 0, len(rule.BodyRewrites))
		for _, rw := range rule.BodyRewrites {
			if rw == nil {
				continue
			}
			item := map[string]interface{}{}
			if rw.Path != nil {
				item["path"] = *rw.Path
			}
			if rw.Value != nil {
				item["value"] = *rw.Value
			}
			list = append(list, item)
		}
		m["body_rewrites"] = list
	}
	if rule.PathRewrite != nil {
		m["path_rewrite"] = *rule.PathRewrite
	}
	if rule.CreatedAt != nil {
		m["created_at"] = *rule.CreatedAt
	}
	if rule.UpdatedAt != nil {
		m["updated_at"] = *rule.UpdatedAt
	}

	return m
}

// trafficMirrorRulesParamToMap builds the audit snapshot of a submitted rule list.
func trafficMirrorRulesParamToMap(rules []*shared.TrafficMirrorRuleParam) map[string]interface{} {
	list := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		if one := trafficMirrorRuleParamToMap(rule); one != nil {
			list = append(list, one)
		}
	}

	return map[string]interface{}{"rules": list}
}

// trafficMirrorRulesSnapshotToMap builds the audit snapshot of a storage-level
// rule list (converted to the API vocabulary, timestamps included when present).
func trafficMirrorRulesSnapshotToMap(rules []*TrafficMirrorRuleParam) map[string]interface{} {
	list := make([]*shared.TrafficMirrorRuleParam, 0, len(rules))
	for _, rule := range rules {
		list = append(list, trafficMirrorRuleParamToShared(rule))
	}

	return trafficMirrorRulesParamToMap(list)
}
