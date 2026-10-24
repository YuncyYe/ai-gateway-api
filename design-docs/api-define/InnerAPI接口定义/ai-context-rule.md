# ai-context-rule 接口

## 1. 接口信息

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 导出 AI 上下文压缩规则配置 | 供 BFE `mod_ai_context` 模块执行上下文压缩与裁剪 |
| 端点 | `/configs/ai-context-rule` | - |
| Method | GET | - |
| 鉴权 | `FeatureAIContext + ActionExport` | - |
| 产物文件 | `context_rule.data` | 由 conf-agent 落盘并触发 BFE `/reload/mod_ai_context` |
| 配套静态配置 | `mod_ai_context.conf` | 规则文件路径/调试开关等静态配置不进本接口，经 conf-agent `CopyFiles` 下发 |

## 2. 请求参数

**Query 参数**

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| version | string | 否 | 上次返回的版本号，用于增量同步 | 可选；无强制格式/长度校验；为空或未传时按首次拉取处理 |

**请求示例**

```shell
curl -X GET "http://api-server:port/inner-api/v1/configs/ai-context-rule?version=00010101000000" \
  -H "Authorization:Token TOKEN_STRING"
```

## 3. 返回数据结构

### 3.1 顶层结构

与 BFE 动态配置文件 `context_rule.data` 格式保持一致（`Version`/`Defaults`/`Config` 首字母大写为 conf-agent 硬契约）：

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
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
            "AI_product": [/* 上下文压缩规则 */]
        }
    },
    "WorkMode": "ModeNormal"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| Version | string | 配置版本号（时间戳格式 `20060102150405`）；MD5 签名覆盖含 `Defaults` 的全量生成内容，修改设置即产生新版本 |
| Defaults | object | 全局调优参数块（**恒导出**，设置行不存在时用默认值 `0.7 / 2 / 2000 / trim-all-but-last / 4 / 1200 / lite / 0.95` 填充）；tag 为 BFE `DefaultsConfFile` 契约 |
| Config | object | 按产品线组织的压缩规则，key 为产品线名称（取自运行时配置 `AIRouteInnerProductName`，默认 `AI_product`）；空表时 product 键对应空数组 `[]` |

### 3.2 Defaults 结构（全局调优参数块）

字段 tag 为 BFE `DefaultsConfFile` 契约，与管理端点 `/ai-context-settings` 的小写下划线词汇不同：

| 字段 | 类型 | 说明 | 管理端点对应字段 | BFE 缺省填充（未导出时） |
|------|------|------|------------------|--------------------------|
| triggerRatio | float64 | proactive 触发阈值（占预算比例，(0,1]） | `trigger_ratio` | `0.7` |
| keepLatestImages | int | 保留最近 N 张内联图片，0=不裁图 | `keep_latest_images` | `2` |
| toolResultMaxChars | int | 单条 tool 结果截断长度，0=不截断 | `tool_result_max_chars` | `2000` |
| thinkingPolicy | string | thinking 块策略 | `thinking_policy` | `trim-all-but-last` |
| charsPerToken | int | 文本估算系数（字节/token） | `chars_per_token` | `4` |
| imageTokenEstimate | int | 单张内联图片估值 token | `image_token_estimate` | `1200` |
| rewrite.strength | string | 改写强度 lite/full | `rewrite.strength` | `lite` |
| rewrite.protectedSurvivalRate | float64 | fidelity gate 存活率阈值 (0,1] | `rewrite.protected_survival_rate` | `0.95` |

### 3.3 Config 结构（压缩规则数组）

规则按 first-match-wins 匹配，**数组顺序即优先级**（= 控制面 PUT `/open-api/v1/ai-context-rules` 提交的数组顺序）：

```json
{
    "Config": {
        "AI_product": [
            {
                "cond": "req_path_in(\"/v1/chat/completions\", false)",
                "mode": "balanced",
                "maxContextTokens": 64000,
                "reserveTokens": 8192
            }
        ]
    }
}
```

**字段说明**（tag 为 BFE `ContextRuleConfFile` 契约，与 Open API 的小写下划线词汇不同）：

| 字段 | 类型 | 一期是否导出 | 说明 | BFE 缺省填充（未导出时） |
|------|------|--------------|------|--------------------------|
| cond | string | 是（必含） | BFE 条件表达式，命中即按 mode 档位执行压缩 | 无默认，缺失则 BFE 加载失败 |
| mode | string | 是（必含） | 压缩档位 off/conservative/balanced/aggressive | 无默认，缺失或非法则 BFE 整文件拒载 |
| maxContextTokens | int | 是 | 预算上限覆盖（token），0 = 用模型表窗口 | `0` |
| reserveTokens | int | 是 | 预留输出 token，0 = 自动 clamp(窗口×15%, 256, 16000) | `0` |

**说明**：

- 控制面每条规则恒导出 `cond` / `mode` / `maxContextTokens` / `reserveTokens` 四字段；
- 二期字段（规则级 `override`、Defaults 内 `summary`）一期不出现在导出中；phase-2 引入后由生成器原样透传，当前 BFE 对未知字段忽略并计数告警（`CTX_CFG_UNKNOWN_FIELD`），升级平滑；
- 规则集合 = 控制面规则表全量（`id` 升序，无 enabled 过滤）；空表时 product 键对应空数组 `[]`，`Defaults` 块**仍导出**（设置独立于规则生命周期）；
- 压缩生效时 BFE 在转发上游前改写请求 `messages`，访问日志记录 `ai_context_compress_status`（trim / rewrite / skip_* / repair_rollback）与 `ai_context_tokens_before`/`ai_context_tokens_after`/`ai_context_compress_mode`（793-796，bfe-access-pb v0.3.11）；
- BFE 侧校验（最后防线）：`cond` 必须 `condition.Build` 编译通过；`mode` 缺失或非法整文件拒载；数值字段范围同 §3.2/§3.3；同一 product 内 `cond` 不可重复。

## 4. 成功返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
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
            "AI_product": [
                {
                    "cond": "req_path_in(\"/v1/chat/completions\", false)",
                    "mode": "balanced",
                    "maxContextTokens": 64000,
                    "reserveTokens": 8192
                },
                {
                    "cond": "default_t()",
                    "mode": "off",
                    "maxContextTokens": 0,
                    "reserveTokens": 0
                }
            ]
        }
    },
    "WorkMode": "ModeNormal"
}
```

## 5. 配置未变化返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": null,
    "WorkMode": "ModeNormal"
}
```

---
