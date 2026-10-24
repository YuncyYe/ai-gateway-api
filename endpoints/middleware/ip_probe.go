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

package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/bfenetworks/go-lib/log"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// resolveClientIP determines the real client IP. The X-Forwarded-For header
// is only honored when the direct peer is a trusted proxy (LB/Ingress/BFE
// egress); walking XFF from right to left skipping trusted proxies yields
// the closest untrusted IP (same algorithm as BFE mod_trust_clientip).
// An untrusted peer's XFF is ignored entirely, so header forgery is
// ineffective. Returns nil when the peer address cannot be parsed.
func resolveClientIP(r *http.Request, trustNets []netip.Prefix) *netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	remote = remote.Unmap()

	if !prefixContains(trustNets, remote) {
		return &remote
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return &remote
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			continue
		}
		ip = ip.Unmap()
		if !prefixContains(trustNets, ip) {
			return &ip
		}
	}
	return &remote
}

func prefixContains(nets []netip.Prefix, ip netip.Addr) bool {
	for i := range nets {
		if nets[i].Contains(ip) {
			return true
		}
	}
	return false
}

// matchRule picks the rule with the longest matching PathPrefix. Rules are
// pre-sorted at compile time (see stateful.CompileAccessControl).
func matchRule(rules []stateful.CompiledRule, path string) *stateful.CompiledRule {
	for i := range rules {
		if strings.HasPrefix(path, rules[i].Rule.PathPrefix) {
			return &rules[i]
		}
	}
	return nil
}

// IPProbeAction is the management-plane IP whitelist. It runs before auth
// (network layer): requests from non-whitelisted subnets get a 403 without
// ever reaching session/token validation. Audit mode logs and counts but
// lets the request through. Unresolvable client IPs fail open (the whitelist
// must never take the whole management plane down) but stay visible via
// metric and log.
func IPProbeAction(req *http.Request) (*http.Request, error) {
	cc := stateful.LoadCompiledAccessControl()
	if cc == nil || !cc.Enabled {
		return req, nil
	}

	clientIP := resolveClientIP(req, cc.TrustNets)
	if clientIP == nil {
		stateful.MetricMgmtAccessReject.WithLabelValues("unresolvable").Inc()
		log.Logger.Warn("mgmt ip whitelist: unresolvable client ip, remote=%q path=%q",
			req.RemoteAddr, req.URL.Path)
		return req, nil
	}

	rule := matchRule(cc.Rules, req.URL.Path)
	if rule == nil {
		return req, nil
	}
	if prefixContains(rule.Nets, *clientIP) {
		return req, nil
	}

	stateful.MetricMgmtAccessReject.WithLabelValues(rule.Rule.Name).Inc()
	log.Logger.Warn("mgmt ip whitelist: reject rule=%s client=%s remote=%q xff=%q path=%q audit=%v",
		rule.Rule.Name, clientIP.String(), req.RemoteAddr,
		req.Header.Get("X-Forwarded-For"), req.URL.Path, rule.Rule.Audit)

	if rule.Rule.Audit {
		return req, nil
	}
	return nil, xerror.WrapAccessForbiddenErrorWithMsg("access denied by management ip whitelist")
}
