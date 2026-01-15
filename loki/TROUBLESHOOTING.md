# Loki 日志栈故障排查记录

本文档记录了在部署和配置 Loki + Grafana + Promtail 日志栈时遇到的问题及解决方案。

---

## 目录

- [问题 1: Loki 容器健康检查失败](#问题-1-loki-容器健康检查失败)
- [问题 2: Promtail 无法连接到 Loki](#问题-2-promtail-无法连接到-loki)
- [问题 3: Grafana 中无 Loki 数据源](#问题-3-grafana-中无-loki-数据源)
- [验证日志系统正常工作](#验证日志系统正常工作)
- [常用排查命令](#常用排查命令)

---

## 问题 1: Loki 容器健康检查失败

### 🔴 问题现象

```bash
$ docker ps
CONTAINER ID   IMAGE                  STATUS
7a83122f64cc   grafana/loki:latest    Up 16 minutes (unhealthy)
```

错误信息：
```
dependency failed to start: container loki is unhealthy
```

### 🔍 根本原因

**健康检查配置与镜像不兼容：**

1. Loki 使用的是 **distroless 精简镜像**，不包含 `/bin/sh` 和常用 Unix 工具
2. 健康检查配置使用了 `CMD-SHELL` 和 `wget` 命令
3. 容器内无法执行健康检查命令，导致持续失败

**详细错误日志：**
```json
{
    "Status": "unhealthy",
    "FailingStreak": 100,
    "Output": "OCI runtime exec failed: exec failed: unable to start container process: exec: \"/bin/sh\": stat /bin/sh: no such file or directory: unknown"
}
```

### ✅ 解决方案

**方案：移除健康检查配置**

修改 `loki/docker-compose-logging.yml`：

```yaml
  loki:
    image: grafana/loki:latest
    container_name: loki
    restart: always
    ports:
      - "3100:3100"
    volumes:
      - ./loki-config.yaml:/etc/loki/config.yaml:ro
      - ./loki-data:/loki
    command: -config.file=/etc/loki/config.yaml
    # 健康检查已移除，因为 distroless 镜像不包含 shell
    # Loki 服务通过日志和端口监听状态即可验证正常运行
    networks:
      - logging
```

**同时修改 Grafana 的依赖配置：**

```yaml
  grafana:
    depends_on:
      - loki  # 移除 condition: service_healthy，使用简单依赖
```

### 📝 说明

- Loki 服务本身运行正常，通过端口监听和日志输出即可验证
- 移除健康检查不影响实际功能
- Grafana 会在 Loki 启动后自动连接（可能需要几秒钟）

---

## 问题 2: Promtail 无法连接到 Loki

### 🔴 问题现象

Promtail 日志中持续报错：

```
level=warn msg="error sending batch, will retry"
error="Post \"http://host.docker.internal:3100/loki/api/v1/push\": dial tcp 192.168.65.254:3100: connect: connection refused"
```

### 🔍 根本原因

**网络隔离问题：**

1. Loki 运行在独立的 `loki_logging` 网络中
2. Promtail 运行在 `one-api_one-api-network` 网络中
3. Promtail 配置使用 `host.docker.internal:3100` 无法访问 Loki
4. 两个容器网络隔离，无法互相通信

### ✅ 解决方案

**方案：让 Promtail 加入 Loki 网络**

修改 `docker-compose-deps.yml`：

#### 步骤 1: 修改 Promtail 网络配置

```yaml
  promtail:
    image: grafana/promtail:latest
    container_name: one-api-promtail
    restart: always
    volumes:
      - ./logs:/var/log/oneapi:ro
      - ./promtail-config.yaml:/etc/promtail/config.yaml:ro
    command: -config.file=/etc/promtail/config.yaml -config.expand-env=true
    environment:
      - LOKI_URL=${LOKI_URL:-http://loki:3100/loki/api/v1/push}  # 改用容器名称
    depends_on:
      one-api:
        condition: service_healthy
    networks:
      - one-api-network
      - loki_logging  # 加入 Loki 网络以便直接通信
```

#### 步骤 2: 声明外部网络

```yaml
networks:
  one-api-network:
    driver: bridge
  loki_logging:
    external: true  # 使用外部网络（由 loki/docker-compose-logging.yml 创建）
```

### 🔄 重启服务

```bash
cd /path/to/one-api
docker compose -f docker-compose-deps.yml down promtail
docker compose -f docker-compose-deps.yml up -d promtail
```

### ✅ 验证

检查 Promtail 日志，应该没有错误：

```bash
docker logs one-api-promtail --tail 20
```

正常输出：
```
level=info msg="tail routine: started" path=/var/log/oneapi/oneapi-20260115.log
```

---

## 问题 3: Grafana 中无 Loki 数据源

### 🔴 问题现象

打开 Grafana 界面，数据源列表中没有看到 Loki。

### 🔍 根本原因

**Provisioning 配置挂载路径错误：**

1. `docker-compose-logging.yml` 中配置：`./grafana/provisioning`
2. 实际挂载路径：`/path/to/loki/grafana/provisioning` （目录不存在）
3. 正确路径应该是：`../grafana/provisioning` （项目根目录下的 grafana）

**错误配置：**
```yaml
volumes:
  - ./grafana/provisioning:/etc/grafana/provisioning  # 错误：相对于 loki/ 目录
```

**容器内检查：**
```bash
$ docker exec grafana ls /etc/grafana/provisioning/datasources/
ls: /etc/grafana/provisioning/datasources/: No such file or directory
```

### ✅ 解决方案

**方案：修正挂载路径**

修改 `loki/docker-compose-logging.yml` 第 30 行：

```yaml
  grafana:
    image: grafana/grafana:latest
    container_name: grafana
    restart: always
    ports:
      - "3200:3000"
    volumes:
      - ./grafana-data:/var/lib/grafana
      - ../grafana/provisioning:/etc/grafana/provisioning  # 使用项目根目录的 grafana 配置
    environment:
      - GF_SECURITY_ADMIN_USER=${GF_ADMIN_USER:-admin}
      - GF_SECURITY_ADMIN_PASSWORD=${GF_ADMIN_PASSWORD:-admin}
```

### 🔄 重新创建容器

```bash
cd /path/to/one-api/loki
docker compose -f docker-compose-logging.yml down grafana
docker compose -f docker-compose-logging.yml up -d grafana
```

### ✅ 验证

#### 方法 1: 检查容器内配置文件

```bash
docker exec grafana ls -la /etc/grafana/provisioning/datasources/
# 应该看到 loki.yaml
```

#### 方法 2: 检查 Grafana 日志

```bash
docker logs grafana --tail 100 | grep datasource
# 应该看到：inserting datasource from configuration name=Loki
```

#### 方法 3: 通过 API 查询

```bash
curl -s -u admin:admin "http://localhost:3200/api/datasources" | python3 -m json.tool
```

正常输出：
```json
[
    {
        "name": "Loki",
        "type": "loki",
        "url": "http://loki:3100",
        "isDefault": true
    }
]
```

#### 方法 4: 测试数据源连接

```bash
curl -s -u admin:admin "http://localhost:3200/api/datasources/uid/P8E80F9AEF21F6940/health" | python3 -m json.tool
```

正常输出：
```json
{
    "message": "Data source successfully connected.",
    "status": "OK"
}
```

---

## 验证日志系统正常工作

### ✅ 完整验证流程

#### 1. 检查所有容器状态

```bash
docker ps | grep -E "(loki|grafana|promtail)"
```

预期输出：
```
grafana          Up X minutes (healthy)
loki             Up X minutes
one-api-promtail Up X minutes
```

#### 2. 检查 Promtail 是否推送日志

```bash
# 检查 Promtail 日志（不应有错误）
docker logs one-api-promtail --tail 20

# 查询 Loki 中的标签
curl -s "http://localhost:3100/loki/api/v1/labels" | python3 -m json.tool
```

预期看到标签：
```json
{
    "status": "success",
    "data": ["job", "level", "service", "stream", "instance"]
}
```

#### 3. 发送测试请求生成日志

```bash
# 正常请求
curl -s http://localhost:3000/api/status

# 404 请求
curl -s http://localhost:3000/api/nonexistent-endpoint

# 登录请求
curl -s -X POST http://localhost:3000/api/user/login \
  -H "Content-Type: application/json" \
  -d '{"username":"test","password":"wrong"}'
```

#### 4. 查询 Loki 日志

```bash
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi"}' \
  --data-urlencode 'limit=10' \
  --data-urlencode "start=$(date -u -v-5M +%s)000000000" \
  --data-urlencode "end=$(date -u +%s)000000000" | python3 -m json.tool
```

应该能看到日志记录。

#### 5. 在 Grafana 中查询

1. 访问 http://localhost:3200
2. 登录（admin/admin）
3. 左侧菜单 → Explore
4. 确认数据源选择为 "Loki"
5. 输入查询：`{job="oneapi"}`
6. 点击 "Run query"

应该能看到日志流。

---

## 常用排查命令

### 容器状态检查

```bash
# 查看所有日志栈容器
docker ps -a | grep -E "(loki|grafana|promtail)"

# 查看容器详细状态
docker inspect loki --format='{{json .State.Health}}' | python3 -m json.tool

# 查看容器网络
docker inspect loki --format='{{json .NetworkSettings.Networks}}'
```

### 日志查看

```bash
# Loki 日志
docker logs loki --tail 100

# Grafana 日志
docker logs grafana --tail 100 | grep -i "datasource\|error"

# Promtail 日志
docker logs one-api-promtail --tail 50
```

### 配置验证

```bash
# 检查 Grafana 挂载
docker inspect grafana --format='{{json .Mounts}}' | python3 -m json.tool

# 检查容器内文件
docker exec grafana ls -la /etc/grafana/provisioning/datasources/
docker exec grafana cat /etc/grafana/provisioning/datasources/loki.yaml
```

### Loki API 查询

```bash
# 查询标签
curl -s "http://localhost:3100/loki/api/v1/labels" | python3 -m json.tool

# 查询标签值
curl -s "http://localhost:3100/loki/api/v1/label/job/values" | python3 -m json.tool

# 查询日志
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi"}' \
  --data-urlencode 'limit=5' | python3 -m json.tool
```

### Grafana API 查询

```bash
# 查询数据源列表
curl -s -u admin:admin "http://localhost:3200/api/datasources" | python3 -m json.tool

# 测试数据源连接
curl -s -u admin:admin "http://localhost:3200/api/datasources/uid/<UID>/health" | python3 -m json.tool
```

### 网络排查

```bash
# 查看网络列表
docker network ls | grep loki

# 查看网络详情
docker network inspect loki_logging

# 测试容器间连通性
docker exec one-api-promtail ping -c 2 loki
docker exec one-api-promtail wget -O- http://loki:3100/ready
```

---

## 最佳实践和建议

### 1. 目录结构

建议的项目目录结构：

```
one-api/
├── loki/                          # Loki 配置目录
│   ├── docker-compose-logging.yml # Loki + Grafana 编排文件
│   ├── loki-config.yaml           # Loki 配置
│   ├── loki-data/                 # Loki 数据目录（忽略）
│   ├── grafana-data/              # Grafana 数据目录（忽略）
│   ├── LOGGING_STACK_GUIDE.md     # 使用指南
│   └── TROUBLESHOOTING.md         # 本文档
├── grafana/                       # Grafana provisioning 配置
│   └── provisioning/
│       ├── datasources/
│       │   └── loki.yaml          # Loki 数据源配置
│       └── dashboards/
│           ├── default.yaml       # Dashboard 配置
│           └── *.json             # Dashboard 定义
├── logs/                          # one-api 日志目录
│   ├── oneapi-*.log               # 普通日志
│   └── oneapi-error-*.log         # 错误日志
├── promtail-config.yaml           # Promtail 配置
└── docker-compose-deps.yml        # one-api + Promtail 编排文件
```

### 2. 网络配置

- **推荐方式**：将 Promtail 加入 Loki 网络，使用容器名直接通信
- **避免使用**：`host.docker.internal`（跨平台兼容性差）

### 3. 健康检查

- Distroless 镜像不支持 shell 命令健康检查
- 可以通过日志和端口监听验证服务状态
- 如需健康检查，考虑使用带工具的镜像（如 Alpine 版本）

### 4. 数据持久化

确保关键数据目录被正确挂载和备份：

```yaml
volumes:
  - ./loki-data:/loki              # Loki 数据
  - ./grafana-data:/var/lib/grafana # Grafana 数据
```

在 `.gitignore` 中忽略数据目录：

```
/loki/grafana-data
/loki/loki-data
```

### 5. 日志保留策略

在 `loki-config.yaml` 中配置日志保留时间：

```yaml
limits_config:
  retention_period: 168h  # 保留 7 天
```

定期清理旧日志文件：

```bash
# 清理 7 天前的日志
find ./logs -name "oneapi-*.log" -mtime +7 -delete
```

---

## 相关文档

- [Loki 日志栈使用指南](./LOGGING_STACK_GUIDE.md)
- [Loki 官方文档](https://grafana.com/docs/loki/latest/)
- [Promtail 配置参考](https://grafana.com/docs/loki/latest/send-data/promtail/)
- [LogQL 查询语言](https://grafana.com/docs/loki/latest/query/)

---

## 更新日志

- **2026-01-15**: 初始版本，记录 Loki 健康检查、Promtail 网络、Grafana 数据源问题及解决方案
