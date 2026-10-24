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

package rate_limit_policy

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/shared"
)

// TPMConfig 定义 TPM 限制配置
type TPMConfig struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	WindowMinutes int    `json:"window_minutes"`
	MaxTokens     int    `json:"max_tokens"`
	StepMinutes   int    `json:"step_minutes"`
}

// RPMConfig 定义 RPM 限制配置
type RPMConfig struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	WindowMinutes int    `json:"window_minutes"`
	MaxRequests   int    `json:"max_requests"`
}

// BatchLimits 定义批量限流维度（与 model/shared.BatchLimits 同构；
// 省略/null = 该策略不参与批量限流，0 = 不配此维度）
type BatchLimits struct {
	MaxCreateRPM     int   `json:"max_create_rpm"`
	MaxActiveBatches int   `json:"max_active_batches"`
	MaxFileBytes     int64 `json:"max_file_bytes"`
	MaxFileLines     int   `json:"max_file_lines"`
}

// RateLimitPolicyParam 定义限流策略参数
type RateLimitPolicyParam struct {
	ID             *int64       `json:"id"`
	Enabled        *bool        `json:"enabled"`
	MaxConcurrency *int         `json:"max_concurrency"`
	TpmConfigs     []TPMConfig  `json:"tpm_configs"`
	RpmConfigs     []RPMConfig  `json:"rpm_configs"`
	BatchLimits    *BatchLimits `json:"batch_limits,omitempty"`
}

// RateLimitPolicyFilter 定义限流策略过滤条件
type RateLimitPolicyFilter struct {
	ID *int64
}

// RateLimitPolicyStorager 定义限流策略存储接口
type RateLimitPolicyStorager interface {
	CreateRateLimitPolicy(ctx context.Context, param *RateLimitPolicyParam) (int64, error)
	FetchRateLimitPolicy(ctx context.Context, filter *RateLimitPolicyFilter) (*RateLimitPolicyParam, error)
	FetchRateLimitPolicyList(ctx context.Context, filter *RateLimitPolicyFilter) ([]*RateLimitPolicyParam, error)
	UpdateRateLimitPolicy(ctx context.Context, filter *RateLimitPolicyFilter, param *RateLimitPolicyParam) (int64, error)
	DeleteRateLimitPolicy(ctx context.Context, filter *RateLimitPolicyFilter) error
}

var _ shared.RateLimitPolicyStorager = (*rateLimitPolicyStoragerAdapter)(nil)

type rateLimitPolicyStoragerAdapter struct {
	storager RateLimitPolicyStorager
}

func (a *rateLimitPolicyStoragerAdapter) CreateRateLimitPolicy(ctx context.Context, param *shared.RateLimitPolicyParam) (int64, error) {
	var tpmConfigs []TPMConfig
	var rpmConfigs []RPMConfig
	var maxConcurrency *int
	var batchLimits *BatchLimits

	if param.Rules != nil {
		tpmConfigs = make([]TPMConfig, 0, len(param.Rules.TpmConfigs))
		for _, c := range param.Rules.TpmConfigs {
			tpmConfigs = append(tpmConfigs, TPMConfig{
				Name:          c.Name,
				Model:         c.Model,
				WindowMinutes: c.WindowMinutes,
				MaxTokens:     c.MaxTokens,
				StepMinutes:   c.StepMinutes,
			})
		}
		rpmConfigs = make([]RPMConfig, 0, len(param.Rules.RpmConfigs))
		for _, c := range param.Rules.RpmConfigs {
			rpmConfigs = append(rpmConfigs, RPMConfig{
				Name:          c.Name,
				Model:         c.Model,
				WindowMinutes: c.WindowMinutes,
				MaxRequests:   c.MaxRequests,
			})
		}
		maxConcurrency = param.Rules.MaxConcurrency
		batchLimits = batchLimitsFromShared(param.Rules.BatchLimits)
	}

	return a.storager.CreateRateLimitPolicy(ctx, &RateLimitPolicyParam{
		Enabled:        param.Enabled,
		MaxConcurrency: maxConcurrency,
		TpmConfigs:     tpmConfigs,
		RpmConfigs:     rpmConfigs,
		BatchLimits:    batchLimits,
	})
}

