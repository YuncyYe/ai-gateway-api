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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
}

// Close 停止 api 进程、关闭种子连接并 DROP 测试数据库。
func (s *ReportServer) Close() {
	if s.Server != nil {
		s.Server.Shutdown()
	}
	if s.DB != nil {
		s.DB.Close()
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
