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

package batch_price_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	var err error
	sm, err = testutil.StartServerAuto()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	sm.Shutdown()
	os.Exit(code)
}

// ---------- MPB-1-001 创建 mode=batch 价格行 ----------

func TestBatchPrice_CreateBatchModeRow(t *testing.T) {
	provider := testutil.UniqueName("bp-provider")
	model := testutil.UniqueName("bp-model")

	id, err := testutil.CreateModelPrice(map[string]interface{}{
		"provider":   provider,
		"model":      model,
		"base_model": model,
		"mode":       "batch",
		"prices": map[string]interface{}{
			"input_cost_per_token":  0.000001,
			"output_cost_per_token": 0.000004,
		},
	})
	require.NoError(t, err)
	defer testutil.DeleteModelPrice(id)
	assert.Greater(t, id, int64(0))

	resp, err := testutil.GetClient().Get(fmt.Sprintf("/open-api/v1/model-prices/%d", id))
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	testutil.AssertDataFieldEquals(t, resp, "mode", "batch")
	testutil.AssertDataFieldEquals(t, resp, "provider", provider)
}

// ---------- MPB-1-002 创建 chat 行携带 batch_discount=0.5 ----------

func TestBatchPrice_CreateChatRowWithDiscount(t *testing.T) {
	provider := testutil.UniqueName("bp-provider")
	model := testutil.UniqueName("bp-model")

	id, err := testutil.CreateModelPrice(map[string]interface{}{
		"provider":       provider,
		"model":          model,
		"base_model":     model,
		"mode":           "chat",
		"batch_discount": 0.5,
		"prices": map[string]interface{}{
			"input_cost_per_token":  0.000002,
			"output_cost_per_token": 0.000008,
		},
	})
	require.NoError(t, err)
	defer testutil.DeleteModelPrice(id)

	resp, err := testutil.GetClient().Get(fmt.Sprintf("/open-api/v1/model-prices/%d", id))
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	testutil.AssertDataFieldEquals(t, resp, "mode", "chat")

	// batch_discount 回读值精确（导出展开依赖该值）
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.InDelta(t, 0.5, data["batch_discount"], 1e-12, "batch_discount must round-trip exactly")
}

// ---------- MPB-1-003 非法 batch_discount（0 / 负数 / >1）→ 422 ----------

func TestBatchPrice_InvalidDiscount(t *testing.T) {
	tests := []struct {
		name     string
		discount float64
	}{
		{"batch_discount=0", 0},
		{"batch_discount=-0.5", -0.5},
		{"batch_discount=1.5", 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := testutil.GetClient().Post("/open-api/v1/model-prices", map[string]interface{}{
				"provider":       testutil.UniqueName("bp-provider"),
				"model":          testutil.UniqueName("bp-model"),
				"base_model":     "bp-base",
				"mode":           "chat",
				"batch_discount": tt.discount,
				"prices": map[string]interface{}{
					"input_cost_per_token": 0.000002,
				},
			})
			require.NoError(t, err)
			testutil.AssertErrCode(t, resp, 422)
			assert.Contains(t, resp.ErrMsg, "batch_discount")
		})
	}
}

// ---------- MPB-1-004 InnerAPI 导出：batch_discount 展开 ----------

