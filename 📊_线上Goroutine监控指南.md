# 📊 线上 Goroutine 监控指南

## 🎯 部署后，你可以用这些方法查看 goroutine 数量

---

## 方法 1: 使用监控 API（最简单）⭐

### 快速查看

```bash
# 查看当前状态
curl http://your-server:3000/api/monitor/health

# 或使用 jq 格式化输出
curl -s http://your-server:3000/api/monitor/health | jq .
```

**返回示例：**
```json
{
  "status": "ok",
  "goroutines": 1234,
  "memory": {
    "alloc_mb": 256,
    "total_alloc_mb": 1024,
    "sys_mb": 512,
    "num_gc": 45
  }
}
```

### 实时监控（自动刷新）

```bash
# 方法1: 使用脚本（推荐）
chmod +x monitor_goroutines.sh
./monitor_goroutines.sh http://your-server:3000

# 方法2: 使用 watch 命令
watch -n 5 'curl -s http://your-server:3000/api/monitor/health | jq .'

# 方法3: 简单循环
while true; do 
  curl -s http://your-server:3000/api/monitor/health | jq .
  sleep 5
done
```

---

## 方法 2: 查看日志（自动记录）

服务会自动在日志中记录 goroutine 数量：

```bash
# Docker 方式
docker logs -f one-api | grep -i "goroutine"

# 或者查看最近的记录
docker logs --tail 100 one-api | grep -i "goroutine"
```

**日志示例：**
```
2025-10-27 10:30:00 Goroutine count: 856        ✅ 正常
2025-10-27 10:30:30 Goroutine count: 923        ✅ 正常
2025-10-27 10:31:00 ⚠️ Goroutine count elevated: 2156   ⚠️ 略高
2025-10-27 10:31:30 ⚠️ High goroutine count detected: 5234  🔴 异常
```

**告警级别：**
- **< 2000**: 正常（不记录，除非开启DEBUG）
- **2000-5000**: 警告（记录到日志）
- **> 5000**: 危险（记录错误日志）

---

## 方法 3: 使用 Docker 命令

### 查看容器资源使用

```bash
# 查看实时资源使用
docker stats one-api --no-stream

# 持续监控
docker stats one-api
```

**输出示例：**
```
CONTAINER   CPU %   MEM USAGE / LIMIT   MEM %   NET I/O
one-api     2.5%    1.2GiB / 4GiB      30%     1.2GB / 890MB
```

---

## 方法 4: 远程服务器监控

### 通过 SSH 监控

```bash
# 登录服务器
ssh user@your-server

# 查看goroutine数量
curl -s http://localhost:3000/api/monitor/health | jq .goroutines

# 或使用实时监控脚本
cd /path/to/ezlinkai
./monitor_goroutines.sh
```

### 通过公网监控（如果有公网IP）

```bash
# 从你的本地电脑监控线上服务器
./monitor_goroutines.sh http://your-public-ip:3000

# 或者设置定时任务
crontab -e
# 添加：每5分钟检查一次
*/5 * * * * curl -s http://your-server:3000/api/monitor/health >> /var/log/oneapi-monitor.log
```

---

## 方法 5: 集成到监控系统

### Prometheus 格式（如果需要）

创建 Prometheus exporter：

```bash
# 创建监控脚本
cat > /usr/local/bin/oneapi_exporter.sh << 'EOF'
#!/bin/bash
RESPONSE=$(curl -s http://localhost:3000/api/monitor/health)
GOROUTINES=$(echo "$RESPONSE" | jq -r .goroutines)
MEMORY_MB=$(echo "$RESPONSE" | jq -r .memory.alloc_mb)

cat << METRICS
# HELP oneapi_goroutines Number of goroutines
# TYPE oneapi_goroutines gauge
oneapi_goroutines $GOROUTINES

# HELP oneapi_memory_mb Memory usage in MB
# TYPE oneapi_memory_mb gauge
oneapi_memory_mb $MEMORY_MB
METRICS
EOF

chmod +x /usr/local/bin/oneapi_exporter.sh
```

### Grafana 监控面板

如果你使用 Grafana，可以配置：

```json
{
  "panels": [
    {
      "title": "Goroutine Count",
      "targets": [{
        "expr": "oneapi_goroutines",
        "legendFormat": "Goroutines"
      }],
      "alert": {
        "conditions": [
          {
            "evaluator": {
              "params": [5000],
              "type": "gt"
            }
          }
        ]
      }
    }
  ]
}
```

---

## 方法 6: 设置告警通知

### 简单的邮件告警

```bash
#!/bin/bash
# check_and_alert.sh

GOROUTINES=$(curl -s http://localhost:3000/api/monitor/health | jq -r .goroutines)

if [ $GOROUTINES -gt 5000 ]; then
    echo "High goroutine count: $GOROUTINES" | \
    mail -s "⚠️ OneAPI Alert: High Goroutine Count" admin@example.com
fi
```

### 企业微信/钉钉告警

