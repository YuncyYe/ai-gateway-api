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

package rdb

import (
	"context"
	"fmt"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ai_cache"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/epp_pool"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iai_route"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibasic"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/icluster_conf"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iintent_config"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ik8s_pool"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/imodel_price"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/imods"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprotocol"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iprovider"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ireport"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iroute_conf"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/quota"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/quotacache"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/rate_limit_policy"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/route_rules"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/dorisreport"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/mysqlreport"
	aiCacheStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/ai_cache"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/ai_route"
	apiKeyStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/api_key"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/auth"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/basic"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/cluster_conf"
	entityStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/entity"
	eppPoolStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/epp_pool"
	intentConfigStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/iintent_config"
	k8sPoolStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/k8s_pool"
	operationLogStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/model_price"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/protocol"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/provider"
	quotaStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/quota"
	rateLimitPolicyStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/rate_limit_policy"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/route_conf"
	routeRulesStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/route_rules"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/txn"
	trafficMirrorStorage "github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/traffic_mirror"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/version_control"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/entity"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/traffic_mirror"
)

func Init() error {
	container.TxnStoragerSingleton = txn.NewRDBTxnStorager(stateful.NewBFEDBContext)
	container.VersionControlStoragerSingleton = version_control.NewVersionControllerStorage(stateful.NewBFEDBContext)
	container.OperationLogStorager = operationLogStorage.NewOperationLogStorager(stateful.NewBFEDBContext)
	container.OperationLogManager = ioperlog.NewOperationLogManager(container.OperationLogStorager, 0)
	container.OperationLogManager.SetContextExtractor(operationLogContextExtractor)

	// Report query module (see design-docs
	// modifications/2026-09-15-report-query-api). Assembled only when
	// [Report].Backend is configured; otherwise ReportManager stays nil
	// and the /report/* routes are not registered.
	if err := initReport(); err != nil {
		return err
	}

	container.RouteRuleStoragerSingleton = route_conf.NewRouteRuleStorager(
		stateful.NewBFEDBContext,
		container.VersionControlStoragerSingleton)

	container.ProductStoragerSingleton = basic.NewProductManager(stateful.NewBFEDBContext)
	container.BFEClusterStoragerSingleton = basic.NewRDBBFEClusterStorager(stateful.NewBFEDBContext)
	container.PoolStoragerSingleton = cluster_conf.NewRDBPoolStorager(
		stateful.NewBFEDBContext,
		container.ProductStoragerSingleton)
	container.SubClusterStoragerSingleton = cluster_conf.NewRDBSubClusterStorager(
		stateful.NewBFEDBContext,
		container.PoolStoragerSingleton,
		container.ProductStoragerSingleton)
	container.ClusterStoragerSingleton = cluster_conf.NewRDBClusterStorager(
		stateful.NewBFEDBContext,
		container.SubClusterStoragerSingleton)

	container.ProviderStoragerSingleton = provider.NewRDBProviderStorager(stateful.NewBFEDBContext)
	container.ProviderManager = iprovider.NewProviderManager(
		container.TxnStoragerSingleton,
		container.ProviderStoragerSingleton)
	container.ProviderManager.SetOperationLogManager(container.OperationLogManager)

	container.APIKeyStorager = apiKeyStorage.NewAPIKeyStorager(
		stateful.NewBFEDBContext,
	)
	container.APIKeyIDGenerator = apiKeyStorage.NewRDBAPIKeyIDGenerator(
		stateful.NewBFEDBContext,
	)

	container.AIRouteRuleStorager = ai_route.NewRDBAIRouteRuleStorager(
		stateful.NewBFEDBContext,
	)
	container.CertificateStoragerSingleton = protocol.NewCertificateStorager(stateful.NewBFEDBContext)
	container.AuthenticateStoragerSingleton = auth.NewAuthenticateStorager(stateful.NewBFEDBContext)
	container.AuthorizeStoragerSingleton = auth.NewAuthorizeStorager(stateful.NewBFEDBContext,
		container.ProductStoragerSingleton,
		container.AuthenticateStoragerSingleton,
	)

	container.DomainStoragerSingleton = route_conf.NewDomainStorager(stateful.NewBFEDBContext)
	container.ExtraFileStoragerSingleton = basic.NewRDBExtraFileStorager(stateful.NewBFEDBContext)

	container.ExtraFileManager = ibasic.NewExtraFileManager(container.ExtraFileStoragerSingleton)
	container.VersionControlManager = iversion_control.NewVersionControllerManager(
		container.TxnStoragerSingleton,
		container.VersionControlStoragerSingleton)

	container.BFEClusterManager = ibasic.NewBFEClusterManager(
		container.TxnStoragerSingleton,
		container.BFEClusterStoragerSingleton)

	container.CertificateManager = iprotocol.NewCertificateManager(
		container.TxnStoragerSingleton,
		container.CertificateStoragerSingleton,
		container.VersionControlManager,
		container.ExtraFileStoragerSingleton)
	container.CertificateManager.SetOperationLogManager(container.OperationLogManager)

	container.ProductManager = ibasic.NewProductManager(
		container.TxnStoragerSingleton,
		container.ProductStoragerSingleton)

	container.AIRouteRuleManager = iai_route.NewAIRouteRuleManager(
		container.TxnStoragerSingleton,
		container.AIRouteRuleStorager,
		container.VersionControlManager,
		container.RouteRuleStoragerSingleton,
	)
	// Initialize model pricing before route rule manager so InnerAPI exports can
	// attach ModelTable to AIConf.
	container.ModelPriceStorager = model_price.NewRDBModelPriceStorager(stateful.NewBFEDBContext)
	container.ModelPriceManager = imodel_price.NewManager(
		container.TxnStoragerSingleton,
		container.ModelPriceStorager)
	container.ModelPriceManager.SetOperationLogManager(container.OperationLogManager)

	container.RouteRuleManager = iroute_conf.NewRouteRuleManager(
		container.TxnStoragerSingleton,
		container.RouteRuleStoragerSingleton,
		container.ClusterStoragerSingleton,
		container.ProductStoragerSingleton,
		container.VersionControlManager,
		container.DomainStoragerSingleton)
	container.RouteRuleManager.SetModelPriceStorager(container.ModelPriceStorager)
	container.RouteRuleManager.SetProviderStorager(container.ProviderStoragerSingleton)
	container.RouteRuleManager.SetOperationLogManager(container.OperationLogManager)

	// Initialize route rules components before cluster manager because the
	// cluster delete checker depends on RouteRulesManager.
	container.RouteRulesStorager = routeRulesStorage.NewRouteRulesStorager(stateful.NewBFEDBContext)
	container.RouteRulesManager = route_rules.NewRouteRulesManager(
		container.TxnStoragerSingleton,
		container.RouteRulesStorager)
	container.RouteRulesManager.SetOperationLogManager(container.OperationLogManager)

	container.ClusterManager = icluster_conf.NewClusterManager(
		container.TxnStoragerSingleton,
		container.ClusterStoragerSingleton,
		container.SubClusterStoragerSingleton,
		container.BFEClusterStoragerSingleton,
		container.PoolStoragerSingleton,
		container.ProviderStoragerSingleton,
		container.VersionControlManager,
		map[string]func(context.Context, *ibasic.Product, *icluster_conf.Cluster) error{
			"rules":       container.RouteRuleManager.ClusterDeleteChecker,
			"route_rules": container.RouteRulesManager.ClusterDeleteChecker,
		},
		map[string]func(context.Context, *ibasic.Product, *icluster_conf.Cluster, *icluster_conf.ClusterParam) error{
			"route_rules": container.RouteRulesManager.ClusterModelUpdateChecker,
		})
	container.ClusterManager.SetOperationLogManager(container.OperationLogManager)

	// K8s pool manager: maintains the k8s_pools table and the
	// k8s_instance_pool mirrors of referencing providers, propagating the
	// effective pool to derived cluster pools transactionally (k8s-pools.md).
	container.K8sPoolStorager = k8sPoolStorage.NewRDBK8sPoolStorager(stateful.NewBFEDBContext)
	container.K8sPoolManager = ik8s_pool.NewK8sPoolManager(
		container.TxnStoragerSingleton,
		container.K8sPoolStorager,
		container.ProviderStoragerSingleton,
		container.ClusterManager)

	// EPP pool manager: the cluster manager provides the EPPClusterSource
	// (balance_mode=EPP clusters and their raw epp_config); ManagerOptions are
	// read from the RunTime config. The reconciler lifecycle follows the
	// process lifecycle like QuotaResetScheduler (design-changes.md §4.3).
	container.EppPoolManager = epp_pool.NewEppPoolManager(
		container.TxnStoragerSingleton,
		eppPoolStorage.NewEppPoolStorager(stateful.NewBFEDBContext),
		container.ClusterManager,
		container.VersionControlManager,
		&epp_pool.ManagerOptions{
			PoolName:          stateful.DefaultConfig.RunTime.DefaultEPPInstancePoolName,
			ReconcileInterval: time.Duration(stateful.DefaultConfig.RunTime.EPPReconcileIntervalSeconds) * time.Second,
		})
	container.ClusterManager.SetEppPoolManager(container.EppPoolManager)
	container.RouteRuleManager.SetEPPAssignmentResolver(container.EppPoolManager)
	container.EppPoolManager.StartReconciler()

	container.SubClusterManager = icluster_conf.NewSubClusterManager(
		container.TxnStoragerSingleton,
		container.SubClusterStoragerSingleton,
		container.ProductStoragerSingleton,
		container.PoolStoragerSingleton,
		container.ClusterStoragerSingleton)

	container.DomainManager = iroute_conf.NewDomainManager(
		container.TxnStoragerSingleton,
		container.DomainStoragerSingleton,
		container.RouteRuleManager)
	container.DomainManager.SetOperationLogManager(container.OperationLogManager)

	container.AuthenticateManager = iauth.NewAuthenticateManager(
		container.TxnStoragerSingleton,
		container.AuthenticateStoragerSingleton,
		container.AuthorizeStoragerSingleton,
	)
	container.AuthenticateManager.SetOperationLogManager(container.OperationLogManager)
	container.AuthorizeManager = iauth.NewAuthorizeManager(
		container.TxnStoragerSingleton,
		container.AuthorizeStoragerSingleton)
	container.AuthorizeManager.SetOperationLogManager(container.OperationLogManager)

	container.PoolManager = icluster_conf.NewPoolManager(
		container.TxnStoragerSingleton,
		container.PoolStoragerSingleton,
		container.BFEClusterStoragerSingleton,
		container.SubClusterStoragerSingleton)

	// Initialize quota management components
	container.EntityTypeStorager = entityStorage.NewEntityTypeStorager(stateful.NewBFEDBContext)
	container.EntityStorager = entityStorage.NewEntityStorager(stateful.NewBFEDBContext)
	container.EntityIDGenerator = entityStorage.NewRDBEntityIDGenerator(stateful.NewBFEDBContext)
	container.QuotaPlanStorager = quotaStorage.NewQuotaPlanStorager(stateful.NewBFEDBContext)
	container.RateLimitPolicyStorager = rateLimitPolicyStorage.NewRateLimitPolicyStorager(stateful.NewBFEDBContext)

	container.QuotaCacheSingleton = quotacache.NewRedisQuotaCache(
		stateful.DefaultClientSet.RedisClient,
	)

	container.EntityTypeManager = entity.NewEntityTypeManager(
		container.TxnStoragerSingleton,
		container.EntityTypeStorager)
	container.EntityTypeManager.SetOperationLogManager(container.OperationLogManager)

	container.EntityManager = entity.NewEntityManager(
		container.TxnStoragerSingleton,
		container.EntityStorager,
		container.EntityTypeStorager,
		quota.NewQuotaPlanStoragerAdapter(container.QuotaPlanStorager),
		rate_limit_policy.NewRateLimitPolicyStoragerAdapter(container.RateLimitPolicyStorager),
		container.RouteRulesStorager,
		container.QuotaCacheSingleton)
	container.EntityManager.SetOperationLogManager(container.OperationLogManager)

	container.APIKeyRuleManager = imods.NewAPIKeyRuleManager(
		container.TxnStoragerSingleton,
		container.VersionControlManager,
		container.APIKeyStorager,
		container.AIRouteRuleStorager,
		container.QuotaPlanStorager,
		container.EntityStorager,
		container.EntityTypeStorager,
		container.QuotaCacheSingleton,
	)

	container.ModBodyProcessManager = imods.NewModBodyProcessManager(
		container.VersionControlManager,
	)

	container.QuotaPlanManager = quota.NewQuotaPlanManager(
		container.TxnStoragerSingleton,
		container.QuotaPlanStorager,
		container.APIKeyStorager,
		container.EntityStorager,
		container.QuotaCacheSingleton)
	container.QuotaPlanManager.SetOperationLogManager(container.OperationLogManager)

	container.RateLimitPolicyManager = rate_limit_policy.NewRateLimitPolicyManager(
		container.TxnStoragerSingleton,
		container.RateLimitPolicyStorager,
		container.APIKeyStorager,
		container.EntityStorager,
		container.VersionControlManager)
	container.RateLimitPolicyManager.SetOperationLogManager(container.OperationLogManager)

	container.AICacheStorager = aiCacheStorage.NewAICacheStorager(stateful.NewBFEDBContext)
	container.AICacheManager = ai_cache.NewAICacheManager(
		container.TxnStoragerSingleton,
		container.AICacheStorager,
		container.VersionControlManager,
		stateful.DefaultConfig.RunTime.AIRouteInnerProductName)
	container.AICacheManager.SetOperationLogManager(container.OperationLogManager)

	container.TrafficMirrorStorager = trafficMirrorStorage.NewTrafficMirrorStorager(stateful.NewBFEDBContext)
	container.TrafficMirrorManager = traffic_mirror.NewTrafficMirrorManager(
		container.TxnStoragerSingleton,
		container.TrafficMirrorStorager,
		container.VersionControlManager,
		stateful.DefaultConfig.RunTime.AIRouteInnerProductName)
	container.TrafficMirrorManager.SetOperationLogManager(container.OperationLogManager)

	container.IntentConfigStorager = intentConfigStorage.NewIntentConfigStorager(stateful.NewBFEDBContext)
	container.IntentConfigManager = iintent_config.NewIntentConfigManager(
		container.TxnStoragerSingleton,
		container.IntentConfigStorager,
		container.VersionControlManager)
	container.IntentConfigManager.SetOperationLogManager(container.OperationLogManager)

	// Wire nested-resource auditors so Entity/API Key nested quota-plan and
	// rate-limit-policy writes emit operation logs with resource_parent_id
	// filled (issue #161).
	container.EntityManager.SetQuotaPlanAuditor(container.QuotaPlanManager)
	container.EntityManager.SetRateLimitPolicyAuditor(container.RateLimitPolicyManager)

	container.AIRouteExporter = imods.NewAIRouteExporter(
		container.APIKeyStorager,
		container.EntityStorager,
		container.RouteRulesStorager,
		container.VersionControlManager)

	container.APIKeyManager = api_key.NewAPIKeyManager(
		container.TxnStoragerSingleton,
		container.APIKeyStorager,
		quota.NewQuotaPlanStoragerAdapter(container.QuotaPlanStorager),
		quota.NewRateLimitPolicyStoragerAdapter(container.RateLimitPolicyStorager),
		container.RouteRulesStorager,
		quota.NewEntityStoragerAdapter(container.EntityStorager),
		container.QuotaCacheSingleton,
	)
	container.APIKeyManager.SetOperationLogManager(container.OperationLogManager)
	container.APIKeyManager.SetQuotaPlanAuditor(container.QuotaPlanManager)
	container.APIKeyManager.SetRateLimitPolicyAuditor(container.RateLimitPolicyManager)

	// Initialize quota reset scheduler
	container.BalanceSyncManager = quota.NewBalanceSyncManager(
		container.TxnStoragerSingleton,
		container.APIKeyStorager,
		container.QuotaPlanStorager,
		container.EntityStorager,
		container.QuotaCacheSingleton,
		quota.NewRealClock())

	container.QuotaResetScheduler = quota.NewQuotaResetScheduler(
		container.TxnStoragerSingleton,
		container.BalanceSyncManager,
		quotacache.NewRedisDistributedLock(stateful.DefaultClientSet.RedisClient))

	container.QuotaResetScheduler.Start()

	// Ensure the global route table exists on startup.
	if err := container.RouteRulesManager.EnsureGlobalRouteRules(context.Background()); err != nil {
		return err
	}

	return nil
}

