# Kling API 双主键方案上线部署流程

## 📋 部署概览

本文档提供 Kling API 集成的完整上线部署流程,包括数据库变更、代码部署、验证测试等步骤。

**变更内容:**
- ✅ 为 `videos` 表添加自增主键 `id`
- ✅ 将 `task_id` 改为唯一索引
- ✅ 优化常用查询索引(user_id, channel_id, status, created_at)
- ✅ 集成 Kling API 四个视频生成端点
- ✅ 实现后扣费计费模型
- ✅ 支持回调机制

---

## 🚀 部署流程

### 阶段一: 部署前准备(Pre-deployment)

#### 1.1 环境检查

```bash
# 检查 MySQL 版本(建议 5.7+ 或 8.0+)
mysql --version

# 检查 Go 版本(建议 1.19+)
go version

# 检查磁盘空间(确保有足够空间用于数据库备份)
df -h

# 检查当前数据库连接
mysql -u root -p -e "SHOW PROCESSLIST;"
```

#### 1.2 代码准备

```bash
# 拉取最新代码
cd /path/to/one-api
git pull origin main

# 查看变更文件
git diff HEAD~1 HEAD --name-only

# 编译新版本
go build -o one-api-new main.go

# 验证编译成功
./one-api-new --version
```

#### 1.3 备份现有数据

```bash
# 备份整个数据库
mysqldump -u root -p one-api > backup_one-api_$(date +%Y%m%d_%H%M%S).sql

# 仅备份 videos 表
mysqldump -u root -p one-api videos > backup_videos_$(date +%Y%m%d_%H%M%S).sql

# 验证备份文件
ls -lh backup_*.sql
```

---

### 阶段二: 数据库变更(Database Migration)

#### 2.1 连接数据库

```bash
mysql -u root -p one-api
```

#### 2.2 执行变更前检查

```sql
-- 检查 videos 表当前结构
SHOW CREATE TABLE videos;

-- 检查表数据量
SELECT COUNT(*) FROM videos;

-- 检查现有索引
SHOW INDEX FROM videos;

-- 检查是否有正在运行的长事务
SELECT * FROM information_schema.innodb_trx;
```

#### 2.3 执行数据库变更脚本

**方式一: 直接在 MySQL 客户端执行**

```sql
-- 使用数据库
USE one-api;

-- 如果表不存在,创建新表(跳过此步骤如果表已存在)
-- 如果表已存在,执行以下变更

-- 添加自增主键
ALTER TABLE `videos` 
ADD COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT FIRST,
ADD PRIMARY KEY (`id`);

-- 删除旧索引
ALTER TABLE `videos` DROP INDEX IF EXISTS `idx_tid`;

-- 修改 task_id 为唯一索引
ALTER TABLE `videos` 
MODIFY COLUMN `task_id` VARCHAR(200) NOT NULL COMMENT '业务任务ID',
ADD UNIQUE INDEX `idx_task_id` (`task_id`(40));

-- 添加优化索引
ALTER TABLE `videos` 
ADD INDEX IF NOT EXISTS `idx_created_at` (`created_at`),
ADD INDEX IF NOT EXISTS `idx_user_id` (`user_id`),
ADD INDEX IF NOT EXISTS `idx_channel_id` (`channel_id`),
ADD INDEX IF NOT EXISTS `idx_status` (`status`),
ADD INDEX IF NOT EXISTS `idx_video_id` (`video_id`(40)),
ADD INDEX IF NOT EXISTS `idx_user_created` (`user_id`, `created_at`),
ADD INDEX IF NOT EXISTS `idx_channel_created` (`channel_id`, `created_at`);
```

**方式二: 使用脚本文件执行**

```bash
# 执行变更脚本
mysql -u root -p one-api < bin/migration_kling_dual_key.sql

# 查看执行日志
tail -f /var/log/mysql/error.log
```

#### 2.4 验证数据库变更

```sql
-- 验证表结构
SHOW CREATE TABLE videos;

-- 验证索引
SHOW INDEX FROM videos;

-- 验证数据完整性
SELECT COUNT(*) FROM videos;
SELECT COUNT(DISTINCT task_id) FROM videos;

-- 验证主键自增
SELECT MAX(id) FROM videos;

-- 测试查询性能
EXPLAIN SELECT * FROM videos WHERE task_id = 'test_task_id';
EXPLAIN SELECT * FROM videos WHERE user_id = 1 ORDER BY created_at DESC LIMIT 10;
```

