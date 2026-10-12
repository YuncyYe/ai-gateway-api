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

package icluster_conf

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
)

func (cm *ClusterManager) recordClusterOperation(ctx context.Context, action string, clusterID int64, clusterName string, before, after map[string]interface{}, err error) {
	if cm.operationLogManager == nil {
		return
	}

	status := ioperlog.StatusSuccess
	errorMsg := ""
	if err != nil {
		status = ioperlog.StatusFailed
		errorMsg = ioperlog.TruncateErrorMessageDefault(err)
	}

	entry := &ioperlog.OperationLogEntry{
		Action:       action,
		ResourceType: string(ioperlog.ResourceTypeCluster),
		ResourceID:   strconv.FormatInt(clusterID, 10),
		ResourceName: clusterName,
		Status:       status,
		ErrorMsg:     errorMsg,
		CreatedAt:    time.Now(),
	}

	entry.ChangeSummary = ioperlog.BuildChangeSummary(action, before, after)

	cm.operationLogManager.Record(ctx, entry)
}

// clusterHashStrategyI2S mirrors the API string vocabulary of
// sticky_sessions.hash_strategy (endpoints/openapi_v1/product_cluster).
// The model layer cannot import endpoints (dependency direction), so the
// audit snapshot keeps its own copy; the literals must stay in sync with
// hashStrategyConvert (create.go) and clusterModel2Control (one.go).
var clusterHashStrategyI2S = map[int32]string{
	ClusterHashStrategyClientIDOnlyI:     "CLIENT_ID_ONLY",
	ClusterHashStrategyClientIPOnlyI:     "CLIENT_IP_ONLY",
	ClusterHashStrategyClientIDPreferedI: "CLIENT_ID_PREFERED",
}

// clusterParamToMap builds the audit after-snapshot for cluster writes.
// Keys and value representations follow the cluster Open API vocabulary
// (GET /open-api/v1/clusters) so that change_summary can be consumed by API
// field name: hash_strategy is the string enum, epp_config is the decoded
// JSON object, and internal bookkeeping fields (ID/ProductID/SubClusters/
// Scheduler/InstancePool) are trimmed (issue #205). Fields omitted from a
// partial update are nil pointers and are dropped, preserving the issue #201
// phantom-diff semantics; explicit zero values are retained.
func clusterParamToMap(param *ClusterParam) map[string]interface{} {
	if param == nil {
		return nil
	}

	m := map[string]interface{}{}
	if param.Name != nil {
		m["name"] = *param.Name
	}
	if param.Description != nil {
		m["description"] = *param.Description
	}
	if v := clusterBasicParamToSnapshot(param.Basic); v != nil {
		m["basic"] = v
	}
	if v := clusterStickySessionsParamToSnapshot(param.StickySessions); v != nil {
		m["sticky_sessions"] = v
	}
	if v := clusterPassiveHealthCheckParamToSnapshot(param.PassiveHealthCheck); v != nil {
		m["passive_health_check"] = v
	}
	if v := ioperlog.ParamToMap(param.LLMConfig); v != nil {
		m["llm_config"] = v
	}
	if param.BalanceMode != nil {
		m["balance_mode"] = *param.BalanceMode
	}
	if param.EppConfig != nil {
		if v := eppConfigSnapshotValue(*param.EppConfig); v != nil {
			m["epp_config"] = v
		}
	}
	if param.EppRuntime != nil {
		if v := eppRuntimeSnapshotValue(*param.EppRuntime); v != nil {
			m["epp_runtime"] = v
		}
	}
	return m
}

