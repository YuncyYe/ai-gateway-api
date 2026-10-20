package shared

import (
	"context"
	"encoding/json"
	"time"
)

type EntitySummary struct {
	ID   *string `json:"id"`
	Name *string `json:"name"`
	Type *string `json:"type"`
}

type EntityFilter struct {
	EntityID *string `form:"id"`
	Name     *string `form:"name"`
	Type     *string `form:"type"`
	ParentID *string `form:"parent_id"`
	Page     *int    `form:"page"`
	PageSize *int    `form:"page_size"`
}

type EntityStorager interface {
	FetchEntity(ctx context.Context, filter *EntityFilter) (*EntitySummary, error)
}

type BalanceSummary struct {
	Used      *float64 `json:"used"`
	Remaining *float64 `json:"remaining"`
}

type QuotaPlanParam struct {
	Unlimited             *bool           `json:"unlimited"`
	PassWhenNoEnoughQuota *bool           `json:"pass_when_no_enough_quota"`
	Quota                 *float64        `json:"quota"`
	Unit                  *string         `json:"unit"`
	ResetPeriod           *string         `json:"reset_period"`
	LastResetAt           *time.Time      `json:"last_reset_at,omitempty"`
	Balance               *BalanceSummary `json:"balance,omitempty"`
}

type TPMConfig struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	WindowMinutes int    `json:"window_minutes"`
	MaxTokens     int    `json:"max_tokens"`
	StepMinutes   int    `json:"step_minutes"`
}

type RPMConfig struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	WindowMinutes int    `json:"window_minutes"`
	MaxRequests   int    `json:"max_requests"`
}

type RateLimitRules struct {
	TpmConfigs     []TPMConfig  `json:"tpm"`
	RpmConfigs     []RPMConfig  `json:"rpm"`
	MaxConcurrency *int         `json:"max_concurrency"`
	BatchLimits    *BatchLimits `json:"batch_limits,omitempty"`
}

// BatchLimits defines the batch-request rate limit dimensions of a policy
// (2026-10-07, batch & async task support phase 1). All dimensions are
// counted per api key and are model-agnostic (batch request bodies carry no
// model field). Nil means the policy does not participate in batch rate
// limiting (omitted/null, zero migration for existing rows); 0 means the
// dimension is not limited. A policy with no batch segment still falls under
// the BFE mod_ai_batch global hard caps.
type BatchLimits struct {
	MaxCreateRPM     int   `json:"max_create_rpm"`
	MaxActiveBatches int   `json:"max_active_batches"`
	MaxFileBytes     int64 `json:"max_file_bytes"`
	MaxFileLines     int   `json:"max_file_lines"`
}

type RateLimitPolicyParam struct {
	Enabled *bool           `json:"enabled"`
	Rules   *RateLimitRules `json:"rules"`
}

type AiRouteTargetParam struct {
	ClusterName *string `json:"cluster_name"`
	Model       *string `json:"model"`
	Weight      *int    `json:"weight"`
}

