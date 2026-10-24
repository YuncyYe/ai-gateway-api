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

package partition_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
)

// 本用例组在真实 MySQL 8.x 上验证分区维护 JOB 的自动建分区行为
//（issue #191 回归：配置 [Report].Database 后表名带 schema 限定，
// information_schema.PARTITIONS 的 TABLE_NAME 只存裸表名，分区查询恒
// 返回 0 行，JOB 误判表未分区、静默降级为 DELETE 兜底，从不 ADD
// PARTITION，log-reader 写入报 Error 1526）。
//
// 数据源：环境变量 REPORT_MYSQL_DSN（格式 user:pass@tcp(host:port)/，与
// 组 B query 同约定）。未设置时全部用例 Skip。
// 测试自建专用随机名数据库 report_it_<ns>，套用项目 db_ddl_report_mysql.sql，
// ${INIT_DATE} 替换为**昨天**（与组 B 的"今天+3"相反）：p_init 边界 < 今天，
// 待建分区窗口 [今天, 今天+3) 非空，JOB 必须有 ADD PARTITION 动作；若按组 B
// 方式建表，p_init 已覆盖前瞻窗口，修复前后用例都通过，失去回归意义。
// 服务装配为 issue #191 生产同型：[Report].Database 非空（schema 限定名路径，
// 本 bug 的触发条件）、EnablePartitionMgmt=true、EnableAggregateJob=false。

var (
	sm        *testutil.ServerManager
	mysqlDB   *sql.DB // 种子数据库连接
	serverDSN string  // user:pass@tcp(addr)/，用于清理时重建连接
	dbName    string
	today     time.Time // 测试当天民用日，JOB 与断言共用同一时钟
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("REPORT_MYSQL_DSN")
	if dsn == "" {
		fmt.Println("REPORT_MYSQL_DSN not set; skipping report partition integration tests.")
		fmt.Println("To enable: REPORT_MYSQL_DSN=\"root:****@tcp(127.0.0.1:3306)/\" go test ./tests/report/partition/")
		os.Exit(0)
	}

	cfg, err := parseServerDSN(dsn)
	if err != nil {
		fmt.Println("invalid REPORT_MYSQL_DSN:", err)
		os.Exit(1)
	}
	serverDSN = fmt.Sprintf("%s:%s@tcp(%s)/", cfg.user, cfg.pass, cfg.addr)

	y, mth, d := time.Now().Date()
	today = time.Date(y, mth, d, 0, 0, 0, 0, time.Local)

	dbName = fmt.Sprintf("report_it_%d", time.Now().UnixNano())

	// 1. 创建专用数据库并应用 DDL（INIT_DATE=昨天，p_init 不覆盖前瞻窗口）。
	serverDB, err := sql.Open("mysql", serverDSN)
	if err != nil {
		fmt.Println("open mysql failed:", err)
		os.Exit(1)
	}
	if _, err := serverDB.Exec("DROP DATABASE IF EXISTS " + dbName); err != nil {
		fmt.Println("pre-drop test database failed:", err)
		serverDB.Close()
		os.Exit(1)
	}
	if _, err := serverDB.Exec("CREATE DATABASE " + dbName); err != nil {
		fmt.Println("create test database failed (check REPORT_MYSQL_DSN privileges):", err)
		serverDB.Close()
		os.Exit(1)
	}
	serverDB.Close()

	mysqlDB, err = sql.Open("mysql", serverDSN+dbName)
	if err != nil {
		cleanupDB()
		fmt.Println("open test database failed:", err)
		os.Exit(1)
	}
	if err := applyReportDDL(mysqlDB, today.AddDate(0, 0, -1)); err != nil {
		cleanupDB()
		fmt.Println("apply report ddl failed:", err)
		os.Exit(1)
	}

	// 2. 注入 [Report] 配置（issue #191 生产同型，Database 非空）并启动
	// api 子进程（注意：子进程运行项目根 ai-gateway-api.exe，须先 make build
	// 使二进制包含待验证代码）。
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
Backend = "mysql"
Datasource = "report_db"
Database = "%s"
EnableAggregateJob = false
EnablePartitionMgmt = true
RetentionDays = 7
`, dbName, cfg.addr, cfg.user, tomlString(cfg.pass), dbName)

	sm, err = testutil.StartServerWithExtraConfig(extraTOML)
	if err != nil {
		cleanupDB()
		fmt.Println("failed to start server:", err)
		os.Exit(1)
	}

	code := m.Run()

	sm.Shutdown()
	mysqlDB.Close()
	cleanupDB()
	os.Exit(code)
}

func cleanupDB() {
	serverDB, err := sql.Open("mysql", serverDSN)
	if err != nil {
		return
	}
	defer serverDB.Close()
	serverDB.Exec("DROP DATABASE IF EXISTS " + dbName)
}

type serverConfig struct {
	user string
	pass string
	addr string
}

// parseServerDSN 解析 "user:pass@tcp(host:port)/" 形式的服务器级 DSN
// （允许不带库名；忽略 query 参数）。
func parseServerDSN(dsn string) (*serverConfig, error) {
	cfg := &serverConfig{}

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

// tomlString 生成 TOML 字符串字面量（无特殊字符时用 literal string，避免转义差异）。
func tomlString(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, `'"\\`) {
		return "'" + s + "'"
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// applyReportDDL 套用项目根 db_ddl_report_mysql.sql，${INIT_DATE} 替换为
// 入参 initDate（本组固定为昨天，见文件头说明）。
func applyReportDDL(db *sql.DB, initDate time.Time) error {
	ddlPath, err := findRepoFile("db_ddl_report_mysql.sql")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(ddlPath)
	if err != nil {
		return err
	}
	script := strings.ReplaceAll(string(data), "${INIT_DATE}", initDate.Format("2006-01-02"))

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
