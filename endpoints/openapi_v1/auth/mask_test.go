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

// Copyright (c) 2021 The BFE Authors.
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

package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
)

func TestMaskTokensIfReadonly(t *testing.T) {
	t.Run("readonly masks", func(t *testing.T) {
		ctx := iauth.NewVisitorContext(context.Background(), &iauth.Visitor{
			User: &iauth.User{Name: "viewer", Scopes: []string{iauth.ScopeSystemReadonly}},
		})
		data := []*TokenData{{Name: "t1", Token: "Px2szn6R1HQo-WRSIJyt", Scope: iauth.ScopeSystemReadonly}}

		require.NoError(t, maskTokensIfReadonly(ctx, data...))
		assert.Equal(t, "Px2s********IJyt", data[0].Token)
	})

	t.Run("admin untouched", func(t *testing.T) {
		ctx := iauth.NewVisitorContext(context.Background(), &iauth.Visitor{
			User: &iauth.User{Name: "admin", Admin: true},
		})
		data := []*TokenData{{Name: "t1", Token: "Px2szn6R1HQo-WRSIJyt", Scope: iauth.ScopeSystem}}

		require.NoError(t, maskTokensIfReadonly(ctx, data...))
		assert.Equal(t, "Px2szn6R1HQo-WRSIJyt", data[0].Token)
	})
}
