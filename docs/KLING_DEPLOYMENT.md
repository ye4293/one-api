# Kling API 部署指南

## 部署前准备

### 1. 系统要求

- Go 版本 >= 1.18
- MySQL 或 SQLite 数据库
- Redis（可选，用于缓存）
- 公网可访问的服务器（用于接收回调）

### 2. 获取 Kling API 密钥

1. 访问可灵开放平台：https://app.klingai.com/cn/dev
2. 注册并登录账号
3. 创建应用并获取 API Key

## 部署步骤

### 步骤 1: 更新代码

确保所有 Kling 相关文件已正确添加到项目中：

```bash
# 检查文件是否存在
ls -la relay/channel/kling/
ls -la relay/controller/kling_video.go
ls -la docs/KLING_API_GUIDE.md
```

### 步骤 2: 编译项目

```bash
# 进入项目根目录
cd /path/to/one-api

# 编译项目
go build -o one-api

# 或使用 make（如果有 Makefile）
make build
```

### 步骤 3: 配置数据库

确保 `videos` 表已创建。如果是新部署，系统会自动创建表结构。

检查表结构：

```sql
DESCRIBE videos;
```

应包含以下字段：
- task_id (主键)
- video_id
- user_id
- channel_id
- model
- provider
- type
- status
- quota
- prompt
- duration
- store_url
- fail_reason
- created_at

### 步骤 4: 配置环境变量

创建或编辑 `.env` 文件：

```bash
# 服务器地址（用于生成回调 URL）
SERVER_ADDRESS=https://your-domain.com

# 数据库配置
SQL_DSN=root:password@tcp(localhost:3306)/oneapi?charset=utf8mb4&parseTime=True&loc=Local

# Redis 配置（可选）
REDIS_CONN_STRING=redis://localhost:6379

# 日志级别
LOG_LEVEL=info
```

### 步骤 5: 添加 Kling 渠道

#### 方式 1: 通过管理后台添加

1. 启动服务：`./one-api`
2. 访问管理后台：`http://localhost:3000`
3. 登录管理员账号
4. 进入"渠道管理"页面
5. 点击"添加渠道"
6. 填写以下信息：
   - **渠道名称**: Kling AI
   - **渠道类型**: 选择 "Keling" (ChannelType=41)
   - **Base URL**: `https://api.klingai.com` 或 `https://api-singapore.klingai.com`
   - **API Key**: 粘贴从可灵平台获取的密钥
   - **模型列表**: `kling-v1-5-std,kling-v1-5-pro,kling-v1-6-std,kling-v1-6-pro`
   - **状态**: 启用

#### 方式 2: 通过 SQL 直接插入

```sql
INSERT INTO channels (
    type, name, key, status, base_url, models, created_time
) VALUES (
    41, 
    'Kling AI', 
    'YOUR_API_KEY_HERE', 
    1, 
    'https://api.klingai.com', 
    'kling-v1-5-std,kling-v1-5-pro,kling-v1-6-std,kling-v1-6-pro',
    UNIX_TIMESTAMP()
);
```

### 步骤 6: 配置回调域名

确保 `SERVER_ADDRESS` 环境变量配置正确，并且：

1. 域名可公网访问
2. 防火墙已开放相应端口（默认 3000）
3. 如使用 HTTPS，确保证书有效

测试回调 URL 可访问性：

```bash
curl https://your-domain.com/kling/callback/test
# 应返回 404 或其他响应，而不是连接超时
```

### 步骤 7: 启动服务

```bash
# 前台运行（用于测试）
./one-api

# 后台运行
nohup ./one-api > logs/oneapi.out 2>&1 &

# 使用 systemd（推荐）
sudo systemctl start one-api
sudo systemctl enable one-api
```

### 步骤 8: 验证部署

#### 8.1 检查服务状态

```bash
# 查看进程
ps aux | grep one-api

# 查看日志
tail -f logs/oneapi.log

# 查看端口监听
netstat -tlnp | grep 3000
```

#### 8.2 测试 API 接口

```bash
# 获取 Token（从管理后台或数据库）
TOKEN="your_token_here"

# 测试文生视频接口
curl -X POST https://your-domain.com/kling/v1/videos/text2video \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "kling-v1-5-std",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "duration": 5,
    "aspect_ratio": "16:9"
  }'

# 应返回类似以下响应：
# {
#   "task_id": "kling_abc123...",
#   "kling_task_id": "kt_xyz...",
#   "status": "submitted",
#   "message": "任务已提交，请通过查询接口获取结果"
# }
```

#### 8.3 测试查询接口

```bash
# 使用上一步返回的 task_id 查询
curl -X GET "https://your-domain.com/kling/v1/videos/kling_abc123..." \
  -H "Authorization: Bearer $TOKEN"
```

#### 8.4 测试回调接口（模拟）

```bash
# 模拟 Kling 回调（用于测试）
curl -X POST https://your-domain.com/kling/callback/kling_abc123... \
  -H "Content-Type: application/json" \
  -d '{
    "task_id": "kt_xyz...",
    "task_status": "succeed",
    "task_result": {
      "videos": [{
        "id": "video_123",
        "url": "https://example.com/video.mp4",
        "duration": "5"
      }]
    },
    "external_task_id": "kling_abc123..."
  }'
```

## 配置优化

### 1. 调整模型定价

编辑 `common/model-ratio.go`：

```go
// 根据实际成本调整定价
"kling-v1-5-std":  50,   // 调整为实际价格
"kling-v1-5-pro":  100,
"kling-v1-6-std":  60,
"kling-v1-6-pro":  120,
```

重新编译并重启服务。

