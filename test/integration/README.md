# AI Gateway API 集成测试

## 简介

`integration/` 是 `ai-gateway-api` 的本地离线集成测试环境。通过 `make build` 编译项目二进制，使用 SQLite 本地数据库运行完整的 API 测试，无需 MySQL/Redis 等外部依赖。

## 目录结构

```
integration/
├── go.mod / go.sum                # 独立 Go module（通过 replace 指向主项目）
├── conf/
│   ├── ai_gateway_api.toml        # 测试配置文件（SQLite + SkipTokenValidate）
│   ├── nav_tree.toml              # 导航树配置
│   └── i18n/
│       └── zh.toml                # 中文国际化
├── data/                          # SQLite 数据库文件（自动创建，含 .gitkeep）
├── testutil/                      # 测试工具包
│   ├── server.go                  # 编译/复制二进制、子进程启动/关闭管理
│   ├── client.go                  # HTTP 客户端封装（Get/Post/Put/Patch/Delete）
│   ├── assert.go                  # 断言函数（AssertSuccess, AssertErrCode 等）
│   ├── fixture.go                 # 测试数据工厂（随机字符串、唯一名称、证书生成等）
│   └── db.go                      # SQLite 数据库初始化/清理
└── tests/                         # 测试用例代码 + 设计文档（按模块组织）
    ├── api_key/
    │   ├── design.md
    │   └── create/create_test.go
    │       ...
    ├── ai_cache/                    # AI 缓存规则（集合级 PUT/GET + Inner 导出）
    │   ├── design.md
    │   ├── update/update_test.go
    │   └── get/get_test.go
    ├── ai_context/                  # AI 上下文压缩规则（集合级 PUT/GET，mode 必填 + cond 编译校验）
    │   ├── design.md
    │   ├── update/update_test.go
    │   └── get/get_test.go
    ├── ai_context_settings/         # AI 上下文压缩全局设置（单例 upsert，空表=默认值）
    │   ├── design.md
    │   ├── get/get_test.go
    │   └── update/update_test.go
    ├── traffic_mirror/              # 流量镜像规则（集合级 PUT/GET + Inner 导出）
    │   ├── design.md
    │   ├── update/update_test.go      # 含 //go:build mysql 并发用例（需 AIAPI_MYSQL_DSN）
    │   └── get/get_test.go
    ├── ai_route/
    ├── auth/
    ├── batch/                       # 批量任务管控（一期：list/detail/cancel/file + helper）
    ├── entity/
    ├── entity_type/
    ├── clusters/
    ├── certificate/
    ├── model_provider/
    ├── model_price/
    ├── operation_log/
    ├── report/                    # 报表查询（组 A 离线必跑；组 B/C 需 REPORT_MYSQL_DSN）
    │   ├── design.md
    │   ├── not_assembled/not_assembled_test.go
    │   ├── partition/partition_test.go
    │   └── query/query_test.go
    ├── schema/                    # 接口契约 schema 校验（report 组需 REPORT_MYSQL_DSN）
    │   ├── openapi/
    │   ├── innerapi/
    │   └── report/
    └── innerapi/
```

## 环境要求

| 项目 | 要求 |
|------|------|
| Go | 1.22+ |
| 操作系统 | Windows / Linux / macOS |
| 磁盘空间 | 建议 2GB+（go-sqlite 纯 Go 编译需要较大临时空间） |
| 外部依赖 | 无（SQLite 纯 Go 实现，无需 CGO） |

## 快速开始

### 1. 编译项目二进制

```bash
# 在 ai-gateway-api 项目根目录编译
make build
# 或者: go build -o ai-gateway-api.exe .
```

### 2. 下载测试依赖

```bash
cd ai-gateway-api/test/integration
go mod tidy
```

### 3. 运行测试

```bash
# 运行所有模块测试
cd ai-gateway-api/test/integration
../scripts/run_all_tests.sh
# 或者: go test -v -count=1 -timeout 300s ./tests/...

# 运行指定模块测试
../scripts/run_module_tests.sh api_key
# 或者: go test -v -count=1 -timeout 120s ./tests/api_key/...

# 运行单个接口测试
go test -v -count=1 -timeout 120s ./tests/api_key/create/

# 运行单个用例
go test -v -run TestCreate_Normal_MinimalParams ./tests/api_key/create/
```

### MySQL 并发用例（build tag 隔离）

并发正确性（ID 生成竞态、行锁超时，issue #80/#99/#132；批量任务 cancel
抢占原子性 BT-3-007）在 SQLite 单连接串行
环境下无法暴露，相关用例以 `//go:build mysql` 隔离，需真实 MySQL：

```bash
# admin DSN 不带库名；测试自动建库 ai_gateway_it_*、执行 db_ddl.sql、用毕删库
AIAPI_MYSQL_DSN="root:pass@tcp(127.0.0.1:3306)/" \
  go test -tags mysql -count=1 -timeout 600s \
  ./tests/api_key/create/ ./tests/entity/create/ -run Concurrent
```

