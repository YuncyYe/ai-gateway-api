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

package api_key

import (
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/stretchr/testify/assert"
)

func validAPIKeyParam() *api_key.APIKeyParam {
	desc := "test api key"
	key := "ak-test_123"
	neverExpire := int64(-1)
	return &api_key.APIKeyParam{
		Description: &desc,
		Key:         &key,
		ExpiredTime: &neverExpire,
	}
}

func validRouteRules() *shared.RouteRulesParam {
	name := "r1"
	cond := "default_t()"
	cluster := "cluster_1"
	weight := 100
	return &shared.RouteRulesParam{
		Rules: []*shared.AiRouteRuleParam{
			{
				Name: &name,
				Cond: &cond,
				Targets: []*shared.AiRouteTargetParam{
					{ClusterName: &cluster, Weight: &weight},
				},
			},
		},
	}
}

func TestCheckCreateAPIKey(t *testing.T) {
	longDesc := make([]byte, 513)
	invalidKey := "ak@123"
	invalidCIDR := "not-a-cidr"
	past := time.Now().Unix() - 1000
	invalidUnit := "invalid"

	cases := []struct {
		name    string
		param   *api_key.APIKeyParam
		wantErr bool
	}{
		{"valid", validAPIKeyParam(), false},
		{"missing description", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Description = nil
			return p
		}(), true},
		{"empty description", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Description = lib.PString("")
			return p
		}(), true},
		{"description too long", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Description = lib.PString(string(longDesc))
			return p
		}(), true},
		{"missing key", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Key = nil
			return p
		}(), true},
		{"invalid key", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Key = &invalidKey
			return p
		}(), true},
		{"invalid subnet", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Subnet = []string{invalidCIDR}
			return p
		}(), true},
		{"expired time in past", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.ExpiredTime = &past
			return p
		}(), true},
		{"invalid quota plan unit", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.QuotaPlan = &shared.QuotaPlanParam{Quota: lib.PFloat64(100), Unit: &invalidUnit}
			return p
		}(), true},
		{"valid batch limits", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.RateLimitPolicy = &shared.RateLimitPolicyParam{
				Enabled: lib.PBool(true),
				Rules: &shared.RateLimitRules{
					BatchLimits: &shared.BatchLimits{
						MaxCreateRPM:     60,
						MaxActiveBatches: 10,
						MaxFileBytes:     104857600,
						MaxFileLines:     100000,
					},
				},
			}
			return p
		}(), false},
		{"enabled with only batch limits", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.RateLimitPolicy = &shared.RateLimitPolicyParam{
				Enabled: lib.PBool(true),
				Rules: &shared.RateLimitRules{
					BatchLimits: &shared.BatchLimits{MaxActiveBatches: 5},
				},
			}
			return p
		}(), false},
		{"invalid batch limits negative value", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.RateLimitPolicy = &shared.RateLimitPolicyParam{
				Enabled: lib.PBool(true),
				Rules: &shared.RateLimitRules{
					BatchLimits: &shared.BatchLimits{MaxCreateRPM: -1},
				},
			}
			return p
		}(), true},
		{"invalid route rules", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.RouteRules = &shared.RouteRulesParam{Rules: []*shared.AiRouteRuleParam{}}
			return p
		}(), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCreateAPIKey(tc.param, "product_1")
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckUpdateAPIKey(t *testing.T) {
	longDesc := make([]byte, 513)
	invalidKey := "ak@123"
	invalidCIDR := "not-a-cidr"
	invalidUnit := "invalid"
	zeroWindow := 0

	cases := []struct {
		name    string
		param   *api_key.APIKeyParam
		wantErr bool
	}{
		{"empty update valid", &api_key.APIKeyParam{}, false},
		{"valid description", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Key = nil
			return p
		}(), false},
		{"description too long", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.Description = lib.PString(string(longDesc))
			return p
		}(), true},
		{"invalid subnet", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.Subnet = []string{invalidCIDR}
			return p
		}(), true},
		{"invalid key", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.Key = &invalidKey
			return p
		}(), true},
		{"invalid quota plan unit", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.QuotaPlan = &shared.QuotaPlanParam{Quota: lib.PFloat64(100), Unit: &invalidUnit}
			return p
		}(), true},
		{"valid batch limits", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.RateLimitPolicy = &shared.RateLimitPolicyParam{
				Enabled: lib.PBool(true),
				Rules: &shared.RateLimitRules{
					BatchLimits: &shared.BatchLimits{
						MaxCreateRPM:     60,
						MaxActiveBatches: 10,
						MaxFileBytes:     104857600,
						MaxFileLines:     100000,
					},
				},
			}
			return p
		}(), false},
		{"enabled with only batch limits", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.RateLimitPolicy = &shared.RateLimitPolicyParam{
				Enabled: lib.PBool(true),
				Rules: &shared.RateLimitRules{
					BatchLimits: &shared.BatchLimits{MaxActiveBatches: 5},
				},
			}
			return p
		}(), false},
		{"invalid batch limits negative value", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.RateLimitPolicy = &shared.RateLimitPolicyParam{
				Enabled: lib.PBool(true),
				Rules: &shared.RateLimitRules{
					BatchLimits: &shared.BatchLimits{MaxCreateRPM: -1},
				},
			}
			return p
		}(), true},
		{"invalid rate limit window", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.RateLimitPolicy = &shared.RateLimitPolicyParam{
				Enabled: lib.PBool(true),
				Rules: &shared.RateLimitRules{
					TpmConfigs: []shared.TPMConfig{
						{Name: "t1", Model: "*", WindowMinutes: zeroWindow, MaxTokens: 100, StepMinutes: 1},
					},
				},
			}
			return p
		}(), true},
		{"duplicate route rule names", func() *api_key.APIKeyParam {
			p := &api_key.APIKeyParam{}
			p.RouteRules = validRouteRules()
			name := "r1"
			cond := "default_t()"
			cluster := "cluster_1"
			weight := 100
			p.RouteRules.Rules = append(p.RouteRules.Rules, &shared.AiRouteRuleParam{
				Name:    &name,
				Cond:    &cond,
				Targets: []*shared.AiRouteTargetParam{{ClusterName: &cluster, Weight: &weight}},
			})
			return p
		}(), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkUpdateAPIKey(tc.param, "product_1")
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckFullUpdateAPIKey(t *testing.T) {
	cases := []struct {
		name    string
		param   *api_key.APIKeyParam
		wantErr bool
	}{
		{"valid", validAPIKeyParam(), false},
		{"missing description", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Description = nil
			return p
		}(), true},
		{"empty description", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.Description = lib.PString("")
			return p
		}(), true},
		{"invalid quota plan", func() *api_key.APIKeyParam {
			p := validAPIKeyParam()
			p.QuotaPlan = &shared.QuotaPlanParam{Quota: lib.PFloat64(100), Unit: lib.PString("invalid")}
			return p
		}(), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFullUpdateAPIKey(tc.param, "product_1")
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
