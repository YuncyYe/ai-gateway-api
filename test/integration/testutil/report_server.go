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

package testutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Register the ClickHouse database/sql driver (clickhouse-go v2 stdlib
	// mode, native protocol) for the clickhouse report datasource.
	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/go-sql-driver/mysql"
)

// ErrReportMySQLDSNNotSet 由 StartReportServer 在环境变量 REPORT_MYSQL_DSN
// 未设置时返回；TestMain 应打印提示后以 0 退出（整个用例组 Skip）。
var ErrReportMySQLDSNNotSet = errors.New("REPORT_MYSQL_DSN not set")

// ReportServer 封装报表集成测试的装配状态：专用随机名数据库、种子连接与 api 进程。
type ReportServer struct {
	Server    *ServerManager
	DB        *sql.DB
	ServerDSN string // user:pass@tcp(addr)/，用于清理时重建连接
	DBName    string
	// CHServerDSN 非空表示 ClickHouse 数据源（clickhouse://user:pass@host:port，
	// 不带库名，管理连接固定落 default 库）：Close 时用该 DSN 重建连接并
	// DROP DATABASE DBName。MySQL 形态下为空串，Close 行为不变。
	CHServerDSN string
}

// Close 停止 api 进程、关闭种子连接并 DROP 测试数据库。
func (s *ReportServer) Close() {
	if s.Server != nil {
		s.Server.Shutdown()
	}
	if s.DB != nil {
		s.DB.Close()
	}
	if s.CHServerDSN != "" && s.DBName != "" {
		serverDB, err := sql.Open("clickhouse", s.CHServerDSN)
		if err == nil {
			serverDB.Exec("DROP DATABASE IF EXISTS " + s.DBName)
			serverDB.Close()
		}
	}
	if s.ServerDSN != "" && s.DBName != "" {
		serverDB, err := sql.Open("mysql", s.ServerDSN)
		if err == nil {
			serverDB.Exec("DROP DATABASE IF EXISTS " + s.DBName)
			serverDB.Close()
		}
	}
}

// StartReportServer 装配报表查询集成测试环境（Backend=mysql）：
//  1. 读取 REPORT_MYSQL_DSN（格式 user:pass@tcp(host:port)/，允许不带库名）；
//  2. 创建专用随机名数据库 report_it_<ns>，套用项目 db_ddl_report_mysql.sql
//    （${INIT_DATE} 替换为今天+3 天，历史时间戳落入 p_init 分区）；
//  3. 依次执行 seedSQL 中的种子语句；
//  4. 注入 [Databases.report_db] + [Report]（聚合/分区 JOB 关闭）并启动 api 进程。
//
// 任一失败时清理已创建的资源并返回错误。
func StartReportServer(seedSQL ...string) (*ReportServer, error) {
	return StartReportServerWithBackend("mysql", seedSQL...)
}

