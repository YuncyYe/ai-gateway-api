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
	"encoding/json"
	"fmt"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/itxn"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
)

// ConfigTopicIntentConfig is the configuration topic of the intent config
// export (config_versions name), independent version line like every other
// topic.
const ConfigTopicIntentConfig = "intent_config"

// IntentConfigDataExport is the exported intent_questions.data payload
// consumed by BFE mod_ai_intent. The JSON tags are the frozen BFE file
// contract (PascalCase; see design-docs/sys-design/details/意图配置与导出.md
// section 5): Data is the file content with the version embedded (the
// ai-route shape), not the ai-cache Version+Config wrapper. The Open API
// stores the questions document in its lowercase vocabulary; the conversion
// into the PascalCase file contract happens in the export generator.
type IntentConfigDataExport struct {
	Version       string                        `json:"Version"`
	MinConfidence float64                       `json:"MinConfidence"`
	Questions     []*IntentConfigQuestionExport `json:"Questions"`
}

// IntentConfigQuestionExport is the per-question PascalCase file structure
// (frozen contract with BFE QuestionFile). Criteria/Levels/MinConfidence use
// omitempty so a choice question exports no Levels key and vice versa,
// exactly like the BFE sample file.
type IntentConfigQuestionExport struct {
	Name          string                     `json:"Name"`
	Type          string                     `json:"Type"`
	Instructions  string                     `json:"Instructions"`
	Criteria      map[string]string          `json:"Criteria,omitempty"`
	Levels        []*IntentConfigLevelExport `json:"Levels,omitempty"`
	MinConfidence *float64                   `json:"MinConfidence,omitempty"`
}

// IntentConfigLevelExport is one score level of the file contract.
type IntentConfigLevelExport struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
}

// UpdateVersion updates the configuration version.
func (conf *IntentConfigDataExport) UpdateVersion(version string) error {
	conf.Version = version
	return nil
}

// IntentConfigManager manages the AI intent config singleton and its export.
type IntentConfigManager struct {
	txn                   itxn.TxnStorager
	storager              IntentConfigStorager
	versionControlManager *iversion_control.VersionControlManager
	operationLogManager   ioperlog.OperationLogRecorder
}

// NewIntentConfigManager creates a new IntentConfigManager.
func NewIntentConfigManager(txn itxn.TxnStorager, storager IntentConfigStorager,
	versionControlManager *iversion_control.VersionControlManager) *IntentConfigManager {
	return &IntentConfigManager{
		txn:                   txn,
		storager:              storager,
		versionControlManager: versionControlManager,
	}
}

// SetOperationLogManager injects the operation log recorder.
func (m *IntentConfigManager) SetOperationLogManager(manager ioperlog.OperationLogRecorder) {
	m.operationLogManager = manager
}

// Get returns the current published intent config. It returns a Record Not
// Exist error when no config has ever been published (the caller maps it to
// 404); a published config with an empty questions array is returned as-is.
func (m *IntentConfigManager) Get(ctx context.Context) (*IntentConfigParam, error) {
	config, err := m.storager.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, xerror.WrapRecordNotExist("intent config")
	}

	return intentConfigToParam(config), nil
}

// Put full-replaces the singleton (single-row upsert, fixed id=1) in one
// transaction: validate the whole document, generate a new version
// (yyyyMMddHHmmss, +1s past the stored version on collision), upsert the row
// and return the re-read config. On any failure the row stays unchanged and
// a failed audit is recorded.
func (m *IntentConfigManager) Put(ctx context.Context, param *IntentConfigParam) (*IntentConfigParam, error) {
	if err := ValidateIntentConfig(param); err != nil {
		return nil, err
	}

	// Fetch the current row for the audit snapshot and the version bump;
	// ignore errors here because the upsert below re-reads the state.
	before, _ := m.storager.Fetch(ctx)
	version := newConfigVersion(before)

	err := m.txn.AtomExecute(ctx, func(ctx context.Context) error {
		return m.storager.Upsert(ctx, intentConfigParamToStorage(param, version))
	})
	if err != nil {
		m.recordPutOperation(ctx, intentConfigSnapshotToMap(before), intentConfigParamToMap(param), err)
		return nil, err
	}

	after, err := m.storager.Fetch(ctx)
	if err != nil {
		m.recordPutOperation(ctx, intentConfigSnapshotToMap(before), intentConfigParamToMap(param), err)
		return nil, err
	}

	m.recordPutOperation(ctx, intentConfigSnapshotToMap(before), intentConfigSnapshotToMap(after), nil)

	return intentConfigToParam(after), nil
}

