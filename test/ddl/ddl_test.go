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

// Package ddl_test keeps the shipped DDL consistent with the at-rest
// encryption contract: the DAO stores one enc$v1$ envelope per sensitive
// column, and an envelope is not valid JSON. MySQL validates JSON syntax on
// write, so a JSON-typed envelope column silently turns every write into
// "Error 3140" (issue #264). Checking the files here is cheap and catches the
// drift before any database sees it.
package ddl_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	projectRoot = "../.."
	// textTypes are the acceptable declared types for an envelope column.
	textTypes = "MEDIUMTEXT|TEXT|VARCHAR"
)

// tableBlock returns the CREATE TABLE body of one table, without backticks
// confusion between the MySQL and SQLite dialects.
func tableBlock(t *testing.T, content, table string) string {
	t.Helper()

	re := regexp.MustCompile("(?i)CREATE TABLE\\s+`?" + table + "`?\\s*\\(")
	loc := re.FindStringIndex(content)
	require.NotNil(t, loc, "CREATE TABLE %s not found", table)

	rest := content[loc[1]:]
	end := strings.Index(rest, "\n);")
	require.GreaterOrEqual(t, end, 0, "CREATE TABLE %s is not terminated", table)
	return rest[:end]
}

// columnType returns the declared type of one column inside a table block.
func columnType(t *testing.T, block, column string) string {
	t.Helper()

	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(line, ",")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.EqualFold(strings.Trim(fields[0], "`"), column) {
			return strings.ToUpper(fields[1])
		}
	}
	require.FailNow(t, "column not found", "column %q", column)
	return ""
}

func readFile(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(projectRoot, name))
	require.NoError(t, err)
	return string(data)
}

var envelopeColumns = []struct{ Table, Column string }{
	{Table: "providers", Column: "api_keys"},
	{Table: "api_keys", Column: "api_key"},
}

func TestMySQLDDL_EnvelopeColumnsAreText(t *testing.T) {
	content := readFile(t, "db_ddl.sql")
	for _, col := range envelopeColumns {
		declType := columnType(t, tableBlock(t, content, col.Table), col.Column)
		assert.NotEqual(t, "JSON", declType,
			"db_ddl.sql %s.%s must not be JSON: the DAO stores an enc$v1$ envelope there and MySQL rejects it (Error 3140)", col.Table, col.Column)
		assert.Regexp(t, "^(?:"+textTypes+")", declType,
			"db_ddl.sql %s.%s should be declared as a text type", col.Table, col.Column)
	}
}

func TestSQLiteDDL_EnvelopeColumnsAreText(t *testing.T) {
	content := readFile(t, "db_ddl_sqlite.sql")
	for _, col := range envelopeColumns {
		declType := columnType(t, tableBlock(t, content, col.Table), col.Column)
		assert.NotEqual(t, "JSON", declType,
			"db_ddl_sqlite.sql %s.%s must not be JSON", col.Table, col.Column)
	}
}

// Existing deployments upgrade through the script, so the column change must
// be reachable there too (adding it only to db_ddl.sql fixes new installs).
func TestUpgradeScript_MigratesProviderAPIKeysColumn(t *testing.T) {
	content := readFile(t, filepath.Join("database", "db_ddl_upgrade_to_0.0.11.sql"))

	re := regexp.MustCompile("(?is)MODIFY\\s+COLUMN\\s+`api_keys`\\s+MEDIUMTEXT")
	assert.Regexp(t, re, content,
		"database/db_ddl_upgrade_to_0.0.11.sql must contain the providers.api_keys JSON -> MEDIUMTEXT change")
	assert.Contains(t, content, "ALGORITHM=COPY, LOCK=SHARED",
		"the JSON -> text conversion requires ALGORITHM=COPY, LOCK=SHARED")
}