**预期结果:**
- ✅ `id` 字段为 `BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY`
- ✅ `task_id` 字段有 `UNIQUE INDEX idx_task_id`
- ✅ 其他索引正常创建
- ✅ 数据行数与变更前一致

---

### 阶段三: 应用部署(Application Deployment)

#### 3.1 停止旧服务

```bash
# 方式一: 使用 systemd
sudo systemctl stop one-api

# 方式二: 使用 PID 文件
kill $(cat one-api.pid)

# 方式三: 手动查找进程
ps aux | grep one-api
kill -15 <PID>

# 验证服务已停止
ps aux | grep one-api
```

#### 3.2 替换可执行文件

```bash
# 备份旧版本
mv one-api one-api.backup_$(date +%Y%m%d_%H%M%S)

# 部署新版本
mv one-api-new one-api
chmod +x one-api

# 验证文件权限
ls -l one-api
```

#### 3.3 更新配置文件(如需要)

```bash
# 编辑配置文件,添加 Kling 相关配置
vim config.yaml  # 或 .env 文件

# 示例配置
# CALLBACK_DOMAIN=https://your-domain.com
# KLING_BASE_URL=https://api.klingai.com
```

#### 3.4 启动新服务

```bash
# 方式一: 使用 systemd
sudo systemctl start one-api
sudo systemctl status one-api

# 方式二: 直接启动
nohup ./one-api > logs/oneapi.out 2>&1 &
echo $! > one-api.pid

# 查看启动日志
tail -f logs/oneapi.out
```

#### 3.5 验证服务启动

```bash
# 检查进程
ps aux | grep one-api

# 检查端口监听
netstat -tuln | grep 3000  # 假设服务端口为 3000

# 检查健康状态
curl http://localhost:3000/health

# 查看日志
tail -100 logs/oneapi.out
```

---

### 阶段四: 功能验证(Functional Testing)

#### 4.1 配置 Kling 渠道

**通过管理后台配置:**

1. 登录管理后台
2. 进入"渠道管理"
3. 点击"添加渠道"
4. 配置以下信息:
   - **渠道类型**: Keling (41)
   - **渠道名称**: Kling AI
   - **Base URL**: `https://api.klingai.com`
   - **密钥格式**: `AK|SK` (例如: `your_access_key|your_secret_key`)
   - **模型映射**: 根据需要配置
   - **优先级**: 设置合适的优先级
   - **状态**: 启用

5. 保存配置

#### 4.2 API 功能测试

**测试 1: 文本生成视频(Text2Video)**

```bash
curl -X POST http://localhost:3000/kling/v1/videos/text2video \
  -H "Authorization: Bearer YOUR_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "kling-v1-5-std",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "duration": 5,
    "aspect_ratio": "16:9"
  }'
```

**预期响应:**
```json
{
  "task_id": "kling_abc123...",
  "kling_task_id": "kl_xyz789...",
  "status": "submitted"
}
```

**测试 2: 查询任务结果**

```bash
curl -X GET http://localhost:3000/kling/v1/videos/kling_abc123... \
  -H "Authorization: Bearer YOUR_API_TOKEN"
```

**预期响应:**
```json
{
  "task_id": "kling_abc123...",
  "kling_task_id": "kl_xyz789...",
  "status": "processing",
  "video_url": "",
  "duration": "",
  "fail_reason": ""
}
```

**测试 3: 模拟回调(内部测试)**

```bash
curl -X POST http://localhost:3000/kling/callback/kling_abc123... \
  -H "Content-Type: application/json" \
  -d '{
    "task_id": "kl_xyz789...",
    "task_status": "succeed",
    "task_status_msg": "",
    "task_result": {
      "videos": [{
        "id": "video_123",
        "url": "https://cdn.klingai.com/video_123.mp4",
        "duration": "5"
      }]
    }
  }'
```

**预期响应:**
```json
{
  "message": "success"
}
```

#### 4.3 数据库验证

