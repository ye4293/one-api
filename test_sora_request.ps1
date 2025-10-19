# Sora 视频生成测试脚本 (PowerShell)
# 使用方法: .\test_sora_request.ps1

# 配置
$API_ENDPOINT = "http://localhost:3000"  # 修改为你的 API 地址
$API_KEY = "your_api_key_here"           # 修改为你的 API Key

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "Sora 视频生成 API 测试" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host ""

# 测试 1: sora-2 标准分辨率
Write-Host "测试 1: sora-2 模型，720x1280 分辨率，5秒" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body1 = @{
    model = "sora-2"
    prompt = "一只可爱的小猫在草地上玩耍"
    duration = 5
    size = "720x1280"
} | ConvertTo-Json

$headers = @{
    "Content-Type" = "application/json"
    "Authorization" = "Bearer $API_KEY"
}

try {
    $response1 = Invoke-RestMethod -Uri "$API_ENDPOINT/v1/videos/generations" -Method Post -Headers $headers -Body $body1
    $response1 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 2: sora-2-pro 标准分辨率
Write-Host "测试 2: sora-2-pro 模型，1280x720 分辨率，10秒" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body2 = @{
    model = "sora-2-pro"
    prompt = "壮丽的山脉日出景色，云雾缭绕"
    duration = 10
    size = "1280x720"
} | ConvertTo-Json

try {
    $response2 = Invoke-RestMethod -Uri "$API_ENDPOINT/v1/videos/generations" -Method Post -Headers $headers -Body $body2
    $response2 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 3: sora-2-pro 高分辨率
Write-Host "测试 3: sora-2-pro 模型，1792x1024 分辨率，5秒" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body3 = @{
    model = "sora-2-pro"
    prompt = "城市夜景，车水马龙，霓虹灯闪烁"
    duration = 5
    size = "1792x1024"
} | ConvertTo-Json

try {
    $response3 = Invoke-RestMethod -Uri "$API_ENDPOINT/v1/videos/generations" -Method Post -Headers $headers -Body $body3
    $response3 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "测试完成" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan

