# 自动禁用原因存储功能说明

## 功能概述

本功能为单Key渠道和多Key渠道的自动禁用增加了详细的原因记录和邮件通知功能。

## 主要改进

### 1. 单Key渠道改进

**新增字段：**
- `auto_disabled_reason`: 存储自动禁用的详细原因
- `auto_disabled_time`: 存储禁用的时间戳

**改进的邮件通知：**
- 增加了HTML格式的邮件模板
- 包含禁用原因、时间等详细信息
- 提供更好的用户体验

### 2. 多Key渠道改进

**KeyMetadata扩展：**
```go
type KeyMetadata struct {
    // 原有字段...
    DisabledReason *string `json:"disabled_reason"`   // 禁用原因
    DisabledTime   *int64  `json:"disabled_time"`     // 禁用时间戳  
    StatusCode     *int    `json:"status_code"`       // HTTP状态码
}
```

**新增邮件通知：**
- 单个Key被禁用时发送专门的邮件通知
- 包含Key索引、脱敏Key、错误原因、状态码等信息
- 通过异步通道机制避免循环导入

### 3. 系统架构改进

**通知机制：**
- 新增`KeyDisableNotificationChan`通道处理Key禁用通知
- 在系统启动时启动`StartKeyNotificationListener()`监听器
- 避免了包之间的循环依赖

## 部署步骤

### 1. 数据库迁移

执行`migration_auto_disable_reason.sql`脚本：

```sql
-- 添加新字段
ALTER TABLE channels ADD COLUMN auto_disabled_reason TEXT NULL;
ALTER TABLE channels ADD COLUMN auto_disabled_time BIGINT NULL;

-- 创建索引
CREATE INDEX idx_channels_auto_disabled_time ON channels(auto_disabled_time);
CREATE INDEX idx_channels_auto_disabled_reason ON channels(auto_disabled_reason(255));
```

### 2. 代码部署

直接部署修改后的代码，系统会自动启动新的通知监听器。

### 3. 功能验证

**验证单Key渠道：**
1. 配置一个无效的API Key
2. 发送请求触发错误
3. 检查渠道是否被禁用并记录了原因
4. 检查是否收到邮件通知

**验证多Key渠道：**
1. 配置多Key渠道，其中包含无效Key
2. 发送请求触发特定Key错误
3. 检查该Key是否被禁用并记录了原因
4. 检查是否收到Key禁用邮件通知

## 配置要求

### 邮件通知配置

确保以下邮件配置正确：

```go
// 在环境变量或配置文件中设置
SMTP_SERVER=your_smtp_server
SMTP_PORT=587
SMTP_ACCOUNT=your_email@domain.com  
SMTP_TOKEN=your_email_password
SMTP_FROM=your_email@domain.com
ROOT_USER_EMAIL=admin@domain.com
```

### 自动禁用开关

确保自动禁用功能已启用：

```go
// 全局开关
AutomaticDisableChannelEnabled = true

// 渠道级别开关（每个渠道的auto_disabled字段）
channel.AutoDisabled = true
```

## 使用说明

### 查看禁用原因

**单Key渠道：**
```sql
SELECT 
    id, name, status, 
    auto_disabled_reason, 
    FROM_UNIXTIME(auto_disabled_time) as disabled_time
FROM channels 
WHERE auto_disabled_reason IS NOT NULL;
```

**多Key渠道：**
多Key渠道的Key禁用原因存储在`multi_key_info`字段的JSON数据中，可以通过管理界面查看，或使用以下SQL查询：

```sql
SELECT 
    id, name,
    JSON_EXTRACT(multi_key_info, '$.key_metadata') as key_metadata
FROM channels 
WHERE JSON_EXTRACT(multi_key_info, '$.is_multi_key') = true;
```

### 邮件通知内容

**单Key渠道禁用通知：**
- 渠道名称和ID
- 禁用原因
- 禁用时间
- HTML格式，便于阅读

**多Key渠道Key禁用通知：**
- 渠道名称和ID
- 被禁用的Key索引和脱敏Key
- 禁用原因和HTTP状态码
- 禁用时间
- 提醒检查Key有效性

## 故障排除

### 1. 邮件通知不工作

检查项：
- SMTP配置是否正确
- ROOT_USER_EMAIL是否设置
- 网络连接是否正常
- 查看系统日志中的错误信息

### 2. 禁用原因没有记录

检查项：
- 数据库迁移是否执行成功
- AutomaticDisableChannelEnabled是否为true
- 渠道的auto_disabled字段是否为true

### 3. 多Key通知不工作

检查项：
- 系统启动日志中是否有"key disable notification listener started"
- 查看系统日志中是否有Key禁用的记录
- 检查KeyDisableNotificationChan是否正常工作

## 性能影响

- 新增字段对数据库性能影响极小
- 邮件发送使用异步goroutine，不影响API响应速度
- 通知通道使用缓冲区，避免阻塞
- 索引优化查询性能

## 兼容性

- 向后兼容，不影响现有功能
- 新字段允许NULL值，兼容旧数据
- 渐进式功能，可以逐步启用

## 监控建议

建议监控以下指标：
- 自动禁用频率
- 邮件发送成功率  
- Key健康状态
- 错误原因分布

通过这些改进，系统现在能够：
1. 详细记录每次自动禁用的原因和时间
2. 为单Key和多Key渠道分别发送专门的邮件通知
3. 提供更好的故障诊断能力
4. 改善运维体验
