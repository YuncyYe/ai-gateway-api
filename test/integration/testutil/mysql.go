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
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// StartServerWithMySQL 以 MySQL 为后端启动测试服务器，供 //go:build mysql
// 并发用例使用（SQLite 单连接串行无法暴露 ID 生成竞态与行锁超时，
// issue #80 / #99 / #132）。
//
// adminDSN 为不带库名的管理员连接串，形如 "root:pass@tcp(127.0.0.1:3306)/"
// （从 AIAPI_MYSQL_DSN 环境变量读取）。流程：
//  1. CREATE DATABASE ai_gateway_it_<pid>_<rand>
//  2. 执行项目 db_ddl.sql（含默认产品线种子）
//  3. 写入 MySQL 版种子数据（对齐 SeedTestData）
//  4. 以 Driver=mysql 启动 ai-gateway-api 子进程
//
// 返回的 ServerManager 不接管全局 client（调用方应以 sm.ServerURL 自建
// Client）；Shutdown 时自动 DROP DATABASE。
func StartServerWithMySQL(adminDSN string) (*ServerManager, error) {
	return startServerWithMySQL(adminDSN, "", 0)
}

// startServerWithMySQL 是 MySQL 后端的统一入口：monitorPortMode 语义同
// startServer（1=启用随机监控端口），extraTOML 追加在默认配置之后。
// 每次调用创建独立数据库，Shutdown 时 DROP。
func startServerWithMySQL(adminDSN, extraTOML string, monitorPortMode int) (*ServerManager, error) {
	dbName, err := provisionMySQLDatabase(adminDSN)
	if err != nil {
		return nil, err
	}
	section, err := mysqlDBSection(adminDSN, dbName)
	if err != nil {
		dropMySQLDatabase(adminDSN, dbName)
		return nil, err
	}

	sm, err := startServer(nil, "", extraTOML, &dbPatch{driver: "mysql", section: section}, monitorPortMode)
	if err != nil {
		dropMySQLDatabase(adminDSN, dbName)
		return nil, err
	}
	sm.mysqlAdminDSN = adminDSN
	sm.mysqlDBName = dbName
	return sm, nil
}

// defaultMySQLAdminDSN 是本地开发默认探测的管理员 DSN（root 无口令）。
const defaultMySQLAdminDSN = "root@tcp(127.0.0.1:3306)/"

// resolveMySQLAdminDSN 决定默认入口（StartServer/StartServerWithMonitor/
// StartServerWithSharedInfra 等）使用的后端。优先级：
//  1. AIAPI_FORCE_SQLITE=1 → 回退 SQLite（显式保留旧行为，供无 MySQL 的环境/CI）；
//  2. AIAPI_MYSQL_DSN（管理员 DSN，不带库名，形如 root:pass@tcp(host:port)/）；
//  3. 探测 defaultMySQLAdminDSN（本地 root 无口令实例）可达则用 MySQL，否则回退 SQLite。
func resolveMySQLAdminDSN() string {
	if v := strings.TrimSpace(os.Getenv("AIAPI_FORCE_SQLITE")); v == "1" || strings.EqualFold(v, "true") {
		return ""
	}
	if d := strings.TrimSpace(os.Getenv("AIAPI_MYSQL_DSN")); d != "" {
		return d
	}
	db, err := sql.Open("mysql", defaultMySQLAdminDSN)
	if err != nil {
		return ""
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return ""
	}
	return defaultMySQLAdminDSN
}

// provisionMySQLDatabase 创建独立测试库：建库 → 执行项目 db_ddl.sql →
// 写入 MySQL 版种子数据（对齐 SQLite 的 SeedTestData）。
func provisionMySQLDatabase(adminDSN string) (string, error) {
	if strings.TrimSpace(adminDSN) == "" {
		return "", fmt.Errorf("adminDSN is empty")
	}
	if !strings.HasSuffix(adminDSN, "/") {
		return "", fmt.Errorf("adminDSN must end with '/': %q", adminDSN)
	}

	projectRoot, err := findProjectRoot()
	if err != nil {
		return "", err
	}

	dbName := fmt.Sprintf("ai_gateway_it_%d_%d", os.Getpid(), time.Now().UnixNano()%100000)

	adminDB, err := sql.Open("mysql", adminDSN)
	if err != nil {
		return "", fmt.Errorf("open mysql admin: %w", err)
	}
	defer adminDB.Close()

	if _, err := adminDB.Exec(fmt.Sprintf("CREATE DATABASE `%s`", dbName)); err != nil {
		return "", fmt.Errorf("create database %s: %w", dbName, err)
	}

	// multiStatements=true 允许整体执行 db_ddl.sql（库级语句已被过滤）
	db, err := sql.Open("mysql", adminDSN+dbName+"?multiStatements=true")
	if err != nil {
		dropMySQLDatabase(adminDSN, dbName)
		return "", fmt.Errorf("open mysql db %s: %w", dbName, err)
	}
	defer db.Close()

	ddlPath := filepath.Join(projectRoot, "db_ddl.sql")
	if err := execDDLFile(db, ddlPath); err != nil {
		dropMySQLDatabase(adminDSN, dbName)
		return "", fmt.Errorf("exec mysql ddl: %w", err)
	}
	if err := seedTestDataMySQL(db); err != nil {
		dropMySQLDatabase(adminDSN, dbName)
		return "", fmt.Errorf("seed mysql test data: %w", err)
	}
	return dbName, nil
}

