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
	"bytes"
	"encoding/json"
	"net"
	"regexp"
	"strings"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
)

// Simplified epp_config field values and defaults (see api-changes.md §3.2).
const (
	LoadProfileQueueFirst = "queue-first"
	LoadProfileBalanced   = "balanced"
	LoadProfileKVFirst    = "kv-first"

	AffinityOff    = "off"
	AffinityLow    = "low"
	AffinityMedium = "medium"
	AffinityHigh   = "high"

	DefaultLoadProfile                 = LoadProfileBalanced
	DefaultAffinity                    = AffinityMedium
	DefaultPrefixCacheAffinity         = true
	DefaultKVCacheUtilizationMax       = 0.9
	DefaultWaitingQueueMax             = 0
	DefaultRunningRequestsMax          = 0
	DefaultFallbackOnEmpty             = false
	DefaultMetricsStalenessThresholdMs = 200
	DefaultSessionAffinityEnabled      = false

	// Detector internal fixed values (not exposed this round): stalenessPolicy
	// "ignore" avoids "monitoring jitter halts everything"; queue depth defaults
	// to 5 when waiting_queue_max is not enabled.
	defaultSaturationQueueDepthThreshold = 5
	stalenessPolicyIgnore                = "ignore"
)

// FlowControlUnlimited marks an explicit "no limit" max_requests.
const FlowControlUnlimited = -1

// EppConfigSimplified is the user-facing simplified shape of the per-cluster
// EPP scheduling configuration stored in clusters.epp_config. All fields are
// pointers so that "not set" (follow compiler defaults) is distinguishable
// from an explicit value; unset fields are not persisted.
type EppConfigSimplified struct {
	// ── ① 调度倾向 ──
	LoadProfile            *string `json:"load_profile,omitempty"`
	Affinity               *string `json:"affinity,omitempty"`
	PrefixCacheAffinity    *bool   `json:"prefix_cache_affinity,omitempty"`
	SessionAffinityEnabled *bool   `json:"session_affinity_enabled,omitempty"`
	SessionAffinityHeader  *string `json:"session_affinity_header,omitempty"`

	// ── ② 谁能接（准入 + 坏后端剔除） ──
	KVCacheUtilizationMax       *float64 `json:"kv_cache_utilization_max,omitempty"`
	WaitingQueueMax             *float64 `json:"waiting_queue_max,omitempty"`
	RunningRequestsMax          *float64 `json:"running_requests_max,omitempty"`
	FallbackOnEmpty             *bool    `json:"fallback_on_empty,omitempty"`
	MetricsStalenessThresholdMs *int     `json:"metrics_staleness_threshold_ms,omitempty"`

	// ── ③ 容量与排队 ──
	FlowControl *FlowControlSimplified `json:"flow_control,omitempty"`
}

// FlowControlSimplified is the simplified flow-control section of epp_config.
type FlowControlSimplified struct {
	MaxRequests        *int  `json:"max_requests,omitempty"`
	QueueTTL           *int  `json:"queue_ttl,omitempty"`
	NoEndpointQueueTTL *int  `json:"no_endpoint_queue_ttl,omitempty"`
	EnableEviction     *bool `json:"enable_eviction,omitempty"`
}

// ParseEppConfig decodes the raw stored JSON of epp_config with strict field
// checking. Empty input returns nil (no config).
func ParseEppConfig(raw string) (*EppConfigSimplified, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()

	conf := &EppConfigSimplified{}
	if err := decoder.Decode(conf); err != nil {
		return nil, xerror.WrapParamErrorWithMsg("epp_config is invalid: %s", err.Error())
	}

	return conf, nil
}

// IsEmpty reports whether no field of the simplified config is set (an empty
// JSON object {} decodes to an all-nil struct).
func (c *EppConfigSimplified) IsEmpty() bool {
	return c == nil ||
		(c.LoadProfile == nil &&
			c.Affinity == nil &&
			c.PrefixCacheAffinity == nil &&
			c.SessionAffinityEnabled == nil &&
			c.SessionAffinityHeader == nil &&
			c.KVCacheUtilizationMax == nil &&
			c.WaitingQueueMax == nil &&
			c.RunningRequestsMax == nil &&
			c.FallbackOnEmpty == nil &&
			c.MetricsStalenessThresholdMs == nil &&
			c.FlowControl == nil)
}

