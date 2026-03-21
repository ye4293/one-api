---
title: EZLINK AI API 接口文档
language_tabs:
  - shell: Shell
  - http: HTTP
  - javascript: JavaScript
  - ruby: Ruby
  - python: Python
  - php: PHP
  - java: Java
  - go: Go
toc_footers: []
includes: []
search: true
code_clipboard: true
highlight_theme: darkula
headingLevel: 2
generator: "@tarslib/widdershins v4.0.30"

---

# EZLINK AI API 接口文档

Base URLs:

# Authentication

- HTTP Authentication, scheme: bearer

# 接口列表

## GET 查询令牌用量

GET /v1/dashboard/billing/usage

查询该令牌总使用量

### 请求参数

|名称|位置|类型|必选|说明|
|---|---|---|---|---|
|start_date|query|string| 否 |时间戳（ms）开始时间|
|end_date|query|string| 否 |时间戳（ms）截止时间|

> 返回示例

> 200 Response

```json
{
  "object": "list",
  "total_usage": 695.9646
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» object|string|true|none||none|
|» total_usage|number|true|none|令牌用量|total_usage /100  = 实际用量|

## GET 查询令牌限额

GET /v1/dashboard/billing/subscription

通过该接口查询令牌key的授权额度（限额），如果令牌设置为 `无限额度` 则查询结果是100000000

注意：令牌的授权额度一旦修改之后，授权额度（限制额度）将会在上次额度之上进行不断累加。

> 返回示例

> 200 Response

```json
{
  "object": "billing_subscription",
  "has_payment_method": true,
  "soft_limit_usd": 5,
  "hard_limit_usd": 5,
  "system_hard_limit_usd": 5,
  "access_until": 0
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» object|string|true|none||none|
|» has_payment_method|boolean|true|none||none|
|» soft_limit_usd|integer|true|none||none|
|» hard_limit_usd|integer|true|none||none|
|» system_hard_limit_usd|integer|true|none||none|
|» access_until|integer|true|none||none|

## GET 查询账户信息

GET /api/user/self

通过该接口查询账户的具体信息。
包括：剩余额度、已用额度、总请求次数等等

### 请求参数

|名称|位置|类型|必选|说明|
|---|---|---|---|---|
|X-Api-User|header|integer| 否 |用户数字ID编号，可在网站个人中心查看|
|Authorization|header|string| 否 |系统访问令牌，可在网站个人中心获取|

> 返回示例

> 200 Response

```json
{
  "data": {
    "id": 1,
    "username": "xx@qq.com",
    "password": "",
    "display_name": "xx",
    "role": 10,
    "status": 1,
    "email": "xx@qq.com",
    "github_id": "xxxooo",
    "wechat_id": "",
    "oidc_id": "",
    "google_id": "10283048099857931806",
    "verification_code": "",
    "access_token": null,
    "quota": 1867213653,
    "used_quota": 597081489,
    "request_count": 3427970,
    "group": "default",
    "can_use_self_group": false,
    "aff_code": "xXxx",
    "inviter_id": 0,
    "aff_count": 2,
    "aff_quota": 0,
    "aff_history_quota": 500000,
    "created_at": 0,
    "last_login_at": 1746636191,
    "last_login_ip": "138.116.11.42",
    "deleted_at": null,
    "group_ratio": 1,
    "topup_ratio": 1
  },
  "message": "",
  "success": true
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» data|object|true|none||none|
|»» id|integer|true|none||none|
|»» username|string|true|none||none|
|»» password|string|true|none||none|
|»» display_name|string|true|none||none|
|»» role|integer|true|none||none|
|»» status|integer|true|none||none|
|»» email|string|true|none||none|
|»» github_id|string|true|none||none|
|»» wechat_id|string|true|none||none|
|»» oidc_id|string|true|none||none|
|»» google_id|string|true|none||none|
|»» verification_code|string|true|none||none|
|»» access_token|null|true|none||none|
|»» quota|integer|true|none|剩余额度|该额度除以 500000 既为实际额度|
|»» used_quota|integer|true|none|已用额度|该额度除以 500000 既为实际额度|
|»» request_count|integer|true|none|总请求次数|none|
|»» group|string|true|none|用户分组|none|
|»» can_use_self_group|boolean|true|none||none|
|»» aff_code|string|true|none|Aff邀请ID|none|
|»» inviter_id|integer|true|none||none|
|»» aff_count|integer|true|none|邀请次数|none|
|»» aff_quota|integer|true|none|邀请奖励额度|none|
|»» aff_history_quota|integer|true|none|历史邀请奖励额度|none|
|»» created_at|integer|true|none||none|
|»» last_login_at|integer|true|none||none|
|»» last_login_ip|string|true|none||none|
|»» deleted_at|null|true|none||none|
|»» group_ratio|integer|true|none||none|
|»» topup_ratio|integer|true|none||none|
|» message|string|true|none||none|
|» success|boolean|true|none||none|

# 基础接口

## POST Claude (原生格式)-可PDF分析

POST /v1/messages

:::tip
Claude模型同时支持OpenAI请求格式与官方请求格式。
官方请求格式只列出常用参数，更详细的请求参数请阅读 [claude官方文档](https://docs.anthropic.com/zh-CN/api/getting-started)

走OpenAI格式 将不会计算缓存，走官方格式会完全走缓存计费方式。  [官方缓存使用与说明](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
:::

支持Claude原生请求的模型：
- cld-、claude-开头的所有模型
- kimi-k2-0711-preview、kimi-k2-0905-preview
- qwen3-coder-plus
- glm-4.5、glm-4.6

> Body 请求参数

```json
{
  "model": "claude-3-5-sonnet-20240620",
  "messages": [
    {
      "role": "user",
      "content": "你好，你是？"
    }
  ],
  "max_tokens": 1688,
  "temperature": 0.5,
  "stream": false
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|Content-Type|header|string| 是 ||none|
|body|body|object| 否 ||none|
|» model|body|string| 是 | 模型名称|none|
|» messages|body|[object]| 是 ||none|
|»» role|body|string| 是 | 角色 user|none|
|»» content|body|[anyOf]| 是 | 展开查看所有组合|如果只有文字内容，可直接传string内容。|
|»»» *anonymous*|body|object| 否 | 文本消息|none|
|»»»» type|body|string| 是 | 文本类型|none|
|»»»» text|body|string| 是 | 提示词|none|
|»»» *anonymous*|body|object| 否 | 图片分析/PDF分析（base64格式）|none|
|»»»» type|body|string| 是 | 类型|图片分析或PDF分析|
|»»»» source|body|object| 是 | 文件资源|none|
|»»»»» type|body|string| 是 | 类型|请填写 `base64`|
|»»»»» data|body|string| 否 | base64|none|
|»»»»» media_type|body|string| 否 | 文件类型|例如：`image/png` `application/pdf`|
|»»»» cache_control|body|object| 否 | 缓存配置|如果上下文都需要用到PDF文件，可设置该参数 将pdf进行缓存。后续将会节省更多tokens。|
|»»»»» type|body|string| 否 ||值为`ephemeral `|
|»»» *anonymous*|body|object| 否 | 图片分析/PDF分析（url格式）|none|
|»»»» type|body|string| 是 | 类型|图片分析或PDF分析|
|»»»» source|body|object| 是 | 文件资源|none|
|»»»»» type|body|string| 是 | 类型|请填写 `url`|
|»»»»» url|body|string| 否 | 网络url|更推荐使用base64方式，响应更快，url方式需下载图片再转base64，效率更慢。|
|»»»» cache_control|body|object| 否 | 缓存配置|如果上下文都需要用到PDF文件，可设置该参数 将pdf进行缓存。后续将会节省更多tokens。|
|»»»»» type|body|string| 否 ||值为`ephemeral `|
|» temperature|body|number| 否 | 温度|使用什么采样温度，介于 0 和 2 之间。较高的值（如 0.8）将使输出更加随机，而较低的值（如 0.2）将使输出更加集中和确定。 我们通常建议改变这个或`top_p`但不是两者同时使用。|
|» top_p|body|number| 否 ||一种替代温度采样的方法，称为核采样，其中模型考虑具有 top_p 概率质量的标记的结果。所以 0.1 意味着只考虑构成前 10% 概率质量的标记。 我们通常建议改变这个或`temperature`但不是两者同时使用。|
|» max_tokens|body|number| 否 | 最大回复|聊天完成时生成的最大Tokens数量。 输入标记和生成标记的总长度受模型上下文长度的限制。|
|» stream|body|boolean| 否 | 流式输出|流式输出或非流式输出|
|» top_k|body|integer| 否 ||none|
|» tools|body|object| 否 | 函数调用|tools函数详细参数与用法，请阅读 [官方文档](https://docs.anthropic.com/en/docs/build-with-claude/tool-use)|
|» thinking|body|object| 否 | 开启思考|该参数仅支持模型`claude-3-7-sonnet-20250219`|
|»» type|body|string| 是 | 类型|值必须为 "enabled"|
|»» budget_tokens|body|integer| 是 | 最大思考|最大思考tokens，必须大于1024，同时 `max_tokens`参数必须大于此处的值。|

#### 详细说明

**» thinking**: 该参数仅支持模型`claude-3-7-sonnet-20250219`
设置后ai回复将会先思考 再回复。

#### 枚举值

|属性|值|
|---|---|
|»»»» type|image|
|»»»» type|document|
|»»»» type|image|
|»»»» type|document|

> 返回示例

> 200 Response

```json
{
  "id": "msg_014AoBefsejHUjbdRntn7euw",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "你好!很高兴见到你。今天过得怎么样?有什么我可以帮助你的吗?"
    }
  ],
  "model": "claude-3-5-sonnet-20240620",
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 12,
    "output_tokens": 38
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» id|string|true|none||none|
|» type|string|true|none||none|
|» role|string|true|none||none|
|» content|[object]|true|none||none|
|»» type|string|false|none||none|
|»» text|string|false|none||none|
|» model|string|true|none||none|
|» stop_reason|string|true|none||none|
|» stop_sequence|null|true|none||none|
|» usage|object|true|none||none|
|»» input_tokens|integer|true|none||none|
|»» output_tokens|integer|true|none||none|

## POST Gemini generateContent（原生格式）

POST /v1beta/models/{model}:generateContent

:::tip
Gemini模型同时支持OpenAI请求格式与官方原生请求格式。
官方请求格式只列出常用参数，更详细的请求参数请阅读 [Gemini官方文档](https://ai.google.dev/gemini-api/docs/text-generation?hl=zh-cn)

走OpenAI格式调用Gemini模型，请使用 `/v1/chat/completions` 接口。走原生格式则使用本接口。
:::

支持Gemini原生请求的模型：
- gemini-2.0-flash
- gemini-2.5-flash-preview-05-20
- gemini-2.5-pro-preview-05-06
- gemini-3-flash-preview
- gemini-3-pro-preview
- gemini-3.1-pro-preview

> Body 请求参数

```json
{
  "contents": [
    {
      "role": "user",
      "parts": [
        {
          "text": "你好，你是谁？"
        }
      ]
    }
  ],
  "generationConfig": {
    "temperature": 1.0,
    "maxOutputTokens": 2048
  }
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|Content-Type|header|string| 是 ||application/json|
|model|path|string| 是 | 模型名称|路径中的模型名称，例如 `gemini-2.5-flash-preview-05-20`|
|body|body|object| 是 ||none|
|» contents|body|[object]| 是 | 对话内容|当前对话内容，支持多轮对话|
|»» role|body|string| 是 | 角色|`user` 或 `model`|
|»» parts|body|[object]| 是 | 内容部分|消息的内容部分|
|»»» text|body|string| 否 | 文本|文本内容|
|»»» inlineData|body|object| 否 | 内联数据|用于传递图片/音频等多模态数据|
|»»»» mimeType|body|string| 是 | MIME类型|例如 `image/jpeg`、`image/png`、`application/pdf`|
|»»»» data|body|string| 是 | Base64数据|文件的base64编码数据|
|» generationConfig|body|object| 否 | 生成配置|模型生成参数配置|
|»» temperature|body|number| 否 | 温度|采样温度，建议使用默认值 `1.0`|
|»» topP|body|number| 否 ||核采样参数|
|»» topK|body|integer| 否 ||Top-K采样参数|
|»» maxOutputTokens|body|integer| 否 | 最大输出tokens|生成内容的最大token数|
|»» responseMimeType|body|string| 否 | 响应类型|指定输出格式，例如 `application/json` 可输出JSON结构化内容|
|»» responseJsonSchema|body|object| 否 | JSON Schema|配合 `responseMimeType` 为 `application/json` 使用，定义输出的JSON结构|
|»» thinkingConfig|body|object| 否 | 思考配置|控制模型的推理深度（仅Gemini 3系列支持）|
|»»» thinkingLevel|body|string| 否 | 思考等级|可选值：`minimal`、`low`、`medium`、`high`（默认）|
|» systemInstruction|body|object| 否 | 系统指令|开发者设置的系统提示词|
|»» parts|body|[object]| 是 ||none|
|»»» text|body|string| 是 | 系统提示|系统指令文本内容|
|» safetySettings|body|[object]| 否 | 安全设置|内容安全过滤配置|
|»» category|body|string| 是 | 类别|安全类别，例如 `HARM_CATEGORY_SEXUALLY_EXPLICIT`|
|»» threshold|body|string| 是 | 阈值|过滤阈值，例如 `BLOCK_NONE`|
|» tools|body|[object]| 否 | 工具|工具配置，支持函数调用、代码执行等|

> 返回示例

> 200 Response

```json
{
  "candidates": [
    {
      "content": {
        "parts": [
          {
            "text": "你好！我是 Gemini，由 Google 开发的大型语言模型。"
          }
        ],
        "role": "model"
      },
      "finishReason": "STOP",
      "safetyRatings": [
        {
          "category": "HARM_CATEGORY_SEXUALLY_EXPLICIT",
          "probability": "NEGLIGIBLE"
        }
      ]
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 5,
    "candidatesTokenCount": 20,
    "totalTokenCount": 25
  },
  "modelVersion": "gemini-2.5-flash-preview-05-20"
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» candidates|[object]|true|none|候选回复|模型生成的候选回复列表|
|»» content|object|true|none|内容|回复内容|
|»»» parts|[object]|true|none|内容部分|none|
|»»»» text|string|false|none|文本|模型生成的文本|
|»»» role|string|true|none|角色|始终为 `model`|
|»» finishReason|string|true|none|结束原因|生成结束的原因，例如 `STOP`、`MAX_TOKENS`、`SAFETY`|
|»» safetyRatings|[object]|false|none|安全评级|内容安全评级|
|» usageMetadata|object|true|none|用量信息|Token用量统计|
|»» promptTokenCount|integer|true|none|输入tokens|输入的token数量|
|»» candidatesTokenCount|integer|true|none|输出tokens|输出的token数量|
|»» totalTokenCount|integer|true|none|总tokens|总token数量|
|» modelVersion|string|false|none|模型版本|使用的模型版本|

## POST Gemini streamGenerateContent（原生流式）

POST /v1beta/models/{model}:streamGenerateContent?alt=sse

:::tip
Gemini原生格式的流式接口，请求体与 `generateContent` 完全一致，区别在于：
1. 路径使用 `:streamGenerateContent` 替代 `:generateContent`
2. 需添加查询参数 `alt=sse` 以获得SSE格式的流式响应
:::

> Body 请求参数

```json
{
  "contents": [
    {
      "role": "user",
      "parts": [
        {
          "text": "写一首关于春天的诗"
        }
      ]
    }
  ],
  "generationConfig": {
    "temperature": 1.0,
    "maxOutputTokens": 2048
  }
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|Content-Type|header|string| 是 ||application/json|
|model|path|string| 是 | 模型名称|路径中的模型名称，例如 `gemini-2.5-flash-preview-05-20`|
|alt|query|string| 是 | 响应格式|必须为 `sse`，表示使用 Server-Sent Events 流式传输|
|body|body|object| 是 ||请求体与 `generateContent` 接口完全一致，参数详见上方|

### 请求参数说明

请求体参数与 `generateContent` 完全一致，请参考上方参数表。

> 返回示例

> 200 Response（SSE流式）

每个SSE事件返回一个 `GenerateContentResponse` 对象：

```
data: {"candidates":[{"content":{"parts":[{"text":"春"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}}

data: {"candidates":[{"content":{"parts":[{"text":"风拂面"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}

data: {"candidates":[{"content":{"parts":[{"text":"花开满园"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":15,"totalTokenCount":20}}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|SSE流式返回，每个事件包含一个GenerateContentResponse对象|Inline|

### 返回数据结构

与 `generateContent` 接口返回结构一致，每个SSE事件(`data:`)中包含一个完整的 `GenerateContentResponse` 对象。最后一个事件中的 `finishReason` 字段标识生成结束。

## POST responses接口(原生)

POST /v1/responses

官方接口文档：https://platform.openai.com/docs/api-reference/responses/create

> Body 请求参数

```json
{
  "model": "gpt-4.1",
  "input": "1+2 等于多少.",
  "stream": false
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## POST 创建嵌入

POST /v1/embeddings

> Body 请求参数

```json
{
  "model": "text-embedding-3-large",
  "input": "她长得非常漂亮，喜欢..."
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 | 模型名称|要使用的模型的 ID。您可以使用[模型列表](https://platform.openai.com/docs/api-reference/models/list)|
|» input|body|string| 是 | 输入文本|输入文本以获取嵌入，编码为字符串或标记数组。要在单个请求中获取多个输入的嵌入，请传递一个字符串数组或令牌数组数组。每个输入的长度不得超过 8192 个标记。|

#### 详细说明

**» model**: 要使用的模型的 ID。您可以使用[模型列表](https://platform.openai.com/docs/api-reference/models/list)
API 来查看所有可用模型，或查看我们的[模型概述](https://platform.openai.com/docs/models/overview)以了解它们的描述。

> 返回示例

> 200 Response

```json
{
  "object": "list",
  "data": [
    {
      "object": "embedding",
      "index": 0,
      "embedding": [
        -0.022425562,
        -0.010263717,
        0.022136442,
        0.015323295,
        -0.0013466021,
        -0.009163808,
        -0.025279038,
        0.007510803,
        -0.017573394,
        -0.049326178,
        0.025216186,
        -0.012972634,
        0.01647977,
        0.025316749,
        0.03813854,
        -0.005716381,
        -0.011501899,
        -0.04020008,
        0.0023978003,
        -0.037761427,
        -0.016064947,
        0.029364413,
        0.013664005,
        0.035674743,
        0.034442846,
        -0.014506221,
        0.014091398,
        0.012488674,
        -0.03167736,
        -0.014506221,
        -0.0009066388
      ]
    }
  ],
  "model": "text-embedding-3-large",
  "usage": {
    "prompt_tokens": 16,
    "total_tokens": 16
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» object|string|true|none||none|
|» data|[object]|true|none||none|
|»» object|string|false|none||none|
|»» index|integer|false|none||none|
|»» embedding|[number]|false|none||none|
|» model|string|true|none||none|
|» usage|object|true|none||none|
|»» prompt_tokens|integer|true|none||none|
|»» total_tokens|integer|true|none||none|

# 可灵

## POST OMNI video

POST /kling/v1/videos/omni-video

> Body 请求参数

```json
{
  "model_name": "kling-v1",
  "prompt": "一只可爱的小狗在太空中开心的玩耍",
  "negative_prompt": "模糊,低质量",
  "duration": 5,
  "aspect_ratio": "16:9",
  "cfg_scale": 0.5,
  "mode": "std",
  "callback_url": "54.215.252.214:3000/kling/callback"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 ||none|
|» prompt|body|string| 是 ||none|
|» negative_prompt|body|string| 是 ||none|
|» duration|body|integer| 是 ||none|
|» aspect_ratio|body|string| 是 ||none|
|» cfg_scale|body|number| 是 ||none|
|» mode|body|string| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## POST 任务：文生视频

POST /kling/v1/videos/text2video

> Body 请求参数

```json
{
  "model_name": "kling-v1-6",
  "prompt": "20岁的女生，瓜子脸，五官精致，鹅蛋脸，黑色长发，皮肤白暂，氛围光线，穿着一袭白色短裙，坐在街道旁边的长椅上，面带微笑，甩着头发",
  "mode": "std",
  "aspect_ratio": "1:1",
  "duration": "5"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model_name|body|string| 否 | 模型版本|注意：版本越高价格越高，v2.0的价格是v1的10倍，10秒为20倍价格。|
|» prompt|body|string| 是 | 正向文本提示|不能超过2500个字符|
|» negative_prompt|body|string| 否 | 负向文本提示|不能超过2500个字符|
|» cfg_scale|body|number| 否 | 创意想象力|值越大，创意相关性越高，取值范围：[0,1]|
|» mode|body|string| 否 | 视频模式|**注意 注意 注意**：设置为 `pro` 模式 价格 * 3.5倍|
|» camera_control|body|object| 否 | 运镜控制|控制摄像机运动方式，未指定则智能匹配|
|»» type|body|string| 否 | 镜头类型|预定义的运镜类型|
|»» config|body|object| 否 | 镜头配置|包含六个字段，用于指定摄像机在不同方向上的运动或变化|
|»»» horizontal|body|integer| 否 | 水平运镜|取值范围：[-10, 10]|
|»»» vertical|body|integer| 否 | 垂直运镜|取值范围：[-10, 10]|
|»»» pan|body|integer| 否 | 水平摇镜|取值范围：[-10, 10]|
|»»» tit|body|integer| 否 | 垂直摇镜|取值范围：[-10, 10]|
|»»» roll|body|integer| 否 | 旋转运镜|取值范围：[-10, 10]|
|»»» zoom|body|integer| 否 | 变焦|取值范围：[-10, 10]|
|» aspect_ratio|body|string| 否 | 画面比例|none|
|» duration|body|string| 否 | 视频时长（单位秒）|**注意 注意 注意**：设置时长 `10` 价格 * 两倍|
|» callback_url|body|string| 否 | 通知地址|本次任务结果回调通知地址，详见 CallBack协议|

#### 详细说明

**» mode**: **注意 注意 注意**：设置为 `pro` 模式 价格 * 3.5倍
kling-v1-5、kling-v2-5-turbo只支持pro模式
kling-v2-master kling-v2-1-master 不支持该参数

其中std：标准模式（标准），基础模式，性价比高
其中pro：专家模式（高品质），高表现模式，生成视频质量更佳

**»» type**: 预定义的运镜类型
枚举值：“simple”, “down_back”, “forward_up”, “right_turn_forward”, “left_turn_forward”
simple：简单运镜，此类型下可在"config"中六选一进行运镜
down_back：镜头下压并后退 ➡️ 下移拉远，此类型下config参数无需填写
forward_up：镜头前进并上仰 ➡️ 推进上移，此类型下config参数无需填写
right_turn_forward：先右旋转后前进 ➡️ 右旋推进，此类型下config参数无需填写
left_turn_forward：先左旋并前进 ➡️ 左旋推进，此类型下config参数无需填写

**»» config**: 包含六个字段，用于指定摄像机在不同方向上的运动或变化

当运镜类型指定simple时必填，指定其他类型时不填
以下参数6选1，即只能有一个参数不为0，其余参数为0

#### 枚举值

|属性|值|
|---|---|
|» model_name|kling-v1|
|» model_name|kling-v1-5|
|» model_name|kling-v1-6|
|» model_name|kling-v2-master|
|» model_name|kling-v2-1-master|
|» model_name|kling-v2-5-turbo|
|» mode|std|
|» mode|pro|
|»» type|simple|
|»» type|down_back|
|»» type|forward_up|
|»» type|right_turn_forward|
|»» type|left_turn_forward|
|» aspect_ratio|16:9|
|» aspect_ratio|9:16|
|» aspect_ratio|1:1|
|» duration|5|
|» duration|10|

> 返回示例

> 200 Response

```json
{
  "code": 0,
  "message": "SUCCEED",
  "request_id": "CmYgjmbyMToAAAAAAF6svw",
  "data": {
    "task_id": "CmYgjmbyMToAAAAAAF6svw",
    "task_status": "submitted",
    "created_at": 1727338013674,
    "updated_at": 1727338013674
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» code|integer|true|none||none|
|» message|string|true|none||none|
|» data|object|true|none||none|
|»» task_id|string|true|none||none|
|»» action|string|true|none||none|
|»» status|string|true|none||none|
|»» fail_reason|string|true|none||none|
|»» submit_time|integer|true|none||none|
|»» start_time|integer|true|none||none|
|»» finish_time|integer|true|none||none|
|»» progress|string|true|none||none|
|»» data|object|true|none||none|
|»»» task_id|string|true|none||none|
|»»» created_at|integer|true|none||none|
|»»» updated_at|integer|true|none||none|
|»»» task_result|object|true|none||none|
|»»»» images|[object]|true|none||none|
|»»»»» url|string|false|none||none|
|»»»»» index|integer|false|none||none|
|»»»» videos|null|true|none||none|
|»»» task_status|string|true|none||none|
|»»» task_status_msg|string|true|none||none|
|»» created_at|integer|true|none||none|
|»» updated_at|integer|true|none||none|
|»» task_result|object|true|none||none|
|»»» images|[object]|true|none||none|
|»»»» url|string|false|none||none|
|»»»» index|integer|false|none||none|
|»»» videos|null|true|none||none|
|»» task_status|string|true|none||none|
|»» task_status_msg|string|true|none||none|
|» request_id|string|true|none||none|

## POST 任务：图生视频

POST /kling/v1/videos/image2video

> Body 请求参数

```json
{
  "image": "https://p2.a.kwimgs.com/bs2/upload-ylab-stunt/ai_portal/1731125871/6N0QrAnAeU/409-f054fa25c21a.png",
  "prompt": "一位年轻的中国女性自信地走在一片广阔的绿色草地上，身边有一匹棕色的马，她的脸是用IPA或Ecomid技术创作的，展示了受顾柔软自然外表启发的高度实现的皮肤纹理，有微妙的皮肤缺陷，如淡淡的雀斑和浅色毛孔，以保持真实感，富有表现力的眼睛和柔软的嘴唇为她的表情增添了深度，她长长的黑发在风中自然流动，穿着米色连身裤，上面有可见的织物皱纹，黑色靴子略带灰尘，柔和的自然光线突出了她的脸，与她旁边马的精致皮毛纹理无缝融合，带有IC照明设置的电影构图增强了深度和真实感，鲜艳而自然的色彩，环境光散射在草地上，远处滚动蓝天下的山丘上有蓬松的白云，绿草上有微妙的纹理变化，微风中她头发和马尾的动态运动，以电影和纪录片风格编辑，强调现实主义，用50mm镜头、f/2.8光圈、超现实纹理、浅景深和散景背景拍摄",
  "model": "kling-v1-5",
  "mode": "std",
  "duration": "5"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model_name|body|string| 否 | 模型版本|注意v2-master大师版本 价格是v1.0的10～20倍|
|» image|body|string| 是 | 参考图片|支持Base64编码或图片URL，支持.jpg / .jpeg / .png格式，大小不能超过10MB，分辨率不小于300*300px，图片宽高比要在1:2.5 ~ 2.5:1之间|
|» image_tail|body|string| 否 | 参考图像 - 尾帧控制|支持Base64编码或图片URL，支持.jpg / .jpeg / .png格式，大小不能超过10MB，分辨率不小于300*300px，填写该参数后，mode参数只能设置为pro|
|» prompt|body|string| 否 | 正向文本提示|不能超过2500个字符|
|» negative_prompt|body|string| 否 | 负向文本提示|不能超过2500个字符|
|» cfg_scale|body|number| 否 | 创意想象力|值越大，创意相关性越高，取值范围：[0,1]|
|» mode|body|string| 否 | 视频质量模式|**注意 注意 注意**：设置为 `pro` 模式 价格 * 3.5倍|
|» static_mask|body|string| 否 | 静态笔刷涂抹区域（用户通过运动笔刷涂抹的 mask 图片|● 支持传入图片Base64编码或图片URL（确保可访问，格式要求同 image 字段）|
|» dynamic_masks|body|[object]| 否 | 动态笔刷配置列表|可配置多组（最多6组），每组包含“涂抹区域 mask”与“运动轨迹 trajectories”序列|
|»» mask|body|string| 否 ||动态笔刷涂抹区域（用户通过运动笔刷涂抹的 mask 图片）|
|»» trajectories|body|[object]| 否 ||运动轨迹坐标序列|
|»»» x|body|string| 否 | 轨迹点横坐标|（在像素二维坐标系下，以输入图片image左下为原点的像素坐标）|
|»»» y|body|string| 否 | 轨迹点纵坐标|（在像素二维坐标系下，以输入图片image左下为原点的像素坐标）|
|» duration|body|string| 否 | 视频时长（单位秒）|**注意 注意 注意**：设置时长 `10` 价格 * 两倍|
|» callback_url|body|string| 否 | 通知地址|本次任务结果回调通知地址，详见 CallBack协议|
|» camera_control|body|object| 否 | 运镜控制|控制摄像机运动方式，未指定则智能匹配|
|»» type|body|string| 否 | 镜头类型|simple：简单运镜，此类型下可在"config"中六选一进行运镜|
|»» config|body|object| 否 | 镜头配置|包含六个字段，用于指定摄像机的运动或变化|
|»»» horizontal|body|integer| 否 | 水平运镜|取值范围：[-10, 10]|
|»»» vertical|body|integer| 否 | 垂直运镜|取值范围：[-10, 10]|
|»»» pan|body|integer| 否 | 水平摇镜|取值范围：[-10, 10]|
|»»» tit|body|integer| 否 | 垂直摇镜|取值范围：[-10, 10]|
|»»» roll|body|integer| 否 | 旋转运镜|取值范围：[-10, 10]|
|»»» zoom|body|integer| 否 | 变焦|取值范围：[-10, 10]|

#### 详细说明

**» mode**: **注意 注意 注意**：设置为 `pro` 模式 价格 * 3.5倍

其中std：标准模式（标准），基础模式，性价比高
其中pro：专家模式（高品质），高表现模式，生成视频质量更佳

**» static_mask**: ● 支持传入图片Base64编码或图片URL（确保可访问，格式要求同 image 字段）
● 图片格式支持.jpg / .jpeg / .png
● 图片长宽比必须与输入图片相同（即image字段），否则任务失败（failed）
static_mask 和 dynamic_masks.mask 这两张图片的分辨率必须一致，否则任务失败（failed）

**» dynamic_masks**: 可配置多组（最多6组），每组包含“涂抹区域 mask”与“运动轨迹 trajectories”序列
不同模型版本、视频模式支持范围不同

**»» mask**: 动态笔刷涂抹区域（用户通过运动笔刷涂抹的 mask 图片）
● 支持传入图片Base64编码或图片URL（确保可访问，格式要求同 image 字段）
● 图片格式支持.jpg / .jpeg / .png
● 图片长宽比必须与输入图片相同（即image字段），否则任务失败（failed）
static_mask 和 dynamic_masks.mask 这两张图片的分辨率必须一致，否则任务失败（failed）

**»» trajectories**: 运动轨迹坐标序列
● 生成5s的视频，轨迹长度不超过77，即坐标个数取值范围：[2, 77]
● 轨迹坐标系，以图片左下角为坐标原点
注1：坐标点个数越多轨迹刻画越准确，如只有2个轨迹点则为这两点连接的直线
注2：轨迹方向以传入顺序为指向，以最先传入的坐标为轨迹起点，依次链接后续坐标形成运动轨迹

**»» type**: simple：简单运镜，此类型下可在"config"中六选一进行运镜
down_back：镜头下压并后退 ➡️ 下移拉远，此类型下config参数无需填写
forward_up：镜头前进并上仰 ➡️ 推进上移，此类型下config参数无需填写
right_turn_forward：先右旋转后前进 ➡️ 右旋推进，此类型下config参数无需填写
left_turn_forward：先左旋并前进 ➡️ 左旋推进，此类型下config参数无需填写

#### 枚举值

|属性|值|
|---|---|
|» model_name| kling-v1|
|» model_name| kling-v1-5|
|» model_name|kling-v1-6|
|» model_name|kling-v2-master|
|» model_name|kling-v2-1|
|» model_name|kling-v2-1-master|
|» model_name|kling-v2-5-turbo|
|» mode|std|
|» mode|pro|
|» duration|5|
|» duration|10|
|»» type|simple|
|»» type|down_back|
|»» type|forward_up|
|»» type|right_turn_forward|
|»» type|left_turn_forward|

> 返回示例

> 200 Response

```json
{
  "code": 0,
  "message": "SUCCEED",
  "request_id": "CmYgjmbyMToAAAAAAF6svw",
  "data": {
    "task_id": "CmYgjmbyMToAAAAAAF6svw",
    "task_status": "submitted",
    "created_at": 1727338013674,
    "updated_at": 1727338013674
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» code|integer|true|none||none|
|» message|string|true|none||none|
|» data|object|true|none||none|
|»» task_id|string|true|none||none|
|»» action|string|true|none||none|
|»» status|string|true|none||none|
|»» fail_reason|string|true|none||none|
|»» submit_time|integer|true|none||none|
|»» start_time|integer|true|none||none|
|»» finish_time|integer|true|none||none|
|»» progress|string|true|none||none|
|»» data|object|true|none||none|
|»»» task_id|string|true|none||none|
|»»» created_at|integer|true|none||none|
|»»» updated_at|integer|true|none||none|
|»»» task_result|object|true|none||none|
|»»»» images|[object]|true|none||none|
|»»»»» url|string|false|none||none|
|»»»»» index|integer|false|none||none|
|»»»» videos|null|true|none||none|
|»»» task_status|string|true|none||none|
|»»» task_status_msg|string|true|none||none|
|»» created_at|integer|true|none||none|
|»» updated_at|integer|true|none||none|
|»» task_result|object|true|none||none|
|»»» images|[object]|true|none||none|
|»»»» url|string|false|none||none|
|»»»» index|integer|false|none||none|
|»»» videos|null|true|none||none|
|»» task_status|string|true|none||none|
|»» task_status_msg|string|true|none||none|
|» request_id|string|true|none||none|

## GET 查询

GET /kling/v1/videos/text2video/846167906332725314

> Body 请求参数

```yaml
{}

```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|model_name|query|string| 否 ||none|
|model|header|string| 是 ||none|
|body|body|object| 否 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 通用查询

GET /kling/v1/video/843220821212033063

> Body 请求参数

```yaml
{}

```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|taskid|query|string| 是 ||none|
|body|body|object| 否 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## POST motion-control

POST /kling/v1/videos/motion-control

> Body 请求参数

```json
{
  "model_name": "kling-v1-6",
  "image_url": "https://www.shutterstock.com/zh/image-photo/man-snowboarding-on-snowy-hill-winter-1664714053",
  "video_url": "https://v4-fdl.kechuangai.com/ksc2/JD23rTwLF8nO7HcBOKLLRbDN-br6305GeahJDUtregFeuisLQCj3m1DImibsFq8M9iL2cgwT-jOuAgnkluXRWZ6yuWTpYeOSlVjeUhl373SZ47o-kuy3D_tXtGsCjWJokwCIzQTDSBIFhD44dONlaxSCl7QJ8_ZqF6yvRtZxTCFDFftvW1t3-62CekzOpkIn-rf4mX22rshUP-8kXtXbzw.mp4?cacheKey=ChtzZWN1cml0eS5rbGluZy5tZXRhX2VuY3J5cHQSsAElaJUlOjUD4m17YeCp9JkbFEWK-HKkyQvL8GV6imdP4xfdfN9fNSyba54aoZfX3ZrBruMjyqgJltyhoD4T43l6z5mZoetKvWR-5ETx6DkUDlRCZpL8RN8uChcVDY0ehRBil6rUauFVx51mirGO5r4YEfpod2nln-qzIgsYdxoS0MdIrdsuxKI3L3B-ZgrSoWOa-nS413vOGA3gl8EWR2CHDALKY4qLTvpCNoqEAtaOiRoSu4b9SEvlSFLKC9ylAQ2baCJIIiAdZfzoK9ez0XjClG1V-E1Oa1QfECufBy3q6XYW-zSfUygFMAE&x-kcdn-pid=112757&pkey=AAV_WvQmJSKlCznk706zqmb-ljZ_XsWYNafu_qQxugjiEnIywg8feP7tRUfJ-5GPp3eJsw-JCDrQNSSB3Clc8VH1rCmPymt2djaoewbNeS-rm1fFc5WcP-N2bRnRQcyl_Yk",
  "mode": "std",
  "duration": 10,
  "character_orientation": "video"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 ||none|
|» image_url|body|string| 是 ||none|
|» video_url|body|string| 是 ||none|
|» mode|body|string| 是 ||none|
|» duration|body|integer| 是 ||none|
|» character_orientation|body|string| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## POST custom-elements

POST /kling/v1/general/custom-elements

> Body 请求参数

```json
{
  "model_name": "kling-v1-6",
  "element_name": "自定义主体-001",
  "element_description": "自定义主体测试-001",
  "element_frontal_image": "https://docs.qingque.cn/image/api/convert/loadimage?id=-8654991330408162800eZQDlFDacBuEmer7HQstW4wes&docId=eZQAl5y8xNSkr0iYUS8-bpGvP&identityId=2Oa28mncRIC&loadSource=true",
  "element_refer_list": [
    {
      "image_url": "https://docs.qingque.cn/image/api/convert/loadimage?id=-8654991330408162800eZQDlFDacBuEmer7HQstW4wes&docId=eZQAl5y8xNSkr0iYUS8-bpGvP&identityId=2Oa28mncRIC&loadSource=true"
    }
  ],
  "tag_list": [
    {
      "tag_id": "o_101"
    }
  ]
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 ||none|
|» element_name|body|string| 是 ||none|
|» element_description|body|string| 是 ||none|
|» element_frontal_image|body|string| 是 ||none|
|» element_refer_list|body|[object]| 是 ||none|
|»» image_url|body|string| 否 ||none|
|» tag_list|body|[object]| 是 ||none|
|»» tag_id|body|string| 否 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询主体

GET /kling/v1/general/custom-elements

> Body 请求参数

```yaml
{}

```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|model_name|query|string| 是 ||none|
|body|body|object| 否 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## POST 自定义音色

POST /kling/v1/general/custom-voices

> Body 请求参数

```json
{
  "model_name": "kling-v1",
  "voice_name": "自定义主体-001",
  "voice_url": "https://sis-sample-audio.obs.cn-north-1.myhuaweicloud.com/16k16bit.mp3"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 ||none|
|» voice_name|body|string| 是 ||none|
|» voice_url|body|string| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询音色列表

GET /kling/v1/general/custom-voices

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|model_name|query|string| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询音色

GET /kling/v1/general/custom-voices/842893307092422696

> Body 请求参数

```yaml
{}

```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|model_name|query|string| 是 ||none|
|body|body|object| 否 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询-动作控制

GET /kling/v1/videos/motion-control/843636824022622221

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|model_name|query|string| 否 ||none|

> 返回示例

> 200 Response

```json
{
  "code": 0,
  "message": "SUCCEED",
  "request_id": "07bb4004-93d7-4347-b4e9-5763a301111f",
  "data": {
    "task_id": "843636824022622221",
    "task_status": "succeed",
    "task_info": {},
    "task_result": {
      "videos": [
        {
          "id": "843636824123285507",
          "url": "https://v2-fdl.kechuangai.com/ksc2/7ulVAZOHgSg5FvLxwhQh315V6QCIBEKLytNWkcQEDq-MSe5eSl2WOEc3VOWCh_zic0HU6Yv3815kVn-duZx6zIME9bUZ6VabjI8R44lhcfwcczdfH_SXl6JFCceS1PDqiEOLhtvGFCr-uRyDuJcHw8b-XM-SnEz70ekS0-OsphY7D0DopFSLaNwzs4GSfPF_cFqQsn1QDmIL9XT-bsn4lQ.mp4?cacheKey=ChtzZWN1cml0eS5rbGluZy5tZXRhX2VuY3J5cHQSsAEZ7lRNS-xJ_NNJqqaPP_3z05W1w5ZnJeiYMbDwdziXc_bDI1UootT33SNToiK-SDhiYozmBVBzZQTZsgrVRIS0RRGruTmmvlSJ3q9RbtWcq6CETY4Ay1scxgACSkmutrI5h1cr46VXpBllKDfzZoqXzB0w7X0734KfgIx8r7tk1yd1DTyWcQXulsDox78eckk5Oxi0wSL9MuTI-CfC6APqY4wfk8SM8kCPbO3NCEm77RoSS2Y9drB8Z4ednHxTIh7XZcnaIiBDMXYPacP-CyC-9p0skusHebPxUA_-x5viVho142p6ACgFMAE&x-kcdn-pid=112757&pkey=AAU295xPRmI4QOjOmfDuYDsLLPSyUnUK73tEYU7_YR4jwHsHx_xS0yGiRq0Qw4111Sa6lCfO-ZTL8Zd7eccWbMXlrju9pMwV_H6KOMIHgNv4XvsEb7x6GtDWlHisZpYFQgw",
          "duration": "4.8"
        }
      ]
    },
    "task_status_msg": "",
    "created_at": 1769167211644,
    "updated_at": 1769167392522,
    "final_unit_deduction": "4"
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询文生视频

GET /kling/v1/videos/text2video/845426607803756578

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|model_name|query|string| 否 ||none|

> 返回示例

> 200 Response

```json
{
  "code": 0,
  "message": "SUCCEED",
  "request_id": "07839fd8-e2e9-4340-b283-e479be80d59a",
  "data": {
    "task_id": "845426607803756578",
    "task_status": "succeed",
    "task_info": {
      "external_task_id": "10052"
    },
    "task_result": {
      "videos": [
        {
          "id": "845426607917010989",
          "url": "https://v4-fdl.kechuangai.com/ksc2/UccoRzEeRYCqIdlz9KTUxynTWxVkaExDPOlek4kPioDwzu03OuBzavujzLi8p2Vd60yF2xRpCSxfYzY7ETsHi-6z762EgtRWaAmTHns_dvwoZjoEdqrH7bVZKIDInxdOJ2mlH3gtj7PbiyRx1Ao4x9iBBNuJzdLB-s38b1h6gbhFRpopmSH4Y4W5C74uqI8SeObdmC5rHEzIpmN5IVdD9w.mp4?cacheKey=ChtzZWN1cml0eS5rbGluZy5tZXRhX2VuY3J5cHQSsAEXUoluUw3T4-1vCw5oiHOzoo0VADeoWJ2CyCM2KPAgVYHMUWFGuJ1S6vSN4Wps9ftpSCQmhzia3plYEZ3GkxYTdxUw-6x3G-ryMLJgpPb3AncYi55rROhy9Q12QqfY6MKUnhOXJx2DEvZhARHU-8IFDGcRcTUae_TdiBACR0WcO528RQGNjC57FkwRXbtNGhCuhpZlL2ioGMtaH5Xb6lM0CgDmu_BSHlAhOt-sEI98EhoSiLisQ3JW5QwpPz64VCCZ6-c0IiDM325tb8wZ6PdvI8wX0ZvdcOMiKIjhhuGTlcyGyR0p8ygFMAE&x-kcdn-pid=112757&pkey=AAWmSBoFfriiM-BNocw8xn0wftSLqbwa30gJDz3QAchrsrJqDWw60LeB-HmH4_aqJssOteyOLtOJcdjRXdQ8ktMm4zW9rIdHEreSn1I7F5R39vrkZtNEDxcFDtOCm-CaU7w",
          "duration": "5.1"
        }
      ]
    },
    "task_status_msg": "",
    "created_at": 1769593929347,
    "updated_at": 1769594234791,
    "final_unit_deduction": "1"
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## POST omni-image

POST /kling/v1/images/omni-image

> Body 请求参数

```json
{
  "model_name": "kling-image-o1",
  "prompt": "一只可爱的小狗在太空中开心的玩耍"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 是 ||none|

> 返回示例

> 200 Response

```json
{
  "code": 0,
  "message": "SUCCEED",
  "request_id": "38463816-da48-4dd4-834d-a076a9341ad2",
  "data": {
    "created_at": 1770349656149,
    "task_id": "848596355739721798",
    "task_info": {
      "external_task_id": "770000"
    },
    "task_status": "submitted",
    "updated_at": 1770349656149
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询omni-images

GET /kling/v1/images/omni-image/848598408389988433

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|model_name|query|string| 否 ||none|

> 返回示例

> 200 Response

```json
{
  "code": 0,
  "message": "SUCCEED",
  "request_id": "8b2580cb-d74b-4754-98c7-4524c4ef1372",
  "data": {
    "task_id": "848598408389988433",
    "task_status": "succeed",
    "task_info": {
      "external_task_id": "770002"
    },
    "task_result": {
      "result_type": "single",
      "images": [
        {
          "index": 0,
          "url": "https://p4-fdl.klingai.com/ksc2/IsqU-KLioff1PrtuaOt61O5IOACSMdr5JumbsOZ0kcu3bdwL63iGUO9Ai8NBqgO6Xt2Fn3A7WkK1ZNKzwaHV98yJVq47nAUipAhcAKgLGexIpyboIV3yHQOYsAV5jVPIwDz8knEm_Ke8H3HMCCbI3oCS6v0fDwFeBAY-IqjedSGgcI0O2tPVYAUTvdRls74zoxe-tjiw3QJwhgOdduPo3w.png?cacheKey=ChtzZWN1cml0eS5rbGluZy5tZXRhX2VuY3J5cHQSsAEjxTYCoCCABWwyJPBdVtCMSQM5u6PJ_3x3K1KOzY-SMZo1m6tOSCgymJ-ycaGTH55Ruj8buPXKC6j3FxBKewL5AMBJ64NMhgbv1nAciAp2COhZIcV7BpbW8Z0c_TDgm-5Rs0sNMyf81TlcGma2c3KtWOwsfqbE822Jcl1oBEbNBSK9qwQLkNtA6pYte9rT3gq4Cuk8I5LKcQyl0JNNom47fzbE6BSCmmxQhAWTet_H2BoSMiIwzDkRLkXJDo0Gm30rVg5KIiBItkscswqs4yry0222SCPoRr4wwYBqDzq46qhYwynakCgFMAE&x-kcdn-pid=112757&pkey=AAVTv9MAgOYJYj9_iI95X170GI_EYCMYo5ecg6CvKi6AsaznHq00MViRVBLDCnyRIhirLHbIP7fwMHTsu1GqTETsVNsoWVZY_PMCCrGG8B4BWgXNX3Q614-VqkN82BDbfQQ"
        }
      ],
      "series_images": []
    },
    "task_status_msg": "",
    "created_at": 1770350145540,
    "updated_at": 1770350183041,
    "final_unit_deduction": "8"
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

# Flux

## POST /v1/flux-2-pro

POST /flux/v1/flux-2-pro

> Body 请求参数

```json
{
  "model": "flux-2-pro",
  "prompt": "A sunset over tree with a little bird flying over the sky",
  "seed": 42,
  "width": 1024,
  "height": 972,
  "safety_tolerance": 2,
  "output_format": "jpeg"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 ||none|
|» prompt|body|string| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询flux

GET /flux/v1/get_result/c075c9ca-5e89-4d18-b0da-b63244779524

> Body 请求参数

```yaml
model: flux-2-pro

```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

# sora(原生)

## POST 文生视频

POST /v1/videos

> Body 请求参数

```json
{
  "prompt": "一只凶猛的老虎破门闯入农家小院，土狗吓得跑开，猫咪跳出来击败吓跑老虎。",
  "model": "sora-2",
  "seconds": "8",
  "size": "1280x720"
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» prompt|body|string| 是 | 提示词|描述要生成视频的文本提示|
|» model|body|string| 否 | 模型名称|none|
|» seconds|body|string| 否 | 视频秒数|最终扣费：模型价格x秒数，注意：参数类型为字符串类型|
|» size|body|string| 否 | 视频尺寸|1792尺寸只支持 `sora-2-pro` 模型|

#### 枚举值

|属性|值|
|---|---|
|» model|sora-2|
|» model|sora-2-pro|
|» seconds|4|
|» seconds|8|
|» seconds|12|
|» size|720x1280|
|» size|1280x720|
|» size|1024x1792|
|» size|1792x1024|

> 返回示例

> 200 Response

```json
{
  "id": "video_68f082321ed08193a4eaf01376fa10bc0284bd663de64dc5",
  "object": "video",
  "created_at": 1760592434,
  "status": "queued",
  "completed_at": null,
  "error": null,
  "expires_at": null,
  "model": "sora-2",
  "progress": 0,
  "remixed_from_video_id": null,
  "seconds": "8",
  "size": "1280x720"
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» id|string|true|none||none|
|» object|string|true|none||none|
|» created_at|integer|true|none||none|
|» status|string|true|none||none|
|» completed_at|null|true|none||none|
|» error|null|true|none||none|
|» expires_at|null|true|none||none|
|» model|string|true|none||none|
|» progress|integer|true|none||none|
|» remixed_from_video_id|null|true|none||none|
|» seconds|string|true|none||none|
|» size|string|true|none||none|

## GET 查询内容接口

GET /videos/{videoId}/content

官方文档地址:https://platform.openai.com/docs/api-reference/videos/content?lang=ruby

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|videoId|path|string| 是 ||none|
|variant|query|string| 否 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询详情接口

GET /v1/videos/{video_id}

官方文档：https://platform.openai.com/docs/api-reference/videos/retrieve?lang=ruby

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|video_id|path|string| 是 ||none|

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

# 阿里万相

## POST 图生视频

POST /ali/api/v1/services/aigc/video-generation/video-synthesis

> Body 请求参数

```json
{
  "model": "wan2.6-i2v",
  "input": {
    "prompt": "一幅都市奇幻艺术的场景。一个充满动感的涂鸦艺术角色。一个由喷漆所画成的少年，正从一面混凝土墙上活过来。他一边用极快的语速演唱一首英文rap，一边摆着一个经典的、充满活力的说唱歌手姿势。场景设定在夜晚一个充满都市感的铁路桥下。灯光来自一盏孤零零的街灯，营造出电影般的氛围，充满高能量和惊人的细节。视频的音频部分完全由他的rap构成，没有其他对话或杂音。",
    "img_url": "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20250925/wpimhv/rap.png",
    "audio_url": "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20250925/ozwpvi/rap.mp3"
  },
  "parameters": {
    "resolution": "720P",
    "prompt_extend": true,
    "duration": 10,
    "shot_type": "multi"
  }
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 是 ||none|

> 返回示例

> 200 Response

```json
{
  "error": {
    "message": "Token not provided (request id: 202603111458126196500003648750)",
    "type": "api_error"
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 查询任务

GET /ali/api/v1/tasks/a31a88b5-fbee-46ad-a73c-a1d5579cb5b6

> 返回示例

> 200 Response

```json
{
  "error": {
    "message": "Token not provided (request id: 2026031117452356884600010667889)",
    "type": "api_error"
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

# Video通用接口（持续开发中）

<a id="opIdgenerateVideoJimeng"></a>

## POST 即梦视频生成

POST /v1/video/generations

使用火山引擎即梦（豆包）模型进行视频生成。支持文生视频和图生视频，遵循火山引擎官方API规范。

## 支持的即梦模型：

#### 版本模型系列：
- **doubao-seedance-1-0-lite-i2v-250428**：图生视频增强版，支持首帧图片+尾帧图片（可选）+文本提示词（可选）+参数（可选）生成目标视频。**注意：使用首尾帧功能时，仅支持480p和720p分辨率**

- **doubao-seedance-1-0-lite-t2v-250428**：文生视频增强版，根据文本提示词+参数（可选）生成目标视频

- **doubao-seedance-1-0-pro-250528**：专业版模型，支持文生视频和图生视频，提供更高质量的视频生成

## 请求体格式：

即梦模型使用`content`数组格式，遵循火山引擎官方API规范：
- **model**：模型ID
- **content**：内容数组，支持文本和图片类型
- **callback_url**：可选的回调URL

## 即梦模型图片使用说明：

### 图片类型支持：
- **首帧图生视频**：传入1个`image_url`对象，`role`为`first_frame`或不填
- **首尾帧图生视频**：传入2个`image_url`对象，一个`role`为`first_frame`，另一个`role`为`last_frame`

### 首尾帧功能详细说明：
- **使用首尾帧图生视频功能时**：需传入2个`image_url`对象，1个`role`为`first_frame`，另一个`role`为`last_frame`
- **图片要求**：传入的首尾帧图片可相同
- **宽高比处理**：首尾帧图片的宽高比不一致时，以首帧图片为主，尾帧图片会自动裁剪适配
- **分辨率限制**：使用首尾帧功能时，仅支持480p和720p分辨率
- **首帧图生视频功能时**：role填写`first_frame`或不填

### 图片格式支持：
- **URL方式**：`image_url.url`字段，支持HTTP/HTTPS协议的图片链接
- **Base64方式**：`image_url.image_url`字段，格式为`data:image/[格式];base64,[数据]`

## 即梦模型文本命令：
即梦模型支持在文本提示词后追加参数命令，格式为 `--[参数名] [值]`，控制视频输出的规格：

### 常用参数：
- `--ratio [宽高比]`：如 16:9、9:16、1:1、adaptive、keep_ratio等
- `--duration [时长]`：视频时长（秒），如 5、10
- `--rs [分辨率]`：分辨率，如 480p、720p、1080p
- `--fps [帧率]`：帧率，如 16、24
- `--wm [true/false]`：是否包含水印
- `--seed [数字]`：随机种子，-1为随机
- `--cf [true/false]`：是否固定摄像头

### 示例：
```
多个镜头。一名侦探进入一间光线昏暗的房间 --ratio 16:9 --duration 5 --fps 24
```

**注意：**使用不同的模型，可能对应支持不同的参数与取值。当输入的参数或取值不符合所选的模型时，内容会被忽略或报错。

**参考文档：**[火山引擎官方文档](https://www.volcengine.com/docs/82379/1520757)

> Body 请求参数

```json
{
  "model": "doubao-seedance-1-0-lite-t2v-250428",
  "content": [
    {
      "type": "text",
      "text": "日落下的城市，延时摄影 --ratio 16:9 --duration 5"
    }
  ]
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|Content-Type|header|string| 否 ||请求体类型，固定为 application/json|
|Authorization|header|string| 否 ||API 鉴权 Token，格式为 Bearer {API_KEY}|
|body|body|[JimengVideoGenerationRequest](#schemajimengvideogenerationrequest)| 否 ||none|

> 返回示例

> 200 Response

```json
{
  "taskId": "string",
  "status": "string"
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|即梦视频生成任务提交成功|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» taskId|string|false|none||生成任务ID|
|» status|string|false|none||任务状态|

## POST 可灵视频生成

POST /

> 返回示例

> 200 Response

```json
{}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

## GET 视频生成结果查询

GET /v1/video/generations/result

查询视频生成任务的状态和结果。通过提交视频生成请求时返回的task_id来查询生成进度和获取最终的视频结果。

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|taskid|query|string| 是 ||视频生成任务ID，即提交视频生成请求时返回的task_id|
|response_format|query|string| 否 ||响应格式，通常为url格式返回视频链接|
|Content-Type|header|string| 否 ||请求体类型，固定为 application/json|
|Authorization|header|string| 否 ||API 鉴权 Token，格式为 Bearer {API_KEY}|

> 返回示例

> 200 Response

```json
{
  "task_id": "cgt-20250714221020-668xc",
  "video_result": "https://example.com/video.mp4",
  "video_results": [],
  "video_id": "cgt-20250714221020-668xc",
  "task_status": "succeeded",
  "message": "",
  "duration": "5.0"
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|视频生成结果查询成功|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» task_id|string|true|none||任务ID|
|» video_result|string|true|none||单个视频任务的结果URL（注意：该字段后期会被取消）|
|» video_results|[string]|true|none||多视频任务的结果URL数组|
|» video_id|string|true|none||视频ID，通常与task_id相同|
|» task_status|string|true|none||任务状态|
|» message|string|true|none||响应消息，成功时通常为空|
|» duration|string|true|none||视频时长|

#### 枚举值

|属性|值|
|---|---|
|task_status|pending|
|task_status|processing|
|task_status|succeeded|
|task_status|failed|

# 图片生成（image）/Qwen

## POST 图像编辑

POST /v1/images/edits

:::tip
注意：该接口使用FormData参数请求，并非Json格式参数。
:::

> Body 请求参数

```yaml
image: "@otter.png"
mask: "@mask.png"
prompt: 一只可爱的海獺宝宝戴着贝雷帽。
n: 2
size: 1024x1024

```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» model|body|string| 是 ||模型名称，用于图像生成的模型。推荐使用 `qwen-image-edit`|
|» image|body|string(binary)| 是 ||源图像（最多传入1张图）|
|» prompt|body|string| 是 ||提示词，所需图像的文本描述。最大长度为 800 个字符。|
|» negative_prompt|body|string| 否 ||描述不希望在画面中看到的内容。|
|» n|body|integer| 否 ||（默认值 1，1～4张）一次性生成的图片数量：数量越多生成速度越慢。|
|» response_format|body|string| 否 ||返回生成图像的数据格式。必须是 `url` 或 `b64_json`之一。URL 仅在图像生成后的 60 分钟内有效。|
|» seed|body|integer| 否 ||随机数种子。取值范围是[0, 2147483647]。|

#### 详细说明

**» image**: 源图像（最多传入1张图）
要编辑的图像 `File` 对象或对象数组。必须是有效的图片 文件且小于 `10MB`。图像格式：JPG、JPEG、PNG、BMP、TIFF、WEBP。

**» negative_prompt**: 描述不希望在画面中看到的内容。
示例值：低分辨率、错误、最差质量、低质量、残缺、多余的手指、比例不良等。

**» n**: （默认值 1，1～4张）一次性生成的图片数量：数量越多生成速度越慢。
`qwen-image-edit`只支持生成一张，请勿超过1张，否则扣费会*n。

**» seed**: 随机数种子。取值范围是[0, 2147483647]。

如果不提供，则算法自动生成一个随机数作为种子。

如果您希望生成内容保持相对稳定，请使用相同的seed参数值。

提示：模型生成过程具有概率性，因此即使使用相同的 seed，也不能保证每次生成结果完全一致。

#### 枚举值

|属性|值|
|---|---|
|» model|qwen-image-edit|
|» response_format|url|
|» response_format|b64_json|

> 返回示例

```json
{
  "created": 1757255628,
  "data": [
    {
      "url": "https://dashscope-result-sh.oss-cn-shanghai.aliyuncs.com/example/path/example-image.png?Expires=1757861427&OSSAccessKeyId=YOUR_ACCESS_KEY_ID&Signature=REDACTED_SIGNATURE"
    }
  ],
  "usage": {
    "total_tokens": 0,
    "input_tokens": 0,
    "output_tokens": 0,
    "input_tokens_details": {
      "text_tokens": 0,
      "image_tokens": 0
    }
  }
}
```

```json
{
  "created": 1745984918,
  "data": [
    {
      "b64_json": "iVBORw0KGgoAAAANSUhEUgAABAAAAAYACAI..."
    }
  ],
  "usage": {
    "input_tokens": 8,
    "input_tokens_details": {
      "image_tokens": 0,
      "text_tokens": 8
    },
    "output_tokens": 408,
    "total_tokens": 416
  }
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» created|integer|true|none||none|
|» data|[object]|true|none||none|
|»» url|string|false|none||none|

# 图片生成（image）/Grok

## POST 图像生成

POST /v1/images/generations

grok的图片生成接口与dalle 基本一致，区别是支持的参数更少。

> Body 请求参数

```json
{
  "model": "grok-2-image-1212",
  "prompt": "一幅详细的图像描绘了一个广阔的石器时代城市，城市中高耸的摩天大楼由巨石和骨头构成。这里热闹非凡，各种性别和种族的人们共同展现出原始人和原始女性的统一外貌。他们身穿商务服装，与史前背景形成鲜明对比。化石化的塔楼点缀着石质天际线，而人们则保留着原始的设计，身上披着毛皮，搭配现代的西装和领带。形成了现代生活与史前时代的美丽融合。",
  "n": 1
}
```

### 请求参数

|名称|位置|类型|必选|中文名|说明|
|---|---|---|---|---|---|
|body|body|object| 否 ||none|
|» prompt|body|string| 是 | 提示词|所需图像的文本描述。最大长度为 1000 个字符，最大长度为 4000 个字符。|
|» model|body|string| 是 | 模型名称|支持 `grok-2-image-1212`|
|» n|body|number¦null| 否 | 生成数量|（默认值 1）一次性生成的图片数量：`dall-e-2` 支持1-10张 `dall-e-3` 只支持1张（可进行批量轮询请求多张）|
|» response_format|body|string¦null| 否 | 数据格式|返回生成图像的数据格式。必须是 `url` 或 `b64_json`之一。URL 仅在图像生成后的 60 分钟内有效。|

> 返回示例

> 200 Response

```json
{
  "data": [
    {
      "url": "https://imgen.x.ai/xai-imgen/xai-tmp-imgen-11b443c1-3780-4690-849a-51430eecc0a9.jpeg",
      "revised_prompt": "A high-resolution photograph of a bustling prehistoric city during the day, featuring towering skyscrapers made of large stones and bones. The main focus is on a group of people dressed in a mix of modern business attire and animal skins, interacting in the foreground. The central building, a grand stone and bone structure, stands prominently in the background. The scene is set under a clear sky, with the cityscape extending into the distance, showing more of the unique architecture without distracting elements. The overall composition emphasizes the harmonious blend of ancient and modern elements, with a focus on the people and their unique attire."
    }
  ]
}
```

### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|none|Inline|

### 返回数据结构

状态码 **200**

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|» data|[object]|true|none||none|
|»» url|string|false|none||none|
|»» revised_prompt|string|false|none||none|

# 数据模型

<h2 id="tocS_Veo3VideoGenerationRequest">Veo3VideoGenerationRequest</h2>

<a id="schemaveo3videogenerationrequest"></a>
<a id="schema_Veo3VideoGenerationRequest"></a>
<a id="tocSveo3videogenerationrequest"></a>
<a id="tocsveo3videogenerationrequest"></a>

```json
{
  "model": "veo-3.0-generate-preview",
  "instances": [
    {
      "prompt": "string",
      "image": {
        "bytesBase64Encoded": "string",
        "mimeType": "string"
      }
    }
  ],
  "parameters": {
    "aspectRatio": "string",
    "durationSeconds": 0,
    "enhancePrompt": true,
    "generateAudio": true,
    "negativePrompt": "string",
    "personGeneration": "string",
    "sampleCount": 0,
    "seed": 0
  }
}

```

### 属性

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|model|string|true|none||Veo3模型名称|
|instances|[[Veo3Instance](#schemaveo3instance)]|true|none||生成实例列表，每个实例可包含文本和图片提示词|
|parameters|[Veo3Parameters](#schemaveo3parameters)|true|none||none|

#### 枚举值

|属性|值|
|---|---|
|model|veo-3.0-generate-preview|
|model|veo-3.0-fast-generate-preview|

<h2 id="tocS_Veo3Instance">Veo3Instance</h2>

<a id="schemaveo3instance"></a>
<a id="schema_Veo3Instance"></a>
<a id="tocSveo3instance"></a>
<a id="tocsveo3instance"></a>

```json
{
  "prompt": "string",
  "image": {
    "bytesBase64Encoded": "string",
    "mimeType": "string"
  }
}

```

### 属性

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|prompt|string|true|none||文本提示词，描述希望生成的视频内容|
|image|[Veo3Image](#schemaveo3image)|false|none||可选，图片提示词，Base64 编码|

<h2 id="tocS_Veo3Image">Veo3Image</h2>

<a id="schemaveo3image"></a>
<a id="schema_Veo3Image"></a>
<a id="tocSveo3image"></a>
<a id="tocsveo3image"></a>

```json
{
  "bytesBase64Encoded": "string",
  "mimeType": "string"
}

```

### 属性

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|bytesBase64Encoded|string|true|none||图片内容的 Base64 编码|
|mimeType|string|true|none||图片 MIME 类型，如 image/png|

<h2 id="tocS_Veo3Parameters">Veo3Parameters</h2>

<a id="schemaveo3parameters"></a>
<a id="schema_Veo3Parameters"></a>
<a id="tocSveo3parameters"></a>
<a id="tocsveo3parameters"></a>

```json
{
  "aspectRatio": "string",
  "durationSeconds": 0,
  "enhancePrompt": true,
  "generateAudio": true,
  "negativePrompt": "string",
  "personGeneration": "string",
  "sampleCount": 0,
  "seed": 0
}

```

### 属性

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|aspectRatio|string|true|none||宽高比，如 16:9、9:16、1:1|
|durationSeconds|integer|true|none||视频时长（秒）|
|enhancePrompt|boolean|true|none||是否增强提示词|
|generateAudio|boolean|true|none||是否生成音频|
|negativePrompt|string|true|none||反向提示词，描述不希望出现的内容|
|personGeneration|string|true|none||人物生成策略，如 allow/deny|
|sampleCount|integer|true|none||生成样本数量|
|seed|integer(uint32)|true|none||随机种子|

<h2 id="tocS_JimengVideoGenerationRequest">JimengVideoGenerationRequest</h2>

<a id="schemajimengvideogenerationrequest"></a>
<a id="schema_JimengVideoGenerationRequest"></a>
<a id="tocSjimengvideogenerationrequest"></a>
<a id="tocsjimengvideogenerationrequest"></a>

```json
{
  "model": "doubao-seedance-1-0-lite-i2v-250428",
  "content": [
    {
      "type": "text",
      "text": "string",
      "image_url": {
        "url": "string",
        "image_url": "string"
      },
      "role": "first_frame"
    }
  ],
  "callback_url": "string"
}

```

### 属性

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|model|string|true|none||必选。即梦模型ID，您需要调用的模型的 ID，不同模型支持不同的功能。您也可通过 Endpoint ID 来调用模型，获得限流、计费类型（前付费/后付费）、运行状态查询、监控、安全等高级能力。|
|content|[[JimengContent](#schemajimengcontent)]|true|none||必选。输入给模型，生成视频的信息，支持文本信息和图片信息。|
|callback_url|string|false|none||可选。回调 URL，当视频生成完成后，火山引擎会将视频的 URL 通过该 URL 通知您。|

#### 枚举值

|属性|值|
|---|---|
|model|doubao-seedance-1-0-lite-i2v-250428|
|model|doubao-seedance-1-0-lite-t2v-250428|
|model|doubao-seedance-1-0-pro-250528|

<h2 id="tocS_JimengContent">JimengContent</h2>

<a id="schemajimengcontent"></a>
<a id="schema_JimengContent"></a>
<a id="tocSjimengcontent"></a>
<a id="tocsjimengcontent"></a>

```json
{
  "type": "text",
  "text": "string",
  "image_url": {
    "url": "string",
    "image_url": "string"
  },
  "role": "first_frame"
}

```

### 属性

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|type|string|true|none||必选。输入内容的类型|
|text|string|false|none||文本提示词，描述希望生成的视频内容。可以在文本后追加模型文本命令，如 '--ratio 16:9 --duration 5'。仅当type为'text'时使用。|
|image_url|[JimengImageUrl](#schemajimengimageurl)|false|none||必选。输入给模型的图片对象。仅当type为'image_url'时使用。|
|role|string|false|none||条件必填。图片的位置或用途。当type为'image_url'时需要指定。<br /><br />枚举值：<br />- first_frame：首帧图片<br />- last_frame：尾帧图片<br /><br />注意：<br />- 当使用首尾帧图生视频功能时，需传入2个image_url对象，1个role为first_frame，另一个role为last_frame<br />- 传入的首尾帧图片可相同<br />- 首尾帧图片的宽高比不一致时，以首帧图片为主，尾帧图片会自动裁剪适配<br />- 当使用首帧图生视频功能时，role填写first_frame或不填|

#### 枚举值

|属性|值|
|---|---|
|type|text|
|type|image_url|
|role|first_frame|
|role|last_frame|

<h2 id="tocS_JimengImageUrl">JimengImageUrl</h2>

<a id="schemajimengimageurl"></a>
<a id="schema_JimengImageUrl"></a>
<a id="tocSjimengimageurl"></a>
<a id="tocsjimengimageurl"></a>

```json
{
  "url": "string",
  "image_url": "string"
}

```

### 属性

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|url|string|false|none||图片的URL地址，支持HTTP/HTTPS协议。与image_url字段二选一使用。|
|image_url|string|false|none||图片的Base64编码数据，格式为data:image/[格式];base64,[数据]。与url字段二选一使用。|

anyOf

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|*anonymous*|object|false|none||none|

or

|名称|类型|必选|约束|中文名|说明|
|---|---|---|---|---|---|
|*anonymous*|object|false|none||none|

