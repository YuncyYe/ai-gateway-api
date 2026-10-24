// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iprovider

import (
	"context"
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveInstancePool(t *testing.T) {
	assert.Nil(t, EffectiveInstancePool(nil))

	manual := []ProviderInstance{{Addr: "1.2.3.4", Port: 443, Weight: 100}}
	mirror := []ProviderInstance{{Addr: "10.0.0.1", Port: 8000, Weight: 100}}

	p := &Provider{InstanceSource: InstanceSourceInstancePool, InstancePool: manual}
	assert.Equal(t, manual, EffectiveInstancePool(p))

	// Empty/legacy source (rows written before instance_source existed)
	// resolve to instance_pool as well.
	p = &Provider{InstancePool: manual}
	assert.Equal(t, manual, EffectiveInstancePool(p))

	p = &Provider{InstanceSource: InstanceSourceK8sPool, InstancePool: manual, K8sInstancePool: mirror}
	assert.Equal(t, mirror, EffectiveInstancePool(p))

	// k8s_pool mode with an empty mirror yields a nil (empty) effective pool.
	p = &Provider{InstanceSource: InstanceSourceK8sPool, InstancePool: manual}
	assert.Empty(t, EffectiveInstancePool(p))
}

func TestK8sPoolName(t *testing.T) {
	valid := []string{"svc-a", "svc_a", "svc.a", "a", "A1-b2.c3_d4"}
	for _, s := range valid {
		require.NoError(t, K8sPoolName(s), "name %q should be valid", s)
	}

	invalid := []string{
		"",                     // empty
		"-abc", "abc-", "_abc", // leading/trailing '-', '_'
		".abc", "abc.", // leading/trailing '.'
		"ab cd", "ab\tcd", // whitespace
		"ab/cd", "ab:cd", // invalid characters
	}
	for _, s := range invalid {
		require.Error(t, K8sPoolName(s), "name %q should be invalid", s)
	}

	long := make([]byte, MaxK8sPoolNameLength+1)
	for i := range long {
		long[i] = 'a'
	}
	require.Error(t, K8sPoolName(string(long)))
}

func TestValidateProviderParam_InstanceSource(t *testing.T) {
	base := func() *ProviderParam {
		return validProviderParam()
	}

	t.Run("default source keeps legacy instance_pool rules", func(t *testing.T) {
		require.NoError(t, ValidateProviderParam(base()))
	})

	t.Run("explicit instance_pool source", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceInstancePool)
		require.NoError(t, ValidateProviderParam(param))
	})

	t.Run("instance_pool mode rejects empty pool", func(t *testing.T) {
		param := base()
		param.InstancePool = nil
		require.Error(t, ValidateProviderParam(param))
	})

	t.Run("k8s_pool mode requires k8s_pool_name", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		err := ValidateProviderParam(param)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "k8s_pool_name is required")
	})

	t.Run("k8s_pool mode accepts empty k8s_pool_name rejection", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("")
		require.Error(t, ValidateProviderParam(param))
	})

	t.Run("k8s_pool mode allows empty dormant instance_pool", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("svc-a")
		param.InstancePool = nil
		require.NoError(t, ValidateProviderParam(param))
	})

	t.Run("k8s_pool mode does not validate dormant instance_pool members", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("svc-a")
		param.InstancePool = []ProviderInstance{
			{Addr: "", Port: 0, Weight: 200},
			{Addr: "", Port: 0, Weight: 200},
		}
		require.NoError(t, ValidateProviderParam(param))
	})

	t.Run("invalid instance_source enum rejected", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString("unknown")
		err := ValidateProviderParam(param)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid instance_source")
	})

	t.Run("k8s_instance_pool is read-only", func(t *testing.T) {
		param := base()
		param.K8sInstancePool = &[]ProviderInstance{{Addr: "10.0.0.1", Port: 8000, Weight: 100}}
		err := ValidateProviderParam(param)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})

	t.Run("k8s_instance_pool rejected in k8s_pool mode as well", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("svc-a")
		param.K8sInstancePool = &[]ProviderInstance{}
		require.Error(t, ValidateProviderParam(param))
	})

	t.Run("k8s_pool_name format enforced in k8s_pool mode", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("-bad-")
		require.Error(t, ValidateProviderParam(param))
	})

	t.Run("k8s_pool_name dormant but format-checked in instance_pool mode", func(t *testing.T) {
		param := base()
		param.InstanceSource = lib.PString(InstanceSourceInstancePool)
		param.K8sPoolName = lib.PString("svc-a")
		require.NoError(t, ValidateProviderParam(param))

		param.K8sPoolName = lib.PString("bad name")
		require.Error(t, ValidateProviderParam(param))
	})
}

