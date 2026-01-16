# Grafana 日志显示优化指南

将多行JSON日志压缩为单行显示，点击展开查看详情。

---

## 🎯 目标效果

**优化前（多行显示）**：
```
2026-01-16 16:00:51.966
info
{
  "ts": "2026-01-16T16:00:51.966207792+08:00",
  "level": "info",
  "request_id": "2026011616005196528912539522107",
  "msg": "No unfinished tasks found",
  "service": "one-api",
  "instance": "dev-localhost-li"
}
```

**优化后（单行显示）**：
```
2026-01-16T16:00:51+08:00 [info] No unfinished tasks found
2026-01-16T16:00:52+08:00 [info] GET /api/status 200 3ms - 192.168.65.1
2026-01-16T16:00:53+08:00 [warn] POST /api/login 401 15ms - 192.168.65.1
```

点击任意行 → 展开查看完整JSON详情

---

## 方法1：配置Grafana显示选项（最简单）

### 步骤1：打开Explore页面

1. 访问 http://localhost:3200
2. 点击左侧菜单 **Explore**（罗盘图标 🧭）
3. 确认数据源为 **Loki**

### 步骤2：配置显示选项

1. 输入查询（例如）：
   ```logql
   {job="oneapi"}
   ```

2. 点击查询框右侧的 **Options** 按钮（或齿轮图标⚙️）

3. 配置以下选项：

   | 选项 | 设置 | 说明 |
   |------|------|------|
   | **Wrap lines** | ❌ 关闭 | **关键！** 使日志显示为单行 |
   | **Prettify JSON** | ❌ 关闭 | **关键！** 避免格式化JSON |
   | **Show time** | ✅ 开启 | 显示时间戳 |
   | **Show labels** | None 或 Selected | 隐藏或选择性显示标签 |
   | **Deduplication** | None | 不去重 |
   | **Order** | Newest first | 最新日志在前 |

4. 点击日志行可以展开查看完整内容

### 效果

- 日志会显示为单行
- 点击任意日志行会展开显示完整JSON
- 可以复制、搜索、高亮

---

## 方法2：使用LogQL自定义格式（推荐）

使用`line_format`自定义日志的显示格式。

### 2.1 HTTP访问日志（紧凑格式）

```logql
{job="oneapi", msg="HTTP request"}
| json
| line_format "{{.ts}} [{{.level}}] {{.method}} {{.path}} {{.status}} {{.latency_ms}}ms - {{.client_ip}}"
```

**显示效果**：
```
2026-01-16T16:00:51+08:00 [info] GET /api/status 200 3ms - 192.168.65.1
2026-01-16T16:00:52+08:00 [warn] GET /api/test 404 2ms - 192.168.65.1
2026-01-16T16:00:53+08:00 [error] POST /api/data 500 120ms - 10.0.1.5
```

### 2.2 系统日志（简洁格式）

```logql
{job="oneapi"} | json | msg != "HTTP request"
| line_format "{{.ts}} [{{.level}}] {{.msg}}"
```

**显示效果**：
```
2026-01-16T16:00:51+08:00 [info] No unfinished tasks found
2026-01-16T16:00:52+08:00 [info] channels synced from database
2026-01-16T16:00:53+08:00 [warn] Rate limit exceeded
```

### 2.3 表格对齐格式

```logql
{job="oneapi", msg="HTTP request"}
| json
| line_format "{{.level | printf \"%-5s\"}} | {{.method | printf \"%-6s\"}} | {{.status | printf \"%-3s\"}} | {{.latency_ms | printf \"%4s\"}}ms | {{.path}}"
```

**显示效果**：
```
info  | GET    | 200 |    3ms | /api/status
warn  | POST   | 404 |    2ms | /api/user/login
error | GET    | 500 |  120ms | /api/data
```

### 2.4 带emoji的彩色格式

```logql
{job="oneapi"}
| json
| line_format `{{if eq .level "error"}}🔴{{else if eq .level "warn"}}🟡{{else}}🟢{{end}} {{.ts}} {{.msg}}`
```

**显示效果**：
```
🟢 2026-01-16T16:00:51 No unfinished tasks found
🟡 2026-01-16T16:00:52 HTTP request
🔴 2026-01-16T16:00:53 Database connection failed
```

---

## 方法3：导入预配置Dashboard

### 步骤1：导入Dashboard

1. 打开Grafana → **Dashboards** → **Import**
2. 点击 **Upload JSON file**
3. 选择文件：`/Users/yueqingli/code/one-api/loki/grafana-dashboard-logs.json`
4. 点击 **Load**
5. 选择数据源：**Loki**
6. 点击 **Import**

### 步骤2：查看Dashboard

Dashboard包含3个优化的日志面板：

1. **HTTP 访问日志（单行格式）** - 显示所有HTTP请求
2. **系统日志（单行格式）** - 显示系统消息
3. **错误和警告日志** - 只显示warn和error级别

所有面板都配置为：
- ✅ 单行显示
- ✅ 点击展开详情
- ✅ 自动刷新（10秒）

---

## 常用查询模板

### 场景1：只看某个路径的日志

```logql
{job="oneapi", path="/api/status"}
| json
| line_format "{{.ts}} [{{.level}}] {{.method}} {{.status}} {{.latency_ms}}ms"
```

### 场景2：只看错误和警告

```logql
{job="oneapi", level=~"warn|error"}
| json
| line_format "{{.ts}} [{{.level | ToUpper}}] {{.msg}} {{if .path}}| {{.method}} {{.path}}{{end}}"
```

