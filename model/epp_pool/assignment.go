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
	"sort"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
)

// AssignCluster assigns an EPP instance group to the cluster. It is idempotent:
// if a valid assignment already exists it is a no-op; otherwise the greedy
// deterministic allocator (design-changes.md §4.2.2) picks a group and
// upserts the assignment. It is called when a cluster is created
// with balance_mode=EPP or switched from WRR to EPP.
func (m *EppPoolManager) AssignCluster(ctx context.Context, clusterName string) error {
	if clusterName == "" {
		return xerror.WrapParamErrorWithMsg("cluster name is empty")
	}

	instances, err := m.storager.FetchInstanceList(ctx, &InstanceFilter{})
	if err != nil {
		return err
	}
	pool := buildPool(m.poolName, instances)

	assignments, err := m.storager.FetchAssignmentList(ctx)
	if err != nil {
		return err
	}

	if findValidAssignment(pool, assignments, clusterName) != nil {
		return nil
	}

	return m.allocateAndUpsert(ctx, pool, assignments, clusterName)
}

// GetAssignmentEndpoints returns the endpoint addresses of the cluster's
// assigned group (a single address in the current single-instance stage),
// joined by net.JoinHostPort. found is false when the cluster has no valid
// assignment (no record or the assigned group is gone/empty); storager
// errors are propagated. It is the read path behind the EPPAddr export
// (design-changes.md §4.3).
func (m *EppPoolManager) GetAssignmentEndpoints(ctx context.Context, clusterName string) ([]string, bool, error) {
	if clusterName == "" {
		return nil, false, xerror.WrapParamErrorWithMsg("cluster name is empty")
	}

	instances, err := m.storager.FetchInstanceList(ctx, &InstanceFilter{})
	if err != nil {
		return nil, false, err
	}
	pool := buildPool(m.poolName, instances)

	assignments, err := m.storager.FetchAssignmentList(ctx)
	if err != nil {
		return nil, false, err
	}

	assignment := findValidAssignment(pool, assignments, clusterName)
	if assignment == nil {
		return nil, false, nil
	}

	group := findGroup(pool, assignment.GroupName)
	if group == nil || len(group.Instances) == 0 {
		return nil, false, nil
	}

	endpoints := make([]string, 0, len(group.Instances))
	for _, inst := range group.Instances {
		endpoints = append(endpoints, instanceAddress(inst.Host, inst.Port))
	}

	return endpoints, true, nil
}

// allocateAndUpsert runs the greedy allocator and upserts its result with a
// unique-key conflict fallback: on conflict the latest assignments are
// re-read and the allocation is retried once (design-changes.md §4.2.2).
func (m *EppPoolManager) allocateAndUpsert(ctx context.Context, pool *EppPool, assignments []*AssignmentParam, clusterName string) error {
	assignment, err := allocateCluster(pool, assignments, clusterName)
	if err != nil {
		// No assignable candidate group: clear the assignment (unassigned state).
		if errDel := m.storager.DeleteAssignment(ctx, clusterName); errDel != nil {
			return errDel
		}
		return nil
	}

	err = m.txn.AtomExecute(ctx, func(ctx context.Context) error {
		return m.storager.UpsertAssignment(ctx, assignment)
	})
	if err == nil {
		return nil
	}

	// Unique-key conflict fallback: re-read and retry once.
	assignments, err = m.storager.FetchAssignmentList(ctx)
	if err != nil {
		return err
	}
	assignment, err = allocateCluster(pool, assignments, clusterName)
	if err != nil {
		if errDel := m.storager.DeleteAssignment(ctx, clusterName); errDel != nil {
			return errDel
		}
		return nil
	}

	return m.txn.AtomExecute(ctx, func(ctx context.Context) error {
		return m.storager.UpsertAssignment(ctx, assignment)
	})
}

// RepairDangling repairs assignments that became dangling after an
// /epp-pool change (design-changes.md §4.2.1):
//   - assigned group gone or empty -> cross-group reallocation (greedy);
//   - no candidate group left -> clear the assignment (unassigned state).
func (m *EppPoolManager) RepairDangling(ctx context.Context) error {
	instances, err := m.storager.FetchInstanceList(ctx, &InstanceFilter{})
	if err != nil {
		return err
	}
	pool := buildPool(m.poolName, instances)

	assignments, err := m.storager.FetchAssignmentList(ctx)
	if err != nil {
		return err
	}

	// Deterministic repair order.
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].Cluster < assignments[j].Cluster })

	changed := false
	for _, assignment := range assignments {
		repaired, action := repairOne(pool, assignments, assignment)
		switch action {
		case repairKeep:
			continue
		case repairReallocate:
			if err := m.txn.AtomExecute(ctx, func(ctx context.Context) error {
				return m.storager.UpsertAssignment(ctx, repaired)
			}); err != nil {
				return err
			}
			changed = true
		case repairClear:
			if err := m.txn.AtomExecute(ctx, func(ctx context.Context) error {
				return m.storager.DeleteAssignment(ctx, assignment.Cluster)
			}); err != nil {
				return err
			}
			changed = true
		}
	}

	if changed {
		m.triggerEppDataExport(ctx)
	}

	return nil
}