// clusterToMap builds the audit before/delete snapshot in the same API
// vocabulary as clusterParamToMap. balance_mode is normalized like the API
// read path: an empty storage value reports the WRR column default.
func clusterToMap(cluster *Cluster) map[string]interface{} {
	if cluster == nil {
		return nil
	}

	m := map[string]interface{}{
		"name":         cluster.Name,
		"description":  cluster.Description,
		"balance_mode": cluster.getBalanceMode(),
	}
	if v := clusterBasicToSnapshot(cluster.Basic); v != nil {
		m["basic"] = v
	}
	if v := clusterStickySessionsToSnapshot(cluster.StickySessions); v != nil {
		m["sticky_sessions"] = v
	}
	if v := clusterPassiveHealthCheckToSnapshot(cluster.PassiveHealthCheck); v != nil {
		m["passive_health_check"] = v
	}
	if v := ioperlog.ParamToMap(cluster.LLMConfig); v != nil {
		m["llm_config"] = v
	}
	if v := eppConfigSnapshotValue(cluster.EppConfig); v != nil {
		m["epp_config"] = v
	}
	if v := eppRuntimeSnapshotValue(cluster.EppRuntime); v != nil {
		m["epp_runtime"] = v
	}
	return m
}

// clusterBasicParamToSnapshot maps ClusterBasicParam to the API "basic"
// shape. Only fields exposed by the API are kept: retries carries
// max_retry_in_cluster only (MaxRetryCrossSubcluster is internal), buffers
// carries req_write_buffer_size only (flush intervals are internal).
// Sub-objects that end up empty (nothing submitted) are omitted so that
// no-op partial submissions do not surface as phantom diffs.
func clusterBasicParamToSnapshot(p *ClusterBasicParam) map[string]interface{} {
	if p == nil {
		return nil
	}

	m := map[string]interface{}{}
	if p.Connection != nil {
		c := map[string]interface{}{}
		if p.Connection.MaxIdleConnPerRs != nil {
			c["max_idle_conn_per_rs"] = *p.Connection.MaxIdleConnPerRs
		}
		if p.Connection.CancelOnClientClose != nil {
			c["cancel_on_client_close"] = *p.Connection.CancelOnClientClose
		}
		if len(c) > 0 {
			m["connection"] = c
		}
	}
	if p.Retries != nil {
		r := map[string]interface{}{}
		if p.Retries.MaxRetryInSubcluster != nil {
			r["max_retry_in_cluster"] = *p.Retries.MaxRetryInSubcluster
		}
		if len(r) > 0 {
			m["retries"] = r
		}
	}
	if p.Buffers != nil {
		b := map[string]interface{}{}
		if p.Buffers.ReqWriteBufferSize != nil {
			b["req_write_buffer_size"] = *p.Buffers.ReqWriteBufferSize
		}
		if len(b) > 0 {
			m["buffers"] = b
		}
	}
	if p.Timeouts != nil {
		to := map[string]interface{}{}
		if p.Timeouts.TimeoutConnServ != nil {
			to["timeout_conn_serv"] = *p.Timeouts.TimeoutConnServ
		}
		if p.Timeouts.TimeoutResponseHeader != nil {
			to["timeout_response_header"] = *p.Timeouts.TimeoutResponseHeader
		}
		if p.Timeouts.TimeoutReadbodyClient != nil {
			to["timeout_readbody_client"] = *p.Timeouts.TimeoutReadbodyClient
		}
		if p.Timeouts.TimeoutReadClientAgain != nil {
			to["timeout_read_client_again"] = *p.Timeouts.TimeoutReadClientAgain
		}
		if p.Timeouts.TimeoutWriteClient != nil {
			to["timeout_write_client"] = *p.Timeouts.TimeoutWriteClient
		}
		if len(to) > 0 {
			m["timeouts"] = to
		}
	}
	if p.Protocol != nil {
		m["protocol"] = *p.Protocol
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func clusterBasicToSnapshot(b *ClusterBasic) map[string]interface{} {
	if b == nil {
		return nil
	}

	m := map[string]interface{}{}
	if b.Connection != nil {
		m["connection"] = map[string]interface{}{
			"max_idle_conn_per_rs":    b.Connection.MaxIdleConnPerRs,
			"cancel_on_client_close": b.Connection.CancelOnClientClose,
		}
	}
	if b.Retries != nil {
		m["retries"] = map[string]interface{}{
			"max_retry_in_cluster": b.Retries.MaxRetryInSubcluster,
		}
	}
	if b.Buffers != nil {
		m["buffers"] = map[string]interface{}{
			"req_write_buffer_size": b.Buffers.ReqWriteBufferSize,
		}
	}
	if b.Timeouts != nil {
		m["timeouts"] = map[string]interface{}{
			"timeout_conn_serv":         b.Timeouts.TimeoutConnServ,
			"timeout_response_header":   b.Timeouts.TimeoutResponseHeader,
			"timeout_readbody_client":   b.Timeouts.TimeoutReadbodyClient,
			"timeout_read_client_again": b.Timeouts.TimeoutReadClientAgain,
			"timeout_write_client":      b.Timeouts.TimeoutWriteClient,
		}
	}
	if b.Protocol != nil {
		m["protocol"] = *b.Protocol
	}
	return m
}

func clusterStickySessionsParamToSnapshot(p *ClusterStickySessionsParam) map[string]interface{} {
	if p == nil {
		return nil
	}

	m := map[string]interface{}{}
	if p.SessionSticky != nil {
		m["enabled"] = *p.SessionSticky
	}
	if p.HashStrategy != nil {
		m["hash_strategy"] = clusterHashStrategyI2S[*p.HashStrategy]
	}
	if p.HashHeader != nil {
		m["hash_header"] = *p.HashHeader
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func clusterStickySessionsToSnapshot(s *ClusterStickySessions) map[string]interface{} {
	if s == nil {
		return nil
	}

	return map[string]interface{}{
		"enabled":       s.SessionSticky,
		"hash_strategy": clusterHashStrategyI2S[s.HashStrategy],
		"hash_header":   s.HashHeader,
	}
}

// clusterPassiveHealthCheckParamToSnapshot maps the passive health check
// param to the API shape. Schema is internal probe bookkeeping and is
// trimmed: the API PassiveHealthCheck carries interval/failnum/statuscode/
// host/uri only.
func clusterPassiveHealthCheckParamToSnapshot(p *ClusterPassiveHealthCheckParam) map[string]interface{} {
	if p == nil {
		return nil
	}

	m := map[string]interface{}{}
	if p.Interval != nil {
		m["interval"] = *p.Interval
	}
	if p.Failnum != nil {
		m["failnum"] = *p.Failnum
	}
	if p.Statuscode != nil {
		m["statuscode"] = *p.Statuscode
	}
	if p.Host != nil {
		m["host"] = *p.Host
	}
	if p.Uri != nil {
		m["uri"] = *p.Uri
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func clusterPassiveHealthCheckToSnapshot(p *ClusterPassiveHealthCheck) map[string]interface{} {
	if p == nil {
		return nil
	}

	return map[string]interface{}{
		"interval":   p.Interval,
		"failnum":    p.Failnum,
		"statuscode": p.Statuscode,
		"host":       p.Host,
		"uri":        p.Uri,
	}
}

// eppConfigSnapshotValue decodes the stored raw epp_config JSON into a plain
// JSON value so the snapshot matches the API representation (GET /clusters
// echoes epp_config as an object). Unset configs are omitted from the
// snapshot. Snapshot values must stay plain JSON types: MaskSensitiveFields
// recurses map[string]interface{}/[]interface{} only, and embedding Go
// structs or json.RawMessage would escape the masking pass.
func eppConfigSnapshotValue(raw string) interface{} {
	if raw == "" {
		return nil
	}

	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

// eppRuntimeSnapshotValue decodes the stored raw epp_runtime JSON into a plain
// JSON value so the snapshot matches the API representation (GET /clusters
// echoes epp_runtime as an object). Unset configs are omitted. Same masking
// constraint as eppConfigSnapshotValue: values must stay plain JSON types.
func eppRuntimeSnapshotValue(raw string) interface{} {
	if raw == "" {
		return nil
	}

	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}
