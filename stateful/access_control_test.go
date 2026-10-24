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

package stateful

import (
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/bfenetworks/go-lib/log"
	"github.com/bfenetworks/go-lib/log/log4go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAccessControlTestLogger() func() {
	orig := log.Logger
	log.Logger = log4go.NewDefaultLogger(log4go.DEBUG)
	return func() { log.Logger = orig }
}

func TestCompileAccessControl_SortByPrefixLength(t *testing.T) {
	conf := &AccessControlConf{
		Enable: true,
		Rules: []AccessRuleConf{
			{Name: "mgmt", PathPrefix: "/", Subnets: []string{"192.168.1.0/24"}},
			{Name: "inner", PathPrefix: "/inner-api/v1", Subnets: []string{"10.0.0.0/8"}},
		},
	}
	cc, err := CompileAccessControl(conf)
	require.NoError(t, err)
	require.Len(t, cc.Rules, 2)
	assert.Equal(t, "inner", cc.Rules[0].Rule.Name)
	assert.Equal(t, "mgmt", cc.Rules[1].Rule.Name)
	assert.True(t, cc.Enabled)
}

func TestCompileAccessControl_InvalidCIDR(t *testing.T) {
	// invalid trusted proxy
	_, err := CompileAccessControl(&AccessControlConf{
		Enable:         true,
		TrustedProxies: []string{"not-a-cidr"},
	})
	require.Error(t, err)

	// invalid rule subnet
	_, err = CompileAccessControl(&AccessControlConf{
		Enable: true,
		Rules:  []AccessRuleConf{{Name: "r", PathPrefix: "/", Subnets: []string{"300.0.0.0/8"}}},
	})
	require.Error(t, err)
}

func TestCompileAccessControl_EmptySubnets(t *testing.T) {
	rule := AccessRuleConf{Name: "r", PathPrefix: "/"}

	// enabled: empty subnets rejected (fail-fast)
	_, err := CompileAccessControl(&AccessControlConf{Enable: true, Rules: []AccessRuleConf{rule}})
	require.Error(t, err)

	// disabled: empty subnets tolerated (backward compatible)
	cc, err := CompileAccessControl(&AccessControlConf{Enable: false, Rules: []AccessRuleConf{rule}})
	require.NoError(t, err)
	assert.False(t, cc.Enabled)
}

func TestCompileAccessControl_V4MappedNormalization(t *testing.T) {
	cc, err := CompileAccessControl(&AccessControlConf{
		Enable:         true,
		TrustedProxies: []string{"::ffff:10.244.0.0/112"}, // -> 10.244.0.0/16
		Rules: []AccessRuleConf{
			{Name: "r", PathPrefix: "/", Subnets: []string{"::ffff:192.168.0.0/112"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, cc.TrustNets, 1)
	assert.True(t, cc.TrustNets[0].Addr().Is4())
	clientIP := netip.MustParseAddr("192.168.1.10")
	assert.True(t, cc.Rules[0].Nets[0].Contains(clientIP))
}

func TestValidateAccessControl_CIDRTag(t *testing.T) {
	good := &AccessControlConf{
		Enable:         true,
		TrustedProxies: []string{"10.0.0.0/8"},
		Rules: []AccessRuleConf{
			{Name: "r", PathPrefix: "/", Subnets: []string{"192.168.1.0/24", "2001:db8::/32"}},
		},
	}
	assert.NoError(t, validateAccessControl(good))

	bad := &AccessControlConf{
		Enable: true,
		Rules: []AccessRuleConf{
			{Name: "r", PathPrefix: "/", Subnets: []string{"bad-cidr"}},
		},
	}
	assert.Error(t, validateAccessControl(bad))
}

func TestReloadAccessControl(t *testing.T) {
	cleanup := setupAccessControlTestLogger()
	defer cleanup()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "ai_gateway_api.toml")
	content := `
[Server]
ServerAddr = "0.0.0.0"
ServerPort = 8183
GracefulTimeOutInMs = 5000

[RunTime]
SessionExpireInDay = 1

[Databases.bfe_db]
Driver = "mysql"
MaxOpenConns = 10

[AccessControl]
Enable = true
TrustedProxies = ["10.244.0.0/16"]

[[AccessControl.Rules]]
Name = "inner-api"
PathPrefix = "/inner-api/v1"
Subnets = ["10.10.0.0/16"]
Audit = false
`
	require.NoError(t, os.WriteFile(confPath, []byte(content), 0o600))

	// initial load
	require.NoError(t, LoadConfig(confPath))
	cc := LoadCompiledAccessControl()
	require.NotNil(t, cc)
	assert.True(t, cc.Enabled)
	require.Len(t, cc.Rules, 1)

	// reload with updated subnets takes effect
	updated := `
[AccessControl]
Enable = true
TrustedProxies = ["10.244.0.0/16"]

[[AccessControl.Rules]]
Name = "inner-api"
PathPrefix = "/inner-api/v1"
Subnets = ["172.16.0.0/12"]

[[AccessControl.Rules]]
Name = "mgmt"
PathPrefix = "/"
Subnets = ["192.168.1.0/24"]
`
	require.NoError(t, os.WriteFile(confPath, []byte(updated), 0o600))

	msg, err := ReloadAccessControl(url.Values{})
	require.NoError(t, err)
	assert.Contains(t, msg, "2 rule(s)")

	cc = LoadCompiledAccessControl()
	require.Len(t, cc.Rules, 2)
	assert.Equal(t, "inner-api", cc.Rules[0].Rule.Name)
	assert.Equal(t, "mgmt", cc.Rules[1].Rule.Name)

	// reload with invalid config: error and old config stays effective
	bad := `
[AccessControl]
Enable = true

[[AccessControl.Rules]]
Name = "broken"
PathPrefix = "/"
`
	require.NoError(t, os.WriteFile(confPath, []byte(bad), 0o600))

	_, err = ReloadAccessControl(url.Values{})
	require.Error(t, err)

	cc = LoadCompiledAccessControl()
	require.NotNil(t, cc)
	assert.Len(t, cc.Rules, 2, "old config must stay effective after failed reload")
}
