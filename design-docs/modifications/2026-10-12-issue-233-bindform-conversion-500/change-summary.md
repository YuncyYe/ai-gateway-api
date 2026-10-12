# Issue #233：Report `stream=not-bool` 返回 500（BindForm 类型转换错误未包装为参数错误）修复方案

## 1. 问题来源

[rainway-ai-gateway/ai-gateway-api/issues/233](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/233)

> Report 五个查询端点的共有过滤项 `stream` 接收非法布尔值（如 `stream=not-bool`）时，返回 HTTP 500 `{"ErrNum":500,"ErrMsg":"Unknown Exception: strconv.ParseBool: parsing \"not-bool\": invalid syntax"}`；预期为 HTTP 422 `{"ErrNum":422,"ErrMsg":"Param Illegal: ..."}`，与同矩阵其它 15 个非法参数用例及同族 `err_only=not-bool` 的正确包装一致。2026-10-09 复测确认 overview/logs/timeseries/rankings/distribution 五端点全部复现。

产品条款依据（Oracle）：

- `design-docs/api-define/OpenAPI接口定义/report.md` §3 错误码表：参数缺失/非法（含非枚举值、超上限等非法取值）→ **HTTP 422（ErrNum 422，Param Illegal）**；
- `design-docs/api-define/OpenAPI接口定义/00-common.md` 全局返回码：`422：参数不合法造成的失败`；`500：其他业务逻辑错误`。非法布尔取值属参数不合法，不属业务逻辑错误；
- 同族正确包装实证：`GET /report/logs?err_only=not-bool` → 422 `Param Illegal: invalid err_only: not-bool`。

影响：非法输入触发 500 违背错误语义契约；内部 Go 解析错误原文回显给调用方（错误信息卫生）；监控/告警把参数错误误报为服务端故障，干扰 SLO 与排障。P2。

## 2. 根因（已对当前检出逐环直读核实，2026-10-12）

`stream` 是 report 共有过滤项中唯一以 `*bool` struct tag 直接绑定的参数，其余同族参数均先按 string 取值再显式解析包装：

- `err_only`：`endpoints/openapi_v1/report/logs.go:53-59`，经 `parseBoolParam`（`logs.go:86-93`）→ `xerror.WrapParamErrorWithMsg("invalid %s: %s", ...)`；
- `mirror_hit`：`endpoints/openapi_v1/report/params.go:123-129`，同样经 `parseBoolParam`。

`stream` 的漏网路径逐环如下：

