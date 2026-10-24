# ai-cache-rule 接口

## 1. 接口信息

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 导出 AI 缓存规则配置 | 供 BFE `mod_ai_cache` 模块执行缓存命中/回写 |
| 端点 | `/configs/ai-cache-rule` | - |
| Method | GET | - |
| 鉴权 | `FeatureAICache + ActionExport` | - |
| 产物文件 | `ai_cache.data` | 由 conf-agent 落盘并触发 BFE `/reload/mod_ai_cache` |
| 配套静态配置 | `mod_ai_cache.conf` | Redis 连接等静态配置不进本接口，经 conf-agent `CopyFiles` 下发 |

## 2. 请求参数

**Query 参数**

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| version | string | 否 | 上次返回的版本号，用于增量同步 | 可选；无强制格式/长度校验；为空或未传时按首次拉取处理 |

**请求示例**

```shell
curl -X GET "http://api-server:port/inner-api/v1/configs/ai-cache-rule?version=00010101000000" \
  -H "Authorization:Token TOKEN_STRING"
```

## 3. 返回数据结构

### 3.1 顶层结构

与 BFE 动态配置文件 `ai_cache.data` 格式保持一致：

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Config": {
            "AI_product": [/* 缓存规则 */]
        },
        "Semantic": {
            "topK": 1,
            "threshold": 0.15,
            "thresholdRelation": "lt"
        },
        "Version": "00010101000000"
    },
    "WorkMode": "ModeNormal"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| Config | object | 按产品线组织的缓存规则，key 为产品线名称（取自运行时配置 `AIRouteInnerProductName`，默认 `AI_product`） |
| Semantic | object | 语义缓存全局调优参数（二期新增，**恒导出**）；值为控制面设置（缺省 `1 / 0.15 / lt`）；旧版 BFE 忽略未知顶层字段，向后兼容 |
| Version | string | 配置版本号（时间戳格式 `20060102150405`）；MD5 签名覆盖含 `Semantic` 的全量生成内容，修改设置即产生新版本 |

### 3.2 Config 结构（缓存规则数组）

规则按 first-match-wins 匹配，**数组顺序即优先级**（= 控制面 PUT `/open-api/v1/ai-cache-rules` 提交的数组顺序）：

```json
{
    "Config": {
        "AI_product": [
            {
                "cond": "req_path_in(\"/v1/chat/completions\", false) && req_body_json_in(\"model\", \"deepseek-chat\", false)",
                "cacheKeyStrategy": "lastQuestion",
                "cacheTTL": 3600,
                "maxBodyBytes": 1048576,
                "maxValueBytes": 1048576
            }
        ]
    }
}
```

**字段说明**（tag 为 BFE `mod_ai_cache` 规则加载器契约，与 Open API 的小写下划线词汇不同）：

| 字段 | 类型 | 一期是否导出 | 说明 | BFE 缺省填充（未导出时） |
|------|------|--------------|------|--------------------------|
| cond | string | 是（必含） | BFE 条件表达式，命中即启用缓存 | 无默认，缺失则 BFE 加载失败 |
| cacheKeyStrategy | string | 是 | 缓存键策略；`disabled` 表示命中即不缓存（缓存豁免，阻断后续规则匹配） | `lastQuestion` |
| cacheTTL | int | 是 | 缓存 TTL（秒），`0` 表示不过期 | 模块 `Basic.DefaultCacheTTL`（默认 3600，conf 可配） |
| cacheKeyFrom | string | 否（二期） | 缓存键提取 GJSON 路径 | `""`（内置默认 `messages.@reverse.0.content`） |
| cacheValueFrom | string | 否（二期） | 非流式答案提取 GJSON 路径 | `choices.0.message.content` |
| cacheStreamValueFrom | string | 否（二期） | 流式答案提取 GJSON 路径 | `choices.0.delta.content` |
| cacheToolCallsFrom | string | 否（预留） | tool-calls 提取路径（预留，不开放） | `""` |
| responseTemplate | string | 否（二期） | 命中非流式响应模板（须含 `%s` 占位） | 内置非流式模板 |
| streamResponseTemplate | string | 否（二期） | 命中流式（SSE）响应模板（须含 `%s` 占位） | 内置 SSE 模板 |
| maxBodyBytes | int64 | 是 | 请求体大小上限（字节），超限不缓存 | 1048576（1MB） |
| maxValueBytes | int64 | 是 | 缓存值大小上限（字节），超限不缓存 | 1048576（1MB） |
| enableSemanticCache | bool | 是（二期起） | 语义缓存开关（规则级，按路由灰度用）；`cacheKeyStrategy=disabled` 时 BFE 忽略 | `false` |

**顶层 `Semantic` 块**（二期新增，恒导出；字段 tag 为 BFE `SemanticConfFile` 契约，与管理端点小写下划线词汇不同）：

| 字段 | 类型 | 说明 | 管理端点对应字段 |
|------|------|------|------------------|
| topK | int | 向量检索近邻个数（1-10） | `top_k` |
| threshold | float64 | 相似度阈值（0-2，量纲随 relation） | `threshold` |
| thresholdRelation | string | 阈值比较：`lt`/`lte`/`gt`/`gte` | `threshold_relation` |

**说明**：

- 一期控制面每条规则只导出 `cond` / `cacheKeyStrategy` / `cacheTTL` / `maxBodyBytes` / `maxValueBytes`，其余字段由 BFE 按上表缺省值填充；
- 二期起每条规则额外恒导出 `enableSemanticCache`（默认 `false`），顶层恒导出 `Semantic` 块（设置行不存在时用默认值 `1 / 0.15 / lt`）；
- 规则集合 = 控制面规则表全量（`id` 升序，无 enabled 过滤）；空表时 product 键对应空数组 `[]`，`Semantic` 块**仍导出**（设置独立于规则生命周期）；
- 命中后的响应由 BFE `mod_ai_cache` 直接构造（非流式 JSON / 流式 SSE），不转发到后端；访问日志记录 `ai_cache_status`（hit / **hit_semantic** / miss / skip）、语义命中时的 `ai_cache_semantic` / `ai_cache_similarity`（791/792，bfe-access-pb v0.3.10）与 `ai_cache_key`；
- BFE 侧校验（最后防线）：`cond` 必须 `condition.Build` 编译通过；`cacheTTL >= 0`；`maxBodyBytes`/`maxValueBytes > 0`；同一 product 内 `cond` 不可重复；`topK` 1-10、`threshold` 0-2、`thresholdRelation` 四枚举（Semantic 块存在时）；控制面 `PUT /open-api/v1/ai-cache-rules` 已在写入前拒绝集合内重复 `cond`，本接口不再产生该类非法文件（issue #261）

## 4. 成功返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Config": {
            "AI_product": [
                {
                    "cond": "req_path_in(\"/v1/chat/completions\", false) && req_body_json_in(\"model\", \"deepseek-chat\", false)",
                    "cacheKeyStrategy": "lastQuestion",
                    "cacheTTL": 3600,
                    "enableSemanticCache": true,
                    "maxBodyBytes": 1048576,
                    "maxValueBytes": 1048576
                },
                {
                    "cond": "req_path_in(\"/v1/chat/completions\", false)",
                    "cacheKeyStrategy": "lastQuestion",
                    "cacheTTL": 86400,
                    "enableSemanticCache": false,
                    "maxBodyBytes": 1048576,
                    "maxValueBytes": 1048576
                }
            ]
        },
        "Semantic": {
            "topK": 1,
            "threshold": 0.15,
            "thresholdRelation": "lt"
        },
        "Version": "20260924103000"
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
