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

package epp_pool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func inst(id, group, host string, port int) *InstanceParam {
	return &InstanceParam{ID: id, Host: host, Port: port, GroupName: group}
}

func makePool(groups ...*InstanceGroup) *EppPool {
	return &EppPool{Name: "EPP.pool", Groups: groups}
}

func TestAllocateCluster_Deterministic(t *testing.T) {
	pool := makePool(
		&InstanceGroup{Name: "g1", Instances: []*InstanceParam{inst("epp-a", "g1", "10.0.0.1", 9002)}},
		&InstanceGroup{Name: "g2", Instances: []*InstanceParam{inst("epp-c", "g2", "10.0.0.3", 9002)}},
	)

	first, err := allocateCluster(pool, nil, "cluster-a")
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		got, err := allocateCluster(pool, nil, "cluster-a")
		require.NoError(t, err)
		assert.Equal(t, first, got)
	}
	// Equal load: group name tie-break.
	assert.Equal(t, "g1", first.GroupName)
}

func TestAllocateCluster_GroupLoadBalancing(t *testing.T) {
	pool := makePool(
		&InstanceGroup{Name: "g1", Instances: []*InstanceParam{inst("epp-a", "g1", "10.0.0.1", 9002)}},
		&InstanceGroup{Name: "g2", Instances: []*InstanceParam{inst("epp-c", "g2", "10.0.0.3", 9002)}},
	)

	// g1 already carries two assignments -> the next cluster goes to g2.
	assignments := []*AssignmentParam{
		{Cluster: "cluster-x", GroupName: "g1"},
		{Cluster: "cluster-y", GroupName: "g1"},
	}
	got, err := allocateCluster(pool, assignments, "cluster-z")
	require.NoError(t, err)
	assert.Equal(t, "g2", got.GroupName)

	// Equal total load: group name tie-break.
	assignments = []*AssignmentParam{
		{Cluster: "cluster-x", GroupName: "g2"},
	}
	got, err = allocateCluster(pool, assignments, "cluster-z")
	require.NoError(t, err)
	// g1 load = 0, g2 load = 1 -> g1.
	assert.Equal(t, "g1", got.GroupName)
}

func TestAllocateCluster_NoCandidates(t *testing.T) {
	_, err := allocateCluster(makePool(), nil, "cluster-a")
	require.Error(t, err)
}

func TestRepairOne(t *testing.T) {
	pool := makePool(
		&InstanceGroup{Name: "g1", Instances: []*InstanceParam{inst("epp-a", "g1", "10.0.0.1", 9002)}},
		&InstanceGroup{Name: "g2", Instances: []*InstanceParam{inst("epp-c", "g2", "10.0.0.3", 9002)}},
	)

	t.Run("valid assignment kept", func(t *testing.T) {
		assignment := &AssignmentParam{Cluster: "c1", GroupName: "g1"}
		_, action := repairOne(pool, []*AssignmentParam{assignment}, assignment)
		assert.Equal(t, repairKeep, action)
	})

	t.Run("cross group reallocate when group gone", func(t *testing.T) {
		assignment := &AssignmentParam{Cluster: "c1", GroupName: "g9"}
		repaired, action := repairOne(pool, []*AssignmentParam{assignment}, assignment)
		require.Equal(t, repairReallocate, action)
		assert.Equal(t, "g1", repaired.GroupName)
	})

	t.Run("clear when no candidate groups", func(t *testing.T) {
		assignment := &AssignmentParam{Cluster: "c1", GroupName: "g9"}
		_, action := repairOne(makePool(), []*AssignmentParam{assignment}, assignment)
		assert.Equal(t, repairClear, action)
	})
}

