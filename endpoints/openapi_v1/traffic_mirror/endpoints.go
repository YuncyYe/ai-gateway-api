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

package traffic_mirror

import (
	"net/http"
	"strings"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/validate"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/icluster_conf"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

var Endpoints = []*xreq.Endpoint{
	TrafficMirrorRulesGetRoute,
	TrafficMirrorRulesUpdateRoute,
}

var TrafficMirrorRulesGetRoute = &xreq.Endpoint{
	Path:       "/traffic-mirror-rules",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(TrafficMirrorRulesGetAction),
	Authorizer: iauth.FA(iauth.FeatureTrafficMirror, iauth.ActionRead),
}

var TrafficMirrorRulesUpdateRoute = &xreq.Endpoint{
	Path:       "/traffic-mirror-rules",
	Method:     http.MethodPut,
	Handler:    xreq.Convert(TrafficMirrorRulesUpdateAction),
	Authorizer: iauth.FA(iauth.FeatureTrafficMirror, iauth.ActionUpdate),
}

// TrafficMirrorRulesGetAction returns the whole rule set in priority order
// (first-match-wins; the internal id is not exposed).
func TrafficMirrorRulesGetAction(req *http.Request) (interface{}, error) {
	rules, err := container.TrafficMirrorManager.GetTrafficMirrorRules(req.Context())
	if err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []*shared.TrafficMirrorRuleParam{}
	}

	return &shared.TrafficMirrorRulesParam{Rules: rules}, nil
}

// TrafficMirrorRulesUpdateAction full-replaces the rule set in a single
// transaction and returns the rebuilt collection (same shape as GET). The
// mirror_cluster references are validated for existence against the cluster
// manager before the rebuild (HTTP 422 when a referenced cluster is missing).
func TrafficMirrorRulesUpdateAction(req *http.Request) (interface{}, error) {
	param := &shared.TrafficMirrorRulesParam{}
	if err := xreq.BindJSON(req, param); err != nil {
		return nil, err
	}

	if err := validate.TrafficMirrorRules(param); err != nil {
		// Record the rejected write for audit. Identity and the before
		// snapshot are independent of the request body (issue #155).
		container.TrafficMirrorManager.RecordSetTrafficMirrorRulesFailure(req.Context(), param, err)
		return nil, err
	}

	if err := checkMirrorClustersExist(req, param); err != nil {
		container.TrafficMirrorManager.RecordSetTrafficMirrorRulesFailure(req.Context(), param, err)
		return nil, err
	}

	if param.Rules == nil {
		param.Rules = []*shared.TrafficMirrorRuleParam{}
	}

	updated, err := container.TrafficMirrorManager.SetTrafficMirrorRules(req.Context(), param)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		updated = &shared.TrafficMirrorRulesParam{Rules: []*shared.TrafficMirrorRuleParam{}}
	}
	if updated.Rules == nil {
		updated.Rules = []*shared.TrafficMirrorRuleParam{}
	}

	return updated, nil
}

// checkMirrorClustersExist batch-fetches the referenced mirror clusters and
// rejects the request when any name does not exist (HTTP 422). Existence is
// checked here at the endpoint layer, not in lib/validate, per the
// "checked separately by the endpoint" precedent.
func checkMirrorClustersExist(req *http.Request, param *shared.TrafficMirrorRulesParam) error {
	clusterNames := make([]string, 0, len(param.Rules))
	seen := map[string]struct{}{}
	for _, rule := range param.Rules {
		if rule == nil || rule.MirrorCluster == nil {
			continue
		}
		if _, ok := seen[*rule.MirrorCluster]; ok {
			continue
		}
		seen[*rule.MirrorCluster] = struct{}{}
		clusterNames = append(clusterNames, *rule.MirrorCluster)
	}
	if len(clusterNames) == 0 {
		return nil
	}

	clusters, err := container.ClusterManager.FetchClusterList(req.Context(), &icluster_conf.ClusterFilter{
		Names: clusterNames,
	})
	if err != nil {
		return err
	}

	clusterMap := icluster_conf.ClusterList2MapByName(clusters)
	missing := make([]string, 0)
	for _, name := range clusterNames {
		if _, ok := clusterMap[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return xerror.WrapParamErrorWithMsg("traffic mirror rule mirror_cluster not found: %s", strings.Join(missing, ", "))
	}

	return nil
}
