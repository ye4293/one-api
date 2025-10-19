# Sora 视频查询功能测试脚本 (PowerShell)

param(
    [string]$ApiEndpoint = "http://localhost:3000",
    [string]$ApiKey = "your_api_key_here",
    [string]$TaskId = "video_123"
)

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "Sora 视频查询功能测试" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "API Endpoint: $ApiEndpoint"
Write-Host "Task ID: $TaskId"
Write-Host ""
Write-Host "统一查询地址: /v1/video/generations/result" -ForegroundColor Yellow
Write-Host ""

$headers = @{
    "Content-Type" = "application/json"
    "Authorization" = "Bearer $ApiKey"
}

# 测试 1: 查询视频状态
Write-Host "测试 1: 查询 Sora 视频状态" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow
Write-Host "请求: 查询任务 $TaskId 的状态"

$body1 = @{
    task_id = $TaskId
} | ConvertTo-Json

try {
    $response1 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/video/generations/result" -Method Post -Headers $headers -Body $body1
    Write-Host "响应:" -ForegroundColor Green
    $response1 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
    if ($_.Exception.Response) {
        $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
        $reader.BaseStream.Position = 0
        $responseBody = $reader.ReadToEnd()
        Write-Host "响应体: $responseBody" -ForegroundColor Red
    }
}

Write-Host ""
Write-Host ""

# 测试 2: 查询另一个视频
Write-Host "测试 2: 查询另一个视频（可能已完成）" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$TaskId2 = if ($env:TASK_ID_2) { $env:TASK_ID_2 } else { "video_456" }

$body2 = @{
    task_id = $TaskId2
} | ConvertTo-Json

try {
    $response2 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/video/generations/result" -Method Post -Headers $headers -Body $body2
    Write-Host "响应:" -ForegroundColor Green
    $response2 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 3: 查询不存在的视频
Write-Host "测试 3: 错误处理 - 查询不存在的视频" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body3 = @{
    task_id = "video_nonexistent_999"
} | ConvertTo-Json

try {
    $response3 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/video/generations/result" -Method Post -Headers $headers -Body $body3
    $response3 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "预期错误 (task_id 不存在):" -ForegroundColor Gray
    if ($_.Exception.Response) {
        $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
        $reader.BaseStream.Position = 0
        $responseBody = $reader.ReadToEnd()
        Write-Host $responseBody -ForegroundColor Gray
    }
}

Write-Host ""
Write-Host ""
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "测试完成" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "响应格式说明：" -ForegroundColor Yellow
Write-Host ""
Write-Host "进行中的视频：" -ForegroundColor Green
Write-Host @"
{
  "task_id": "video_123",
  "task_status": "processing",
  "message": "Video generation in progress (50%)",
  "duration": "5"
}
"@
Write-Host ""
Write-Host "已完成的视频（首次查询）：" -ForegroundColor Green
Write-Host @"
{
  "task_id": "video_123",
  "video_result": "https://file.ezlinkai.com/123_video.mp4",
  "task_status": "succeed",
  "message": "Video generation completed and uploaded to R2",
  "duration": "5"
}
"@
Write-Host ""
Write-Host "已完成的视频（缓存）：" -ForegroundColor Green
Write-Host @"
{
  "task_id": "video_123",
  "video_result": "https://file.ezlinkai.com/123_video.mp4",
  "task_status": "succeed",
  "message": "Video retrieved from cache",
  "duration": "5"
}
"@
Write-Host ""
Write-Host "处理流程：" -ForegroundColor Yellow
Write-Host "1. 检查数据库 storeurl - 如有缓存直接返回"
Write-Host "2. 调用 OpenAI GET /v1/videos/{id} 查询状态"
Write-Host "3. 如果 status = 'completed':"
Write-Host "   a. 调用 GET /v1/videos/{id}/content 下载视频"
Write-Host "   b. 上传到 Cloudflare R2"
Write-Host "   c. 保存 URL 到数据库 storeurl"
Write-Host "   d. 返回 URL"
Write-Host "4. 如果未完成，返回当前状态和进度"
Write-Host ""
Write-Host "使用方法：" -ForegroundColor Green
Write-Host ".\test_sora_query.ps1 -ApiEndpoint 'http://localhost:3000' -ApiKey 'your_key' -TaskId 'video_123'"

