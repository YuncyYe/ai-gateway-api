package shared

import (
	"fmt"
	"strings"
)

// BuildBFERateLimitRedisKey 构造 BFE 实际使用的完整 Redis Key。
// 控制面在导出时直接下发完整 Key，BFE 侧优先使用该 Key 而不再拼接前缀。
func BuildBFERateLimitRedisKey(policyID int64, ruleType string, name string) string {
	return fmt.Sprintf("default_bfe_rlp-%d_%s_rlp-%d_%s", policyID, ruleType, policyID, name)
}

// BuildBatchRateLimitRedisKey 构造批量创建 RPM 计数器的完整 Redis Key
// （生成惯例同 RL_TPM/RL_RPM）。仅 max_create_rpm > 0 的策略需要该 Key；
// 文件上限与在途上限为请求内本地校验 / 复用数据面 BATCH_ACTIVE ZSET，无 Key。
func BuildBatchRateLimitRedisKey(policyID int64) string {
	return BuildBFERateLimitRedisKey(policyID, "RL_BATCH", "rpm")
}

// BatchRateLimitEnabled 报告策略是否配置了批量创建 RPM 维度（决定是否需要
// 批量计数 Redis Key）。
func BatchRateLimitEnabled(rules *RateLimitRules) bool {
	return rules != nil && rules.BatchLimits != nil && rules.BatchLimits.MaxCreateRPM > 0
}

// BuildRateLimitRedisKeys 根据限流规则生成 BFE 实际使用的完整 Redis Key 列表。
// 该函数供 API-Key / Entity 删除或更新时清理 Redis Key 使用。
func BuildRateLimitRedisKeys(policyID int64, rules *RateLimitRules) []string {
	keys := buildTpmRpmRateLimitRedisKeys(policyID, rules)
	if BatchRateLimitEnabled(rules) {
		keys = append(keys, BuildBatchRateLimitRedisKey(policyID))
	}
	return keys
}

// buildTpmRpmRateLimitRedisKeys 生成 tpm/rpm 规则 Key 列表（不含批量 Key，
// 供 DiffRateLimitRedisKeys 的 name 匹配逻辑使用）。
func buildTpmRpmRateLimitRedisKeys(policyID int64, rules *RateLimitRules) []string {
	if rules == nil {
		return nil
	}
	var keys []string
	for _, tpm := range rules.TpmConfigs {
		keys = append(keys, BuildBFERateLimitRedisKey(policyID, "RL_TPM", tpm.Name))
	}
	for _, rpm := range rules.RpmConfigs {
		keys = append(keys, BuildBFERateLimitRedisKey(policyID, "RL_RPM", rpm.Name))
	}
	return keys
}

// DiffRateLimitRedisKeys 比较新旧限流策略，返回被删除规则的 Redis Key 列表。
// 规则按 name 匹配；旧策略中存在但新策略中不存在的 name 视为被删除。
// 批量创建 RPM Key 无 name 维度，单独按“旧配置启用且新配置未启用”判定。
func DiffRateLimitRedisKeys(policyID int64, oldRules, newRules *RateLimitRules) []string {
	oldKeys := buildTpmRpmRateLimitRedisKeys(policyID, oldRules)
	newNames := make(map[string]struct{})
	if newRules != nil {
		for _, tpm := range newRules.TpmConfigs {
			newNames[tpm.Name] = struct{}{}
		}
		for _, rpm := range newRules.RpmConfigs {
			newNames[rpm.Name] = struct{}{}
		}
	}

	var deleted []string
	for _, key := range oldKeys {
		name := extractRateLimitNameFromKey(key)
		if _, ok := newNames[name]; !ok {
			deleted = append(deleted, key)
		}
	}
	if BatchRateLimitEnabled(oldRules) && !BatchRateLimitEnabled(newRules) {
		deleted = append(deleted, BuildBatchRateLimitRedisKey(policyID))
	}
	return deleted
}

// extractRateLimitNameFromKey 从 BuildBFERateLimitRedisKey 生成的 Key 中提取规则 name。
// Key 格式：default_bfe_rlp-<policyID>_RL_<TYPE>_rlp-<policyID>_<name>
func extractRateLimitNameFromKey(key string) string {
	idx := strings.LastIndex(key, "_rlp-")
	if idx == -1 {
		return ""
	}
	// 找到最后一个 "_rlp-" 之后的 "_"，name 在其后
	rest := key[idx+5:]
	underscore := strings.Index(rest, "_")
	if underscore == -1 || underscore+1 >= len(rest) {
		return ""
	}
	return rest[underscore+1:]
}
