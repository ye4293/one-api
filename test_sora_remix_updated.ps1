# Sora Remix 功能测试脚本 (使用 model 参数识别) (PowerShell)

param(
    [string]$ApiEndpoint = "http://localhost:3000",
    [string]$ApiKey = "your_api_key_here",
    [string]$VideoId = "video_123"
)

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "Sora Remix 功能测试（model参数方式）" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "API Endpoint: $ApiEndpoint"
Write-Host "Original Video ID: $VideoId"
Write-Host ""
Write-Host "说明：" -ForegroundColor Yellow
Write-Host "- 使用 model: 'sora-2-remix' 来识别 remix 请求"
Write-Host "- 系统会自动去掉 model 参数后发送给 OpenAI"
Write-Host "- 只有 prompt 会被发送到 OpenAI API"
Write-Host ""

$headers = @{
    "Content-Type" = "application/json"
    "Authorization" = "Bearer $ApiKey"
}

# 测试 1: 使用 sora-2-remix 模型
Write-Host "测试 1: 基础 Remix 请求 (model: sora-2-remix)" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow
Write-Host "请求: 延长场景，猫向观众鞠躬"

$body1 = @{
    model = "sora-2-remix"
    video_id = $VideoId
    prompt = "Extend the scene with the cat taking a bow to the cheering audience"
} | ConvertTo-Json

try {
    $response1 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos" -Method Post -Headers $headers -Body $body1
    $response1 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
    if ($_.Exception.Response) {
        $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
        $reader.BaseStream.Position = 0
        $responseBody = $reader.ReadToEnd()
        Write-Host "响应: $responseBody" -ForegroundColor Red
    }
}

Write-Host ""
Write-Host ""

# 测试 2: 使用 sora-2-pro-remix 模型
Write-Host "测试 2: Pro 版本 Remix (model: sora-2-pro-remix)" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow
Write-Host "请求: 转换为夜晚霓虹灯效果"

$body2 = @{
    model = "sora-2-pro-remix"
    video_id = $VideoId
    prompt = "Transform to nighttime with neon lights and vibrant colors"
} | ConvertTo-Json

try {
    $response2 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos" -Method Post -Headers $headers -Body $body2
    $response2 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 3: 添加新元素
Write-Host "测试 3: 添加新元素" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow
Write-Host "请求: 添加奔跑的小狗"

$body3 = @{
    model = "sora-2-remix"
    video_id = $VideoId
    prompt = "Add a playful puppy running across the field"
} | ConvertTo-Json

try {
    $response3 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos" -Method Post -Headers $headers -Body $body3
    $response3 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 4: 错误处理 - 不存在的 video_id
Write-Host "测试 4: 错误处理 - 不存在的 video_id" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body4 = @{
    model = "sora-2-remix"
    video_id = "video_nonexistent_999"
    prompt = "Try to remix a non-existent video"
} | ConvertTo-Json

try {
    $response4 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos" -Method Post -Headers $headers -Body $body4
    $response4 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "预期错误 (video_id 不存在):" -ForegroundColor Gray
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
Write-Host "使用说明：" -ForegroundColor Yellow
Write-Host ".\test_sora_remix_updated.ps1 -ApiEndpoint 'http://localhost:3000' -ApiKey 'your_key' -VideoId 'video_123'"
Write-Host ""
Write-Host "请求格式：" -ForegroundColor Green
Write-Host "- 必需参数："
Write-Host "  * model: 'sora-2-remix' 或 'sora-2-pro-remix'"
Write-Host "  * video_id: 原视频的任务ID"
Write-Host "  * prompt: 新的描述"
Write-Host ""
Write-Host "处理流程：" -ForegroundColor Green
Write-Host "1. 系统根据 model 参数识别这是 remix 请求"
Write-Host "2. 查找原视频的渠道信息"
Write-Host "3. 构建请求时去掉 model 和 video_id 参数"
Write-Host "4. 只发送 prompt 到 OpenAI: POST /v1/videos/{video_id}/remix"
Write-Host "5. 根据响应的 model, size, seconds 进行计费"