未设置 `AIAPI_MYSQL_DSN` 时上述用例自动 Skip；不带 `-tags mysql` 时不编译。

### 双后端全量运行（StartServerAuto / StartServerWithMonitorAuto）

批量特性新增包（`tests/batch/*`、`tests/model_price/batch_price`、
`tests/api_key/batch_limits`）的 TestMain 使用 `testutil.StartServerAuto`：
默认 SQLite；设置 `AIAPI_MYSQL_DSN` 后同一批用例在 MySQL 上运行（自动
建库 `ai_gateway_it_*`、执行主项目 `db_ddl.sql`、用毕 DROP）。因此：

```bash
# 仅跑 MySQL 隔离的并发用例
AIAPI_MYSQL_DSN="root:pass@tcp(127.0.0.1:3306)/" \
  go test -tags mysql -count=1 -timeout 600s ./tests/batch/cancel/ -run Concurrent

# 三个模块全量用例跑到 MySQL 上（新包自动切换后端，既有包仍为 SQLite）
AIAPI_MYSQL_DSN="root:pass@tcp(127.0.0.1:3306)/" \
  go test -count=1 -timeout 900s ./tests/batch/... ./tests/model_price/... ./tests/api_key/...
```

`tests/secret_at_rest/`（DB 落盘加密）使用 `testutil.StartServerWithMonitorAuto`
——与上面同一套后端选择，额外启用监控端口（`/reload/security`）。**该模块推荐在
MySQL 后端上跑一遍**：`providers.api_keys` 若是 `JSON` 列，整列 `enc$v1$` 信封在
MySQL 严格模式下写入报 `Error 3140`，而 SQLite 的 `TEXT` 列不校验 JSON、测不出来
（issue #264，用例 SAR-4-001）：

```bash
AIAPI_MYSQL_DSN="root:pass@tcp(127.0.0.1:3306)/" \
  go test -count=1 -timeout 900s ./tests/secret_at_rest/...
```

### 4. 清理运行时数据

```bash
../scripts/clean.sh
```

## 配置说明

### 数据库配置

```toml
[Databases.bfe_db]
DBName  = "./data/test_ai_gateway.db"  # SQLite 文件路径（自动包含进程 ID 隔离）
Driver  = "sqlite"                      # 使用 glebarez/go-sqlite 纯 Go 驱动
MaxOpenConns = 5                        # 防止连接池死锁
```

- 每次测试启动时自动执行 `db_ddl_sqlite.sql` 初始化表结构
- 数据库文件名包含进程 PID，确保多进程并发测试不会冲突
- 测试结束后自动删除数据库文件（包括 WAL/SHM 文件）

### 认证配置

```toml
[RunTime]
SkipTokenValidate = true           # 跳过 Token 认证，所有请求直接放行
```

### 服务器配置

```toml
[Server]
ServerPort = 8199                  # 运行时会被替换为随机端口
MonitorPort = -1                   # -1 = 禁用监控端口
```

## testutil 工具包 API

### 服务器管理

```go
// 启动测试服务器（自动初始化 SQLite、加载配置、注册路由）
sm, err := testutil.StartServer()

// 停止测试服务器并清理资源
defer sm.Shutdown()
```

### HTTP 客户端

```go
client := testutil.GetClient()

// GET 请求
resp, err := client.Get("/open-api/v1/ai-route-rules")

// POST 请求
resp, err := client.Post("/open-api/v1/api-keys", body)

// PUT 请求
resp, err := client.Put("/open-api/v1/entities/{id}", body)

// PATCH 请求
resp, err := client.Patch("/open-api/v1/ai-route-rules", body)

// DELETE 请求
resp, err := client.Delete("/open-api/v1/api-keys/{id}")
```

### 响应结构

```go
type APIResponse struct {
    ErrNum   int             `json:"ErrNum"`   // 错误码：200=成功
    ErrMsg   string          `json:"ErrMsg"`   // 错误信息
    Data     json.RawMessage `json:"Data"`      // 返回数据
    WorkMode string          `json:"WorkMode"`  // 工作模式
}
```

### 断言函数

| 函数 | 说明 |
|------|------|
| `AssertSuccess(t, resp)` | 验证 ErrNum=200 |
| `AssertErrCode(t, resp, code)` | 验证指定错误码 |
| `AssertDataNotEmpty(t, resp)` | 验证 Data 不为空 |
| `AssertDataFieldEquals(t, resp, field, expected)` | 验证 Data 中指定字段值 |
| `AssertDataFieldNotEmpty(t, resp, field)` | 验证 Data 中指定字段不为空 |
| `AssertListLen(t, resp, len)` | 验证 Data 中的列表长度 |
| `AssertListFieldLen(t, resp, field, len)` | 验证 Data 中列表字段长度 |
| `AssertPagination(t, resp, page, pageSize, minTotal)` | 验证分页信息 |

### 辅助函数