func initReport() error {
	cfg := stateful.DefaultConfig.Report
	if cfg.Backend == "" {
		// Pure-incremental default: no report assembly, /report/* stay 404.
		return nil
	}

	db, err := stateful.DbGet(cfg.Datasource)
	if err != nil {
		return err
	}

	switch cfg.Backend {
	case "mysql":
		container.ReportManager = ireport.NewReportManager(mysqlreport.New(db, cfg.Database, "mysql"))
		if cfg.EnableAggregateJob || cfg.EnablePartitionMgmt {
			interval := time.Duration(cfg.AggregateIntervalSec) * time.Second
			if !cfg.EnableAggregateJob {
				interval = 0
			}
			retentionDays := cfg.RetentionDays
			if !cfg.EnablePartitionMgmt {
				retentionDays = 0
			}
			job := mysqlreport.NewJob(db, cfg.Database, interval, retentionDays)
			job.Start()
		}
	case "doris":
		// Doris aggregation is maintained by the existing Doris insert job;
		// the api only queries. The backend identifier feeds Capabilities(),
		// which gates the cache/mirror/intent dimensions (phase 1: mysql
		// only, see design-docs modifications/2026-09-27-report-cache-mirror-intent-fields).
		container.ReportManager = ireport.NewReportManager(dorisreport.New(db, cfg.Database, "doris"))
	default:
		container.ReportManager = nil
		return fmt.Errorf("unsupported [Report].Backend: %s", cfg.Backend)
	}
	return nil
}

func operationLogContextExtractor(ctx context.Context, entry *ioperlog.OperationLogEntry) {
	if visitor, err := iauth.MustGetVisitor(ctx); err == nil && visitor != nil {
		entry.OperatorName = visitor.GetName()
		if visitor.User != nil {
			entry.OperatorType = ioperlog.OperatorTypeUser
			entry.OperatorID = visitor.User.ID
		} else if visitor.Token != nil {
			entry.OperatorType = ioperlog.OperatorTypeToken
			entry.OperatorID = visitor.Token.ID
		}
	}

	if reqInfo := xreq.GetRequestInfo(ctx); reqInfo != nil {
		entry.LogID = reqInfo.LogID
		entry.RequestPath = reqInfo.URLPath
		entry.RequestMethod = reqInfo.Method
		entry.ClientIP = reqInfo.ClientIP
		entry.UserAgent = reqInfo.UserAgent
	}
}
