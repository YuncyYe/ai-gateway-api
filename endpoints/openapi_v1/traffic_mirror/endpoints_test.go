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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints/openapi_v1/internal/testutil"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibasic"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/icluster_conf"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	trafficMirrorModel "github.com/rainway-ai-gateway/ai-gateway-api/model/traffic_mirror"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTrafficMirrorStorager emulates the DB behavior: ReplaceAll rebuilds the
// table (new ids in slice order, timestamps attached), FetchAll reads it back.
type fakeTrafficMirrorStorager struct {
	rules  []*trafficMirrorModel.TrafficMirrorRuleParam
	nextID int64
}

func (f *fakeTrafficMirrorStorager) FetchAll(ctx context.Context) ([]*trafficMirrorModel.TrafficMirrorRuleParam, error) {
	return f.rules, nil
}

func (f *fakeTrafficMirrorStorager) ReplaceAll(ctx context.Context, rules []*trafficMirrorModel.TrafficMirrorRuleParam) error {
	f.rules = make([]*trafficMirrorModel.TrafficMirrorRuleParam, 0, len(rules))
	now := time.Date(2026, 9, 25, 10, 30, 0, 0, time.UTC)
	for _, r := range rules {
		f.nextID++
		f.rules = append(f.rules, &trafficMirrorModel.TrafficMirrorRuleParam{
			ID:            lib.PInt64(f.nextID),
			Name:          r.Name,
			Cond:          r.Cond,
			MirrorCluster: r.MirrorCluster,
			Percentage:    r.Percentage,
			RemoveHeaders: r.RemoveHeaders,
			SetHeaders:    r.SetHeaders,
			BodyRewrites:  r.BodyRewrites,
			PathRewrite:   r.PathRewrite,
			CreatedAt:     &now,
			UpdatedAt:     &now,
		})
	}
	return nil
}

// fakeClusterStorager serves the referenced cluster names for the existence check.
type fakeClusterStorager struct {
	names []string
}

func (f *fakeClusterStorager) FetchCluster(ctx context.Context, param *icluster_conf.ClusterFilter) (*icluster_conf.Cluster, error) {
	return nil, nil
}

func (f *fakeClusterStorager) FetchClusterList(ctx context.Context, param *icluster_conf.ClusterFilter) ([]*icluster_conf.Cluster, error) {
	if param == nil || len(param.Names) == 0 {
		return nil, nil
	}
	want := map[string]bool{}
	for _, name := range param.Names {
		want[name] = true
	}
	rst := []*icluster_conf.Cluster{}
	for _, name := range f.names {
		if want[name] {
			rst = append(rst, &icluster_conf.Cluster{Name: name})
		}
	}
	return rst, nil
}

func (f *fakeClusterStorager) ClusterUpdate(ctx context.Context, product *ibasic.Product, old *icluster_conf.Cluster, param *icluster_conf.ClusterParam) error {
	return nil
}

func (f *fakeClusterStorager) ClusterCreate(ctx context.Context, product *ibasic.Product, param *icluster_conf.ClusterParam, subClusters []*icluster_conf.SubCluster) (int64, error) {
	return 0, nil
}

func (f *fakeClusterStorager) ClusterDelete(ctx context.Context, product *ibasic.Product, cluster *icluster_conf.Cluster) error {
	return nil
}

func (f *fakeClusterStorager) BindSubCluster(ctx context.Context, cluster *icluster_conf.Cluster, appendSubClusters, unbindSubClusters []*icluster_conf.SubCluster) error {
	return nil
}

func (f *fakeClusterStorager) FetchLBMatrixList(ctx context.Context) (map[int64]map[string]map[string]int, error) {
	return nil, nil
}

type fakeSubClusterStorager struct{}

func (f *fakeSubClusterStorager) FetchSubClusterList(ctx context.Context, param *icluster_conf.SubClusterFilter) ([]*icluster_conf.SubCluster, error) {
	return nil, nil
}

func (f *fakeSubClusterStorager) CreateSubCluster(ctx context.Context, param *icluster_conf.SubClusterParam) error {
	return nil
}

func (f *fakeSubClusterStorager) DeleteSubCluster(ctx context.Context, param *icluster_conf.SubCluster) error {
	return nil
}

func (f *fakeSubClusterStorager) UpdateSubCluster(ctx context.Context, one *icluster_conf.SubCluster, param *icluster_conf.SubClusterParam) error {
	return nil
}

type fakeBFEClusterStorager struct{}

func (f *fakeBFEClusterStorager) DeleteBFECluster(ctx context.Context, cluster *ibasic.BFECluster) error {
	return nil
}

