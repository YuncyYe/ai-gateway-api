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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/rate_limit_policy"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

func TestRateLimitPolicyDataToParam_BatchLimits(t *testing.T) {
	t.Run("nil batch limits write empty-string sentinel", func(t *testing.T) {
		data := rateLimitPolicyDataToParam(&rate_limit_policy.RateLimitPolicyParam{
			Enabled: lib.PBool(true),
		})
		require.NotNil(t, data)
		// Whole-replacement semantics: nil must overwrite the stored column
		// with the empty sentinel instead of being skipped by the DAO.
		require.NotNil(t, data.BatchLimits)
		assert.Equal(t, "", *data.BatchLimits)
	})

	t.Run("batch limits marshal to JSON", func(t *testing.T) {
		data := rateLimitPolicyDataToParam(&rate_limit_policy.RateLimitPolicyParam{
			Enabled: lib.PBool(true),
			BatchLimits: &rate_limit_policy.BatchLimits{
				MaxCreateRPM:     10,
				MaxActiveBatches: 5,
				MaxFileBytes:     104857600,
				MaxFileLines:     50000,
			},
		})
		require.NotNil(t, data)
		require.NotNil(t, data.BatchLimits)
		assert.JSONEq(t, `{
			"max_create_rpm": 10,
			"max_active_batches": 5,
			"max_file_bytes": 104857600,
			"max_file_lines": 50000
		}`, *data.BatchLimits)
	})
}

func TestRateLimitPolicyParamToData_BatchLimits(t *testing.T) {
	t.Run("empty batch limits parse to nil", func(t *testing.T) {
		param := rateLimitPolicyParamToData(&dao.TRateLimitPolicy{BatchLimits: ""})
		assert.Nil(t, param.BatchLimits)
	})

	t.Run("null batch limits parse to nil", func(t *testing.T) {
		param := rateLimitPolicyParamToData(&dao.TRateLimitPolicy{BatchLimits: "null"})
		assert.Nil(t, param.BatchLimits)
	})

	t.Run("json batch limits parse back", func(t *testing.T) {
		param := rateLimitPolicyParamToData(&dao.TRateLimitPolicy{BatchLimits: `{"max_create_rpm":10,"max_file_lines":50000}`})
		require.NotNil(t, param.BatchLimits)
		assert.Equal(t, 10, param.BatchLimits.MaxCreateRPM)
		assert.Equal(t, 0, param.BatchLimits.MaxActiveBatches)
		assert.Equal(t, int64(0), param.BatchLimits.MaxFileBytes)
		assert.Equal(t, 50000, param.BatchLimits.MaxFileLines)
	})

	t.Run("invalid json keeps batch limits nil", func(t *testing.T) {
		param := rateLimitPolicyParamToData(&dao.TRateLimitPolicy{BatchLimits: `{not-json`})
		assert.Nil(t, param.BatchLimits)
	})
}
