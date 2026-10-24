package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildBFERateLimitRedisKey(t *testing.T) {
	got := BuildBFERateLimitRedisKey(101, "RL_TPM", "tpm-1")
	assert.Equal(t, "default_bfe_rlp-101_RL_TPM_rlp-101_tpm-1", got)
}

func TestBuildRateLimitRedisKeys(t *testing.T) {
	rules := &RateLimitRules{
		TpmConfigs: []TPMConfig{
			{Name: "tpm-1", Model: "gpt-4", WindowMinutes: 1, MaxTokens: 100, StepMinutes: 1},
			{Name: "tpm-2", Model: "gpt-3.5", WindowMinutes: 10, MaxTokens: 200, StepMinutes: 1},
		},
		RpmConfigs: []RPMConfig{
			{Name: "rpm-1", Model: "*", WindowMinutes: 1, MaxRequests: 10},
		},
	}

	got := BuildRateLimitRedisKeys(101, rules)
	assert.Equal(t, []string{
		"default_bfe_rlp-101_RL_TPM_rlp-101_tpm-1",
		"default_bfe_rlp-101_RL_TPM_rlp-101_tpm-2",
		"default_bfe_rlp-101_RL_RPM_rlp-101_rpm-1",
	}, got)
}

func TestBuildRateLimitRedisKeys_NilRules(t *testing.T) {
	assert.Nil(t, BuildRateLimitRedisKeys(101, nil))
}

func TestDiffRateLimitRedisKeys(t *testing.T) {
	oldRules := &RateLimitRules{
		TpmConfigs: []TPMConfig{
			{Name: "keep", Model: "gpt-4", WindowMinutes: 1, MaxTokens: 100, StepMinutes: 1},
			{Name: "remove", Model: "gpt-3.5", WindowMinutes: 1, MaxTokens: 200, StepMinutes: 1},
		},
		RpmConfigs: []RPMConfig{
			{Name: "rpm-keep", Model: "*", WindowMinutes: 1, MaxRequests: 10},
			{Name: "rpm-remove", Model: "*", WindowMinutes: 1, MaxRequests: 20},
		},
	}

	newRules := &RateLimitRules{
		TpmConfigs: []TPMConfig{
			{Name: "keep", Model: "gpt-4", WindowMinutes: 1, MaxTokens: 100, StepMinutes: 1},
		},
		RpmConfigs: []RPMConfig{
			{Name: "rpm-keep", Model: "*", WindowMinutes: 1, MaxRequests: 10},
		},
	}

	got := DiffRateLimitRedisKeys(101, oldRules, newRules)
	assert.ElementsMatch(t, []string{
		"default_bfe_rlp-101_RL_TPM_rlp-101_remove",
		"default_bfe_rlp-101_RL_RPM_rlp-101_rpm-remove",
	}, got)
}

func TestDiffRateLimitRedisKeys_AllRemoved(t *testing.T) {
	oldRules := &RateLimitRules{
		TpmConfigs: []TPMConfig{
			{Name: "tpm-1", Model: "gpt-4", WindowMinutes: 1, MaxTokens: 100, StepMinutes: 1},
		},
	}

	got := DiffRateLimitRedisKeys(101, oldRules, nil)
	assert.Equal(t, []string{"default_bfe_rlp-101_RL_TPM_rlp-101_tpm-1"}, got)
}

func TestDiffRateLimitRedisKeys_NilOld(t *testing.T) {
	assert.Empty(t, DiffRateLimitRedisKeys(101, nil, &RateLimitRules{}))
}

func TestBuildBatchRateLimitRedisKey(t *testing.T) {
	got := BuildBatchRateLimitRedisKey(101)
	assert.Equal(t, "default_bfe_rlp-101_RL_BATCH_rlp-101_rpm", got)
}

func TestBuildRateLimitRedisKeys_WithBatchLimits(t *testing.T) {
	rules := &RateLimitRules{
		RpmConfigs: []RPMConfig{
			{Name: "rpm-1", Model: "*", WindowMinutes: 1, MaxRequests: 10},
		},
		BatchLimits: &BatchLimits{MaxCreateRPM: 10, MaxActiveBatches: 5},
	}

	got := BuildRateLimitRedisKeys(101, rules)
	assert.Equal(t, []string{
		"default_bfe_rlp-101_RL_RPM_rlp-101_rpm-1",
		"default_bfe_rlp-101_RL_BATCH_rlp-101_rpm",
	}, got)

	// No key when max_create_rpm is not configured (>0).
	rules.BatchLimits = &BatchLimits{MaxActiveBatches: 5}
	assert.Equal(t, []string{
		"default_bfe_rlp-101_RL_RPM_rlp-101_rpm-1",
	}, BuildRateLimitRedisKeys(101, rules))

	// No key when batch limits are absent entirely.
	rules.BatchLimits = nil
	assert.Equal(t, []string{
		"default_bfe_rlp-101_RL_RPM_rlp-101_rpm-1",
	}, BuildRateLimitRedisKeys(101, rules))
}

func TestDiffRateLimitRedisKeys_Batch(t *testing.T) {
	oldRules := &RateLimitRules{
		BatchLimits: &BatchLimits{MaxCreateRPM: 10},
	}
	newRules := &RateLimitRules{
		BatchLimits: &BatchLimits{MaxCreateRPM: 10},
	}

	// Unchanged batch limits produce no deletion.
	assert.Empty(t, DiffRateLimitRedisKeys(101, oldRules, newRules))

	// Removing the create-rpm dimension schedules the batch key for cleanup.
	newRules.BatchLimits.MaxCreateRPM = 0
	assert.Equal(t, []string{"default_bfe_rlp-101_RL_BATCH_rlp-101_rpm"}, DiffRateLimitRedisKeys(101, oldRules, newRules))

	// Removing batch limits entirely schedules the batch key for cleanup.
	assert.Equal(t, []string{"default_bfe_rlp-101_RL_BATCH_rlp-101_rpm"}, DiffRateLimitRedisKeys(101, oldRules, &RateLimitRules{}))

	// Adding batch limits produces no deletion.
	assert.Empty(t, DiffRateLimitRedisKeys(101, &RateLimitRules{}, oldRules))
}

func TestBatchRateLimitEnabled(t *testing.T) {
	assert.False(t, BatchRateLimitEnabled(nil))
	assert.False(t, BatchRateLimitEnabled(&RateLimitRules{}))
	assert.False(t, BatchRateLimitEnabled(&RateLimitRules{BatchLimits: &BatchLimits{}}))
	assert.False(t, BatchRateLimitEnabled(&RateLimitRules{BatchLimits: &BatchLimits{MaxCreateRPM: 0, MaxActiveBatches: 5}}))
	assert.True(t, BatchRateLimitEnabled(&RateLimitRules{BatchLimits: &BatchLimits{MaxCreateRPM: 1}}))
}
