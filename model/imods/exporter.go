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

package imods

import (
	"context"
	"fmt"
	"net"
	"strings"

	golibquota "github.com/bfenetworks/go-lib/quota"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	entpkg "github.com/rainway-ai-gateway/ai-gateway-api/model/entity"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iai_route"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/quota"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// ConfigTopicProductAPIKeyRule is the configuration topic for API key rules
const ConfigTopicProductAPIKeyRule = "mod_api_key_rule"

// maxEntityHierarchyDepth limits the number of ancestor levels walked when
// resolving Entity hierarchy, protecting against accidental parent_id cycles.
const maxEntityHierarchyDepth = 10

// ModAPIKeyRuleConf defines the configuration structure for API key rules module
type ModAPIKeyRuleConf struct {
	Version    *string                          `json:"version"`
	Config     map[string][]*TokenRuleFile      `json:"config"`
	QuotaPlans map[string][]*QuotaPlan          `json:"QuotaPlans"`
	Tokens     map[string]map[string]*TokenFile `json:"tokens"`
}

type TokenRuleFile struct {
	Cond   *string
	Action *ActionFile
}

type ApikeyTag struct {
	TagName  string //eg entity.type
	TagValue string //eg entity.name
	TagLevel int
}

type ActionFile struct {
	Cmd string
}

type QuotaPlan struct {
	Id          string
	Unlimited   bool
	PassNoQuota bool
	RedisKey    string
	ExpiredTime int64 // -1 means never expired
	Quota       int64 // 配额总量
	Unit        string
}

type TokenFile struct {
	Key            string  `json:"key"`
	KeyID          string  `json:"key_id"`
	Enabled        bool    `json:"enabled"`
	ExpiredTime    int64   `json:"expired_time"` // -1 means never expired
	UnlimitedQuota bool    `json:"unlimited_quota"`
	Models         *string `json:"allow_models"` // allowed models
	BlockModels    *string `json:"block_models"` // blocked models
	Subnet         *string `json:"subnet"`       // allowed subnet
	Tags           []ApikeyTag
	QuotaPlans     []string `json:"quota_plans"` // quotaPlan IDs
	models         []string
	blockModels    []string
	subnet         []*net.IPNet
}

// UpdateVersion updates the configuration version
func (conf *ModAPIKeyRuleConf) UpdateVersion(version string) error {
	conf.Version = &version
	return nil
}

// ConfigExport exports API key rule configuration for BFE
func (rcm *APIKeyRuleManager) ConfigExport(ctx context.Context, lastVersion string) (*ModAPIKeyRuleConf, error) {
	// Export configuration using version control manager
	rst, err := rcm.versionControlManager.ExportConfig(ctx, ConfigTopicProductAPIKeyRule, rcm.APIKeyRuleGenerator)
	if err != nil {
		return nil, err
	}

	if rst.DataWithoutVersion == nil {
		return nil, fmt.Errorf("APIKeyRuleGenerator.DataWithoutVersion is nil")
	}

	// Convert exported data to configuration structure
	conf, ok := rst.DataWithoutVersion.(*ModAPIKeyRuleConf)
	if ok {
		// Return nil if version hasn't changed
		if *conf.Version == lastVersion {
			return nil, nil
		}

		return conf, nil
	}

	return nil, fmt.Errorf("convert APIKeyRuleGenerator.DataWithoutVersion to ModAPIKeyRuleConf is error")
}

func (rlm *APIKeyRuleManager) FormatAIRouteAPIKeyRules(ctx context.Context, productName string) ([]*APIKeyRule, error) {
	aiRouteRules, err := rlm.aiRouteStorager.FetchAIRouteRules(ctx, &iai_route.AIRouteFilter{
		ProductName: &productName,
	})
	if err != nil {
		return nil, err
	}

	var ruleResult []*APIKeyRule
	for _, rule := range aiRouteRules {
		cond := iai_route.BuildAIRouteCond(ctx, rule.Basic)

		ruleResult = append(ruleResult, &APIKeyRule{
			Cond: cond,
			Actions: []Action{
				{
					Cmd: APIKeyActionCMD,
				},
			},
			ProductName: stateful.DefaultConfig.RunTime.AIRouteInnerProductName,
		})
	}

	ruleResult = append(ruleResult, &APIKeyRule{
		Cond: "default_t()",
		Actions: []Action{
			{
				Cmd: APIKeyActionCMD,
			},
		},
		ProductName: stateful.DefaultConfig.RunTime.AIRouteInnerProductName,
	})

	return ruleResult, nil
}

func (rlm *APIKeyRuleManager) buildAIRouteAPIKeyRules(ctx context.Context) (map[string]*APIKey, error) {
	aiProductName := stateful.DefaultConfig.RunTime.AIRouteInnerProductName
	rules, err := rlm.FormatAIRouteAPIKeyRules(ctx, aiProductName)
	if err != nil {
		return nil, fmt.Errorf("read ai route api key rule is error:%s", err.Error())
	}

	product2config := make(map[string]*APIKey)
	if len(rules) > 0 {
		product2config[aiProductName] = &APIKey{
			Rules: rules,
		}
	}

	return product2config, nil
}

// buildEntityMap loads all entities into a map indexed by EntityID.
func (rlm *APIKeyRuleManager) buildEntityMap(ctx context.Context) (map[string]*entpkg.EntityParam, error) {
	if rlm.entityStorager == nil {
		return nil, nil
	}

	allEntities, err := rlm.entityStorager.FetchEntityList(ctx, &entpkg.EntityFilter{})
	if err != nil {
		return nil, err
	}

	entityMap := make(map[string]*entpkg.EntityParam, len(allEntities))
	for _, e := range allEntities {
		if e.EntityID != nil {
			entityMap[*e.EntityID] = e
		}
	}

	return entityMap, nil
}

// buildQuotaPlanMap loads all quota plans into a map indexed by ID.
func (rlm *APIKeyRuleManager) buildQuotaPlanMap(ctx context.Context) (map[int64]*quota.QuotaPlanParam, error) {
	if rlm.quotaPlanStorager == nil {
		return nil, nil
	}

	allPlans, err := rlm.quotaPlanStorager.FetchQuotaPlanList(ctx, &quota.QuotaPlanFilter{})
	if err != nil {
		return nil, err
	}

	planMap := make(map[int64]*quota.QuotaPlanParam, len(allPlans))
	for _, qp := range allPlans {
		if qp.ID != nil {
			planMap[*qp.ID] = qp
		}
	}

	return planMap, nil
}

// buildEntityTypeMap loads all entity types into a map indexed by type name.
func (rlm *APIKeyRuleManager) buildEntityTypeMap(ctx context.Context) (map[string]*entpkg.EntityTypeParam, error) {
	if rlm.entityTypeStorager == nil {
		return nil, nil
	}

	allTypes, err := rlm.entityTypeStorager.FetchEntityTypeList(ctx, &entpkg.EntityTypeFilter{})
	if err != nil {
		return nil, err
	}

	typeMap := make(map[string]*entpkg.EntityTypeParam, len(allTypes))
	for _, et := range allTypes {
		if et.TypeName != nil {
			typeMap[*et.TypeName] = et
		}
	}

	return typeMap, nil
}

// APIKeyRuleGenerator generates API key rules and token information for BFE configuration
func (rlm *APIKeyRuleManager) APIKeyRuleGenerator(ctx context.Context) (*iversion_control.ExportData, error) {
	collectedQuotaPlans := make(map[string][]*QuotaPlan)

	product2config, err := rlm.buildAIRouteAPIKeyRules(ctx)
	if err != nil {
		return nil, err
	}

	productName2Config := make(map[string][]*TokenRuleFile)
	for productName, productConfig := range product2config {
		if len(productConfig.Rules) > 0 {
			productName2Config[productName] = convertAPIKeyRulesToBfeRules(productConfig.Rules)
		}
	}

	apiKeyList, err := rlm.apiKeyStorager.FetchAPIKeyList(ctx, &api_key.APIKeyFilter{})
	if err != nil {
		return nil, err
	}

	entityMap, err := rlm.buildEntityMap(ctx)
	if err != nil {
		return nil, err
	}

	quotaPlanMap, err := rlm.buildQuotaPlanMap(ctx)
	if err != nil {
		return nil, err
	}

	entityTypeMap, err := rlm.buildEntityTypeMap(ctx)
	if err != nil {
		return nil, err
	}

	apiKey2Config := make(map[string]map[string]*TokenFile)
	for _, one := range apiKeyList {
		if _, ok := apiKey2Config[*one.ProductName]; !ok {
			items := make(map[string]*TokenFile)
			apiKey2Config[*one.ProductName] = items
		}

		expiredTime := int64(UnlimitedQuota)
		if one.ExpiredTime != nil {
			expiredTime = *one.ExpiredTime
		}

		enabled := one.Enable != nil && *one.Enable

		items := apiKey2Config[*one.ProductName]

		tokenFile := &TokenFile{
			Key:            *one.Key,
			KeyID:          *one.ID,
			Enabled:        enabled,
			ExpiredTime:    expiredTime,
			UnlimitedQuota: one.UnlimitedQuota != nil && *one.UnlimitedQuota,
		}

		// Collect allow_models and block_models from entity hierarchy
		var entityAllowModels []string
		var entityBlockModels []string
		if one.EntityID != nil && *one.EntityID != "" && rlm.entityStorager != nil {
			entityAllowModels, entityBlockModels, err = rlm.fetchEntityModelHierarchy(ctx, entityMap, *one.EntityID)
			if err != nil {
				return nil, fmt.Errorf("fetch entity model hierarchy error: %s", err.Error())
			}
		}

		// Collect api-key's non-empty, non-* allow_models
		var apiKeyAllowModels []string
		for _, m := range one.Models {
			if m != "" && m != "*" {
				apiKeyAllowModels = append(apiKeyAllowModels, m)
			}
		}

		// entityAllowModels is already the intersection of all non-* entity allow_models
		// Compute the final intersection: api-key models ∩ entity models
		var finalAllowModels []string
		if len(apiKeyAllowModels) == 0 && len(entityAllowModels) == 0 {
			// Rule 1: both are empty or * → allow all
			modelsStr := ""
			tokenFile.Models = &modelsStr
		} else {
			// Rule 2: compute intersection of all non-empty, non-* models
			if len(apiKeyAllowModels) == 0 {
				finalAllowModels = entityAllowModels
			} else if len(entityAllowModels) == 0 {
				finalAllowModels = apiKeyAllowModels
			} else {
				finalAllowModels = intersectSlices(apiKeyAllowModels, entityAllowModels)
			}

			if len(finalAllowModels) == 0 {
				// Rule 3: intersection empty → disable the token, models empty
				modelsStr := ""
				tokenFile.Models = &modelsStr
				enabled = false
			} else {
				modelsStr := strings.Join(finalAllowModels, ",")
				tokenFile.Models = &modelsStr
			}
		}

		tokenFile.Enabled = enabled

		// Merge block_models from entity hierarchy (union of all)
		if len(entityBlockModels) > 0 {
			blockModelsStr := strings.Join(entityBlockModels, ",")
			if blockModelsStr == "*" {
				blockModelsStr = ""
			}
			tokenFile.BlockModels = &blockModelsStr
		}

		if len(one.Subnet) > 0 {
			subnetStr := strings.Join(one.Subnet, ",")
			if subnetStr == "*" {
				subnetStr = ""
			}
			tokenFile.Subnet = &subnetStr
		}

		quotaPlanIDs, tags, err := rlm.fetchQuotaPlansWithEntityHierarchy(ctx, one, *one.ProductName, collectedQuotaPlans, entityMap, quotaPlanMap, entityTypeMap)
		if err != nil {
			return nil, fmt.Errorf("fetch quota plans error: %s", err.Error())
		}
		tokenFile.QuotaPlans = quotaPlanIDs
		tokenFile.Tags = tags

		// Defense: if all related quota plans are unlimited and QuotaPlans is empty,
		// treat this token as unlimited to avoid BFE load failure.
		if !tokenFile.UnlimitedQuota && len(tokenFile.QuotaPlans) == 0 {
			tokenFile.UnlimitedQuota = true
		}

		tokenKey := *one.Key
		if stateful.ExportCryptoEnabled() {
			// Field-level encryption at rest: the Tokens outer key IS the
			// api-key value. Encrypt it so no key material lands on BFE
			// disk in plaintext; the inner "key" field is cleared (the data
			// plane fills it back from the decrypted key). Any failure
			// aborts the export rather than emitting a half-encrypted file.
			tokenKey, err = stateful.ExportEncrypt(tokenKey)
			if err != nil {
				return nil, fmt.Errorf("encrypt token key_id %s failed: %s", tokenFile.KeyID, err)
			}
			tokenFile.Key = ""
		}

		items[tokenKey] = tokenFile
		apiKey2Config[*one.ProductName] = items
	}

	conf := &ModAPIKeyRuleConf{
		Config:     productName2Config,
		QuotaPlans: collectedQuotaPlans,
		Tokens:     apiKey2Config,
	}

	conf.UpdateVersion(iversion_control.ZeroVersion)

	return &iversion_control.ExportData{
		Topic:              ConfigTopicProductAPIKeyRule,
		DataWithoutVersion: conf,
	}, nil
}

// convertAPIKeyRulesToBfeRules converts internal API key rules to BFE format
func convertAPIKeyRulesToBfeRules(oldRules []*APIKeyRule) []*TokenRuleFile {
	exportRules := make([]*TokenRuleFile, len(oldRules))
	for i, rule := range oldRules {
		newRule := &TokenRuleFile{
			Cond: &rule.Cond,
		}

		if len(rule.Actions) > 0 {
			newRule.Action = &ActionFile{
				Cmd: rule.Actions[0].Cmd,
			}
		}

		exportRules[i] = newRule
	}
	return exportRules
}

func (rlm *APIKeyRuleManager) fetchQuotaPlansWithEntityHierarchy(ctx context.Context, apiKey *api_key.APIKeyParam, productName string, collectedQuotaPlans map[string][]*QuotaPlan, entityMap map[string]*entpkg.EntityParam, quotaPlanMap map[int64]*quota.QuotaPlanParam, entityTypeMap map[string]*entpkg.EntityTypeParam) ([]string, []ApikeyTag, error) {
	quotaPlanIDs := make([]string, 0)
	tags := make([]ApikeyTag, 0)

	if apiKey.QuotaPlanID != nil && rlm.quotaPlanStorager != nil {
		if quotaPlan, ok := quotaPlanMap[*apiKey.QuotaPlanID]; ok && quotaPlan != nil && apiKey.Key != nil {
			// Skip API-Key own unlimited quota plans: they do not need to be
			// referenced in the token's quota_plans list.
			if quotaPlan.Unlimited == nil || !*quotaPlan.Unlimited {
				qp := convertQuotaPlanToExport(quotaPlan, *apiKey.Key, *apiKey.Key)
				if !containsQuotaPlan(collectedQuotaPlans, productName, qp.Id) {
					if _, ok := collectedQuotaPlans[productName]; !ok {
						collectedQuotaPlans[productName] = make([]*QuotaPlan, 0)
					}
					collectedQuotaPlans[productName] = append(collectedQuotaPlans[productName], qp)
				}
				quotaPlanIDs = append(quotaPlanIDs, qp.Id)
			}
		}
	}

	if apiKey.EntityID != nil && *apiKey.EntityID != "" && rlm.entityStorager != nil {
		entityQuotaPlanIDs, entityTags, err := rlm.fetchEntityQuotaPlanHierarchy(ctx, entityMap, quotaPlanMap, entityTypeMap, *apiKey.EntityID, productName, collectedQuotaPlans)
		if err != nil {
			return nil, nil, err
		}
		quotaPlanIDs = append(quotaPlanIDs, entityQuotaPlanIDs...)
		tags = append(tags, entityTags...)
	}

	return quotaPlanIDs, tags, nil
}

func (rlm *APIKeyRuleManager) fetchEntityQuotaPlanHierarchy(ctx context.Context, entityMap map[string]*entpkg.EntityParam, quotaPlanMap map[int64]*quota.QuotaPlanParam, entityTypeMap map[string]*entpkg.EntityTypeParam, entityID string, productName string, collectedQuotaPlans map[string][]*QuotaPlan) ([]string, []ApikeyTag, error) {
	quotaPlanIDs := make([]string, 0)
	tags := make([]ApikeyTag, 0)

	for depth := 0; depth < maxEntityHierarchyDepth; depth++ {
		entity, ok := entityMap[entityID]
		if !ok || entity == nil {
			break
		}

		if entity.EntityID != nil && entity.Type != nil && entity.Name != nil {
			tag := ApikeyTag{
				TagName:  *entity.Type,
				TagValue: *entity.Name,
			}

			// Query entity type level for TagLevel.
			// By business constraint, every entity type must exist and have a valid level.
			if rlm.entityTypeStorager != nil {
				entityType, ok := entityTypeMap[*entity.Type]
				if !ok || entityType == nil || entityType.Level == nil {
					return nil, nil, fmt.Errorf("entity type %s not found or level invalid", *entity.Type)
				}
				tag.TagLevel = *entityType.Level
			}

			tags = append(tags, tag)
		}

		if entity.QuotaPlanID != nil && rlm.quotaPlanStorager != nil {
			if quotaPlan, ok := quotaPlanMap[*entity.QuotaPlanID]; ok && quotaPlan != nil && entity.EntityID != nil {
				// Skip unlimited entity quota plans: they do not enforce any quota
				// and should not be referenced by the token.
				if quotaPlan.Unlimited == nil || !*quotaPlan.Unlimited {
					qp := convertQuotaPlanToExport(quotaPlan, *entity.EntityID, *entity.EntityID)
					if !containsQuotaPlan(collectedQuotaPlans, productName, qp.Id) {
						if _, ok := collectedQuotaPlans[productName]; !ok {
							collectedQuotaPlans[productName] = make([]*QuotaPlan, 0)
						}
						collectedQuotaPlans[productName] = append(collectedQuotaPlans[productName], qp)
					}
					quotaPlanIDs = append(quotaPlanIDs, qp.Id)
				}
			}
		}

		if entity.ParentID == nil || *entity.ParentID == "" {
			break
		}
		entityID = *entity.ParentID
	}

	return quotaPlanIDs, tags, nil
}

// fetchEntityModelHierarchy collects AllowModels and BlockModels from the entity hierarchy
// by walking parent_id pointers in the provided entityMap.
// Returns:
//   - intersectedAllowModels: intersection of all AllowModels from entities in the hierarchy (nil if any entity has empty AllowModels)
//   - unionBlockModels: union of all BlockModels from entities in the hierarchy
func (rlm *APIKeyRuleManager) fetchEntityModelHierarchy(ctx context.Context, entityMap map[string]*entpkg.EntityParam, entityID string) ([]string, []string, error) {
	var allAllowModels [][]string
	var allBlockModels []string

	for depth := 0; depth < maxEntityHierarchyDepth; depth++ {
		entity, ok := entityMap[entityID]
		if !ok || entity == nil {
			break
		}

		if len(entity.AllowModels) > 0 && !containsStar(entity.AllowModels) {
			allAllowModels = append(allAllowModels, entity.AllowModels)
		}
		if len(entity.BlockModels) > 0 && !containsStar(entity.BlockModels) {
			allBlockModels = append(allBlockModels, entity.BlockModels...)
		}

		if entity.ParentID == nil || *entity.ParentID == "" {
			break
		}
		entityID = *entity.ParentID
	}

	return intersectAllowModels(allAllowModels), allBlockModels, nil
}

// containsStar checks if the string slice contains "*"
func containsStar(slice []string) bool {
	for _, s := range slice {
		if s == "*" {
			return true
		}
	}
	return false
}

// intersectAllowModels computes the intersection of multiple AllowModels slices
// Returns nil if there are no AllowModels configured at any entity level
func intersectAllowModels(allAllowModels [][]string) []string {
	if len(allAllowModels) == 0 {
		return nil
	}

	result := make([]string, len(allAllowModels[0]))
	copy(result, allAllowModels[0])

	for _, models := range allAllowModels[1:] {
		result = intersectSlices(result, models)
	}

	return result
}

// intersectSlices returns the intersection of two string slices
func intersectSlices(a, b []string) []string {
	set := make(map[string]bool)
	for _, s := range b {
		set[s] = true
	}

	var result []string
	for _, s := range a {
		if set[s] {
			result = append(result, s)
		}
	}
	return result
}

func containsQuotaPlan(collectedQuotaPlans map[string][]*QuotaPlan, productName, id string) bool {
	qpList, ok := collectedQuotaPlans[productName]
	if !ok {
		return false
	}
	for _, qp := range qpList {
		if qp.Id == id {
			return true
		}
	}
	return false
}

func convertQuotaPlanToExport(qp *quota.QuotaPlanParam, id string, redisKeyID string) *QuotaPlan {
	result := &QuotaPlan{
		Id:          id,
		RedisKey:    fmt.Sprintf("QUOTA_%s", redisKeyID),
		Unlimited:   qp.Unlimited != nil && *qp.Unlimited,
		PassNoQuota: qp.PassWhenNoEnoughQuota != nil && *qp.PassWhenNoEnoughQuota,
		ExpiredTime: -1,
	}
	if qp.Quota != nil {
		result.Quota = golibquota.PtrToRedisValue(qp.Quota, qp.Unit)
	}
	if qp.Unit != nil {
		result.Unit = *qp.Unit
	}

	return result
}
