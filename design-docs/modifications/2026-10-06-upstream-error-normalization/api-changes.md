# 上游错误体归一（normalize_upstream_error）——接口变更

## 1. 变更范围

影响以下 OpenAPI 与 InnerAPI 接口：

- `POST /clusters`
- `PATCH /clusters/{name}`
- `GET /clusters`
- `GET /clusters/{name}`
- `GET /configs/tls_conf/server_data_conf`（InnerAPI，导出响应中 `AIConf` 新增字段）

向后兼容：以上接口对**不带** `normalize_upstream_error` 的请求/存量数据行为完全
不变（字段缺省 = BFE 关闭归一）。注意：`/clusters` 无 `PUT` 全量更新接口
（更新仅 `PATCH`，且 `llm_config` 提交即整体替换——省略 `normalize_upstream_error`
 等价于清空，与 `llm_config.keys` 的全量替换语义一致）。

## 2. OpenAPI `/clusters` 变更

### 2.1 请求/响应数据模型扩展

在 `llm_config` 中新增 `normalize_upstream_error` 字段：

```json
{
    "llm_config": {
        "models": ["deepseek-chat"],
        "keys": [
            {"name": "key-primary", "weight": 100}
        ],
        "provider": "deepseek",
        "normalize_upstream_error": {
            "enabled": true,
            "stream_enabled": true,
            "unrecognized_action": "passthrough",
            "max_body_bytes": 65536,
            "redact_secrets": true
        }
    }
}
```

### 2.2 新增字段说明

**表：`llm_config.normalize_upstream_error`**

| 参数名 | 类型 | 参数含义 | 必填 | 补充描述 | 合法性条件 |
| - | - | - | - | - | - |
| `enabled` | bool | 非流式上游错误归一开关 | N | `true` 时上游 4xx/5xx 错误重写为统一错误体（OpenAI 兼容），状态码按统一映射表重映射（如上游 401→502 `UPSTREAM_AUTH_ERROR`）；默认 `false`（透传，历史行为） | 非必填；必须为 bool |
| `stream_enabled` | bool | 流式（SSE）错误归一开关 | N | 独立于 `enabled` 灰度；`true` 时流内错误事件 data 载荷改写为统一错误 JSON（响应状态保持 200），并对流截断（EOF 缺失协议终止事件）打访问日志标记；默认 `false` | 非必填；必须为 bool |
| `unrecognized_action` | string | 未识别错误体的处理 | N | `passthrough`（默认：原样透传，仍做凭证脱敏）/ `rewrite_generic`（重写为 `UPSTREAM_UNKNOWN` 通用错误）；流式未识别事件始终透传 | 非必填；取值仅支持 `passthrough`、`rewrite_generic` |
| `max_body_bytes` | integer | 错误响应体读取上限（字节） | N | 超限按未识别处理；`0`/缺省用 BFE 默认 65536 | 非必填；∈ [0, 4194304] |
| `redact_secrets` | bool | 凭证脱敏开关 | N | 外发错误内容（客户端响应与访问日志，含归一重写与透传路径）中出现的本集群 API-Key 各编码形态（原文/base64/URL 编码/JSON 转义）替换为掩码 `••••••••`；默认 `true`，显式 `false` 关闭 | 非必填；必须为 bool |

### 2.3 行为说明

- 归一仅在转发重试（fallback）结束、最终结果确定后生效，**不影响**重试与 Key
  罚分决策；
- 网关自生成错误（认证/限流/配额等）已是统一格式，不参与归一；
- 上游原始状态码与原始错误码记录于响应 `error.details.upstream_status` /
  `upstream_code` 与访问日志字段（`ai_upstream_status` 等，bfe-access-pb 810-815）。

### 2.4 响应示例（`GET /clusters/{name}` 片段）

```json
{
    "name": "cluster-deepseek",
    "llm_config": {
        "provider": "deepseek",
        "normalize_upstream_error": {
            "enabled": true,
            "stream_enabled": false,
            "unrecognized_action": "passthrough",
            "redact_secrets": true
        }
    }
}
```

> 未配置的字段在响应中为 `null`（全指针结构、无 omitempty，与 `key_affinity` 序列化
> 行为一致）；整个 `normalize_upstream_error` 未配置时为 `null`。

## 3. InnerAPI 导出变更

`GET /configs/tls_conf/server_data_conf` 导出的 `cluster_conf.data` 中，对应
cluster 的 `AIConf` 新增可选字段（字段名 PascalCase，随 BFE 结构体）：

```json
"AIConf": {
    "Keys": [{"Name": "key-primary", "Key": "sk-...", "Weight": 100}],
    "NormalizeUpstreamError": {
        "Enabled": true,
        "StreamEnabled": false,
        "UnrecognizedAction": "passthrough",
        "MaxBodyBytes": 65536,
        "RedactSecrets": true
    }
}
```

- 控制面未配置该字段时，导出结果中 `AIConf.NormalizeUpstreamError` 为 `null`
  （结构体无 omitempty，与 `ModelMapping` 等指针字段序列化行为一致），BFE 按关闭处理；
- 旧版本 BFE 收到该字段时 JSON 反序列化忽略未知字段，行为不变；
- 配置文档同步见 `bfe/docs/zh_cn/configuration/server_data_conf/cluster_conf.data.md` §9.4。

## 4. 错误码

无新增错误码；参数校验失败沿用 `422` 参数错误（`unrecognized_action` 非法值、
`max_body_bytes` 越界）。
