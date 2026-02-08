# 通义万相 Wan 2.6 视频生成 API 调用文档

## 概述

通义万相 Wan 系列视频模型支持基于文本提示词生成流畅视频，支持多种分辨率、时长和音频配置。请求体采用**阿里云 DashScope 原生格式透传**，响应体和请求路径统一为 EzlinkAI 标准格式。

### 支持的模型

| 模型名称 | 说明 | 分辨率 | 时长 |
|----------|------|--------|------|
| `wan2.6-t2v` | Wan 2.6 文生视频 | 720P、1080P | 2-15秒 |
| `wan2.6-t2v-us` | Wan 2.6 文生视频（美区） | 720P、1080P | 5、10秒 |
| `wan2.5-t2v-preview` | Wan 2.5 文生视频预览 | 480P、720P、1080P | 5、10秒 |
| `wan2.2-t2v-plus` | Wan 2.2 文生视频增强 | 480P、1080P | 固定5秒 |
| `wanx2.1-t2v-turbo` | WanX 2.1 文生视频快速 | 480P、720P | 固定5秒 |
| `wanx2.1-t2v-plus` | WanX 2.1 文生视频增强 | 720P | 固定5秒 |

---

## API 端点

### 创建视频任务

**POST** `/v1/video/generations`

### 查询任务结果

**GET** `/v1/video/generations/result?taskid={task_id}`

---

## 请求格式（透传 DashScope 原生格式）

请求体直接透传至阿里云 DashScope API，格式与阿里云官方 API 保持一致。

### 1. 基础文生视频

```json
{
  "model": "wan2.6-t2v",
  "input": {
    "prompt": "一只小猫在月光下奔跑"
  },
  "parameters": {
    "size": "1280*720",
    "duration": 5,
    "prompt_extend": true
  }
}
```

### 2. 自定义音频视频

```json
{
  "model": "wan2.6-t2v",
  "input": {
    "prompt": "一幅史诗级可爱的场景。一只小巧可爱的卡通小猫将军...",
    "audio_url": "https://example.com/audio.mp3"
  },
  "parameters": {
    "size": "1280*720",
    "duration": 10,
    "prompt_extend": true,
    "watermark": false
  }
}
```

### 3. 多镜头叙事（仅 wan2.6 系列）

```json
{
  "model": "wan2.6-t2v",
  "input": {
    "prompt": "一幅史诗级可爱的场景..."
  },
  "parameters": {
    "size": "1280*720",
    "duration": 10,
    "prompt_extend": true,
    "shot_type": "multi"
  }
}
```

### 4. 使用反向提示词

```json
{
  "model": "wan2.2-t2v-plus",
  "input": {
    "prompt": "一只小猫在月光下奔跑",
    "negative_prompt": "低分辨率、错误、最差质量"
  },
  "parameters": {
    "size": "832*480"
  }
}
```

---

## 请求参数说明

### 顶层参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型名称，如 `wan2.6-t2v` |
| `input` | object | 是 | 输入信息 |
| `parameters` | object | 否 | 视频参数配置 |

### input 参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `prompt` | string | 是 | 文本提示词，描述视频内容。wan2.6/wan2.5 最长1500字符，wan2.2/wanx2.1 最长800字符 |
| `negative_prompt` | string | 否 | 反向提示词，排除不希望出现的内容，最长500字符 |
| `audio_url` | string | 否 | 音频文件 URL（仅 wan2.6 和 wan2.5 系列），支持 wav/mp3 格式，3~30秒，最大15MB |

### parameters 参数

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `size` | string | 否 | 1920*1080 | 视频分辨率，格式为 `宽*高`，详见分辨率列表 |
| `duration` | integer | 否 | 5 | 视频时长（秒），不同模型支持不同范围 |
| `prompt_extend` | boolean | 否 | true | 是否开启 prompt 智能改写 |
| `shot_type` | string | 否 | single | 镜头类型：`single`（单镜头）、`multi`（多镜头），仅 wan2.6 系列 |
| `watermark` | boolean | 否 | false | 是否添加水印 |
| `seed` | integer | 否 | 随机 | 随机数种子，范围 [0, 2147483647] |

### 分辨率列表

| 档位 | 可选分辨率（宽*高） | 宽高比 |
|------|---------------------|--------|
| **1080P** | `1920*1080` | 16:9 |
| | `1080*1920` | 9:16 |
| | `1440*1440` | 1:1 |
| | `1632*1248` | 4:3 |
| | `1248*1632` | 3:4 |
| **720P** | `1280*720` | 16:9 |
| | `720*1280` | 9:16 |
| | `960*960` | 1:1 |
| | `1088*832` | 4:3 |
| | `832*1088` | 3:4 |
| **480P** | `832*480` | 16:9 |
| | `480*832` | 9:16 |
| | `624*624` | 1:1 |

---

## 响应格式

### 任务创建成功

```json
{
  "task_id": "0385dc79-5ff8-4d82-bcb6-xxxxxx",
  "task_status": "succeed",
  "message": "Request submitted successfully, request_id: 4909100c-7b5a-9f92-bfe5-xxxxxx"
}
```

