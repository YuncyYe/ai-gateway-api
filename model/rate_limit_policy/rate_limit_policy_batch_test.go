// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
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

package rate_limit_policy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

func TestAdapter_BatchLimitsPassThrough(t *testing.T) {
	ctx := context.Background()

	t.Run("create passes batch limits through", func(t *testing.T) {
		store := &fakeRateLimitPolicyStorager{}
		adapter := NewRateLimitPolicyStoragerAdapter(store)

		_, err := adapter.CreateRateLimitPolicy(ctx, &shared.RateLimitPolicyParam{
			Enabled: lib.PBool(true),
			Rules: &shared.RateLimitRules{
				BatchLimits: &shared.BatchLimits{
					MaxCreateRPM:     10,
					MaxActiveBatches: 5,
					MaxFileBytes:     104857600,
					MaxFileLines:     50000,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, store.created, 1)
		require.NotNil(t, store.created[0].BatchLimits)
		assert.Equal(t, 10, store.created[0].BatchLimits.MaxCreateRPM)
		assert.Equal(t, 5, store.created[0].BatchLimits.MaxActiveBatches)
		assert.Equal(t, int64(104857600), store.created[0].BatchLimits.MaxFileBytes)
		assert.Equal(t, 50000, store.created[0].BatchLimits.MaxFileLines)
	})

	t.Run("create without rules keeps batch limits nil", func(t *testing.T) {
		store := &fakeRateLimitPolicyStorager{}
		adapter := NewRateLimitPolicyStoragerAdapter(store)

		_, err := adapter.CreateRateLimitPolicy(ctx, &shared.RateLimitPolicyParam{
			Enabled: lib.PBool(false),
		})
		require.NoError(t, err)
		require.Len(t, store.created, 1)
		assert.Nil(t, store.created[0].BatchLimits)
	})

	t.Run("update passes batch limits through", func(t *testing.T) {
		store := &fakeRateLimitPolicyStorager{}
		adapter := NewRateLimitPolicyStoragerAdapter(store)

		_, err := adapter.UpdateRateLimitPolicy(ctx, 7, &shared.RateLimitPolicyParam{
			Enabled: lib.PBool(true),
			Rules: &shared.RateLimitRules{
				BatchLimits: &shared.BatchLimits{MaxCreateRPM: 3},
			},
		})
		require.NoError(t, err)
		require.Len(t, store.updated, 1)
		require.NotNil(t, store.updated[0].param.BatchLimits)
		assert.Equal(t, 3, store.updated[0].param.BatchLimits.MaxCreateRPM)
	})

	t.Run("fetch converts batch limits back", func(t *testing.T) {
		store := &fakeRateLimitPolicyStorager{
			fetchFn: func(ctx context.Context, filter *RateLimitPolicyFilter) (*RateLimitPolicyParam, error) {
				return &RateLimitPolicyParam{
					ID:      lib.PInt64(7),
					Enabled: lib.PBool(true),
					BatchLimits: &BatchLimits{
						MaxCreateRPM: 10,
						MaxFileLines: 50000,
					},
				}, nil
			},
		}
		adapter := NewRateLimitPolicyStoragerAdapter(store)

		result, err := adapter.FetchRateLimitPolicy(ctx, 7)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Rules)
		require.NotNil(t, result.Rules.BatchLimits)
		assert.Equal(t, 10, result.Rules.BatchLimits.MaxCreateRPM)
		assert.Equal(t, 0, result.Rules.BatchLimits.MaxActiveBatches)
		assert.Equal(t, int64(0), result.Rules.BatchLimits.MaxFileBytes)
		assert.Equal(t, 50000, result.Rules.BatchLimits.MaxFileLines)
	})

	t.Run("fetch without batch limits stays nil", func(t *testing.T) {
		store := &fakeRateLimitPolicyStorager{
			fetchFn: func(ctx context.Context, filter *RateLimitPolicyFilter) (*RateLimitPolicyParam, error) {
				return &RateLimitPolicyParam{ID: lib.PInt64(7), Enabled: lib.PBool(true)}, nil
			},
		}
		adapter := NewRateLimitPolicyStoragerAdapter(store)

		result, err := adapter.FetchRateLimitPolicy(ctx, 7)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Nil(t, result.Rules.BatchLimits)
	})
}

func TestRateLimitPolicyManager_GeneratorBatchLimits(t *testing.T) {
	ctx := context.Background()

	buildManager := func(batchLimits *BatchLimits) *RateLimitPolicyManager {
		apiKey := "ak-1"
		policyID := int64(101)
		apiKeyStore := &fakeAPIKeyStorager{
			fetchListFn: func(ctx context.Context, filter *api_key.APIKeyFilter) ([]*api_key.APIKeyParam, error) {
				return []*api_key.APIKeyParam{{Key: &apiKey, RateLimitPolicyID: &policyID}}, nil
			},
		}
		policyStore := &fakeRateLimitPolicyStorager{
			listFn: func(ctx context.Context, filter *RateLimitPolicyFilter) ([]*RateLimitPolicyParam, error) {
				return []*RateLimitPolicyParam{
					{ID: &policyID, Enabled: lib.PBool(true), BatchLimits: batchLimits},
				}, nil
			},
		}
		return NewRateLimitPolicyManager(&fakeTxn{}, policyStore, apiKeyStore, &fakeEntityStorager{}, nil)
	}

	t.Run("nil batch limits omits the batch segment", func(t *testing.T) {
		m := buildManager(nil)
		data, err := m.RateLimitPolicyGenerator(ctx)
		require.NoError(t, err)
		conf := data.DataWithoutVersion.(*ExportRateLimitPolicyConfig)
		policy := conf.RateLimitPolicies["rlp-101"]
		require.NotNil(t, policy)
		assert.Nil(t, policy.Rules.Batch)

		raw, err := json.Marshal(policy.Rules)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "batch")
	})

	t.Run("full batch limits export with redis key", func(t *testing.T) {
		m := buildManager(&BatchLimits{
			MaxCreateRPM:     10,
			MaxActiveBatches: 5,
			MaxFileBytes:     104857600,
			MaxFileLines:     50000,
		})
		data, err := m.RateLimitPolicyGenerator(ctx)
		require.NoError(t, err)
		conf := data.DataWithoutVersion.(*ExportRateLimitPolicyConfig)
		policy := conf.RateLimitPolicies["rlp-101"]
		require.NotNil(t, policy)
		require.NotNil(t, policy.Rules.Batch)
		assert.Equal(t, 10, policy.Rules.Batch.MaxCreateRPM)
		assert.Equal(t, 5, policy.Rules.Batch.MaxActiveBatches)
		assert.Equal(t, int64(104857600), policy.Rules.Batch.MaxFileBytes)
		assert.Equal(t, 50000, policy.Rules.Batch.MaxFileLines)
		assert.Equal(t, "default_bfe_rlp-101_RL_BATCH_rlp-101_rpm", policy.Rules.Batch.RedisKey)
	})

	t.Run("zero create rpm exports no redis key", func(t *testing.T) {
		m := buildManager(&BatchLimits{
			MaxActiveBatches: 5,
			MaxFileBytes:     104857600,
		})
		data, err := m.RateLimitPolicyGenerator(ctx)
		require.NoError(t, err)
		conf := data.DataWithoutVersion.(*ExportRateLimitPolicyConfig)
		policy := conf.RateLimitPolicies["rlp-101"]
		require.NotNil(t, policy)
		require.NotNil(t, policy.Rules.Batch)
		assert.Equal(t, 0, policy.Rules.Batch.MaxCreateRPM)
		assert.Equal(t, "", policy.Rules.Batch.RedisKey)
		assert.Equal(t, 5, policy.Rules.Batch.MaxActiveBatches)

		raw, err := json.Marshal(policy.Rules)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "redis_key")
	})
}

func TestExportBatchLimits_Nil(t *testing.T) {
	assert.Nil(t, exportBatchLimits(101, nil))
}
