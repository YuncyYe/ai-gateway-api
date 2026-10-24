# AI Gateway API 集成测试设计方案

**更新日期**：2026-08-15
**文档类型**：测试设计方案
**目标读者**：后端开发工程师、测试工程师

---

## 一、概述

### 1.1 设计目标

为 AI Gateway API 提供一套完整的本地离线接口测试方案，实现：

- **环境隔离**：测试代码和配置完全独立于生产代码
- **离线运行**：使用 SQLite 本地数据库，无需外部依赖（MySQL/Redis）
- **白盒测试覆盖**：覆盖正常参数、异常参数、边界值、必填校验、返回数据验证
- **按模块组织**：同一业务模块的测试用例和设计文档放在一起，减少目录层级

### 1.2 测试范围

| API 模块 | 接口路径前缀 | 说明 |
|----------|-------------|------|
| OpenAPI - Auth | `/open-api/v1/auth` | 用户认证与管理 |
| OpenAPI - API-Key | `/open-api/v1/api-keys` | API-Key 管理 |
| OpenAPI - Entity-Type | `/open-api/v1/entity-types` | 实体类型管理 |
| OpenAPI - Entity | `/open-api/v1/entities` | 实体管理 |
| OpenAPI - Global Route Rules | `/open-api/v1/global-route-rules` | 全局路由规则 |
| OpenAPI - Route Tables | `/open-api/v1/route-tables` | 路由表 |
| OpenAPI - Clusters | `/open-api/v1/clusters` | 集群管理 |
| OpenAPI - Certificate | `/open-api/v1/certificates` | 证书管理 |
| OpenAPI - Providers | `/open-api/v1/providers` | 模型提供商管理 |
| OpenAPI - Model Prices | `/open-api/v1/model-prices` | 模型定价管理 |
| OpenAPI - Batches | `/open-api/v1/batches` | 批量任务列表/详情/取消（批量与异步任务支持一期） |
| OpenAPI - Batch Files | `/open-api/v1/batch-files` | 批量文件元数据查询（批量与异步任务支持一期） |
| OpenAPI - Expression Verify | `/open-api/v1/expression/verify` | 路由表达式校验 |
| InnerAPI | `/inner-api/v1/configs` | 配置导出接口 |