// Reconcile idempotently scans all EPP clusters and ensures every one has a
// valid assignment: dangling repairs first, then greedy allocation for
// clusters without any assignment. It is the reconciler body and safe to run
// repeatedly (most passes perform zero writes).
func (m *EppPoolManager) Reconcile(ctx context.Context) error {
	if m.clusterSource == nil {
		return nil
	}

	if err := m.RepairDangling(ctx); err != nil {
		return err
	}

	eppClusters, err := m.clusterSource.FetchEPPClusters(ctx)
	if err != nil {
		return err
	}
	if len(eppClusters) == 0 {
		return nil
	}

	instances, err := m.storager.FetchInstanceList(ctx, &InstanceFilter{})
	if err != nil {
		return err
	}
	pool := buildPool(m.poolName, instances)

	assignments, err := m.storager.FetchAssignmentList(ctx)
	if err != nil {
		return err
	}

	sort.Slice(eppClusters, func(i, j int) bool { return eppClusters[i].Name < eppClusters[j].Name })

	for _, cluster := range eppClusters {
		if findValidAssignment(pool, assignments, cluster.Name) != nil {
			continue
		}
		if err := m.allocateAndUpsert(ctx, pool, assignments, cluster.Name); err != nil {
			return err
		}
		// Re-read so the next allocation sees this new assignment.
		assignments, err = m.storager.FetchAssignmentList(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// OverrideAssignment manually overrides the assignment of one cluster
// (operations fallback). The cluster must exist and be in EPP mode, the
// group must exist in the pool and be non-empty. The epp_data export is
// triggered after a successful upsert.
func (m *EppPoolManager) OverrideAssignment(ctx context.Context, clusterName, groupName string) (*ClusterAssignmentView, error) {
	if clusterName == "" {
		return nil, xerror.WrapParamErrorWithMsg("cluster name is empty")
	}
	if groupName == "" {
		return nil, xerror.WrapParamErrorWithMsg("group_name is empty")
	}

	if m.clusterSource == nil {
		return nil, xerror.WrapModelErrorWithMsg("epp pool: cluster source is not configured")
	}
	eppClusters, err := m.clusterSource.FetchEPPClusters(ctx)
	if err != nil {
		return nil, err
	}
	clusterFound := false
	for _, cluster := range eppClusters {
		if cluster.Name == clusterName {
			clusterFound = true
			break
		}
	}
	if !clusterFound {
		return nil, xerror.WrapRecordNotExist("epp assignment", clusterName)
	}

	instances, err := m.storager.FetchInstanceList(ctx, &InstanceFilter{GroupName: &groupName})
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, xerror.WrapParamErrorWithMsg("epp pool group %q does not exist or is empty", groupName)
	}

	err = m.txn.AtomExecute(ctx, func(ctx context.Context) error {
		return m.storager.UpsertAssignment(ctx, &AssignmentParam{
			Cluster:   clusterName,
			GroupName: groupName,
		})
	})
	if err != nil {
		return nil, err
	}

	m.triggerEppDataExport(ctx)

	return m.GetAssignmentView(ctx, clusterName)
}

// ClusterAssignmentView is the per-cluster assignment view (api-changes.md
// §3.3.1): the cluster name and its assigned group. Only validly assigned
// clusters appear in the view.
type ClusterAssignmentView struct {
	Cluster string  `json:"cluster"`
	Group   *string `json:"group"`
}

// AssignmentsView is the full assignment view: valid per-cluster assignments
// plus the unassigned cluster list and the idle group list.
type AssignmentsView struct {
	Clusters           []*ClusterAssignmentView `json:"clusters"`
	UnassignedClusters []string                 `json:"unassigned_clusters"`
	IdleGroups         []string                 `json:"idle_groups"`
}

// GetAssignmentsView builds the full assignment view (read-time join of
// epp_assignments and epp_instances). Only balance_mode=EPP clusters are in
// scope; an assignment is valid when its group exists in the pool and is
// non-empty, otherwise the cluster is listed in UnassignedClusters (the
// periodic reconciler repairs dangling assignments).
func (m *EppPoolManager) GetAssignmentsView(ctx context.Context) (*AssignmentsView, error) {
	if m.clusterSource == nil {
		return nil, xerror.WrapModelErrorWithMsg("epp pool: cluster source is not configured")
	}

	eppClusters, err := m.clusterSource.FetchEPPClusters(ctx)
	if err != nil {
		return nil, err
	}

	instances, err := m.storager.FetchInstanceList(ctx, &InstanceFilter{})
	if err != nil {
		return nil, err
	}
	pool := buildPool(m.poolName, instances)

	assignments, err := m.storager.FetchAssignmentList(ctx)
	if err != nil {
		return nil, err
	}

	return buildAssignmentsView(pool, eppClusters, assignments), nil
}

// GetAssignmentView builds the assignment view of one cluster. The cluster
// must be in EPP mode; nil is returned when the cluster has no assignment.
func (m *EppPoolManager) GetAssignmentView(ctx context.Context, clusterName string) (*ClusterAssignmentView, error) {
	view, err := m.GetAssignmentsView(ctx)
	if err != nil {
		return nil, err
	}

	for _, entry := range view.Clusters {
		if entry.Cluster == clusterName {
			return entry, nil
		}
	}

	return nil, xerror.WrapRecordNotExist("epp assignment", clusterName)
}

// buildAssignmentsView is the pure view builder shared by manager methods and tests.
func buildAssignmentsView(pool *EppPool, eppClusters []*EPPClusterInfo, assignments []*AssignmentParam) *AssignmentsView {
	groupMap := map[string]*InstanceGroup{}
	for _, group := range pool.Groups {
		groupMap[group.Name] = group
	}

	assignmentMap := map[string]*AssignmentParam{}
	for _, assignment := range assignments {
		assignmentMap[assignment.Cluster] = assignment
	}

	view := &AssignmentsView{
		Clusters:           []*ClusterAssignmentView{},
		UnassignedClusters: []string{},
		IdleGroups:         []string{},
	}

	occupiedGroups := map[string]bool{}

	sort.Slice(eppClusters, func(i, j int) bool { return eppClusters[i].Name < eppClusters[j].Name })
	for _, cluster := range eppClusters {
		assignment := assignmentMap[cluster.Name]
		if assignment != nil {
			if group := groupMap[assignment.GroupName]; group != nil && len(group.Instances) > 0 {
				groupName := assignment.GroupName
				view.Clusters = append(view.Clusters, &ClusterAssignmentView{
					Cluster: cluster.Name,
					Group:   &groupName,
				})
				occupiedGroups[groupName] = true
				continue
			}
		}

		// Unassigned or dangling assignment (group removed/empty).
		view.UnassignedClusters = append(view.UnassignedClusters, cluster.Name)
	}

	for _, group := range pool.Groups {
		if !occupiedGroups[group.Name] {
			view.IdleGroups = append(view.IdleGroups, group.Name)
		}
	}
	sort.Strings(view.IdleGroups)

	return view
}

// findValidAssignment returns the assignment of the cluster if it exists and
// its assigned group is still present and non-empty in the pool.
func findValidAssignment(pool *EppPool, assignments []*AssignmentParam, clusterName string) *AssignmentParam {
	groupMap := map[string]*InstanceGroup{}
	for _, group := range pool.Groups {
		groupMap[group.Name] = group
	}

	for _, assignment := range assignments {
		if assignment.Cluster != clusterName {
			continue
		}
		group := groupMap[assignment.GroupName]
		if group == nil || len(group.Instances) == 0 {
			return nil
		}
		return assignment
	}

	return nil
}

type repairAction int

const (
	repairKeep repairAction = iota
	repairReallocate
	repairClear
)

// repairOne decides the repair action for one dangling-or-valid assignment.
// A valid assignment maps to repairKeep; a dangling one is cross-group
// reallocated, or cleared when no candidate group remains.
func repairOne(pool *EppPool, assignments []*AssignmentParam, assignment *AssignmentParam) (*AssignmentParam, repairAction) {
	if findValidAssignment(pool, assignments, assignment.Cluster) != nil {
		return assignment, repairKeep
	}

	reallocated, err := allocateCluster(pool, assignments, assignment.Cluster)
	if err != nil {
		return nil, repairClear
	}
	return reallocated, repairReallocate
}

// allocateCluster implements the greedy deterministic allocator
// (design-changes.md §4.2.2). The allocation granularity is the group: it
// selects the group with the smallest assignment load, tie-break by group
// name.
func allocateCluster(pool *EppPool, assignments []*AssignmentParam, clusterName string) (*AssignmentParam, error) {
	candidates := candidateGroups(pool)
	if len(candidates) == 0 {
		return nil, xerror.WrapModelErrorWithMsg("epp pool has no assignable instance group")
	}

	loads := countGroupAssignments(assignments)

	// Select the group with the smallest load; tie-break by group name.
	selected := candidates[0]
	selectedLoad := loads[selected.Name]
	for _, group := range candidates[1:] {
		load := loads[group.Name]
		if load < selectedLoad || (load == selectedLoad && group.Name < selected.Name) {
			selected = group
			selectedLoad = load
		}
	}

	return &AssignmentParam{
		Cluster:   clusterName,
		GroupName: selected.Name,
	}, nil
}

// candidateGroups returns all pool groups ordered by group name for
// determinism. Every group in the pool is a valid candidate: PATCH
// validation rejects empty groups, so every group holds exactly 1 instance.
func candidateGroups(pool *EppPool) []*InstanceGroup {
	candidates := append([]*InstanceGroup(nil), pool.Groups...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates
}

// countGroupAssignments counts, per group name, in how many assignments the
// group is the assignment target.
func countGroupAssignments(assignments []*AssignmentParam) map[string]int {
	counts := map[string]int{}
	for _, assignment := range assignments {
		counts[assignment.GroupName]++
	}
	return counts
}

func findGroup(pool *EppPool, groupName string) *InstanceGroup {
	for _, group := range pool.Groups {
		if group.Name == groupName {
			return group
		}
	}
	return nil
}
