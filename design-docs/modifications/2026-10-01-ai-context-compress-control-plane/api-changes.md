# /ai-context-rules 与 /ai-context-settings —— API 契约定义

> 数据面（BFE `mod_ai_context`）侧加载器契约已冻结并实现，见 BFE 仓 `docs/zh_cn/modifications/2026-10-01-ai-context-compress/design-changes.md` §4。本文定义控制面 Open API 与 Inner API 导出契约；导出 JSON 的 tag（`Defaults`/`Config` 首字母大写、字段 camelCase）为 conf-agent 硬契约，逐字不可改。

## 1. 变更概览

| 变更类型 | 端点 | 说明 |
|----------|------|------|
| 新增 | `GET /open-api/v1/ai-context-rules` | 全量查询上下文压缩规则集合（`id` 升序，即导出顺序） |
| 新增 | `PUT /open-api/v1/ai-context-rules` | 全量更新规则集合（单事务 delete-all + insert-all 整体替换） |
| 新增 | `GET /open-api/v1/ai-context-settings` | 查询全局调优设置（单例，空表返回默认值） |
| 新增 | `PUT /open-api/v1/ai-context-settings` | 全量更新全局调优设置（upsert 语义） |
| 新增（导出） | `GET /inner-api/v1/configs/ai-context-rule` | 导出 `context_rule.data`（BFE `mod_ai_context` 数据源） |

设计取向：rules/settings 均照 ai-cache 域形态——**集合资源仅 GET/PUT 全量读写**（无 `/{id}`、无 PATCH、无 enabled），**设置单例**（GET 读全量 + PUT upsert 单行覆盖）。本期**无任何既有端点变更**（精确 `context_window` 数据源二期另立变更，见 design-changes.md §11）。

## 2. 字段统一定义（Open API 词汇：小写下划线）

### 2.1 规则元素（`ai_context_rules`）

| 字段 | 类型 | 必填 | 说明 | 校验 |
|------|------|------|------|------|
| `cond` | string | **是** | bfe 条件表达式，规则匹配（复用 `mod_ai_cache` 规则模型） | 非空 + **控制面编译校验**（照 ai-cache 同款 `ConditionExpression`，非法表达式 422）；BFE 侧加载期编译失败整文件拒载为最后防线 |
| `mode` | string | **是** | 压缩档位：`off` / `conservative` / `balanced` / `aggressive`，**详细语义见 §2.1.1** | 枚举四值，缺失或非法 422 |
| `max_context_tokens` | int | 否 | 预算上限覆盖（token 数），**语义与算式见 §2.1.2** | ≥0；缺省/0 = 用模型表窗口 |
| `reserve_tokens` | int | 否 | 预留输出 token 数，**语义与算式见 §2.1.2** | ≥0；缺省/0 = 自动 `clamp(窗口×15%, 256, 16000)` |

- 优先级语义：数组顺序 = `id` 升序 = first-match-wins，与 `ai-cache-rules` 一致；
- 无 `enabled`、无 `name` 唯一键需求（cond 即身份，整体替换语义）——如后续需要规则名展示可再加，本期不做。

#### 2.1.1 mode 档位说明

`mode` 决定规则命中后 BFE 对请求 `messages` 执行哪些处理层。处理按降级管线执行，**每完成一层重新估算 token，压到预算以内即停**，因此实际执行到第几层取决于超长程度：