func TestBuildAssignmentsView(t *testing.T) {
	pool := makePool(
		&InstanceGroup{Name: "g1", Instances: []*InstanceParam{inst("epp-a", "g1", "10.0.0.1", 9002)}},
		&InstanceGroup{Name: "g2", Instances: []*InstanceParam{inst("epp-c", "g2", "10.0.0.3", 9002)}},
		&InstanceGroup{Name: "g3", Instances: []*InstanceParam{inst("epp-e", "g3", "10.0.0.5", 9002)}},
	)

	eppClusters := []*EPPClusterInfo{
		{Name: "cluster-a"},
		{Name: "cluster-b"},
		{Name: "cluster-c"},
		{Name: "cluster-d"},
	}
	assignments := []*AssignmentParam{
		{Cluster: "cluster-a", GroupName: "g1"},
		{Cluster: "cluster-b", GroupName: "g1"},
		{Cluster: "cluster-c", GroupName: "g9"},
	}

	view := buildAssignmentsView(pool, eppClusters, assignments)
	// Only validly assigned clusters appear in the view.
	require.Len(t, view.Clusters, 2)

	assert.Equal(t, "cluster-a", view.Clusters[0].Cluster)
	assert.Equal(t, "g1", *view.Clusters[0].Group)
	assert.Equal(t, "cluster-b", view.Clusters[1].Cluster)
	assert.Equal(t, "g1", *view.Clusters[1].Group)

	// Dangling assignment (group gone) and missing assignment: unassigned.
	assert.Equal(t, []string{"cluster-c", "cluster-d"}, view.UnassignedClusters)
	// g2 and g3 carry no assignment.
	assert.Equal(t, []string{"g2", "g3"}, view.IdleGroups)
}

func TestBuildAssignmentsView_SingleInstanceGroup(t *testing.T) {
	pool := makePool(
		&InstanceGroup{Name: "g1", Instances: []*InstanceParam{inst("epp-a", "g1", "10.0.0.1", 9002)}},
	)
	eppClusters := []*EPPClusterInfo{{Name: "cluster-a"}}
	assignments := []*AssignmentParam{{Cluster: "cluster-a", GroupName: "g1"}}

	view := buildAssignmentsView(pool, eppClusters, assignments)
	require.Len(t, view.Clusters, 1)
	assert.Equal(t, "g1", *view.Clusters[0].Group)
	assert.Empty(t, view.UnassignedClusters)
}

func TestEppPoolManager_GetAssignmentEndpoints(t *testing.T) {
	ctx := context.Background()

	newManager := func(store *memoryEppPoolStorager) *EppPoolManager {
		return NewEppPoolManager(&fakeTxn{}, store, nil, nil, &ManagerOptions{})
	}

	t.Run("group instance address joined by net.JoinHostPort", func(t *testing.T) {
		store := newMemoryEppPoolStorager()
		store.seedInstances(
			&InstanceParam{ID: "epp-a", Host: "2001:db8::10", Port: 9002, GroupName: "g1"},
		)
		store.seedAssignments(&AssignmentParam{Cluster: "c1", GroupName: "g1"})

		endpoints, found, err := newManager(store).GetAssignmentEndpoints(ctx, "c1")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, []string{"[2001:db8::10]:9002"}, endpoints)
	})

	t.Run("no assignment record", func(t *testing.T) {
		store := newMemoryEppPoolStorager()
		store.seedInstances(
			&InstanceParam{ID: "epp-a", Host: "10.0.0.1", Port: 9002, GroupName: "g1"},
		)

		_, found, err := newManager(store).GetAssignmentEndpoints(ctx, "c1")
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("dangling assignment", func(t *testing.T) {
		store := newMemoryEppPoolStorager()
		store.seedInstances(
			&InstanceParam{ID: "epp-a", Host: "10.0.0.1", Port: 9002, GroupName: "g1"},
		)
		store.seedAssignments(&AssignmentParam{Cluster: "c1", GroupName: "g1-removed"})

		_, found, err := newManager(store).GetAssignmentEndpoints(ctx, "c1")
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("empty cluster name", func(t *testing.T) {
		store := newMemoryEppPoolStorager()
		_, _, err := newManager(store).GetAssignmentEndpoints(ctx, "")
		require.Error(t, err)
	})
}
