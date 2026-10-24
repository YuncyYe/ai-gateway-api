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

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibasic"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/icluster_conf"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
)

type fakeTxn struct{}

func (f *fakeTxn) AtomExecute(ctx context.Context, do func(context.Context) error) error {
	return do(ctx)
}

type fakeK8sPoolStorager struct {
	upsertFn    func(ctx context.Context, name string, instances []iprovider.ProviderInstance) error
	fetchFn     func(ctx context.Context, name string) (*K8sPool, error)
	fetchListFn func(ctx context.Context) ([]*K8sPool, error)
	deleteFn    func(ctx context.Context, name string) error
	upserted    []string
	deleted     []string
}

func (s *fakeK8sPoolStorager) UpsertPool(ctx context.Context, name string, instances []iprovider.ProviderInstance) error {
	s.upserted = append(s.upserted, name)
	if s.upsertFn != nil {
		return s.upsertFn(ctx, name, instances)
	}
	return nil
}

func (s *fakeK8sPoolStorager) FetchPool(ctx context.Context, name string) (*K8sPool, error) {
	if s.fetchFn != nil {
		return s.fetchFn(ctx, name)
	}
	return nil, nil
}

func (s *fakeK8sPoolStorager) FetchPoolList(ctx context.Context) ([]*K8sPool, error) {
	if s.fetchListFn != nil {
		return s.fetchListFn(ctx)
	}
	return nil, nil
}

func (s *fakeK8sPoolStorager) DeletePool(ctx context.Context, name string) error {
	s.deleted = append(s.deleted, name)
	if s.deleteFn != nil {
		return s.deleteFn(ctx, name)
	}
	return nil
}

type fakeProviderStorager struct {
	updateFn    func(ctx context.Context, name string, param *iprovider.ProviderParam) error
	fetchListFn func(ctx context.Context, filter *iprovider.ProviderFilter) ([]*iprovider.Provider, int64, error)
	updated     map[string]*iprovider.ProviderParam
}

func (s *fakeProviderStorager) CreateProvider(ctx context.Context, param *iprovider.ProviderParam) (int64, error) {
	return 1, nil
}

func (s *fakeProviderStorager) UpdateProvider(ctx context.Context, name string, param *iprovider.ProviderParam) error {
	if s.updated == nil {
		s.updated = map[string]*iprovider.ProviderParam{}
	}
	s.updated[name] = param
	if s.updateFn != nil {
		return s.updateFn(ctx, name, param)
	}
	return nil
}

func (s *fakeProviderStorager) DeleteProvider(ctx context.Context, name string) error {
	return nil
}

func (s *fakeProviderStorager) FetchProvider(ctx context.Context, filter *iprovider.ProviderFilter) (*iprovider.Provider, error) {
	return nil, nil
}

func (s *fakeProviderStorager) FetchProviderList(ctx context.Context, filter *iprovider.ProviderFilter) ([]*iprovider.Provider, int64, error) {
	if s.fetchListFn != nil {
		return s.fetchListFn(ctx, filter)
	}
	return nil, 0, nil
}

func (s *fakeProviderStorager) FetchProviderNames(ctx context.Context) ([]string, error) {
	return nil, nil
}

// fakeClusterStorager implements icluster_conf.ClusterStorager; only
// FetchClusterList is exercised by the sync fan-out.
type fakeClusterStorager struct {
	fetchClusterListFn func(ctx context.Context, param *icluster_conf.ClusterFilter) ([]*icluster_conf.Cluster, error)
}

func (f *fakeClusterStorager) FetchCluster(ctx context.Context, param *icluster_conf.ClusterFilter) (*icluster_conf.Cluster, error) {
	return nil, nil
}

func (f *fakeClusterStorager) FetchClusterList(ctx context.Context, param *icluster_conf.ClusterFilter) ([]*icluster_conf.Cluster, error) {
	if f.fetchClusterListFn != nil {
		return f.fetchClusterListFn(ctx, param)
	}
	return nil, nil
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

// fakePoolStorager implements icluster_conf.PoolStorager; UpdatePool captures
// the instances propagated to derived cluster pools.
type fakePoolStorager struct {
	updateFn func(ctx context.Context, oldData *icluster_conf.Pool, diff *icluster_conf.PoolParam) error
	updated  []*icluster_conf.PoolParam
}

func (f *fakePoolStorager) FetchPool(ctx context.Context, name string) (*icluster_conf.Pool, error) {
	return nil, nil
}

func (f *fakePoolStorager) FetchPools(ctx context.Context, param *icluster_conf.PoolFilter) ([]*icluster_conf.Pool, error) {
	return nil, nil
}

func (f *fakePoolStorager) CreatePool(ctx context.Context, product *ibasic.Product, data *icluster_conf.PoolParam) (*icluster_conf.Pool, error) {
	return &icluster_conf.Pool{}, nil
}

func (f *fakePoolStorager) UpdatePool(ctx context.Context, oldData *icluster_conf.Pool, diff *icluster_conf.PoolParam) error {
	f.updated = append(f.updated, diff)
	if f.updateFn != nil {
		return f.updateFn(ctx, oldData, diff)
	}
	return nil
}

func (f *fakePoolStorager) DeletePool(ctx context.Context, pool *icluster_conf.Pool) error {
	return nil
}

// newClusterForTest builds a ClusterManager whose only behavior is the
// effective-pool fan-out over the given clusters.
func newClusterManagerForTest(clusters []*icluster_conf.Cluster, poolStorager *fakePoolStorager) *icluster_conf.ClusterManager {
	clusterStorager := &fakeClusterStorager{
		fetchClusterListFn: func(ctx context.Context, param *icluster_conf.ClusterFilter) ([]*icluster_conf.Cluster, error) {
			return clusters, nil
		},
	}
	return icluster_conf.NewClusterManager(&fakeTxn{}, clusterStorager, nil, nil, poolStorager, nil, nil, nil, nil)
}