// mysqlDBSection 把管理员 DSN 解析为 [Databases.bfe_db] TOML 配置段
// （按 mysql.Config 字段展开，与 conf/ai_gateway_api.toml 生产样例同构）。
func mysqlDBSection(adminDSN, dbName string) (string, error) {
	cfg, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		return "", fmt.Errorf("parse adminDSN: %w", err)
	}
	net := cfg.Net
	if net == "" {
		net = "tcp"
	}
	return fmt.Sprintf(`[Databases.bfe_db]
Driver               = "mysql"
DBName               = %s
Addr                 = %s
Net                  = %s
User                 = %s
Passwd               = %s
MultiStatements      = true
ParseTime            = true
AllowNativePasswords = true
MaxOpenConns         = 50
MaxIdleConns         = 10
ConnMaxIdleTimeInMs  = 500000
ConnMaxLifetimeInMs  = 5000000`,
		strconv.Quote(dbName),
		strconv.Quote(cfg.Addr),
		strconv.Quote(net),
		strconv.Quote(cfg.User),
		strconv.Quote(cfg.Passwd)), nil
}

// dropMySQLDatabase 尽力删除测试库（清理失败不阻断主流程）。
func dropMySQLDatabase(adminDSN, dbName string) {
	db, err := sql.Open("mysql", adminDSN)
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
}

// MySQLDSN 返回 MySQL 后端模式下被测库的完整 DSN（供测试直写种子数据 /
// 直查断言使用）。非 MySQL（SQLite）模式返回空串，调用方据此后退
// sm.DBPath + sqlite-strip 驱动。
func (sm *ServerManager) MySQLDSN() string {
	if sm.mysqlAdminDSN == "" || sm.mysqlDBName == "" {
		return ""
	}
	return sm.mysqlAdminDSN + sm.mysqlDBName + "?parseTime=true&multiStatements=true"
}

