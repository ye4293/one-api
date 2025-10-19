# Sora 视频生成快速开始指南

## 简介

Sora 是 OpenAI 推出的文本生成视频模型。本系统已经集成了 Sora API，支持透传请求、自动计费和统一响应格式。

## 支持的模型

| 模型 | 描述 | 支持的分辨率 |
|------|------|-------------|
| sora-2 | 标准版本 | 720x1280, 1280x720 |
| sora-2-pro | 专业版本 | 720x1280, 1280x720, 1024x1792, 1792x1024 |

## 快速开始

### 1. 配置渠道

在系统管理后台添加 OpenAI 渠道：
- **渠道名称**：OpenAI Sora
- **渠道类型**：OpenAI
- **Base URL**：`https://api.openai.com`
- **API Key**：你的 OpenAI API Key
- **模型**：添加 `sora-2` 和 `sora-2-pro`

### 2. 发送请求

#### 最简单的请求

```bash
curl -X POST https://your-api-endpoint/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍"
  }'
```

**响应示例：**
```json
{
  "task_id": "vid_abc123",
  "task_status": "succeed",
  "message": "Video generation request submitted successfully, task_id: vid_abc123"
}
```

#### 完整参数请求

```bash
curl -X POST https://your-api-endpoint/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2-pro",
    "prompt": "壮丽的山脉日出景色，云雾缭绕，金色阳光洒在山峰上",
    "duration": 10,
    "size": "1792x1024"
  }'
```

### 3. 查询视频状态（即将支持）

```bash
curl -X GET https://your-api-endpoint/v1/videos/{task_id} \
  -H "Authorization: Bearer YOUR_API_KEY"
```

## 请求参数说明

| 参数 | 类型 | 必填 | 说明 | 默认值 |
|------|------|------|------|--------|
| model | string | 是 | 模型名称：sora-2 或 sora-2-pro | - |
| prompt | string | 是 | 视频描述文本 | - |
| duration | integer | 否 | 视频时长（秒），范围 1-10 | 5 |
| size | string | 否 | 视频分辨率 | 720x1280 |

### 支持的分辨率

**sora-2 支持：**
- `720x1280` (纵向, Portrait)
- `1280x720` (横向, Landscape)

**sora-2-pro 额外支持：**
- `1024x1792` (高清纵向)
- `1792x1024` (高清横向)

## 定价说明

系统会根据以下规则自动计费：

| 模型 | 分辨率类型 | 价格/秒 |
|------|-----------|---------|
| sora-2 | 标准 (720x1280, 1280x720) | $0.10 |
| sora-2-pro | 标准 (720x1280, 1280x720) | $0.30 |
| sora-2-pro | 高清 (1024x1792, 1792x1024) | $0.50 |

### 计费示例

**示例 1：标准视频**
- 模型：sora-2
- 分辨率：720x1280
- 时长：5秒
- **费用**：5 × $0.10 = **$0.50**

**示例 2：专业版标准分辨率**
- 模型：sora-2-pro
- 分辨率：1280x720
- 时长：10秒
- **费用**：10 × $0.30 = **$3.00**

**示例 3：专业版高清**
- 模型：sora-2-pro
- 分辨率：1792x1024
- 时长：10秒
- **费用**：10 × $0.50 = **$5.00**

## 使用技巧

### 1. 编写有效的提示词

**好的提示词：**
```
"一只橙色的小猫在阳光明媚的草地上追逐蝴蝶，背景是蓝天白云"
```

**不太好的提示词：**
```
"猫"
```

### 2. 选择合适的分辨率

- **社交媒体短视频**：使用 720x1280 (纵向)
- **YouTube 横屏**：使用 1280x720 或 1792x1024
- **高质量展示**：使用 sora-2-pro 的高清分辨率

### 3. 控制成本

- 从 sora-2 开始测试
- 使用较短的时长进行预览
- 满意后再使用 sora-2-pro 生成最终版本

## 测试脚本

### Bash 脚本

项目根目录下的 `test_sora_request.sh`：

```bash
bash test_sora_request.sh
```

### PowerShell 脚本

项目根目录下的 `test_sora_request.ps1`：

```powershell
.\test_sora_request.ps1
```

**使用前请修改脚本中的 API_ENDPOINT 和 API_KEY**

## Python 示例

```python
import requests
import json

API_ENDPOINT = "http://localhost:3000"
API_KEY = "your_api_key_here"

def generate_video(prompt, model="sora-2", duration=5, size="720x1280"):
    url = f"{API_ENDPOINT}/v1/videos/generations"
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {API_KEY}"
    }
    data = {
        "model": model,
        "prompt": prompt,
        "duration": duration,
        "size": size
    }
    
    response = requests.post(url, headers=headers, json=data)
    return response.json()

# 使用示例
result = generate_video(
    prompt="一只可爱的小猫在草地上玩耍",
    model="sora-2",
    duration=5,
    size="720x1280"
)

print(json.dumps(result, indent=2, ensure_ascii=False))
```

## JavaScript/Node.js 示例

```javascript
const axios = require('axios');

const API_ENDPOINT = 'http://localhost:3000';
const API_KEY = 'your_api_key_here';

async function generateVideo(prompt, model = 'sora-2', duration = 5, size = '720x1280') {
  const url = `${API_ENDPOINT}/v1/videos/generations`;
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${API_KEY}`
  };
  const data = {
    model,
    prompt,
    duration,
    size
  };
  
  try {
    const response = await axios.post(url, data, { headers });
    return response.data;
  } catch (error) {
    console.error('Error:', error.response?.data || error.message);
    throw error;
  }
}

// 使用示例
generateVideo(
  '一只可爱的小猫在草地上玩耍',
  'sora-2',
  5,
  '720x1280'
).then(result => {
  console.log(JSON.stringify(result, null, 2));
});
```

## 常见问题

### Q: 如何获取生成的视频？
A: 响应中会返回 `task_id`，使用这个 ID 查询视频状态和下载链接（即将支持）。

### Q: 视频生成需要多长时间？
A: 通常需要几分钟，具体取决于视频时长和复杂度。

### Q: 余额不足怎么办？
A: 系统会在请求前检查余额，如果不足会返回错误。请联系管理员充值。

### Q: 支持哪些语言的提示词？
A: 支持中文和英文，建议使用详细的描述以获得更好的效果。

### Q: 可以上传图片作为首帧吗？
A: 当前版本仅支持文本生成视频，图片功能即将支持。

## 错误码说明

| 错误码 | 说明 | 解决方法 |
|--------|------|----------|
| insufficient_balance | 余额不足 | 充值账户 |
| invalid_request | 无效请求 | 检查请求参数 |
| invalid_prompt | 无效提示词 | 修改提示词内容 |
| rate_limit_exceeded | 超出速率限制 | 稍后重试 |

## 最佳实践

1. **详细的提示词**：包含场景、动作、风格等细节
2. **合理的时长**：建议 5-10 秒，过长可能影响质量
3. **适当的分辨率**：根据用途选择合适的分辨率
4. **测试优先**：使用 sora-2 测试效果后再用 sora-2-pro
5. **余额监控**：定期检查账户余额

## 更多资源

- [OpenAI Sora 官方文档](https://platform.openai.com/docs/api-reference/videos/create)
- [完整实现文档](./SORA_VIDEO_IMPLEMENTATION.md)
- [示例视频库](./examples/) (即将添加)

## 技术支持

如有问题，请：
1. 查看日志文件
2. 检查 API Key 是否正确
3. 确认余额充足
4. 联系技术支持团队

