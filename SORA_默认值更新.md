# Sora 默认值更新 - seconds 默认 4 秒

## ✅ 更新内容

根据 OpenAI 官方文档，Sora 的 `seconds` 默认值是 **4 秒**（不是 5 秒）。

已将所有默认值从 `"5"` 更新为 `"4"`。

---

## 📝 修改位置

### 1. handleSoraVideoRequestFormData
```go
// 第 199-202 行
secondsStr := c.Request.FormValue("seconds")
if secondsStr == "" {
    secondsStr = "4" // ✅ 默认值 - Sora 官方默认 4 秒
}
```

### 2. handleSoraVideoRequestJSON
```go
// 第 233-235 行
if soraReq.Seconds == "" {
    soraReq.Seconds = "4" // ✅ 默认值 - Sora 官方默认 4 秒
}
```

### 3. handleSoraRemixRequest (响应提取)
```go
// 第 340-343 行
secondsStr := soraResponse.Seconds
if secondsStr == "" {
    secondsStr = "4" // ✅ 默认时长 - Sora 官方默认 4 秒
}
```

### 4. calculateSoraQuota
```go
// 第 615-619 行
seconds, err := strconv.Atoi(secondsStr)
if err != nil || seconds == 0 {
    seconds = 4 // ✅ 默认值 - Sora 官方默认 4 秒
    log.Printf("Invalid seconds value '%s', using default 4", secondsStr)
}
```

---

## 💰 定价影响

### sora-2
- **默认费用**: 4 秒 × $0.10/秒 = **$0.40**（之前是 $0.50）

### sora-2-pro（标准分辨率）
- **默认费用**: 4 秒 × $0.30/秒 = **$1.20**（之前是 $1.50）

### sora-2-pro（高清分辨率）
- **默认费用**: 4 秒 × $0.50/秒 = **$2.00**（之前是 $2.50）

---

## 📋 使用示例

### 不指定 seconds 参数

```bash
# 会使用默认值 4 秒
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍"
  }'

# 费用: $0.40（4秒 × $0.10）
```

### 明确指定 seconds 参数

```bash
# 使用指定的秒数
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "seconds": 10
  }'

# 费用: $1.00（10秒 × $0.10）
```

---

## ✅ 验证

- ✅ 代码编译成功
- ✅ 所有默认值已更新为 4
- ✅ 日志输出正确
- ✅ 计费逻辑正确

---

**更新时间**: 2025-10-19  
**修改位置**: 4 处  
**状态**: ✅ 已完成

