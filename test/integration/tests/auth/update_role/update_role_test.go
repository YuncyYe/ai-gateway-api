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
	sm.Shutdown()
	os.Exit(code)
}

// assertRole fetches a user and asserts its role field.
func assertRole(t *testing.T, userName, wantRole string) {
	t.Helper()
	getResp, err := testutil.GetClient().Get("/open-api/v1/auth/users/" + userName)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	testutil.AssertSuccess(t, getResp)
	testutil.AssertDataFieldEquals(t, getResp, "role", wantRole)
}

func TestAuth_UpdateRole(t *testing.T) {
	userName := testutil.UniqueUserName()
	if err := testutil.CreateUser(userName, "password@123"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("AUTH-5-001 变更为只读管理员并复核", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/role", map[string]interface{}{
			"role": "system_admin_readonly",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, resp)
		assertRole(t, userName, "system_admin_readonly")
	})

	t.Run("AUTH-5-002 变更回系统管理员并复核", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/role", map[string]interface{}{
			"role": "system_admin",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, resp)
		assertRole(t, userName, "system_admin")
	})

	t.Run("AUTH-5-003 内置 admin 不可降级", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/admin/role", map[string]interface{}{
			"role": "system_admin_readonly",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.ErrNum == 200 {
			t.Errorf("downgrading builtin admin must fail, got success")
		}
		assertRole(t, "admin", "system_admin")
	})

	t.Run("AUTH-5-004 内置 admin 大小写变体同样不可降级", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/Admin/role", map[string]interface{}{
			"role": "system_admin_readonly",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.ErrNum == 200 {
			t.Errorf("downgrading builtin admin (case variant) must fail, got success")
		}
	})

	t.Run("AUTH-5-005 role 非法", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/role", map[string]interface{}{
			"role": "other",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.ErrNum == 200 {
			t.Errorf("invalid role must fail, got success")
		}
	})

	t.Run("AUTH-5-006 用户不存在", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/not_exist_user/role", map[string]interface{}{
			"role": "system_admin_readonly",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.ErrNum == 200 {
			t.Errorf("missing user must fail, got success")
		}
	})

	t.Run("AUTH-5-007 旧 is_admin 端点已废弃", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/is_admin", map[string]interface{}{
			"is_admin": true,
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		// The route no longer exists: gorilla/mux falls through to 404.
		if resp.ErrNum == 200 {
			t.Errorf("deprecated is_admin endpoint must not succeed, got ErrNum=%d", resp.ErrNum)
		}
	})

	t.Run("AUTH-5-008 角色变更写操作日志", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/role", map[string]interface{}{
			"role": "system_admin_readonly",
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		entry, err := testutil.WaitForOperationLog(map[string]string{
			"resource_type": "user",
			"action":        "update",
			"resource_name": userName,
		}, 0)
		require.NoError(t, err, "role change operation log not found")
		_, ok := entry.ChangeSummary["after"].(map[string]interface{})
		require.True(t, ok, "change_summary.after should be an object")
		serialized, err := json.Marshal(entry.ChangeSummary)
		require.NoError(t, err)
		assert.Contains(t, string(serialized), "system_admin_readonly",
			"audit log must record the new role")
	})

	t.Cleanup(func() {
		testutil.DeleteUser(userName)
	})
}