| 档位 | 执行的处理层 | 有损性 | 适用场景 |
|------|--------------|--------|----------|
| `off` | 不处理（命中即跳过，等价于未配置该规则） | — | 灰度占位、显式关闭；`default_t()` 兜底规则常用 |
| `conservative` | 仅**无损裁剪层**：① tool/function 结果按 `tool_result_max_chars` 截断（附 `...[truncated]` 标记）；② 旧内联图片只保留最近 `keep_latest_images` 张（被裁的替换为占位文本）；③ 删除除最后一条 assistant 外的所有 thinking 块（`thinking_policy=keep` 时跳过） | **无损**——保留内容原样不动，只丢弃/截短 | 首期灰度首选：零语义风险，直接收益是防 `prompt too long` 与工具结果爆窗口 |
| `balanced` | 裁剪层（同上）＋**规则改写层（lite 强度）**：tombstone 保护代码块/URL/路径/版本号/JSON key/数字等结构 token → 对普通文本做冗余措辞精简（中英文客套语、冗长连接词等）→ 还原保护块 → fidelity gate 校验（保护 token 存活率 ≥ `rewrite.protected_survival_rate`，默认 95%） | 有损，但有双重兜底：gate 不过自动回退为裁剪层产物；system prompt 与最后一条 user 消息永不改写 | 常态档位：典型收益约 15–30% token 缩减 |
| `aggressive` | 裁剪层＋规则改写层（**full 强度**，改写规则更激进） | 同上（一期） | 长会话/Agent 等成本敏感场景。**注意**：一期 aggressive 不含 LLM 会话摘要（管线 P3），摘要为二期能力，二期上线后 aggressive 才是完整形态 |

三个与档位无关的全局保证（任何非 `off` 档位均成立）：

1. **最新消息不动**：最后一条 user 消息（含多模态内容）与 system prompt 不被裁剪/改写——保证语义锚点与缓存 key 稳定；
2. **结构保真**：`tool_calls` 与其响应消息必须成对，裁剪拆散后由修复逻辑重组；修复后仍不合法则**整体回滚原始请求**直接转发（fail-open，宁可不压不可压坏）；
3. **触发是软阈值**：估算 prompt token 超过 `budget × trigger_ratio`（`trigger_ratio` 为 §2.2 全局设置，默认 0.7）才触发；任何处理失败同样回滚原始请求。

#### 2.1.2 预算字段说明（`max_context_tokens` / `reserve_tokens`）

这两个字段共同决定**压缩目标线 budget**（单位 token，估算值）：

```
budget = min(模型上下文窗口, max_context_tokens 若非 0) − 预留输出 token
其中：预留输出 token = reserve_tokens 若非 0，否则自动 clamp(窗口 × 15%, 256, 16000)
触发线 = budget × trigger_ratio        （估算超过触发线即启动处理，处理目标是压到 ≤ budget）
```

- **模型上下文窗口**：一期不由控制面下发，BFE 按模型名启发式识别（claude→200k、gemini→1M、gpt/codex→400k，其余→128k 兜底），识别兜底时记 `CTX_WINDOW_DEFAULTED` 计数器——**模型窗口不可见时，用 `max_context_tokens` 给出显式预算**。

字段逐项说明：

| 字段 | 含义 | 取值建议 |
|------|------|----------|
| `max_context_tokens` | 预算的**上限覆盖**：把"可用预算的输入部分"显式钉死到一个值，替代模型表窗口参与算式。设 0（默认）= 完全跟随模型表窗口（一期即启发式/兜底值）。 | ① 模型窗口识别不到或不可信（兜底计数器持续增长）时，按业务实际上限显式设定；② 多模型共用一条规则、而各模型窗口差异大时，取最小公约数；③ 想给输出留更多余量时，配合 `reserve_tokens` 调小输入预算 |
| `reserve_tokens` | 从窗口中**预留给模型输出**的 token 数——压缩目标不是窗口边缘，而是"窗口 − 预留"，给应答留出生长的空间。设 0（默认）= 自动 `clamp(窗口×15%, 256, 16000)`：128k 窗口自动预留约 16k，32k 窗口约 4.8k，8k 窗口约 1.2k。 | ① 预期长输出的场景（报告生成、代码补全）调大（如 8k–16k）；② 短问答场景可调小以放宽输入预算；③ 一般**不建议**手工设置，默认公式已覆盖主流场景 |

算例（默认 `trigger_ratio=0.7`）：

