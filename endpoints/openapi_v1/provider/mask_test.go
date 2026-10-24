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

package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
)

func TestMaskKeysIfReadonly(t *testing.T) {
	newProviders := func() []*iprovider.Provider {
		return []*iprovider.Provider{
			{Keys: []iprovider.ProviderKey{{Name: "k1", Key: "sk-aaaaaaaaaaaa"}, {Name: "k2", Key: "short"}}},
		}
	}

	t.Run("readonly masks and keeps original slice intact", func(t *testing.T) {
		p := newProviders()[0]
		original := p.Keys
		ctx := iauth.NewVisitorContext(context.Background(), &iauth.Visitor{
			User: &iauth.User{Name: "viewer", Scopes: []string{iauth.ScopeSystemReadonly}},
		})

		require.NoError(t, maskKeysIfReadonly(ctx, p))
		assert.Equal(t, "sk-a********aaaa", p.Keys[0].Key)
		assert.Equal(t, "sh****rt", p.Keys[1].Key)
		// The shared backing array must not be written back.
		assert.Equal(t, "sk-aaaaaaaaaaaa", original[0].Key)
	})

	t.Run("admin untouched", func(t *testing.T) {
		p := newProviders()[0]
		ctx := iauth.NewVisitorContext(context.Background(), &iauth.Visitor{
			User: &iauth.User{Name: "admin", Admin: true},
		})

		require.NoError(t, maskKeysIfReadonly(ctx, p))
		assert.Equal(t, "sk-aaaaaaaaaaaa", p.Keys[0].Key)
	})

	t.Run("support token untouched", func(t *testing.T) {
		p := newProviders()[0]
		ctx := iauth.NewVisitorContext(context.Background(), &iauth.Visitor{
			Token: &iauth.Token{Name: "t", Scope: iauth.ScopeSupport},
		})

		require.NoError(t, maskKeysIfReadonly(ctx, p))
		assert.Equal(t, "sk-aaaaaaaaaaaa", p.Keys[0].Key)
	})
}
