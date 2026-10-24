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

package traffic_mirror

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

func storageRule(id int64, name, cond, mirrorCluster string) *TrafficMirrorRuleParam {
	now := time.Date(2026, 9, 25, 10, 30, 0, 0, time.UTC)
	return &TrafficMirrorRuleParam{
		ID:            lib.PInt64(id),
		Name:          lib.PString(name),
		Cond:          lib.PString(cond),
		MirrorCluster: lib.PString(mirrorCluster),
		Percentage:    lib.PInt(100),
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}
}

func sharedRule(name, cond, mirrorCluster string) *shared.TrafficMirrorRuleParam {
	return &shared.TrafficMirrorRuleParam{
		Name:          lib.PString(name),
		Cond:          lib.PString(cond),
		MirrorCluster: lib.PString(mirrorCluster),
	}
}

func TestTrafficMirrorManager_GetTrafficMirrorRules(t *testing.T) {
	ctx := context.Background()

	t.Run("returns rules in storage order with timestamps", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return []*TrafficMirrorRuleParam{
					storageRule(1, "rule-a", "default_t()", "cluster_shadow"),
					storageRule(2, "rule-b", "req_path_in(\"/v1\", false)", "cluster_drill"),
				}, nil
			},
		}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")

		rules, err := m.GetTrafficMirrorRules(ctx)
		require.NoError(t, err)
		require.Len(t, rules, 2)
		assert.Equal(t, "rule-a", *rules[0].Name)
		assert.Equal(t, "rule-b", *rules[1].Name)
		assert.Equal(t, "cluster_shadow", *rules[0].MirrorCluster)
		assert.NotNil(t, rules[0].CreatedAt)
	})

	t.Run("empty collection returns empty slice", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return []*TrafficMirrorRuleParam{}, nil
			},
		}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")

		rules, err := m.GetTrafficMirrorRules(ctx)
		require.NoError(t, err)
		assert.NotNil(t, rules)
		assert.Len(t, rules, 0)
	})

	t.Run("storage error propagates", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return nil, errors.New("db down")
			},
		}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")

		_, err := m.GetTrafficMirrorRules(ctx)
		require.Error(t, err)
	})
}