| 场景 | 窗口 | max_context_tokens | reserve_tokens | budget | 触发线 |
|------|------|--------------------|----------------|--------|--------|
| 128k 模型全默认 | 131072（128k 兜底） | 0 | 0（自动 → 16000） | 115072 | 80550 |
| 窗口不可信，显式钉上限 | 131072（被覆盖为 64000） | **64000** | 0（自动 → 9600，按覆盖后窗口计算） | 54400 | 38080 |
| 32k 模型 + 长输出 | 32768 | 0 | **8192** | 24576 | 17203 |

> 注意自动预留的取数窗口：`reserve_tokens=0` 时按 **`max_context_tokens` 覆盖后的窗口**计算 15%（覆盖场景见算例 2）。BFE 侧估算为启发式（字节/token，Defaults 可调），以上数值为设计值，实际触发以估算为准。若误配到 `budget ≤ 0`（如 `max_context_tokens` 小于预留输出），BFE 视为无效配置**跳过不处理**（fail-open）并记 debug 日志，不会报错拦截请求。

### 2.2 全局调优设置对象（`ai_context_settings`，单例）

| 字段 | 类型 | 必填 | 说明 | 校验 |
|------|------|------|------|------|
| `trigger_ratio` | float64 | 否 | proactive 触发阈值（占预算比例），**详见 §2.2.1** | (0, 1]，默认 `0.7` |
| `keep_latest_images` | int | 否 | 保留最近 N 张内联图片，**详见 §2.2.1** | ≥0，默认 `2`；0 = 不裁图 |
| `tool_result_max_chars` | int | 否 | 单条 tool 结果最大字符数，**详见 §2.2.1** | ≥0，默认 `2000`；0 = 不截断 |
| `thinking_policy` | string | 否 | thinking 块策略，**详见 §2.2.1** | `trim-all-but-last`（默认）/ `keep` |
| `chars_per_token` | int | 否 | 文本估算系数（字节/token），**详见 §2.2.1** | ≥1，默认 `4`；中文密集可调 3 |
| `image_token_estimate` | int | 否 | 单张内联图片估值 token，**详见 §2.2.1** | ≥0，默认 `1200` |
| `rewrite` | object | 否 | 改写层子对象，**详见 §2.2.1** | - |
| `rewrite.strength` | string | 否 | 规则改写强度 | `lite`（默认）/ `full` |
| `rewrite.protected_survival_rate` | float64 | 否 | fidelity gate 保护 token 存活率阈值 | (0, 1]，默认 `0.95` |

设置对象无 id/name 寻址字段（单例）；响应不携带 `created_at`/`updated_at`（全行覆盖语义下时间戳无信息价值，与 rules 一致；审计时间见 `GET /operation-logs`）。

```json
{
  "trigger_ratio": 0.7,
  "keep_latest_images": 2,
  "tool_result_max_chars": 2000,
  "thinking_policy": "trim-all-but-last",
  "chars_per_token": 4,
  "image_token_estimate": 1200,
  "rewrite": { "strength": "lite", "protected_survival_rate": 0.95 }
}
```

> `max_context_tokens` 说明：精确模型上下文窗口一期**不由控制面下发**（数据面按模型名启发式 + 128k 兜底，见 design-changes.md §11）；需要精确预算时通过规则级 `max_context_tokens` 覆盖。

#### 2.2.1 参数详解

各参数作用于 §2.1.1 所述降级管线的不同层，全部为**全局生效**（一期无规则级覆盖；二期 `override` 引入后可按规则差异化）。

**触发控制**

| 参数 | 默认 | 机制 | 调参指引 |
|------|------|------|----------|
| `trigger_ratio` | 0.7 | 触发线 = `budget × trigger_ratio`（算式见 §2.1.2）。估算 prompt token **超过触发线**才启动处理，处理目标是压到 ≤ budget。0.7 即"预算用到七成就开始压"，给估算误差和突发增长留 30% 余量 | 调**小**（如 0.5）= 更早触发、上下文更短、质量余量更大，处理请求占比上升；调**大**（如 0.9）= 更少触发、更接近硬上限，但估算偏低时有顶到窗口边缘的风险。调参依据：`CTX_SKIP_UNDER_THRESHOLD`（未触发量）与 `CTX_TRIGGERED_*`（触发量）的比例 |

