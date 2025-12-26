# Kling API 认证机制说明

## 认证方式

Kling API 使用 **JWT (JSON Web Token)** 进行认证，而不是简单的 API Key。

### JWT Token 生成

系统会自动将 Kling 渠道的 AK (Access Key) 和 SK (Secret Key) 转换为 JWT Token：

```go
claims := jwt.MapClaims{
    "iss": ak,                                      // Issuer: Access Key
    "exp": time.Now().Add(30 * time.Minute).Unix(), // 过期时间：30分钟
    "nbf": time.Now().Add(-5 * time.Second).Unix(), // 生效时间：提前5秒
}

token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
tokenString := token.SignedString([]byte(sk)) // 使用 SK 签名
```

## 渠道配置方式

### 方式 1: Key 字段（推荐）

在渠道的 **Key** 字段中填入 `AK|SK` 格式：

```
your_access_key|your_secret_key
```

**示例**：
```
kl_abc123def456|sk_xyz789uvw012
```

**优点**：
- 支持多密钥轮换
- 格式简洁清晰
- 自动解析和验证

### 方式 2: Config 字段（向后兼容）

在渠道的 **Config** 字段中配置 JSON：

```json
{
  "AK": "your_access_key",
  "SK": "your_secret_key"
}
```

**说明**：系统会优先尝试从 Key 字段解析，如果失败则回退到 Config 字段。

## 认证流程

### 1. 请求进入

```
客户端请求 -> TokenAuth 中间件 -> Distribute 中间件
```

### 2. Distribute 中间件处理

```go
// 1. 选择渠道
channel := model.CacheGetRandomSatisfiedChannel(userGroup, model, 0)

// 2. 获取 Key（支持多密钥）
actualKey, keyIndex := channel.GetNextAvailableKey()

// 3. 针对 Kling 渠道特殊处理
if channel.Type == common.ChannelTypeKeling {
    // 解析 AK|SK
    credentials := keling.GetKelingCredentialsFromConfig(cfg, channel, keyIndex)
    
    // 生成 JWT Token
    token := encodeKlingJWTToken(credentials.AK, credentials.SK)
    
    // 设置 Authorization Header
    c.Request.Header.Set("Authorization", "Bearer " + token)
}
```

### 3. 请求转发

```
Distribute 中间件 -> Kling Adaptor -> Kling API
```

Adaptor 会使用已设置好的 Authorization Header（包含 JWT Token）。

## 多密钥支持

Kling 渠道支持多密钥配置，格式如下：

```
ak1|sk1
ak2|sk2
ak3|sk3
```

系统会：
1. 自动轮换使用不同的密钥对
2. 当某个密钥失败时，自动切换到下一个
3. 记录每个密钥的使用情况

## 配置示例

### 管理后台配置

1. **渠道类型**: 选择 "Keling" (ChannelType=41)
2. **渠道名称**: Kling AI
3. **Base URL**: `https://api.klingai.com` 或 `https://api-singapore.klingai.com`
4. **Key**: `your_ak|your_sk`
5. **模型列表**: `kling-v1-5-std,kling-v1-5-pro,kling-v1-6-std,kling-v1-6-pro`

### 数据库配置

```sql
INSERT INTO channels (
    type, 
    name, 
    key, 
    status, 
    base_url, 
    models, 
    created_time
) VALUES (
    41,                                                    -- ChannelTypeKeling
    'Kling AI', 
    'kl_abc123def456|sk_xyz789uvw012',                   -- AK|SK 格式
    1,                                                     -- 启用
    'https://api.klingai.com', 
    'kling-v1-5-std,kling-v1-5-pro,kling-v1-6-std,kling-v1-6-pro',
    UNIX_TIMESTAMP()
);
```

### 多密钥配置

```sql
UPDATE channels 
SET key = 'ak1|sk1
ak2|sk2
ak3|sk3'
WHERE id = YOUR_CHANNEL_ID;
```

## 错误处理

### 常见错误

1. **Invalid AK|SK format**
   - 原因：Key 格式不正确
   - 解决：确保格式为 `ak|sk`，中间用竖线分隔

2. **Failed to generate JWT token**
   - 原因：SK 签名失败
   - 解决：检查 SK 是否正确

3. **AccessKey/SecretKey is empty**
   - 原因：AK 或 SK 为空
   - 解决：检查配置是否完整

4. **Invalid credentials**
   - 原因：凭证验证失败
   - 解决：检查 AK/SK 长度和格式

### 降级策略

如果 JWT Token 生成失败，系统会：
1. 记录错误日志
2. 使用原始 Key 作为 Bearer Token（降级方案）
3. 继续处理请求

## 日志示例

### 成功日志

```
[Keling] 从Key字段获取凭证 - 多密钥模式: true
[Keling] 成功解析凭证 - AK: kl_a***
Kling JWT token generated for channel 123, AK: kl_a***
channel:123;requestModel:kling-v1-5-std;keyIndex:0;maskedKey:kl_a***xyz
```

### 错误日志

```
[Keling] Key字段解析失败: invalid AK|SK format，尝试Config回退
Failed to get Kling credentials for channel 123: 无法从Key字段或Config获取有效的可灵凭证
Invalid Kling key format for channel 123, expected 'ak|sk'
```

## 安全建议

1. **保护密钥**
   - 不要在日志中输出完整的 AK/SK
   - 使用脱敏显示（如 `kl_a***xyz`）

2. **定期轮换**
   - 定期更换 AK/SK
   - 使用多密钥配置实现无缝切换

3. **监控使用**
   - 监控每个密钥的使用频率
   - 及时发现异常调用

4. **访问控制**
   - 限制数据库访问权限
   - 使用环境变量存储敏感配置

## 测试验证

### 测试 JWT Token 生成

```bash
# 使用 curl 测试（需要先获取 token）
curl -X POST https://your-domain.com/kling/v1/videos/text2video \
  -H "Authorization: Bearer YOUR_ONE_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "kling-v1-5-std",
    "prompt": "测试视频",
    "duration": 5
  }'
```

### 验证日志

```bash
# 查看 Kling 认证相关日志
tail -f logs/oneapi.log | grep -i "kling\|jwt"

# 查看特定渠道的日志
grep "channel:YOUR_CHANNEL_ID" logs/oneapi.log
```

## 故障排查

### 问题 1: 401 Unauthorized

**可能原因**：
- AK/SK 不正确
- JWT Token 过期
- 签名算法不匹配

**排查步骤**：
1. 检查 Key 字段格式
2. 查看日志中的错误信息
3. 验证 AK/SK 是否有效
4. 测试 JWT Token 生成

### 问题 2: Token 生成失败

**可能原因**：
- SK 格式错误
- 缺少 JWT 库依赖

**排查步骤**：
1. 检查 `github.com/golang-jwt/jwt` 是否安装
2. 验证 SK 是否包含特殊字符
3. 查看详细错误日志

### 问题 3: 多密钥不生效

**可能原因**：
- Key 字段格式错误
- 换行符处理问题

**排查步骤**：
1. 确保每个密钥对占一行
2. 检查是否有多余的空格
3. 验证每个密钥对格式正确

## 参考资源

- [Kling API 官方文档](https://app.klingai.com/cn/dev/document-api)
- [JWT 标准规范](https://jwt.io/)
- [Go JWT 库文档](https://github.com/golang-jwt/jwt)

---

**最后更新**: 2025-12-25

