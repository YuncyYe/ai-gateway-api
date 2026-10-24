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

package iintent_config

import (
	"context"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
)

// intentConfigResourceID identifies the singleton in operation logs (the
// singleton is the only addressable resource; the fixed row id is internal).
const intentConfigResourceID = "intent_config"

// RecordPutIntentConfigFailure records a failed update audit for a PUT
// rejected by validation (4xx). It is called from the endpoint layer, which
// validation never passes through Put. Identity is the fixed singleton id
// and the before snapshot is read from storage, so the audit never depends
// on the request body (issue #155 discipline).
func (m *IntentConfigManager) RecordPutIntentConfigFailure(ctx context.Context, param *IntentConfigParam, validateErr error) {
	before, _ := m.storager.Fetch(ctx)

	m.recordPutOperation(ctx, intentConfigSnapshotToMap(before), intentConfigParamToMap(param), validateErr)
}

func (m *IntentConfigManager) recordPutOperation(ctx context.Context, before, after map[string]interface{}, err error) {
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
		Action:       string(ioperlog.ActionUpdate),
		ResourceType: string(ioperlog.ResourceTypeIntentConfig),
		ResourceID:   intentConfigResourceID,
		ResourceName: intentConfigResourceID,
		Status:       status,
		ErrorMsg:     errorMsg,
		CreatedAt:    time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(string(ioperlog.ActionUpdate), before, after)

	m.operationLogManager.Record(ctx, entry)
}

// intentConfigParamToMap builds the audit snapshot of a submitted document
// using the Open API lowercase vocabulary.
func intentConfigParamToMap(param *IntentConfigParam) map[string]interface{} {
	if param == nil {
		return nil
	}

	m := map[string]interface{}{}
	if param.MinConfidence != nil {
		m["min_confidence"] = *param.MinConfidence
	}
	if param.Questions != nil {
		m["questions"] = string(param.Questions)
	}
	if param.CreatedAt != nil {
		m["created_at"] = *param.CreatedAt
	}
	if param.UpdatedAt != nil {
		m["updated_at"] = *param.UpdatedAt
	}

	return m
}

// intentConfigSnapshotToMap builds the audit snapshot of the storage-level
// singleton (converted to the API vocabulary, timestamps included).
func intentConfigSnapshotToMap(config *IntentConfig) map[string]interface{} {
	return intentConfigParamToMap(intentConfigToParam(config))
}
