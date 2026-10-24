# traffic-mirror-rule 接口

## 1. 接口信息

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 导出流量镜像规则配置 | 供 BFE `mod_traffic_mirror` 模块执行镜像复制（shadow traffic） |
| 端点 | `/configs/traffic-mirror-rule` | - |
| Method | GET | - |
| 鉴权 | `FeatureTrafficMirror + ActionExport` | - |
| 产物文件 | `mirror_rule.data` | 由 conf-agent 落盘并触发 BFE `/reload/mod_traffic_mirror` |
| 配套静态配置 | `mod_traffic_mirror.conf` | 分层超时/并发上限/熔断阈值等模块调参不进本接口，经 conf-agent `CopyFiles` 下发 |

## 2. 请求参数

**Query 参数**

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| version | string | 否 | 上次返回的版本号，用于增量同步 | 可选；无强制格式/长度校验；为空或未传时按首次拉取处理 |

**请求示例**

```shell
curl -X GET "http://api-server:port/inner-api/v1/configs/traffic-mirror-rule?version=00010101000000" \
  -H "Authorization:Token TOKEN_STRING"
```

## 3. 返回数据结构

### 3.1 顶层结构

与 BFE 动态配置文件 `mirror_rule.data` 格式保持一致：

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Config": {
            "AI_product": [/* 镜像规则 */]
        },
        "Version": "00010101000000"
    },
    "WorkMode": "ModeNormal"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| Config | object | 按产品线组织的镜像规则，key 为产品线名称（取自运行时配置 `AIRouteInnerProductName`，默认 `AI_product`） |
| Version | string | 配置版本号（时间戳格式 `20060102150405`） |

### 3.2 Config 结构（镜像规则数组）

规则按 first-match-wins 匹配，**数组顺序即优先级**（= 控制面 PUT `/open-api/v1/traffic-mirror-rules` 提交的数组顺序）：

```json
{
    "Config": {
        "AI_product": [
            {
                "cond": "req_path_prefix_in(\"/v1/chat/completions\", true) && req_body_json_in(\"model\", \"gpt-4o\", false)",
                "mirrorCluster": "cluster_shadow_v2",
                "percentage": 10,
                "removeHeaders": ["Authorization", "Cookie", "X-Api-Key"],
                "setHeaders": {},
                "bodyRewrites": [{"path": "model", "value": "deepseek-v3"}],
                "pathRewrite": ""
            }
        ]
    }
}
```

**字段说明**（tag 为 BFE `mod_traffic_mirror` 规则加载器契约，与 Open API 的小写下划线词汇不同）：

| 字段 | 类型 | 一期是否导出 | 说明 | BFE 缺省填充（未导出时） |
|------|------|--------------|------|--------------------------|
| cond | string | 是（恒输出非空） | BFE 条件表达式，命中即启用镜像；控制面规则 `cond` 必填，全匹配显式写 `default_t()`，本接口不生成空 cond | `""`（仅直接手写 BFE conf 时生效） |
| mirrorCluster | string | 是（必含、显式非空） | 镜像目标 cluster 名 | 无默认，缺失则 BFE 加载失败 |
| percentage | int | 是 | 镜像采样百分比（0-100），`0` = 命中但不采样 | `100` |
| removeHeaders | array | 是 | 镜像副本剔除的 Header 黑名单。**注意：BFE 缺省为空列表（不剔除）；默认黑名单 `["Authorization","Cookie","X-Api-Key"]` 是控制面对"未提交 `remove_headers`"规则的导出填充行为，两侧语义不同** | `[]` |
| setHeaders | object | 是（空输出 `{}`） | 镜像副本注入的自定义 Header；`X-Bfe-Mirror` 由 BFE 缺失时兜底注入 `true` | `{}` |
| bodyRewrites | array | 是（空输出 `[]`） | body 字段改写，元素 `{"path","value"}`；**一期 `path` 仅允许 `"model"`** | `[]` |
| pathRewrite | string | 是（空输出 `""`） | 镜像路径整体替换（query 保留）；`""` = 不改写 | `""` |

**说明**：

- 规则集合 = 控制面规则表全量（`id` 升序，无 enabled 过滤）；空表时 product 键对应空数组 `[]`；
- 同一 product 内 `cond` 不可重复；控制面 PUT 时已做 fail-fast 校验（`cond` 必填 + 集合内唯一，全匹配显式 `default_t()`），BFE 加载为最后防线；
- 镜像请求为网关内部异步子请求（不触发配额扣减/限流，响应读空丢弃），镜像结果经模块 Prometheus 指标与访问日志 `mirror_hit`/`mirror_cluster` 字段输出；
- BFE 侧校验（最后防线）：`mirrorCluster` 非空；`percentage ∈ [0,100]`；`cond` 非空时必须 `condition.Build` 编译通过；`bodyRewrites[].path = "model"`。

## 4. 成功返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Config": {
            "AI_product": [
                {
                    "cond": "req_path_prefix_in(\"/v1/chat/completions\", true) && req_body_json_in(\"model\", \"gpt-4o\", false)",
                    "mirrorCluster": "cluster_shadow_v2",
                    "percentage": 10,
                    "removeHeaders": ["Authorization", "Cookie", "X-Api-Key"],
                    "setHeaders": {},
                    "bodyRewrites": [{"path": "model", "value": "deepseek-v3"}],
                    "pathRewrite": ""
                },
                {
                    "cond": "req_path_prefix_in(\"/v1/chat/completions\", true)",
                    "mirrorCluster": "cluster_fallback_drill",
                    "percentage": 1,
                    "removeHeaders": ["Authorization", "Cookie", "X-Api-Key", "X-Custom-Secret"],
                    "setHeaders": {},
                    "bodyRewrites": [],
                    "pathRewrite": ""
                }
            ]
        },
        "Version": "20260925103000"
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