func (a *rateLimitPolicyStoragerAdapter) UpdateRateLimitPolicy(ctx context.Context, id int64, param *shared.RateLimitPolicyParam) (int64, error) {
	var tpmConfigs []TPMConfig
	var rpmConfigs []RPMConfig
	var maxConcurrency *int
	var batchLimits *BatchLimits

	if param.Rules != nil {
		tpmConfigs = make([]TPMConfig, 0, len(param.Rules.TpmConfigs))
		for _, c := range param.Rules.TpmConfigs {
			tpmConfigs = append(tpmConfigs, TPMConfig{
				Name:          c.Name,
				Model:         c.Model,
				WindowMinutes: c.WindowMinutes,
				MaxTokens:     c.MaxTokens,
				StepMinutes:   c.StepMinutes,
			})
		}
		rpmConfigs = make([]RPMConfig, 0, len(param.Rules.RpmConfigs))
		for _, c := range param.Rules.RpmConfigs {
			rpmConfigs = append(rpmConfigs, RPMConfig{
				Name:          c.Name,
				Model:         c.Model,
				WindowMinutes: c.WindowMinutes,
				MaxRequests:   c.MaxRequests,
			})
		}
		maxConcurrency = param.Rules.MaxConcurrency
		batchLimits = batchLimitsFromShared(param.Rules.BatchLimits)
	}

	return a.storager.UpdateRateLimitPolicy(ctx, &RateLimitPolicyFilter{ID: &id}, &RateLimitPolicyParam{
		Enabled:        param.Enabled,
		MaxConcurrency: maxConcurrency,
		TpmConfigs:     tpmConfigs,
		RpmConfigs:     rpmConfigs,
		BatchLimits:    batchLimits,
	})
}

func (a *rateLimitPolicyStoragerAdapter) DeleteRateLimitPolicy(ctx context.Context, id int64) error {
	return a.storager.DeleteRateLimitPolicy(ctx, &RateLimitPolicyFilter{ID: &id})
}

func (a *rateLimitPolicyStoragerAdapter) FetchRateLimitPolicy(ctx context.Context, id int64) (*shared.RateLimitPolicyParam, error) {
	result, err := a.storager.FetchRateLimitPolicy(ctx, &RateLimitPolicyFilter{ID: &id})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	tpmConfigs := make([]shared.TPMConfig, 0, len(result.TpmConfigs))
	for _, c := range result.TpmConfigs {
		tpmConfigs = append(tpmConfigs, shared.TPMConfig{
			Name:          c.Name,
			Model:         c.Model,
			WindowMinutes: c.WindowMinutes,
			MaxTokens:     c.MaxTokens,
			StepMinutes:   c.StepMinutes,
		})
	}
	rpmConfigs := make([]shared.RPMConfig, 0, len(result.RpmConfigs))
	for _, c := range result.RpmConfigs {
		rpmConfigs = append(rpmConfigs, shared.RPMConfig{
			Name:          c.Name,
			Model:         c.Model,
			WindowMinutes: c.WindowMinutes,
			MaxRequests:   c.MaxRequests,
		})
	}
	return &shared.RateLimitPolicyParam{
		Enabled: result.Enabled,
		Rules: &shared.RateLimitRules{
			TpmConfigs:     tpmConfigs,
			RpmConfigs:     rpmConfigs,
			MaxConcurrency: result.MaxConcurrency,
			BatchLimits:    batchLimitsToShared(result.BatchLimits),
		},
	}, nil
}

func batchLimitsFromShared(src *shared.BatchLimits) *BatchLimits {
	if src == nil {
		return nil
	}
	return &BatchLimits{
		MaxCreateRPM:     src.MaxCreateRPM,
		MaxActiveBatches: src.MaxActiveBatches,
		MaxFileBytes:     src.MaxFileBytes,
		MaxFileLines:     src.MaxFileLines,
	}
}

func batchLimitsToShared(src *BatchLimits) *shared.BatchLimits {
	if src == nil {
		return nil
	}
	return &shared.BatchLimits{
		MaxCreateRPM:     src.MaxCreateRPM,
		MaxActiveBatches: src.MaxActiveBatches,
		MaxFileBytes:     src.MaxFileBytes,
		MaxFileLines:     src.MaxFileLines,
	}
}