// ConfigExport exports the intent_questions.data payload for BFE
// mod_ai_intent. When the exported version matches lastVersion (content
// unchanged since the last distribution), it returns nil (HTTP Data is null,
// conf-agent does not rewrite the file nor reload BFE). An unpublished
// config exports as nil as well (BFE keeps its current state).
func (m *IntentConfigManager) ConfigExport(ctx context.Context, lastVersion string) (*IntentConfigDataExport, error) {
	// Unpublished state short-circuits before the export pipeline: the
	// generator would return nil data, which the standard version-control
	// flow cannot consume.
	current, err := m.storager.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, nil
	}

	rst, err := m.versionControlManager.ExportConfig(ctx, ConfigTopicIntentConfig, m.IntentConfigGenerator)
	if err != nil {
		return nil, err
	}

	if rst.DataWithoutVersion == nil {
		return nil, fmt.Errorf("IntentConfigGenerator.DataWithoutVersion is nil")
	}

	conf, ok := rst.DataWithoutVersion.(*IntentConfigDataExport)
	if !ok {
		return nil, fmt.Errorf("convert IntentConfigGenerator.DataWithoutVersion to IntentConfigDataExport is error")
	}

	if conf.Version == lastVersion {
		return nil, nil
	}

	return conf, nil
}

// IntentConfigGenerator generates the intent_questions.data export content:
// the singleton row converted into the frozen PascalCase file contract
// (Version embedded in the file, ai-route shape). When no config has ever
// been published it returns nil, nil: nothing is exported and BFE keeps its
// current state. ConfigExport guards this case before entering the
// version-control flow.
func (m *IntentConfigManager) IntentConfigGenerator(ctx context.Context) (*iversion_control.ExportData, error) {
	config, err := m.storager.Fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch intent config error: %s", err.Error())
	}
	if config == nil {
		return nil, nil
	}

	questions, err := intentQuestionsForExport(config.Questions)
	if err != nil {
		return nil, fmt.Errorf("convert intent config questions error: %s", err.Error())
	}

	conf := &IntentConfigDataExport{
		MinConfidence: config.MinConfidence,
		Questions:     questions,
	}
	conf.UpdateVersion(iversion_control.ZeroVersion)

	return &iversion_control.ExportData{
		Topic:              ConfigTopicIntentConfig,
		DataWithoutVersion: conf,
	}, nil
}

// intentQuestionsForExport parses the stored lowercase questions document
// (validated at PUT time) and converts it into the PascalCase BFE file
// contract. An empty document exports as an empty array (never null): the
// soft switch ships as `Questions: []`.
func intentQuestionsForExport(raw string) ([]*IntentConfigQuestionExport, error) {
	var stored []*intentQuestion
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, err
	}

	export := make([]*IntentConfigQuestionExport, 0, len(stored))
	for _, question := range stored {
		if question == nil {
			continue
		}

		item := &IntentConfigQuestionExport{
			Name:          question.Name,
			Type:          question.Type,
			Instructions:  question.Instructions,
			MinConfidence: question.MinConfidence,
		}
		if question.Type == QuestionTypeChoice {
			item.Criteria = question.Criteria
		}
		if question.Type == QuestionTypeScore {
			levels := make([]*IntentConfigLevelExport, 0, len(question.Levels))
			for _, level := range question.Levels {
				if level == nil {
					continue
				}
				levels = append(levels, &IntentConfigLevelExport{
					Name:        level.Name,
					Description: level.Description,
				})
			}
			item.Levels = levels
		}
		export = append(export, item)
	}

	return export, nil
}