```go
// 生成唯一名称
name := testutil.UniqueName("prefix")    // prefix-a1b2c3

// 生成随机字符串
s := testutil.RandomString(8)            // 8位随机字符串

// 生成自签名证书
certPEM, keyPEM, err := testutil.GenerateTestCert("common-name")

// 指针工具
p := testutil.StringPtr("value")
p := testutil.Int64Ptr(123)
p := testutil.BoolPtr(true)
```

## 测试用例编写规范

### 文件结构

测试用例按模块组织，每个模块目录下包含：

- **`design.md`**：该模块的测试用例设计文档
- **`{interface}/{interface}_test.go`**：各接口的 Go 测试代码

```
integration/tests/{module}/
├── design.md                              # 测试用例设计文档
├── create/create_test.go                  # 创建接口测试
├── list/list_test.go                      # 列表接口测试
└── ...
```

### 测试代码模板

```go
package create

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

func TestCreate_NormalCase(t *testing.T) {
    body := map[string]interface{}{
        "field": "value",
    }
    resp, err := testutil.GetClient().Post("/open-api/v1/xxx", body)
    if err != nil {
        t.Fatalf("request failed: %v", err)
    }
    testutil.AssertSuccess(t, resp)
    testutil.AssertDataFieldEquals(t, resp, "field", "value")
}
```

### 表格驱动测试推荐

同一接口的多个相似场景建议使用表格驱动测试：

```go
func TestCreate(t *testing.T) {
    tests := []struct {
        name     string
        body     map[string]interface{}
        wantCode int
    }{
        {"最小参数", map[string]interface{}{"description": "test"}, 200},
        {"缺少必填", map[string]interface{}{}, 422},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            resp, _ := testutil.GetClient().Post("/open-api/v1/api-keys", tt.body)
            testutil.AssertErrCode(t, resp, tt.wantCode)
        })
    }
}
```

## 注意事项

1. **磁盘空间**：`modernc.org/sqlite` 是纯 Go 实现的 SQLite，编译时需要较大临时空间（约 1GB）。如果磁盘空间不足，请先清理 Go 缓存：
   ```bash
   go clean -cache -testcache
   ```

2. **进程隔离**：每个测试包使用独立的数据库文件（含 PID），多进程可并发运行。

3. **认证跳过**：配置 `SkipTokenValidate = true`，所有 API 请求无需 Token 认证。

4. **不污染源码**：`integration/` 和 `tests/` 均在 `test/` 目录下，通过独立 Go module 和 replace 指令引用主项目，不会修改任何程序代码。

## Schema 集成测试

为严格校验每个接口返回值是否符合 `design-docs/api-define` 中的定义，项目新增了 schema 集成测试：

```bash
# 全部 schema 测试
go test -v -count=1 -timeout 300s ./tests/schema/...

# 仅 OpenAPI schema 测试
go test -v -count=1 -timeout 300s ./tests/schema/openapi/...

# 仅 InnerAPI schema 测试
go test -v -count=1 -timeout 300s ./tests/schema/innerapi/...

# Report schema 测试（需 REPORT_MYSQL_DSN，未设置时自动 Skip）
REPORT_MYSQL_DSN="root:****@tcp(127.0.0.1:3306)/" go test -v -count=1 -timeout 300s ./tests/schema/report/...
```

### 目录说明

```
tests/schema/
├── openapi/                 # OpenAPI v1 schema 定义与测试
│   ├── schema.go            # 公共类型（QuotaPlan、RateLimitPolicy、RouteRules 等）
│   ├── api_key.go
│   ├── entity.go
│   ├── entity_type.go
│   ├── cluster.go
│   ├── certificate.go
│   ├── auth.go
│   ├── model_price.go
│   ├── route_tables.go
│   ├── global_route_rules.go
│   ├── tools.go
│   └── openapi_schema_test.go
└── innerapi/                # InnerAPI v1 schema 定义与测试
│   ├── schema.go            # 各导出配置顶层 schema
│   └── innerapi_schema_test.go
└── report/                  # /report/* schema 定义与测试（REPORT_MYSQL_DSN 门控）
    ├── schema.go            # OverviewResult / CostItem / CostMetricPoint / LogRow schema
    └── report_schema_test.go
```

### 校验框架

通用校验器位于 `testutil/schema.go`，支持：

- `AssertSchema`：校验对象字段存在性与类型
- `AssertListSchema`：校验数组及元素结构
- `AssertPagedListSchema`：校验 `{list, pagination}` 分页结构
- 必填字段、可选字段、嵌套对象、数组元素、枚举值校验

## 相关文档

| 文档 | 路径 |
|------|------|
| 测试设计方案 | [../docs/README.md](../docs/README.md) |
| OpenAPI 接口文档 | [ai-gateway-api/docs/zh_cn/open_api/](../../docs/zh_cn/open_api/) |
| InnerAPI 接口设计 | [design-docs/api-define/InnerAPI接口定义/README.md](../../../design-docs/api-define/InnerAPI接口定义/README.md) |