**裁剪层（conservative 档的全部动作，也是 balanced/aggressive 的前置层）**

| 参数 | 默认 | 机制 | 调参指引 |
|------|------|------|----------|
| `tool_result_max_chars` | 2000 | 单条 tool/function 调用结果超过该字符数时截断为"前段 + `...[truncated]`"标记。Agent 场景工具输出（文件内容、命令回显）是上下文膨胀第一大来源 | 调**小**（如 500）显著降本但工具信息损失大；调**大**保留更多信息。0 = 不截断。建议按"工具输出对回答的必要性"定，日志/检索类输出可激进 |
| `keep_latest_images` | 2 | 多模态消息中的内联图片只保留**最近 N 张**，更旧的替换为占位文本 `[Earlier image removed to fit context window]`（单张按 `image_token_estimate` 计 token，是估值大头）。**受保护的消息不参与裁剪**：最后一条 user（含其图片）与 system 永不裁剪，因此多轮带图对话的"当前问题图"总是完整保留 | 视觉密集型应用调大；纯文本应用可调 0。0 = 完全不裁图 |
| `thinking_policy` | trim-all-but-last | `trim-all-but-last`：删除**除最后一条 assistant 外**所有消息中的 thinking/推理块（模型已消费过的历史推理对后续回答贡献低、token 占比高）；`keep`：不动 | 推理链完整性敏感的场景（如可解释性要求）用 `keep`；一般场景默认即可。最后一条 assistant 的 thinking 永远保留（它是当前推理上下文的一部分） |

**估算系数（决定 token 估值的标尺，间接影响触发时机与预算判断）**

| 参数 | 默认 | 机制 | 调参指引 |
|------|------|------|----------|
| `chars_per_token` | 4 | 文本估算：`token ≈ 字节数 / chars_per_token`，与 bfe 既有惯例（`GetPromptToken`）一致。中文 UTF-8 约 3 字节/字且一字多为 1–2 token，按 4 估算会**低估**中文密集流量约 30–50% | 中文为主的业务调 **3**；纯英文可保持 4。方向感：**系数调小 → 估算变大 → 更早触发**（宁可早压不可晚压）。变更后注意 `tokens_before/after` 指标前后不可比 |
| `image_token_estimate` | 1200 | 单张内联 base64 图片的固定估值（对齐 OmniRoute `IMAGE_TOKEN_ESTIMATE`）。base64 原文每张数百 KB，不按字节计 | 高分辨率/细节敏感图调大；经压缩的小图调小。与 `keep_latest_images` 联动决定图片对预算的占用 |

**改写层（balanced/aggressive 档的动作）**

| 参数 | 默认 | 机制 | 调参指引 |
|------|------|------|----------|
| `rewrite.strength` | lite | 规则改写的激进程度：`lite` 为保守规则包（中英文客套语、冗长连接词、重复空白等）；`full` 规则包更激进。改写前 tombstone 保护代码块/URL/路径/版本号/JSON key/数字字面量，改写后还原，fidelity gate 兜底 | 当前规则包下 full/lite 产物差异有限，差异行为以单测 `TestRewriteFullStrength` 为准；规则包随版本演进，调参先看 `CTX_DONE_REWRITE` 与压缩比 |
| `rewrite.protected_survival_rate` | 0.95 | fidelity gate 阈值：改写后受保护 token 的**存活率**（未被意外改动的比例）不得低于该值，否则本次改写**整体回退**为裁剪层产物（无损）。是"压缩收益 vs 语义安全"的安全阀 | 降**低**（如 0.9）允许更激进的改写通过；升**高**（如 0.99）近乎只在无损时接受改写。`CTX_GATE_FALLBACK` 持续偏高说明 gate 拦截频繁， either 降阈值 or 调小 strength |

