# 自动禁用逻辑示例

## 📝 实际场景演示

### 场景1：单Key渠道API密钥无效

**原始情况**：
- 渠道：OpenAI-GPT4 (#123)
- 类型：单Key渠道
- 状态：启用

**错误发生**：
```
API响应：{
  "error": {
    "message": "Incorrect API key provided: sk-abc***def. You can find your API key at https://platform.openai.com/account/api-keys.",
    "type": "invalid_request_error",
    "code": "invalid_api_key"
  }
}
```

**系统处理**：
1. 调用 `monitor.DisableChannel(123, "OpenAI-GPT4", "Incorrect API key provided: sk-abc***def...")`
2. 更新数据库：
   ```sql
   UPDATE channels SET 
     status = 3,
     auto_disabled_reason = 'Incorrect API key provided: sk-abc***def. You can find your API key at https://platform.openai.com/account/api-keys.',
     auto_disabled_time = 1705307400
   WHERE id = 123;
   ```
3. 发送邮件通知

**前端显示**：
```
渠道列表：
| 名称          | 状态     | 禁用原因                           |
|---------------|----------|-----------------------------------|
| OpenAI-GPT4   | 自动禁用 | Incorrect API key provided: sk... |
```

---

### 场景2：多Key渠道部分Key失效

**原始情况**：
- 渠道：OpenAI-Multi (#456)
- 类型：多Key渠道
- Key数量：3个
- 状态：启用

**第一次错误**（Key #1失效）：
```
API响应：{
  "error": {
    "message": "You exceeded your current quota, please check your plan and billing details.",
    "type": "insufficient_quota",
    "code": "quota_exceeded"
  }
}
```

**系统处理**：
1. 调用 `channel.HandleKeyError(1, "You exceeded your current quota...", 429)`
2. 更新 Key #1 状态：
   ```json
   {
     "key_status_list": {"1": 3},
     "key_metadata": {
       "1": {
         "disabled_reason": "You exceeded your current quota, please check your plan and billing details.",
         "disabled_time": 1705307400,
         "status_code": 429
       }
     }
   }
   ```
3. 检查：还有Key #0和#2可用，渠道继续运行
4. 发送Key级别禁用邮件

**前端显示**：
```
渠道列表：
| 名称          | 状态 | 禁用原因 |
|---------------|------|----------|
| OpenAI-Multi  | 启用 | -        |

多Key管理页面：
| Key索引 | 状态     | 禁用原因                        | 禁用时间        |
|---------|----------|--------------------------------|----------------|
| Key #0  | 启用     | -                              | -              |
| Key #1  | 自动禁用 | You exceeded your current quota | 2024-01-15 14:30 |
| Key #2  | 启用     | -                              | -              |
```

---

### 场景3：多Key渠道所有Key都被禁用

**继续上面的场景，Key #0和#2也相继失效**

**Key #0失效**：
```
API错误：Incorrect API key provided
```

**Key #2失效**：
```
API错误：API key not valid
```

**系统处理**：
1. Key #0被禁用后，更新状态
2. Key #2被禁用后：
   - 检测到所有Key都已禁用
   - 调用 `checkAndUpdateChannelStatus()`
   - 设置渠道级别禁用：
     ```sql
     UPDATE channels SET 
       status = 3,
       auto_disabled_reason = 'all keys disabled',
       auto_disabled_time = 1705310800
     WHERE id = 456;
     ```
3. 发送渠道完全禁用邮件

**前端显示**：
```
渠道列表：
| 名称          | 状态     | 禁用原因          |
|---------------|----------|-------------------|
| OpenAI-Multi  | 自动禁用 | all keys disabled |

多Key管理页面：
| Key索引 | 状态     | 禁用原因                        | 禁用时间        |
|---------|----------|--------------------------------|----------------|
| Key #0  | 自动禁用 | Incorrect API key provided     | 2024-01-15 14:45 |
| Key #1  | 自动禁用 | You exceeded your current quota | 2024-01-15 14:30 |
| Key #2  | 自动禁用 | API key not valid              | 2024-01-15 15:00 |
```

---

## 🔍 数据查询示例

### 查看单Key渠道禁用原因
```sql
SELECT 
  id, name, status,
  auto_disabled_reason,
  FROM_UNIXTIME(auto_disabled_time) as disabled_time
FROM channels 
WHERE auto_disabled_reason IS NOT NULL 
  AND JSON_EXTRACT(multi_key_info, '$.is_multi_key') IS NULL;
```

### 查看多Key渠道状态
```sql
SELECT 
  id, name, status,
  auto_disabled_reason,
  JSON_EXTRACT(multi_key_info, '$.key_count') as total_keys,
  JSON_EXTRACT(multi_key_info, '$.key_status_list') as key_status,
  JSON_EXTRACT(multi_key_info, '$.key_metadata') as key_metadata
FROM channels 
WHERE JSON_EXTRACT(multi_key_info, '$.is_multi_key') = true;
```

### 查看所有自动禁用的原因统计
```sql
-- 单Key渠道禁用原因统计
SELECT auto_disabled_reason, COUNT(*) as count
FROM channels 
WHERE auto_disabled_reason IS NOT NULL 
  AND JSON_EXTRACT(multi_key_info, '$.is_multi_key') IS NULL
GROUP BY auto_disabled_reason;

-- 多Key渠道禁用原因统计（需要应用层处理JSON）
SELECT 
  CASE 
    WHEN auto_disabled_reason = 'all keys disabled' THEN 'Channel: all keys disabled'
    ELSE 'Individual key errors'
  END as disable_type,
  COUNT(*) as count
FROM channels 
WHERE JSON_EXTRACT(multi_key_info, '$.is_multi_key') = true
  AND status = 3
GROUP BY disable_type;
```

---

## 📧 邮件通知示例

### 单Key渠道禁用邮件
```
主题：渠道「OpenAI-GPT4」（#123）已被禁用

内容：
渠道自动禁用通知

渠道名称：OpenAI-GPT4
渠道ID：#123
禁用原因：Incorrect API key provided: sk-abc***def. You can find your API key at https://platform.openai.com/account/api-keys.
禁用时间：2024-01-15 14:30:00

该渠道因出现错误已被系统自动禁用，请检查渠道配置和密钥的有效性。
```

### Key级别禁用邮件
```
主题：多Key渠道「OpenAI-Multi」（#456）中的Key已被禁用

内容：
多Key渠道Key自动禁用通知

渠道名称：OpenAI-Multi
渠道ID：#456
被禁用的Key：Key #1 (sk-abc***def)
禁用原因：You exceeded your current quota, please check your plan and billing details.
状态码：429
禁用时间：2024-01-15 14:30:00

该Key因出现错误已被系统自动禁用，请检查Key的有效性。如果所有Key都被禁用，整个渠道也将被禁用。
```

### 渠道完全禁用邮件
```
主题：多Key渠道「OpenAI-Multi」（#456）已被完全禁用

内容：
多Key渠道完全禁用通知

渠道名称：OpenAI-Multi
渠道ID：#456
禁用原因：all keys disabled
禁用时间：2024-01-15 15:00:00

该渠道的所有Key都已被禁用，因此整个渠道已被系统自动禁用。请检查并修复所有Key的问题后重新启用。
```

这样的实现完全符合您的预期：
✅ 单Key直接禁用显示具体原因
✅ 多Key在管理页面显示每个Key的原因  
✅ 全Key禁用时显示"all keys disabled"