// OpenDB 打开本测试进程的数据库句柄：MySQL 后端用 mysql 驱动连被测库，
// 否则用 sqlite-strip 打开 sm.DBPath（busy_timeout 与 createTempConfig
// 同款，避免与后台审计刷盘瞬态争用写锁）。同一断言代码双后端零改动。
func OpenDB(t testing.TB, sm *ServerManager) *sql.DB {
	t.Helper()

	if dsn := sm.MySQLDSN(); dsn != "" {
		db, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		return db
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", filepath.ToSlash(sm.DBPath))
	db, err := sql.Open("sqlite-strip", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// StartServerAuto 按环境选择测试后端：设置 AIAPI_MYSQL_DSN（管理员
// DSN，不带库名，形如 root:pass@tcp(127.0.0.1:3306)/）时以 MySQL 启动
// （StartServerWithMySQL：自动建库 ai_gateway_it_*、执行 db_ddl.sql、
// 用毕 DROP），否则以 SQLite 启动（StartServer）。使同一测试包在两套
// 后端下零改动运行；MySQL 模式下单实例包补设全局 client URL，保持
// testutil.GetClient() 可用。
func StartServerAuto() (*ServerManager, error) {
	dsn := strings.TrimSpace(os.Getenv("AIAPI_MYSQL_DSN"))
	if dsn != "" {
		sm, err := StartServerWithMySQL(dsn)
		if err != nil {
			return nil, err
		}
		SetServerURL(sm.ServerURL)
		return sm, nil
	}
	return StartServer()
}

// StartServerWithMonitorAuto 是 StartServerAuto 的「启用监控端口」版本：
// 设置 AIAPI_MYSQL_DSN 时以 MySQL 启动（monitorPortMode=1，MonitorURL 可用），
// 否则等价 StartServerWithMonitor（SQLite）。extraTOML 原样注入两套后端，
// 使 secret_at_rest 这类需要 [Security] 段 + /reload/security 的用例
// 在两套后端下同一份实现运行。
func StartServerWithMonitorAuto(extraTOML string) (*ServerManager, error) {
	dsn := strings.TrimSpace(os.Getenv("AIAPI_MYSQL_DSN"))
	if dsn != "" {
		sm, err := startServerWithMySQL(dsn, extraTOML, 1)
		if err != nil {
			return nil, err
		}
		SetServerURL(sm.ServerURL)
		return sm, nil
	}
	return StartServerWithMonitor(extraTOML)
}

// execDDLFile 在已选定的库上执行项目 db_ddl.sql。该文件开头含库级语句
// （DROP DATABASE / CREATE DATABASE / USE open_bfe），且部分语句以 "; \n"
// 收尾（按 ";\n" 切分不可靠），因此改为：过滤库级语句后整体执行
// （multiStatements=true）。MySQL DDL 无触发器/存储过程，整体执行安全。
func execDDLFile(db *sql.DB, ddlPath string) error {
	ddlContent, err := os.ReadFile(ddlPath)
	if err != nil {
		return fmt.Errorf("read ddl file: %w", err)
	}

	var kept []string
	for _, line := range strings.Split(string(ddlContent), "\n") {
		trimmed := strings.ToUpper(strings.TrimSpace(line))
		// 库级语句跳过：测试自建独立库，不能跟随脚本切到 open_bfe
		if strings.HasPrefix(trimmed, "DROP DATABASE") ||
			strings.HasPrefix(trimmed, "CREATE DATABASE") ||
			strings.HasPrefix(trimmed, "USE ") {
			continue
		}
		kept = append(kept, line)
	}

	if _, err := db.Exec(strings.Join(kept, "\n")); err != nil {
		return fmt.Errorf("exec mysql ddl: %w", err)
	}
	return nil
}

// seedTestDataMySQL 写入 MySQL 版默认测试数据，行集对齐 SeedTestData
// （SQLite 方言 INSERT OR IGNORE / datetime('now') 在此替换为
// INSERT IGNORE / NOW()）。
func seedTestDataMySQL(db *sql.DB) error {
	var productID int64
	if err := db.QueryRow("SELECT id FROM products WHERE name = 'AI_product'").Scan(&productID); err != nil {
		return fmt.Errorf("find AI_product: %w", err)
	}

	if _, err := db.Exec(`
		INSERT IGNORE INTO providers (
			id, name, description, model_endpoint, models, api_keys,
			instance_pool, model_protocols, created_at, updated_at
		) VALUES (
			1, 'deepseek', 'Default AI provider for integration tests',
			'{"schema":"https","uri":"/v1/models"}',
			'["deepseek-chat"]',
			'[]',
			'[{"addr":"10.0.0.1","weight":100,"port":8080}]',
			'["openai"]',
			NOW(), NOW()
		);
	`); err != nil {
		return fmt.Errorf("seed provider: %w", err)
	}

	llmConfig := `{"model_endpoint":{"schema":"https","uri":"/v1/models","headers":null},"models":["deepseek-chat"],"model_mappings":null,"keys":[],"key_policy":null,"provider":"deepseek","match_prefix":null,"strip_prefix":null}`
	if _, err := db.Exec(`
		INSERT IGNORE INTO clusters (
			id, name, description, product_id, protocol,
			healthcheck_host, healthcheck_uri, llm_config, created_at, updated_at
		) VALUES (
			1, 'BFE-AI_product.szyf', 'Default AI cluster for integration tests', ?, 'http',
			'localhost', '/health', ?, NOW(), NOW()
		);
	`, productID, llmConfig); err != nil {
		return fmt.Errorf("seed cluster: %w", err)
	}

	if _, err := db.Exec(`
		INSERT IGNORE INTO sub_clusters (
			id, name, cluster_id, product_id, description, bns_name_id, enabled, role, created_at, updated_at
		) VALUES (
			1, 'BFE-AI_product.szyf', 1, ?, 'Default sub cluster for integration tests', 1, 1, 'COMMON',
			NOW(), NOW()
		);
	`, productID); err != nil {
		return fmt.Errorf("seed sub_cluster: %w", err)
	}

	lbMatrix := `{"BFE-AI_product.szyf":{"BFE-AI_product.szyf":100,"GSLB_BLACKHOLE":0}}`
	if _, err := db.Exec(`
		INSERT IGNORE INTO lb_matrices (
			cluster_id, lb_matrix, product_id, created_at, updated_at
		) VALUES (
			1, ?, ?, NOW(), NOW()
		);
	`, lbMatrix, productID); err != nil {
		return fmt.Errorf("seed lb_matrix: %w", err)
	}

	productNames := []string{
		"product_demo",
		"product_search",
		"product_empty",
		"product_unbind",
		"product_token",
		"product_token_del",
		"product_token_search",
		"product_token_list",
		"product_token_list2",
		"product_token_detail",
		"product_token_empty",
	}
	for _, name := range productNames {
		if _, err := db.Exec(`
			INSERT IGNORE INTO products (
				name, mail_list, contact_person, sms_list, description, created_at, updated_at
			) VALUES (?, 'test@example.com', 'test', 'no sms', 'Seeded product for integration tests', NOW(), NOW());
		`, name); err != nil {
			return fmt.Errorf("seed product %s: %w", name, err)
		}
	}

	return nil
}
