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

package icluster_conf

import (
	"testing"

	"github.com/bfenetworks/bfe/bfe_config/bfe_cluster_conf/cluster_conf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/imodel_price"
)

func batchPriceEntry(provider, model string, discount *float64) *imodel_price.ModelPrice {
	return &imodel_price.ModelPrice{
		Provider:      provider,
		Model:         model,
		BaseModel:     model,
		Mode:          "chat",
		BatchDiscount: discount,
		Prices: imodel_price.PriceMap{
			"input_cost_per_token":  0.00000125,
			"output_cost_per_token": 0.00001,
		},
		TierPrices: imodel_price.TierPriceMap{
			"peak": {
				"input_cost_per_token":  0.0000025,
				"output_cost_per_token": 0.00002,
			},
		},
		PriceCurrency: "RMB",
	}
}

func findModelPriceRow(models []cluster_conf.ModelPrice, model, mode string) *cluster_conf.ModelPrice {
	for i := range models {
		if models[i].Model == model && models[i].Mode == mode {
			return &models[i]
		}
	}
	return nil
}

func TestBuildModelTableModels_BatchExpansion(t *testing.T) {
	t.Run("nil discount expands nothing", func(t *testing.T) {
		models := buildModelTableModels([]*imodel_price.ModelPrice{batchPriceEntry("openai", "gpt-4", nil)})
		require.Len(t, models, 1)
		assert.Equal(t, "chat", models[0].Mode)
	})

	t.Run("discount expands prices and tier prices rounded to 8 decimals", func(t *testing.T) {
		entry := batchPriceEntry("openai", "gpt-4", lib.PFloat64(0.5))
		models := buildModelTableModels([]*imodel_price.ModelPrice{entry})
		require.Len(t, models, 2)
		assert.Equal(t, "chat", models[0].Mode)

		batch := findModelPriceRow(models, "gpt-4", "batch")
		require.NotNil(t, batch)
		assert.Equal(t, entry.Provider, batch.Provider)
		assert.Equal(t, entry.BaseModel, batch.BaseModel)
		// 1.25e-6 * 0.5 = 6.25e-7 = 62.5e-8, rounded onto the 1e-8 grid.
		assert.InDelta(t, 6.3e-7, batch.Prices["input_cost_per_token"], 1e-15)
		assert.InDelta(t, 5e-6, batch.Prices["output_cost_per_token"], 1e-15)
		require.NotNil(t, batch.TierPrices)
		assert.InDelta(t, 1.25e-6, batch.TierPrices["peak"]["input_cost_per_token"], 1e-15)
		assert.InDelta(t, 1e-5, batch.TierPrices["peak"]["output_cost_per_token"], 1e-15)
	})

	t.Run("rounding keeps the 1e-8 fixed-point denomination clean", func(t *testing.T) {
		entry := &imodel_price.ModelPrice{
			Provider:      "openai",
			Model:         "gpt-4",
			BaseModel:     "gpt-4",
			Mode:          "chat",
			BatchDiscount: lib.PFloat64(0.7),
			Prices: imodel_price.PriceMap{
				// Each product is quantized onto the 1e-8 grid (round half
				// away from zero at the 9th decimal place).
				"input_cost_per_token":        0.00000125,  // *0.7 = 8.75e-7 -> 8.8e-7
				"output_cost_per_token":       0.00000003,  // *0.7 = 2.1e-8  -> 2e-8
				"cache_read_input_token_cost": 0.00000001,  // *0.7 = 7e-9    -> 1e-8
				"output_cost_per_image":       0.000000015, // *0.7 = 1.05e-8 -> 1e-8
			},
		}
		models := buildModelTableModels([]*imodel_price.ModelPrice{entry})
		require.Len(t, models, 2)
		batch := findModelPriceRow(models, "gpt-4", "batch")
		require.NotNil(t, batch)
		assert.InDelta(t, 8.8e-7, batch.Prices["input_cost_per_token"], 1e-15)
		assert.InDelta(t, 2e-8, batch.Prices["output_cost_per_token"], 1e-15)
		assert.InDelta(t, 1e-8, batch.Prices["cache_read_input_token_cost"], 1e-15)
		assert.InDelta(t, 1e-8, batch.Prices["output_cost_per_image"], 1e-15)
	})

	t.Run("manual batch row takes precedence over expansion", func(t *testing.T) {
		manual := &imodel_price.ModelPrice{
			Provider:  "openai",
			Model:     "gpt-4",
			BaseModel: "gpt-4",
			Mode:      "batch",
			Prices: imodel_price.PriceMap{
				"input_cost_per_token": 0.0000006,
			},
		}
		entry := batchPriceEntry("openai", "gpt-4", lib.PFloat64(0.5))
		models := buildModelTableModels([]*imodel_price.ModelPrice{entry, manual})
		require.Len(t, models, 2)
		batch := findModelPriceRow(models, "gpt-4", "batch")
		require.NotNil(t, batch)
		// The manual row must be the one kept: its price is the manual 6e-7,
		// not the expanded 6.3e-7 (1.25e-6 * 0.5 rounded onto the 1e-8 grid).
		assert.InDelta(t, 0.0000006, batch.Prices["input_cost_per_token"], 1e-15)
	})

	t.Run("batch row with discount does not self-expand", func(t *testing.T) {
		manual := batchPriceEntry("openai", "gpt-4", lib.PFloat64(0.5))
		manual.Mode = "batch"
		models := buildModelTableModels([]*imodel_price.ModelPrice{manual})
		require.Len(t, models, 1)
	})

	t.Run("nil entries are skipped", func(t *testing.T) {
		entry := batchPriceEntry("openai", "gpt-4", lib.PFloat64(0.5))
		models := buildModelTableModels([]*imodel_price.ModelPrice{nil, entry})
		require.Len(t, models, 2)
	})

	t.Run("discount on one model does not leak into another", func(t *testing.T) {
		discounted := batchPriceEntry("openai", "gpt-4", lib.PFloat64(0.5))
		plain := batchPriceEntry("openai", "gpt-3.5", nil)
		models := buildModelTableModels([]*imodel_price.ModelPrice{discounted, plain})
		require.Len(t, models, 3)
		assert.Nil(t, findModelPriceRow(models, "gpt-3.5", "batch"))
	})
}
