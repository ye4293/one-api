# Sora 快速使用指南

## 🚀 三个核心功能

### 1️⃣ 生成视频

```bash
# 最简单的方式（JSON）
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "seconds": 5
  }'

# 响应
{
  "task_id": "video_abc123",
  "task_status": "succeed",
  "message": "Video generation request submitted successfully..."
}
```

### 2️⃣ Remix 视频

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2-remix",
    "video_id": "video_abc123",
    "prompt": "Extend the scene with the cat taking a bow"
  }'

# 响应
{
  "task_id": "video_def456",
  "task_status": "succeed",
  "message": "Video remix request submitted successfully, remixed_from: video_abc123"
}
```

### 3️⃣ 查询视频

```bash
curl -X POST http://localhost:3000/v1/video/generations/result \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "task_id": "video_abc123"
  }'

# 响应（进行中）
{
  "task_id": "video_abc123",
  "task_status": "processing",
  "message": "Video generation in progress (45%)"
}

# 响应（已完成）
{
  "task_id": "video_abc123",
  "video_result": "https://file.ezlinkai.com/123_video.mp4",
  "task_status": "succeed",
  "message": "Video generation completed and uploaded to R2",
  "duration": "5"
}
```

## 💡 高级用法

### 使用参考图片（URL）

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2-pro",
    "prompt": "基于这张图片生成动态视频",
    "seconds": 5,
    "size": "1280x720",
    "input_reference": "https://example.com/image.jpg"
  }'
```

### 使用文件上传（form-data）

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -F "model=sora-2-pro" \
  -F "prompt=基于这张图片生成视频" \
  -F "seconds=5" \
  -F "size=1280x720" \
  -F "input_reference=@/path/to/image.jpg"
```

## 💰 定价

| 模型 | 分辨率 | 价格/秒 |
|------|--------|---------|
| sora-2 | 标准 | $0.10 |
| sora-2-pro | 标准 | $0.30 |
| sora-2-pro | 高清 | $0.50 |

## 📖 详细文档

- **完整实现**: `docs/SORA_UPDATED_IMPLEMENTATION.md`
- **Remix 功能**: `docs/SORA_REMIX_MODEL_PARAM.md`
- **功能确认**: `SORA_功能实现确认.md`

## 🧪 测试脚本

- `test_sora_comprehensive.sh/ps1` - 生成测试
- `test_sora_remix_updated.sh/ps1` - Remix 测试
- `test_sora_query.sh/ps1` - 查询测试

---

✅ **所有功能已完成并可直接使用！**