// Validate checks all field-level rules of the simplified epp_config. It is
// enforced whenever epp_config is non-empty, regardless of balance_mode.
func (c *EppConfigSimplified) Validate() error {
	if c == nil {
		return nil
	}

	if c.LoadProfile != nil {
		switch *c.LoadProfile {
		case LoadProfileQueueFirst, LoadProfileBalanced, LoadProfileKVFirst:
		default:
			return xerror.WrapParamErrorWithMsg("epp_config.load_profile must be one of %q/%q/%q, got %q",
				LoadProfileQueueFirst, LoadProfileBalanced, LoadProfileKVFirst, *c.LoadProfile)
		}
	}

	if c.Affinity != nil {
		switch *c.Affinity {
		case AffinityOff, AffinityLow, AffinityMedium, AffinityHigh:
		default:
			return xerror.WrapParamErrorWithMsg("epp_config.affinity must be one of %q/%q/%q/%q, got %q",
				AffinityOff, AffinityLow, AffinityMedium, AffinityHigh, *c.Affinity)
		}
	}

	if c.KVCacheUtilizationMax != nil {
		if *c.KVCacheUtilizationMax <= 0 || *c.KVCacheUtilizationMax > 1 {
			return xerror.WrapParamErrorWithMsg("epp_config.kv_cache_utilization_max must be in (0, 1], got %v", *c.KVCacheUtilizationMax)
		}
	}

	if c.WaitingQueueMax != nil && *c.WaitingQueueMax < 0 {
		return xerror.WrapParamErrorWithMsg("epp_config.waiting_queue_max must be >= 0, got %v", *c.WaitingQueueMax)
	}

	if c.RunningRequestsMax != nil && *c.RunningRequestsMax < 0 {
		return xerror.WrapParamErrorWithMsg("epp_config.running_requests_max must be >= 0, got %v", *c.RunningRequestsMax)
	}

	if c.MetricsStalenessThresholdMs != nil && *c.MetricsStalenessThresholdMs <= 0 {
		return xerror.WrapParamErrorWithMsg("epp_config.metrics_staleness_threshold_ms must be > 0, got %d", *c.MetricsStalenessThresholdMs)
	}

	if c.SessionAffinityEnabled != nil && *c.SessionAffinityEnabled {
		if c.SessionAffinityHeader == nil || strings.TrimSpace(*c.SessionAffinityHeader) == "" {
			return xerror.WrapParamErrorWithMsg("epp_config.session_affinity_header is required when session_affinity_enabled is true")
		}
	}

	if c.SessionAffinityHeader != nil {
		if err := validateHeaderName(*c.SessionAffinityHeader); err != nil {
			return xerror.WrapParamErrorWithMsg("epp_config.session_affinity_header: %s", err.Error())
		}
	}

	if c.FlowControl != nil {
		if err := c.FlowControl.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks the flow_control section.
func (f *FlowControlSimplified) Validate() error {
	if f == nil {
		return nil
	}

	if f.MaxRequests != nil && *f.MaxRequests != FlowControlUnlimited && *f.MaxRequests <= 0 {
		return xerror.WrapParamErrorWithMsg("epp_config.flow_control.max_requests must be > 0 or -1, got %d", *f.MaxRequests)
	}

	if f.QueueTTL != nil && *f.QueueTTL < 0 {
		return xerror.WrapParamErrorWithMsg("epp_config.flow_control.queue_ttl must be >= 0, got %d", *f.QueueTTL)
	}

	if f.NoEndpointQueueTTL != nil && *f.NoEndpointQueueTTL < 0 {
		return xerror.WrapParamErrorWithMsg("epp_config.flow_control.no_endpoint_queue_ttl must be >= 0, got %d", *f.NoEndpointQueueTTL)
	}

	// enable_eviction is not wired in EPP yet; keep it false (the compiled
	// output never emits it).
	if f.EnableEviction != nil && *f.EnableEviction {
		return xerror.WrapParamErrorWithMsg("epp_config.flow_control.enable_eviction is not supported, keep false")
	}

	return nil
}

// EffectiveLoadProfile returns the load profile value with default applied.
func (c *EppConfigSimplified) EffectiveLoadProfile() string {
	if c == nil || c.LoadProfile == nil {
		return DefaultLoadProfile
	}
	return *c.LoadProfile
}

// EffectiveAffinity returns the affinity strength with default applied.
func (c *EppConfigSimplified) EffectiveAffinity() string {
	if c == nil || c.Affinity == nil {
		return DefaultAffinity
	}
	return *c.Affinity
}

// EffectivePrefixCacheAffinity returns the prefix cache affinity switch with default applied.
func (c *EppConfigSimplified) EffectivePrefixCacheAffinity() bool {
	if c == nil || c.PrefixCacheAffinity == nil {
		return DefaultPrefixCacheAffinity
	}
	return *c.PrefixCacheAffinity
}

// EffectiveKVCacheUtilizationMax returns the utilization filter threshold with default applied.
func (c *EppConfigSimplified) EffectiveKVCacheUtilizationMax() float64 {
	if c == nil || c.KVCacheUtilizationMax == nil {
		return DefaultKVCacheUtilizationMax
	}
	return *c.KVCacheUtilizationMax
}

// EffectiveWaitingQueueMax returns the waiting-queue filter threshold with default applied.
func (c *EppConfigSimplified) EffectiveWaitingQueueMax() float64 {
	if c == nil || c.WaitingQueueMax == nil {
		return DefaultWaitingQueueMax
	}
	return *c.WaitingQueueMax
}

// EffectiveRunningRequestsMax returns the running-requests filter threshold with default applied.
func (c *EppConfigSimplified) EffectiveRunningRequestsMax() float64 {
	if c == nil || c.RunningRequestsMax == nil {
		return DefaultRunningRequestsMax
	}
	return *c.RunningRequestsMax
}

// EffectiveFallbackOnEmpty returns the filter fallback switch with default applied.
func (c *EppConfigSimplified) EffectiveFallbackOnEmpty() bool {
	if c == nil || c.FallbackOnEmpty == nil {
		return DefaultFallbackOnEmpty
	}
	return *c.FallbackOnEmpty
}

// EffectiveMetricsStalenessThresholdMs returns the bad-endpoint eviction threshold (ms).
func (c *EppConfigSimplified) EffectiveMetricsStalenessThresholdMs() int {
	if c == nil || c.MetricsStalenessThresholdMs == nil {
		return DefaultMetricsStalenessThresholdMs
	}
	return *c.MetricsStalenessThresholdMs
}

// EffectiveSessionAffinityEnabled returns the session affinity switch with default applied.
func (c *EppConfigSimplified) EffectiveSessionAffinityEnabled() bool {
	if c == nil || c.SessionAffinityEnabled == nil {
		return DefaultSessionAffinityEnabled
	}
	return *c.SessionAffinityEnabled
}

var (
	// hostnameLabel matches a single DNS label (RFC 1123) with length 1-63.
	hostnameLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
	// httpHeaderNameToken matches valid HTTP header name characters (RFC 7230 token).
	httpHeaderNameToken = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)
)

// validateHost checks that host is a hostname or an IP literal (IPv6 without
// brackets). Implemented locally (instead of lib/validate) to avoid an
// import cycle with the cluster model in later wiring.
func validateHost(host string) error {
	if host == "" {
		return xerror.WrapParamErrorWithMsg("host is required")
	}
	if len(host) > 255 {
		return xerror.WrapParamErrorWithMsg("host length must be <= 255")
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !hostnameLabel.MatchString(label) {
			return xerror.WrapParamErrorWithMsg("host %q contains invalid label %q", host, label)
		}
	}
	return nil
}

// validatePort checks a TCP port number.
func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return xerror.WrapParamErrorWithMsg("port must be between 1 and 65535, got %d", port)
	}
	return nil
}

// validateHeaderName checks a non-empty HTTP header name.
func validateHeaderName(name string) error {
	if strings.TrimSpace(name) == "" {
		return xerror.WrapParamErrorWithMsg("header name is required")
	}
	if !httpHeaderNameToken.MatchString(name) {
		return xerror.WrapParamErrorWithMsg("invalid HTTP header name %q", name)
	}
	return nil
}
