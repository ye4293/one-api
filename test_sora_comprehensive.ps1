# Sora 视频生成综合测试脚本 (PowerShell)
# 测试原生 form-data 和 JSON 两种格式，以及 input_reference 的不同用法

param(
    [string]$ApiEndpoint = "http://localhost:3000",
    [string]$ApiKey = "your_api_key_here"
)

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "Sora 视频生成 - 综合测试" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "API Endpoint: $ApiEndpoint"
Write-Host ""

$headers = @{
    "Authorization" = "Bearer $ApiKey"
}

# 测试 1: JSON 格式 - 基础文本生成视频
Write-Host "测试 1: JSON 格式 - 基础文本生成视频" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body1 = @{
    model = "sora-2"
    prompt = "一只可爱的小猫在草地上玩耍"
    seconds = 5
    size = "720x1280"
} | ConvertTo-Json

try {
    $response1 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/generations" -Method Post -Headers $headers -Body $body1 -ContentType "application/json"
    $response1 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 2: JSON 格式 - 使用 URL 参考图片
Write-Host "测试 2: JSON 格式 - 使用 URL 参考图片" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body2 = @{
    model = "sora-2-pro"
    prompt = "基于这张图片生成动态视频"
    seconds = 5
    size = "1280x720"
    input_reference = "https://example.com/sample-image.jpg"
} | ConvertTo-Json

try {
    $response2 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/generations" -Method Post -Headers $headers -Body $body2 -ContentType "application/json"
    $response2 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 3: JSON 格式 - 高清分辨率
Write-Host "测试 3: JSON 格式 - 高清分辨率" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

$body3 = @{
    model = "sora-2-pro"
    prompt = "壮丽的山脉日出景色，云雾缭绕，金色阳光"
    seconds = 10
    size = "1792x1024"
} | ConvertTo-Json

try {
    $response3 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/generations" -Method Post -Headers $headers -Body $body3 -ContentType "application/json"
    $response3 | ConvertTo-Json -Depth 10
} catch {
    Write-Host "错误: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host ""

# 测试 4: JSON 格式 - 使用 Base64 编码的图片
Write-Host "测试 4: JSON 格式 - 使用 Base64 编码的图片" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow

if (Test-Path "test_image.jpg") {
    $imageBytes = [System.IO.File]::ReadAllBytes("test_image.jpg")
    $base64Image = [System.Convert]::ToBase64String($imageBytes)
    $dataUrl = "data:image/jpeg;base64,$base64Image"
    
    $body4 = @{
        model = "sora-2-pro"
        prompt = "基于这张图片生成动态视频"
        seconds = 5
        size = "1280x720"
        input_reference = $dataUrl
    } | ConvertTo-Json
    
    try {
        $response4 = Invoke-RestMethod -Uri "$ApiEndpoint/v1/videos/generations" -Method Post -Headers $headers -Body $body4 -ContentType "application/json"
        $response4 | ConvertTo-Json -Depth 10
    } catch {
        Write-Host "错误: $_" -ForegroundColor Red
    }
} else {
    Write-Host "跳过 (缺少 test_image.jpg)" -ForegroundColor Gray
}

Write-Host ""
Write-Host ""

# 测试 5: Form-data 格式（使用 PowerShell 的 multipart/form-data）
Write-Host "测试 5: Form-data 格式" -ForegroundColor Yellow
Write-Host "-----------------------------------------" -ForegroundColor Yellow
Write-Host "注意: PowerShell 的 Invoke-RestMethod 对 multipart/form-data 支持有限" -ForegroundColor Gray
Write-Host "建议使用 curl 或其他工具测试 form-data 格式" -ForegroundColor Gray

Write-Host ""
Write-Host ""
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "测试完成" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "注意事项：" -ForegroundColor Yellow
Write-Host "1. 请确保设置了正确的 API_ENDPOINT 和 API_KEY"
Write-Host "2. seconds 字段使用的是官方字段名（不是 duration）"
Write-Host "3. input_reference 支持 URL、base64、data URL 三种格式"
Write-Host "4. form-data 格式是原生格式，性能更好"
Write-Host "5. JSON 格式会自动转换为 form-data 发送"
Write-Host ""
Write-Host "使用方法：" -ForegroundColor Green
Write-Host ".\test_sora_comprehensive.ps1 -ApiEndpoint 'http://localhost:3000' -ApiKey 'your_api_key'"

