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

package iintent_config

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var validQuestionsJSON = json.RawMessage(`[
	{"name": "task_type", "type": "choice", "instructions": "这条请求属于哪类研发任务？",
	 "criteria": {"coding": "编写或修改代码", "test_writing": "编写测试用例", "doc_writing": "编写文档"}},
	{"name": "complexity", "type": "score", "instructions": "这个任务的复杂度如何？", "min_confidence": 0.7,
	 "levels": [{"name": "simple", "description": "单步即可完成"},
	            {"name": "medium", "description": "多步但模式常见"},
	            {"name": "complex", "description": "需要深入推理"}]}
]`)

func validPutParam() *IntentConfigParam {
	return &IntentConfigParam{
		MinConfidence: float64Ptr(0.6),
		Questions:     append(json.RawMessage{}, validQuestionsJSON...),
	}
}

func float64Ptr(v float64) *float64 { return &v }

func TestIntentConfigManager_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("published config is returned without version", func(t *testing.T) {
		publishedAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
		store := &fakeIntentConfigStorager{
			config: &IntentConfig{
				Id:            IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.6,
				Questions:     `[{"name":"q1","type":"choice","instructions":"i","criteria":{"a":"b"}}]`,
				CreatedAt:     publishedAt,
				UpdatedAt:     publishedAt,
			},
		}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		param, err := m.Get(ctx)
		require.NoError(t, err)
		require.NotNil(t, param)
		assert.Equal(t, 0.6, *param.MinConfidence)
		assert.JSONEq(t, `[{"name":"q1","type":"choice","instructions":"i","criteria":{"a":"b"}}]`, string(param.Questions))
		assert.Equal(t, publishedAt, *param.CreatedAt)
		assert.Equal(t, publishedAt, *param.UpdatedAt)

		bs, err := json.Marshal(param)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"version"`)
		assert.NotContains(t, string(bs), `"id"`)
	})

	t.Run("unpublished config returns record not exist", func(t *testing.T) {
		m := NewIntentConfigManager(&fakeTxn{}, &fakeIntentConfigStorager{}, nil)

		_, err := m.Get(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Record Not Exist")
	})

	t.Run("fetch error is passed through", func(t *testing.T) {
		store := &fakeIntentConfigStorager{
			fetchFn: func(ctx context.Context) (*IntentConfig, error) {
				return nil, errors.New("db down")
			},
		}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		_, err := m.Get(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}

func TestIntentConfigManager_Put(t *testing.T) {
	ctx := context.Background()

	t.Run("first put inserts the singleton with fixed id and generated version", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		param, err := m.Put(ctx, validPutParam())
		require.NoError(t, err)
		require.NotNil(t, param)
		assert.InDelta(t, 0.6, *param.MinConfidence, 1e-9)

		require.Len(t, store.upsertCalls, 1)
		row := store.upsertCalls[0]
		assert.Equal(t, IntentConfigFixedID, row.Id)
		assertValidVersion(t, row.Version)
		assert.InDelta(t, 0.6, row.MinConfidence, 1e-9)
		assert.JSONEq(t, string(validQuestionsJSON), row.Questions)
	})

	t.Run("omitted min_confidence defaults to 0.6", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		param := validPutParam()
		param.MinConfidence = nil
		_, err := m.Put(ctx, param)
		require.NoError(t, err)

		require.Len(t, store.upsertCalls, 1)
		assert.InDelta(t, DefaultMinConfidence, store.upsertCalls[0].MinConfidence, 1e-9)
	})

	t.Run("second put overwrites and bumps the version past the stored one", func(t *testing.T) {
		base := time.Now().Add(10 * time.Second)
		now := time.Now()
		store := &fakeIntentConfigStorager{
			config: &IntentConfig{
				Id:            IntentConfigFixedID,
				Version:       base.Format(intentConfigVersionLayout),
				MinConfidence: 0.6,
				Questions:     `[]`,
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		param := validPutParam()
		param.MinConfidence = float64Ptr(0.8)
		_, err := m.Put(ctx, param)
		require.NoError(t, err)

		require.Len(t, store.upsertCalls, 1)
		row := store.upsertCalls[0]
		// Stored version is in the future: the new version must bump +1s
		// past it (conflict deferral), never reuse it.
		assert.Equal(t, base.Add(time.Second).Format(intentConfigVersionLayout), row.Version)
		assert.InDelta(t, 0.8, row.MinConfidence, 1e-9)
	})

	t.Run("validation failure writes nothing and records no audit inside put", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		recorder := &fakeOperationLogRecorder{}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)
		m.SetOperationLogManager(recorder)

		_, err := m.Put(ctx, &IntentConfigParam{Questions: json.RawMessage(`"not-an-array"`)})
		require.Error(t, err)
		assert.Empty(t, store.upsertCalls)
		// The failed audit for validation rejections is recorded by the
		// endpoint via RecordPutIntentConfigFailure (see its own test).
		assert.Empty(t, recorder.entries)
	})

	t.Run("upsert failure keeps the published config and records a failed audit", func(t *testing.T) {
		store := &fakeIntentConfigStorager{
			upsertFn: func(ctx context.Context, config *IntentConfig) error {
				return errors.New("duplicate key")
			},
		}
		recorder := &fakeOperationLogRecorder{}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)
		m.SetOperationLogManager(recorder)

		_, err := m.Put(ctx, validPutParam())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate key")

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ResourceTypeIntentConfig), entry.ResourceType)
		assert.Equal(t, intentConfigResourceID, entry.ResourceID)
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)
		assert.NotEmpty(t, entry.ErrorMsg)
	})

	t.Run("successful put records a success audit with before and after", func(t *testing.T) {
		publishedAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
		store := &fakeIntentConfigStorager{
			config: &IntentConfig{
				Id:            IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.6,
				Questions:     `[]`,
				CreatedAt:     publishedAt,
				UpdatedAt:     publishedAt,
			},
		}
		recorder := &fakeOperationLogRecorder{}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)
		m.SetOperationLogManager(recorder)

		_, err := m.Put(ctx, validPutParam())
		require.NoError(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ActionUpdate), entry.Action)
		assert.Equal(t, string(ioperlog.ResourceTypeIntentConfig), entry.ResourceType)
		assert.Equal(t, ioperlog.StatusSuccess, entry.Status)
		assert.NotNil(t, entry.ChangeSummary)
	})

	t.Run("operation log recorder absence does not break put", func(t *testing.T) {
		store := &fakeIntentConfigStorager{}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		_, err := m.Put(ctx, validPutParam())
		require.NoError(t, err)
	})
}

func TestIntentConfigManager_RecordPutIntentConfigFailure(t *testing.T) {
	ctx := context.Background()

	t.Run("records a failed audit with the storage snapshot as before", func(t *testing.T) {
		publishedAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
		store := &fakeIntentConfigStorager{
			config: &IntentConfig{
				Id:            IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.6,
				Questions:     `[]`,
				CreatedAt:     publishedAt,
				UpdatedAt:     publishedAt,
			},
		}
		recorder := &fakeOperationLogRecorder{}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)
		m.SetOperationLogManager(recorder)

		validateErr := ValidateIntentConfig(&IntentConfigParam{Questions: json.RawMessage(`"bad"`)})
		require.Error(t, validateErr)
		m.RecordPutIntentConfigFailure(ctx, &IntentConfigParam{Questions: json.RawMessage(`"bad"`)}, validateErr)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ResourceTypeIntentConfig), entry.ResourceType)
		assert.Equal(t, intentConfigResourceID, entry.ResourceID)
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)
		assert.Contains(t, entry.ErrorMsg, "questions")
		assert.NotNil(t, entry.ChangeSummary)
	})

	t.Run("nil recorder is a no-op", func(t *testing.T) {
		m := NewIntentConfigManager(&fakeTxn{}, &fakeIntentConfigStorager{}, nil)
		m.RecordPutIntentConfigFailure(ctx, validPutParam(), errors.New("bad"))
	})
}

func TestIntentConfigManager_ConfigExport(t *testing.T) {
	ctx := context.Background()

	newManager := func(store *fakeIntentConfigStorager, vc *fakeVersionControlStorager) *IntentConfigManager {
		vcm := iversion_control.NewVersionControllerManager(&fakeTxn{}, vc)
		return NewIntentConfigManager(&fakeTxn{}, store, vcm)
	}

	published := func() *fakeIntentConfigStorager {
		return &fakeIntentConfigStorager{
			config: &IntentConfig{
				Id:            IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.6,
				Questions:     string(validQuestionsJSON),
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			},
		}
	}

	t.Run("unpublished config exports nil without touching version control", func(t *testing.T) {
		vc := &fakeVersionControlStorager{}
		m := newManager(&fakeIntentConfigStorager{}, vc)

		conf, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
		require.NoError(t, err)
		assert.Nil(t, conf)
		assert.Empty(t, vc.seen)
	})

	t.Run("unchanged version exports nil", func(t *testing.T) {
		vc := &fakeVersionControlStorager{
			upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
				return "20260926120000", nil
			},
		}
		m := newManager(published(), vc)

		conf, err := m.ConfigExport(ctx, "20260926120000")
		require.NoError(t, err)
		assert.Nil(t, conf)
	})

	t.Run("changed content exports the file content with the new version", func(t *testing.T) {
		vc := &fakeVersionControlStorager{
			upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
				return "20260926130000", nil
			},
		}
		m := newManager(published(), vc)

		conf, err := m.ConfigExport(ctx, "20260926120000")
		require.NoError(t, err)
		require.NotNil(t, conf)
		assert.Equal(t, "20260926130000", conf.Version)
		assert.InDelta(t, 0.6, conf.MinConfidence, 1e-9)
		require.Len(t, conf.Questions, 2)
		assert.Equal(t, "task_type", conf.Questions[0].Name)
		assert.Equal(t, "complexity", conf.Questions[1].Name)

		// The export went through the standard version-control flow with
		// the intent_config topic and a version-independent sign.
		require.Len(t, vc.seen, 1)
		assert.Equal(t, ConfigTopicIntentConfig, vc.seen[0].Topic)
		assert.NotEmpty(t, vc.seen[0].DataSignWithoutVersion)
	})

	t.Run("version control failure is passed through", func(t *testing.T) {
		vc := &fakeVersionControlStorager{
			upsertFn: func(ctx context.Context, css *iversion_control.ExportData) (string, error) {
				return "", errors.New("vc down")
			},
		}
		m := newManager(published(), vc)

		_, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vc down")
	})
}

func TestIntentConfigManager_IntentConfigGenerator(t *testing.T) {
	ctx := context.Background()

	t.Run("no record returns nil", func(t *testing.T) {
		m := NewIntentConfigManager(&fakeTxn{}, &fakeIntentConfigStorager{}, nil)

		rst, err := m.IntentConfigGenerator(ctx)
		require.NoError(t, err)
		assert.Nil(t, rst)
	})

	t.Run("published config converts into the frozen PascalCase file shape", func(t *testing.T) {
		store := &fakeIntentConfigStorager{
			config: &IntentConfig{
				Id:            IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.7,
				Questions:     string(validQuestionsJSON),
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			},
		}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		rst, err := m.IntentConfigGenerator(ctx)
		require.NoError(t, err)
		require.NotNil(t, rst)
		assert.Equal(t, ConfigTopicIntentConfig, rst.Topic)

		conf, ok := rst.DataWithoutVersion.(*IntentConfigDataExport)
		require.True(t, ok)
		assert.Equal(t, iversion_control.ZeroVersion, conf.Version)
		assert.InDelta(t, 0.7, conf.MinConfidence, 1e-9)
		require.Len(t, conf.Questions, 2)

		// Exact JSON keys: the BFE file contract is PascalCase with the
		// version embedded (ai-route shape, no Config wrapper); criteria and
		// levels stay mutually exclusive exactly like the BFE sample file.
		bs, err := json.Marshal(conf)
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"Version": "00010101000000",
			"MinConfidence": 0.7,
			"Questions": [
				{"Name": "task_type", "Type": "choice",
				 "Instructions": "这条请求属于哪类研发任务？",
				 "Criteria": {"coding": "编写或修改代码", "test_writing": "编写测试用例", "doc_writing": "编写文档"}},
				{"Name": "complexity", "Type": "score",
				 "Instructions": "这个任务的复杂度如何？", "MinConfidence": 0.7,
				 "Levels": [{"Name": "simple", "Description": "单步即可完成"},
				            {"Name": "medium", "Description": "多步但模式常见"},
				            {"Name": "complex", "Description": "需要深入推理"}]}
			]
		}`, string(bs))
		assert.NotContains(t, string(bs), `"Levels":null`)
		assert.NotContains(t, string(bs), `"Criteria":null`)
		assert.NotContains(t, string(bs), `"name"`)
	})

	t.Run("empty questions array exports as empty array soft switch", func(t *testing.T) {
		store := &fakeIntentConfigStorager{
			config: &IntentConfig{
				Id:            IntentConfigFixedID,
				Version:       "20260926120000",
				MinConfidence: 0.6,
				Questions:     `[]`,
			},
		}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		rst, err := m.IntentConfigGenerator(ctx)
		require.NoError(t, err)
		require.NotNil(t, rst)
		conf := rst.DataWithoutVersion.(*IntentConfigDataExport)
		require.NotNil(t, conf.Questions)
		assert.Len(t, conf.Questions, 0)

		bs, err := json.Marshal(conf)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"Questions":[]`)
	})

	t.Run("fetch error is wrapped", func(t *testing.T) {
		store := &fakeIntentConfigStorager{
			fetchFn: func(ctx context.Context) (*IntentConfig, error) {
				return nil, errors.New("db down")
			},
		}
		m := NewIntentConfigManager(&fakeTxn{}, store, nil)

		_, err := m.IntentConfigGenerator(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch intent config error")
	})
}

func TestNewConfigVersion(t *testing.T) {
	t.Run("no stored config uses the current timestamp", func(t *testing.T) {
		version := newConfigVersion(nil)
		assertValidVersion(t, version)
		assert.WithinDuration(t, time.Now(), mustParseVersion(t, version), 2*time.Second)
	})

	t.Run("stored version in the future is bumped exactly one second", func(t *testing.T) {
		base := time.Now().Add(10 * time.Second)
		version := newConfigVersion(&IntentConfig{Version: base.Format(intentConfigVersionLayout)})
		assert.Equal(t, base.Add(time.Second).Format(intentConfigVersionLayout), version)
	})

	t.Run("stored version in the past keeps the current timestamp", func(t *testing.T) {
		stored := time.Now().Add(-10 * time.Second).Format(intentConfigVersionLayout)
		version := newConfigVersion(&IntentConfig{Version: stored})
		assertValidVersion(t, version)
		assert.True(t, version > stored)
		assert.WithinDuration(t, time.Now(), mustParseVersion(t, version), 2*time.Second)
	})

	t.Run("unparsable stored version falls back to the current timestamp", func(t *testing.T) {
		version := newConfigVersion(&IntentConfig{Version: "not-a-version"})
		assertValidVersion(t, version)
	})
}

func assertValidVersion(t *testing.T, version string) {
	t.Helper()
	mustParseVersion(t, version)
}

func mustParseVersion(t *testing.T, version string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation(intentConfigVersionLayout, version, time.Local)
	require.NoError(t, err, "version %q must use the yyyyMMddHHmmss layout", version)
	return parsed
}
