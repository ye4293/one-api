# ✅ Sora自动禁用和重试问题已修复

## 问题描述

当 Sora API 返回错误（如 "Billing hard limit has been reached"）时：
- 虽然配置了自动禁用关键词 "Billing hard limit"
- 但是渠道没有被自动禁用
- 也没有触发重试逻辑

## 根本原因

在 `relay/controller/video.go` 的 `handleSoraVideoResponse` 函数中：

**修复前的问题逻辑：**
```go
func handleSoraVideoResponse(...) *model.ErrorWithStatusCode {
    if soraResponse.Error != nil {
        // 检测到错误
        taskStatus = "failed"
        message = fmt.Sprintf("Error: %s (type: %s, code: %s)", ...)
        logger.SysError(fmt.Sprintf("Sora video request failed: %s", message))
    }
    
    // 将错误信息返回给客户端
    generalResponse := model.GeneralVideoResponse{
        TaskStatus: taskStatus,  // "failed"
        Message:    message,      // 包含错误信息
    }
    c.Writer.Write(jsonResponse)
    
    return nil  // ❌ 问题：返回 nil 而不是错误对象
}
```

**问题链：**
1. `handleSoraVideoResponse` 返回 `nil`
2. `RelayVideoGenerate` 中 `bizErr == nil` 判断为true，认为请求成功
3. 不会调用 `processChannelRelayError`，所以不会检查自动禁用关键词
4. 不会调用 `shouldRetry`，所以不会触发重试逻辑

## 修复方案

修改 `handleSoraVideoResponse` 函数，当检测到错误时返回错误对象：

**修复后的正确逻辑：**
```go
func handleSoraVideoResponse(...) *model.ErrorWithStatusCode {
    // 情况1：soraResponse.Error != nil
    if soraResponse.Error != nil {
        logger.SysError(fmt.Sprintf("Sora video request failed: %s (type: %s, code: %s)", 
            soraResponse.Error.Message, soraResponse.Error.Type, soraResponse.Error.Code))
        
        // ✅ 返回错误对象
        return &model.ErrorWithStatusCode{
            Error: model.Error{
                Message: soraResponse.Error.Message,  // "Billing hard limit has been reached"
                Type:    soraResponse.Error.Type,      // "billing_limit_user_error"
                Code:    soraResponse.Error.Code,      // "billing_hard_limit_reached"
            },
            StatusCode: soraResponse.StatusCode,
        }
    }
    
    // 情况2：StatusCode == 200，成功
    if soraResponse.StatusCode == 200 {
        // ... 扣费逻辑 ...
        // ✅ 返回 nil 表示成功
        return nil
    }
    
    // 情况3：其他错误状态码
    // ✅ 返回错误对象
    return &model.ErrorWithStatusCode{
        Error: model.Error{
            Message: errMsg,
            Type:    errType,
            Code:    errCode,
        },
        StatusCode: soraResponse.StatusCode,
    }
}
```

## 修复效果

修复后，当 Sora 返回错误时：

1. **错误对象正确返回**
   - `RelayVideoGenerate` 收到 `bizErr != nil`
   - 触发错误处理流程

2. **自动禁用逻辑触发**
   ```
   processChannelRelayError() 被调用
     ↓
   util.ShouldDisableChannel() 检查错误
     ↓
   检查 err.Message 是否包含配置的关键词
     ↓
   "Billing hard limit has been reached" 包含 "Billing hard limit"
     ↓
   返回 true，渠道应该被禁用
     ↓
   monitor.DisableChannelWithStatusCode() 禁用渠道
   ```

3. **重试逻辑触发**
   ```
   shouldRetry() 检查是否应该重试
     ↓
   根据状态码判断是否重试
     ↓
   如果允许重试，选择其他渠道继续请求
   ```

## 关键词配置说明

自动禁用关键词配置位置：
- 系统设置 → 运维设置 → 自动禁用关键词
- 每行一个关键词
- 不区分大小写
- 支持部分匹配

示例配置：
```
Billing hard limit
insufficient_quota
account_deactivated
invalid_api_key
```

## 修改文件

- `relay/controller/video.go` 
  - **handleSoraVideoResponse 函数**（Sora视频生成）
    - 行 1032-1045：修复 `soraResponse.Error != nil` 的情况
    - 行 1083-1110：修复其他错误状态码的情况
  - **handleSoraRemixResponse 函数**（Sora视频Remix）
    - 行 361-374：修复 `soraResponse.Error != nil` 的情况
    - 行 411-438：修复其他错误状态码的情况

## 测试建议

1. 配置自动禁用关键词包含 "Billing hard limit"
2. 使用一个超出额度的 Sora API Key
3. 发送 Sora 视频生成请求
4. 验证：
   - 渠道被自动禁用
   - 请求自动重试到其他可用渠道
   - 终端日志显示重试过程

## 相关代码流程

```
客户端请求
  ↓
RelayVideoGenerate() [controller/relay.go:830]
  ↓
DoVideoRequest() → DoSoraRequest() → handleSoraVideoResponse()
  ↓
返回 ErrorWithStatusCode (非 nil)
  ↓
processChannelRelayError() [controller/relay.go:853]
  ↓
util.ShouldDisableChannel() [relay/util/common.go:23]
  ↓
检查关键词匹配 [relay/util/common.go:54-64]
  ↓
monitor.DisableChannelWithStatusCode()
  ↓
shouldRetry() 判断是否重试 [controller/relay.go:856]
  ↓
重试逻辑：选择其他渠道继续请求
```

## 注意事项

1. **此修复同时适用于所有 Sora 错误**，不仅限于额度错误
2. **状态码会被正确传递**，用于判断是否应该重试
3. **错误类型和代码会被保留**，用于更精确的错误处理
4. **只有真正成功的请求才返回 nil**，避免误判

## 日期

2025年10月20日

