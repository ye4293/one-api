# Sora 渠道配置指南

## ❗ 错误原因

您遇到的错误：
```
"There are no channels available for model under the current group Lv1"
```

**原因**：系统中没有配置 `sora-2` 或 `sora-2-pro` 模型的渠道。

---

## ✅ 解决方案：配置 Sora 渠道

### 步骤 1: 登录后台管理

访问您的管理后台（通常是 `http://localhost:3000`）

### 步骤 2: 添加渠道

1. 进入"渠道管理"页面
2. 点击"添加渠道"
3. 填写以下信息：

#### 基础配置
- **渠道名称**: OpenAI Sora
- **渠道类型**: 选择 `OpenAI`
- **状态**: 启用

#### API 配置
- **Base URL**: `https://api.openai.com`
- **API Key**: 您的 OpenAI API Key（以 sk- 开头）

#### 模型配置
在"模型"字段中添加：
```
sora-2
sora-2-pro
sora-2-remix
sora-2-pro-remix
```

或者使用通配符：
```
sora*
```

#### 用户组配置
- **用户组**: 选择需要访问的用户组（如 default, Lv1 等）

### 步骤 3: 保存并测试

保存渠道配置后，重新测试您的请求。

---

## 🔍 验证渠道配置

### 方法 1: 查看数据库

```sql
SELECT id, name, type, models FROM channels WHERE type = 15 AND status = 1;
```

### 方法 2: 后台界面

在"渠道管理"中查看是否有包含 sora 模型的渠道。

### 方法 3: 测试请求

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "test"
  }'
```

---

## 📝 渠道配置示例

### 最小配置（JSON 格式）

```json
{
  "name": "OpenAI Sora",
  "type": 15,
  "key": "sk-your-openai-api-key",
  "base_url": "https://api.openai.com",
  "models": "sora-2,sora-2-pro,sora-2-remix,sora-2-pro-remix",
  "status": 1,
  "groups": ["default"]
}
```

### 完整配置

```json
{
  "name": "OpenAI Sora Main",
  "type": 15,
  "key": "sk-your-openai-api-key",
  "base_url": "https://api.openai.com",
  "models": "sora-2,sora-2-pro,sora-2-remix,sora-2-pro-remix",
  "status": 1,
  "groups": ["default", "Lv1", "Lv2"],
  "priority": 0,
  "weight": 0
}
```

---

## 🎯 支持的模型列表

必须在渠道中配置以下模型：

| 模型名称 | 用途 | 是否必需 |
|---------|------|---------|
| `sora-2` | 标准视频生成 | ✅ 必需 |
| `sora-2-pro` | 专业视频生成 | ✅ 必需 |
| `sora-2-remix` | 标准 Remix | ✅ 必需 |
| `sora-2-pro-remix` | 专业 Remix | ✅ 必需 |

或使用通配符 `sora*` 匹配所有。

---

## ⚠️ 常见问题

### Q1: 渠道类型选什么？
**A**: 选择 `OpenAI` (type = 15)

### Q2: Base URL 填什么？
**A**: `https://api.openai.com` 或您的代理地址

### Q3: 模型字段怎么填？
**A**: 
```
sora-2,sora-2-pro,sora-2-remix,sora-2-pro-remix
```
或
```
sora*
```

### Q4: 用户组怎么配置？
**A**: 勾选需要访问的用户组，如 `default`, `Lv1` 等

### Q5: 配置后还是报错？
**A**: 
1. 检查渠道是否启用（status = 1）
2. 检查用户组是否正确
3. 重启服务（如有缓存）
4. 查看后台日志

---

## 🧪 配置完成后测试

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍"
  }'
```

**期望返回**：
```json
{
  "task_id": "video_xxx",
  "task_status": "succeed",
  "message": "Video generation request submitted successfully..."
}
```

---

## 📋 配置检查清单

配置完成后，请确认：
- [ ] 渠道已添加
- [ ] 渠道类型为 OpenAI
- [ ] Base URL 正确
- [ ] API Key 正确
- [ ] 模型列表包含 sora-2
- [ ] 渠道状态为"启用"
- [ ] 用户组配置正确
- [ ] 测试请求成功

---

**配置完成后就可以正常使用了！**

