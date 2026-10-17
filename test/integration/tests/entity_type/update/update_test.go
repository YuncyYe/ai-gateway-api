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

package entity_type_test

import (
	"os"
	"strings"
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

func TestEntityType_Update(t *testing.T) {
	typeName := testutil.UniqueEntityTypeName()
	if _, err := testutil.CreateEntityType(typeName, 1); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("ET-4-001 更新 Entity-Type 描述", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
			"description": "更新后的描述",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, resp)
		testutil.AssertDataFieldEquals(t, resp, "type_name", typeName)
		testutil.AssertDataFieldEquals(t, resp, "description", "更新后的描述")
		testutil.AssertDataFieldEquals(t, resp, "level", float64(1))
	})

	t.Run("ET-4-002 更新后查询一致性", func(t *testing.T) {
		_, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
			"description": "一致性校验描述",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp, err := testutil.GetClient().Get("/open-api/v1/entity-types/" + typeName)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, resp)
		testutil.AssertDataFieldEquals(t, resp, "description", "一致性校验描述")
	})

	t.Run("ET-4-003 更新不存在的 Entity-Type", func(t *testing.T) {
		resp, err := testutil.GetClient().Patch("/open-api/v1/entity-types/non_existent_type", map[string]interface{}{
			"description": "x",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 404)
	})

	t.Run("ET-4-004 更新 description 超过最大长度", func(t *testing.T) {
		// entity-types.md §1：0-255 字节。恰好 256 是契约边界（必须拒绝），
		// 300 为"远超限"臂，保留既有覆盖。
		for _, n := range []int{256, 300} {
			resp, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
				"description": strings.Repeat("a", n),
			})
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if resp.ErrNum != 422 {
				t.Errorf("len=%d: expected ErrNum=422, got ErrNum=%d, ErrMsg=%s", n, resp.ErrNum, resp.ErrMsg)
			}
		}
	})

	t.Run("ET-4-005 更新 description 恰好 255 字节（合法边界）", func(t *testing.T) {
		desc := strings.Repeat("b", 255)
		resp, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
			"description": desc,
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, resp)
		testutil.AssertDataFieldEquals(t, resp, "description", desc)

		got, err := testutil.GetClient().Get("/open-api/v1/entity-types/" + typeName)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, got)
		testutil.AssertDataFieldEquals(t, got, "description", desc)
	})

	t.Run("ET-4-006 更新 description 含控制字符被拒绝且旧值保留", func(t *testing.T) {
		baseline := "控制字符基线"
		if _, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
			"description": baseline,
		}); err != nil {
			t.Fatalf("request failed: %v", err)
		}

		resp, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
			"description": "abc\x01def",
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 422)

		got, err := testutil.GetClient().Get("/open-api/v1/entity-types/" + typeName)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, got)
		testutil.AssertDataFieldEquals(t, got, "description", baseline)
	})

	t.Run("ET-4-007 非法 description 不污染存量值（原子性）", func(t *testing.T) {
		baseline := "原子性基线"
		if _, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
			"description": baseline,
		}); err != nil {
			t.Fatalf("request failed: %v", err)
		}

		resp, err := testutil.GetClient().Patch("/open-api/v1/entity-types/"+typeName, map[string]interface{}{
			"description": strings.Repeat("c", 256),
		})
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertErrCode(t, resp, 422)

		got, err := testutil.GetClient().Get("/open-api/v1/entity-types/" + typeName)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		testutil.AssertSuccess(t, got)
		testutil.AssertDataFieldEquals(t, got, "description", baseline)
	})

	t.Cleanup(func() {
		testutil.DeleteEntityType(typeName)
	})
}
