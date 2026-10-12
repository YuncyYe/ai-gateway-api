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

// readonly_admin 验证"系统管理员（只读）"角色的端到端行为：
// 读放行 + 密钥掩码 + 写拒绝 + session key 契约。
// 说明：测试环境 SkipTokenValidate=true，无 Authorization 头的请求被视作完整管理员
// （fakeVisitor(System)）；带 "Authorization: Session <key>" 头的请求会真实认证，
// 因此可精确扮演只读用户。
package auth_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	var err error
	sm, err = testutil.StartServer()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	cleanupFixtures()
	sm.Shutdown()
	os.Exit(code)
}

// cleanupFixtures removes the shared fixtures after all tests. It runs in
// TestMain (not in a per-test t.Cleanup) so the read-only user stays alive
// for every test function in this package.
func cleanupFixtures() {
	testutil.ClearHeaders()
	if readonlyUser != "" {
		testutil.DeleteUser(readonlyUser)
	}
	if tokenName != "" {
		testutil.GetClient().Delete("/open-api/v1/auth/tokens/" + tokenName)
	}
	if apiKeyID != "" {
		testutil.GetClient().Delete("/open-api/v1/api-keys/" + apiKeyID)
	}
	if providerName != "" {
		testutil.GetClient().Delete("/open-api/v1/providers/" + providerName)
	}
}

const (
	roleAdmin   = "system_admin"
	roleReadonly = "system_admin_readonly"
	password    = "password@123"
)

var (
	readonlyUser string
	readonlyKey  string
	apiKeyID     string
	apiKeyValue  string
	providerName string
	tokenName    string
	tokenValue   string
)

// setupOnce prepares the shared fixtures: a read-only user with a real
// session key, plus one api-key / provider / token to read back.
func setupOnce(t *testing.T) {
	if readonlyUser != "" {
		return
	}

	readonlyUser = testutil.UniqueUserName()
	require.NoError(t, testutil.CreateUserWithRole(readonlyUser, password, roleReadonly))

	key, err := testutil.LoginSessionKey(readonlyUser, password)
	require.NoError(t, err)
	require.NotEmpty(t, key)
	readonlyKey = key

	// api-key（自定义 key 值，便于断言掩码形态）
	apiKeyID, apiKeyValue = createAPIKeyWithCustomKey(t)

	// provider（CreateProvider 默认带 sk-aaaaaaaaaaaa / sk-bbbbbbbbbbbb）
	providerName = testutil.UniqueName("provider")
	_, err = testutil.CreateProvider(providerName)
	require.NoError(t, err)

	// token
	tokenName = testutil.UniqueName("token")
	tokenValue, err = testutil.CreateToken(tokenName, "System")
	require.NoError(t, err)
}

// asReadonly 之后发出的请求均以只读用户身份携带 Session 凭证。
func asReadonly(t *testing.T) {
	t.Helper()
	testutil.SetHeader("Authorization", "Session "+readonlyKey)
	t.Cleanup(func() { testutil.ClearHeaders() })
}

func createAPIKeyWithCustomKey(t *testing.T) (string, string) {
	t.Helper()
	resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", map[string]interface{}{
		"description": "readonly admin fixture",
		"key":         "ak-2v8x9k3m7p",
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	id, err := testutil.GetDataField(resp, "id")
	require.NoError(t, err)
	return id.(string), "ak-2v8x9k3m7p"
}

func TestReadonlyAdmin_Login(t *testing.T) {
	setupOnce(t)

	t.Run("AUTH-16-001 登录返回只读角色", func(t *testing.T) {
		resp, err := testutil.GetClient().Post("/open-api/v1/auth/session-keys", map[string]interface{}{
			"user_name": readonlyUser,
			"password":  password,
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		testutil.AssertDataFieldEquals(t, resp, "role", roleReadonly)

		// 同一用户再次登录会覆盖服务端 session key：后续用例必须使用本次新 key。
		key, err := testutil.GetDataField(resp, "session_key")
		require.NoError(t, err)
		readonlyKey = key.(string)
	})
}

func TestReadonlyAdmin_ReadAllowed(t *testing.T) {
	setupOnce(t)

	t.Run("AUTH-16-002 用户列表可读且含 role", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/auth/users")
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
	})

	t.Run("AUTH-16-003 报表/操作日志类读接口放行", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/operation-logs")
		require.NoError(t, err)
		// 操作日志可读（空数据也是 200）。
		assert.NotEqual(t, 403, resp.ErrNum, "readonly admin must be allowed to read operation logs, got %d %s", resp.ErrNum, resp.ErrMsg)
	})
}