```bash
#!/bin/bash
# wechat_alert.sh

GOROUTINES=$(curl -s http://localhost:3000/api/monitor/health | jq -r .goroutines)
WEBHOOK_URL="你的企业微信机器人webhook"

if [ $GOROUTINES -gt 5000 ]; then
    curl -X POST $WEBHOOK_URL \
    -H 'Content-Type: application/json' \
    -d "{
        \"msgtype\": \"text\",
        \"text\": {
            \"content\": \"⚠️ OneAPI告警\n当前Goroutine数量: $GOROUTINES\n已超过阈值5000，请检查！\"
        }
    }"
fi
```

---

## 📊 健康指标参考

### Goroutine 数量判断标准

| 数量范围 | 状态 | 说明 | 建议 |
|---------|------|------|------|
| < 1000 | 🟢 优秀 | 轻负载或刚启动 | 无需操作 |
| 1000-2000 | 🟢 良好 | 正常负载 | 正常运行 |
| 2000-3000 | 🟡 注意 | 较高负载 | 持续观察 |
| 3000-5000 | 🟡 警告 | 高负载 | 检查日志 |
| 5000-10000 | 🟠 告警 | 异常高 | 排查问题 |
| > 10000 | 🔴 危险 | 可能泄漏 | 立即处理 |

### 内存使用判断标准

| 内存占用 | 状态 | 建议 |
|---------|------|------|
| < 1GB | 🟢 正常 | 无需操作 |
| 1-2GB | 🟢 良好 | 正常运行 |
| 2-3GB | 🟡 注意 | 持续观察 |
| 3-4GB | 🟠 警告 | 检查是否泄漏 |
| > 4GB | 🔴 危险 | 考虑重启 |

---

## 🛠️ 实用监控脚本

### 脚本 1: 一键检查脚本

创建 `quick_check.sh`：

```bash
#!/bin/bash
echo "🔍 OneAPI 健康检查"
echo "=================="

# 获取数据
DATA=$(curl -s http://localhost:3000/api/monitor/health)
GOROUTINES=$(echo "$DATA" | jq -r .goroutines)
MEMORY=$(echo "$DATA" | jq -r .memory.alloc_mb)

# 显示结果
echo "Goroutines: $GOROUTINES"
echo "内存使用: ${MEMORY}MB"

# 判断状态
if [ $GOROUTINES -gt 5000 ]; then
    echo "状态: 🔴 危险 - Goroutine过多！"
    exit 1
elif [ $GOROUTINES -gt 2000 ]; then
    echo "状态: 🟡 警告 - Goroutine略高"
    exit 0
else
    echo "状态: 🟢 正常"
    exit 0
fi
```

### 脚本 2: 持续监控脚本（带历史记录）

创建 `continuous_monitor.sh`：

```bash
#!/bin/bash

LOG_FILE="/var/log/oneapi-goroutines.log"
API_URL="http://localhost:3000/api/monitor/health"

while true; do
    TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')
    DATA=$(curl -s $API_URL)
    GOROUTINES=$(echo "$DATA" | jq -r .goroutines)
    MEMORY=$(echo "$DATA" | jq -r .memory.alloc_mb)
    
    # 记录到日志文件
    echo "[$TIMESTAMP] Goroutines: $GOROUTINES, Memory: ${MEMORY}MB" >> $LOG_FILE
    
    # 如果异常，输出到终端
    if [ $GOROUTINES -gt 5000 ]; then
        echo "[$TIMESTAMP] ⚠️ ALERT: Goroutines=$GOROUTINES" | tee -a $LOG_FILE
    fi
    
    sleep 60  # 每分钟检查一次
done
```

### 脚本 3: 对比分析脚本

```bash
#!/bin/bash
# compare_goroutines.sh - 对比修复前后的效果

echo "📊 Goroutine 数量趋势分析"
echo "========================"

# 获取最近1小时的数据
tail -60 /var/log/oneapi-goroutines.log | awk '{print $4}' | sort -n | uniq -c

echo ""
echo "📈 统计："
CURRENT=$(curl -s http://localhost:3000/api/monitor/health | jq -r .goroutines)
echo "当前: $CURRENT"
echo "峰值: $(tail -60 /var/log/oneapi-goroutines.log | awk '{print $4}' | sort -rn | head -1)"
echo "谷值: $(tail -60 /var/log/oneapi-goroutines.log | awk '{print $4}' | sort -n | head -1)"
```

---

## 🚀 快速开始（部署后立即使用）

### 步骤 1: 部署修复代码

```bash
cd /path/to/ezlinkai
go build -o one-api
docker-compose down
docker-compose up -d --build
```

### 步骤 2: 等待 1-2 分钟启动

```bash
docker logs -f one-api
# 看到 "One API xxx started" 和 "monitoring endpoints enabled" 就OK了
```

### 步骤 3: 开始监控

```bash
# 方法A: 单次查看
curl http://localhost:3000/api/monitor/health | jq .

# 方法B: 实时监控（推荐）
chmod +x monitor_goroutines.sh
./monitor_goroutines.sh http://localhost:3000 5

# 方法C: 查看日志
docker logs -f one-api | grep -i "goroutine"
```