func TestTrafficMirrorManager_SetTrafficMirrorRules(t *testing.T) {
	ctx := context.Background()

	t.Run("replaces collection in slice order with percentage default filled", func(t *testing.T) {
		var replaced [][]*TrafficMirrorRuleParam
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return []*TrafficMirrorRuleParam{}, nil
			},
			replaceAllFn: func(ctx context.Context, rules []*TrafficMirrorRuleParam) error {
				replaced = append(replaced, rules)
				return nil
			},
		}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")

		updated, err := m.SetTrafficMirrorRules(ctx, &shared.TrafficMirrorRulesParam{
			Rules: []*shared.TrafficMirrorRuleParam{
				sharedRule("first", "default_t()", "cluster_shadow"),
				{
					Name:          lib.PString("second"),
					Cond:          lib.PString("req_path_in(\"/v1\", false)"),
					MirrorCluster: lib.PString("cluster_drill"),
					Percentage:    lib.PInt(10),
					RemoveHeaders: &[]string{"Authorization"},
					SetHeaders:    map[string]string{"X-Env": "shadow"},
					BodyRewrites: []*shared.TrafficMirrorBodyRewriteParam{
						{Path: lib.PString("model"), Value: lib.PString("deepseek-v3")},
					},
					PathRewrite: lib.PString("/v1/mirror"),
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, updated)

		// ReplaceAll must be invoked exactly once, in array order.
		require.Len(t, replaced, 1)
		require.Len(t, replaced[0], 2)
		assert.Equal(t, "first", *replaced[0][0].Name)
		assert.Equal(t, "second", *replaced[0][1].Name)

		// Omitted percentage is filled with the documented default; the NULL
		// semantics of the remaining optional fields are preserved.
		assert.Equal(t, DefaultTrafficMirrorPercentage, *replaced[0][0].Percentage)
		assert.Nil(t, replaced[0][0].RemoveHeaders)
		assert.Nil(t, replaced[0][0].SetHeaders)
		assert.Nil(t, replaced[0][0].BodyRewrites)
		assert.Nil(t, replaced[0][0].PathRewrite)

		// Explicit values are preserved verbatim.
		assert.Equal(t, 10, *replaced[0][1].Percentage)
		require.NotNil(t, replaced[0][1].RemoveHeaders)
		assert.Equal(t, []string{"Authorization"}, *replaced[0][1].RemoveHeaders)
		assert.Equal(t, map[string]string{"X-Env": "shadow"}, replaced[0][1].SetHeaders)
		require.Len(t, replaced[0][1].BodyRewrites, 1)
		assert.Equal(t, "model", *replaced[0][1].BodyRewrites[0].Path)
		assert.Equal(t, "/v1/mirror", *replaced[0][1].PathRewrite)
	})

	t.Run("nil param and nil rules clear the collection", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")

		_, err := m.SetTrafficMirrorRules(ctx, nil)
		require.NoError(t, err)
		_, err = m.SetTrafficMirrorRules(ctx, &shared.TrafficMirrorRulesParam{})
		require.NoError(t, err)

		require.Len(t, store.replaceAllCalls, 2)
		assert.Len(t, store.replaceAllCalls[0], 0)
		assert.Len(t, store.replaceAllCalls[1], 0)
	})

	t.Run("replace failure rolls back and records failed audit", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		store := &fakeTrafficMirrorRuleStorager{
			replaceAllFn: func(ctx context.Context, rules []*TrafficMirrorRuleParam) error {
				return errors.New("replace error")
			},
		}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		_, err := m.SetTrafficMirrorRules(ctx, &shared.TrafficMirrorRulesParam{
			Rules: []*shared.TrafficMirrorRuleParam{sharedRule("r1", "default_t()", "cluster_shadow")},
		})
		require.Error(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ActionUpdate), entry.Action)
		assert.Equal(t, string(ioperlog.ResourceTypeTrafficMirrorRule), entry.ResourceType)
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)
		assert.NotEmpty(t, entry.ErrorMsg)
	})

	t.Run("records successful audit with collection snapshots", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		calls := 0
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				calls++
				if calls == 1 {
					return []*TrafficMirrorRuleParam{storageRule(1, "old-rule", "default_t()", "cluster_shadow")}, nil
				}
				return []*TrafficMirrorRuleParam{storageRule(2, "new-rule", "default_t()", "cluster_shadow")}, nil
			},
		}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		_, err := m.SetTrafficMirrorRules(ctx, &shared.TrafficMirrorRulesParam{
			Rules: []*shared.TrafficMirrorRuleParam{sharedRule("new-rule", "default_t()", "cluster_shadow")},
		})
		require.NoError(t, err)

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, ioperlog.StatusSuccess, entry.Status)
		assert.Empty(t, entry.ErrorMsg)

		before, ok := entry.ChangeSummary["before"].(map[string]interface{})
		require.True(t, ok)
		beforeRules, ok := before["rules"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, beforeRules, 1)
		assert.Equal(t, "old-rule", beforeRules[0]["name"])

		after, ok := entry.ChangeSummary["after"].(map[string]interface{})
		require.True(t, ok)
		afterRules, ok := after["rules"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, afterRules, 1)
		assert.Equal(t, "new-rule", afterRules[0]["name"])
		// Snapshot keys use the API lowercase vocabulary; internal id is absent.
		_, hasID := afterRules[0]["id"]
		assert.False(t, hasID)
	})

	t.Run("records failed audit for validation rejection", func(t *testing.T) {
		recorder := &fakeOperationLogRecorder{}
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return []*TrafficMirrorRuleParam{storageRule(1, "old-rule", "default_t()", "cluster_shadow")}, nil
			},
		}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")
		m.SetOperationLogManager(recorder)

		m.RecordSetTrafficMirrorRulesFailure(ctx, &shared.TrafficMirrorRulesParam{
			Rules: []*shared.TrafficMirrorRuleParam{sharedRule("bad-rule", "cond ! compile", "cluster_shadow")},
		}, errors.New("cond.Build(): syntax error"))

		require.Len(t, recorder.entries, 1)
		entry := recorder.entries[0]
		assert.Equal(t, string(ioperlog.ActionUpdate), entry.Action)
		assert.Equal(t, string(ioperlog.ResourceTypeTrafficMirrorRule), entry.ResourceType)
		assert.Equal(t, trafficMirrorRulesResourceID, entry.ResourceID)
		assert.Equal(t, ioperlog.StatusFailed, entry.Status)
		assert.NotEmpty(t, entry.ErrorMsg)

		// Identity and before snapshot come from storage, not the request.
		before, ok := entry.ChangeSummary["before"].(map[string]interface{})
		require.True(t, ok)
		beforeRules, ok := before["rules"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, beforeRules, 1)
		assert.Equal(t, "old-rule", beforeRules[0]["name"])

		after, ok := entry.ChangeSummary["after"].(map[string]interface{})
		require.True(t, ok)
		afterRules, ok := after["rules"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, afterRules, 1)
		assert.Equal(t, "bad-rule", afterRules[0]["name"])
	})

	t.Run("no audit when operation log manager is nil", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{}
		m := NewTrafficMirrorManager(&fakeTxn{}, store, nil, "AI_product")

		_, err := m.SetTrafficMirrorRules(ctx, &shared.TrafficMirrorRulesParam{
			Rules: []*shared.TrafficMirrorRuleParam{sharedRule("r1", "default_t()", "cluster_shadow")},
		})
		require.NoError(t, err)
	})
}