func (f *fakeBFEClusterStorager) CreateBFECluster(ctx context.Context, param *ibasic.BFEClusterParam) error {
	return nil
}

func (f *fakeBFEClusterStorager) FetchBFEClusters(ctx context.Context, param *ibasic.BFEClusterFilter) ([]*ibasic.BFECluster, error) {
	return nil, nil
}

type fakePoolStorager struct{}

func (f *fakePoolStorager) FetchPool(ctx context.Context, name string) (*icluster_conf.Pool, error) {
	return nil, nil
}

func (f *fakePoolStorager) FetchPools(ctx context.Context, param *icluster_conf.PoolFilter) ([]*icluster_conf.Pool, error) {
	return nil, nil
}

func (f *fakePoolStorager) CreatePool(ctx context.Context, product *ibasic.Product, data *icluster_conf.PoolParam) (*icluster_conf.Pool, error) {
	return nil, nil
}

func (f *fakePoolStorager) UpdatePool(ctx context.Context, oldData *icluster_conf.Pool, param *icluster_conf.PoolParam) error {
	return nil
}

func (f *fakePoolStorager) DeletePool(ctx context.Context, pool *icluster_conf.Pool) error {
	return nil
}

var existingClusterNames = []string{"cluster_shadow", "cluster_drill"}

func setupTrafficMirrorManager(storager trafficMirrorModel.TrafficMirrorStorager) func() {
	oldManager := container.TrafficMirrorManager
	oldClusterManager := container.ClusterManager

	container.TrafficMirrorManager = trafficMirrorModel.NewTrafficMirrorManager(
		&testutil.FakeTxn{}, storager, nil, "AI_product")
	container.ClusterManager = icluster_conf.NewClusterManager(
		&testutil.FakeTxn{},
		&fakeClusterStorager{names: existingClusterNames},
		&fakeSubClusterStorager{},
		&fakeBFEClusterStorager{},
		&fakePoolStorager{},
		nil,
		nil,
		nil,
		nil,
	)

	return func() {
		container.TrafficMirrorManager = oldManager
		container.ClusterManager = oldClusterManager
	}
}

