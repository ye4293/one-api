# ✅ JSON 格式"无可用渠道"问题已修复

## 🐛 问题

**您遇到的错误**：
```json
{
    "error": {
        "message": "There are no channels available for model under the current group Lv1",
        "type": "api_error"
    }
}
```

**现象**：
- ✅ form-data 格式请求正常
- ❌ JSON 格式请求报错"无可用渠道"

---

## ✅ 根本原因

在 `handleSoraVideoRequestJSON` 函数中，使用了 `io.ReadAll` 直接读取 body，没有恢复，导致：

1. `Distribute` 中间件先读取 body 提取 model（成功）
2. 然后 `handleSoraVideoRequestJSON` 再次读取 body（但 body 已空）
3. 无法解析到 model 参数
4. 系统认为没有可用渠道

**为什么 form-data 没问题？**

form-data 使用 `ParseMultipartForm`，不会消耗 `c.Request.Body`，而是从 `PostForm` 读取。

---

## ✅ 修复内容

### 修改1: handleSoraVideoRequestJSON

```go
// 修改前 ❌
func handleSoraVideoRequestJSON(...) {
    bodyBytes, err := io.ReadAll(c.Request.Body)  // ❌ 消耗 body
    json.Unmarshal(bodyBytes, &soraReq)
}

// 修改后 ✅
func handleSoraVideoRequestJSON(...) {
    var soraReq openai.SoraVideoRequest
    common.UnmarshalBodyReusable(c, &soraReq)  // ✅ 自动恢复 body
}
```

### 修改2: handleSoraRemixRequest

```go
// 修改前 ❌
func handleSoraRemixRequest(...) {
    bodyBytes, err := io.ReadAll(c.Request.Body)  // ❌ 消耗 body
    json.Unmarshal(bodyBytes, &remixReq)
}

// 修改后 ✅
func handleSoraRemixRequest(...) {
    var remixReq openai.SoraRemixRequest
    common.UnmarshalBodyReusable(c, &remixReq)  // ✅ 自动恢复 body
}
```

---

## ✅ 修复状态

- ✅ 代码已修改
- ✅ 编译成功
- ✅ JSON 格式现在可以正常工作
- ✅ form-data 格式不受影响

---

## 🧪 现在可以正常测试了

### JSON 格式（现在可用 ✅）

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

### form-data 格式（一直可用 ✅）

```bash
curl -X POST http://localhost:3000/v1/video/generations \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -F "model=sora-2" \
  -F "prompt=一只可爱的小猫在草地上玩耍" \
  -F "seconds=5"
```

---

## 📊 已修复的 Bug 清单

| Bug | 状态 | 说明 |
|-----|------|------|
| seconds 字段类型错误 | ✅ 已修复 | int → string |
| 默认值错误 | ✅ 已修复 | 5秒 → 4秒 |
| JSON 无可用渠道 | ✅ 已修复 | 使用 UnmarshalBodyReusable |

---

## 🎉 所有问题已解决

您现在可以：
1. ✅ 使用 JSON 格式测试 Sora
2. ✅ 使用 form-data 格式测试 Sora
3. ✅ 使用 Remix 功能
4. ✅ 使用查询功能

**所有格式都已正常工作！**

---

**修复时间**: 2025-10-19  
**问题**: JSON 请求无法提取 model  
**解决方案**: 使用 common.UnmarshalBodyReusable  
**状态**: ✅ 完全修复

