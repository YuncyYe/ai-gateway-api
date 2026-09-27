# Report 体现 ai-cache / 流量镜像 / ai-intent 字段：API 变更说明

> 对应 `design-docs/api-define/OpenAPI接口定义/report.md` 的计划修改内容
> （实现阶段 Step 3 执行时按本文落地并核对行号）。全部为**增量变更**，无
> breaking change。

## 1. 变更总览

| 端点 | 变更 |
|------|------|
| GET /report/overview | 响应新增缓存（次数/命中率/cache token）、镜像（命中数）、意图（分类数/unknown 数/unknown 率）三组指标 |
| GET /report/timeseries | `metric` 新增 `cache_tokens`；新维度 `ai_intent_answer`/`ai_cache_status`/`mirror_hit`（仅 mysql 后端，doris 返回 422） |
| GET /report/rankings | `dimension` 新增同上 3 值（仅 mysql 后端） |
| GET /report/distribution | `dimension` 新增同上 3 值（仅 mysql 后端） |
| GET /report/logs | 明细行新增 10 列；查询参数新增 `cache_status`、`mirror_hit`、`intent_question`、`intent_answer`、`intent_source` |

## 2. 新增枚举值

### 2.1 dimension（rankings / distribution / timeseries 维度参数）

| 新值 | 聚合表列 | 可用后端（一期） |
|------|----------|-------------------|
| `ai_intent_answer` | `ai_intent_answer` | mysql；doris 422 |
| `ai_cache_status` | `ai_cache_status` | mysql；doris 422 |
| `mirror_hit` | `mirror_hit` | mysql；doris 422 |

doris 后端请求上述维度返回 422，错误信息：
`dimension <name> not supported by doris backend (mysql only until doris support lands)`。

### 2.2 metric（timeseries 指标参数）

| 新值 | 说明 |
|------|------|
| `cache_tokens` | 缓存 token 速率：按 `kind=cache_read/cache_write` 两条序列（序列级区分，与 cost 按 currency 多条序列同风格） |

## 3. overview 响应新增字段

```json
{
  "cache": {
    "hit_count": 120, "miss_count": 30, "skip_count": 5,
    "hit_rate": 0.8,
    "read_tokens": 123456, "write_tokens": 7890
  },
  "mirror": { "hit_count": 12 },
  "intent": { "classified_count": 145, "unknown_count": 15, "unknown_rate": 0.0938 }
}
```

口径说明（须写入 report.md）：

- `cache.hit_rate = hit/(hit+miss)`，skip 不计入分母；
- `cache.read_tokens/write_tokens` 为聚合表 `cache_read_tokens`/`cache_write_tokens`
  SUM 列消费（既有列，口径与 BFE 日志一致）；
- `intent.*` 统计的是**路由实际消费的意图**（BFE 一期只记录 `ConsumedQuestion`
  单条），非全部已配置问题的分类量；unknown = 消费时低于置信度门限的答案。

## 4. logs 明细行新增列

| 列 | 类型 | 说明 |
|----|------|------|
| `ai_cache_status` | string | hit/miss/skip，空=未启用缓存 |
| `mirror_hit` | bool | 是否被镜像（未镜像为 null/false，按既有可空列风格） |
| `mirror_cluster` | string | 镜像目标集群名 |
| `ai_intent_question` | string | 路由消费的问题名；意图未求值为 null |
| `ai_intent_answer` | string | 分类答案，含 `unknown` |
| `ai_intent_confidence` | number | 门控后置信度 |
| `ai_intent_source` | string | explicit_header / classifier / cache |
| `ai_intent_latency_us` | number | 决策服务耗时（μs）；cache/显式源为 null |
| `ai_intent_cache_hit` | bool | 意图 LRU 命中 |
| `ai_intent_questions_version` | string | questions 配置版本 |

## 5. logs 新增查询参数

| 参数 | 类型 | 说明 |
|------|------|------|
| `cache_status` | string | 精确匹配 `ai_cache_status` |
| `mirror_hit` | bool | 精确匹配 `mirror_hit` |
| `intent_question` | string | 精确匹配 |
| `intent_answer` | string | 精确匹配（可传 `unknown`） |
| `intent_source` | string | 枚举：explicit_header / classifier / cache |

参数风格与既有过滤参数（models/apikey_ids 等）一致；两后端均可用（基于明细列）。

## 6. 数据可得性说明（写入 report.md）

新字段自各链路（log-reader 扩列、报表库 DDL、聚合 JOB）上线后逐步积累，
此前历史数据对应字段为空/0；意图与缓存维度数据从聚合表维度上线后的分钟
窗口起可用。