// TestBatchPrice_ExportExpansion 验证携带 batch_discount 的 chat 行在
// ModelTable 导出中展开为 mode=batch 行，展开价 = 基价 × discount
// （8 位小数定点舍入）；未配置 discount 的行无展开行。
func TestBatchPrice_ExportExpansion(t *testing.T) {
	provider := testutil.UniqueName("bp-provider")
	discountModel := testutil.UniqueName("bp-disc-model")
	plainModel := testutil.UniqueName("bp-plain-model")

	// 显式创建 provider 且 models 覆盖被测模型（cluster 导出要求
	// llm 模型为 provider models 子集，参照 MP-5-004 配方）。
	_, err := testutil.CreateProvider(provider, map[string]interface{}{
		"models": []string{discountModel, plainModel},
	})
	require.NoError(t, err)
	defer testutil.DeleteProvider(provider)

	const (
		baseInput  = 0.000002 // 2e-6
		baseOutput = 0.000008 // 8e-6
		discount   = 0.5
	)
	id1, err := testutil.CreateModelPrice(map[string]interface{}{
		"provider":       provider,
		"model":          discountModel,
		"base_model":     discountModel,
		"mode":           "chat",
		"batch_discount": discount,
		"prices": map[string]interface{}{
			"input_cost_per_token":  baseInput,
			"output_cost_per_token": baseOutput,
		},
	})
	require.NoError(t, err)
	defer testutil.DeleteModelPrice(id1)

	id2, err := testutil.CreateModelPrice(map[string]interface{}{
		"provider":   provider,
		"model":      plainModel,
		"base_model": plainModel,
		"mode":       "chat",
		"prices": map[string]interface{}{
			"input_cost_per_token": baseInput,
		},
	})
	require.NoError(t, err)
	defer testutil.DeleteModelPrice(id2)

	// 创建引用该 provider 与模型的 cluster，使 AIConf.ModelTable 导出
	clusterName := testutil.UniqueClusterName()
	resp, err := testutil.GetClient().Post("/open-api/v1/clusters", map[string]interface{}{
		"name": clusterName,
		"llm_config": map[string]interface{}{
			"models":   []string{discountModel, plainModel},
			"provider": provider,
		},
	})
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)
	defer testutil.DeleteCluster(clusterName)

	export, err := testutil.GetClient().Get("/inner-api/v1/configs/tls_conf/server_data_conf")
	require.NoError(t, err)
	testutil.AssertSuccess(t, export)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(export.Data, &data))
	config := data["ClusterConf"].(map[string]interface{})["Config"].(map[string]interface{})
	cluster, ok := config[clusterName].(map[string]interface{})
	require.True(t, ok, "cluster %s should exist in export", clusterName)
	aiConf, ok := cluster["AIConf"].(map[string]interface{})
	require.True(t, ok, "AIConf should exist")
	table, ok := aiConf["ModelTable"].(map[string]interface{})
	require.True(t, ok, "ModelTable should be exported for priced cluster")
	models := table["Models"].([]interface{})

	// 定点比较：展开价 = round(基价 × discount × 1e8) / 1e8（同
	// batchPricePrecision=1e8 口径；此处基价 ×0.5 为精确二进制值，
	// 舍入不改变结果，定点公式与实现对任意取值一致）。
	fixed := func(base float64) float64 {
		return math.Round(base*discount*1e8) / 1e8
	}

	byModelMode := map[string]map[string]interface{}{}
	for _, item := range models {
		entry := item.(map[string]interface{})
		key := fmt.Sprintf("%s|%s", entry["Model"], entry["Mode"])
		byModelMode[key] = entry
	}

	// 1) chat 原行保留，价格不折扣
	chatRow, ok := byModelMode[discountModel+"|chat"]
	require.True(t, ok, "chat row must exist for %s", discountModel)
	chatPrices := chatRow["Prices"].(map[string]interface{})
	assert.InDelta(t, baseInput, chatPrices["input_cost_per_token"], 1e-12)

	// 2) 展开 mode=batch 行存在且价格 = 基价 × 0.5（1e-8 定点）
	batchRow, ok := byModelMode[discountModel+"|batch"]
	require.True(t, ok, "expanded mode=batch row must exist for discounted model %s", discountModel)
	batchPrices := batchRow["Prices"].(map[string]interface{})
	assert.InDelta(t, fixed(baseInput), batchPrices["input_cost_per_token"], 1e-12)
	assert.InDelta(t, fixed(baseOutput), batchPrices["output_cost_per_token"], 1e-12)

	// 3) 未配置 discount 的行无展开行
	_, ok = byModelMode[plainModel+"|batch"]
	assert.False(t, ok, "no expanded batch row for model without batch_discount")
	plainRow, ok := byModelMode[plainModel+"|chat"]
	require.True(t, ok, "chat row must exist for %s", plainModel)
	plainPrices := plainRow["Prices"].(map[string]interface{})
	assert.InDelta(t, baseInput, plainPrices["input_cost_per_token"], 1e-12)
}
