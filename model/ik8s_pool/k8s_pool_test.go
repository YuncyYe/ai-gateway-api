// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ik8s_pool

import (
	"context"
	"errors"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/icluster_conf"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateInstances(t *testing.T) {
	t.Run("empty list is valid zero instances", func(t *testing.T) {
		instances, err := ValidateInstances(nil)
		require.NoError(t, err)
		require.NotNil(t, instances)
		assert.Len(t, instances, 0)
	})

	t.Run("weight defaults to 100", func(t *testing.T) {
		instances, err := ValidateInstances([]InstanceParam{
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(8000)},
		})
		require.NoError(t, err)
		require.Len(t, instances, 1)
		assert.Equal(t, int64(100), instances[0].Weight)
		assert.Equal(t, "10.0.0.1", instances[0].Addr)
		assert.Equal(t, 8000, instances[0].Port)
	})

	t.Run("explicit weight kept", func(t *testing.T) {
		instances, err := ValidateInstances([]InstanceParam{
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(8000), Weight: lib.PInt64(0)},
			{Addr: lib.PString("10.0.0.2"), Port: lib.PInt(8000), Weight: lib.PInt64(100)},
		})
		require.NoError(t, err)
		require.Len(t, instances, 2)
		assert.Equal(t, int64(0), instances[0].Weight)
		assert.Equal(t, int64(100), instances[1].Weight)
	})

	t.Run("weight out of range rejected", func(t *testing.T) {
		_, err := ValidateInstances([]InstanceParam{
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(8000), Weight: lib.PInt64(101)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "weight")

		_, err = ValidateInstances([]InstanceParam{
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(8000), Weight: lib.PInt64(-1)},
		})
		require.Error(t, err)
	})

	t.Run("missing addr rejected", func(t *testing.T) {
		_, err := ValidateInstances([]InstanceParam{
			{Port: lib.PInt(8000)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "addr")

		_, err = ValidateInstances([]InstanceParam{
			{Addr: lib.PString("  "), Port: lib.PInt(8000)},
		})
		require.Error(t, err)
	})

	t.Run("port out of range rejected", func(t *testing.T) {
		_, err := ValidateInstances([]InstanceParam{
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(0)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "port")

		_, err = ValidateInstances([]InstanceParam{
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(65536)},
		})
		require.Error(t, err)
	})

	t.Run("duplicate addr/port rejected", func(t *testing.T) {
		_, err := ValidateInstances([]InstanceParam{
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(8000)},
			{Addr: lib.PString("10.0.0.1"), Port: lib.PInt(8000), Weight: lib.PInt64(50)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})
}

func makeReferencingCluster(providerName string) *icluster_conf.Cluster {
	return &icluster_conf.Cluster{
		ID:   1,
		Name: "c1",
		LLMConfig: &icluster_conf.LLMConfig{
			Provider: lib.PString(providerName),
		},
		SubClusters: []*icluster_conf.SubCluster{
			{
				ID:   1,
				Name: "sc1",
				InstancePool: &icluster_conf.Pool{
					ID:   1,
					Name: "p.c1",
				},
			},
		},
	}
}

func TestK8sPoolManager_ReplaceInstances(t *testing.T) {
	ctx := context.Background()

	t.Run("upserts pool and returns the fresh entry", func(t *testing.T) {
		poolStore := &fakeK8sPoolStorager{
			fetchFn: func(ctx context.Context, name string) (*K8sPool, error) {
				return &K8sPool{
					Name:       name,
					Instances:  []iprovider.ProviderInstance{{Addr: "10.0.0.1", Port: 8000, Weight: 100}},
					UpdateTime: 1720000000,
				}, nil
			},
		}
		providerStore := &fakeProviderStorager{}
		m := NewK8sPoolManager(&fakeTxn{}, poolStore, providerStore,
			newClusterManagerForTest(nil, &fakePoolStorager{}))

		pool, err := m.ReplaceInstances(ctx, "svc-a", []iprovider.ProviderInstance{
			{Addr: "10.0.0.1", Port: 8000, Weight: 100},
		})
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, "svc-a", pool.Name)
		assert.Len(t, pool.Instances, 1)
		assert.Equal(t, []string{"svc-a"}, poolStore.upserted)
		// Zero referencing providers: no mirror refresh, no cluster sync.
		assert.Empty(t, providerStore.updated)
	})

	t.Run("fans out to all referencing providers and their clusters", func(t *testing.T) {
		poolName := "svc-a"
		newInstances := []iprovider.ProviderInstance{
			{Addr: "10.0.0.1", Port: 8000, Weight: 100},
			{Addr: "10.0.0.2", Port: 8000, Weight: 100},
		}
		providers := []*iprovider.Provider{
			{Name: "p1", InstanceSource: iprovider.InstanceSourceK8sPool, K8sPoolName: &poolName},
			{Name: "p2", InstanceSource: iprovider.InstanceSourceK8sPool, K8sPoolName: &poolName},
		}
		providerStore := &fakeProviderStorager{
			fetchListFn: func(ctx context.Context, filter *iprovider.ProviderFilter) ([]*iprovider.Provider, int64, error) {
				require.NotNil(t, filter.InstanceSource)
				assert.Equal(t, iprovider.InstanceSourceK8sPool, *filter.InstanceSource)
				require.NotNil(t, filter.K8sPoolName)
				assert.Equal(t, poolName, *filter.K8sPoolName)
				return providers, int64(len(providers)), nil
			},
		}
		poolStorager := &fakePoolStorager{}
		clusters := []*icluster_conf.Cluster{
			makeReferencingCluster("p1"),
			makeReferencingCluster("p2"),
		}
		m := NewK8sPoolManager(&fakeTxn{}, &fakeK8sPoolStorager{}, providerStore,
			newClusterManagerForTest(clusters, poolStorager))

		_, err := m.ReplaceInstances(ctx, poolName, newInstances)
		require.NoError(t, err)

		// Both provider mirrors refreshed with the new list.
		require.Len(t, providerStore.updated, 2)
		for _, p := range providers {
			param, ok := providerStore.updated[p.Name]
			require.True(t, ok, "provider %s mirror should be refreshed", p.Name)
			require.NotNil(t, param.K8sInstancePool)
			assert.Equal(t, newInstances, *param.K8sInstancePool)
		}

		// Both referencing clusters had their derived pool synced.
		require.Len(t, poolStorager.updated, 2)
		for _, diff := range poolStorager.updated {
			assert.Equal(t, newInstances, providerInstancesOf(t, diff))
		}
	})

	t.Run("mirror update failure rolls back the whole fan-out", func(t *testing.T) {
		poolName := "svc-a"
		providerStore := &fakeProviderStorager{
			fetchListFn: func(ctx context.Context, filter *iprovider.ProviderFilter) ([]*iprovider.Provider, int64, error) {
				return []*iprovider.Provider{
					{Name: "p1", InstanceSource: iprovider.InstanceSourceK8sPool, K8sPoolName: &poolName},
				}, 1, nil
			},
			updateFn: func(ctx context.Context, name string, param *iprovider.ProviderParam) error {
				return errors.New("mirror update failed")
			},
		}
		m := NewK8sPoolManager(&fakeTxn{}, &fakeK8sPoolStorager{}, providerStore,
			newClusterManagerForTest(nil, &fakePoolStorager{}))

		_, err := m.ReplaceInstances(ctx, poolName, []iprovider.ProviderInstance{
			{Addr: "10.0.0.1", Port: 8000, Weight: 100},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mirror update failed")
	})

	t.Run("cluster sync failure rolls back the whole fan-out", func(t *testing.T) {
		poolName := "svc-a"
		providerStore := &fakeProviderStorager{
			fetchListFn: func(ctx context.Context, filter *iprovider.ProviderFilter) ([]*iprovider.Provider, int64, error) {
				return []*iprovider.Provider{
					{Name: "p1", InstanceSource: iprovider.InstanceSourceK8sPool, K8sPoolName: &poolName},
				}, 1, nil
			},
		}
		poolStorager := &fakePoolStorager{
			updateFn: func(ctx context.Context, oldData *icluster_conf.Pool, diff *icluster_conf.PoolParam) error {
				return errors.New("cluster sync failed")
			},
		}
		m := NewK8sPoolManager(&fakeTxn{}, &fakeK8sPoolStorager{}, providerStore,
			newClusterManagerForTest([]*icluster_conf.Cluster{makeReferencingCluster("p1")}, poolStorager))

		_, err := m.ReplaceInstances(ctx, poolName, []iprovider.ProviderInstance{
			{Addr: "10.0.0.1", Port: 8000, Weight: 100},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cluster sync failed")
	})

	t.Run("pool upsert failure aborts before fan-out", func(t *testing.T) {
		providerStore := &fakeProviderStorager{}
		m := NewK8sPoolManager(&fakeTxn{}, &fakeK8sPoolStorager{
			upsertFn: func(ctx context.Context, name string, instances []iprovider.ProviderInstance) error {
				return errors.New("upsert failed")
			},
		}, providerStore, newClusterManagerForTest(nil, &fakePoolStorager{}))

		_, err := m.ReplaceInstances(ctx, "svc-a", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upsert failed")
		assert.Empty(t, providerStore.updated)
	})
}

func TestK8sPoolManager_DeletePool(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes an unreferenced pool", func(t *testing.T) {
		poolStore := &fakeK8sPoolStorager{
			fetchFn: func(ctx context.Context, name string) (*K8sPool, error) {
				return &K8sPool{Name: name}, nil
			},
		}
		m := NewK8sPoolManager(&fakeTxn{}, poolStore, &fakeProviderStorager{},
			newClusterManagerForTest(nil, &fakePoolStorager{}))

		require.NoError(t, m.DeletePool(ctx, "svc-a"))
		assert.Equal(t, []string{"svc-a"}, poolStore.deleted)
	})

	t.Run("missing pool returns not exist", func(t *testing.T) {
		m := NewK8sPoolManager(&fakeTxn{}, &fakeK8sPoolStorager{}, &fakeProviderStorager{},
			newClusterManagerForTest(nil, &fakePoolStorager{}))
		err := m.DeletePool(ctx, "svc-a")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Not Exist")
	})

	t.Run("referenced pool clears mirrors and derived pools", func(t *testing.T) {
		poolName := "svc-a"
		poolStore := &fakeK8sPoolStorager{
			fetchFn: func(ctx context.Context, name string) (*K8sPool, error) {
				return &K8sPool{Name: name}, nil
			},
		}
		providerStore := &fakeProviderStorager{
			fetchListFn: func(ctx context.Context, filter *iprovider.ProviderFilter) ([]*iprovider.Provider, int64, error) {
				return []*iprovider.Provider{
					{Name: "p1", InstanceSource: iprovider.InstanceSourceK8sPool, K8sPoolName: &poolName},
				}, 1, nil
			},
		}
		poolStorager := &fakePoolStorager{}
		m := NewK8sPoolManager(&fakeTxn{}, poolStore, providerStore,
			newClusterManagerForTest([]*icluster_conf.Cluster{makeReferencingCluster("p1")}, poolStorager))

		require.NoError(t, m.DeletePool(ctx, poolName))

		// The row is deleted and the mirror is reset to an empty list.
		assert.Equal(t, []string{poolName}, poolStore.deleted)
		require.Len(t, providerStore.updated, 1)
		param := providerStore.updated["p1"]
		require.NotNil(t, param.K8sInstancePool)
		assert.Len(t, *param.K8sInstancePool, 0)

		// The derived cluster pool is cleared with an explicit empty list.
		require.Len(t, poolStorager.updated, 1)
		require.NotNil(t, poolStorager.updated[0].Instances)
		assert.Len(t, poolStorager.updated[0].Instances, 0)
	})

	t.Run("mirror failure rolls back", func(t *testing.T) {
		poolName := "svc-a"
		poolStore := &fakeK8sPoolStorager{
			fetchFn: func(ctx context.Context, name string) (*K8sPool, error) {
				return &K8sPool{Name: name}, nil
			},
		}
		providerStore := &fakeProviderStorager{
			fetchListFn: func(ctx context.Context, filter *iprovider.ProviderFilter) ([]*iprovider.Provider, int64, error) {
				return []*iprovider.Provider{
					{Name: "p1", InstanceSource: iprovider.InstanceSourceK8sPool, K8sPoolName: &poolName},
				}, 1, nil
			},
			updateFn: func(ctx context.Context, name string, param *iprovider.ProviderParam) error {
				return errors.New("mirror update failed")
			},
		}
		m := NewK8sPoolManager(&fakeTxn{}, poolStore, providerStore,
			newClusterManagerForTest(nil, &fakePoolStorager{}))

		err := m.DeletePool(ctx, poolName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mirror update failed")
	})
}

func TestK8sPoolManager_Fetch(t *testing.T) {
	ctx := context.Background()

	t.Run("fetch pool passthrough", func(t *testing.T) {
		poolStore := &fakeK8sPoolStorager{
			fetchFn: func(ctx context.Context, name string) (*K8sPool, error) {
				return &K8sPool{Name: name, Instances: []iprovider.ProviderInstance{
					{Addr: "10.0.0.1", Port: 8000, Weight: 100},
				}}, nil
			},
		}
		m := NewK8sPoolManager(&fakeTxn{}, poolStore, &fakeProviderStorager{},
			newClusterManagerForTest(nil, &fakePoolStorager{}))

		pool, err := m.FetchPool(ctx, "svc-a")
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, "svc-a", pool.Name)
		assert.Len(t, pool.Instances, 1)
	})

	t.Run("fetch pool list passthrough", func(t *testing.T) {
		poolStore := &fakeK8sPoolStorager{
			fetchListFn: func(ctx context.Context) ([]*K8sPool, error) {
				return []*K8sPool{{Name: "svc-a"}, {Name: "svc-b"}}, nil
			},
		}
		m := NewK8sPoolManager(&fakeTxn{}, poolStore, &fakeProviderStorager{},
			newClusterManagerForTest(nil, &fakePoolStorager{}))

		pools, err := m.FetchPoolList(ctx)
		require.NoError(t, err)
		require.Len(t, pools, 2)
	})
}

// providerInstancesOf converts the stored cluster-pool instances back to
// provider instances for comparison.
func providerInstancesOf(t *testing.T, diff *icluster_conf.PoolParam) []iprovider.ProviderInstance {
	t.Helper()
	rst := make([]iprovider.ProviderInstance, 0, len(diff.Instances))
	for _, inst := range diff.Instances {
		rst = append(rst, iprovider.ProviderInstance{
			Addr:    inst.Addr,
			Port:    inst.Port,
			Weight:  inst.Weight,
			Disable: inst.Disable,
		})
	}
	return rst
}
