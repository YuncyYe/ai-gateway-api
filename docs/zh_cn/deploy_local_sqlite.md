# 本地单机部署（SQLite）

本文介绍如何在本地以 **SQLite + 内存 Redis Mock** 方式部署 AI Gateway API，适用于开发、测试与功能体验。
不依赖 MySQL、Redis 等外部服务，单机即可运行。

## 一、部署要求

| 项目 | 要求 |
|------|------|
| 操作系统 | Linux / macOS / Windows |
| Go（源码编译时） | 1.24 及以上（见 `go.mod`） |
| SQLite 驱动 | 已内置纯 Go 驱动 `github.com/glebarez/go-sqlite`，**无需 CGO** |
| 初始化工具 | `sqlite3` 命令行 **或** `python3`（二选一，用于导入 DDL） |
| 端口 | 8183（API / Dashboard）、8284（监控） |

> 说明：程序**不会自动建表**，首次启动前必须先执行 `db_ddl_sqlite.sql` 初始化数据库。

## 二、编译（独立步骤）

**编译与部署分离**：部署脚本不负责编译，请先独立完成编译。

```bash
cd ai-gateway-api
make            # 编译产物：./ai-gateway-api
# 或使用已有产物 output/ai-gateway-api
```

> 若机器上没有 Go 工具链，可直接使用仓库自带的 `output/ai-gateway-api`（部署脚本会在根目录缺少二进制时自动复用该产物）。

## 三、方式一：一键脚本（推荐）

编译完成后，执行脚本 `scripts/deploy_local_sqlite.sh`，它会自动完成：初始化 SQLite → 启动服务（不会编译）。

```bash
cd ai-gateway-api
./scripts/deploy_local_sqlite.sh 
```

脚本参数：

| 参数 | 说明 |
|------|------|
| `--reinit` | 删除并重建 SQLite 数据库（**数据会丢失**） |
| `--no-start` | 只初始化数据库，不启动服务 |

> 脚本启动前若找不到根目录的 `./ai-gateway-api`，会自动复用 `output/ai-gateway-api`；两者都不存在时会提示先执行 `make`。

启动成功后访问：

- Dashboard：<http://localhost:8183>
- 默认账号 / 密码：`admin` / `admin`（首次登录后请立即修改）

## 四、方式二：手动部署

### 1. 初始化 SQLite 数据库

```bash
mkdir -p data log
sqlite3 ./data/ai_gateway.db < db_ddl_sqlite.sql
```

若没有 `sqlite3`，可用 Python 导入（自带 `sqlite3` 模块，能正确处理触发器）：

```bash
python3 - <<'PY'
import sqlite3
conn = sqlite3.connect("./data/ai_gateway.db")
conn.executescript(open("db_ddl_sqlite.sql", encoding="utf-8").read())
conn.commit()
conn.close()
PY
```

DDL 中已内置默认数据：系统管理员 `admin/admin`、产品线 `BFE` / `AI_product`、默认 pool 与 cluster 等。

### 2. 使用 SQLite 配置

仓库已提供配置文件 `conf/ai_gateway_api_sqlite.toml`，关键内容如下：

```toml
[Databases.bfe_db]
DBName = "./data/ai_gateway.db"   # SQLite 数据库文件路径
Driver = "sqlite-strip"           # sqlite + 自动剥离不支持的 FOR UPDATE
MaxOpenConns = 5
MaxIdleConns = 1

[RedisConf]
Bns         = "mock"              # 使用内存 Redis Mock，无需外部 Redis
ClusterMode = "mock"
```

> - `Driver` 也可写 `"sqlite"`；推荐 `"sqlite-strip"`，它会自动移除 SQLite 不支持的 `FOR UPDATE` 子句（见 `stateful/sqlite_strip.go`）。
> - `DBName` 即数据库文件路径，纯 Go 驱动按此路径创建/打开数据库。
> - Redis 设为 `mock` 后使用进程内存实现（见 `stateful/config_redis.go`），**仅适用于单实例本地部署**，多实例共享配额/分布式锁需换成真实 Redis。

### 3. 启动服务

```bash
./ai-gateway-api -c ./conf -sc ai_gateway_api_sqlite.toml -l ./log
```

参数含义（见 `main.go`）：

- `-c`：配置目录，默认 `./conf/`
- `-sc`：配置文件名，这里指向 `ai_gateway_api_sqlite.toml`
- `-l`：日志目录，默认 `./log`

### 4. 验证

```bash
curl -s http://localhost:8183/ -o /dev/null -w '%{http_code}\n'   # Dashboard
curl -s http://localhost:8284/ -o /dev/null -w '%{http_code}\n'   # 监控
```

浏览器打开 <http://localhost:8183>，用 `admin/admin` 登录。

## 五、目录与文件说明

| 路径 | 说明 |
|------|------|
| `data/ai_gateway.db` | SQLite 数据库文件（会自动生成 `-wal` / `-shm` 文件） |
| `conf/ai_gateway_api_sqlite.toml` | SQLite 本地部署配置 |
| `db_ddl_sqlite.sql` | SQLite 建表与初始化脚本 |
| `scripts/deploy_local_sqlite.sh` | 一键部署脚本 |
| `log/` | 运行日志 |
| `static/` | Dashboard 静态资源目录（为空时仅提供 API） |

## 六、可选：启用 Dashboard 静态页面

`static/` 默认只有占位文件。需要完整管理界面时，可从
[ai-gateway-web releases](https://github.com/rainway-ai-gateway/ai-gateway-web/releases) 下载前端产物，
解压后放入 `static/` 目录即可（`RunTime.StaticFilePath = "./static"`）。

## 七、常见问题

**1. `database is locked`**
SQLite 为库级写锁。本地部署建议保持 `MaxOpenConns` 较小（配置中为 5），避免多写入连接争抢；单用户场景可进一步调为 1。

**2. 表不存在 / `no such table`**
未执行 DDL。运行 `sqlite3 ./data/ai_gateway.db < db_ddl_sqlite.sql`，或使用 `--reinit` 重建。

**3. 忘记密码 / 想重置数据**
删除 `data/ai_gateway.db*` 后重新执行 DDL：`./scripts/deploy_local_sqlite.sh --reinit`。

**4. 报表接口 `/open-api/v1/report/*` 返回 404**
报表模块仅支持 MySQL / Doris 后端。未配置 `[Report]` 时该模块不装配，路由返回 404，属预期行为。

**5. 多实例部署**
内存 Redis Mock 不支持多实例间共享，请将 `[RedisConf]` 改为真实 Redis，并将数据库替换为 MySQL（`db_ddl.sql`）。

## 八、切换回 MySQL（可选）

若要切回 MySQL，只需：

```bash
mysql -u{user} -p{password} < db_ddl.sql
./ai-gateway-api -c ./conf -sc ai_gateway_api.toml -l ./log
```

并修改 `conf/ai_gateway_api.toml` 中的 `[Databases.bfe_db]` 连接信息。