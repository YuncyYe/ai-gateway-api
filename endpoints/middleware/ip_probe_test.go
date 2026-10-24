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

package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

func newRequest(remoteAddr, xff string) *http.Request {
	req := httptest.NewRequest("GET", "http://mgmt.example.com/open-api/v1/api-keys", nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	return req
}

func mustCompile(t *testing.T, conf *stateful.AccessControlConf) {
	t.Helper()
	cc, err := stateful.CompileAccessControl(conf)
	require.NoError(t, err)
	stateful.StoreCompiledAccessControl(cc)
}

func TestResolveClientIP(t *testing.T) {
	trust := []string{"10.0.0.0/8"}
	trustNets := mustParsePrefixes(t, trust)

	// untrusted peer: XFF ignored entirely (forgery ineffective)
	ip := resolveClientIP(newRequest("203.0.113.9:1234", "1.1.1.1"), trustNets)
	require.NotNil(t, ip)
	assert.Equal(t, "203.0.113.9", ip.String())

	// trusted single-level proxy
	ip = resolveClientIP(newRequest("10.0.0.1:1234", "203.0.113.9"), trustNets)
	require.NotNil(t, ip)
	assert.Equal(t, "203.0.113.9", ip.String())

	// multi-level proxies: right-most untrusted IP wins
	ip = resolveClientIP(newRequest("10.0.0.1:1234", "203.0.113.9, 10.1.1.1"), trustNets)
	require.NotNil(t, ip)
	assert.Equal(t, "203.0.113.9", ip.String())

	// whole chain trusted: fall back to peer address
	ip = resolveClientIP(newRequest("10.0.0.1:1234", "10.1.1.1, 10.2.2.2"), trustNets)
	require.NotNil(t, ip)
	assert.Equal(t, "10.0.0.1", ip.String())

	// unresolvable peer
	assert.Nil(t, resolveClientIP(newRequest("garbage", ""), trustNets))
}

func TestMatchRule_LongestPrefix(t *testing.T) {
	mustCompile(t, &stateful.AccessControlConf{
		Enable: true,
		Rules: []stateful.AccessRuleConf{
			{Name: "mgmt", PathPrefix: "/", Subnets: []string{"192.168.1.0/24"}},
			{Name: "inner", PathPrefix: "/inner-api/v1", Subnets: []string{"10.0.0.0/8"}},
		},
	})
	cc := stateful.LoadCompiledAccessControl()

	rule := matchRule(cc.Rules, "/inner-api/v1/server_data")
	require.NotNil(t, rule)
	assert.Equal(t, "inner", rule.Rule.Name)

	rule = matchRule(cc.Rules, "/login")
	require.NotNil(t, rule)
	assert.Equal(t, "mgmt", rule.Rule.Name)
}

func TestIPProbeAction(t *testing.T) {
	setupTestLoggers(t)

	storeCleanup := func() func() {
		prev := stateful.LoadCompiledAccessControl()
		return func() { stateful.StoreCompiledAccessControl(prev) }
	}

	t.Run("DisabledPassesThrough", func(t *testing.T) {
		defer storeCleanup()()
		stateful.StoreCompiledAccessControl(nil)

		req := newRequest("203.0.113.9:1234", "")
		got, err := IPProbeAction(req)
		require.NoError(t, err)
		assert.Same(t, req, got)
	})

	t.Run("WhitelistedPasses", func(t *testing.T) {
		defer storeCleanup()()
		mustCompile(t, &stateful.AccessControlConf{
			Enable: true,
			Rules:  []stateful.AccessRuleConf{{Name: "mgmt", PathPrefix: "/", Subnets: []string{"192.168.1.0/24"}}},
		})

		got, err := IPProbeAction(newRequest("192.168.1.10:1234", ""))
		require.NoError(t, err)
		assert.NotNil(t, got)
	})

	t.Run("NonWhitelistedRejected403", func(t *testing.T) {
		defer storeCleanup()()
		mustCompile(t, &stateful.AccessControlConf{
			Enable: true,
			Rules:  []stateful.AccessRuleConf{{Name: "mgmt", PathPrefix: "/", Subnets: []string{"192.168.1.0/24"}}},
		})

		_, err := IPProbeAction(newRequest("203.0.113.9:1234", ""))
		require.Error(t, err)
		rr := xerror.Resolve(err)
		require.NotNil(t, rr)
		assert.Equal(t, 403, rr.ErrNo)
		assert.Equal(t, "Access Forbidden", rr.Type)
	})

	t.Run("AuditModePassesButCounts", func(t *testing.T) {
		defer storeCleanup()()
		mustCompile(t, &stateful.AccessControlConf{
			Enable: true,
			Rules:  []stateful.AccessRuleConf{{Name: "mgmt", PathPrefix: "/", Subnets: []string{"192.168.1.0/24"}, Audit: true}},
		})

		got, err := IPProbeAction(newRequest("203.0.113.9:1234", ""))
		require.NoError(t, err)
		assert.NotNil(t, got)
	})

	t.Run("ForgedXffCannotBypass", func(t *testing.T) {
		defer storeCleanup()()
		mustCompile(t, &stateful.AccessControlConf{
			Enable:         true,
			TrustedProxies: []string{"10.0.0.0/8"},
			Rules:          []stateful.AccessRuleConf{{Name: "mgmt", PathPrefix: "/", Subnets: []string{"192.168.1.0/24"}}},
		})

		// untrusted peer forges a whitelisted XFF -> still rejected
		_, err := IPProbeAction(newRequest("203.0.113.9:1234", "192.168.1.10"))
		require.Error(t, err)

		// trusted proxy forwards the real client IP -> rejected
		_, err = IPProbeAction(newRequest("10.0.0.1:1234", "203.0.113.9"))
		require.Error(t, err)

		// trusted proxy forwards a whitelisted client IP -> allowed
		got, err := IPProbeAction(newRequest("10.0.0.1:1234", "192.168.1.10"))
		require.NoError(t, err)
		assert.NotNil(t, got)
	})

	t.Run("UnresolvableFailsOpen", func(t *testing.T) {
		defer storeCleanup()()
		mustCompile(t, &stateful.AccessControlConf{
			Enable: true,
			Rules:  []stateful.AccessRuleConf{{Name: "mgmt", PathPrefix: "/", Subnets: []string{"192.168.1.0/24"}}},
		})

		got, err := IPProbeAction(newRequest("garbage", ""))
		require.NoError(t, err)
		assert.NotNil(t, got)
	})
}

func mustParsePrefixes(t *testing.T, cidrs []string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, s := range cidrs {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}
