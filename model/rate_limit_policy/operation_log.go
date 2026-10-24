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

package rate_limit_policy

import (
	"context"
	"strconv"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

func (m *RateLimitPolicyManager) recordRateLimitPolicyOperation(ctx context.Context, action string, policyID, parentID string, before, after map[string]interface{}, err error) {
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
		Action:           action,
		ResourceType:     string(ioperlog.ResourceTypeRateLimitPolicy),
		ResourceID:       policyID,
		ResourceName:     "",
		ResourceParentID: parentID,
		Status:           status,
		ErrorMsg:         errorMsg,
		CreatedAt:        time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(action, before, after)

	m.operationLogManager.Record(ctx, entry)
}

func rateLimitPolicyIDString(id int64) string {
	return strconv.FormatInt(id, 10)
}

// AuditRateLimitPolicyCreate records the audit entry for a rate-limit-policy
// create whose write was executed inside the owning resource's transaction
// (issue #161). policyID is the created policy ID on success and 0 on failure.
func (m *RateLimitPolicyManager) AuditRateLimitPolicyCreate(ctx context.Context, param *shared.RateLimitPolicyParam, policyID int64, owner shared.ResourceOwner, err error) {
	resourceID := ""
	if policyID > 0 {
		resourceID = rateLimitPolicyIDString(policyID)
	}
	m.recordRateLimitPolicyOperation(ctx, string(ioperlog.ActionCreate), resourceID, owner.ID, nil, rateLimitPolicyParamToMap(rateLimitPolicyParamFromShared(param)), err)
}

// AuditRateLimitPolicyUpdate records the audit entry for a rate-limit-policy
// update whose write was executed inside the owning resource's transaction.
func (m *RateLimitPolicyManager) AuditRateLimitPolicyUpdate(ctx context.Context, oldPolicy, param *shared.RateLimitPolicyParam, policyID int64, owner shared.ResourceOwner, err error) {
	resourceID := ""
	if policyID > 0 {
		resourceID = rateLimitPolicyIDString(policyID)
	}
	m.recordRateLimitPolicyOperation(ctx, string(ioperlog.ActionUpdate), resourceID, owner.ID, rateLimitPolicyParamToMap(rateLimitPolicyParamFromShared(oldPolicy)), rateLimitPolicyParamToMap(rateLimitPolicyParamFromShared(param)), err)
}

// AuditRateLimitPolicyDelete records the audit entry for a rate-limit-policy
// delete whose write was executed inside the owning resource's transaction.
func (m *RateLimitPolicyManager) AuditRateLimitPolicyDelete(ctx context.Context, oldPolicy *shared.RateLimitPolicyParam, policyID int64, owner shared.ResourceOwner, err error) {
	resourceID := ""
	if policyID > 0 {
		resourceID = rateLimitPolicyIDString(policyID)
	}
	m.recordRateLimitPolicyOperation(ctx, string(ioperlog.ActionDelete), resourceID, owner.ID, rateLimitPolicyParamToMap(rateLimitPolicyParamFromShared(oldPolicy)), nil, err)
}

// rateLimitPolicyParamFromShared converts the shared rate-limit-policy param
// into the storage-level param so the audit map builder can be reused.
func rateLimitPolicyParamFromShared(param *shared.RateLimitPolicyParam) *RateLimitPolicyParam {
	if param == nil {
		return nil
	}
	result := &RateLimitPolicyParam{
		Enabled: param.Enabled,
	}
	if param.Rules != nil {
		result.MaxConcurrency = param.Rules.MaxConcurrency
		if len(param.Rules.TpmConfigs) > 0 {
			result.TpmConfigs = make([]TPMConfig, 0, len(param.Rules.TpmConfigs))
			for _, c := range param.Rules.TpmConfigs {
				result.TpmConfigs = append(result.TpmConfigs, TPMConfig{
					Name:          c.Name,
					Model:         c.Model,
					WindowMinutes: c.WindowMinutes,
					MaxTokens:     c.MaxTokens,
					StepMinutes:   c.StepMinutes,
				})
			}
		}
		if len(param.Rules.RpmConfigs) > 0 {
			result.RpmConfigs = make([]RPMConfig, 0, len(param.Rules.RpmConfigs))
			for _, c := range param.Rules.RpmConfigs {
				result.RpmConfigs = append(result.RpmConfigs, RPMConfig{
					Name:          c.Name,
					Model:         c.Model,
					WindowMinutes: c.WindowMinutes,
					MaxRequests:   c.MaxRequests,
				})
			}
		}
		result.BatchLimits = batchLimitsFromShared(param.Rules.BatchLimits)
	}
	return result
}

func rateLimitPolicyParamToMap(param *RateLimitPolicyParam) map[string]interface{} {
	if param == nil {
		return nil
	}

	m := map[string]interface{}{}
	if param.ID != nil {
		m["id"] = *param.ID
	}
	if param.Enabled != nil {
		m["enabled"] = *param.Enabled
	}
	if param.MaxConcurrency != nil {
		m["max_concurrency"] = *param.MaxConcurrency
	}
	if len(param.TpmConfigs) > 0 {
		m["tpm_configs"] = param.TpmConfigs
	}
	if len(param.RpmConfigs) > 0 {
		m["rpm_configs"] = param.RpmConfigs
	}
	if param.BatchLimits != nil {
		m["batch_limits"] = param.BatchLimits
	}

	return m
}
