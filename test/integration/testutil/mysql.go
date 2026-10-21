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
	"time"

	"github.com/go-sql-driver/mysql"
)

// StartServerWithMySQL 以 MySQL 为后端启动测试服务器，供 //go:build mysql
// 并发用例使用（SQLite 单连接串行无法暴露 ID 生成竞态与行锁超时，
// issue #80 / #99 / #132）。
//
// adminDSN 为不带库名的管理员连接串，形如 "root:pass@tcp(127.0.0.1:3306)/"
//（从 AIAPI_MYSQL_DSN 环境变量读取）。流程：
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
func startServerWithMySQL(adminDSN, extraTOML string, monitorPortMode int) (*ServerManager, error) {
	if strings.TrimSpace(adminDSN) == "" {
		return nil, fmt.Errorf("adminDSN is empty")
	}
	if !strings.HasSuffix(adminDSN, "/") {
		return nil, fmt.Errorf("adminDSN must end with '/': %q", adminDSN)
	}

	projectRoot, err := findProjectRoot()
	if err != nil {
		return nil, err
	}

	dbName := fmt.Sprintf("ai_gateway_it_%d_%d", os.Getpid(), time.Now().UnixNano()%100000)

	// adminDSN 形如 "user:pass@tcp(host:port)/"：解析出连接要素，改写为
	// mysql.Config 字段形态的服务器配置段。
	cfg, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		return nil, fmt.Errorf("parse adminDSN: %w", err)
	}

	adminDB, err := sql.Open("mysql", adminDSN)
	if err != nil {
		return nil, fmt.Errorf("open mysql admin: %w", err)
	}
	defer adminDB.Close()

	if _, err := adminDB.Exec(fmt.Sprintf("CREATE DATABASE `%s`", dbName)); err != nil {
		return nil, fmt.Errorf("create database %s: %w", dbName, err)
	}

	// multiStatements=true 允许整体执行 db_ddl.sql（库级语句已被过滤）
	db, err := sql.Open("mysql", adminDSN+dbName+"?multiStatements=true")
	if err != nil {
		return nil, fmt.Errorf("open mysql db %s: %w", dbName, err)
	}
	defer db.Close()

	ddlPath := filepath.Join(projectRoot, "db_ddl.sql")
	if err := execDDLFile(db, ddlPath); err != nil {
		return nil, fmt.Errorf("exec mysql ddl: %w", err)
	}
	if err := seedTestDataMySQL(db); err != nil {
		return nil, fmt.Errorf("seed mysql test data: %w", err)
	}

	// MySQL 配置在 TOML 中按 mysql.Config 字段展开（非 DSN 串），
	// 与 conf/ai_gateway_api.toml 生产样例同构。
	net := cfg.Net
	if net == "" {
		net = "tcp"
	}
	section := fmt.Sprintf(`[Databases.bfe_db]
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
		strconv.Quote(cfg.Passwd))

	sm, err := startServer(nil, "", extraTOML, &dbPatch{driver: "mysql", section: section}, monitorPortMode)
	if err != nil {
		if admin, oErr := sql.Open("mysql", adminDSN); oErr == nil {
			admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
			admin.Close()
		}
		return nil, err
	}
	sm.mysqlAdminDSN = adminDSN
	sm.mysqlDBName = dbName
	return sm, nil
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

// StartServerAuto 按环境选择测试后端：设置 AIAPI_MYSQL_DSN（管理员
// DSN，不带库名，形如 root:pass@tcp(127.0.0.1:3306)/）时以 MySQL 启动
//（StartServerWithMySQL：自动建库 ai_gateway_it_*、执行 db_ddl.sql、
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
//（DROP DATABASE / CREATE DATABASE / USE open_bfe），且部分语句以 "; \n"
// 收尾（按 ";\n" 切分不可靠），因此改为：过滤库级语句后整体执行
//（multiStatements=true）。MySQL DDL 无触发器/存储过程，整体执行安全。
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
//（SQLite 方言 INSERT OR IGNORE / datetime('now') 在此替换为
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
