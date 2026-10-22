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

package entity

import (
	"strings"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/entity"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
	"github.com/stretchr/testify/assert"
)

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

func TestValidateEntityParam(t *testing.T) {
	validName := "entity_1"
	validType := "dept_1"
	invalidNameWithSpace := " entity_1"
	invalidType := "Dep"
	duplicateRuleName := "r1"
	cond := "default_t()"
	cluster := "cluster_1"
	weight := 100

	cases := []struct {
		name            string
		param           *entity.EntityParam
		requireNameType bool
		wantErr         bool
	}{
		{
			name: "create valid",
			param: &entity.EntityParam{
				Name:       &validName,
				Type:       &validType,
				RouteRules: validRouteRules(),
			},
			requireNameType: true,
			wantErr:         false,
		},
		{
			name:            "create missing name",
			param:           &entity.EntityParam{Type: &validType},
			requireNameType: true,
			wantErr:         true,
		},
		{
			name:            "create missing type",
			param:           &entity.EntityParam{Name: &validName},
			requireNameType: true,
			wantErr:         true,
		},
		{
			name: "create invalid name leading space",
			param: &entity.EntityParam{
				Name:       &invalidNameWithSpace,
				Type:       &validType,
				RouteRules: validRouteRules(),
			},
			requireNameType: true,
			wantErr:         true,
		},
		{
			name: "create invalid type",
			param: &entity.EntityParam{
				Name:       &validName,
				Type:       &invalidType,
				RouteRules: validRouteRules(),
			},
			requireNameType: true,
			wantErr:         true,
		},
		{
			name: "create duplicate route rule names",
			param: &entity.EntityParam{
				Name: &validName,
				Type: &validType,
				RouteRules: &shared.RouteRulesParam{
					Rules: []*shared.AiRouteRuleParam{
						{Name: &duplicateRuleName, Cond: &cond, Targets: []*shared.AiRouteTargetParam{{ClusterName: &cluster, Weight: &weight}}},
						{Name: &duplicateRuleName, Cond: &cond, Targets: []*shared.AiRouteTargetParam{{ClusterName: &cluster, Weight: &weight}}},
					},
				},
			},
			requireNameType: true,
			wantErr:         true,
		},
		{
			name:            "update empty body valid",
			param:           &entity.EntityParam{},
			requireNameType: false,
			wantErr:         false,
		},
		{
			name: "update invalid name",
			param: &entity.EntityParam{
				Name: &invalidNameWithSpace,
			},
			requireNameType: false,
			wantErr:         true,
		},
		{
			name: "update invalid type",
			param: &entity.EntityParam{
				Type: &invalidType,
			},
			requireNameType: false,
			wantErr:         true,
		},
		{
			name: "update invalid quota plan unit",
			param: &entity.EntityParam{
				QuotaPlan: &shared.QuotaPlanParam{
					Quota: lib.PFloat64(100),
					Unit:  lib.PString("invalid"),
				},
			},
			requireNameType: false,
			wantErr:         true,
		},
		{
			name: "update invalid rate limit window",
			param: &entity.EntityParam{
				RateLimitPolicy: &shared.RateLimitPolicyParam{
					Enabled: lib.PBool(true),
					Rules: &shared.RateLimitRules{
						TpmConfigs: []shared.TPMConfig{
							{Name: "t1", Model: "*", WindowMinutes: 0, MaxTokens: 100, StepMinutes: 1},
						},
					},
				},
			},
			requireNameType: false,
			wantErr:         true,
		},
		{
			name: "create valid batch limits",
			param: &entity.EntityParam{
				Name: &validName,
				Type: &validType,
				RateLimitPolicy: &shared.RateLimitPolicyParam{
					Enabled: lib.PBool(true),
					Rules: &shared.RateLimitRules{
						BatchLimits: &shared.BatchLimits{
							MaxCreateRPM:     60,
							MaxActiveBatches: 10,
							MaxFileBytes:     104857600,
							MaxFileLines:     100000,
						},
					},
				},
			},
			requireNameType: true,
			wantErr:         false,
		},
		{
			name: "create enabled with only batch limits",
			param: &entity.EntityParam{
				Name: &validName,
				Type: &validType,
				RateLimitPolicy: &shared.RateLimitPolicyParam{
					Enabled: lib.PBool(true),
					Rules: &shared.RateLimitRules{
						BatchLimits: &shared.BatchLimits{MaxActiveBatches: 5},
					},
				},
			},
			requireNameType: true,
			wantErr:         false,
		},
		{
			name: "create invalid batch limits negative value",
			param: &entity.EntityParam{
				Name: &validName,
				Type: &validType,
				RateLimitPolicy: &shared.RateLimitPolicyParam{
					Enabled: lib.PBool(true),
					Rules: &shared.RateLimitRules{
						BatchLimits: &shared.BatchLimits{MaxCreateRPM: -1},
					},
				},
			},
			requireNameType: true,
			wantErr:         true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEntityParam(tc.param, tc.requireNameType)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateTypeImmutable(t *testing.T) {
	childType := "dept_child"
	rootType := "dept_root"
	emptyType := ""

	cases := []struct {
		name         string
		paramType    *string
		existingType *string
		wantErr      bool
	}{
		{name: "nil param type allowed", paramType: nil, existingType: &childType, wantErr: false},
		{name: "nil existing allowed", paramType: &childType, existingType: nil, wantErr: false},
		{name: "both nil allowed", paramType: nil, existingType: nil, wantErr: false},
		{name: "same type allowed", paramType: &childType, existingType: &childType, wantErr: false},
		{name: "different type rejected", paramType: &rootType, existingType: &childType, wantErr: true},
		{name: "set type on empty stored rejected", paramType: &rootType, existingType: &emptyType, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTypeImmutable(tc.paramType, tc.existingType)
			if tc.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "type is immutable")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEntityParamDescription(t *testing.T) {
	desc255 := strings.Repeat("a", 255)
	desc256 := strings.Repeat("a", 256)
	descWithNull := "abc\x00def"
	descWithTab := "abc\tdef"
	empty := ""
	normal := "运营部，负责线上业务"

	cases := []struct {
		name    string
		desc    *string
		wantErr bool
	}{
		{name: "nil omitted allowed", desc: nil, wantErr: false},
		{name: "empty string allowed", desc: &empty, wantErr: false},
		{name: "normal text allowed", desc: &normal, wantErr: false},
		{name: "boundary 255 allowed", desc: &desc255, wantErr: false},
		{name: "over 255 rejected", desc: &desc256, wantErr: true},
		{name: "null control char rejected", desc: &descWithNull, wantErr: true},
		{name: "tab control char rejected", desc: &descWithTab, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			param := &entity.EntityParam{Description: tc.desc}
			err := validateEntityParam(param, false)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