### 2. 配置日志级别

在 `.env` 文件中：

```bash
# 生产环境建议使用 info 或 warn
LOG_LEVEL=info

# 开发环境可使用 debug
# LOG_LEVEL=debug
```

### 3. 配置数据库连接池

在配置文件中调整：

```go
// 最大连接数
MaxOpenConns: 100

// 最大空闲连接数
MaxIdleConns: 10

// 连接最大生命周期
ConnMaxLifetime: time.Hour
```

### 4. 配置 Redis 缓存

如果使用 Redis，配置连接字符串：

```bash
REDIS_CONN_STRING=redis://localhost:6379/0
```

## 监控与维护

### 1. 日志监控

```bash
# 实时查看日志
tail -f logs/oneapi.log

# 查看 Kling 相关日志
grep -i kling logs/oneapi.log

# 查看错误日志
grep -i error logs/oneapi.log

# 查看计费日志
grep "billing" logs/oneapi.log
```

### 2. 数据库监控

```sql
-- 查看任务统计
SELECT 
    status, 
    COUNT(*) as count,
    SUM(quota) as total_quota
FROM videos 
WHERE provider = 'kling'
GROUP BY status;

-- 查看最近的任务
SELECT * FROM videos 
WHERE provider = 'kling' 
ORDER BY created_at DESC 
LIMIT 10;

-- 查看失败任务
SELECT task_id, fail_reason, created_at 
FROM videos 
WHERE provider = 'kling' AND status = 'failed'
ORDER BY created_at DESC 
LIMIT 20;
```

### 3. 性能监控

```bash
# 查看系统资源使用
top -p $(pgrep one-api)

# 查看内存使用
ps aux | grep one-api

# 查看网络连接
netstat -an | grep :3000
```

### 4. 定期维护

#### 清理过期任务

```sql
-- 删除 30 天前的已完成任务
DELETE FROM videos 
WHERE provider = 'kling' 
AND status IN ('succeed', 'failed')
AND created_at < UNIX_TIMESTAMP(DATE_SUB(NOW(), INTERVAL 30 DAY));
```

#### 备份数据库

```bash
# 备份数据库
mysqldump -u root -p oneapi > backup_$(date +%Y%m%d).sql

# 或使用自动化脚本
0 2 * * * /path/to/backup_script.sh
```

## 故障排查

### 问题 1: 回调未收到

**症状**: 任务一直处于 `submitted` 状态

**排查步骤**:
1. 检查回调 URL 是否可公网访问
2. 查看防火墙设置
3. 检查 Kling 平台是否正确配置回调 URL
4. 查看系统日志是否有回调请求记录

**解决方案**:
- 使用轮询作为备选方案
- 配置正确的公网域名
- 开放相应端口

### 问题 2: 计费异常

**症状**: 用户余额扣除不正确

**排查步骤**:
1. 查看数据库 `videos` 表的 `quota` 字段
2. 检查日志中的计费记录
3. 验证计费公式是否正确

**解决方案**:
- 检查 `CalculateQuota` 函数逻辑
- 调整模型定价配置
- 手动补偿用户余额

### 问题 3: API 请求失败

**症状**: 返回 500 错误

**排查步骤**:
1. 查看详细错误日志
2. 检查 API Key 是否有效
3. 验证 Base URL 是否正确
4. 测试网络连接

**解决方案**:
- 更新 API Key
- 检查渠道配置
- 联系 Kling 技术支持

### 问题 4: 数据库连接失败

**症状**: 服务启动失败或频繁断开

**排查步骤**:
1. 检查数据库服务状态
2. 验证连接字符串
3. 查看数据库日志

**解决方案**:
- 重启数据库服务
- 调整连接池配置
- 检查数据库权限

## 安全建议

### 1. API Key 安全

- 不要在代码中硬编码 API Key
- 使用环境变量或配置文件存储
- 定期轮换 API Key
- 限制 API Key 的访问权限

### 2. 回调安全

- 验证回调请求的来源
- 使用 HTTPS 加密传输
- 实现签名验证机制
- 设置 IP 白名单

### 3. 数据安全

- 定期备份数据库
- 加密敏感信息
- 限制数据库访问权限
- 使用强密码

### 4. 系统安全

- 及时更新系统补丁
- 配置防火墙规则
- 使用 fail2ban 防止暴力破解
- 定期审计日志

## 扩展部署

### 负载均衡

使用 Nginx 进行负载均衡：

```nginx
upstream one_api {
    server 127.0.0.1:3000;
    server 127.0.0.1:3001;
    server 127.0.0.1:3002;
}

server {
    listen 80;
    server_name your-domain.com;

    location / {
        proxy_pass http://one_api;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### Docker 部署

创建 `Dockerfile`:

```dockerfile
FROM golang:1.20-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o one-api

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/one-api .
COPY --from=builder /app/web ./web
EXPOSE 3000
CMD ["./one-api"]
```

构建并运行：

```bash
docker build -t one-api:latest .
docker run -d -p 3000:3000 --name one-api one-api:latest
```

### Kubernetes 部署

创建 `deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: one-api
spec:
  replicas: 3
  selector:
    matchLabels:
      app: one-api
  template:
    metadata:
      labels:
        app: one-api
    spec:
      containers:
      - name: one-api
        image: one-api:latest
        ports:
        - containerPort: 3000
        env:
        - name: SERVER_ADDRESS
          value: "https://your-domain.com"
```

## 联系支持

如遇到问题，请：

1. 查看官方文档
2. 搜索 GitHub Issues
3. 加入社区讨论群
4. 联系技术支持团队

---

**最后更新**: 2025-12-25

