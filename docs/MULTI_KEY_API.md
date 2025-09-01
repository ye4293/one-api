# 多Key聚合渠道 API 文档

## 概述

多Key聚合功能允许在单个渠道中管理多个API Key，支持轮询和随机两种选择模式，提供细粒度的Key状态管理和使用统计。

## 核心概念

### 1. 多Key模式
- **单Key模式**: 传统模式，一个渠道对应一个Key
- **多Key模式**: 聚合模式，一个渠道可包含多个Key

### 2. Key选择模式
- **轮询模式 (0)**: 按顺序循环使用Key
- **随机模式 (1)**: 随机选择可用Key

### 3. 批量导入模式
- **覆盖模式 (0)**: 替换所有现有Key
- **追加模式 (1)**: 添加到现有Key列表

## API 接口

### 1. 获取渠道Key统计信息

```http
GET /api/channel/{id}/keys/stats
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

### 2. 获取渠道Key详细信息

```http
GET /api/channel/{id}/keys/details
```

**响应示例:**
```json
{
  "success": true,
  "message": "",
  "data": {
    "channel_id": 1,
    "channel_name": "GPT-4渠道聚合",
    "is_multi_key": true,
    "selection_mode": 0,
    "total_keys": 3,
    "keys": [
      {
        "index": 0,
        "key": "sk-1234...abcd",
        "status": 1,
        "status_text": "已启用",
        "balance": 100.50,
        "usage": 1500,
        "last_used": 1640995200,
        "import_batch": "batch_1640995000",
        "note": ""
      },
      {
        "index": 1,
        "key": "sk-5678...efgh",
        "status": 2,
        "status_text": "手动禁用",
        "balance": 50.25,
        "usage": 800,
        "last_used": 1640994000,
        "import_batch": "batch_1640995000",
        "note": ""
      }
    ]
  }
}
```

### 3. 批量导入Keys

```http
POST /api/channel/keys/import
```

**请求体:**
```json
{
  "channel_id": 1,
  "keys": [
    "sk-1234567890abcdef1234567890abcdef",
    "sk-abcdef1234567890abcdef1234567890",
    "sk-9876543210fedcba9876543210fedcba"
  ],
  "mode": 0
}
```

**响应示例:**
```json
{
  "success": true,
  "message": "Successfully imported 3 keys",
  "data": {
    "imported_count": 3,
    "mode": 0
  }
}
```

### 4. 切换单个Key状态

```http
POST /api/channel/keys/toggle
```

**请求体:**
```json
{
  "channel_id": 1,
  "key_index": 2,
  "enabled": false
}
```

**响应示例:**
```json
{
  "success": true,
  "message": "Key disabled successfully"
}
```

### 5. 批量切换Keys状态

```http
POST /api/channel/keys/batch-toggle
```

**请求体:**
```json
{
  "channel_id": 1,
  "key_indices": [0, 2, 4],
  "enabled": true
}
```

**响应示例:**
```json
{
  "success": true,
  "message": "Successfully enabled 3 keys"
}
```

### 6. 按批次切换Keys状态

```http
POST /api/channel/keys/batch-toggle-by-batch
```

**请求体:**
```json
{
  "channel_id": 1,
  "batch_id": "batch_1640995000",
  "enabled": false
}
```

**响应示例:**
```json
{
  "success": true,
  "message": "Successfully disabled keys in batch batch_1640995000"
}
```

### 7. 更新渠道多Key设置

```http
PUT /api/channel/multi-key/settings
```

**请求体:**
```json
{
  "channel_id": 1,
  "is_multi_key": true,
  "key_selection_mode": 0
}
```

**响应示例:**
```json
{
  "success": true,
  "message": "Channel settings updated successfully",
  "data": {
    "is_multi_key": true,
    "key_selection_mode": 0
  }
}
```

## 数据结构

### Channel 扩展字段

```go
type Channel struct {
    // ... 现有字段 ...
    MultiKeyInfo MultiKeyInfo `json:"multi_key_info" gorm:"type:json"`
}

type MultiKeyInfo struct {
    IsMultiKey           bool                    `json:"is_multi_key"`
    KeyCount             int                     `json:"key_count"`
    KeySelectionMode     KeySelectionMode        `json:"key_selection_mode"`
    PollingIndex         int                     `json:"polling_index"`
    KeyStatusList        map[int]int             `json:"key_status_list"`
    KeyMetadata          map[int]KeyMetadata     `json:"key_metadata"`
    LastBatchImportTime  int64                   `json:"last_batch_import_time"`
    BatchImportMode      BatchImportMode         `json:"batch_import_mode"`
}
```

### Key状态码

| 状态码 | 状态名称 | 描述 |
|--------|----------|------|
| 1 | 已启用 | Key正常可用 |
| 2 | 手动禁用 | 管理员手动禁用 |
| 3 | 自动禁用 | 系统自动禁用（如余额不足、API错误等） |

### 选择模式

| 模式值 | 模式名称 | 描述 |
|--------|----------|------|
| 0 | 轮询模式 | 按顺序循环使用Key |
| 1 | 随机模式 | 随机选择可用Key |

## 使用场景

### 1. 基本设置
1. 创建渠道并启用多Key模式
2. 批量导入Keys
3. 设置选择模式（轮询/随机）
4. 开始使用

### 2. Key管理
1. 查看Key使用统计
2. 根据需要启用/禁用特定Key
3. 按导入批次管理Key
4. 追加新的Key

### 3. 监控和维护
1. 定期检查Key状态和余额
2. 根据使用情况调整Key配置
3. 处理失效或余额不足的Key

## 最佳实践

### 1. Key配置建议
- 建议每个聚合渠道包含3-10个Key
- 定期检查Key余额和状态
- 使用导入批次功能便于管理

### 2. 选择模式选择
- **轮询模式**: 适合负载均衡场景
- **随机模式**: 适合避免API限流的场景

### 3. 监控建议
- 定期查看Key使用统计
- 设置余额预警机制
- 监控Key的失败率

## 故障排除

### 常见问题

1. **所有Key都被禁用**
   - 检查Key余额和状态
   - 验证API Key的有效性
   - 查看错误日志

2. **轮询不均匀**
   - 检查Key的启用状态
   - 确认轮询索引正常

3. **导入失败**
   - 验证Key格式正确性
   - 检查渠道是否存在
   - 确认权限设置

## 安全注意事项

1. **Key保护**: API Key在传输和存储时都会被适当脱敏
2. **权限控制**: 所有API都需要管理员权限
3. **审计日志**: 所有Key操作都会被记录
4. **自动保护**: 异常Key会被自动禁用

---

**版本**: v1.0  
**更新时间**: 2024-01-15  
**作者**: OneAPI Team
