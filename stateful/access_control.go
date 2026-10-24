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
//limitations under the License. All rights reserved.

// Copyright (c) 2021 The BFE Authors.
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

package stateful

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"sync/atomic"

	"github.com/bfenetworks/go-lib/log"
	"github.com/go-playground/validator/v10"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
)

// AccessRuleConf is one management-plane IP whitelist rule of the
// [AccessControl] section in ai_gateway_api.toml. Rules are matched by
// longest PathPrefix; a request that hits no rule is allowed (backward
// compatibility).
type AccessRuleConf struct {
	Name       string   `validate:"required"`
	PathPrefix string   `validate:"required,startswith=/"`
	Subnets    []string `validate:"dive,cidr"` // IPv4/IPv6 CIDR, /32 and /128 allowed
	Audit      bool     // observe-only: log and count but do not reject
}

// AccessControlConf is the [AccessControl] section: management-plane IP
// whitelist. It lives in the server config file (not DB / OpenAPI) on
// purpose: the whitelist protects the control-plane API itself, so it must
// stay editable even when the API is unreachable (see design-docs
// modifications/2026-10-04-mgmt-ip-whitelist).
type AccessControlConf struct {
	Enable         bool
	TrustedProxies []string         `validate:"dive,cidr"` // LB/Ingress/BFE egress CIDRs
	Rules          []AccessRuleConf `validate:"dive"`
}

// CompiledRule is the runtime form of AccessRuleConf.
type CompiledRule struct {
	Rule AccessRuleConf
	Nets []netip.Prefix
}

// CompiledAccessControl is the runtime, pre-parsed form of the whitelist.
// It is the single source of truth at runtime and is swapped atomically by
// the /reload/access_control handler.
type CompiledAccessControl struct {
	Enabled   bool
	TrustNets []netip.Prefix
	Rules     []CompiledRule // sorted by PathPrefix length descending
}

var (
	confFilePath string
	compiledAC   atomic.Value // stores *CompiledAccessControl
)

// LoadCompiledAccessControl returns the current compiled whitelist; nil when
// not configured or not enabled.
func LoadCompiledAccessControl() *CompiledAccessControl {
	if v := compiledAC.Load(); v != nil {
		return v.(*CompiledAccessControl)
	}
	return nil
}

// StoreCompiledAccessControl atomically replaces the runtime whitelist.
func StoreCompiledAccessControl(cc *CompiledAccessControl) {
	compiledAC.Store(cc)
}

// ValidateCIDR is the custom "cidr" validator tag: valid IPv4/IPv6 prefix.
func ValidateCIDR(fl validator.FieldLevel) bool {
	s, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}
	_, err := netip.ParsePrefix(s)
	return err == nil
}

// newConfigValidator builds the validator used for the server config,
// including the custom "cidr" tag.
func newConfigValidator() *validator.Validate {
	v := validator.New()
	v.RegisterValidation("cidr", ValidateCIDR)
	return v
}

// parseCIDRPrefix parses a CIDR and normalizes IPv4-mapped IPv6 addresses
// (e.g. ::ffff:192.168.0.1/120 -> 192.168.0.1/24) so dual-stack clients are
// judged consistently.
func parseCIDRPrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96), nil
	}
	return p, nil
}

// CompileAccessControl parses and pre-sorts the whitelist. Startup and the
// hot-reload handler share this function so both paths enforce identical
// semantics: with Enable=true any rule with empty Subnets is rejected
// (fail-fast), and unspecified addresses (0.0.0.0/0, ::/0) are accepted but
// warned about.
func CompileAccessControl(conf *AccessControlConf) (*CompiledAccessControl, error) {
	cc := &CompiledAccessControl{Enabled: conf.Enable}

	for _, s := range conf.TrustedProxies {
		p, err := parseCIDRPrefix(s)
		if err != nil {
			return nil, fmt.Errorf("AccessControl.TrustedProxies: invalid cidr %q: %v", s, err)
		}
		cc.TrustNets = append(cc.TrustNets, p)
	}

	rules := make([]CompiledRule, 0, len(conf.Rules))
	for i := range conf.Rules {
		rc := &conf.Rules[i]
		if conf.Enable && len(rc.Subnets) == 0 {
			return nil, fmt.Errorf("AccessControl.Rules[%s]: Subnets must not be empty when access control enabled", rc.Name)
		}
		cr := CompiledRule{Rule: *rc}
		for _, s := range rc.Subnets {
			p, err := parseCIDRPrefix(s)
			if err != nil {
				return nil, fmt.Errorf("AccessControl.Rules[%s]: invalid cidr %q: %v", rc.Name, s, err)
			}
			if p.Addr().IsUnspecified() {
				log.Logger.Warn("AccessControl.Rules[%s]: subnet %s is unspecified (whitelist not effective for it)", rc.Name, s)
			}
			cr.Nets = append(cr.Nets, p)
		}
		rules = append(rules, cr)
	}

	// Longest PathPrefix wins; stable sort keeps config order on ties.
	sort.SliceStable(rules, func(i, j int) bool {
		return len(rules[i].Rule.PathPrefix) > len(rules[j].Rule.PathPrefix)
	})
	cc.Rules = rules

	return cc, nil
}

// validateAccessControl runs the same validation as startup on a standalone
// [AccessControl] section (used by the hot-reload path).
func validateAccessControl(conf *AccessControlConf) error {
	return newConfigValidator().Struct(conf)
}

// ReloadAccessControl re-reads the [AccessControl] section from the server
// config file, validates and compiles it, then swaps it in atomically. Any
// failure keeps the previous configuration effective.
func ReloadAccessControl(query url.Values) (string, error) {
	if confFilePath == "" {
		return "", errors.New("conf file path not recorded, cannot reload")
	}

	var partial struct {
		AccessControl AccessControlConf
	}
	if err := lib.LoadConfAuto(confFilePath, &partial); err != nil {
		return "", fmt.Errorf("reload access_control: read conf: %v", err)
	}
	if err := validateAccessControl(&partial.AccessControl); err != nil {
		return "", fmt.Errorf("reload access_control: validate: %v", err)
	}

	newCC, err := CompileAccessControl(&partial.AccessControl)
	if err != nil {
		return "", fmt.Errorf("reload access_control: compile: %v", err)
	}

	oldCC := LoadCompiledAccessControl()
	StoreCompiledAccessControl(newCC)

	oldRules, newRules := -1, len(newCC.Rules)
	if oldCC != nil {
		oldRules = len(oldCC.Rules)
	}
	log.Logger.Info("access_control config reloaded: enabled=%v, rules %d -> %d",
		newCC.Enabled, oldRules, newRules)

	return fmt.Sprintf("access_control reloaded: enabled=%v, %d rule(s)", newCC.Enabled, newRules), nil
}
