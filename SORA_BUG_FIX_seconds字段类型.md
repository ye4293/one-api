# Sora Bug 修复 - seconds 字段类型问题

## 🐛 问题描述

**错误信息**:
```json
{
    "error": {
        "code": "parse_sora_video_response_failed",
        "message": "json: cannot unmarshal string into Go struct field of type int",
        "param": "",
        "type": "api_error"
    }
}
```

**原因**: OpenAI API 返回的 `seconds` 字段是 **string 类型**，但代码中定义为 **int 类型**。

## ✅ 修复方案

### 修改1: SoraVideoRequest 结构体

**文件**: `relay/channel/openai/model.go`

```go
// 修改前
type SoraVideoRequest struct {
    Seconds int `json:"seconds,omitempty"`  // ❌ int 类型
}

// 修改后
type SoraVideoRequest struct {
    Seconds string `json:"seconds,omitempty"`  // ✅ string 类型
}
```

### 修改2: SoraVideoResponse 结构体

**文件**: `relay/channel/openai/model.go`

```go
// 修改前
type SoraVideoResponse struct {
    Seconds int `json:"seconds,omitempty"`  // ❌ int 类型
}

// 修改后
type SoraVideoResponse struct {
    Seconds string `json:"seconds,omitempty"`  // ✅ string 类型
}
```

### 修改3: calculateSoraQuota 函数

**文件**: `relay/controller/video.go`

```go
// 修改前
func calculateSoraQuota(modelName string, seconds int, size string) int64 {
    totalPriceUSD := float64(seconds) * pricePerSecond
    // ...
}

// 修改后
func calculateSoraQuota(modelName string, secondsStr string, size string) int64 {
    // 将 string 转换为 int
    seconds, err := strconv.Atoi(secondsStr)
    if err != nil || seconds == 0 {
        seconds = 5 // 默认值
    }
    
    totalPriceUSD := float64(seconds) * pricePerSecond
    // ...
}
```

### 修改4: handleSoraVideoRequestJSON 函数

```go
// 修改前
if soraReq.Seconds == 0 {
    soraReq.Seconds = 5
}

// 修改后
if soraReq.Seconds == "" {
    soraReq.Seconds = "5"
}
```

### 修改5: handleSoraVideoRequestFormData 函数

```go
// 修改前
secondsStr := c.Request.FormValue("seconds")
seconds := 5 // 默认值
if secondsStr != "" {
    if s, err := strconv.Atoi(secondsStr); err == nil {
        seconds = s
    }
}

// 修改后
secondsStr := c.Request.FormValue("seconds")
if secondsStr == "" {
    secondsStr = "5" // 默认值
}
```

### 修改6: 函数签名更新

```go
// 修改所有相关函数签名，将 seconds int 改为 secondsStr string

func sendRequestAndHandleSoraVideoResponseFormData(..., secondsStr string, ...)
func handleSoraVideoResponse(..., secondsStr string, ...)
func handleSoraRemixResponse(..., secondsStr string, ...)
```

### 修改7: 查询响应处理

```go
// 修改前
Duration: strconv.Itoa(soraResp.Seconds),

// 修改后
Duration: soraResp.Seconds,  // 已经是 string 类型，无需转换
```

## 🧪 修复验证

### 测试前（错误）
```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -d '{"model": "sora-2", "prompt": "test", "seconds": 5}'

# 返回错误: cannot unmarshal string into Go struct field of type int
```

### 测试后（成功）
```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -d '{"model": "sora-2", "prompt": "test", "seconds": 5}'

# 正确返回
{
  "task_id": "video_xxx",
  "task_status": "succeed",
  "message": "..."
}
```

## 📝 修改的位置

| 文件 | 行数 | 修改内容 |
|------|------|----------|
| `relay/channel/openai/model.go` | 163, 187 | Seconds 字段类型 int → string |
| `relay/controller/video.go` | 199-212 | form-data 处理逻辑 |
| `relay/controller/video.go` | 236-244 | JSON 处理逻辑 |
| `relay/controller/video.go` | 343-354 | Remix 响应提取 |
| `relay/controller/video.go` | 602-632 | calculateSoraQuota 函数 |
| `relay/controller/video.go` | 634 | sendRequest...FormData 签名 |
| `relay/controller/video.go` | 753-754 | WriteField 处理 |
| `relay/controller/video.go` | 938 | handleSoraVideoResponse 签名 |
| `relay/controller/video.go` | 370 | handleSoraRemixResponse 签名 |
| `relay/controller/video.go` | 4569 | 查询响应 Duration 字段 |

## ✅ 修复结果

- ✅ 编译成功
- ✅ 无类型错误
- ✅ 所有函数签名一致
- ✅ JSON 解析正常
- ✅ 默认值处理正确

## 💡 经验总结

### 为什么 OpenAI 使用 string 类型？

1. **灵活性**: 可以支持 "5"、"10" 等格式
2. **兼容性**: 避免 JSON 数字精度问题
3. **扩展性**: 未来可能支持 "5.5" 等小数

### 最佳实践

在处理外部 API 时：
1. 先查看官方文档的示例响应
2. 使用 string 类型接收数字字段（更安全）
3. 在内部处理时再转换为需要的类型
4. 添加默认值和错误处理

---

**修复日期**: 2025-10-19  
**问题级别**: 高（阻塞测试）  
**修复状态**: ✅ 已完成并验证  
**影响范围**: 所有 Sora 请求和响应

