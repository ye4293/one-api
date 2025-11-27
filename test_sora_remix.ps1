# Sora Remix 功能测试脚本 (PowerShell)

param(
    [string]$ApiEndpoint = "http://localhost:3000",
    [string]$ApiKey = "your_api_key_here",
    [string]$VideoId = "video_123"
)

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "Sora Remix 功能测试" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "API Endpoint: $ApiEndpoint"
Write-Host "Original Video ID: $VideoId"
Write-Host ""

$headers = @{
    "Content-Type" = "application/json"
    "Authorization" = "Bearer $ApiKey"
}

# 测试 1: 基础 Remix 请求
Write-Host "测试 1: 基础 Remix 请求" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow
Write-Host "请求: 延长场景，猫向观众鞠躬"

$body1 = @{
    video_id = $VideoId
    prompt = "Extend the scene with the cat taking a bow to the cheering audience"
} | ConvertTo-Json

try {
    $response1 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/remix" -Method Post -Headers $headers -Body $body1
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

# 测试 2: 不同的 Remix 描述
Write-Host "测试 2: 改变视频风格" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow
Write-Host "请求: 转换为夜晚霓虹灯效果"

$body2 = @{
    video_id = $VideoId
    prompt = "Transform to nighttime with neon lights and vibrant colors"
} | ConvertTo-Json

try {
    $response2 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/remix" -Method Post -Headers $headers -Body $body2
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
    video_id = $VideoId
    prompt = "Add a playful puppy running across the field"
} | ConvertTo-Json

try {
    $response3 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/remix" -Method Post -Headers $headers -Body $body3
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
    video_id = "video_nonexistent_999"
    prompt = "Try to remix a non-existent video"
} | ConvertTo-Json

try {
    $response4 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/remix" -Method Post -Headers $headers -Body $body4
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
Write-Host ".\test_sora_remix.ps1 -ApiEndpoint 'http://localhost:3000' -ApiKey 'your_key' -VideoId 'video_123'"
Write-Host ""
Write-Host "注意事项：" -ForegroundColor Yellow
Write-Host "- video_id 必须是系统中已存在的视频任务ID"
Write-Host "- 系统会自动使用原视频的渠道和密钥"
Write-Host "- 费用根据响应中的 model、size、seconds 计算"
Write-Host "- 只有成功响应（200）才会扣费"