### 任务创建失败

```json
{
  "error": {
    "message": "The size is not match xxxxxx",
    "type": "api_error",
    "code": "InvalidParameter"
  }
}
```

### 查询结果 - 处理中

```json
{
  "task_id": "0385dc79-5ff8-4d82-bcb6-xxxxxx",
  "task_status": "processing",
  "message": "",
  "video_result": "",
  "video_results": [],
  "video_id": "0385dc79-5ff8-4d82-bcb6-xxxxxx",
  "duration": "5"
}
```

### 查询结果 - 完成

```json
{
  "task_id": "0385dc79-5ff8-4d82-bcb6-xxxxxx",
  "task_status": "succeed",
  "message": "Video generation completed, request_id: caa62a12-8841-41a6-8af2-xxxxxx",
  "video_result": "https://dashscope-result-bj.oss-accelerate.aliyuncs.com/xxx.mp4?Expires=xxx",
  "video_results": [
    {
      "url": "https://dashscope-result-bj.oss-accelerate.aliyuncs.com/xxx.mp4?Expires=xxx"
    }
  ],
  "video_id": "0385dc79-5ff8-4d82-bcb6-xxxxxx",
  "duration": "5"
}
```

### 查询结果 - 失败

```json
{
  "task_id": "0385dc79-5ff8-4d82-bcb6-xxxxxx",
  "task_status": "failed",
  "message": "视频生成失败: [ContentModerationFailed] 内容审核不通过 (request_id: xxx)",
  "video_result": "",
  "video_results": [],
  "video_id": "0385dc79-5ff8-4d82-bcb6-xxxxxx",
  "duration": "5"
}
```

### 任务状态说明

| 状态值 | 说明 |
|--------|------|
| `succeed` | 视频生成成功 |
| `processing` | 视频生成中（对应阿里云 PENDING/RUNNING） |
| `failed` | 视频生成失败（对应阿里云 FAILED/UNKNOWN） |

---

## 调用示例

> 端点：`https://api.ezlinkai.com`
> 认证：`Authorization: Bearer YOUR_API_KEY`

---

### 1. 创建文生视频任务（基础）

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer WakXaSHkrhKf5BXvA381B595A6024eE8810a9f90B52a7eD0" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "wan2.6-t2v",
    "input": {
      "prompt": "一只小猫在月光下奔跑"
    },
    "parameters": {
      "size": "1280*720",
      "duration": 5,
      "prompt_extend": true
    }
  }'
```

### 2. 创建文生视频任务（1080P + 10秒 + 自定义音频）

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer WakXaSHkrhKf5BXvA381B595A6024eE8810a9f90B52a7eD0" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "wan2.6-t2v",
    "input": {
      "prompt": "一幅史诗级可爱的场景。一只小巧可爱的卡通小猫将军，身穿细节精致的金色盔甲，头戴一个稍大的头盔，勇敢地站在悬崖上。",
      "audio_url": "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20250923/hbiayh/%E4%BB%8E%E5%86%9B%E8%A1%8C.mp3"
    },
    "parameters": {
      "size": "1920*1080",
      "duration": 10,
      "prompt_extend": true,
      "watermark": false,
      "seed": 12345
    }
  }'
```

### 3. 创建多镜头叙事视频（仅 wan2.6）

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer WakXaSHkrhKf5BXvA381B595A6024eE8810a9f90B52a7eD0" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "wan2.6-t2v",
    "input": {
      "prompt": "一幅史诗级可爱的场景。一只小巧可爱的卡通小猫将军，身穿细节精致的金色盔甲..."
    },
    "parameters": {
      "size": "1280*720",
      "duration": 10,
      "prompt_extend": true,
      "shot_type": "multi"
    }
  }'
```

### 4. 查询任务结果

```bash
curl -X GET "https://api.ezlinkai.com/v1/video/generations/result?taskid=YOUR_TASK_ID" \
  -H "Authorization: Bearer WakXaSHkrhKf5BXvA381B595A6024eE8810a9f90B52a7eD0"
```

---

### Python 完整示例

```python
import requests
import time

API_BASE = "https://api.ezlinkai.com"
API_KEY = "WakXaSHkrhKf5BXvA381B595A6024eE8810a9f90B52a7eD0"

headers = {
    "Authorization": f"Bearer {API_KEY}",
    "Content-Type": "application/json"
}

# ============================================
# 步骤1: 创建视频生成任务
# ============================================
print("正在创建视频生成任务...")

response = requests.post(
    f"{API_BASE}/v1/video/generations",
    headers=headers,
    json={
        "model": "wan2.6-t2v",
        "input": {
            "prompt": "一只小猫在月光下奔跑，画面唯美，月光洒在小猫身上，背景是星空和草地"
        },
        "parameters": {
            "size": "1280*720",
            "duration": 5,
            "prompt_extend": True,
            "watermark": False
        }
    }
)

result = response.json()
print(f"创建任务响应: {result}")

if "task_id" not in result or not result["task_id"]:
    print(f"任务创建失败: {result}")
    exit(1)