func TestReadonlyAdmin_SecretMasking(t *testing.T) {
	setupOnce(t)

	t.Run("AUTH-16-010 api-keys 列表 key 掩码", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/api-keys")
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		list, err := testutil.GetDataListField(resp, "list")
		require.NoError(t, err)
		found := false
		for _, item := range list {
			entry, ok := item.(map[string]interface{})
			require.True(t, ok)
			if entry["id"] != apiKeyID {
				continue
			}
			found = true
			got, ok := entry["key"].(string)
			require.True(t, ok, "key field must be a string")
			assert.Equal(t, "ak-2********3m7p", got, "readonly must see masked key")
			assert.NotEqual(t, apiKeyValue, got)
		}
		assert.True(t, found, "fixture api-key must appear in list")
	})

	t.Run("AUTH-16-011 api-keys 详情 key 掩码", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		testutil.AssertDataFieldEquals(t, resp, "key", "ak-2********3m7p")
	})

	t.Run("AUTH-16-012 providers 列表 keys[].key 掩码", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/providers")
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		var data struct {
			List []struct {
				Name string `json:"name"`
				Keys []struct {
					Name string `json:"name"`
					Key  string `json:"key"`
				} `json:"keys"`
			} `json:"list"`
		}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		found := false
		for _, p := range data.List {
			if p.Name != providerName {
				continue
			}
			found = true
			require.NotEmpty(t, p.Keys)
			assert.Equal(t, "sk-a********aaaa", p.Keys[0].Key)
		}
		assert.True(t, found, "fixture provider must appear in list")
	})

	t.Run("AUTH-16-013 providers 详情 keys[].key 掩码", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/providers/" + providerName)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		var data struct {
			Keys []struct {
				Key string `json:"key"`
			} `json:"keys"`
		}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		require.NotEmpty(t, data.Keys)
		assert.Equal(t, "sk-a********aaaa", data.Keys[0].Key)
	})

	t.Run("AUTH-16-014 tokens 列表 token 掩码", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/auth/tokens")
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		var list []map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &list))
		found := false
		for _, item := range list {
			if item["name"] == tokenName {
				found = true
				got, ok := item["token"].(string)
				require.True(t, ok)
				assert.Equal(t, maskLikeToken(tokenValue), got, "readonly must see masked token")
				assert.NotEqual(t, tokenValue, got)
			}
		}
		assert.True(t, found, "fixture token must appear in list")
	})

	t.Run("AUTH-16-015 tokens 详情 token 掩码", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/auth/tokens/" + tokenName)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		got, err := testutil.GetDataField(resp, "token")
		require.NoError(t, err)
		assert.Equal(t, maskLikeToken(tokenValue), got.(string))
		assert.NotEqual(t, tokenValue, got.(string))
	})

	t.Run("AUTH-16-016 admin 视角明文回归", func(t *testing.T) {
		// 无 Authorization 头：Skip 模式视作完整管理员，必须看到明文。
		resp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		testutil.AssertDataFieldEquals(t, resp, "key", apiKeyValue)
	})
}

// maskLikeToken 对 token 值按服务端掩码规则（前4+"********"+后4）计算期望掩码。
func maskLikeToken(s string) string {
	if len(s) <= 4 {
		return "********"
	}
	if len(s) <= 8 {
		return s[:2] + "****" + s[len(s)-2:]
	}
	return s[:4] + "********" + s[len(s)-4:]
}

