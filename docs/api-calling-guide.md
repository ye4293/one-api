# API 调用指南

基础地址：`https://api.dataxxapi.com`

## 1. OpenAI 格式调用（不返回思考内容）

**请求地址：** `POST /v1/chat/completions`

**请求头：**

| Header | 值 |
|--------|---|
| `Content-Type` | `application/json` |
| `Authorization` | `Bearer sk-xxxxx` |

**curl 示例：**

```bash
curl -X POST https://api.dataxxapi.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-xxxxx" \
  -d '{
    "model": "stepfun/step-3.5-flash:free",
    "messages": [
        {
            "role": "user",
            "content": "You are a helpful assistant."
        },
        {
            "role": "user",
            "content": "Hello!"
        }
    ]
}'
```

## 2. OpenAI 格式调用（返回思考内容）

模型名称后添加 `-thinking` 后缀即可返回思考内容。

**请求地址：** `POST /v1/chat/completions`

**请求头：**

| Header | 值 |
|--------|---|
| `Content-Type` | `application/json` |
| `Authorization` | `Bearer sk-xxxxx` |

**curl 示例：**

```bash
curl -X POST https://api.dataxxapi.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-xxxxx" \
  -d '{
    "model": "stepfun/step-3.5-flash:free-thinking",
    "messages": [
        {
            "role": "user",
            "content": "You are a helpful assistant."
        },
        {
            "role": "user",
            "content": "Hello!"
        }
    ]
}'
```

## 3. Claude 格式调用（原生 thinking）

使用 Claude 原生消息格式，通过 `thinking` 参数控制思考功能。

**请求地址：** `POST /v1/messages`

**请求头：**

| Header | 值 |
|--------|---|
| `content-type` | `application/json` |
| `x-api-key` | `sk-xxxxx` |

**curl 示例：**

```bash
curl -X POST https://api.dataxxapi.com/v1/messages \
  -H "content-type: application/json" \
  -H "x-api-key: sk-xxxxx" \
  -d '{
    "model": "stepfun/step-3.5-flash:free",
    "max_tokens": 2000,
    "thinking": {
        "type": "enabled",
        "budget_tokens": 10000
    },
    "messages": [
        {
            "role": "user",
            "content": "Are there an infinite number of prime numbers such that n mod 4 == 3?"
        }
    ]
}'
```

### 参数说明

| 参数 | 说明 |
|------|------|
| `thinking.type` | 设置为 `enabled` 开启思考模式 |
| `thinking.budget_tokens` | 思考过程的最大 token 数 |
| `max_tokens` | 最终回复的最大 token 数 |