task_id = result["task_id"]
print(f"任务创建成功! task_id: {task_id}")

# ============================================
# 步骤2: 轮询查询结果（建议间隔15秒）
# ============================================
print("\n开始轮询查询结果...")

max_retries = 40  # 最多等待10分钟 (40 * 15秒)
for i in range(max_retries):
    time.sleep(15)  # 等待15秒

    response = requests.get(
        f"{API_BASE}/v1/video/generations/result",
        headers=headers,
        params={"taskid": task_id}
    )
    result = response.json()
    status = result.get("task_status", "unknown")
    print(f"[{i+1}/{max_retries}] 任务状态: {status}")

    if status == "succeed":
        video_url = result.get("video_result", "")
        print(f"\n视频生成成功!")
        print(f"视频URL: {video_url}")

        # 下载视频到本地
        if video_url:
            print("正在下载视频...")
            video_response = requests.get(video_url, stream=True, timeout=300)
            with open("output_video.mp4", "wb") as f:
                for chunk in video_response.iter_content(chunk_size=8192):
                    f.write(chunk)
            print("视频已保存到: output_video.mp4")
        break

    elif status == "failed":
        print(f"\n视频生成失败: {result.get('message', '未知错误')}")
        break

    else:
        print(f"  处理中... 继续等待")
else:
    print("\n超时：视频生成时间过长，请稍后手动查询")
```

---

### Node.js 完整示例

```javascript
const API_BASE = "https://api.ezlinkai.com";
const API_KEY = "WakXaSHkrhKf5BXvA381B595A6024eE8810a9f90B52a7eD0";

async function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function createVideoTask() {
  const response = await fetch(`${API_BASE}/v1/video/generations`, {
    method: "POST",
    headers: {
      "Authorization": `Bearer ${API_KEY}`,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      model: "wan2.6-t2v",
      input: {
        prompt: "一只小猫在月光下奔跑，画面唯美"
      },
      parameters: {
        size: "1280*720",
        duration: 5,
        prompt_extend: true,
        watermark: false
      }
    })
  });

  return await response.json();
}

async function queryVideoResult(taskId) {
  const response = await fetch(
    `${API_BASE}/v1/video/generations/result?taskid=${taskId}`,
    {
      headers: {
        "Authorization": `Bearer ${API_KEY}`
      }
    }
  );

  return await response.json();
}

async function main() {
  // 步骤1: 创建任务
  console.log("正在创建视频生成任务...");
  const createResult = await createVideoTask();
  console.log("创建任务响应:", JSON.stringify(createResult, null, 2));

  const taskId = createResult.task_id;
  if (!taskId) {
    console.error("任务创建失败:", createResult);
    return;
  }
  console.log(`任务创建成功! task_id: ${taskId}`);

  // 步骤2: 轮询查询结果
  console.log("\n开始轮询查询结果...");
  const maxRetries = 40;

  for (let i = 0; i < maxRetries; i++) {
    await sleep(15000); // 等待15秒

    const result = await queryVideoResult(taskId);
    const status = result.task_status;
    console.log(`[${i + 1}/${maxRetries}] 任务状态: ${status}`);

    if (status === "succeed") {
      console.log(`\n视频生成成功!`);
      console.log(`视频URL: ${result.video_result}`);
      break;
    } else if (status === "failed") {
      console.log(`\n视频生成失败: ${result.message}`);
      break;
    } else {
      console.log("  处理中... 继续等待");
    }
  }
}

main().catch(console.error);
```

---

## 注意事项

1. **异步任务**：视频生成是异步的，创建任务后返回 `task_id`，需轮询查询结果
2. **轮询间隔**：视频生成通常需要 1-5 分钟，建议每 **15 秒** 轮询一次
3. **请求体透传**：请求体直接透传至阿里云 DashScope API，格式与阿里云官方一致
4. **响应体统一**：响应体按照 EzlinkAI 统一格式返回（`task_id`、`task_status`、`message`）
5. **视频链接有效期**：阿里云返回的视频 URL 有效期为 **24 小时**，请及时下载保存
6. **size 影响计费**：`size` 参数直接影响费用，1080P > 720P > 480P
7. **duration 影响计费**：费用 = 单价（基于分辨率） × 时长（秒）
8. **音频说明**：
   - wan2.6 和 wan2.5 系列默认生成有声视频（自动配音）
   - 可通过 `audio_url` 传入自定义音频
   - wan2.2 和 wanx2.1 系列默认生成无声视频
9. **prompt_extend**：对较短的 prompt 建议开启，可显著提升生成效果，但会增加耗时
10. **自动重试**：任务创建失败时系统会自动重试其他渠道

---

## 快速测试流程

1. 使用 curl 或代码发送 POST 请求创建任务，获取返回的 `task_id`
2. 将 `task_id` 填入查询接口的 `taskid` 参数
3. 每隔 15 秒执行查询，直到 `task_status` 变为 `succeed` 或 `failed`
4. 成功后立即下载 `video_result` 中的视频 URL（24小时内有效）