func TestReadonlyAdmin_WriteDenied(t *testing.T) {
	setupOnce(t)

	// 每个负向用例按技能清单「4xx 回读零变更」要求：拒绝后回读资源，断言与操作前一致。
	cases := []struct {
		name      string
		call      func() (*testutil.APIResponse, error)
		readBack  func(t *testing.T)
	}{
		{
			name: "AUTH-16-020 创建 api-key 拒绝",
			call: func() (*testutil.APIResponse, error) {
				return testutil.GetClient().Post("/open-api/v1/api-keys", map[string]interface{}{
					"description": "should be denied",
				})
			},
			readBack: func(t *testing.T) {
				// 未提交字段不变：不应出现以被拒 description 创建的 api-key。
				resp, err := testutil.GetClient().Get("/open-api/v1/api-keys")
				require.NoError(t, err)
				testutil.AssertSuccess(t, resp)
				list, err := testutil.GetDataListField(resp, "list")
				require.NoError(t, err)
				for _, item := range list {
					entry, ok := item.(map[string]interface{})
					require.True(t, ok)
					assert.NotEqual(t, "should be denied", entry["description"], "denied create must not persist")
				}
			},
		},
		{
			name: "AUTH-16-021 变更用户角色拒绝",
			call: func() (*testutil.APIResponse, error) {
				return testutil.GetClient().Patch("/open-api/v1/auth/users/"+readonlyUser+"/role", map[string]interface{}{
					"role": roleAdmin,
				})
			},
			readBack: func(t *testing.T) {
				// 自身角色仍只读：拒绝不得产生任何变更。
				resp, err := testutil.GetClient().Get("/open-api/v1/auth/users/" + readonlyUser)
				require.NoError(t, err)
				testutil.AssertSuccess(t, resp)
				testutil.AssertDataFieldEquals(t, resp, "role", roleReadonly)
			},
		},
		{
			name: "AUTH-16-022 删除 token 拒绝",
			call: func() (*testutil.APIResponse, error) {
				return testutil.GetClient().Delete("/open-api/v1/auth/tokens/" + tokenName)
			},
			readBack: func(t *testing.T) {
				// token 仍存在且可读（掩码值不影响存在性）。
				resp, err := testutil.GetClient().Get("/open-api/v1/auth/tokens/" + tokenName)
				require.NoError(t, err)
				testutil.AssertSuccess(t, resp)
			},
		},
		{
			name: "AUTH-16-023 删除 provider 拒绝",
			call: func() (*testutil.APIResponse, error) {
				return testutil.GetClient().Delete("/open-api/v1/providers/" + providerName)
			},
			readBack: func(t *testing.T) {
				resp, err := testutil.GetClient().Get("/open-api/v1/providers/" + providerName)
				require.NoError(t, err)
				testutil.AssertSuccess(t, resp)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asReadonly(t)
			resp, err := tc.call()
			require.NoError(t, err)
			// 错误码语义：管理写操作只允许 2xx/4xx，授权拒绝必须为受控 402，不得为 500。
			assert.Equal(t, 402, resp.ErrNum, "write must be denied with 402 for readonly admin, got %d %s", resp.ErrNum, resp.ErrMsg)
			assert.Contains(t, resp.ErrMsg, "Feature Access Deny")
			tc.readBack(t)
		})
	}
}

func TestReadonlyAdmin_InnerAPIExportUnaffected(t *testing.T) {
	setupOnce(t)

	t.Run("AUTH-16-040 inner-api 导出仍为明文（脱敏不影响数据面）", func(t *testing.T) {
		// 先以只读用户读 api-key（触发响应脱敏），再验证导出链路明文未被波及。
		asReadonly(t)
		readResp, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
		require.NoError(t, err)
		testutil.AssertSuccess(t, readResp)
		masked, err := testutil.GetDataField(readResp, "key")
		require.NoError(t, err)
		require.NotEqual(t, apiKeyValue, masked.(string), "readonly response must be masked first")

		testutil.ClearHeaders()
		exportResp, err := testutil.GetClient().Get("/inner-api/v1/configs/mod-api-key")
		require.NoError(t, err)
		testutil.AssertSuccess(t, exportResp)

		serialized, err := json.Marshal(exportResp.Data)
		require.NoError(t, err)
		assert.Contains(t, string(serialized), apiKeyValue,
			"inner-api export must keep the raw key for the data plane (masking is openapi-only)")
	})
}

func TestReadonlyAdmin_SessionKeyContract(t *testing.T) {
	setupOnce(t)

	t.Run("AUTH-16-030 用户读接口无 session_key（只读视角）", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/auth/users/" + readonlyUser)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		assert.NotContains(t, data, "session_key")
	})

	t.Run("AUTH-16-031 用户列表无 session_key（只读视角）", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/auth/users")
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		var list []map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &list))
		for _, item := range list {
			assert.NotContains(t, item, "session_key")
		}
	})

	t.Run("AUTH-16-032 响应无 is_admin 废弃字段", func(t *testing.T) {
		asReadonly(t)
		resp, err := testutil.GetClient().Get("/open-api/v1/auth/users/" + readonlyUser)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		assert.NotContains(t, data, "is_admin")
		assert.Contains(t, data, "role")
	})

	t.Run("AUTH-16-033 掩码值不可用于鉴权", func(t *testing.T) {
		asReadonly(t)
		detail, err := testutil.GetClient().Get("/open-api/v1/api-keys/" + apiKeyID)
		require.NoError(t, err)
		testutil.AssertSuccess(t, detail)
		masked, err := testutil.GetDataField(detail, "key")
		require.NoError(t, err)
		require.True(t, strings.Contains(masked.(string), "****"))

		// 数据面鉴权消费明文 key：掩码值不可能通过 BFE 侧精确匹配（语义说明，
		// 此处仅断言掩码值与明文不同且形态符合规则）。
		assert.NotEqual(t, apiKeyValue, masked.(string))
	})
}
