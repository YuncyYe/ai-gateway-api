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

package epp_pool

import (
	"testing"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEppConfig(t *testing.T) {
	t.Run("empty returns nil", func(t *testing.T) {
		conf, err := ParseEppConfig("")
		require.NoError(t, err)
		assert.Nil(t, conf)
	})

	t.Run("full fields", func(t *testing.T) {
		conf, err := ParseEppConfig(`{
			"load_profile": "kv-first",
			"affinity": "high",
			"prefix_cache_affinity": false,
			"session_affinity_enabled": true,
			"session_affinity_header": "x-session-id",
			"kv_cache_utilization_max": 0.8,
			"waiting_queue_max": 5,
			"running_requests_max": 16,
			"fallback_on_empty": true,
			"metrics_staleness_threshold_ms": 300,
			"flow_control": {"max_requests": 100, "queue_ttl": 30}
		}`)
		require.NoError(t, err)
		require.NotNil(t, conf)
		assert.Equal(t, LoadProfileKVFirst, *conf.LoadProfile)
		assert.Equal(t, AffinityHigh, *conf.Affinity)
		require.NotNil(t, conf.PrefixCacheAffinity)
		assert.False(t, *conf.PrefixCacheAffinity)
		require.NotNil(t, conf.SessionAffinityEnabled)
		assert.True(t, *conf.SessionAffinityEnabled)
		assert.Equal(t, "x-session-id", *conf.SessionAffinityHeader)
		require.NotNil(t, conf.KVCacheUtilizationMax)
		assert.Equal(t, 0.8, *conf.KVCacheUtilizationMax)
		require.NotNil(t, conf.WaitingQueueMax)
		assert.Equal(t, float64(5), *conf.WaitingQueueMax)
		require.NotNil(t, conf.RunningRequestsMax)
		assert.Equal(t, float64(16), *conf.RunningRequestsMax)
		require.NotNil(t, conf.FallbackOnEmpty)
		assert.True(t, *conf.FallbackOnEmpty)
		require.NotNil(t, conf.MetricsStalenessThresholdMs)
		assert.Equal(t, 300, *conf.MetricsStalenessThresholdMs)
		require.NotNil(t, conf.FlowControl)
		assert.Equal(t, 100, *conf.FlowControl.MaxRequests)
		assert.Equal(t, 30, *conf.FlowControl.QueueTTL)
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		_, err := ParseEppConfig(`{"unknown_field": true}`)
		require.Error(t, err)
	})

	t.Run("removed legacy fields rejected", func(t *testing.T) {
		_, err := ParseEppConfig(`{"scheduling_profile": "balanced"}`)
		require.Error(t, err)

		_, err = ParseEppConfig(`{"cache_affinity": "low"}`)
		require.Error(t, err)
	})

	t.Run("invalid json rejected", func(t *testing.T) {
		_, err := ParseEppConfig(`{`)
		require.Error(t, err)
	})
}

func TestEppConfigSimplified_Validate(t *testing.T) {
	t.Run("nil is valid", func(t *testing.T) {
		var conf *EppConfigSimplified
		require.NoError(t, conf.Validate())
	})

	t.Run("valid full config", func(t *testing.T) {
		conf := &EppConfigSimplified{
			LoadProfile:                 lib.PString(LoadProfileQueueFirst),
			Affinity:                    lib.PString(AffinityLow),
			PrefixCacheAffinity:         lib.PBool(true),
			SessionAffinityEnabled:      lib.PBool(true),
			SessionAffinityHeader:       lib.PString("x-session-id"),
			KVCacheUtilizationMax:       float64Ptr(0.9),
			WaitingQueueMax:             float64Ptr(0),
			RunningRequestsMax:          float64Ptr(0),
			FallbackOnEmpty:             lib.PBool(false),
			MetricsStalenessThresholdMs: lib.PInt(200),
			FlowControl: &FlowControlSimplified{
				MaxRequests:        lib.PInt(100),
				QueueTTL:           lib.PInt(0),
				NoEndpointQueueTTL: lib.PInt(600),
				EnableEviction:     lib.PBool(false),
			},
		}
		require.NoError(t, conf.Validate())
	})

	t.Run("load_profile enum", func(t *testing.T) {
		conf := &EppConfigSimplified{LoadProfile: lib.PString("bogus")}
		require.Error(t, conf.Validate())
	})

	t.Run("affinity enum", func(t *testing.T) {
		conf := &EppConfigSimplified{Affinity: lib.PString("bogus")}
		require.Error(t, conf.Validate())
	})

	t.Run("kv_cache_utilization_max range", func(t *testing.T) {
		conf := &EppConfigSimplified{KVCacheUtilizationMax: float64Ptr(0)}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{KVCacheUtilizationMax: float64Ptr(1.01)}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{KVCacheUtilizationMax: float64Ptr(1)}
		require.NoError(t, conf.Validate())
	})

	t.Run("waiting/running thresholds", func(t *testing.T) {
		conf := &EppConfigSimplified{WaitingQueueMax: float64Ptr(-1)}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{RunningRequestsMax: float64Ptr(-1)}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{WaitingQueueMax: float64Ptr(0), RunningRequestsMax: float64Ptr(0)}
		require.NoError(t, conf.Validate())
	})

	t.Run("metrics_staleness_threshold_ms range", func(t *testing.T) {
		conf := &EppConfigSimplified{MetricsStalenessThresholdMs: lib.PInt(0)}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{MetricsStalenessThresholdMs: lib.PInt(-1)}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{MetricsStalenessThresholdMs: lib.PInt(200)}
		require.NoError(t, conf.Validate())
	})

	t.Run("enable_eviction must be false", func(t *testing.T) {
		conf := &EppConfigSimplified{FlowControl: &FlowControlSimplified{EnableEviction: lib.PBool(true)}}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{FlowControl: &FlowControlSimplified{EnableEviction: lib.PBool(false)}}
		require.NoError(t, conf.Validate())
	})

	t.Run("session header required when enabled", func(t *testing.T) {
		conf := &EppConfigSimplified{SessionAffinityEnabled: lib.PBool(true)}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{SessionAffinityEnabled: lib.PBool(true), SessionAffinityHeader: lib.PString("")}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{SessionAffinityEnabled: lib.PBool(true), SessionAffinityHeader: lib.PString("x-session-id")}
		require.NoError(t, conf.Validate())
	})

	t.Run("session header format checked when disabled", func(t *testing.T) {
		conf := &EppConfigSimplified{SessionAffinityHeader: lib.PString("bad header")}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{SessionAffinityHeader: lib.PString("x-session-id")}
		require.NoError(t, conf.Validate())
	})

	t.Run("flow_control max_requests", func(t *testing.T) {
		conf := &EppConfigSimplified{FlowControl: &FlowControlSimplified{MaxRequests: lib.PInt(0)}}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{FlowControl: &FlowControlSimplified{MaxRequests: lib.PInt(-1)}}
		require.NoError(t, conf.Validate())

		conf = &EppConfigSimplified{FlowControl: &FlowControlSimplified{MaxRequests: lib.PInt(1)}}
		require.NoError(t, conf.Validate())
	})

	t.Run("flow_control ttl", func(t *testing.T) {
		conf := &EppConfigSimplified{FlowControl: &FlowControlSimplified{QueueTTL: lib.PInt(-1)}}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{FlowControl: &FlowControlSimplified{NoEndpointQueueTTL: lib.PInt(-1)}}
		require.Error(t, conf.Validate())

		conf = &EppConfigSimplified{FlowControl: &FlowControlSimplified{QueueTTL: lib.PInt(0)}}
		require.NoError(t, conf.Validate())
	})
}

func TestValidateHost(t *testing.T) {
	valid := []string{"10.0.0.1", "epp-0", "epp-0.epp-headless", "2001:db8::1", "a.b-c.d"}
	for _, host := range valid {
		require.NoError(t, validateHost(host), host)
	}

	invalid := []string{"", "-bad", "bad..host", "2001:db8::1]", "a" + string(make([]byte, 300))}
	for _, host := range invalid {
		require.Error(t, validateHost(host), host)
	}
}

func TestValidatePort(t *testing.T) {
	require.NoError(t, validatePort(1))
	require.NoError(t, validatePort(65535))
	require.Error(t, validatePort(0))
	require.Error(t, validatePort(65536))
}

func TestEppConfigSimplified_IsEmpty(t *testing.T) {
	assert.True(t, (*EppConfigSimplified)(nil).IsEmpty())
	assert.True(t, (&EppConfigSimplified{}).IsEmpty())

	conf, err := ParseEppConfig(`{}`)
	require.NoError(t, err)
	require.NotNil(t, conf)
	assert.True(t, conf.IsEmpty())

	conf, err = ParseEppConfig(`{"load_profile":"balanced"}`)
	require.NoError(t, err)
	assert.False(t, conf.IsEmpty())

	conf, err = ParseEppConfig(`{"affinity":"off"}`)
	require.NoError(t, err)
	assert.False(t, conf.IsEmpty())

	conf, err = ParseEppConfig(`{"waiting_queue_max":0}`)
	require.NoError(t, err)
	assert.False(t, conf.IsEmpty())

	conf, err = ParseEppConfig(`{"flow_control":{"enable_eviction":false}}`)
	require.NoError(t, err)
	assert.False(t, conf.IsEmpty())
}
