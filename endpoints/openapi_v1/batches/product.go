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
//limitations under the License. All rights reserved.

package batches

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibasic"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// resolveProduct 解析本组端点的产品线：优先取 McProductProbe 已注入的
// 上下文（请求路径携带 product_id/product_name 时）；本组路由自身不
// 含产品路径变量，ProductProbe 无从注入，故按配置的默认产品线
// AIRouteInnerProductName 解析（与 ai_route 等模块的 default_product.go
// 同模式）。Token 不绑定产品线（2026-07-27 权限模型收敛决策，见
// design-docs/modifications/2026-07-27-openapi-optimize/design-changes.md
// §2.4），不存在凭证→产品线解析；对外口径见 batches.md §1/§6
//（issue #276，2026-10-23 校准）。
func resolveProduct(ctx context.Context) (*ibasic.Product, error) {
	if product, err := ibasic.MustGetProduct(ctx); err == nil {
		return product, nil
	}

	name := stateful.DefaultConfig.RunTime.AIRouteInnerProductName
	products, err := container.ProductManager.FetchProducts(ctx, &ibasic.ProductFilter{Name: &name})
	if err != nil {
		return nil, err
	}
	if len(products) != 1 {
		return nil, xerror.WrapParamErrorWithMsg("Default Product Not Exist")
	}
	return products[0], nil
}
