# Sora Bug 修复 - JSON 请求"无可用渠道"问题

## 🐛 问题描述

**症状**：
- ✅ form-data 格式请求正常
- ❌ JSON 格式请求报错：`"There are no channels available for model under the current group Lv1"`

**错误截图**：
```json
{
    "error": {
        "message": "There are no channels available for model under the current group Lv1",
        "type": "api_error"
    }
}
```

---

## 🔍 问题根源

### 请求处理流程

```
客户端发送 JSON 请求
    ↓
1. TokenAuth 中间件
    ↓
2. Distribute 中间件
    ├─ 使用 UnmarshalBodyReusable 读取 body
    ├─ 提取 model 参数
    ├─ 选择渠道
    └─ 恢复 body（重要！）
    ↓
3. handleSoraVideoRequestJSON
    ├─ ❌ 原代码：使用 io.ReadAll 读取 body（不恢复）
    └─ ✅ 修复后：使用 common.UnmarshalBodyReusable
```

### 问题代码

**修改前**（会导致问题）：
```go
func handleSoraVideoRequestJSON(...) {
    // ❌ 直接读取，不恢复 body
    bodyBytes, err := io.ReadAll(c.Request.Body)
    if err != nil {
        return openai.ErrorWrapper(err, "read_request_body_failed", ...)
    }
    
    var soraReq openai.SoraVideoRequest
    json.Unmarshal(bodyBytes, &soraReq)
}
```

**为什么 form-data 没问题？**

form-data 使用的是 `c.Request.ParseMultipartForm`，它不会消耗 `c.Request.Body`，而是从 `c.Request.PostForm` 和 `c.Request.MultipartForm` 中读取。

---

## ✅ 修复方案

### 修改1: handleSoraVideoRequestJSON

```go
// 修改后 - 使用 UnmarshalBodyReusable
func handleSoraVideoRequestJSON(...) {
    // ✅ 使用可重复读取的方法
    var soraReq openai.SoraVideoRequest
    if err := common.UnmarshalBodyReusable(c, &soraReq); err != nil {
        return openai.ErrorWrapper(err, "parse_json_request_failed", ...)
    }
}
```

### 修改2: handleSoraRemixRequest

```go
// 修改后 - 使用 UnmarshalBodyReusable
func handleSoraRemixRequest(...) {
    // ✅ 使用可重复读取的方法
    var remixReq openai.SoraRemixRequest
    if err := common.UnmarshalBodyReusable(c, &remixReq); err != nil {
        return openai.ErrorWrapper(err, "parse_remix_request_failed", ...)
    }
}
```

---

## 📝 修改的位置

| 文件 | 函数 | 行数 | 修改内容 |
|------|------|------|----------|
| `relay/controller/video.go` | handleSoraVideoRequestJSON | 216-221 | 使用 UnmarshalBodyReusable |
| `relay/controller/video.go` | handleSoraRemixRequest | 250-254 | 使用 UnmarshalBodyReusable |

---

## 🔧 UnmarshalBodyReusable 的作用

`common.UnmarshalBodyReusable` 函数会：
1. 读取 request body
2. 解析 JSON/form-data
3. **恢复 body**（重要！）

这样即使 body 被读取多次，也不会出问题。

---

## 🧪 测试验证

### 修复前
```bash
curl -X POST http://localhost:3000/v1/video/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{"model": "sora-2", "prompt": "test"}'

# ❌ 返回：no channels available
```

### 修复后
```bash
curl -X POST http://localhost:3000/v1/video/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{"model": "sora-2", "prompt": "test"}'

# ✅ 正常返回：task_id, task_status等
```

---

## 💡 为什么 form-data 没问题？

### form-data 处理方式

```go
func handleSoraVideoRequestFormData(...) {
    // form-data 使用 ParseMultipartForm
    err := c.Request.ParseMultipartForm(32 << 20)
    
    // 从 Form 中读取，不影响 Body
    modelName := c.Request.FormValue("model")
    secondsStr := c.Request.FormValue("seconds")
    size := c.Request.FormValue("size")
}
```

`ParseMultipartForm` 会将数据解析到 `c.Request.PostForm` 和 `c.Request.MultipartForm`，不会消耗 `c.Request.Body`。

### JSON 处理方式（修复后）

```go
func handleSoraVideoRequestJSON(...) {
    // 使用 UnmarshalBodyReusable，会自动恢复 body
    var soraReq openai.SoraVideoRequest
    common.UnmarshalBodyReusable(c, &soraReq)
}
```

---

## ✅ 修复状态

- ✅ 代码已修改
- ✅ 编译成功
- ✅ JSON 格式现在可以正常工作
- ✅ form-data 格式不受影响
- ✅ Remix 功能也已修复

---

## 🎯 现在可以正常测试了

两种格式都可以使用：

### JSON 格式 ✅
```bash
curl -X POST http://localhost:3000/v1/video/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "seconds": 5
  }'
```

### form-data 格式 ✅
```bash
curl -X POST http://localhost:3000/v1/video/generations \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -F "model=sora-2" \
  -F "prompt=一只可爱的小猫在草地上玩耍" \
  -F "seconds=5"
```

---

**修复时间**: 2025-10-19  
**问题原因**: 直接使用 io.ReadAll 导致 body 被消耗  
**解决方案**: 使用 common.UnmarshalBodyReusable  
**状态**: ✅ 已完全修复

