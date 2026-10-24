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

// Package ik8s_pool implements the K8s instance pool domain (k8s-pools.md):
// the pool is a control-plane resource whose member list is written only by
// the K8s discovery component through the /k8s_pools InnerAPI. Providers
// referencing the pool (instance_source=k8s_pool, k8s_pool_name=name) hold a
// read-only mirror in k8s_instance_pool that this manager refreshes
// transactionally together with the derived pools of referencing clusters.
package ik8s_pool

import (
	"context"
	"fmt"
	"strings"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/icluster_conf"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/itxn"
)

// DefaultInstanceWeight is the weight assigned to a discovered instance when
// the discovery component omits it (k8s-pools.md §2).
const DefaultInstanceWeight int64 = 100

// K8sPool is the control-plane representation of one k8s_pools row.
type K8sPool struct {
	Name       string
	Instances  []iprovider.ProviderInstance
	CreateTime int64
	UpdateTime int64
}

// InstanceParam is one element of the PUT /k8s_pools/{name}/instances body.
type InstanceParam struct {
	Addr   *string `json:"addr"`
	Port   *int    `json:"port"`
	Weight *int64  `json:"weight,omitempty"`
}

// ValidateInstances normalizes and validates a full-replacement instance
// snapshot: addr required, port within 1-65535, weight defaulting to 100 and
// within 0-100, and (addr, port) unique within the pool. An empty list is
// valid and means "zero instances".
func ValidateInstances(params []InstanceParam) ([]iprovider.ProviderInstance, error) {
	rst := make([]iprovider.ProviderInstance, 0, len(params))
	comboSet := map[string]struct{}{}
	for i, p := range params {
		if p.Addr == nil || strings.TrimSpace(*p.Addr) == "" {
			return nil, xerror.WrapParamErrorWithMsg("instances[%d]: addr is required", i)
		}
		if p.Port == nil || *p.Port < 1 || *p.Port > 65535 {
			return nil, xerror.WrapParamErrorWithMsg("instances[%d]: port must be between 1 and 65535", i)
		}
		weight := DefaultInstanceWeight
		if p.Weight != nil {
			if *p.Weight < 0 || *p.Weight > 100 {
				return nil, xerror.WrapParamErrorWithMsg("instances[%d]: weight must be between 0 and 100", i)
			}
			weight = *p.Weight
		}
		key := fmt.Sprintf("%s|%d", *p.Addr, *p.Port)
		if _, ok := comboSet[key]; ok {
			return nil, xerror.WrapParamErrorWithMsg("instances: duplicate addr/port combination: %s", key)
		}
		comboSet[key] = struct{}{}
		rst = append(rst, iprovider.ProviderInstance{
			Addr:   *p.Addr,
			Port:   *p.Port,
			Weight: weight,
		})
	}
	return rst, nil
}

// K8sPoolStorager defines persistence operations for k8s_pools.
type K8sPoolStorager interface {
	// UpsertPool creates or replaces the instance list of the named pool.
	UpsertPool(ctx context.Context, name string, instances []iprovider.ProviderInstance) error
	// FetchPool returns the named pool, or (nil, nil) when it does not exist.
	FetchPool(ctx context.Context, name string) (*K8sPool, error)
	// FetchPoolList returns all pools ordered by id.
	FetchPoolList(ctx context.Context) ([]*K8sPool, error)
	// DeletePool deletes the named pool.
	DeletePool(ctx context.Context, name string) error
}

// K8sPoolManager provides business-level operations for k8s_pools.
type K8sPoolManager struct {
	txn              itxn.TxnStorager
	storager         K8sPoolStorager
	providerStorager iprovider.ProviderStorager
	clusterManager   *icluster_conf.ClusterManager
}

// NewK8sPoolManager creates a K8sPoolManager. Dependencies are injected (no
// stateful.Default* access) so tests can mock them; icluster_conf does not
// depend on this package, so the import graph stays acyclic.
func NewK8sPoolManager(txn itxn.TxnStorager, storager K8sPoolStorager,
	providerStorager iprovider.ProviderStorager, clusterManager *icluster_conf.ClusterManager) *K8sPoolManager {

	return &K8sPoolManager{
		txn:              txn,
		storager:         storager,
		providerStorager: providerStorager,
		clusterManager:   clusterManager,
	}
}

// ReplaceInstances replaces the pool's instance list (idempotent upsert; an
// empty list means "zero instances") and fans the new list out, within a
// single transaction, to every provider referencing the pool and to the
// derived pools of clusters referencing those providers. Any failure rolls
// the whole fan-out back.
func (m *K8sPoolManager) ReplaceInstances(ctx context.Context, name string,
	instances []iprovider.ProviderInstance) (*K8sPool, error) {

	if err := m.txn.AtomExecute(ctx, func(ctx context.Context) error {
		if err := m.storager.UpsertPool(ctx, name, instances); err != nil {
			return err
		}
		return m.syncProviderMirrors(ctx, name, instances)
	}); err != nil {
		return nil, err
	}

	return m.storager.FetchPool(ctx, name)
}

// DeletePool deletes the named pool without reference protection: providers
// referencing it get an empty k8s_instance_pool mirror and their referencing
// clusters' derived pools are cleared, all within a single transaction.
// "Pool does not exist" is equivalent to "zero instances" for consumers.
func (m *K8sPoolManager) DeletePool(ctx context.Context, name string) error {
	existing, err := m.storager.FetchPool(ctx, name)
	if err != nil {
		return err
	}
	if existing == nil {
		return xerror.WrapRecordNotExist("k8s pool")
	}

	return m.txn.AtomExecute(ctx, func(ctx context.Context) error {
		if err := m.storager.DeletePool(ctx, name); err != nil {
			return err
		}
		return m.syncProviderMirrors(ctx, name, []iprovider.ProviderInstance{})
	})
}

// FetchPool fetches a single pool by name.
func (m *K8sPoolManager) FetchPool(ctx context.Context, name string) (*K8sPool, error) {
	return m.storager.FetchPool(ctx, name)
}

// FetchPoolList fetches all pools.
func (m *K8sPoolManager) FetchPoolList(ctx context.Context) ([]*K8sPool, error) {
	return m.storager.FetchPoolList(ctx)
}

// syncProviderMirrors refreshes the k8s_instance_pool mirror of every
// provider referencing the named pool and propagates the effective pool to
// the derived pools of clusters referencing those providers (N:1 references
// share the same instance list). Provider mirrors are written through the
// storager directly, bypassing ValidateProviderParam's read-only check on
// k8s_instance_pool.
func (m *K8sPoolManager) syncProviderMirrors(ctx context.Context, name string,
	instances []iprovider.ProviderInstance) error {

	k8sSource := iprovider.InstanceSourceK8sPool
	providers, _, err := m.providerStorager.FetchProviderList(ctx, &iprovider.ProviderFilter{
		InstanceSource: &k8sSource,
		K8sPoolName:    &name,
	})
	if err != nil {
		return err
	}

	for _, p := range providers {
		if err := m.providerStorager.UpdateProvider(ctx, p.Name, &iprovider.ProviderParam{
			K8sInstancePool: &instances,
		}); err != nil {
			return err
		}

		updated := *p
		updated.K8sInstancePool = instances
		if err := m.clusterManager.SyncProviderEffectivePool(ctx, &updated); err != nil {
			return err
		}
	}
	return nil
}
