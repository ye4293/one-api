# 日志系统快速启动指南

## 🚀 三步启动完整日志栈

### 1. 启动 Loki + Grafana

```bash
# 复制环境变量配置
cp env.logging.example .env.logging

# 编辑密码（可选）
vim .env.logging

# 启动日志栈
docker-compose -f docker-compose-logging.yml --env-file .env.logging up -d

# 等待服务就绪（约 10 秒）
docker-compose -f docker-compose-logging.yml ps
```

### 2. 启动 one-api + Promtail

```bash
# 启动应用和日志采集
docker-compose -f docker-compose-deps.yml up -d

# 查看 Promtail 是否正常推送
docker logs promtail 2>&1 | grep "POST"
```

### 3. 访问 Grafana 查看日志

```bash
# 浏览器打开
open http://localhost:3200

# 登录：admin / admin（或你配置的密码）
# 查看预置的 "One-API Logs" Dashboard
```

## 📊 服务端口

| 服务 | 端口 | 访问地址 |
|------|------|---------|
| **one-api** | 3000 | http://localhost:3000 |
| **Loki** | 3100 | http://localhost:3100 |
| **Grafana** | 3200 | http://localhost:3200 |
| **MySQL** | 3306 | localhost:3306 |
| **Redis** | 6379 | localhost:6379 |

## 📁 生成的文件和目录

```
one-api/
├── docker-compose-deps.yml      # one-api + 依赖服务
├── docker-compose-logging.yml   # Loki + Grafana
├── loki-config.yaml             # Loki 配置
├── promtail-config.yaml         # Promtail 配置
├── env.logging.example          # 环境变量示例
├── loki-data/                   # Loki 数据（14天保留）
├── grafana-data/                # Grafana 数据
├── grafana/provisioning/        # Grafana 自动配置
└── logs/                        # one-api 日志文件
```

## 🔍 快速查询

在 Grafana Explore 中尝试：

```logql
# 查看所有日志
{service="one-api"}

# 只看错误
{service="one-api", stream="error"}

# 按 request_id 追踪
{service="one-api"} | json | request_id="your-request-id"

# 筛选 5xx 错误
{service="one-api"} | json | status >= 500
```

## 📚 详细文档

- **[LOGGING_STACK_GUIDE.md](LOGGING_STACK_GUIDE.md)** - 完整部署和使用指南
- **[PROMTAIL_SETUP.md](PROMTAIL_SETUP.md)** - Promtail 配置说明

## 🛑 停止服务

```bash
# 停止日志栈
docker-compose -f docker-compose-logging.yml stop

# 停止应用栈
docker-compose -f docker-compose-deps.yml stop

# 完全删除（保留数据）
docker-compose -f docker-compose-logging.yml down
docker-compose -f docker-compose-deps.yml down
```

---

**问题？** 查看 [LOGGING_STACK_GUIDE.md](LOGGING_STACK_GUIDE.md) 的故障排查部分。
