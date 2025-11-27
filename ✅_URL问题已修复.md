# ✅ Sora URL 问题已修复

## 🐛 问题

OpenAI 返回错误：
```json
{
    "task_id": "",
    "task_status": "failed",
    "message": "Error: Invalid method for URL (POST /v1/videos/generations) (type: invalid_request_error, code: )"
}
```

---

## 🔍 问题原因

在 `sendRequestAndHandleSoraVideoResponseJSON` 函数中（处理 JSON 格式请求），URL 构建错误：

```go
// ❌ 错误的 URL（第 732 行）
fullRequestUrl := fmt.Sprintf("%s/v1/videos/generations", baseUrl)
```

而根据 OpenAI 官方文档，正确的地址应该是：
```go
// ✅ 正确的 URL
fullRequestUrl := fmt.Sprintf("%s/v1/videos", baseUrl)
```

**为什么 form-data 格式没问题？**

因为 `sendRequestAndHandleSoraVideoResponseFormData` 函数中使用的是正确的 URL：
```go
// ✅ form-data 使用的是正确地址（第 639 行）
fullRequestUrl := fmt.Sprintf("%s/v1/videos", baseUrl)
```

---

## ✅ 修复内容

### 修改位置

**文件**: `relay/controller/video.go`  
**行数**: 732  
**函数**: `sendRequestAndHandleSoraVideoResponseJSON`

### 修改前 ❌
```go
fullRequestUrl := fmt.Sprintf("%s/v1/videos/generations", baseUrl)
```

### 修改后 ✅
```go
fullRequestUrl := fmt.Sprintf("%s/v1/videos", baseUrl) // Sora 官方地址
```

---

## 📊 现在两个函数使用相同的正确地址

| 函数 | URL | 状态 |
|------|-----|------|
| sendRequestAndHandleSoraVideoResponseFormData | `/v1/videos` | ✅ 一直正确 |
| sendRequestAndHandleSoraVideoResponseJSON | `/v1/videos` | ✅ 已修复 |

---

## 🧪 现在可以正常测试了

### JSON 格式 ✅

```bash
curl -X POST http://localhost:3000/v1/video/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "seconds": 5,
    "size": "720x1280"
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

## 📝 所有已修复的 Bug

| # | Bug | 状态 | 说明 |
|---|-----|------|------|
| 1 | seconds 字段类型错误 | ✅ | int → string |
| 2 | 默认值错误 | ✅ | 5秒 → 4秒 |
| 3 | JSON 无可用渠道 | ✅ | 使用 UnmarshalBodyReusable |
| 4 | JSON URL 错误 | ✅ | /v1/videos/generations → /v1/videos |

---

## ✅ 编译状态

- ✅ 代码编译成功
- ✅ 无语法错误
- ✅ URL 已修正

---

## 🎉 现在 JSON 和 form-data 都可以正常使用了

请重新测试，应该可以正常工作了！

---

**修复时间**: 2025-10-20  
**问题**: URL 路径错误  
**状态**: ✅ 已完全修复

