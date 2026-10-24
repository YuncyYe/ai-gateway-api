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

package stateful

import (
	"os"
	"time"

	"github.com/bfenetworks/bfe/bfe_util/redis_client"
	"github.com/go-sql-driver/mysql"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
)

type ServerConfig struct {
	ServerAddr          string `validate:"ip"`             // service bind address, default 0.0.0.0
	ServerPort          int    `validate:"required,min=1"` // service port
	MonitorAddr         string `validate:"omitempty,ip"`   // monitor bind address, empty = all interfaces
	MonitorPort         int    // monitor port
	GracefulTimeOutInMs int    `validate:"required,min=1"` // time out setting for graceful shutdown
}

type RunTimeConfig struct {
	SessionExpireInDay      int  `validate:"required,min=1"`
	SkipTokenValidate       bool // skip user identify, you can open it when debug
	RecordSQL               bool
	StaticFilePath          string
	Debug                   bool
	AIRouteInnerProductName string // AI inner product name,default AI_product
	DefaultAIClusterName    string // default AI cluster name, e.g. "BFE-AI_product.szyf"

	// EPP scheduling integration (see model/epp_pool).
	DefaultEPPInstancePoolName  string // default EPP instance pool name, e.g. "EPP.pool"
	EPPReconcileIntervalSeconds int    // assignment reconciler period in seconds, default 30
}

// ReportConfig is the [Report] section. It switches the report query
// backend (mysql | doris | clickhouse | starrocks) and tunes the MySQL
// aggregate/partition jobs; when Backend is empty the report module is not
// assembled at all and the /report/* routes are not registered (see
// design-docs modifications/2026-09-15-report-query-api,
// modifications/2026-10-02-report-clickhouse-backend and
// modifications/2026-10-02-report-starrocks-backend).
type ReportConfig struct {
	Backend              string // mysql | doris | clickhouse | starrocks; empty disables the module
	Datasource           string // name of a [Databases.*] entry
	Database             string // optional schema override for table names
	EnableAggregateJob   bool   // backend=mysql: run the minute aggregate job
	AggregateIntervalSec int    // aggregate period, default 60
	RetentionDays        int    // retention window of details, default 7
	EnablePartitionMgmt  bool   // backend=mysql: run the partition management job
}

// BatchJobConfig is the [BatchJob] section. It switches the batch reconcile
// job (Redis BATCH_* sync -> status advance -> backstop settle, see
// design-docs/sys-design/details/批量任务与对账.md §5). The job needs the
// control-plane -> provider egress for duties 2/3; deployments without
// egress leave it disabled (degraded path, 批量任务与对账.md §9).
type BatchJobConfig struct {
	Enable      bool // run the reconcile job; default off
	IntervalSec int  // tick period, default 60
}

// applyDefaults fills the default calibration values.
func (c *ReportConfig) applyDefaults() {
	if c.AggregateIntervalSec <= 0 {
		c.AggregateIntervalSec = 60
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = 7
	}
}

func (c *BatchJobConfig) applyDefaults() {
	if c.IntervalSec <= 0 {
		c.IntervalSec = 60
	}
}

type Config struct {
	Server        ServerConfig
	Loggers       map[string]*LoggerConfig `validate:"dive"`
	Databases     map[string]*DbConfig     `validate:"dive"`
	Depends       DependsConfig
	RunTime       RunTimeConfig
	Report        ReportConfig
	BatchJob      BatchJobConfig
	AccessControl AccessControlConf
	Security      SecurityConfig

	Vars      map[string]string
	LogDir    string
	ConfigDir string
	RedisConf *Redis
}

type ClientSet struct {
	RedisClient redis_client.Client
}

var DefaultConfig *Config
var DefaultClientSet *ClientSet

func LoadConfig(file string) error {
	config := &Config{
		Depends: DependsConfig{
			NavTreeFile: "${conf_dir}/nav_tree.toml",
			I18nDir:     "${conf_dir}/i18n",
		},
		RunTime: RunTimeConfig{
			StaticFilePath: "./static",
		},
		Vars: map[string]string{},
		Databases: map[string]*DbConfig{
			"bfe_db": {
				Config: mysql.Config{
					Loc: time.Local,
				},
			},
		},
	}

	if err := lib.LoadConfAuto(file, config); err != nil {
		return err
	}
	confFilePath = file

	config.Report.applyDefaults()
	config.BatchJob.applyDefaults()

	if err := newConfigValidator().Struct(config); err != nil {
		return err
	}

	// Compile the management-plane IP whitelist; the compiled form is the
	// runtime source of truth (swapped atomically by reload).
	cc, err := CompileAccessControl(&config.AccessControl)
	if err != nil {
		return err
	}
	StoreCompiledAccessControl(cc)

	// Load the secret keyring (fail-fast if configured but invalid). An
	// empty MasterKeyFile stores a nil ring (encryption disabled).
	if err := loadSecretRing(&config.Security); err != nil {
		return err
	}

	// Load the conf-file keyring for export encryption (fail-fast when the
	// switch is on; warning-only when off and the file is not usable yet).
	if err := loadExportSecretRing(&config.Security); err != nil {
		return err
	}

	// Validate and inject the users.password bcrypt cost factor (fail-fast
	// on out-of-range values).
	if err := loadPasswordHashCost(&config.Security); err != nil {
		return err
	}

	DefaultConfig = config
	return nil
}

func (config *Config) Init() error {
	mapping := func(k string) string {
		return config.Vars[k]
	}

	config.Depends.NavTreeFile = os.Expand(config.Depends.NavTreeFile, mapping)
	config.Depends.I18nDir = os.Expand(config.Depends.I18nDir, mapping)

	return nil
}

var (
	BFEProductID int64 = 1

	IgnoreBNSStatusCheck bool
)