### 场景3：只看慢请求（>100ms）

```logql
{job="oneapi"} | json | latency_ms > 100
| line_format "⚠️  {{.ts}} {{.path}} took {{.latency_ms}}ms (status: {{.status}})"
```

### 场景4：按request_id搜索

```logql
{job="oneapi"} | json | request_id="2026011616005196528912539522107"
| line_format "{{.ts}} [{{.level}}] {{.msg}} {{if .path}}{{.method}} {{.path}} {{.status}}{{end}}"
```

---

## line_format语法说明

### 基本语法

```logql
| line_format "文本 {{.字段名}} 更多文本"
```

### 常用函数

| 函数 | 说明 | 示例 |
|------|------|------|
| `ToUpper` | 转大写 | `{{.level \| ToUpper}}` → INFO |
| `ToLower` | 转小写 | `{{.method \| ToLower}}` → get |
| `printf` | 格式化 | `{{.status \| printf "%-3s"}}` → 200_ |

### 条件判断

```logql
{{if eq .level "error"}}🔴{{else if eq .level "warn"}}🟡{{else}}🟢{{end}}
```

### 可用字段

从你的日志JSON中提取的字段：
- `{{.ts}}` - 时间戳
- `{{.level}}` - 日志级别
- `{{.msg}}` - 消息
- `{{.request_id}}` - 请求ID
- `{{.service}}` - 服务名
- `{{.instance}}` - 实例名
- `{{.status}}` - HTTP状态码
- `{{.method}}` - HTTP方法
- `{{.path}}` - 请求路径
- `{{.latency_ms}}` - 响应时间
- `{{.client_ip}}` - 客户端IP

---

## 创建Dashboard Panel

### 步骤1：创建新Dashboard

1. Grafana → **Dashboards** → **New** → **New Dashboard**
2. 点击 **Add visualization**
3. 选择数据源：**Loki**

### 步骤2：配置Query

在Query编辑器中输入：

```logql
{job="oneapi", msg="HTTP request"}
| json
| line_format "{{.ts}} [{{.level}}] {{.method}} {{.path}} {{.status}} {{.latency_ms}}ms"
```

### 步骤3：配置Visualization

1. 右侧选择可视化类型：**Logs**
2. 在 **Logs** 配置中：
   - Show time: ✅
   - Wrap lines: ❌
   - Prettify JSON: ❌
   - Order: Newest first

### 步骤4：保存Panel

1. 点击右上角 **Apply**
2. 点击右上角 💾 **Save dashboard**
3. 输入名称：`One-API 日志监控`

---

## 高级配置

### 配置1：自动高亮关键词

在Grafana Explore中，使用搜索功能：

1. 输入查询并运行
2. 点击顶部的 **Highlight words** 按钮
3. 输入要高亮的关键词（如：`error`, `404`, `500`）

### 配置2：配置时间格式

如果想要更简洁的时间显示：

```logql
{job="oneapi"}
| json
| line_format "{{.ts | date \"15:04:05\"}} [{{.level}}] {{.msg}}"
```

显示效果：
```
16:00:51 [info] No unfinished tasks found
```

### 配置3：创建变量

在Dashboard中创建变量，动态切换查询：

1. Dashboard设置 → **Variables** → **Add variable**
2. 配置：
   - Name: `log_level`
   - Type: Custom
   - Custom options: `info,warn,error`
3. 在查询中使用：
   ```logql
   {job="oneapi", level="$log_level"}
   ```

---

## 快速参考

### 推荐配置组合

**方案A：极简显示**
```logql
{job="oneapi"} | json | line_format "{{.ts}} {{.msg}}"
```

**方案B：HTTP访问日志**
```logql
{job="oneapi", msg="HTTP request"} | json
| line_format "{{.method}} {{.path}} {{.status}} {{.latency_ms}}ms"
```

**方案C：完整但紧凑**
```logql
{job="oneapi"} | json
| line_format "[{{.level}}] {{.msg}} {{if .path}}| {{.method}} {{.path}} {{.status}}{{end}}"
```

### Grafana快捷键

| 快捷键 | 功能 |
|--------|------|
| `Ctrl/Cmd + K` | 打开搜索 |
| `e` | 打开Explore |
| `d` | 打开Dashboard |
| `Ctrl/Cmd + S` | 保存Dashboard |

---

## 故障排查

### 问题1：日志仍然是多行显示

**解决**：
1. 确保 **Wrap lines** 已关闭
2. 确保 **Prettify JSON** 已关闭
3. 刷新浏览器页面

### 问题2：line_format不生效

**检查**：
1. 确保使用了 `| json` 解析
2. 检查字段名是否正确（区分大小写）
3. 检查模板语法是否正确

### 问题3：点击无法展开

**原因**：可能是Grafana版本问题

**解决**：
- 升级到最新版Grafana
- 或使用原始JSON格式（不使用line_format）

---

## 相关文档

- [LOKI_COMMANDS_REFERENCE.md](./LOKI_COMMANDS_REFERENCE.md) - Loki命令参考
- [HTTP_ACCESS_LOG_IMPLEMENTATION.md](./HTTP_ACCESS_LOG_IMPLEMENTATION.md) - 实现报告
- [LogQL官方文档](https://grafana.com/docs/loki/latest/query/log_queries/)

---

**最后更新**: 2026-01-16
