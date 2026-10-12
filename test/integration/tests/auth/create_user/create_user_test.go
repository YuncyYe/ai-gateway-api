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

func TestAuth_CreateUser(t *testing.T) {
	userDup := testutil.UniqueUserName()
	if err := testutil.CreateUser(userDup, "password@123"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	tests := []struct {
		name     string
		body     map[string]interface{}
		wantCode int
	}{
		{
			name:     "AUTH-1-001 创建用户（完整参数）",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "password": "password@123", "role": "system_admin"},
			wantCode: 200,
		},
		{
			name:     "AUTH-1-002 创建只读管理员",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "password": "password@123", "role": "system_admin_readonly"},
			wantCode: 200,
		},
		{
			name:     "AUTH-1-003 创建用户缺少 user_name",
			body:     map[string]interface{}{"password": "password@123", "role": "system_admin"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-004 创建用户缺少 password",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "role": "system_admin"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-005 重复创建用户",
			body:     map[string]interface{}{"user_name": userDup, "password": "password@123", "role": "system_admin"},
			wantCode: 555,
		},
		{
			name:     "AUTH-1-006 非法 user_name（以 - 开头）",
			body:     map[string]interface{}{"user_name": "-baduser", "password": "password@123", "role": "system_admin"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-007 保留 user_name（admin）",
			body:     map[string]interface{}{"user_name": "admin", "password": "password@123", "role": "system_admin"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-008 密码过短",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "password": "short1", "role": "system_admin"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-009 密码包含空白字符",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "password": "pass word", "role": "system_admin"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-010 密码与用户名相同",
			body:     map[string]interface{}{"user_name": "myuser_2024", "password": "myuser_2024", "role": "system_admin"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-011 创建用户缺少 role",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "password": "password@123"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-012 role 非法",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "password": "password@123", "role": "other"},
			wantCode: 422,
		},
		{
			name:     "AUTH-1-013 创建用户携带已废弃的 is_admin",
			body:     map[string]interface{}{"user_name": testutil.UniqueUserName(), "password": "password@123", "is_admin": true},
			wantCode: 422,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := testutil.GetClient().Post("/open-api/v1/auth/users", tt.body)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if resp.ErrNum != tt.wantCode {
				t.Errorf("expected ErrNum=%d, got ErrNum=%d, ErrMsg=%s", tt.wantCode, resp.ErrNum, resp.ErrMsg)
			}
		})
	}

	t.Cleanup(func() {
		testutil.DeleteUser(userDup)
	})
}