func TestFillDefaults_InstanceSource(t *testing.T) {
	param := &ProviderParam{}
	FillDefaults(param)
	require.NotNil(t, param.InstanceSource)
	assert.Equal(t, InstanceSourceInstancePool, *param.InstanceSource)

	existing := InstanceSourceK8sPool
	param = &ProviderParam{InstanceSource: &existing}
	FillDefaults(param)
	assert.Equal(t, InstanceSourceK8sPool, *param.InstanceSource)
}

func TestApplyProviderUpdate_InstanceSource(t *testing.T) {
	existing := &Provider{
		Name:           "deepseek",
		InstanceSource: InstanceSourceInstancePool,
		InstancePool:   []ProviderInstance{{Addr: "1.2.3.4", Port: 443, Weight: 100}},
	}

	t.Run("nil new fields keep existing values", func(t *testing.T) {
		updated := applyProviderUpdate(existing, &ProviderParam{})
		assert.Equal(t, InstanceSourceInstancePool, updated.InstanceSource)
		assert.Nil(t, updated.K8sPoolName)
		assert.Nil(t, updated.K8sInstancePool)
	})

	t.Run("explicit fields applied", func(t *testing.T) {
		poolName := "svc-a"
		mirror := []ProviderInstance{{Addr: "10.0.0.1", Port: 8000, Weight: 100}}
		updated := applyProviderUpdate(existing, &ProviderParam{
			InstanceSource:  lib.PString(InstanceSourceK8sPool),
			K8sPoolName:     &poolName,
			K8sInstancePool: &mirror,
		})
		assert.Equal(t, InstanceSourceK8sPool, updated.InstanceSource)
		require.NotNil(t, updated.K8sPoolName)
		assert.Equal(t, "svc-a", *updated.K8sPoolName)
		assert.Equal(t, mirror, updated.K8sInstancePool)
	})
}

func TestProviderManager_UpdateProvider_InstanceSourceHook(t *testing.T) {
	ctx := context.Background()

	t.Run("mode switch to k8s_pool triggers hook via effective pool change", func(t *testing.T) {
		store := &fakeProviderStorager{
			fetchFn: func(ctx context.Context, filter *ProviderFilter) (*Provider, error) {
				return &Provider{
					Name:           "deepseek",
					InstanceSource: InstanceSourceInstancePool,
					InstancePool:   []ProviderInstance{{Addr: "1.2.3.4", Port: 443, Weight: 100}},
				}, nil
			},
		}
		m := NewProviderManager(&fakeTxn{}, store)

		hookCalled := 0
		var hookNew *Provider
		hook := func(ctx context.Context, oldProvider, newProvider *Provider) error {
			hookCalled++
			hookNew = newProvider
			return nil
		}

		param := validProviderParam()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("svc-a")
		param.InstancePool = nil
		err := m.UpdateProvider(ctx, "deepseek", param, hook)
		require.NoError(t, err)
		require.Equal(t, 1, hookCalled)
		require.NotNil(t, hookNew)
		assert.Equal(t, InstanceSourceK8sPool, hookNew.InstanceSource)
		assert.Empty(t, EffectiveInstancePool(hookNew))
	})

	t.Run("mode switch back to instance_pool triggers hook", func(t *testing.T) {
		store := &fakeProviderStorager{
			fetchFn: func(ctx context.Context, filter *ProviderFilter) (*Provider, error) {
				return &Provider{
					Name:            "deepseek",
					InstanceSource:  InstanceSourceK8sPool,
					K8sPoolName:     lib.PString("svc-a"),
					InstancePool:    []ProviderInstance{{Addr: "1.2.3.4", Port: 443, Weight: 100}},
					K8sInstancePool: []ProviderInstance{{Addr: "10.0.0.1", Port: 8000, Weight: 100}},
				}, nil
			},
		}
		m := NewProviderManager(&fakeTxn{}, store)

		hookCalled := 0
		hook := func(ctx context.Context, oldProvider, newProvider *Provider) error {
			hookCalled++
			return nil
		}

		param := validProviderParam()
		param.InstanceSource = lib.PString(InstanceSourceInstancePool)
		param.K8sPoolName = nil // name stays dormant on the stored record
		err := m.UpdateProvider(ctx, "deepseek", param, hook)
		require.NoError(t, err)
		assert.Equal(t, 1, hookCalled)
	})

	t.Run("dormant instance_pool change in k8s_pool mode does not trigger hook", func(t *testing.T) {
		mirror := []ProviderInstance{{Addr: "10.0.0.1", Port: 8000, Weight: 100}}
		param := validProviderParam()
		store := &fakeProviderStorager{
			fetchFn: func(ctx context.Context, filter *ProviderFilter) (*Provider, error) {
				return &Provider{
					Name:            "deepseek",
					Models:          param.Models,
					Keys:            param.Keys,
					InstanceSource:  InstanceSourceK8sPool,
					K8sPoolName:     lib.PString("svc-a"),
					InstancePool:    []ProviderInstance{{Addr: "1.2.3.4", Port: 443, Weight: 100}},
					K8sInstancePool: mirror,
					ModelProtocols:  param.ModelProtocols,
				}, nil
			},
		}
		m := NewProviderManager(&fakeTxn{}, store)

		hookCalled := 0
		hook := func(ctx context.Context, oldProvider, newProvider *Provider) error {
			hookCalled++
			return nil
		}

		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("svc-a")
		param.InstancePool = []ProviderInstance{{Addr: "5.6.7.8", Port: 443, Weight: 100}}
		err := m.UpdateProvider(ctx, "deepseek", param, hook)
		require.NoError(t, err)
		assert.Equal(t, 0, hookCalled)
	})
}

