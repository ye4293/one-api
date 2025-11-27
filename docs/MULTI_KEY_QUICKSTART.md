# 多Key聚合渠道 - 快速入门

## 1. 启用多Key功能

### 步骤1: 更新现有渠道
```bash
curl -X PUT "http://localhost:3000/api/channel/multi-key/settings" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "is_multi_key": true,
    "key_selection_mode": 0
  }'
```

## 2. 批量导入API Keys

### 步骤2: 导入多个Key
```bash
curl -X POST "http://localhost:3000/api/channel/keys/import" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "keys": [
      "sk-1234567890abcdef1234567890abcdef",
      "sk-abcdef1234567890abcdef1234567890",
      "sk-9876543210fedcba9876543210fedcba",
      "sk-fedcba0987654321fedcba0987654321",
      "sk-1111222233334444555566667777888"
    ],
    "mode": 0
  }'
```

**响应示例:**
```json
{
  "success": true,
  "message": "Successfully imported 5 keys",
  "data": {
    "imported_count": 5,
    "mode": 0
  }
}
```

## 3. 查看Key状态

### 步骤3: 检查导入结果
```bash
curl -X GET "http://localhost:3000/api/channel/1/keys/details" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN"
```

**响应示例:**
```json
{
  "success": true,
  "message": "",
  "data": {
    "channel_id": 1,
    "channel_name": "GPT-4聚合渠道",
    "is_multi_key": true,
    "selection_mode": 0,
    "total_keys": 5,
    "keys": [
      {
        "index": 0,
        "key": "sk-1234...cdef",
        "status": 1,
        "status_text": "已启用",
        "balance": 0,
        "usage": 0,
        "last_used": 0,
        "import_batch": "batch_1703515200",
        "note": ""
      }
      // ... 其他4个Key
    ]
  }
}
```

## 4. 测试Key轮询

### 步骤4: 发送测试请求
现在渠道会自动在5个Key之间轮询：

```bash
# 第一次请求 - 使用Key 0
curl -X POST "http://localhost:3000/v1/chat/completions" \
  -H "Authorization: Bearer YOUR_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# 第二次请求 - 使用Key 1
curl -X POST "http://localhost:3000/v1/chat/completions" \
  -H "Authorization: Bearer YOUR_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo", 
    "messages": [{"role": "user", "content": "How are you?"}]
  }'
```

## 5. 管理Key状态

### 步骤5: 禁用问题Key
如果某个Key出现问题，可以快速禁用：

```bash
curl -X POST "http://localhost:3000/api/channel/keys/toggle" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "key_index": 2,
    "enabled": false
  }'
```

### 步骤6: 查看使用统计
```bash
curl -X GET "http://localhost:3000/api/channel/1/keys/stats" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN"
```

**响应示例:**
```json
{
  "success": true,
  "message": "",
  "data": {
    "total_keys": 5,
    "enabled_keys": 4,
    "disabled_keys": 1,
    "is_multi_key": true,
    "selection_mode": 0
  }
}
```

## 6. 追加新Key

### 步骤7: 动态添加Key
```bash
curl -X POST "http://localhost:3000/api/channel/keys/import" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "keys": [
      "sk-newkey1111222233334444555566667",
      "sk-newkey2222333344445555666677778"
    ],
    "mode": 1
  }'
```

## 7. 批量管理

### 步骤8: 批量操作Key
```bash
# 批量禁用多个Key
curl -X POST "http://localhost:3000/api/channel/keys/batch-toggle" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "key_indices": [3, 4, 5],
    "enabled": false
  }'

# 按批次启用Key
curl -X POST "http://localhost:3000/api/channel/keys/batch-toggle-by-batch" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "batch_id": "batch_1703515200",
    "enabled": true
  }'
```

## 典型场景

### 场景1: 高并发负载均衡
```bash
# 设置为轮询模式，实现负载均衡
curl -X PUT "http://localhost:3000/api/channel/multi-key/settings" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "is_multi_key": true,
    "key_selection_mode": 0
  }'
```

### 场景2: 避免限流
```bash
# 设置为随机模式，避免API限流
curl -X PUT "http://localhost:3000/api/channel/multi-key/settings" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "is_multi_key": true,
    "key_selection_mode": 1
  }'
```

### 场景3: 故障恢复
```bash
# 快速禁用故障Key
curl -X POST "http://localhost:3000/api/channel/keys/toggle" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "key_index": 0,
    "enabled": false
  }'

# 添加新的替换Key
curl -X POST "http://localhost:3000/api/channel/keys/import" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": 1,
    "keys": ["sk-replacement-key-here"],
    "mode": 1
  }'
```

## 监控脚本示例

### Python监控脚本
```python
import requests
import time

def check_channel_health(channel_id, admin_token):
    """检查渠道健康状态"""
    headers = {"Authorization": f"Bearer {admin_token}"}
    
    # 获取Key统计
    response = requests.get(
        f"http://localhost:3000/api/channel/{channel_id}/keys/stats",
        headers=headers
    )
    
    if response.status_code == 200:
        data = response.json()["data"]
        print(f"渠道 {channel_id} 状态:")
        print(f"  总Key数: {data['total_keys']}")
        print(f"  可用Key: {data['enabled_keys']}")
        print(f"  禁用Key: {data['disabled_keys']}")
        
        # 如果可用Key少于总数的50%，发出警告
        if data['enabled_keys'] < data['total_keys'] * 0.5:
            print("⚠️  警告: 可用Key数量过少!")
            
        return data
    else:
        print(f"❌ 检查失败: {response.text}")
        return None

# 使用示例
if __name__ == "__main__":
    ADMIN_TOKEN = "your_admin_token_here"
    CHANNEL_ID = 1
    
    while True:
        check_channel_health(CHANNEL_ID, ADMIN_TOKEN)
        time.sleep(300)  # 每5分钟检查一次
```

---

**恭喜!** 🎉 你已经成功设置了多Key聚合渠道。现在你可以：

- ✅ 管理多个API Key
- ✅ 实现负载均衡
- ✅ 快速处理故障Key
- ✅ 监控使用情况
- ✅ 动态扩展容量

有问题？查看 [完整API文档](./MULTI_KEY_API.md) 或提交Issue。