func NewRateLimitPolicyStoragerAdapter(storager RateLimitPolicyStorager) shared.RateLimitPolicyStorager {
	return &rateLimitPolicyStoragerAdapter{storager: storager}
}

// ExportRateLimitRules 定义导出的限流规则
type ExportRateLimitRules struct {
	TPM            []ExportTPMConfig  `json:"tpm"`
	RPM            []ExportRPMConfig  `json:"rpm"`
	MaxConcurrency int                `json:"max_concurrency"`
	Batch          *ExportBatchLimits `json:"batch,omitempty"`
}

// ExportBatchLimits 定义导出的批量限流配置（源：batch_limits；为空省略）。
// 四个维度均按 apikey 计、与 model 无关（批量创建请求体无 model 字段）；
// 多策略组合由数据面逐策略 AND 等效取 min。max_create_rpm 需要独立计数
// Redis Key；max_active_batches 复用数据面共享的 BATCH_ACTIVE:<api_key_id>
// ZSET 基数、不重复配 key；两个文件上限为请求内本地校验、无 key。
type ExportBatchLimits struct {
	MaxCreateRPM     int    `json:"max_create_rpm,omitempty"`
	MaxActiveBatches int    `json:"max_active_batches,omitempty"`
	MaxFileBytes     int64  `json:"max_file_bytes,omitempty"`
	MaxFileLines     int    `json:"max_file_lines,omitempty"`
	RedisKey         string `json:"redis_key,omitempty"`
}

// exportBatchLimits 将策略的批量限流配置转换为导出段。仅 max_create_rpm>0
// 时生成计数 Redis Key（生成惯例同 RL_TPM/RL_RPM）；文件上限与在途上限无 Key。
func exportBatchLimits(policyID int64, src *BatchLimits) *ExportBatchLimits {
	if src == nil {
		return nil
	}
	rst := &ExportBatchLimits{
		MaxCreateRPM:     src.MaxCreateRPM,
		MaxActiveBatches: src.MaxActiveBatches,
		MaxFileBytes:     src.MaxFileBytes,
		MaxFileLines:     src.MaxFileLines,
	}
	if src.MaxCreateRPM > 0 {
		rst.RedisKey = shared.BuildBatchRateLimitRedisKey(policyID)
	}
	return rst
}

// ExportRateLimitPolicy 定义导出到 BFE 的限流策略结构
type ExportRateLimitPolicy struct {
	Name    string                `json:"name"`
	Enabled bool                  `json:"enabled"`
	Rules   *ExportRateLimitRules `json:"rules"`
}

// ExportTPMConfig 定义导出的 TPM 配置
type ExportTPMConfig struct {
	Name          string   `json:"name"`
	Models        []string `json:"models"`
	WindowMinutes int      `json:"window_minutes"`
	MaxTokens     int      `json:"max_tokens"`
	StepMinutes   int      `json:"step_minutes"`
	RedisKey      string   `json:"redis_key"`
}

// ExportRPMConfig 定义导出的 RPM 配置
type ExportRPMConfig struct {
	Name          string   `json:"name"`
	Models        []string `json:"models"`
	WindowMinutes int      `json:"window_minutes"`
	MaxRequests   int      `json:"max_requests"`
	Burst         int      `json:"burst"`
	RedisKey      string   `json:"redis_key"`
}

// ExportRateLimitPolicyConfig 定义导出的限流策略配置
type ExportRateLimitPolicyConfig struct {
	Config                        map[string][]*ExportRouteRule     `json:"Config"`
	RateLimitPolicies             map[string]*ExportRateLimitPolicy `json:"RateLimitPolicies"`
	ApikeyRateLimitPolicyBindings map[string][]string               `json:"ApikeyRateLimitPolicyBindings"`
	Version                       string                            `json:"Version"`
}

func (conf *ExportRateLimitPolicyConfig) UpdateVersion(version string) error {
	conf.Version = version
	return nil
}

// ExportRouteRule 定义导出的路由规则
type ExportRouteRule struct {
	Cond      string           `json:"cond"`
	HitAction *ExportHitAction `json:"hit_action"`
}

// ExportHitAction 定义导出的命中动作
type ExportHitAction struct {
	Cmd    string   `json:"cmd"`
	Params []string `json:"params"`
}
