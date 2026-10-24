// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
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
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDbConfig_FormatDSN(t *testing.T) {
	t.Run("mysql driver", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: DriverMySQL,
			Config: mysql.Config{
				User:   "user",
				Passwd: "pass",
				Net:    "tcp",
				Addr:   "127.0.0.1:3306",
				DBName: "bfe",
			},
		}
		dsn, err := cfg.FormatDSN()
		require.NoError(t, err)
		assert.Contains(t, dsn, "user:pass@tcp(127.0.0.1:3306)/bfe")
	})

	t.Run("sqlite driver", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: DriverSQLite,
			Config: mysql.Config{
				DBName: ":memory:",
			},
		}
		dsn, err := cfg.FormatDSN()
		require.NoError(t, err)
		assert.Equal(t, ":memory:", dsn)
	})

	t.Run("sqlite-strip driver", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: "sqlite-strip",
			Config: mysql.Config{
				DBName: "/tmp/test.db",
			},
		}
		dsn, err := cfg.FormatDSN()
		require.NoError(t, err)
		assert.Equal(t, "/tmp/test.db", dsn)
	})

	t.Run("sqlite driver without dbname", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: DriverSQLite,
			Config: mysql.Config{
				DBName: "",
			},
		}
		_, err := cfg.FormatDSN()
		require.Error(t, err)
	})

	t.Run("clickhouse driver", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: DriverClickHouse,
			Config: mysql.Config{
				User:   "report_read",
				Passwd: "secret",
				Addr:   "127.0.0.1:9000",
				DBName: "bfe_observability",
			},
		}
		dsn, err := cfg.FormatDSN()
		require.NoError(t, err)
		assert.Equal(t, "clickhouse://report_read:secret@127.0.0.1:9000/bfe_observability?dial_timeout=10s&compress=lz4", dsn)
	})

	t.Run("clickhouse driver without addr", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: DriverClickHouse,
			Config: mysql.Config{
				User:   "report_read",
				DBName: "bfe_observability",
			},
		}
		_, err := cfg.FormatDSN()
		require.Error(t, err)
	})

	t.Run("clickhouse driver without dbname", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: DriverClickHouse,
			Config: mysql.Config{
				User: "report_read",
				Addr: "127.0.0.1:9000",
			},
		}
		_, err := cfg.FormatDSN()
		require.Error(t, err)
	})

	t.Run("unsupported driver", func(t *testing.T) {
		cfg := &DbConfig{
			Driver: "postgres",
		}
		_, err := cfg.FormatDSN()
		require.Error(t, err)
	})
}