**调参工作流建议**（上线后）：`mode=off` 全量部署 → 看 `CTX_SKIP_UNDER_THRESHOLD` 分布确认超长占比 → 开 `conservative` → 再 `balanced`，每步对照 `CTX_DONE_*` 出口阶段、压缩比（`tokens_before/after`）与 `CTX_GATE_FALLBACK`/`CTX_REPAIR_ROLLBACK` 比例。


## 3. Open API 明细

### 3.1 GET /open-api/v1/ai-context-rules

- 鉴权：`iauth.FA(iauth.FeatureAIContext, iauth.ActionRead)`；
- 响应 Data：规则数组（`id` 升序；元素含 §2.1 全部字段，可空字段按默认值填充，风格同 `ai-cache-rules`）。

### 3.2 PUT /open-api/v1/ai-context-rules

- 鉴权：`iauth.FA(iauth.FeatureAIContext, iauth.ActionUpdate)`；
- 请求体：`{ "rules": [ <规则元素>... ] }`（空数组 = 清空规则集合）；
- 单事务 delete-all + insert-all 整体替换；`mode` 缺失/非法、数值越界整体 422 且集合不变；
- 响应 Data：同 GET；
- 操作审计：一条 update 审计，`before`/`after` 为规则集合快照。

### 3.3 GET /open-api/v1/ai-context-settings

- 鉴权：`iauth.FA(iauth.FeatureAIContext, iauth.ActionRead)`；
- 设置行不存在时返回**默认值对象**（§2.2 各默认值，无时间戳字段），与 BFE 侧缺省行为一致；
- 响应 Data：设置对象。

### 3.4 PUT /open-api/v1/ai-context-settings

- 鉴权：`iauth.FA(iauth.FeatureAIContext, iauth.ActionUpdate)`；
- 请求体：设置对象（§2.2，全部字段可省略走默认）；
- **upsert 语义**：单事务内不存在则插入、存在则整行更新（单行表）；
- 校验失败整体 422，设置不变；
- 响应 Data：更新后的完整设置对象；
- 操作审计：一条 update 审计，`before` 为库中现状（空表时为默认值对象快照）。

## 4. Inner API 明细（导出）

### 4.1 GET /inner-api/v1/configs/ai-context-rule

- 鉴权：`iauth.FA(iauth.FeatureAIContext, iauth.ActionExport)`；
- 增量语义：走 `iversion_control.ExportConfig` 标准流程（MD5 签名 + `config_versions` 版本比对），topic `mod_ai_context`；
- 响应 JSON 结构（**tag 冻结**，与 BFE `context_rule_load.go` 逐字段核对）：

```json
{
  "Version": "20261001120000",
  "Defaults": {
    "triggerRatio": 0.7,
    "keepLatestImages": 2,
    "toolResultMaxChars": 2000,
    "thinkingPolicy": "trim-all-but-last",
    "charsPerToken": 4,
    "imageTokenEstimate": 1200,
    "rewrite": { "strength": "lite", "protectedSurvivalRate": 0.95 }
  },
  "Config": {
    "<product>": [
      {
        "cond": "default_t()",
        "mode": "balanced",
        "maxContextTokens": 0,
        "reserveTokens": 0
      }
    ]
  }
}
```

- `Defaults` **恒导出**（设置行不存在时导 BFE 同款默认值）；规则数组按 `id` 升序；空表导出空数组（BFE 侧全部跳过，fail-open）；
- 二期字段（`override`/`summary`）一期不出现在导出中；phase-2 引入后由生成器原样透传——BFE 对未知字段忽略并计数 `CTX_CFG_UNKNOWN_FIELD`（SC24 已验证），升级平滑；
- 产品名取自 `stateful.DefaultConfig.RunTime.AIRouteInnerProductName`（照 ai-cache 惯例）。