```sql
-- 查看新创建的任务
SELECT * FROM videos ORDER BY id DESC LIMIT 5;

-- 验证 task_id 唯一性
SELECT task_id, COUNT(*) FROM videos GROUP BY task_id HAVING COUNT(*) > 1;

-- 验证状态流转
SELECT status, COUNT(*) FROM videos GROUP BY status;

-- 验证计费记录
SELECT user_id, SUM(quota) as total_quota FROM videos WHERE status = 'succeed' GROUP BY user_id;
```

#### 4.4 性能测试

```sql
-- 测试主键查询性能
EXPLAIN SELECT * FROM videos WHERE id = 1;

-- 测试 task_id 查询性能
EXPLAIN SELECT * FROM videos WHERE task_id = 'kling_abc123...';

-- 测试用户查询性能
EXPLAIN SELECT * FROM videos WHERE user_id = 1 ORDER BY created_at DESC LIMIT 10;

-- 测试复合索引性能
EXPLAIN SELECT * FROM videos WHERE user_id = 1 AND created_at > 1700000000 ORDER BY created_at DESC;
```

**预期结果:**
- ✅ 主键查询使用 `PRIMARY KEY` (type: const)
- ✅ task_id 查询使用 `idx_task_id` (type: const)
- ✅ 用户查询使用 `idx_user_created` (type: range/ref)

---

### 阶段五: 监控和观察(Monitoring)

#### 5.1 日志监控

```bash
# 实时查看应用日志
tail -f logs/oneapi.out

# 过滤 Kling 相关日志
grep -i "kling" logs/oneapi.out

# 查看错误日志
grep -i "error" logs/oneapi.out | tail -20
```

#### 5.2 数据库监控

```sql
-- 监控 videos 表增长
SELECT 
    DATE(FROM_UNIXTIME(created_at)) as date,
    COUNT(*) as task_count,
    SUM(CASE WHEN status = 'succeed' THEN 1 ELSE 0 END) as success_count,
    SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failed_count
FROM videos
WHERE created_at > UNIX_TIMESTAMP(DATE_SUB(NOW(), INTERVAL 7 DAY))
GROUP BY DATE(FROM_UNIXTIME(created_at))
ORDER BY date DESC;

-- 监控慢查询
SHOW PROCESSLIST;
SELECT * FROM information_schema.processlist WHERE time > 5;
```

#### 5.3 系统资源监控

```bash
# CPU 使用率
top -p $(pgrep one-api)

# 内存使用
ps aux | grep one-api

# 磁盘 I/O
iostat -x 1

# 网络连接
netstat -antp | grep one-api
```

---

## 🔄 回滚方案(Rollback Plan)

如果部署后发现严重问题,按以下步骤回滚:

### 1. 停止新服务

```bash
sudo systemctl stop one-api
# 或
kill $(cat one-api.pid)
```

### 2. 恢复旧版本代码

```bash
mv one-api one-api.failed
mv one-api.backup_YYYYMMDD_HHMMSS one-api
chmod +x one-api
```

### 3. 回滚数据库(如需要)

```sql
-- 连接数据库
mysql -u root -p one-api

-- 删除主键和新索引
ALTER TABLE `videos` DROP PRIMARY KEY;
ALTER TABLE `videos` DROP COLUMN `id`;
ALTER TABLE `videos` DROP INDEX `idx_task_id`;

-- 恢复旧索引
ALTER TABLE `videos` ADD INDEX `idx_tid` (`task_id`(40));

-- 如果数据损坏,从备份恢复
-- DROP TABLE videos;
-- 然后导入备份: mysql -u root -p one-api < backup_videos_YYYYMMDD_HHMMSS.sql
```

### 4. 启动旧服务

```bash
sudo systemctl start one-api
# 或
nohup ./one-api > logs/oneapi.out 2>&1 &
```

### 5. 验证回滚

```bash
# 检查服务状态
curl http://localhost:3000/health

# 检查数据库
mysql -u root -p one-api -e "SHOW CREATE TABLE videos;"
```

---

## 📊 性能对比分析

### 双主键方案优势

