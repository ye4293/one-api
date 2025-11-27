# Sora 视频查询功能实现方案

## 📋 需求分析

根据 OpenAI 官方文档，Sora 有两个查询接口：

### 1. 查询视频状态
```
GET /v1/videos/{video_id}
```
返回视频任务的状态信息，包括：
- `status`: queued, processing, completed, failed
- `model`, `size`, `seconds` 等信息

### 2. 下载视频内容
```
GET /v1/videos/{video_id}/content
```
- 完成时：返回 200 + 视频文件内容（MP4）
- 未完成时：返回 404 Not Found

## 🎯 实现方案

### 方案流程

```
客户端查询 (video_id)
    ↓
查询数据库获取原渠道信息
    ↓
调用 OpenAI: GET /v1/videos/{video_id}
    ↓
检查状态 (status)
    ↓
┌──────────┴──────────┐
│                     │
未完成              completed
│                     │
返回进度信息        调用 /content 下载
│                     ↓
GeneralFinal      下载视频文件
VideoResponse         ↓
                 上传到 Cloudflare R2
                      ↓
                  生成 URL
                      ↓
                 返回 GeneralFinal
                 VideoResponse
                 (包含 video_url)
```

### API 接口设计

#### 请求端点
```
GET /v1/videos/query/{video_id}
```

或者统一使用：
```
POST /v1/videos/query
Body: {"video_id": "xxx"}
```

#### 响应格式（统一使用 GeneralFinalVideoResponse）

**进行中：**
```json
{
  "task_id": "video_123",
  "video_id": "",
  "task_status": "processing",
  "message": "Video is still processing",
  "duration": "5",
  "video_result": ""
}
```

**已完成：**
```json
{
  "task_id": "video_123",
  "video_id": "video_123",
  "task_status": "success",
  "message": "Video completed and uploaded to R2",
  "duration": "5",
  "video_result": "https://file.ezlinkai.com/123_video.mp4"
}
```

## 🏗️ 技术实现

### 1. 核心函数

```go
// handleSoraQueryRequest - 查询 Sora 视频状态和下载
func handleSoraQueryRequest(c *gin.Context, ctx context.Context, videoId string) *model.ErrorWithStatusCode {
    // 1. 查询数据库获取原视频记录
    videoTask, err := dbmodel.GetVideoTaskByVideoId(videoId)
    
    // 2. 获取原渠道信息
    channel, err := dbmodel.GetChannelById(videoTask.ChannelId, true)
    
    // 3. 调用 OpenAI 查询状态
    statusResp := queryVideoStatus(channel, videoId)
    
    // 4. 检查状态
    if statusResp.Status == "completed" {
        // 下载视频
        videoData := downloadVideoContent(channel, videoId)
        
        // 上传到 R2
        videoUrl := uploadVideoToR2(videoData, userId)
        
        // 返回完成响应
        return buildCompletedResponse(videoId, videoUrl, statusResp)
    } else {
        // 返回进度响应
        return buildProgressResponse(videoId, statusResp)
    }
}

// queryVideoStatus - 查询视频状态
func queryVideoStatus(channel *Channel, videoId string) (*openai.SoraVideoResponse, error) {
    url := fmt.Sprintf("%s/v1/videos/%s", channel.BaseURL, videoId)
    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("Authorization", "Bearer "+channel.Key)
    
    resp, err := http.DefaultClient.Do(req)
    // 解析响应...
    return soraResp, nil
}

// downloadVideoContent - 下载视频内容
func downloadVideoContent(channel *Channel, videoId string) ([]byte, error) {
    url := fmt.Sprintf("%s/v1/videos/%s/content", channel.BaseURL, videoId)
    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("Authorization", "Bearer "+channel.Key)
    
    resp, err := http.DefaultClient.Do(req)
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("video not ready")
    }
    
    return io.ReadAll(resp.Body)
}

// uploadVideoToR2 - 上传视频到 Cloudflare R2
func uploadVideoToR2(videoData []byte, userId int) (string, error) {
    // 转换为 base64
    base64Data := base64.StdEncoding.EncodeToString(videoData)
    
    // 使用现有的上传函数
    return UploadVideoBase64ToR2(base64Data, userId, "mp4")
}
```

### 2. 路由配置

需要在路由中添加查询接口：

```go
// router/relay.go 或相关路由文件
relayGroup.POST("/videos/query", VideoQueryHandler)
relayGroup.GET("/videos/query/:video_id", VideoQueryHandler)
```

### 3. 数据结构

需要使用现有的 `GeneralFinalVideoResponse`：

```go
type GeneralFinalVideoResponse struct {
    TaskId       string            `json:"task_id"`
    VideoResult  string            `json:"video_result,omitempty"`
    VideoResults []VideoResultItem `json:"video_results,omitempty"`
    VideoId      string            `json:"video_id"`
    TaskStatus   string            `json:"task_status"`
    Message      string            `json:"message"`
    Duration     string            `json:"duration"`
}
```

## ⚠️ 注意事项

### 1. 状态映射

OpenAI 状态 → 系统状态：
- `queued` → `processing`
- `processing` → `processing`
- `completed` → `success`
- `failed` → `failed`

### 2. 错误处理

- video_id 不存在 → 返回 404
- 原渠道不存在 → 返回错误
- 下载失败（404） → 返回进度信息
- R2 上传失败 → 返回原始 URL 或重试

### 3. 性能优化

- 下载大文件时使用流式处理
- 设置合理的超时时间（视频下载可能较慢）
- 考虑添加缓存（已下载的视频）

### 4. 安全性

- 验证 video_id 所有权（用户只能查询自己的视频）
- 使用原渠道的 Key，避免权限问题

## 📊 与其他服务对比

### 阿里云视频查询
- 使用 task_id 查询
- 返回 job 信息
- 完成后提供下载 URL

### 可灵视频查询
- 使用 task_id 查询
- 返回状态和进度
- 完成后返回视频 URL

### Sora（本实现）
- 使用 video_id 查询
- 先查状态，再下载
- 上传到 R2 提供稳定 URL

## 🧪 测试用例

### 测试 1: 查询进行中的视频
```bash
curl -X POST http://localhost:3000/v1/videos/query \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{"video_id": "video_processing"}'
  
# 期望返回
{
  "task_id": "video_processing",
  "task_status": "processing",
  "message": "Video is still processing (progress: 50%)"
}
```

### 测试 2: 查询已完成的视频
```bash
curl -X POST http://localhost:3000/v1/videos/query \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{"video_id": "video_completed"}'
  
# 期望返回
{
  "task_id": "video_completed",
  "video_result": "https://file.ezlinkai.com/123_video.mp4",
  "task_status": "success",
  "duration": "5"
}
```

## 📝 实现步骤

1. ✅ 分析需求和设计方案
2. ⏳ 创建 Sora 查询处理函数
3. ⏳ 实现状态查询逻辑
4. ⏳ 实现视频下载逻辑
5. ⏳ 集成 R2 上传功能
6. ⏳ 统一响应格式
7. ⏳ 添加路由配置
8. ⏳ 测试和文档

## 🎯 下一步

开始实现核心函数，您觉得这个方案如何？是否需要调整？