func TestTrafficMirrorRulesGetAction(t *testing.T) {
	t.Run("returns rule collection without internal id", func(t *testing.T) {
		now := time.Date(2026, 9, 25, 10, 30, 0, 0, time.UTC)
		removeHeaders := []string{"Authorization", "Cookie", "X-Api-Key"}
		store := &fakeTrafficMirrorStorager{
			nextID: 2,
			rules: []*trafficMirrorModel.TrafficMirrorRuleParam{
				{
					ID:            lib.PInt64(1),
					Name:          lib.PString("rule1"),
					Cond:          lib.PString("default_t()"),
					MirrorCluster: lib.PString("cluster_shadow"),
					Percentage:    lib.PInt(10),
					RemoveHeaders: &removeHeaders,
					CreatedAt:     &now,
					UpdatedAt:     &now,
				},
			},
		}
		defer setupTrafficMirrorManager(store)()

		req := httptest.NewRequest(http.MethodGet, "/traffic-mirror-rules", nil)
		data, err := TrafficMirrorRulesGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)

		result, ok := data.(*shared.TrafficMirrorRulesParam)
		require.True(t, ok)
		require.Len(t, result.Rules, 1)
		assert.Equal(t, "rule1", *result.Rules[0].Name)
		assert.Equal(t, "cluster_shadow", *result.Rules[0].MirrorCluster)
		assert.Equal(t, []string{"Authorization", "Cookie", "X-Api-Key"}, *result.Rules[0].RemoveHeaders)
		assert.NotNil(t, result.Rules[0].CreatedAt)
	})

	t.Run("empty collection returns empty rules array", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		req := httptest.NewRequest(http.MethodGet, "/traffic-mirror-rules", nil)
		data, err := TrafficMirrorRulesGetAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"rules":[]`)
	})
}

func TestTrafficMirrorRulesUpdateAction(t *testing.T) {
	validBody := `{
		"rules": [
			{
				"name": "mirror-gpt4o-to-shadow",
				"cond": "req_path_in(\"/v1/chat/completions\", false) && req_body_json_in(\"model\", \"gpt-4o\", false)",
				"mirror_cluster": "cluster_shadow",
				"percentage": 10,
				"body_rewrites": [
					{"path": "model", "value": "deepseek-v3"}
				]
			},
			{
				"name": "mirror-all-fallback-drill",
				"cond": "req_path_in(\"/v1/chat/completions\", false)",
				"mirror_cluster": "cluster_drill",
				"remove_headers": ["Authorization", "Cookie", "X-Api-Key", "X-Custom-Secret"]
			}
		]
	}`

	t.Run("round trip with percentage backfill and no internal id", func(t *testing.T) {
		store := &fakeTrafficMirrorStorager{}
		defer setupTrafficMirrorManager(store)()

		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(validBody))
		data, err := TrafficMirrorRulesUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.NotContains(t, string(bs), `"id"`)

		result, ok := data.(*shared.TrafficMirrorRulesParam)
		require.True(t, ok)
		require.Len(t, result.Rules, 2)

		assert.Equal(t, "mirror-gpt4o-to-shadow", *result.Rules[0].Name)
		assert.Equal(t, 10, *result.Rules[0].Percentage)
		require.Len(t, result.Rules[0].BodyRewrites, 1)
		assert.Equal(t, "model", *result.Rules[0].BodyRewrites[0].Path)
		assert.Equal(t, "deepseek-v3", *result.Rules[0].BodyRewrites[0].Value)
		// remove_headers was not submitted: stays absent (NULL semantics).
		assert.Nil(t, result.Rules[0].RemoveHeaders)

		// Omitted percentage is backfilled with the default.
		assert.Equal(t, "mirror-all-fallback-drill", *result.Rules[1].Name)
		assert.Equal(t, 100, *result.Rules[1].Percentage)
		assert.Equal(t, []string{"Authorization", "Cookie", "X-Api-Key", "X-Custom-Secret"}, *result.Rules[1].RemoveHeaders)
		assert.NotNil(t, result.Rules[1].CreatedAt)
	})

	t.Run("empty rules clear the collection", func(t *testing.T) {
		store := &fakeTrafficMirrorStorager{}
		defer setupTrafficMirrorManager(store)()

		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(`{"rules": []}`))
		data, err := TrafficMirrorRulesUpdateAction(req)
		require.NoError(t, err)

		bs, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(bs), `"rules":[]`)
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader("not-json"))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("422 invalid cond", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [{"name": "bad-cond", "cond": "unknown_func()", "mirror_cluster": "cluster_shadow"}]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad-cond")
	})

	t.Run("422 duplicate name in collection", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [
			{"name": "dup", "cond": "default_t()", "mirror_cluster": "cluster_shadow"},
			{"name": "dup", "cond": "req_path_in(\"/v1\", false)", "mirror_cluster": "cluster_shadow"}
		]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("422 duplicate cond in collection", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [
			{"name": "rule-a", "cond": "default_t()", "mirror_cluster": "cluster_shadow"},
			{"name": "rule-b", "cond": "default_t()", "mirror_cluster": "cluster_drill"}
		]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("422 unknown mirror_cluster leaves collection unchanged", func(t *testing.T) {
		store := &fakeTrafficMirrorStorager{}
		defer setupTrafficMirrorManager(store)()

		body := `{"rules": [{"name": "bad-cluster", "cond": "default_t()", "mirror_cluster": "cluster_ghost"}]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cluster_ghost")
		assert.Len(t, store.rules, 0)
	})

	t.Run("422 percentage out of range", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [{"name": "bad-percentage", "cond": "default_t()", "mirror_cluster": "cluster_shadow", "percentage": 101}]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "percentage")
	})

	t.Run("422 body_rewrites path not model", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [{"name": "bad-rewrite", "cond": "default_t()", "mirror_cluster": "cluster_shadow", "body_rewrites": [{"path": "temperature", "value": "0"}]}]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "body_rewrites")
	})

	t.Run("422 path_rewrite not slash-prefixed", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [{"name": "bad-path", "cond": "default_t()", "mirror_cluster": "cluster_shadow", "path_rewrite": "v1/mirror"}]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path_rewrite")
	})

	t.Run("422 null rule element", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [null]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
	})

	t.Run("422 empty name and oversized name", func(t *testing.T) {
		defer setupTrafficMirrorManager(&fakeTrafficMirrorStorager{})()

		body := `{"rules": [{"name": "", "cond": "default_t()", "mirror_cluster": "cluster_shadow"}]}`
		req := httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err := TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)

		longName := strings.Repeat("a", 129)
		body = `{"rules": [{"name": "` + longName + `", "cond": "default_t()", "mirror_cluster": "cluster_shadow"}]}`
		req = httptest.NewRequest(http.MethodPut, "/traffic-mirror-rules", strings.NewReader(body))
		_, err = TrafficMirrorRulesUpdateAction(req)
		require.Error(t, err)
	})
}
