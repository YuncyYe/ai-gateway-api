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

// quota_reset_null_guard_test 验证 issue #228（配额周期重置死循环）的修复效果：
//
//  1. 创建 reset_period=weekly/monthly 的计划时初始化 last_reset_at（源头治理）；
//  2. 存量 last_reset_at IS NULL 的计划触发一次重置后标记必须落库，
//     同周期内再次触发不得重复重置（旧实现因 SQL 三值逻辑丢失 NULL 分支，
//     标记永不落库，每分钟重复重置）；
//  3. 不调用任何重置接口、仅靠调度器自然 tick，也不出现每分钟回满额的现象。
package quota_period_reset_test

import (
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQuotaResetNullGuard_CreateInitLastResetAt 验证创建路径初始化 last_reset_at。
func TestQuotaResetNullGuard_CreateInitLastResetAt(t *testing.T) {
	t.Run("QR-4-001 创建带 monthly 配额的 API-Key 时初始化 last_reset_at", func(t *testing.T) {
		resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", map[string]interface{}{
			"description": "quota-null-guard-init",
			"quota_plan": map[string]interface{}{
				"unlimited":    false,
				"quota":        1000000,
				"unit":         "total_token",
				"reset_period": "monthly",
			},
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		idField, err := testutil.GetDataField(resp, "id")
		require.NoError(t, err)
		apiKeyID := idField.(string)
		defer testutil.DeleteAPIKey(apiKeyID)

		lastResetAt, err := sm.GetQuotaPlanLastResetAt(apiKeyID, "api_key")
		require.NoError(t, err, "monthly 计划创建后 last_reset_at 不应为 NULL")
		assert.WithinDuration(t, time.Now(), *lastResetAt, 2*time.Minute,
			"monthly 计划创建时应将 last_reset_at 初始化为创建时间")
	})

	t.Run("QR-4-002 创建 reset_period 缺省的 API-Key 保持 NULL", func(t *testing.T) {
		resp, err := testutil.GetClient().Post("/open-api/v1/api-keys", map[string]interface{}{
			"description": "quota-null-guard-never",
			"quota_plan": map[string]interface{}{
				"unlimited": false,
				"quota":     1000000,
				"unit":      "total_token",
			},
		})
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)
		idField, err := testutil.GetDataField(resp, "id")
		require.NoError(t, err)
		apiKeyID := idField.(string)
		defer testutil.DeleteAPIKey(apiKeyID)

		lastResetAt, err := sm.GetQuotaPlanLastResetAtNullable(apiKeyID, "api_key")
		require.NoError(t, err)
		assert.Nil(t, lastResetAt, "reset_period 缺省（never）时 last_reset_at 应保持 NULL")
	})
}

// TestQuotaResetNullGuard_LegacyNullClaimAndIdempotency 验证存量 NULL 计划
// （修复前创建、last_reset_at IS NULL）经一次触发重置后标记落库、同周期幂等。
// 旧实现下本测试在“标记落库”与“不得二次重置”两处均失败。
func TestQuotaResetNullGuard_LegacyNullClaimAndIdempotency(t *testing.T) {
	apiKeyID, apiKeyValue, err := testutil.CreateAPIKeyWithKey("quota-null-guard-legacy", "")
	require.NoError(t, err, "setup api-key failed")
	defer testutil.DeleteAPIKey(apiKeyID)

	_, err = testutil.GetClient().Patch("/open-api/v1/api-keys/"+apiKeyID, map[string]interface{}{
		"quota_plan": map[string]interface{}{
			"unlimited":    false,
			"quota":        1000000,
			"unit":         "total_token",
			"reset_period": "monthly",
		},
	})
	require.NoError(t, err, "patch quota plan failed")

	// 模拟存量数据：将 last_reset_at 置回 NULL（修复前创建的计划即此状态），
	// 注意须先 PATCH 出 quota_plan 行再置 NULL，否则查不到计划。
	require.NoError(t, sm.NullQuotaPlanLastResetAt(apiKeyID, "api_key"))
	lastResetAt, err := sm.GetQuotaPlanLastResetAtNullable(apiKeyID, "api_key")
	require.NoError(t, err)
	require.Nil(t, lastResetAt, "前置条件：last_reset_at 应为 NULL")

	// 模拟已消耗
	sm.SetQuotaRemaining(apiKeyValue, 100000, "total_token")

	t.Run("QR-4-003 NULL 计划触发一次重置并回填标记", func(t *testing.T) {
		resp, err := testutil.GetClient().Post("/inner-api/v1/quota/trigger-reset", map[string]interface{}{})
		require.NoError(t, err, "trigger reset failed")
		testutil.AssertSuccess(t, resp)

		remaining := sm.GetQuotaRemaining(apiKeyValue, "total_token")
		assert.InDelta(t, float64(1000000), remaining, 0.1, "NULL 计划的 Redis 剩余量应被重置为 quota")

		lastResetAt, err := sm.GetQuotaPlanLastResetAt(apiKeyID, "api_key")
		require.NoError(t, err, "重置后 last_reset_at 必须落库（旧实现此处仍为 NULL）")
		assert.WithinDuration(t, time.Now(), *lastResetAt, 2*time.Minute)
	})

	t.Run("QR-4-004 回填后同周期再次触发不得重复重置", func(t *testing.T) {
		// 若标记未落库（旧实现），此处会再次重置回 1000000
		sm.SetQuotaRemaining(apiKeyValue, 200000, "total_token")

		resp, err := testutil.GetClient().Post("/inner-api/v1/quota/trigger-reset", map[string]interface{}{})
		require.NoError(t, err, "second trigger reset failed")
		testutil.AssertSuccess(t, resp)

		remaining := sm.GetQuotaRemaining(apiKeyValue, "total_token")
		assert.InDelta(t, float64(200000), remaining, 0.1, "同周期内不得二次重置（旧实现此处会回满额）")
	})
}