| 查询场景 | 旧方案(仅 task_id 索引) | 新方案(id 主键 + task_id 唯一索引) | 性能提升 |
|---------|----------------------|--------------------------------|---------|
| 按 task_id 查询 | O(log n) B-tree | O(log n) B-tree | 相同 |
| 按 id 范围查询 | 全表扫描 | O(log n) 主键查询 | **10-100倍** |
| 按 user_id + 时间排序 | 索引扫描 + 排序 | 复合索引直接扫描 | **2-5倍** |
| JOIN 操作 | 字符串比较 | 整数比较 | **3-10倍** |
| 分页查询(LIMIT OFFSET) | 字符串索引扫描 | 主键范围扫描 | **5-20倍** |

### 索引空间占用

- **旧方案**: VARCHAR(200) 索引,每条记录约 40-200 字节
- **新方案**: BIGINT 主键(8字节) + VARCHAR 唯一索引(40字节)
- **空间增加**: 约 8 字节/记录(可忽略)

---

## ✅ 部署检查清单(Checklist)

### 部署前
- [ ] 代码已拉取并编译成功
- [ ] 数据库已完整备份
- [ ] 磁盘空间充足(至少 20% 剩余)
- [ ] 已通知相关人员维护窗口

### 数据库变更
- [ ] 变更脚本已审核
- [ ] 在测试环境验证通过
- [ ] 已检查无长事务运行
- [ ] 变更执行成功
- [ ] 表结构验证通过
- [ ] 数据完整性验证通过

### 应用部署
- [ ] 旧服务已停止
- [ ] 新版本已部署
- [ ] 配置文件已更新
- [ ] 新服务启动成功
- [ ] 健康检查通过

### 功能验证
- [ ] Kling 渠道配置成功
- [ ] Text2Video API 测试通过
- [ ] 查询 API 测试通过
- [ ] 回调机制测试通过
- [ ] 计费逻辑验证通过

### 监控
- [ ] 应用日志正常
- [ ] 数据库查询性能正常
- [ ] 系统资源使用正常
- [ ] 无异常错误日志

---

## 🆘 常见问题(FAQ)

### Q1: ALTER TABLE 执行时间过长怎么办?

**A:** 对于大表(百万级以上),`ALTER TABLE` 可能需要较长时间:

```sql
-- 方案1: 使用 pt-online-schema-change (推荐)
pt-online-schema-change --alter "ADD COLUMN id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT FIRST, ADD PRIMARY KEY (id)" \
  D=one-api,t=videos --execute

-- 方案2: 分批迁移
-- 1. 创建新表结构
-- 2. 分批复制数据
-- 3. 切换表名
```

### Q2: 如何验证 task_id 唯一性?

**A:** 执行以下查询:

```sql
SELECT task_id, COUNT(*) as cnt 
FROM videos 
GROUP BY task_id 
HAVING cnt > 1;
```

如果返回结果为空,说明 task_id 唯一。

### Q3: 回调失败如何重试?

**A:** Kling API 会自动重试回调,系统使用原子更新防止重复处理:

```sql
-- 查看待处理的任务
SELECT * FROM videos WHERE status IN ('submitted', 'processing');

-- 手动触发重查询
-- 通过 GET /kling/v1/videos/{task_id} 主动查询状态
```

### Q4: 如何监控计费准确性?

**A:** 定期对账:

```sql
-- 统计成功任务的总额度
SELECT 
    user_id,
    COUNT(*) as success_count,
    SUM(quota) as total_quota
FROM videos 
WHERE status = 'succeed'
GROUP BY user_id;

-- 与用户表的额度变化对比
SELECT 
    id,
    username,
    quota,
    used_quota
FROM users
WHERE id IN (SELECT DISTINCT user_id FROM videos WHERE status = 'succeed');
```

---

## 📞 联系支持

如遇到部署问题,请联系:
- **技术支持**: support@example.com
- **紧急热线**: +86 xxx-xxxx-xxxx
- **文档**: https://docs.example.com

---

## 📝 变更记录

| 版本 | 日期 | 变更内容 | 负责人 |
|-----|------|---------|--------|
| v1.0 | 2025-12-26 | 初始版本,双主键方案上线 | System |

---

**部署完成后,请在此签字确认:**

- 部署执行人: ________________  日期: ________
- 验证确认人: ________________  日期: ________
- 上线批准人: ________________  日期: ________