// StartReportServerWithBackend 与 StartReportServer 相同，但 [Report].Backend
// 按参数装配（如 "doris" 门控用例：数据源仍是 MySQL 实例，仅 backend 标识不同，
// 422 在查询层拦截、不触达 SQL）。
func StartReportServerWithBackend(backend string, seedSQL ...string) (*ReportServer, error) {
	dsn := os.Getenv("REPORT_MYSQL_DSN")
	if dsn == "" {
		return nil, ErrReportMySQLDSNNotSet
	}

	cfg, err := parseReportServerDSN(dsn)
	if err != nil {
		return nil, err
	}

	s := &ReportServer{
		ServerDSN: fmt.Sprintf("%s:%s@tcp(%s)/", cfg.user, cfg.pass, cfg.addr),
		DBName:    fmt.Sprintf("report_it_%d", time.Now().UnixNano()),
	}

	serverDB, err := sql.Open("mysql", s.ServerDSN)
	if err != nil {
		return nil, err
	}
	if _, err := serverDB.Exec("DROP DATABASE IF EXISTS " + s.DBName); err != nil {
		serverDB.Close()
		return nil, err
	}
	if _, err := serverDB.Exec("CREATE DATABASE " + s.DBName); err != nil {
		serverDB.Close()
		return nil, fmt.Errorf("create test database failed (check REPORT_MYSQL_DSN privileges): %w", err)
	}
	serverDB.Close()

	s.DB, err = sql.Open("mysql", s.ServerDSN+s.DBName)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := applyReportDDL(s.DB); err != nil {
		s.Close()
		return nil, fmt.Errorf("apply report ddl: %w", err)
	}
	for _, stmt := range seedSQL {
		if _, err := s.DB.Exec(stmt); err != nil {
			s.Close()
			return nil, fmt.Errorf("seed report data: %w", err)
		}
	}

	extraTOML := fmt.Sprintf(`
[Databases.report_db]
Driver = "mysql"
DBName = "%s"
Addr = "%s"
User = "%s"
Passwd = %s
Net = "tcp"
MaxOpenConns = 10
MaxIdleConns = 5

[Report]
Backend = "%s"
Datasource = "report_db"
EnableAggregateJob = false
EnablePartitionMgmt = false
`, s.DBName, cfg.addr, cfg.user, tomlLiteral(cfg.pass), backend)

	s.Server, err = StartServerWithExtraConfig(extraTOML)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// clickhouseDDLFiles 是报表查询所需的无 Kafka 依赖建表资产（相对 ddlDir），
// 顺序即执行顺序：建库、明细表、聚合表。Kafka 引擎表与消费/聚合 MV 由数仓侧
// 维护，不在报表查询测试范围（真实 CH 实例上 Kafka 不可达会产生后台重连噪音）。
var clickhouseDDLFiles = []string{
	"bfe_observability.sql",
	"bfe_ai_request_log.sql",
	"bfe_ai_metrics_1m.sql",
}

// PingClickHouse 探测 ClickHouse 可达性：chDSN 中的库名可能尚不存在（或指向
// 未建的业务库），故管理连接固定落 default 库（其恒存在），5 秒超时。
func PingClickHouse(chDSN string) error {
	adminDSN, err := clickHouseAdminDSN(chDSN)
	if err != nil {
		return err
	}
	db, err := sql.Open("clickhouse", adminDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// realInstanceReportConfig 是真实实例报表服务（ClickHouse / StarRocks）
// 共享装配骨架 startRealInstanceReportServer 的参数：临时库 + DDL 目录资产 +
// 种子 + api 进程。两个真实实例后端仅 DSN 形态、占位符集合与 TOML 驱动
// 不同，清理语义一致（DROP 临时库级联删表）。
type realInstanceReportConfig struct {
	dbName       string // 专用随机名临时库（调用方生成，Close 按名 DROP）
	ddlFiles     []string
	driver       string // sql driver 名，兼作 [Databases.report_db] Driver（clickhouse/mysql）
	net          string // mysql 形态必须显式 "tcp"：mysql.Config.Net 零值经 FormatDSN 会连地址段一起丢弃，驱动回退默认 127.0.0.1:3306
	backend      string // [Report].Backend 装配值
	addr         string // [Databases.report_db] Addr
	user         string // [Databases.report_db] User
	pass         string // [Databases.report_db] Passwd
	allowNativePasswords bool // mysql driver 形态必须显式开启（StarRocks FE 与 Doris 同为 mysql_native_password 认证，mysql.Config.FormatDSN 零值序列化会静默关闭该选项）
	interpolateParams bool  // StarRocks FE 必须显式开启：FE（MySQL 协议重实现）对 COM_STMT 二进制行包的编码存在缺陷——JSON 列与 NULL 列相邻的行会触发解析错位（v1.6.0 静默串位、v1.9.3 在 packets.go readRow panic，均为 FE 行包畸形所致，与驱动版本无关；2030 种子行 1004 稳定复现，列子集二分定位见 design-docs/modifications/2026-10-02-report-mysql-driver-upgrade/change-summary.md）。插值参数走 COM_QUERY 文本协议后 FE 编码正确
	adminDSN     string   // 服务器级 DSN（不带库名），建库/清理重建连接用
	seedDSN      func(dbName string) string            // 含临时库名的 DDL/种子连接 DSN
	placeholders func(dbName string) map[string]string // DDL ${VAR} 替换表
}

// startRealInstanceReportServer 是 StartClickHouseReportServer 与
// StartStarRocksReportServer 的共享实现：
//  1. 以 adminDSN 建立管理连接，建专用随机名临时库
//     （DROP DATABASE IF EXISTS + CREATE DATABASE）；
//  2. 切到临时库连接，按顺序执行 ddlDir 下 ddlFiles 中的建表 SQL
//     （placeholders 占位符做最小替换；残留 ${VAR} 视为资产漂移报错，
//     防止误执行含 Kafka 占位的文件）；
//  3. 依次执行 seedSQL 中的种子语句（方言字面量由调用方给出）；
//  4. 注入 [Databases.report_db] + [Report] Backend 并启动 api 进程。
//
// 任一失败时清理已创建的资源并返回错误。Close 负责停进程并 DROP 临时库
// （级联删除库内表），调用方应以 t.Cleanup(rs.Close) 注册。
func startRealInstanceReportServer(cfg *realInstanceReportConfig, ddlDir string, seedSQL ...string) (*ReportServer, error) {
	s := &ReportServer{DBName: cfg.dbName}
	// Close 的清理通道按 driver 区分：clickhouse 走 CHServerDSN（重建
	// clickhouse 连接 DROP），mysql 形态（StarRocks）走 ServerDSN。
	if cfg.driver == "clickhouse" {
		s.CHServerDSN = cfg.adminDSN
	} else {
		s.ServerDSN = cfg.adminDSN
	}

	adminDB, err := sql.Open(cfg.driver, cfg.adminDSN)
	if err != nil {
		return nil, err
	}
	if _, err := adminDB.Exec("DROP DATABASE IF EXISTS " + s.DBName); err != nil {
		adminDB.Close()
		return nil, fmt.Errorf("drop %s test database: %w", cfg.backend, err)
	}
	if _, err := adminDB.Exec("CREATE DATABASE " + s.DBName); err != nil {
		adminDB.Close()
		return nil, fmt.Errorf("create %s test database failed (check instance DSN privileges): %w", cfg.backend, err)
	}
	adminDB.Close()

	s.DB, err = sql.Open(cfg.driver, cfg.seedDSN(s.DBName))
	if err != nil {
		s.Close()
		return nil, err
	}
	for _, file := range cfg.ddlFiles {
		data, err := os.ReadFile(filepath.Join(ddlDir, file))
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("read %s ddl %s: %w", cfg.backend, file, err)
		}
		script := string(data)
		for k, v := range cfg.placeholders(s.DBName) {
			script = strings.ReplaceAll(script, k, v)
		}
		if strings.Contains(script, "${") {
			s.Close()
			return nil, fmt.Errorf("%s ddl %s contains unreplaced ${VAR} placeholder (asset drift, kafka assets are not supported)", cfg.backend, file)
		}
		// 整文件单条 Exec：StarRocks FE 的服务端分句可处理资产头注释
		// （mysql CLI 的客户端分句反而会触发其 `-- ` 注释解析怪癖）。
		if _, err := s.DB.Exec(script); err != nil {
			s.Close()
			return nil, fmt.Errorf("apply %s ddl %s: %w", cfg.backend, file, err)
		}
	}
	for _, stmt := range seedSQL {
		if _, err := s.DB.Exec(stmt); err != nil {
			s.Close()
			return nil, fmt.Errorf("seed report data: %w", err)
		}
	}

	allowNative := ""
	if cfg.allowNativePasswords {
		allowNative = "\nAllowNativePasswords = true"
	}
	interpolate := ""
	if cfg.interpolateParams {
		interpolate = "\nInterpolateParams = true"
	}
	netLine := ""
	if cfg.net != "" {
		netLine = fmt.Sprintf("\nNet = \"%s\"", cfg.net)
	}
	extraTOML := fmt.Sprintf(`
[Databases.report_db]
Driver = "%s"
DBName = "%s"
Addr = "%s"
User = "%s"
Passwd = %s%s%s%s
MaxOpenConns = 10
MaxIdleConns = 5

[Report]
Backend = "%s"
Datasource = "report_db"
EnableAggregateJob = false
EnablePartitionMgmt = false
`, cfg.driver, s.DBName, cfg.addr, cfg.user, tomlLiteral(cfg.pass), netLine, allowNative, interpolate, cfg.backend)

	s.Server, err = StartServerWithExtraConfig(extraTOML)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// StartClickHouseReportServer 装配报表查询集成测试环境（Backend=clickhouse，
// 数据源为真实 ClickHouse 实例，native TCP）：
//  1. 以 chDSN（clickhouse-go/v2 stdlib DSN，形如
//     clickhouse://user:pass@host:9000[/db][?params]，库名段会被忽略）建立
//     管理连接（落 default 库），建专用随机名数据库 report_ch_it_<ns>
//     （DROP DATABASE IF EXISTS + CREATE DATABASE）；
//  2. 切到临时库连接，按顺序执行 ddlDir 下 clickhouseDDLFiles 中的建表 SQL
//     （仅 ${CLICKHOUSE_DATABASE} 占位符做最小替换，规则同
//     ai-gateway-observability/clickhouse/setup.sh；残留 ${VAR} 视为资产漂移
//     报错，防止误执行含 Kafka 占位的文件）；
//  3. 依次执行 seedSQL 中的种子语句（ClickHouse 方言字面量，由调用方给出）；
//  4. 注入 [Databases.report_db]（Driver="clickhouse"，DBName=临时库）+
//     [Report] Backend="clickhouse" 并启动 api 进程。
//
// 任一失败时清理已创建的资源并返回错误。Close 负责停进程并 DROP 临时库
// （级联删除库内表），调用方应以 t.Cleanup(rs.Close) 注册。
func StartClickHouseReportServer(chDSN, ddlDir string, seedSQL ...string) (*ReportServer, error) {
	u, err := url.Parse(chDSN)
	if err != nil || u.Scheme != "clickhouse" || u.Host == "" {
		return nil, fmt.Errorf("invalid clickhouse dsn %q: scheme/host required", chDSN)
	}
	user := u.User.Username()
	pass, _ := u.User.Password()
	if user == "" {
		return nil, fmt.Errorf("invalid clickhouse dsn %q: user required", chDSN)
	}
	adminDSN, err := clickHouseAdminDSN(chDSN)
	if err != nil {
		return nil, err
	}

	// clickhouse-go 要求 DSN 中的库存在，管理连接固定落 default 库。
	return startRealInstanceReportServer(&realInstanceReportConfig{
		dbName:  fmt.Sprintf("report_ch_it_%d", time.Now().UnixNano()),
		ddlFiles: clickhouseDDLFiles,
		driver:  "clickhouse",
		backend: "clickhouse",
		addr:    u.Host,
		user:    user,
		pass:    pass,
		adminDSN: adminDSN,
		seedDSN: func(dbName string) string {
			seedURL := *u
			seedURL.Path = "/" + dbName
			return seedURL.String()
		},
		placeholders: func(dbName string) map[string]string {
			return map[string]string{"${CLICKHOUSE_DATABASE}": dbName}
		},
	}, ddlDir, seedSQL...)
}

// starrocksDDLFiles 是报表查询所需的 StarRocks 建表资产（相对 ddlDir），
// 顺序即执行顺序：建库、明细表、异步物化视图。Routine Load
// （bfe_ai_log_load_routine.sql）依赖 Kafka，不在报表查询测试范围（与
// clickhouseDDLFiles 同理，真实实例上 Kafka 不可达会产生后台重连噪音）。
var starrocksDDLFiles = []string{
	"bfe_observability.sql",
	"bfe_ai_request_log.sql",
	"bfe_ai_metrics_1m.sql",
}

// starrocksInitPartitionDate 是 bfe_ai_request_log.sql 中
// ${INIT_PARTITION_DATE}（p_init 分区上界）的替换值。种子时间平移到
// 2030-09-15（规避物化视图 partition_ttl 7 天，见 starrocks_seed_test.go
// 文件头），而基表动态分区只维护 [今天-7, 今天+3]——2030 年的行必须落入
// 上界为其后一天的 p_init，否则插入报无分区可用。
const starrocksInitPartitionDate = "2030-09-16"

// PingStarRocks 探测 StarRocks FE 可达性：srDSN 中的库名段可能尚不存在，
// 故管理连接固定为不带库名的服务器级 DSN，5 秒超时。
func PingStarRocks(srDSN string) error {
	cfg, err := parseReportServerDSN(srDSN)
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/", cfg.user, cfg.pass, cfg.addr))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// StartStarRocksReportServer 装配报表查询集成测试环境（Backend=starrocks，
// 数据源为真实 StarRocks 实例，经 FE 的 MySQL 协议端口直连，零新驱动）：
//  1. 以 srDSN（go-sql-driver DSN，形如 root:pass@tcp(127.0.0.1:9030)/，
//     库名段允许为空）建服务器级管理连接，建专用随机名库 report_sr_it_<ns>
//     （DROP DATABASE IF EXISTS + CREATE DATABASE）；
//  2. 套用 ddlDir 下 starrocksDDLFiles 三份资产（${STARROCKS_DATABASE} 与
//     ${INIT_PARTITION_DATE} 占位符替换，规则同
//     ai-gateway-observability/starrocks/setup.sh）；
//  3. 执行 seedSQL——只灌基表 bfe_ai_request_log：bfe_ai_metrics_1m 是异步
//     物化视图（information_schema.tables 中 TABLE_TYPE=VIEW，
//     REFRESH ASYNC EVERY 1 MINUTE），直插被 FE 拒绝（实机报 "The data of
//     'bfe_ai_metrics_1m' cannot be inserted because ... is a materialized
//     view"），聚合数据只能由 MV 从基表聚合，端到端 1~2 分钟（实测约 34s）；
//  4. 轮询物化视图行数达到 expectMVRows 后，注入
//     [Databases.report_db]（Driver="mysql"；Net="tcp"、
//     AllowNativePasswords = true、InterpolateParams = true 三项均须显式
//     给出：前两项为 mysql.Config 零值陷阱，第三项规避 SR FE 二进制行包
//     缺陷，详见 realInstanceReportConfig 字段注释）+ [Report]
//     Backend="starrocks" 并启动 api 进程。
//
// 任一失败时清理已创建的资源并返回错误。Close 负责停进程并 DROP 临时库
// （SR DROP DATABASE 级联删除库内表与物化视图），调用方应以
// t.Cleanup(rs.Close) 注册。
func StartStarRocksReportServer(srDSN, ddlDir string, expectMVRows int, seedSQL ...string) (*ReportServer, error) {
	dsnCfg, err := parseReportServerDSN(srDSN)
	if err != nil {
		return nil, err
	}
	serverDSN := fmt.Sprintf("%s:%s@tcp(%s)/", dsnCfg.user, dsnCfg.pass, dsnCfg.addr)

	// SR FE 经 mysql driver 直连：Net 必须显式 "tcp"（FormatDSN 对零值
	// Net 会连地址段一起丢弃，驱动回退默认 127.0.0.1:3306）；
	// AllowNativePasswords 必须显式开启（SR FE 与 Doris 同为
	// mysql_native_password 认证）；InterpolateParams 必须显式开启——
	// 驱动 v1.9.3 下 FE 的二进制行包仍会在 JSON 列与 NULL 列相邻的行上
	// 触发解析错位（packets.go readRow panic），属 FE 侧协议实现缺陷，
	// 与驱动版本无关，只有文本协议（参数客户端插值）可规避。
	s, err := startRealInstanceReportServer(&realInstanceReportConfig{
		dbName:               fmt.Sprintf("report_sr_it_%d", time.Now().UnixNano()),
		ddlFiles:             starrocksDDLFiles,
		driver:               "mysql",
		net:                  "tcp",
		backend:              "starrocks",
		addr:                 dsnCfg.addr,
		user:                 dsnCfg.user,
		pass:                 dsnCfg.pass,
		allowNativePasswords: true,
		interpolateParams:    true,
		adminDSN:             serverDSN,
		seedDSN: func(dbName string) string {
			return serverDSN + dbName
		},
		placeholders: func(dbName string) map[string]string {
			return map[string]string{
				"${STARROCKS_DATABASE}":  dbName,
				"${INIT_PARTITION_DATE}": starrocksInitPartitionDate,
			}
		},
	}, ddlDir, seedSQL...)
	if err != nil {
		return nil, err
	}

	// 异步 MV 端到端 1~2 分钟：行数达标前聚合端点断言必失败，须先等刷新。
	if err := awaitStarRocksMV(s.DB, expectMVRows); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// awaitStarRocksMV 轮询等待异步物化视图 bfe_ai_metrics_1m 刷新到期望行数
//（临时库独占该视图，COUNT(*) 即种子聚合行数）。刷新周期 1 分钟，上限
// 150 秒（约 2.5 个周期，本机 3.5.21 实测约 34 秒）。
func awaitStarRocksMV(db *sql.DB, expectRows int) error {
	if expectRows <= 0 {
		return nil
	}
	deadline := time.Now().Add(150 * time.Second)
	var lastErr error
	for {
		var n int
		lastErr = db.QueryRow("SELECT COUNT(*) FROM bfe_ai_metrics_1m").Scan(&n)
		if lastErr == nil && n >= expectRows {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("starrocks MV bfe_ai_metrics_1m did not reach %d rows within 150s (last err: %v)", expectRows, lastErr)
		}
		time.Sleep(2 * time.Second)
	}
}

// clickHouseAdminDSN 把 chDSN 的库名段替换为 default（管理连接/清理重建连接用；
// clickhouse-go 要求 DSN 中的库存在，default 恒存在）。
func clickHouseAdminDSN(chDSN string) (string, error) {
	u, err := url.Parse(chDSN)
	if err != nil || u.Scheme != "clickhouse" || u.Host == "" {
		return "", fmt.Errorf("invalid clickhouse dsn %q: scheme/host required", chDSN)
	}
	u.Path = "/default"
	return u.String(), nil
}

type reportServerConfig struct {
	user string
	pass string
	addr string
}

// parseReportServerDSN 解析 "user:pass@tcp(host:port)/" 形式的服务器级 DSN
// （允许不带库名；忽略 query 参数）。
func parseReportServerDSN(dsn string) (*reportServerConfig, error) {
	cfg := &reportServerConfig{}

	at := strings.LastIndex(dsn, "@")
	if at <= 0 {
		return nil, fmt.Errorf("missing userinfo: %q", dsn)
	}
	userinfo := dsn[:at]
	rest := dsn[at+1:]

	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		cfg.user = userinfo
	} else {
		cfg.user = userinfo[:colon]
		cfg.pass = userinfo[colon+1:]
	}
	if cfg.user == "" {
		return nil, fmt.Errorf("empty user")
	}

	if !strings.HasPrefix(rest, "tcp(") {
		return nil, fmt.Errorf("only tcp(host:port) network is supported: %q", dsn)
	}
	end := strings.Index(rest, ")")
	if end < 0 {
		return nil, fmt.Errorf("malformed network: %q", dsn)
	}
	cfg.addr = rest[len("tcp("):end]
	if cfg.addr == "" {
		return nil, fmt.Errorf("empty addr")
	}
	return cfg, nil
}

// applyReportDDL 套用项目根 db_ddl_report_mysql.sql，${INIT_DATE} 替换为今天+3 天。
func applyReportDDL(db *sql.DB) error {
	ddlPath, err := findRepoFile("db_ddl_report_mysql.sql")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(ddlPath)
	if err != nil {
		return err
	}
	script := strings.ReplaceAll(string(data), "${INIT_DATE}",
		time.Now().AddDate(0, 0, 3).Format("2006-01-02"))

	for _, stmt := range splitSQLStatements(script) {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("apply ddl: %w\nstmt: %s", err, stmt)
		}
	}
	return nil
}

// splitSQLStatements 按分号切分 SQL 脚本（跳过 -- 行注释）。
func splitSQLStatements(script string) []string {
	var stmts []string
	var sb strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		sb.WriteString(line)
		sb.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			stmts = append(stmts, strings.TrimSpace(sb.String()))
			sb.Reset()
		}
	}
	if rest := strings.TrimSpace(sb.String()); rest != "" {
		stmts = append(stmts, rest)
	}
	return stmts
}

// findRepoFile 从当前目录向上查找项目根目录中的文件。
func findRepoFile(name string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	startDir := dir
	for {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s not found from %s upwards", name, startDir)
		}
		dir = parent
	}
}

// tomlLiteral 生成 TOML 字符串字面量（无特殊字符时用 literal string，避免转义差异）。
func tomlLiteral(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, "'\"\\") {
		return "'" + s + "'"
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