func TestTrafficMirrorManager_TrafficMirrorRuleGenerator(t *testing.T) {
	ctx := context.Background()

	newManager := func(store *fakeTrafficMirrorRuleStorager, product string) *TrafficMirrorManager {
		return NewTrafficMirrorManager(&fakeTxn{}, store, nil, product)
	}

	t.Run("exports rules in id ascending order under injected product name", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return []*TrafficMirrorRuleParam{
					storageRule(1, "first", "default_t()", "cluster_shadow"),
					storageRule(2, "second", "req_path_in(\"/v1\", false)", "cluster_drill"),
				}, nil
			},
		}
		m := newManager(store, "CustomProduct")

		data, err := m.TrafficMirrorRuleGenerator(ctx)
		require.NoError(t, err)
		assert.Equal(t, ConfigTopicProductTrafficMirror, data.Topic)

		conf, ok := data.DataWithoutVersion.(*ExportTrafficMirrorRuleConfig)
		require.True(t, ok)
		require.NotNil(t, conf.Config)
		rules, ok := (*conf.Config)["CustomProduct"]
		require.True(t, ok)
		require.NotNil(t, rules)
		require.Len(t, *rules, 2)
		assert.Equal(t, "default_t()", *(*rules)[0].Cond)
		assert.Equal(t, "req_path_in(\"/v1\", false)", *(*rules)[1].Cond)
		require.NotNil(t, conf.Version)
		assert.Equal(t, iversion_control.ZeroVersion, *conf.Version)
	})

	t.Run("empty table exports empty array with product key present", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return []*TrafficMirrorRuleParam{}, nil
			},
		}
		m := newManager(store, "AI_product")

		data, err := m.TrafficMirrorRuleGenerator(ctx)
		require.NoError(t, err)
		conf := data.DataWithoutVersion.(*ExportTrafficMirrorRuleConfig)

		rules, ok := (*conf.Config)["AI_product"]
		require.True(t, ok)
		require.NotNil(t, rules)
		assert.Len(t, *rules, 0)

		bs, err := json.Marshal(conf)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"AI_product":[]`)
	})

	t.Run("marshaled JSON keys match the frozen contract verbatim", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				rule := storageRule(1, "r1", "default_t()", "cluster_shadow")
				rule.Percentage = lib.PInt(10)
				rule.SetHeaders = map[string]string{"X-Env": "shadow"}
				rule.BodyRewrites = []*TrafficMirrorBodyRewriteParam{
					{Path: lib.PString("model"), Value: lib.PString("deepseek-v3")},
				}
				rule.PathRewrite = lib.PString("/v1/mirror")
				return []*TrafficMirrorRuleParam{rule}, nil
			},
		}
		m := newManager(store, "AI_product")

		data, err := m.TrafficMirrorRuleGenerator(ctx)
		require.NoError(t, err)
		conf := data.DataWithoutVersion.(*ExportTrafficMirrorRuleConfig)
		conf.UpdateVersion("20260925103000")

		bs, err := json.Marshal(conf)
		require.NoError(t, err)

		var top map[string]interface{}
		require.NoError(t, json.Unmarshal(bs, &top))
		assert.ElementsMatch(t, []string{"Version", "Config"}, keysOf(top))

		config := top["Config"].(map[string]interface{})
		assert.ElementsMatch(t, []string{"AI_product"}, keysOf(config))

		rules := config["AI_product"].([]interface{})
		require.Len(t, rules, 1)
		rule := rules[0].(map[string]interface{})
		assert.ElementsMatch(t,
			[]string{"cond", "mirrorCluster", "percentage", "removeHeaders", "setHeaders", "bodyRewrites", "pathRewrite"},
			keysOf(rule))

		rewrites := rule["bodyRewrites"].([]interface{})
		require.Len(t, rewrites, 1)
		assert.ElementsMatch(t, []string{"path", "value"}, keysOf(rewrites[0].(map[string]interface{})))

		// 非空可选字段的值必须原样导出（防生成器漏拷字段——pathRewrite 曾恒导出 ""）。
		assert.Equal(t, "/v1/mirror", rule["pathRewrite"])
		assert.Equal(t, map[string]interface{}{"X-Env": "shadow"}, rule["setHeaders"])
		assert.Equal(t, "model", rewrites[0].(map[string]interface{})["path"])
		assert.Equal(t, "deepseek-v3", rewrites[0].(map[string]interface{})["value"])
	})

	t.Run("nil remove_headers fills default blacklist, explicit empty array does not", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				implicit := storageRule(1, "implicit", "default_t()", "cluster_shadow")
				explicit := storageRule(2, "explicit", "req_path_in(\"/v1\", false)", "cluster_shadow")
				explicit.RemoveHeaders = &[]string{}
				return []*TrafficMirrorRuleParam{implicit, explicit}, nil
			},
		}
		m := newManager(store, "AI_product")

		data, err := m.TrafficMirrorRuleGenerator(ctx)
		require.NoError(t, err)
		conf := data.DataWithoutVersion.(*ExportTrafficMirrorRuleConfig)
		rules := *(*conf.Config)["AI_product"]

		require.Len(t, rules, 2)
		assert.Equal(t, DefaultSensitiveHeaders, rules[0].RemoveHeaders)
		assert.NotNil(t, rules[0].RemoveHeaders)
		assert.Equal(t, []string{}, rules[1].RemoveHeaders)

		// The exported blacklist must be a copy, not the shared package var.
		rules[0].RemoveHeaders[0] = "mutated"
		assert.Equal(t, "Authorization", DefaultSensitiveHeaders[0])
	})

	t.Run("unset optional fields export zero values", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return []*TrafficMirrorRuleParam{storageRule(1, "r1", "default_t()", "cluster_shadow")}, nil
			},
		}
		m := newManager(store, "AI_product")

		data, err := m.TrafficMirrorRuleGenerator(ctx)
		require.NoError(t, err)
		conf := data.DataWithoutVersion.(*ExportTrafficMirrorRuleConfig)
		conf.UpdateVersion("20260925103000")

		bs, err := json.Marshal(conf)
		require.NoError(t, err)

		var top map[string]interface{}
		require.NoError(t, json.Unmarshal(bs, &top))
		rule := top["Config"].(map[string]interface{})["AI_product"].([]interface{})[0].(map[string]interface{})

		assert.Equal(t, map[string]interface{}{}, rule["setHeaders"])
		assert.Equal(t, []interface{}{}, rule["bodyRewrites"])
		assert.Equal(t, "", rule["pathRewrite"])
		assert.Equal(t, []interface{}{"Authorization", "Cookie", "X-Api-Key"}, rule["removeHeaders"])
	})

	t.Run("storage error propagates", func(t *testing.T) {
		store := &fakeTrafficMirrorRuleStorager{
			fetchAllFn: func(ctx context.Context) ([]*TrafficMirrorRuleParam, error) {
				return nil, errors.New("db down")
			},
		}
		m := newManager(store, "AI_product")

		_, err := m.TrafficMirrorRuleGenerator(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch traffic mirror rules error")
	})
}

func TestTrafficMirrorManager_ConfigExport(t *testing.T) {
	ctx := context.Background()

	buildManager := func(versionStore *fakeVersionControlStorager, product string) *TrafficMirrorManager {
		var vcm *iversion_control.VersionControlManager
		if versionStore != nil {
			vcm = iversion_control.NewVersionControllerManager(&fakeTxn{}, versionStore)
		}
		return NewTrafficMirrorManager(&fakeTxn{}, &fakeTrafficMirrorRuleStorager{}, vcm, product)
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
				return "20260925103000", nil
			},
		}
		m := buildManager(versionStore, "AI_product")

		conf, err := m.ConfigExport(ctx, iversion_control.ZeroVersion)
		require.NoError(t, err)
		require.NotNil(t, conf)
		require.NotNil(t, conf.Version)
		assert.Equal(t, "20260925103000", *conf.Version)
		assert.Contains(t, *conf.Config, "AI_product")
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

func TestTrafficMirrorRuleParam_Conversions(t *testing.T) {
	t.Run("nil inputs stay nil", func(t *testing.T) {
		assert.Nil(t, trafficMirrorRuleParamFromShared(nil))
		assert.Nil(t, trafficMirrorRuleParamToShared(nil))
	})

	t.Run("nil optional fields keep NULL semantics and percentage default is filled", func(t *testing.T) {
		rule := trafficMirrorRuleParamFromShared(sharedRule("r1", "default_t()", "cluster_shadow"))
		require.NotNil(t, rule)
		assert.Equal(t, "r1", *rule.Name)
		assert.Equal(t, DefaultTrafficMirrorPercentage, *rule.Percentage)
		assert.Nil(t, rule.RemoveHeaders)
		assert.Nil(t, rule.SetHeaders)
		assert.Nil(t, rule.BodyRewrites)
		assert.Nil(t, rule.PathRewrite)
	})

	t.Run("round trip preserves values", func(t *testing.T) {
		now := time.Date(2026, 9, 25, 10, 30, 0, 0, time.UTC)
		original := &shared.TrafficMirrorRuleParam{
			Name:          lib.PString("r1"),
			Cond:          lib.PString("default_t()"),
			MirrorCluster: lib.PString("cluster_shadow"),
			Percentage:    lib.PInt(10),
			RemoveHeaders: &[]string{"Authorization", "Cookie"},
			SetHeaders:    map[string]string{"X-Env": "shadow"},
			BodyRewrites: []*shared.TrafficMirrorBodyRewriteParam{
				{Path: lib.PString("model"), Value: lib.PString("deepseek-v3")},
			},
			PathRewrite: lib.PString("/v1/mirror"),
			CreatedAt:   &now,
			UpdatedAt:   &now,
		}

		rule := trafficMirrorRuleParamFromShared(original)
		rule.ID = lib.PInt64(7)
		rst := trafficMirrorRuleParamToShared(rule)
		assert.Equal(t, original, rst)
	})
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