// UnmarshalJSON accepts both the new snake_case keys and the legacy camel-case
// keys for backward compatibility with existing database records and old
// clients. Serialization continues to emit only the new keys.
func (t *AiRouteTargetParam) UnmarshalJSON(data []byte) error {
	type Alias AiRouteTargetParam
	aux := &struct {
		*Alias
		OldClusterName *string `json:"ClusterName"`
		OldModel       *string `json:"Model"`
		OldWeight      *int    `json:"Weight"`
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if t.ClusterName == nil && aux.OldClusterName != nil {
		t.ClusterName = aux.OldClusterName
	}
	if t.Model == nil && aux.OldModel != nil {
		t.Model = aux.OldModel
	}
	if t.Weight == nil && aux.OldWeight != nil {
		t.Weight = aux.OldWeight
	}
	return nil
}

type AiRouteFallbackParam struct {
	ClusterName *string `json:"cluster_name"`
	Model       *string `json:"model"`
}

// UnmarshalJSON accepts both the new snake_case keys and the legacy camel-case
// keys for backward compatibility.
func (f *AiRouteFallbackParam) UnmarshalJSON(data []byte) error {
	type Alias AiRouteFallbackParam
	aux := &struct {
		*Alias
		OldClusterName *string `json:"ClusterName"`
		OldModel       *string `json:"Model"`
	}{
		Alias: (*Alias)(f),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if f.ClusterName == nil && aux.OldClusterName != nil {
		f.ClusterName = aux.OldClusterName
	}
	if f.Model == nil && aux.OldModel != nil {
		f.Model = aux.OldModel
	}
	return nil
}

type AiRouteRuleParam struct {
	Name      *string                 `json:"name"`
	Cond      *string                 `json:"cond"`
	Targets   []*AiRouteTargetParam   `json:"targets"`
	Fallbacks []*AiRouteFallbackParam `json:"fallbacks"`
}

// UnmarshalJSON accepts both the new snake_case keys and the legacy camel-case
// keys for backward compatibility.
func (r *AiRouteRuleParam) UnmarshalJSON(data []byte) error {
	type Alias AiRouteRuleParam
	aux := &struct {
		*Alias
		OldCond *string `json:"Cond"`
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if r.Cond == nil && aux.OldCond != nil {
		r.Cond = aux.OldCond
	}
	return nil
}

type RouteRulesParam struct {
	ID      *int64              `json:"-"`
	Enabled *bool               `json:"enabled"`
	Rules   []*AiRouteRuleParam `json:"rules"`
}

type RouteTableParam struct {
	ID      *int64 `json:"id,omitempty"`
	Type    string `json:"type"`
	Owner   string `json:"owner"`
	Enabled bool   `json:"enabled"`
}

const (
	// RouteRulesTypeAPIKey is the internal storage/BFE export value for
	// API-Key route rules. The OpenAPI /route-tables endpoint exposes it
	// as RouteTableTypeAPIKey ("api_key") for consistency with the API doc.
	RouteRulesTypeAPIKey = "apikey"
	RouteRulesTypeEntity = "entity"
	RouteRulesTypeGlobal = "global"
)

// OpenAPI-facing route table type values, as defined in the API document.
const (
	RouteTableTypeAPIKey = "api_key"
	RouteTableTypeEntity = "entity"
	RouteTableTypeGlobal = "global"
)

// ToRouteTableType maps an internal route rules type to the value exposed by
// the OpenAPI /route-tables endpoint.
func ToRouteTableType(internalType string) string {
	if internalType == RouteRulesTypeAPIKey {
		return RouteTableTypeAPIKey
	}
	return internalType
}

// FromRouteTableType maps an OpenAPI /route-tables type query parameter to the
// internal route rules type used in storage and BFE exports.
func FromRouteTableType(apiType string) string {
	if apiType == RouteTableTypeAPIKey {
		return RouteRulesTypeAPIKey
	}
	return apiType
}

// RouteRulesFilter defines filters for querying route rules
type RouteRulesFilter struct {
	Type      *string `form:"type"`
	Owner     *string `form:"owner"`
	Enabled   *bool   `form:"enabled"`
	Page      *int    `form:"page"`
	PageSize  *int    `form:"page_size"`
	SortBy    *string `form:"sort_by"`
	SortOrder *string `form:"sort_order"`
}

// RouteRulesStorager defines storage operations for route rules
type RouteRulesStorager interface {
	CreateRouteRules(ctx context.Context, ruleType string, owner *string, param *RouteRulesParam) (int64, error)
	FetchRouteRules(ctx context.Context, ruleType string, owner *string) (*RouteRulesParam, error)
	FetchRouteRulesList(ctx context.Context, filter *RouteRulesFilter) ([]*RouteTableParam, int64, error)
	UpdateRouteRules(ctx context.Context, id int64, param *RouteRulesParam) (int64, error)
	DeleteRouteRules(ctx context.Context, id int64) error
	FetchRouteRulesByID(ctx context.Context, id int64) (*RouteRulesParam, error)
	// FetchAllRouteRules returns all route rules without pagination.
	// Used by reference checkers that must scan every rule table.
	FetchAllRouteRules(ctx context.Context) ([]*RouteRulesParam, error)
}

type QuotaPlanStorager interface {
	CreateQuotaPlan(ctx context.Context, param *QuotaPlanParam) (int64, error)
	UpdateQuotaPlan(ctx context.Context, id int64, param *QuotaPlanParam) (int64, error)
	DeleteQuotaPlan(ctx context.Context, id int64) error
	FetchQuotaPlan(ctx context.Context, id int64) (*QuotaPlanParam, error)
}

type RateLimitPolicyStorager interface {
	CreateRateLimitPolicy(ctx context.Context, param *RateLimitPolicyParam) (int64, error)
	UpdateRateLimitPolicy(ctx context.Context, id int64, param *RateLimitPolicyParam) (int64, error)
	DeleteRateLimitPolicy(ctx context.Context, id int64) error
	FetchRateLimitPolicy(ctx context.Context, id int64) (*RateLimitPolicyParam, error)
}

// AICacheRuleParam defines an AI cache rule. The Open API vocabulary is
// lowercase snake_case; optional fields left nil are filled with defaults
// (cache_key_strategy=lastQuestion, cache_ttl=0, max_body_bytes/max_value_bytes=1048576,
// enable_semantic_cache=false).
// Per-rule timestamps are intentionally not part of the vocabulary: the
// collection is a full-replace resource (every PUT recreates all rows and
// renumbers their ids), so created_at/updated_at would only ever equal the last
// PUT time. Not exposing them also keeps the bulk INSERT column set identical
// across rows — gendry derives it from the first row and rejects later rows
// missing a column with "insert data not match" (issue #258). Audit time is
// available through GET /operation-logs.
type AICacheRuleParam struct {
	Name                *string `json:"name"`
	Cond                *string `json:"cond"`
	CacheKeyStrategy    *string `json:"cache_key_strategy,omitempty"`
	CacheTTL            *int    `json:"cache_ttl,omitempty"`
	MaxBodyBytes        *int64  `json:"max_body_bytes,omitempty"`
	MaxValueBytes       *int64  `json:"max_value_bytes,omitempty"`
	EnableSemanticCache *bool   `json:"enable_semantic_cache,omitempty"`
}

// AICacheRulesParam defines the AI cache rule collection (full-replace semantics:
// the submitted list is the effective set; a null rules list is treated as empty).
type AICacheRulesParam struct {
	Rules []*AICacheRuleParam `json:"rules"`
}

// AICacheSemanticSettingsParam defines the singleton AI cache semantic
// settings (phase 2). The Open API vocabulary is lowercase snake_case;
// optional fields left nil are filled with the documented defaults
// (top_k=1, threshold=0.15, threshold_relation=lt).
// CreatedAt/UpdatedAt are read-only response fields (RFC3339), present only
// when a settings row exists in storage.
type AICacheSemanticSettingsParam struct {
	TopK              *int       `json:"top_k,omitempty"`
	Threshold         *float64   `json:"threshold,omitempty"`
	ThresholdRelation *string    `json:"threshold_relation,omitempty"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
}

// AIContextRuleParam defines an AI context compression rule. The Open API
// vocabulary is lowercase snake_case; optional fields left nil are filled
// with defaults (max_context_tokens=0, reserve_tokens=0; 0 means "use the
// model table window" / "auto reserve" on the BFE side). There is no name
// field: cond is the rule identity within the collection.
// Read-only timestamps are intentionally not exposed: the collection is a
// full-replace resource (every PUT recreates all rows), so per-rule
// created_at/updated_at carry no information (aligned with ai-cache-rules).
type AIContextRuleParam struct {
	Cond             *string `json:"cond"`
	Mode             *string `json:"mode"`
	MaxContextTokens *int    `json:"max_context_tokens,omitempty"`
	ReserveTokens    *int    `json:"reserve_tokens,omitempty"`
}

// AIContextRulesParam defines the AI context rule collection (full-replace
// semantics: the submitted list is the effective set; a null rules list is
// treated as empty).
type AIContextRulesParam struct {
	Rules []*AIContextRuleParam `json:"rules"`
}

// AIContextRewriteParam defines the rewrite-layer sub-object of the global
// AI context settings.
type AIContextRewriteParam struct {
	Strength              *string  `json:"strength,omitempty"`
	ProtectedSurvivalRate *float64 `json:"protected_survival_rate,omitempty"`
}

// AIContextSettingsParam defines the singleton AI context global settings.
// The Open API vocabulary is lowercase snake_case; optional fields left nil
// (including a nil rewrite sub-object or its nil fields) are filled with the
// documented defaults (0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 /
// lite / 0.95), frozen in sync with the BFE mod_ai_context setDefaults.
// CreatedAt/UpdatedAt are read-only response fields (RFC3339), present only
// when a settings row exists in storage.
type AIContextSettingsParam struct {
	TriggerRatio       *float64               `json:"trigger_ratio,omitempty"`
	KeepLatestImages   *int                   `json:"keep_latest_images,omitempty"`
	ToolResultMaxChars *int                   `json:"tool_result_max_chars,omitempty"`
	ThinkingPolicy     *string                `json:"thinking_policy,omitempty"`
	CharsPerToken      *int                   `json:"chars_per_token,omitempty"`
	ImageTokenEstimate *int                   `json:"image_token_estimate,omitempty"`
	Rewrite            *AIContextRewriteParam `json:"rewrite,omitempty"`
}

// TrafficMirrorBodyRewriteParam defines a body field rewrite on the mirror copy.
// Phase 1 hard-checks path to "model" (aligned with BFE mod_traffic_mirror Check).
type TrafficMirrorBodyRewriteParam struct {
	Path  *string `json:"path"`
	Value *string `json:"value"`
}

// TrafficMirrorRuleParam defines a traffic mirror rule. The Open API vocabulary
// is lowercase snake_case. Optional fields left nil keep their NULL semantics:
// remove_headers nil means "not submitted" (the export fills the default
// sensitive-header blacklist), while an explicit empty array strips nothing.
// percentage is filled with the documented default (100) on write.
// CreatedAt/UpdatedAt are read-only response fields (RFC3339).
type TrafficMirrorRuleParam struct {
	Name          *string                          `json:"name"`
	Cond          *string                          `json:"cond"`
	MirrorCluster *string                          `json:"mirror_cluster"`
	Percentage    *int                             `json:"percentage,omitempty"`
	RemoveHeaders *[]string                        `json:"remove_headers,omitempty"`
	SetHeaders    map[string]string                `json:"set_headers,omitempty"`
	BodyRewrites  []*TrafficMirrorBodyRewriteParam `json:"body_rewrites,omitempty"`
	PathRewrite   *string                          `json:"path_rewrite,omitempty"`
	CreatedAt     *time.Time                       `json:"created_at,omitempty"`
	UpdatedAt     *time.Time                       `json:"updated_at,omitempty"`
}

// TrafficMirrorRulesParam defines the traffic mirror rule collection
// (full-replace semantics: the submitted list is the effective set; a null
// rules list is treated as empty and clears the collection).
type TrafficMirrorRulesParam struct {
	Rules []*TrafficMirrorRuleParam `json:"rules"`
}