// TestQuotaResetNullGuard_SchedulerTickNoLoop 端到端复现运行时症状（issue #228）：
// 不调用任何重置接口，仅靠调度器自然 tick（间隔 1 分钟）。修复前 NULL 计划每分钟
// 被 SET 回满额；修复后首个 tick 重置并回填，后续 tick 不再重置。
//
// 注：需等待约 2 个调度窗口，整体耗时 ~150s；go test -short 时跳过。
func TestQuotaResetNullGuard_SchedulerTickNoLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("scheduler tick test requires ~150s")
	}

	apiKeyID, apiKeyValue, err := testutil.CreateAPIKeyWithKey("quota-null-guard-tick", "")
	require.NoError(t, err, "setup api-key failed")
	defer testutil.DeleteAPIKey(apiKeyID)

	_, err = testutil.GetClient().Patch("/open-api/v1/api-keys/"+apiKeyID, map[string]interface{}{
		"quota_plan": map[string]interface{}{
			"unlimited":    false,
			"quota":        600000,
			"unit":         "total_token",
			"reset_period": "monthly",
		},
	})
	require.NoError(t, err, "patch quota plan failed")

	// 模拟存量数据：last_reset_at IS NULL
	require.NoError(t, sm.NullQuotaPlanLastResetAt(apiKeyID, "api_key"))

	// 第一窗口：某个自然 tick 应完成“重置 + 回填”
	sm.SetQuotaRemaining(apiKeyValue, 50000, "total_token")
	remaining := waitQuotaRemaining(t, apiKeyValue, "total_token", 600000, 90*time.Second)
	assert.InDelta(t, float64(600000), remaining, 0.1, "首个调度 tick 应重置 NULL 计划的 Redis 剩余量")

	_, err = sm.GetQuotaPlanLastResetAt(apiKeyID, "api_key")
	require.NoError(t, err, "调度 tick 重置后 last_reset_at 必须落库（旧实现此处仍为 NULL）")

	// 第二窗口：标记已回填，任何后续 tick 都不得再重置。
	// 旧实现下 remaining 会在 60 秒间隔内被反复 SET 回 600000。
	sm.SetQuotaRemaining(apiKeyValue, 123000, "total_token")
	deadline := time.Now().Add(75 * time.Second)
	for {
		remaining := sm.GetQuotaRemaining(apiKeyValue, "total_token")
		assert.InDelta(t, float64(123000), remaining, 0.1, "标记落库后调度 tick 不得再次重置（旧实现每分钟回满额）")
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
}

// waitQuotaRemaining 轮询等待某 owner 的 Redis 剩余量达到期望值，超时则返回最后读到的值。
func waitQuotaRemaining(t *testing.T, ownerKey string, unit string, expected float64, timeout time.Duration) float64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := sm.GetQuotaRemaining(ownerKey, unit)
		if remaining == expected {
			return remaining
		}
		if time.Now().After(deadline) {
			return remaining
		}
		time.Sleep(2 * time.Second)
	}
}
