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

package auth_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	var err error
	sm, err = testutil.StartServer()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	sm.Shutdown()
	os.Exit(code)
}

// isBcryptHash 判定 users.password 存储值是否为 bcrypt 哈希串（测试侧最小实现，
// 与 lib/xcrypto.IsHashedPassword 等价）。
func isBcryptHash(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

func login(t *testing.T, userName, password string) *testutil.APIResponse {
	t.Helper()
	resp, err := testutil.GetClient().Post("/open-api/v1/auth/session-keys", map[string]interface{}{
		"user_name": userName,
		"password":  password,
	})
	require.NoError(t, err)
	return resp
}

func TestAuth_PasswordHash(t *testing.T) {
	t.Run("AUTH-14-001 DDL种子admin口令为bcrypt哈希且admin可登录", func(t *testing.T) {
		stored, err := testutil.GetUserPassword(sm.DBPath, "admin")
		require.NoError(t, err)
		assert.True(t, isBcryptHash(stored), "admin password must be stored as bcrypt hash, got %q", stored)
		assert.NotEqual(t, "admin", stored)

		testutil.AssertSuccess(t, login(t, "admin", "admin"))
	})

	t.Run("AUTH-14-002 创建用户落库为哈希", func(t *testing.T) {
		userName := testutil.UniqueUserName()
		require.NoError(t, testutil.CreateUser(userName, "password@123"))
		t.Cleanup(func() { testutil.DeleteUser(userName) })

		stored, err := testutil.GetUserPassword(sm.DBPath, userName)
		require.NoError(t, err)
		assert.True(t, isBcryptHash(stored), "created user password must be stored as bcrypt hash, got %q", stored)
		assert.NotEqual(t, "password@123", stored)

		testutil.AssertErrCode(t, login(t, userName, "wrong-password"), 401)
	})

	t.Run("AUTH-14-003 存量明文登录懒迁移", func(t *testing.T) {
		userName := testutil.UniqueUserName()
		require.NoError(t, testutil.CreateUser(userName, "password@123"))
		t.Cleanup(func() { testutil.DeleteUser(userName) })

		// 模拟老版本存量库：password 为明文。
		require.NoError(t, testutil.SetUserPassword(sm.DBPath, userName, "password@123"))
		stored, err := testutil.GetUserPassword(sm.DBPath, userName)
		require.NoError(t, err)
		require.Equal(t, "password@123", stored, "precondition: plaintext row")

		// 明文口令登录成功，并在同一事务内重哈希落库。
		testutil.AssertSuccess(t, login(t, userName, "password@123"))
		stored, err = testutil.GetUserPassword(sm.DBPath, userName)
		require.NoError(t, err)
		assert.True(t, isBcryptHash(stored), "legacy plaintext must be re-hashed after login, got %q", stored)

		// 迁移后同一口令走哈希校验路径仍可登录（口令本身不变）。
		testutil.AssertSuccess(t, login(t, userName, "password@123"))
	})

	t.Run("AUTH-14-004 SKIP字面量不再旁路", func(t *testing.T) {
		userName := testutil.UniqueUserName()
		require.NoError(t, testutil.CreateUser(userName, "whatever@123"))
		t.Cleanup(func() { testutil.DeleteUser(userName) })

		// 历史后门：password 字面量 "SKIP" 会跳过校验；整改后必须是普通错误口令。
		testutil.AssertErrCode(t, login(t, userName, "SKIP"), 401)
	})

	t.Run("AUTH-14-005 73字节密码拒绝且回读零变更", func(t *testing.T) {
		longPassword := strings.Repeat("a", 73)

		// 创建路径：73 字节密码 422，且库中无该用户（回读零变更）。
		rejectedName := testutil.UniqueUserName()
		resp, err := testutil.GetClient().Post("/open-api/v1/auth/users", map[string]interface{}{
			"user_name": rejectedName,
			"password":  longPassword,
			"role":      "system_admin",
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)
		_, err = testutil.GetUserPassword(sm.DBPath, rejectedName)
		assert.Error(t, err, "rejected user must not exist in DB")

		// 改密路径：73 字节密码 422，且存储值与操作前逐字节一致（回读零变更）。
		userName := testutil.UniqueUserName()
		require.NoError(t, testutil.CreateUser(userName, "password@123"))
		t.Cleanup(func() { testutil.DeleteUser(userName) })

		before, err := testutil.GetUserPassword(sm.DBPath, userName)
		require.NoError(t, err)

		resp, err = testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/passwd", map[string]interface{}{
			"password": longPassword,
		})
		require.NoError(t, err)
		testutil.AssertErrCode(t, resp, 422)

		after, err := testutil.GetUserPassword(sm.DBPath, userName)
		require.NoError(t, err)
		assert.Equal(t, before, after, "rejected password change must not modify the stored value")
	})

	t.Run("AUTH-14-006 重置密码落库为哈希且审计日志无明文", func(t *testing.T) {
		userName := testutil.UniqueUserName()
		require.NoError(t, testutil.CreateUser(userName, "oldpassword@123"))
		t.Cleanup(func() { testutil.DeleteUser(userName) })

		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/passwd", map[string]interface{}{
			"password": "newpassword@456",
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		stored, err := testutil.GetUserPassword(sm.DBPath, userName)
		require.NoError(t, err)
		assert.True(t, isBcryptHash(stored), "reset password must be stored as bcrypt hash, got %q", stored)

		// 旧口令失效、新口令生效。
		testutil.AssertErrCode(t, login(t, userName, "oldpassword@123"), 401)
		testutil.AssertSuccess(t, login(t, userName, "newpassword@456"))

		// 审计脱敏：整条操作日志 JSON 序列化后不得包含新旧口令明文。
		entry, err := testutil.WaitForOperationLog(map[string]string{
			"resource_type": "user",
			"resource_name": userName,
			"action":        "update",
		}, 10*time.Second)
		require.NoError(t, err)
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "newpassword@456", "new password plaintext must not appear in operation log")
		assert.NotContains(t, string(raw), "oldpassword@123", "old password plaintext must not appear in operation log")
	})
}
