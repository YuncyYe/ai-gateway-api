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

package auth_test

import (
	"os"
	"testing"

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

func TestAuth_ResetPassword(t *testing.T) {
	userName := testutil.UniqueUserName()
	if err := testutil.CreateUser(userName, "password@123"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("AUTH-3-001 管理员重置他人密码", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/passwd", map[string]interface{}{
			"password": "newpassword@456",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, resp)
	})

	t.Run("AUTH-3-002 缺少 password", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/passwd", map[string]interface{}{})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("AUTH-3-003 密码过短", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/passwd", map[string]interface{}{
			"password": "short1",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Run("AUTH-3-004 修改不存在用户的密码", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/non_existent_user/passwd", map[string]interface{}{
			"password": "newpassword@456",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 404)
	})

	t.Run("AUTH-3-005 管理员代改内置 admin 密码", func(t *testing.T) {
		// issue #226：保留名校验不得阻断对内置账号的引用。
		// 集成环境 SkipTokenValidate=true，visitor 为 SkipUser，走代改路径无需 old_password。
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/admin/passwd", map[string]interface{}{
			"password": "admin-reset@789",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, resp)
	})

	t.Run("AUTH-3-006 密码 7 字节边界（tag min=8 拦截）", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/auth/users/"+userName+"/passwd", map[string]interface{}{
			"password": "short12",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 422)
	})

	t.Cleanup(func() {
		testutil.DeleteUser(userName)
	})
}
