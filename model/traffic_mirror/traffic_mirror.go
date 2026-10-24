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
	"context"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

// Default applied by the control plane when the optional percentage field is
// omitted. It mirrors the traffic_mirror_rules table column default (see
// db_ddl.sql): 100 means full mirroring of matched traffic.
const (
	DefaultTrafficMirrorPercentage = 100
)

// DefaultSensitiveHeaders is the header blacklist the config export fills in
// when a rule leaves remove_headers unconfigured (NULL column). The mirror
// copy replays the full conversation and must never carry auth/session
// credentials to the shadow target.
var DefaultSensitiveHeaders = []string{"Authorization", "Cookie", "X-Api-Key"}

// TrafficMirrorBodyRewriteParam is the storage-level body field rewrite.
// Phase 1 restricts Path to "model" (aligned with BFE mod_traffic_mirror Check).
type TrafficMirrorBodyRewriteParam struct {
	Path  *string
	Value *string
}

// TrafficMirrorRuleParam is the storage-level traffic mirror rule. The internal
// ID is the ordering/priority key and is never exposed through the Open API.
// RemoveHeaders/SetHeaders/BodyRewrites/PathRewrite keep the nil = NULL column
// semantics (nil remove_headers is a distinct state from an explicit empty
// array); only Percentage is defaulted on write.
type TrafficMirrorRuleParam struct {
	ID            *int64
	Name          *string
	Cond          *string
	MirrorCluster *string
	Percentage    *int
	RemoveHeaders *[]string
	SetHeaders    map[string]string
	BodyRewrites  []*TrafficMirrorBodyRewriteParam
	PathRewrite   *string
	CreatedAt     *time.Time
	UpdatedAt     *time.Time
}

// TrafficMirrorStorager defines storage operations for the traffic mirror rule
// collection. ReplaceAll is executed inside a transaction provided by
// itxn.TxnStorager.
type TrafficMirrorStorager interface {
	// FetchAll returns all rules ordered by id ascending.
	FetchAll(ctx context.Context) ([]*TrafficMirrorRuleParam, error)
	// ReplaceAll atomically replaces the whole rule set with the given rules
	// (delete-all + insert-all, in slice order).
	ReplaceAll(ctx context.Context, rules []*TrafficMirrorRuleParam) error
}

// trafficMirrorRuleParamFromShared converts an API-level rule into the
// storage-level rule, filling the documented default for an omitted
// percentage. The remaining optional fields keep their nil (NULL) state so
// the remove_headers default-blacklist semantics survive the round trip.
func trafficMirrorRuleParamFromShared(param *shared.TrafficMirrorRuleParam) *TrafficMirrorRuleParam {
	if param == nil {
		return nil
	}

	rule := &TrafficMirrorRuleParam{
		Name:          param.Name,
		Cond:          param.Cond,
		MirrorCluster: param.MirrorCluster,
		Percentage:    param.Percentage,
		RemoveHeaders: param.RemoveHeaders,
		SetHeaders:    param.SetHeaders,
		PathRewrite:   param.PathRewrite,
		CreatedAt:     param.CreatedAt,
		UpdatedAt:     param.UpdatedAt,
	}

	if rule.Percentage == nil {
		rule.Percentage = lib.PInt(DefaultTrafficMirrorPercentage)
	}

	if len(param.BodyRewrites) > 0 {
		rule.BodyRewrites = make([]*TrafficMirrorBodyRewriteParam, 0, len(param.BodyRewrites))
		for _, rw := range param.BodyRewrites {
			if rw == nil {
				continue
			}
			rule.BodyRewrites = append(rule.BodyRewrites, &TrafficMirrorBodyRewriteParam{
				Path:  rw.Path,
				Value: rw.Value,
			})
		}
	}

	return rule
}

// trafficMirrorRuleParamToShared converts a storage-level rule into the
// API-level rule.
func trafficMirrorRuleParamToShared(rule *TrafficMirrorRuleParam) *shared.TrafficMirrorRuleParam {
	if rule == nil {
		return nil
	}

	param := &shared.TrafficMirrorRuleParam{
		Name:          rule.Name,
		Cond:          rule.Cond,
		MirrorCluster: rule.MirrorCluster,
		Percentage:    rule.Percentage,
		RemoveHeaders: rule.RemoveHeaders,
		SetHeaders:    rule.SetHeaders,
		PathRewrite:   rule.PathRewrite,
		CreatedAt:     rule.CreatedAt,
		UpdatedAt:     rule.UpdatedAt,
	}

	if len(rule.BodyRewrites) > 0 {
		param.BodyRewrites = make([]*shared.TrafficMirrorBodyRewriteParam, 0, len(rule.BodyRewrites))
		for _, rw := range rule.BodyRewrites {
			if rw == nil {
				continue
			}
			param.BodyRewrites = append(param.BodyRewrites, &shared.TrafficMirrorBodyRewriteParam{
				Path:  rw.Path,
				Value: rw.Value,
			})
		}
	}

	return param
}
