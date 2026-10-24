# mod-ai-intent 接口

## 1. 接口信息

| 项目 | 值 | 说明 |
|------|------|------|
| 含义 | 导出 AI 意图配置 | 供 BFE `mod_ai_intent` 模块执行意图分类（问题集 + 置信度门控阈值） |
| 端点 | `/configs/mod-ai-intent` | - |
| Method | GET | - |
| 鉴权 | `FeatureAIIntent + ActionExport` | - |
| 产物文件 | `intent_questions.data` | 由 conf-agent 落盘到 `conf/mod_ai_intent/intent_questions.data` 并触发 BFE `/reload/mod_ai_intent` |
| 配套静态配置 | `mod_ai_intent.conf` | 决策服务地址/超时/缓存/熔断等静态配置不进本接口，经 conf-agent `CopyFiles` 下发 |

## 2. 请求参数

**Query 参数**

| 参数名 | 类型 | 必填 | 说明 | 合法性条件 |
|--------|------|------|------|------------|
| version | string | 否 | 上次返回的版本号，用于增量同步 | 可选；无强制格式/长度校验；为空或未传时按首次拉取处理 |

**请求示例**

```shell
curl -X GET "http://api-server:port/inner-api/v1/configs/mod-ai-intent?version=00010101000000" \
  -H "Authorization:Token TOKEN_STRING"
```

## 3. 返回数据结构

### 3.1 顶层结构

与 BFE 动态配置文件 `intent_questions.data` 格式保持一致（Version 内嵌文件，
同 `ai-route` 端点形态；`Data` 即文件内容原样）：

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Version": "20260926120000",
        "MinConfidence": 0.6,
        "Questions": [/* 问题数组，见 3.2 */]
    },
    "WorkMode": "ModeNormal"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| Version | string | 配置版本号（时间戳格式 `20060102150405`），由版本控制机制生成；内容任何变更必须更新 |
| MinConfidence | number | 全局置信度门控阈值，低于该置信度的答案视为 unknown（路由意图条件不命中） |
| Questions | array | 问题数组，一次分类调用并行评估全部问题；**空数组 = 停用意图分类（软开关）**，见 3.2 |

**说明**：

- 控制面未发布意图配置时返回 `Data: null`（与"配置未变化"同形态），conf-agent
  不落盘、不触发 reload；
- 路由规则的意图条件**不随本接口下发**：规则 `cond` 中直传书写
  `req_ai_intent_in(...)`（导出链路见 `/configs/ai-route`）；本接口只下发
  问题集与阈值；
- 控制面不做 cond 对问题名/选项的引用完整性校验：引用未配置问题名的条件在
  BFE 运行时**永不命中**（fail-safe），规则 fall through 到默认规则；
- 门控语义：答案置信度低于生效阈值 → 该问题读取时视为 unknown（读取时计算，
  阈值热更即时生效）。

### 3.2 Questions 结构（问题数组）

```json
{
    "Questions": [
        {
            "Name": "task_type",
            "Type": "choice",
            "Instructions": "这条请求属于哪类研发任务？",
            "Criteria": {
                "coding": "编写或修改代码、调试、重构、代码审查",
                "test_writing": "编写测试用例、单元测试、集成测试、补充断言",
                "doc_writing": "编写文档、README、注释、接口说明、使用示例"
            }
        },
        {
            "Name": "complexity",
            "Type": "score",
            "Instructions": "这个任务的复杂度如何？",
            "MinConfidence": 0.7,
            "Levels": [
                {"Name": "simple",  "Description": "单步即可完成"},
                {"Name": "medium",  "Description": "多步但模式常见"},
                {"Name": "complex", "Description": "需要深入推理或跨模块设计"}
            ]
        }
    ]
}
```

**字段说明**（tag 为 BFE `mod_ai_intent` 数据文件契约，与 Open API
`/intent-config` 的小写下划线词汇不同；数量上限为控制面口径，BFE 协议上限 255）：

| 字段 | 类型 | 说明 | BFE 侧校验（最后防线） |
|------|------|------|--------------------------|
| Questions[].Name | string | 问题名，路由 cond `req_ai_intent_in("<Name>", ...)` 按它引用 | 非空、唯一 |
| Questions[].Type | string | `choice` 多选一 / `score` 刻度打分 | 枚举校验 |
| Questions[].Instructions | string | 判定指令，发送给决策模型 | 非空 |
| Questions[].Criteria | map<string,string> | 选项集合（Type=`choice` 时必填，与 Levels 互斥）：选项名 → 描述 | 1–255 项（控制面收紧 1–10）；选项名唯一、不含 `\|` |
| Questions[].Levels | array | 档位集合（Type=`score` 时必填，从低到高，与 Criteria 互斥） | 1–255 档（控制面收紧 1–10）；Name 唯一 |
| Questions[].MinConfidence | number | 逐问题门控阈值，覆盖全局 MinConfidence | [0,1]；缺省用全局 |

**说明**：

- 问题数组顺序无优先级语义（一次调用并行评估全部问题），导出保持控制面提交
  顺序；
- 空数组 `[]` 表示**停用意图分类**：BFE 侧所有意图条件不命中，流量走默认路由；
  恢复 = 发布非空配置，路由规则无需改动（软开关）；
- `Type=score` 时 BFE 将决策服务返回的期望档位索引映射回档位名后供路由条件
  匹配（路由规则侧始终面对离散名）。

## 4. 成功返回示例

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": {
        "Version": "20260926120000",
        "MinConfidence": 0.6,
        "Questions": [
            {
                "Name": "task_type",
                "Type": "choice",
                "Instructions": "这条请求属于哪类研发任务？",
                "Criteria": {
                    "coding": "编写或修改代码、调试、重构、代码审查",
                    "test_writing": "编写测试用例、单元测试、集成测试、补充断言",
                    "doc_writing": "编写文档、README、注释、接口说明、使用示例"
                }
            },
            {
                "Name": "complexity",
                "Type": "score",
                "Instructions": "这个任务的复杂度如何？",
                "MinConfidence": 0.7,
                "Levels": [
                    {"Name": "simple",  "Description": "单步即可完成"},
                    {"Name": "medium",  "Description": "多步但模式常见"},
                    {"Name": "complex", "Description": "需要深入推理或跨模块设计"}
                ]
            }
        ]
    },
    "WorkMode": "ModeNormal"
}
```

## 5. 配置未变化返回示例

请求携带的 `version` 与当前一致（或控制面未发布配置）时：

```json
{
    "ErrNum": 200,
    "ErrMsg": "success",
    "Data": null,
    "WorkMode": "ModeNormal"
}
```

此时 conf-agent 不落盘、不触发 BFE `/reload/mod_ai_intent`。