// TestProviderManager_UpdateProvider_MirrorLifecycle pins the mirror
// lifecycle edge: the read-only k8s_instance_pool mirror is cleared when the
// merged instance_source resolves to instance_pool, and left untouched
// otherwise (the /k8s_pools fan-out is the only writer that refreshes it).
func TestProviderManager_UpdateProvider_MirrorLifecycle(t *testing.T) {
	ctx := context.Background()

	newK8sProviderStore := func(updatedParam **ProviderParam) *fakeProviderStorager {
		return &fakeProviderStorager{
			fetchFn: func(ctx context.Context, filter *ProviderFilter) (*Provider, error) {
				return &Provider{
					Name:            "deepseek",
					InstanceSource:  InstanceSourceK8sPool,
					K8sPoolName:     lib.PString("svc-a"),
					InstancePool:    []ProviderInstance{{Addr: "1.2.3.4", Port: 443, Weight: 100}},
					K8sInstancePool: []ProviderInstance{{Addr: "10.0.0.1", Port: 8000, Weight: 100}},
					ModelProtocols:  []string{"openai"},
				}, nil
			},
			updateFn: func(ctx context.Context, name string, param *ProviderParam) error {
				*updatedParam = param
				return nil
			},
		}
	}

	t.Run("switch to instance_pool mode clears the synced mirror", func(t *testing.T) {
		var updatedParam *ProviderParam
		m := NewProviderManager(&fakeTxn{}, newK8sProviderStore(&updatedParam))

		param := validProviderParam()
		param.InstanceSource = lib.PString(InstanceSourceInstancePool)
		require.NoError(t, m.UpdateProvider(ctx, "deepseek", param))

		require.NotNil(t, updatedParam)
		require.NotNil(t, updatedParam.K8sInstancePool, "mirror must be explicitly cleared, not nil-skip")
		assert.Empty(t, *updatedParam.K8sInstancePool)
	})

	t.Run("k8s_pool mode update leaves the mirror for the fan-out", func(t *testing.T) {
		var updatedParam *ProviderParam
		m := NewProviderManager(&fakeTxn{}, newK8sProviderStore(&updatedParam))

		param := validProviderParam()
		param.InstanceSource = lib.PString(InstanceSourceK8sPool)
		param.K8sPoolName = lib.PString("svc-a")
		param.InstancePool = nil
		require.NoError(t, m.UpdateProvider(ctx, "deepseek", param))

		require.NotNil(t, updatedParam)
		assert.Nil(t, updatedParam.K8sInstancePool, "mirror refresh stays exclusive to the /k8s_pools fan-out")
	})
}