### 步骤 4: 观察修复效果

**前30分钟：**
- 观察 goroutine 数量是否保持在合理范围
- 观察内存是否稳定

**前2小时：**
- 记录峰值，应该 < 3000
- 检查是否有异常波动

**前24小时：**
- 长期稳定性验证
- 确认不会持续增长

---

## 📱 手机监控（可选）

### 使用 Uptime Kuma / UptimeRobot

```yaml
监控类型: HTTP(s) - JSON Query
URL: http://your-server:3000/api/monitor/health
检查间隔: 5分钟
告警条件: $.goroutines > 5000

通知方式:
- 邮件
- 企业微信
- Telegram
```

---

## 🔔 告警配置建议

### 告警级别

```bash
# 级别1: 警告（goroutines > 2000）
# 操作: 发送通知，继续观察

# 级别2: 紧急（goroutines > 5000）
# 操作: 立即检查，准备重启

# 级别3: 严重（goroutines > 10000）
# 操作: 立即重启服务
```

### 自动重启脚本（谨慎使用）

```bash
#!/bin/bash
# auto_restart_if_needed.sh

GOROUTINES=$(curl -s http://localhost:3000/api/monitor/health | jq -r .goroutines)

if [ $GOROUTINES -gt 10000 ]; then
    echo "⚠️ Goroutines > 10000, restarting service..."
    docker-compose restart one-api
    
    # 发送通知
    curl -X POST $WEBHOOK_URL -d "OneAPI 已自动重启（Goroutine过多: $GOROUTINES）"
fi
```

---

## 📈 监控数据分析

### 查看历史趋势

```bash
# 如果你开启了持续监控
tail -1000 /var/log/oneapi-goroutines.log | awk '{print $4}' | \
  awk '{sum+=$1; count++} END {print "平均值:", sum/count}'

# 绘制简单的ASCII图表
tail -100 /var/log/oneapi-goroutines.log | \
  awk '{print $4}' | \
  spark  # 需要安装 spark 工具
```

### 对比修复前后

```bash
# 修复前（从Docker日志推测）
Goroutine峰值: 169,000+  ❌

# 修复后（实际监控）
curl -s http://localhost:3000/api/monitor/health | jq .goroutines
# 预期: < 3000  ✅
```

---

## 💡 常见问题

### Q1: 监控API需要鉴权吗？

A: 当前版本不需要。监控端点 `/api/monitor/health` 是公开的。

如果需要保护，可以添加 IP 白名单或基础认证。

### Q2: 监控会影响性能吗？

A: 影响极小：
- 每30秒检查一次
- `runtime.NumGoroutine()` 是O(1)操作
- CPU消耗 < 0.1%

### Q3: 如何判断是否修复成功？

A: 观察以下指标：

**修复成功的标志：**
- ✅ Goroutine 数量稳定在 < 3000
- ✅ 内存不持续增长
- ✅ 容器不再崩溃
- ✅ 日志无大量超时错误

**仍有问题的标志：**
- ❌ Goroutine 持续增长
- ❌ 内存持续增长
- ❌ 超过 10000 goroutine
- ❌ 容器仍然崩溃

### Q4: 多少 goroutine 算正常？

A: 取决于你的并发量：

```
低并发（< 10 QPS）:   100-500    goroutine
中并发（10-50 QPS）:  500-1500   goroutine
高并发（50-100 QPS）: 1500-3000  goroutine
超高并发（> 100 QPS）: 3000-5000  goroutine
```

**关键是要稳定，不是持续增长！**

---

## 🎯 快速验证修复效果

### 第一天：初步验证

```bash
# 部署后立即记录
curl -s http://localhost:3000/api/monitor/health | jq . > baseline.json

# 1小时后检查
curl -s http://localhost:3000/api/monitor/health | jq .

# 对比goroutine数量
# 如果增长 < 20%，说明基本正常
```

### 第一周：稳定性验证

```bash
# 每天记录峰值
echo "$(date) - $(curl -s http://localhost:3000/api/monitor/health | jq -r .goroutines)" \
  >> weekly_stats.log

# 一周后分析
cat weekly_stats.log
# 应该看到数值在合理范围内波动，而不是持续增长
```

---

## 📞 需要帮助？

如果监控发现异常：

1. **Goroutine > 5000**
   - 检查最近的错误日志
   - 查看是否有某个渠道大量超时
   - 考虑临时禁用问题渠道

2. **Goroutine > 10000**
   - 立即检查日志中的错误模式
   - 可能需要重启服务
   - 联系技术支持排查

3. **持续增长**
   - 说明可能还有其他泄漏点
   - 提供监控数据进行分析

---

**监控工具清单：**
- ✅ `monitor_goroutines.sh` - 实时监控脚本
- ✅ `/api/monitor/health` - 监控API端点
- ✅ 日志自动记录（每30秒）
- ✅ Docker stats 命令
- ✅ 可集成 Prometheus/Grafana

**现在部署后就可以实时监控了！** 🎉