1. **`endpoints/openapi_v1/report/params.go:38`** — `Stream *bool \`form:"stream"\``，`bindCommonQuery`（`params.go:62-68`）经 `xreq.BindForm` 绑定，五个端点共用。
2. **`lib/xreq/internal/binding.go:249-258`** — `setBoolField` 调用 `strconv.ParseBool`，出错时原样 `return err`，不包装。
3. **`lib/xreq/internal/binding.go:146-177 / :179-225`** — 转换错误经 `setByForm` → `setWithProperType` → `mapping` → `MapForm`（`:33-35`）逐层裸上抛。`MapUri`（`:29-31`）复用同一实现。
4. **`lib/xreq/param.go:139-150`（根因所在）** — `BindForm` 对 `internal.MapForm` 的错误直接 `return err`，不做分类。同一函数族其余路径均已包装：`JSONDeserializer`（`param.go:76`）`xerror.WrapParamError`、`ValidateData`（`param.go:92`）`xerror.WrapParamErrorWithMsg`、自定义 `Validate()`（`lib/xreq/validate.go`）`xerror.WrapParamError`；只有"表单/URI 类型转换"这一条漏包装。
5. **`lib/xerror/resolve.go:50-60 / :102-104`** — 未包装错误的 `unwrapMsg` 不匹配任何已知类型，落入 default：`ErrNo` 保持初值 500、`Type="Unknown Exception"`、`Msg=errors.Cause(err)`（即 `strconv.ParseBool` 原文）；只有匹配 `etParam` 才置 422 / "Param Illegal"（`:64-66`）。
6. **`lib/xreq/result.go:135-147（parseError）/ :191（Render）** — Code 落 `500 <= Code < 600` 分支原样输出 HTTP 500，`ErrMsg` 拼为 `"Unknown Exception: " + strconv 原文`。

## 3. 影响面（grep 全量核实，以当前检出为准）

与 issue 报告略有出入（issue 成文后 `security` 模块合入），当前实际数据：

| 类别 | 数量 | 位置 |
|------|------|------|
| `*bool` + `form:` | 5 处 | `report/params.go:38`（stream，本卡）；`api_key/list.go:43`（enabled）、`:45`（unlimited_quota）；`model/shared/types.go:223`（RouteRulesFilter.Enabled，消费方 `route_tables/endpoints.go:48`，对应 SC2101-TC029）；`security/list.go:40`（dry_run） |
| `*int/*int64/*int8` + `form:` | 约 25 处 | report `start`/`end`（`params.go:32-33`）；api_key `page`/`page_size`；operation_log `status`/`start_time`/`end_time`/`page`/`page_size`（`list.go:71-75`）；batches `start_time`/`end_time`/`cursor`/`limit`（`list.go:32-35`）；entity `quota_plan_id`/`route_rules_id`/`page`/`page_size`（`entity.go:50-53`）；entity_type `id`/`level`/`page`/`page_size`（`entity_type.go:30-34`）；`model/shared/types.go:20-21`（Pagination 内嵌，多列表端点共用）；RouteRulesFilter `page`/`page_size` |
| `xreq.BindForm` 调用点 | 14 处 | gslb_data、export_util、report、epp_assignments、entity_type、entity、product、operation_log、batches、auth/user_product_list、subcluster、security、route_tables、api_key |
| 数值 `uri:` 字段（共用 `MapUri` 实现） | 1 处 | `endpoints/middleware/product_probe.go:27`（`ProductID *int64 uri:"product_id"`，经 `BindURI`） |

上述字段的非法取值当前全部走同一条 500 路径（除 issue 已实跑的 report 五端点与 route-tables `enabled` 外，其余未经实跑，但源码路径相同，属确定性推导）。

已确认的安全垫：全库 grep 无 slice/array 类型的 `form:` 消费方（`binding.go` 的 `setSlice`/`setArray` 分支实际无人走到）；`test/integration` 无任何用例断言这些路径返回 500（现有 `report/query/cases_test.go:626-661 TestParamValidation` 只断言 422），改为 422 不冲突。

## 4. 目标

1. report 五端点 `stream=not-bool` → HTTP 422 `{"ErrNum":422,"ErrMsg":"Param Illegal: invalid stream: not-bool"}`，与 `err_only` 同族包装同型；
2. 一次性闭掉同根因的全部消费方：所有 `BindForm` 数值/布尔 form 字段（含 route-tables `enabled` → SC2101-TC029、api-keys `enabled`/`unlimited_quota`、security `dry_run` 及约 25 个数值字段）非法取值同口径返回 422；
3. 不再回显 `strconv` 等内部 Go 解析错误原文；
4. 合法请求行为零变化（含显式 `stream=true|false`、省略 `stream`、`stream=` 空值按 false 处理的现有语义）。

## 5. 最终方案（方案 B：根因修复）

对 issue 给出的两个排查方向，**采纳方案 B（框架层根因修复），否决方案 A（仅改 report）**。方案 A 只把 `commonQuery.Stream` 改成 `parseBoolParam` 同型手写解析，五个端点虽闭环，但 route-tables / api-keys / security 及全部数值 form 字段仍 500，同类缺陷必然以新 issue 复发；且方案 B 的改动面（见 §5.3 回归分析）只触及"本就失败的错误路径"，合法流量无感。

三处协调改动：

### 5.1 `lib/xreq/internal/binding.go`：转换失败产出字段感知错误

`setByForm`（`binding.go:166-176` default 分支）拦截 `setWithProperType` 的错误，替换为字段感知文案（与 `err_only` 的 `invalid <name>: <value>` 同型）：

```go
default:
    var val string
    if !ok {
        val = opt.defaultValue
    }

    if len(vs) > 0 {
        val = vs[0]
    }
    if err := setWithProperType(val, value, field); err != nil {
        return false, fmt.Errorf("invalid %s: %s", tagValue, val)
    }
    return true, nil
}
```

要点：

- `tagValue` 为 form/uri tag 名（如 `stream`、`page`），文案形如 `invalid stream: not-bool`；此处**有意不保留**底层 `strconv` 错误链——`xerror.Resolve` 以 `errors.Cause(err)` 作为对外 `Msg`，保留链会重新泄漏原文；原始值已在文案中，足够服务端定位（与 `parseBoolParam` 丢弃链的既有先例一致）；
- 空值语义不变：`setBoolField`/`setIntField` 等对空串分别按 `false`/`0` 处理后才到不了报错分支，只有 key 存在且值非空、转换失败才报错；
- 只包 default 分支：`Slice`/`Array` 分支（`binding.go:153-165`）全库无 form 消费方，维持原样，避免无谓扩散；
- `MapForm` 与 `MapUri` 共用 `setByForm`，本改动同时覆盖 product_probe 的 `product_id`。

### 5.2 `lib/xreq/param.go`：BindForm/Bind/BindURI 统一包装 PARAM

```go
func BindForm(req *http.Request, data interface{}) error {
    req.ParseForm()
    if err := internal.MapForm(data, req.Form); err != nil {
        return xerror.WrapParamError(err) // 新增：类型转换错误归类为参数错误
    }
    ...
}
```

`Bind`（`param.go:104`）与 `BindURI`（`param.go:128`）的 `internal.MapUri` 错误同样补 `xerror.WrapParamError`。至此 `xreq` 公开绑定函数的错误分类全部闭环，与该函数族既有三条包装路径对齐。

最终响应推导：`fmt.Errorf("invalid stream: not-bool")` → `WrapParamError` 前缀 `PARAM` → `unwrapMsg` 命中 `etParam` → `Resolve` 置 `ErrNo=422`、`Type="Param Illegal"`、`Msg="invalid stream: not-bool"` → `Render` 输出 HTTP 422，`ErrMsg="Param Illegal: invalid stream: not-bool"`。

### 5.3 report 端点零改动

`commonQuery.Stream` 保留 `form:"stream"` tag 绑定——框架修复后自动获得 422 正确包装与"key 缺失 → nil、空值 → false"语义，无需在 report 包内追加手写解析（手写反而制造第二口径）。`err_only`/`mirror_hit` 的 `parseBoolParam` 路径保持现状，两条路径对外文案同型。

### 5.4 行为变化表

| 场景 | 修复前 | 修复后 |
|------|--------|--------|
| 任意 BindForm/BindURI 数值/布尔字段非法取值 | HTTP 500 `Unknown Exception: strconv....` | HTTP 422 `Param Illegal: invalid <name>: <value>` |
| product_probe `product_id=abc` | HTTP 500 + strconv 原文 | HTTP 422 `Param Illegal: invalid product_id: abc` |
| 合法取值 / 缺省 / 空值 | 不变（200） | 不变（200） |

回归风险分析：改动只触及"类型转换已失败"的错误路径，成功路径逐字节不变；受影响请求当前全部是 500 失败响应，按 `00-common.md` 本就该是 422，属行为向契约收敛而非契约变更。SC2101-TC029（route-tables `enabled=not-bool` 观测 500）随本方案一并闭环。

## 6. 测试

### 6.1 单元测试

- 新增 `lib/xreq/internal/binding_test.go`：`MapForm` 绑 `*bool`/`*int` 字段——非法值（`not-bool`、`abc`）→ error 文案 `invalid stream: not-bool` 精确断言；合法值（`true`/`1`）与缺 key → nil；`stream=` 空值 → 绑定 false 不报错；`MapUri` 同组用例（`product_id=abc` → `invalid product_id: abc`）。
- 新增 `lib/xreq/param_test.go`（`package xreq`，同 `validate_test.go` 惯例）：`BindForm`/`Bind`/`BindURI` 转换失败时 `xerror.Resolve(err)` 断言 `ErrNo==422`、`Type=="Param Illegal"`、`Msg=="invalid stream: not-bool"`；已包装错误二次 `WrapParamError` 幂等（`wrap.go:61-63` 不重复前缀）。

### 6.2 集成测试（`test/integration`，真实二进制）

- 扩展 `test/integration/tests/report/query/cases_test.go` 的 `TestParamValidation`（`:631-650` case 表）：新增 5 条 `invalid_stream_{overview,logs,timeseries,rankings,distribution}`（`stream=not-bool`）→ 断言 422 且 `ErrMsg` 含 `invalid stream`；补 `stream=true`/`stream=false` 200 用例（若种子数据已有 stream 请求，可对 `total>0` 做弱断言）。
- 同根因回归扫批（各域现有 list 测试文件中追加非法取值用例，断言 422 + `Param Illegal`）：
  - api_key：`GET /api-keys` `enabled=not-bool`、`unlimited_quota=not-bool`、`page=abc`；
  - route_tables：`enabled=not-bool`、`page=abc`（覆盖 SC2101-TC029 场景）；
  - security（mgmt 白名单域 list）：`dry_run=not-bool`；
  - operation_log：`status=abc`、`start_time=abc`；
  - batch：`limit=abc`、`cursor=abc`；
  - entity / entity_type：`page=abc`、`id=abc`（entity_type）；
  - product probe：带 `product_id=abc` 的产品探测路径 → 422。

实施时对 §6.2 的两处合理偏离（已在 §8 记录）：扫批用例落地为独立跨域套件 `tests/param_binding/`（而非分散改 7 个域的 list 测试文件），便于框架级回归的统一维护；product_probe 的集成用例取消——全库路由无 `{product_id}` 路径变量（OpenAPI v1 仅 `{product_name}` 字符串变量），`BindURI` 包装由单元测试覆盖。

### 6.3 回归验证

1. `go build ./...`、`go vet ./...`、`go test ./...` 全绿；
2. `make test-model-cover-gate`（model ≥70%）——本次改动集中在 `lib/` 与测试，model 层无改动，预期无波动；
3. 集成：`test/integration/tests/report/...` 全量 PASS 后按 issue #233 复现脚本重验——五端点 `stream=not-bool` 全部 422 且不回显 `strconv` 原文，显式 `stream=true|false` 与省略 `stream` 保持 200；
4. §6.2 扫批用例全 PASS。

## 7. 文档与契约

- `design-docs/api-define/` **无需改动**：`report.md` §3 与 `00-common.md` 本就规定非法参数 → 422，本次是运行行为向已批准契约收敛，接口契约（字段、枚举、成功响应）零变化；
- `design-docs/sys-design/接口层设计文档.md` §6.4 同步：修正绑定函数清单（原列出的 `BindQuery` 不存在），并补充"四类绑定函数失败统一包装 PARAM（422），不回显底层解析错误"的约定句；
- 变更记录即本文档；无数据迁移、无配置变更、无数据面（BFE/conf-agent）联动。

## 8. 实施记录

实施内容与 §5 方案一致，已完成并验证（2026-10-12）。

**改动文件**：

- `lib/xreq/internal/binding.go`：`setByForm` default 分支拦截 `setWithProperType` 错误，产出字段感知文案 `invalid <tag>: <value>`，不保留 strconv 错误链；
- `lib/xreq/param.go`：`BindForm`/`Bind`/`BindURI` 对 `internal.MapForm`/`MapUri` 错误统一 `xerror.WrapParamError`；
- 新增 `lib/xreq/internal/binding_test.go`、`lib/xreq/param_test.go`（含二次包装幂等用例）；
- `test/integration/tests/report/query/cases_test.go`：`TestParamValidation` 新增 5 条 `invalid_stream_*` 用例（含 `invalid stream: not-bool` 精确文案与无 `strconv` 泄漏断言），新增 `TestStreamFilterValidValues`；
- 新增 `test/integration/tests/param_binding/binding_test.go`：跨域扫批套件（17 条非法取值 422 用例 + 5 条合法值 200 用例 + 错误信息泄漏专检）；
- 顺手修复既有 flake：`test/integration/tests/ai_context_settings/get/get_test.go:76` 对 map 迭代序结果误用 `assert.Equal`（~50% 假失败，`-count=8` 复现 4/8），改 `assert.ElementsMatch`——与本改动无关的独立缺陷，全量集成首次运行时暴露，单独标注。

**验证结果**（环境无 make，按 Makefile target 等价命令执行）：

- 构建：`go build ./...`、`go vet ./...` 通过；`go build -o ai-gateway-api.exe`（等价 `make build`，集成测试所需二进制）；
- 单元测试：`go test ./...` 全量 96 包通过（exit 0）；
- model 覆盖率门禁：85.0% ≥ 70%，通过；
- 集成测试（重建二进制后，`REPORT_MYSQL_DSN` 指向本地 MySQL 8.4，测试自建临时库 `report_it_<ns>` 并清理）：
  - report 参数校验（issue #233 复现矩阵）：`TestParamValidation` 15 用例全 PASS——五端点 `stream=not-bool` 全部返回 422 `Param Illegal: invalid stream: not-bool`，不回显 `strconv` 原文；`TestStreamFilterValidValues` 显式 true/false/缺省均 200；
  - 跨域扫批 `tests/param_binding`：17 条非法取值用例全 PASS（api_keys/route_tables/sweeps/operation_logs/batches/entities/entity_types），合法值用例全 200，泄漏专检通过；
  - 全量回归 `./tests/...` 共 126 包全 PASS（exit 0；全量并行下观察到一次 `innerapi/tls_conf` 的 `SQLITE_BUSY` 环境 flake，单独复跑通过，与本改动无关）。

**结论**：issue #233 五端点 500 缺陷修复落地；同根因的 route-tables `enabled`（SC2101-TC029）、api-keys、security 及全部数值/布尔 form 字段非法取值统一收敛为 422，错误信息不再回显 Go 内部解析错误。report 端点代码零改动（§5.3 预期），接口契约零变化。

**沉淀评估（六步法 Step 6）**：xreq 绑定层错误包装约定已内联进 `接口层设计文档.md` §6.4，机制属对既有 xerror 约定的补齐而非新核心机制，不再单独建 `sys-design/details/` 文档。
