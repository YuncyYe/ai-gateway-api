# mod_ai_intent 控制面支持：意图配置下发与意图条件消费 —— 变更摘要

## 1. 背景

数据面 BFE 已实现语义路由意图模块 `mod_ai_intent`（提交 `7e482d90`，分支
`v1.8.9-dev`）：

- 新增 condition 原语 `req_ai_intent_in(<question>, "<opt1>[|<opt2>...]" [, <min_conf>])`，
  路由规则 `ai_route.data` 的 `Cond` 可直接引用；
- 新增模块数据文件 `conf/mod_ai_intent/intent_questions.data`（JSON：
  `Version` + `MinConfidence` + `Questions[]`，choice/score 两类问题），支持
  monitor 热更（`GET /reload/mod_ai_intent?path=...`）；
- 决策服务地址等静态项在 `mod/mod_ai_intent.conf`（INI）。

控制面当前没有任何对应能力：意图配置（问题集 + 门控阈值）无处维护、无法下发；
路由规则的 cond 校验因 bfe 依赖未升级，尚不认识新原语。本方案补齐这两环。

> 命名说明：资源命名为 `intent-config`（意图配置），因其内容不止 questions
> （还含 `min_confidence` 与版本信息）；BFE 侧数据文件名 `intent_questions.data`
> 已随 `7e482d90` 发布且 `QuestionsPath` 可配，保持不动，文件名与资源名的差异为
> 有意保留。

## 2. 目标

1. 新增**意图配置**单例资源：OpenAPI 全量维护（PUT/GET），持久化，经
   InnerAPI 导出为 `intent_questions.data`，由 conf-agent 落盘并触发 BFE 热更；
2. 路由规则**保持 cond 直传模式不变**：意图条件由调用方在 cond 字符串中直接
   书写 `req_ai_intent_in(...)`（无 OpenAPI 模型变更）；
3. 校验闭环：`lib/validate` 的 cond 编译校验随 bfe 依赖升级识别新原语。

## 3. 关键决策

| # | 决策 | 理由 |
|---|------|------|
| 1 | 意图配置为**全局单例资源**（非租户/产品级） | BFE 侧 `intent_questions.data` 是模块级单文件；一期最小闭环，差异化下发列二期 |
| 2 | **全量 PUT + 单行覆盖**（version 每次更新；不保存历史版本） | 无历史查询需求；version 仅作下发链路的变更检测依据；与 BFE 侧"内容变更即新 Version"约定一致；回滚=重新 PUT 旧内容 |
| 3 | **路由规则不做结构化 intent filter 扩展，保持 cond 直传** | 新版 route_rules 链路本就是 cond 直传原样存储/导出；零模型侵入；意图条件书写成本可接受（原语签名简单）；二期如需可视化编排再评估结构化 |
| 4 | 引用完整性**不在控制面校验** | cond 中引用的问题名/选项由 BFE 运行时求值——未配置的问题名"永不命中"（fail-safe 语义），不报错不阻断；避免控制面与 BFE 配置状态的双端耦合 |
| 5 | `mod_ai_intent.conf` 静态项（决策服务地址/超时/缓存）**不下发** | 同 ai_cache 先例（"Redis 连接配置不下发"原则）：属部署期静态配置，控制面只下发数据文件 |

## 4. 范围

**本仓（ai-gateway-api）**：

- 新表 `intent_config`（MySQL DDL + SQLite DDL）+ DAO/storager；
- 新 model 包 `model/iintent_config/`（manager + 导出 Generator）；
- 新 OpenAPI：`GET/PUT /open-api/v1/intent-config`；
- `go.mod` 的 bfe 依赖升级（`lib/validate` 的 `ConditionExpression` 随之获得
  `req_ai_intent_in` 编译校验能力，无代码改动）；
- InnerAPI：`GET /inner-api/v1/configs/mod-ai-intent`；
- 容器装配 + 单测（model 覆盖率 ≥70% 门禁）。

**明确不做**：路由规则 OpenAPI 模型变更（无 intent_filters 字段）；决策服务
地址等静态项下发；多租户意图配置；dashboard 页面；访问日志 `ai_intent_*`
字段（BFE 侧已列二期）。

## 5. 非本仓登记点（链路协同，缺一不可）

| 仓库 | 事项 | 状态 |
|------|------|------|
| `bfe` | `mod_ai_intent` 模块 + `/reload/mod_ai_intent` + `req_ai_intent_in` 原语 | ✅ 已实现（`7e482d90`，v1.8.9-dev） |
| `conf-agent` | `conf/conf-agent.toml` 新增 `[Reloaders.mod_ai_intent]`：拉取 `/inner-api/v1/configs/mod-ai-intent` 落盘 `conf/mod_ai_intent/intent_questions.data`，`BFEReloadAPI = "/reload/mod_ai_intent"` | ⬜ 待实施（本方案第 4 章契约冻结后） |
| `ai-gateway-api` 的 bfe 依赖 | `go.mod` 中 `github.com/bfenetworks/bfe` 升级到含 `req_ai_intent_in` 的提交（fork v1.8.9-dev ≥ `7e482d90`），否则 `lib/validate` 的 `condition.Build` 拒绝新原语 | ⬜ 随本方案实施 |

## 6. 文档配套

- 本文（change-summary）+ `design-changes.md` + `api-changes.md` 三件套；
- 实施后更新 `design-docs/api-define/`、`design-docs/sys-design/summary.md`；
- BFE 侧对照：`bfe/docs/zh_cn/sys_design/mod_ai_intent.md`、
  `bfe/docs/zh_cn/configuration/mod_ai_intent/`（契约以 BFE 侧为冻结源）；
- 路由规则 cond 中原语用法：
  `bfe/docs/zh_cn/condition/request/intent.md`。