> **v0.4 更新**：新增 Provider/Model 前缀路由裁剪集成测试，覆盖 OpenAPI `/clusters` 对 `llm_config.match_prefix` / `strip_prefix` 的传入校验，以及 InnerAPI `/configs/tls_conf/server_data_conf` 对 `AIConf.MatchPrefix` / `StripPrefix` 的导出验证。详见 [8.1 相关文档](#81-相关文档)。

> **批量与异步任务支持（一期）覆盖归属**（2026-10-07）：`batch_limits`
> 内嵌对象（api-key 的 `rate_limit_policy.rules.batch_limits`）用例归
> **api_key** 模块（`tests/api_key/batch_limits/`，编号 AK-BL）；`batch_discount`
> 与 `mode=batch` 定价用例归 **model_price** 模块（`tests/model_price/batch_price/`，
> 编号 MPB）；批量任务管控 OpenAPI（`/batches`、`/batch-files`）为独立
> **batch** 模块（`tests/batch/`，编号 BT）。

### 1.3 设计原则

1. **不污染生产代码**：所有测试代码、配置、资源均放在 `ai-gateway-api/test/` 目录下，主项目仅需最小改动
2. **真实二进制子进程**：使用 `make build` 编译的真实 `ai-gateway-api.exe`，通过 `exec.CommandContext` 启动为子进程，测试覆盖完整启动链路
3. **按模块组织**：同一模块的测试代码和测试设计文档放在同一目录下，降低维护成本
4. **独立模块**：测试环境作为独立 Go module，通过 `replace` 指令引用主项目和 bfe 依赖
5. **模块级隔离**：每个模块使用独立的测试包，模块内共享一个服务器实例，模块间通过独立数据库文件隔离
6. **数据库隔离**：每个测试进程使用独立的 SQLite 数据库文件（含进程ID）
7. **自动化清理**：测试完成后自动清理临时文件、复制的二进制和数据库文件（含 WAL/SHM）
8. **跳过认证**：测试环境配置 `SkipTokenValidate=true`，无需真实认证

---

## 二、目录结构

```
ai-gateway-api/test/
├── docs/
│   └── README.md                          # 测试设计方案（本文档）
│
├── scripts/                               # 测试辅助脚本
│   ├── run_all_tests.sh                   # 一键运行全部测试
│   ├── run_module_tests.sh                # 运行指定模块测试
│   └── clean.sh                           # 清理运行时数据
│
└── integration/                           # 集成测试环境（独立 Go module）
    ├── go.mod / go.sum
    ├── README.md                          # 集成测试使用说明
    │
    ├── conf/                              # 测试专用配置
    │   ├── ai_gateway_api.toml            # SQLite + SkipTokenValidate + MockRedis
    │   ├── nav_tree.toml
    │   └── i18n/
    │       └── zh.toml
    │
    ├── data/                              # 运行时数据目录（gitignore）
    │   └── .gitkeep
    │
    ├── testutil/                          # 测试工具包
    │   ├── server.go                      # 子进程服务器管理
    │   ├── client.go                      # HTTP 客户端封装
    │   ├── assert.go                      # 断言函数
    │   ├── fixture.go                     # 测试数据工厂
    │   └── db.go                          # SQLite 数据库初始化/清理/种子数据
    │
    └── tests/                             # 测试用例代码 + 设计文档
        ├── api_key/
        │   ├── design.md                  # API-Key 模块用例设计
        │   ├── create/
        │   │   └── create_test.go
        │   ├── delete/
        │   │   └── delete_test.go
        │   └── ...                        # 其他接口测试子目录
        ├── auth/
        │   ├── design.md
        │   ├── create_user/
        │   │   └── create_user_test.go
        │   └── ...                        # 其他接口测试子目录
        ├── global_route_rules/
        │   ├── design.md
        │   └── update/
        │       └── update_test.go
        ├── route_tables/
        │   ├── design.md
        │   └── list/
        │       └── list_test.go
        ├── expression_verify/
        │   ├── design.md
        │   └── verify/
        │       └── verify_test.go
        └── ...                            # 其他业务模块
```

### 2.1 目录设计说明

#### `integration/`（集成测试环境）

`integration/` 是测试运行的基础设施，采用真实二进制子进程模式运行测试服务器：

| 子目录/文件 | 说明 |
|-------------|------|
| `go.mod` / `go.sum` | 独立 Go module，通过 `replace` 指令引用主项目和 bfe 依赖 |
| `conf/` | 测试专用配置文件（含 nav_tree.toml 和 i18n/zh.toml） |
| `data/` | 测试运行时数据目录，存放复制的二进制和日志文件，测试结束后自动清理 |
| `testutil/` | 测试工具包，提供子进程管理、HTTP 客户端、断言、数据库初始化 |
| `tests/` | 测试用例代码和设计文档，按模块组织 |

#### `tests/{module}/`

每个模块一个目录，包含：

- **`design.md`**：该模块的测试用例设计文档，包含接口列表、参数说明、场景设计
- **`{case}/`**：每个接口一个子目录，内含 `{case}_test.go`

同一接口的正常、异常、边界用例放在同一个测试包中，避免不同接口的 helper 函数命名冲突；设计文档和测试代码放在一起，方便同步维护。

---

## 三、测试环境设计

### 3.1 Go Module 设计

`integration/go.mod` 内容：

```go
module github.com/rainway-ai-gateway/ai-gateway-api/integration

go 1.22

require (
    github.com/glebarez/go-sqlite v1.21.2        // 纯 Go SQLite 驱动，无 CGO
    github.com/gorilla/mux v1.8.0                 // HTTP 路由
    github.com/stretchr/testify v1.10.0           // 测试断言库
    github.com/rainway-ai-gateway/ai-gateway-api v0.0.0  // 主项目
    gopkg.in/tylerb/graceful.v1 v1.2.15           // 优雅关闭
)

replace github.com/rainway-ai-gateway/ai-gateway-api => ../../
```

**说明**：
- `integration` 作为独立 Go module，通过 `replace` 指令引用主项目源码
- 必须引入 `github.com/rainway-ai-gateway/ai-gateway-api` 以触发 `stateful` 包的 `init()` 注册 `sqlite-strip` 驱动
- 测试用例代码通过 `import "github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"` 使用测试工具包

### 3.2 测试配置文件

`integration/conf/ai_gateway_api.toml`：

```toml
[Server]
ServerAddr = "127.0.0.1"       # 仅监听回环地址，避免 Windows 防火墙弹窗
ServerPort = 8199              # 测试端口（实际运行时会被替换为随机端口）
GracefulTimeOutInMs = 5000
MonitorPort = -1               # 禁用监控端口

[Loggers.access]
LogName = "access"
LogLevel = "DEBUG"
RotateWhen = "MIDNIGHT"
BackupCount = 1
Format = "[%D %T] [%L] [%S] %M"
StdOut = false                 # 关闭 stdout 输出，避免干扰子进程通信

[Loggers.sql]
LogName = "sql"
LogLevel = "DEBUG"
RotateWhen = "MIDNIGHT"
BackupCount = 1
Format = "[%D %T] %M"
StdOut = false

[Loggers.exception]
LogName = "exception"
LogLevel = "DEBUG"
RotateWhen = "MIDNIGHT"
BackupCount = 7
Format = "[%D %T] [%L] [%S] %M"
StdOut = false

[Databases.bfe_db]
DBName  = "./data/test_ai_gateway.db"  # 运行时会被替换为实际路径
Driver  = "sqlite-strip"               # 自定义驱动，剥离 FOR UPDATE 子句
MaxOpenConns = 5                       # 防止连接池死锁
MaxIdleConns = 1
ConnMaxIdleTimeInMs = 500000
ConnMaxLifetimeInMs = 5000000

[Depends]
NavTreeFile = "${conf_dir}/nav_tree.toml"
I18nDir     = "${conf_dir}/i18n"

[RedisConf]
Bns = "mock"                           # 测试环境使用内存 Redis Mock
ClusterMode = "mock"

[RunTime]
SkipTokenValidate = true               # 跳过 Token 验证，方便测试
RecordSQL = false                      # 关闭 SQL 日志
SessionExpireInDay = 10
StaticFilePath = "./data"
Debug = false
AIRouteInnerProductName = "AI_product"
DefaultAIClusterName = "BFE-AI_product.szyf"
```

**关键配置说明**：
- `Driver = "sqlite-strip"`：使用自定义 SQLite 驱动，在 Prepare 阶段自动剥离 `FOR UPDATE` 子句
- `StdOut = false`（所有日志器）：关闭 stdout 输出，避免干扰子进程管理
- `SkipTokenValidate = true`：跳过认证，所有请求直接放行
- `RecordSQL = false`：关闭 SQL 记录，减少日志噪音
- `ServerAddr = "127.0.0.1"`：测试服务器仅监听回环地址，避免 Windows 防火墙对子进程监听端口的拦截弹窗
- `RedisConf.ClusterMode = "mock"`：使用内存 Redis 替代真实 Redis，使配额相关接口可在本地离线运行

### 3.3 测试服务器设计

#### 3.3.1 架构概述

测试服务器采用**真实二进制子进程**模式，而非嵌入式服务器。核心思路：

1. 使用 `make build`（或 `go build`）编译出真实的 `ai-gateway-api.exe`
2. 测试运行时，将二进制复制到 `integration/data/` 目录
3. 生成临时配置文件（覆盖端口和数据库路径）
4. 通过 `exec.CommandContext` 启动子进程
5. 通过 TCP 拨号检测确认服务就绪
6. 测试用例通过 HTTP 客户端连接子进程的 API 接口

**为什么采用子进程模式**：
- 使用真实的 `ai-gateway-api.exe`，测试覆盖完整的启动链路（配置加载、中间件注册、路由注册等）
- 避免嵌入式服务器中的依赖注入和初始化顺序问题
- 测试环境与生产环境行为一致

#### 3.3.2 服务器启动流程

`integration/testutil/server.go` 中的 `StartServer()` 函数：

```
StartServer()
    │
    ├─ 1. 定位 integration 目录和项目根目录
    ├─ 2. 生成唯一数据库路径（test_ai_gateway_{pid}.db）
    ├─ 3. 初始化 SQLite 数据库（执行 DDL）
    ├─ 4. 查找项目根目录下的 ai-gateway-api.exe（由 make build 编译）
    ├─ 5. 复制二进制到 data/ 目录（避免文件锁冲突）
    ├─ 6. 生成随机端口 + 创建临时配置文件（覆盖端口和 DB 路径）
    ├─ 7. 通过 exec.CommandContext 启动子进程
    ├─ 8. 消费 stdout 输出（防止子进程阻塞）
    ├─ 9. 等待服务器就绪（TCP 拨号检测，超时 10s）
    └─ 10. 返回 ServerManager（含 ServerURL）
```

#### 3.3.3 服务器停止流程

```
Shutdown()
    │
    ├─ 1. 取消 context，终止子进程
    ├─ 2. 等待子进程退出
    ├─ 3. 清理复制的二进制文件
    ├─ 4. 清理临时配置文件目录
    └─ 5. 清理 SQLite 数据库文件（.db, -wal, -shm）
```

#### 3.3.4 SQLite 自定义驱动（sqlite-strip）

SQLite 不支持 `SELECT ... FOR UPDATE` 语法，需要实现自定义驱动在 `Prepare` 阶段剥离该子句。

**重要**：`sqlite-strip` 驱动位于主项目 `stateful/sqlite_strip.go`（而非 testutil），通过 `init()` 自动注册。这样编译出的 `ai-gateway-api.exe` 天然包含该驱动。

```go
// stateful/sqlite_strip.go
package stateful

import (
    "context"
    "database/sql"
    "database/sql/driver"
    "strings"

    sqlite "github.com/glebarez/go-sqlite"
)

type sqliteStripDriver struct {
    inner driver.Driver
}

func init() {
    sql.Register("sqlite-strip", &sqliteStripDriver{inner: &sqlite.Driver{}})
}

func (d *sqliteStripDriver) Open(name string) (driver.Conn, error) {
    conn, err := d.inner.Open(name)
    if err != nil {
        return nil, err
    }
    return &stripConn{Conn: conn}, nil
}

type stripConn struct {
    driver.Conn
}

func (c *stripConn) Prepare(query string) (driver.Stmt, error) {
    return c.Conn.Prepare(stripForUpdate(query))
}

func (c *stripConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
    if cc, ok := c.Conn.(driver.ConnPrepareContext); ok {
        return cc.PrepareContext(ctx, stripForUpdate(query))
    }
    return c.Conn.Prepare(stripForUpdate(query))
}

func stripForUpdate(query string) string {
    upper := strings.ToUpper(query)
    idx := strings.Index(upper, "FOR UPDATE")
    if idx < 0 {
        return query
    }
    return strings.TrimSpace(query[:idx])
}
```

#### 3.3.5 主项目最小改动

为支持测试环境，主项目 `ai-gateway-api` 仅需两处改动：

1. **新增** `stateful/sqlite_strip.go`：注册 `sqlite-strip` 自定义驱动
2. **修改** `stateful/config_database.go`：`FormatDSN` 允许 `"sqlite-strip"` 驱动名

```go
// config_database.go
case DriverSQLite, "sqlite-strip":
```

#### 3.3.6 关键实现细节

**临时配置文件创建**：
- 复制 `conf/` 目录到系统临时目录
- 修改 `ai_gateway_api.toml` 中的 `ServerPort` 和 `DBName`
- 数据库路径使用正斜杠（`/`），避免 Windows 下 TOML 转义问题

**TCP 就绪检测**：
- 使用 `net.DialTimeout` 拨号 `127.0.0.1:{port}`，超时 10 秒
- 每 100ms 重试一次
- 不使用 HTTP GET 检测（避免 404 误判）

**子进程通信**：
- 通过 `context.WithCancel` 管理子进程生命周期
- 启动 goroutine 消费 stdout 输出（防止子进程写管道阻塞）
- stderr 输出缓存，用于启动失败时的调试

### 3.4 测试执行入口

使用 Go `TestMain` 模式管理服务器生命周期，每个模块包有独立的 `TestMain`：

```go
package api_key_test

import (
    "os"
    "testing"
    "github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
    var err error
    sm, err = testutil.StartServer()
    if err != nil {
        panic("failed to start server: " + err.Error())
    }

    code := m.Run()

    sm.Shutdown()
    os.Exit(code)
}
```

**说明**：
- `TestMain` 所在的包使用黑盒测试包名（如 `package api_key_test`），表示从外部调用 API
- 每个模块启动一个独立的服务器实例，模块内所有测试函数共享同一个服务器实例
- 不同模块之间通过独立的数据库文件实现隔离
- `StartServer()` 返回 `*ServerManager`，包含 `ServerURL` 字段，供 HTTP 客户端使用

---

## 四、测试执行方式

### 4.1 前置条件

运行测试前必须先编译项目二进制：

```bash
# 在 ai-gateway-api 项目根目录编译（必须执行，生成 ai-gateway-api.exe）
cd ai-gateway-api
make build
# 或者: go build -o ai-gateway-api.exe .
```

### 4.2 运行全部测试

```bash
cd ai-gateway-api/test/integration

# 使用项目提供的脚本
../scripts/run_all_tests.sh

# 或者直接使用 go test
go test -v -count=1 -timeout 300s ./tests/...
```

### 4.3 运行指定模块测试

```bash
cd ai-gateway-api/test/integration

# 使用项目提供的脚本
../scripts/run_module_tests.sh api_key

# 或者直接使用 go test
go test -v -count=1 -timeout 120s ./tests/api_key/
```

### 4.4 运行单个用例

```bash
cd ai-gateway-api/test/integration

# 运行 API-Key 模块中的指定测试函数
go test -v -run TestCreate_MinimalParams ./tests/api_key/
```

### 4.5 查看测试结果

Go 测试原生输出已经足够定位问题：

```bash
# 标准输出
go test -v ./tests/...

# JSON 输出，供 CI 解析
go test -json ./tests/...
```

---

## 五、测试工具设计

### 5.1 HTTP 客户端封装 (`testutil/client.go`)

```go
// 封装 HTTP 请求，统一处理认证头、响应解析
type Client struct {
    BaseURL    string
    HTTPClient *http.Client
    Token      string  // 当前测试使用的 Token
}

// 通用方法
func (c *Client) Get(path string, queryParams ...map[string]string) (*APIResponse, error)
func (c *Client) Post(path string, body interface{}) (*APIResponse, error)
func (c *Client) Patch(path string, body interface{}) (*APIResponse, error)
func (c *Client) Put(path string, body interface{}) (*APIResponse, error)
func (c *Client) Delete(path string, body ...interface{}) (*APIResponse, error)
```

### 5.2 断言函数 (`testutil/assert.go`)

```go
// 验证 API 响应的通用断言
func AssertSuccess(t *testing.T, resp *APIResponse)
func AssertErrCode(t *testing.T, resp *APIResponse, expectedErrNum int)
func AssertDataNotEmpty(t *testing.T, resp *APIResponse)
func AssertDataFieldEquals(t *testing.T, resp *APIResponse, field string, expected interface{})
func AssertListLen(t *testing.T, resp *APIResponse, expectedLen int)
func AssertPagination(t *testing.T, resp *APIResponse, expectedPage, expectedPageSize, minTotal int)
```

### 5.3 测试数据工厂 (`testutil/fixture.go`)

提供常用测试数据的构造方法：

```go
// 各类测试数据的默认构造器
func NewDefaultClusterParam(name string) ClusterParam
func NewDefaultAPIKeyParam(desc string) APIKeyParam
func NewDefaultEntityTypeParam(name string, level int) EntityTypeParam
func NewDefaultEntityParam(name, etype string) EntityParam
func NewDefaultCertificateParam(name string) CertificateParam
func NewDefaultRouteRuleParam() RouteRuleParam
func NewDefaultUserParam(name string) UserParam
func UniqueName(prefix string) string  // 生成唯一名称，避免冲突
func GenerateCert() (string, string)   // 生成自签名证书（用于测试）
```

---

## 六、测试用例设计规范

### 6.1 测试用例编号规则

```
{模块缩写}-{接口编号}-{场景编号}
```

模块缩写：
- `AUTH` - 认证模块
- `AK` - API-Key 模块
- `ET` - Entity-Type 模块
- `E` - Entity 模块
- `GRR` - Global Route Rules 模块
- `RT` - Route Tables 模块
- `CL` - 集群模块
- `CERT` - 证书模块
- `MPT` - 模型提供商类型模块
- `MP` - 模型定价模块
- `MPB` - 模型定价 batch_discount 专项（MP 子编号，归 model_price 模块）
- `AK-BL` - API-Key batch_limits 专项（AK 子编号，归 api_key 模块）
- `BT` / `BATCH` - 批量任务管控模块（tests/batch/）
- `TOOL` - 工具模块
- `EV` - 表达式校验模块
- InnerAPI 各子模块

### 6.2 白盒测试覆盖维度

| 测试类型 | 覆盖内容 | 示例 |
|----------|---------|------|
| **正常参数** | 所有必填参数合法值 | 正常创建 API-Key |
| **异常参数** | 参数缺失、类型错误、格式错误 | 缺少 description、无效 UUID |
| **边界值** | 参数取值范围边界 | quota=0、page_size=100（最大值） |
| **必填校验** | 必填字段为空或不传 | 缺少 name、缺少 id |
| **返回数据** | 返回字段完整性、类型正确性 | 验证返回 id、name、create_time |
| **业务规则** | 业务逻辑校验 | Entity 层级校验、Quota 余额更新 |

### 6.3 测试用例场景设计原则

#### 6.3.1 场景分组规范

每个接口的测试用例必须按以下场景类型进行分组，确保测试覆盖全面且结构清晰：

| 场景类型 | 说明 | 设计要点 |
|---------|------|---------|
| **正常参数** | 所有参数使用合法值，验证接口正常功能 | 覆盖每个参数的独立验证及组合场景 |
| **必填校验** | 验证必填字段缺失或为空时的行为 | 每个必填字段至少一个缺失用例和一个空值用例 |
| **边界值** | 验证参数取值范围边界 | 覆盖最小值、最大值、刚好合法、刚好非法 |
| **异常参数** | 验证非法参数的处理 | 类型错误、格式错误、超出范围、违反业务约束 |
| **返回数据** | 验证返回数据结构和字段完整性 | 验证所有字段存在、类型正确、值符合预期 |

#### 6.3.2 参数全覆盖原则

**每个请求参数（包括子结构内的每个嵌套参数）都必须有独立的测试用例场景覆盖**，确保不遗漏任何参数：

1. **顶层参数覆盖**：对于接口请求 Body 中的每个顶层参数，至少设计一个独立测试用例对该参数进行验证
2. **子结构参数覆盖**：对于 `object` 类型的参数（如 `quota_plan`、`rate_limit_policy`），其内部每个子字段都需要独立的测试用例
3. **嵌套子结构参数覆盖**：对于多层嵌套的参数（如 `rate_limit_policy.rules.tpm`），每个叶子字段都需要独立覆盖

#### 6.3.3 表格驱动测试推荐

同一接口的多个相似场景建议使用表格驱动测试组织，提高代码可读性和可维护性：

```go
func TestCreate(t *testing.T) {
    tests := []struct {
        name     string
        body     map[string]interface{}
        wantCode int
        check    func(t *testing.T, data map[string]interface{})
    }{
        {
            name: "最小参数",
            body: map[string]interface{}{"description": "test-key"},
            wantCode: 200,
        },
        {
            name: "缺少 description",
            body: map[string]interface{}{},
            wantCode: 422,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            resp := createAPIKey(t, tt.body)
            testutil.AssertErrCode(t, resp, tt.wantCode)
        })
    }
}
```

### 6.4 用例文件结构规范

测试用例按模块组织，设计文档和测试代码放在同一目录下：

```
integration/tests/{module}/
├── design.md                  # 测试用例设计文档
└── {module}_test.go           # Go 测试代码
```

**`design.md` 模板**：

```markdown
# {接口名称} - 测试用例设计

## 一、接口信息

| 项目 | 值 |
|------|-----|
| 模块 | {模块名称} |
| 接口名称 | {接口中文名} |
| 方法 | POST |
| 路径 | /open-api/v1/api-keys |
| 说明 | 创建一个新的 API-Key |

## 二、接口参数说明

### 请求参数

| 参数名 | 类型 | 必填 | 说明 | 默认值 |
|--------|------|------|------|--------|
| description | string | 是 | API-Key 描述 | - |

### 返回数据字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| id | string | API-Key 唯一标识 |

## 三、测试场景总览

| 编号 | 场景 | 测试类型 | 简要说明 |
|------|------|---------|---------|
| AK-1-001 | 最小参数创建 | 正常参数 | 仅传 description |

## 四、测试场景详细设计

### AK-1-001：最小参数创建（正常参数）

#### 设计思路
...

#### 请求参数

```json
{"description": "test-key-001"}
```

#### 预期返回结果

**ErrNum**：200
**ErrMsg**：success
```

### 6.5 测试用例统计

| 模块 | 接口数 | 测试用例数 |
|------|--------|-----------|
| AUTH 认证 | 13 | 29 |
| AK API-Key | 8 | 22 |
| ET Entity-Type | 5 | 15 |
| E Entity | 8 | 36 |
| GRR Global Route Rules | 2 | 9 |
| RT Route Tables | 1 | 10 |
| CL 集群 | 5 | 36 |
| CERT 证书 | 6 | 11 |
| MPT 模型提供商类型 | 1 | 3 |
| MP 模型定价 | 9 | 30 |
| MPB batch_discount 专项（归 MP） | - | 4 |
| AK-BL batch_limits 专项（归 AK） | - | 5 |
| BATCH 批量任务管控 | 4 | 22 |
| TOOL 工具 | 1 | 6 |
| EV 表达式校验 | 1 | 8 |
| InnerAPI | 9 | 14 |
| **总计** | **73** | **260** |

---

## 七、注意事项

### 7.1 编译前置条件

- 运行测试前必须先执行 `make build`（或 `go build`）编译 `ai-gateway-api.exe`
- 测试运行时会自动查找项目根目录下的 `ai-gateway-api.exe`，复制到 `data/` 目录后启动

### 7.2 数据库隔离

- 每个测试进程使用独立的数据库文件（`test_ai_gateway_{pid}.db`）
- 测试完成后自动清理数据库文件和 WAL/SHM 文件
- 同一模块内的测试函数共享同一个数据库，测试之间应注意数据清理

### 7.3 认证跳过

- 测试环境配置 `SkipTokenValidate=true`
- 中间件在认证头为空时直接放行

### 7.4 SQLite 兼容性

- SQLite 不支持 `SELECT ... FOR UPDATE`，通过自定义驱动 `sqlite-strip` 解决
- `sqlite-strip` 驱动位于主项目 `stateful/sqlite_strip.go`，编译时自动包含
- SQLite 不支持外键级联删除，需在代码层面处理

### 7.5 子进程管理

- 通过 `exec.CommandContext` 管理子进程生命周期，context 取消时自动终止
- 启动 goroutine 消费 stdout 输出，防止子进程写管道阻塞
- stderr 输出缓存，启动失败时用于调试

### 7.6 模块内测试独立性

- 同一模块内的测试共享同一个服务器和数据库实例
- 每个测试函数应尽量清理自己产生的数据，避免相互影响
- 对于列表查询等依赖前置数据的测试，可在测试函数内准备数据并清理

### 7.7 Windows 防火墙

- 测试子进程会启动 `ai-gateway-api.exe` 并监听随机端口
- 测试配置已设置 `ServerAddr = "127.0.0.1"`，仅监听回环地址，可避免 Windows 防火墙弹窗
- 如果仍然收到防火墙提示，可手动放行 `ai-gateway-api.exe`，或将测试监听端口范围加入防火墙白名单

### 7.8 API 路径未匹配

- `/open-api/v1/*` 和 `/inner-api/v1/*` 路径未匹配到任何路由时，统一返回 JSON 格式的 `404 Not Found`
- 避免返回静态文件 HTML，方便测试断言

---

## 八、附录

### 8.1 相关文档

| 文档 | 路径 | 说明 |
|------|------|------|
| 测试使用说明 | `test/integration/README.md` | 集成测试详细使用说明 |
| Cluster 模块用例设计 | `test/integration/tests/clusters/design.md` | 含 `llm_config.match_prefix` / `strip_prefix` 创建/更新校验用例 |
| InnerAPI TlsConf 用例设计 | `test/integration/tests/innerapi/tls_conf/design.md` | 含 `AIConf.MatchPrefix` / `StripPrefix` 导出验证用例 |
| Batch 模块用例设计 | `test/integration/tests/batch/design.md` | 批量任务管控（一期）；含产品线注入缺陷记录（§2.3）与现状钉死用例 |
| OpenAPI 接口文档 | `design-docs/api-define/OpenAPI接口定义/README.md` | 各模块接口定义索引 |
| InnerAPI 接口文档 | `design-docs/api-define/InnerAPI接口定义/README.md` | InnerAPI 接口详细设计索引 |
| 系统设计文档 | `design-docs/sys-design/` | 系统总体与详细设计 |

---

**文档结束**
