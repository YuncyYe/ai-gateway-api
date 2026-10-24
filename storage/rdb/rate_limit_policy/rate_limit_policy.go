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
	"encoding/json"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/rate_limit_policy"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

type RateLimitPolicyStorager struct {
	dbCtxFactory lib.DBContextFactory
}

func NewRateLimitPolicyStorager(dbCtxFactory lib.DBContextFactory) *RateLimitPolicyStorager {
	return &RateLimitPolicyStorager{
		dbCtxFactory: dbCtxFactory,
	}
}

var _ rate_limit_policy.RateLimitPolicyStorager = &RateLimitPolicyStorager{}

func (s *RateLimitPolicyStorager) CreateRateLimitPolicy(ctx context.Context, param *rate_limit_policy.RateLimitPolicyParam) (int64, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return 0, err
	}

	data := rateLimitPolicyDataToParam(param)
	data.CreatedAt = lib.PTimeNow()

	return dao.TRateLimitPolicyCreate(dbCtx, data)
}

func (s *RateLimitPolicyStorager) FetchRateLimitPolicy(ctx context.Context, filter *rate_limit_policy.RateLimitPolicyFilter) (*rate_limit_policy.RateLimitPolicyParam, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}

	where := rateLimitPolicyFilterToParam(filter)
	one, err := dao.TRateLimitPolicyOne(dbCtx, where)
	if err != nil {
		return nil, err
	}
	if one == nil {
		return nil, nil
	}

	return rateLimitPolicyParamToData(one), nil
}

func (s *RateLimitPolicyStorager) FetchRateLimitPolicyList(ctx context.Context, filter *rate_limit_policy.RateLimitPolicyFilter) ([]*rate_limit_policy.RateLimitPolicyParam, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}

	where := rateLimitPolicyFilterToParam(filter)
	list, err := dao.TRateLimitPolicyList(dbCtx, where)
	if err != nil {
		return nil, err
	}

	var rst []*rate_limit_policy.RateLimitPolicyParam
	for _, one := range list {
		rst = append(rst, rateLimitPolicyParamToData(one))
	}

	return rst, nil
}

func (s *RateLimitPolicyStorager) UpdateRateLimitPolicy(ctx context.Context, filter *rate_limit_policy.RateLimitPolicyFilter, param *rate_limit_policy.RateLimitPolicyParam) (int64, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return 0, err
	}

	data := rateLimitPolicyDataToParam(param)
	data.UpdatedAt = lib.PTimeNow()

	return dao.TRateLimitPolicyUpdate(dbCtx, data, rateLimitPolicyFilterToParam(filter))
}

func (s *RateLimitPolicyStorager) DeleteRateLimitPolicy(ctx context.Context, filter *rate_limit_policy.RateLimitPolicyFilter) error {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return err
	}

	_, err = dao.TRateLimitPolicyDelete(dbCtx, rateLimitPolicyFilterToParam(filter))
	return err
}

func rateLimitPolicyFilterToParam(filter *rate_limit_policy.RateLimitPolicyFilter) *dao.TRateLimitPolicyParam {
	if filter == nil {
		return nil
	}

	return &dao.TRateLimitPolicyParam{
		ID: filter.ID,
	}
}

func rateLimitPolicyDataToParam(param *rate_limit_policy.RateLimitPolicyParam) *dao.TRateLimitPolicyParam {
	data := &dao.TRateLimitPolicyParam{
		Enabled:        param.Enabled,
		MaxConcurrency: param.MaxConcurrency,
	}

	// 转换 TpmConfigs 为 JSON 字符串
	if len(param.TpmConfigs) > 0 {
		tpmConfigsJSON, _ := json.Marshal(param.TpmConfigs)
		data.TpmConfigs = lib.PString(string(tpmConfigsJSON))
	} else {
		data.TpmConfigs = lib.PString("[]")
	}

	// 转换 RpmConfigs 为 JSON 字符串
	if len(param.RpmConfigs) > 0 {
		rpmConfigsJSON, _ := json.Marshal(param.RpmConfigs)
		data.RpmConfigs = lib.PString(string(rpmConfigsJSON))
	} else {
		data.RpmConfigs = lib.PString("[]")
	}

	// 转换 BatchLimits 为 JSON 字符串。nil 时写空串哨兵：更新为整体替换语义
	// （省略 batch_limits 等价于清空），读侧将空串视为未配置。
	if param.BatchLimits != nil {
		batchLimitsJSON, _ := json.Marshal(param.BatchLimits)
		data.BatchLimits = lib.PString(string(batchLimitsJSON))
	} else {
		data.BatchLimits = lib.PString("")
	}

	return data
}

func rateLimitPolicyParamToData(one *dao.TRateLimitPolicy) *rate_limit_policy.RateLimitPolicyParam {
	param := &rate_limit_policy.RateLimitPolicyParam{
		ID:             &one.ID,
		Enabled:        &one.Enabled,
		MaxConcurrency: &one.MaxConcurrency,
	}

	// 解析 TpmConfigs
	if one.TpmConfigs != "" {
		json.Unmarshal([]byte(one.TpmConfigs), &param.TpmConfigs)
	}

	// 解析 RpmConfigs
	if one.RpmConfigs != "" {
		json.Unmarshal([]byte(one.RpmConfigs), &param.RpmConfigs)
	}

	// 解析 BatchLimits（空串/null 视为未配置）
	if one.BatchLimits != "" && one.BatchLimits != "null" {
		var batchLimits rate_limit_policy.BatchLimits
		if err := json.Unmarshal([]byte(one.BatchLimits), &batchLimits); err == nil {
			param.BatchLimits = &batchLimits
		}
	}

	return param
}
