# 测试视频生成API
# 使用方法: .\test_video_api.ps1

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "测试视频生成 API" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# 配置参数
$baseUrl = "http://127.0.0.1:3000"
$endpoint = "/v1/video/generations"
$token = "BSKIUyOcr0s1cdyWA40e6eF11c974b188aFdFc0693Ff0959"

# 请求头
$headers = @{
    'Authorization' = "Bearer $token"
    'Content-Type' = 'application/json'
}

# 请求体
$body = @{
    model = "MiniMax-Hailuo-2.3"
    prompt = "A man picks up a book [Pedestal up], then reads [Static shot]."
    duration = 6
    resolution = "1080P"
} | ConvertTo-Json

# 显示请求信息
Write-Host "请求 URL: $baseUrl$endpoint" -ForegroundColor Yellow
Write-Host "请求方法: POST" -ForegroundColor Yellow
Write-Host "Token: $token" -ForegroundColor Yellow
Write-Host ""
Write-Host "请求体:" -ForegroundColor Yellow
Write-Host $body -ForegroundColor Gray
Write-Host ""
Write-Host "发送请求中..." -ForegroundColor Green
Write-Host ""

# 发送请求
try {
    $response = Invoke-WebRequest `
        -Uri "$baseUrl$endpoint" `
        -Method POST `
        -Headers $headers `
        -Body $body `
        -UseBasicParsing `
        -TimeoutSec 30
    
    # 成功响应
    Write-Host "✓ 请求成功!" -ForegroundColor Green
    Write-Host "状态码: $($response.StatusCode)" -ForegroundColor Green
    Write-Host ""
    Write-Host "响应内容:" -ForegroundColor Cyan
    
    # 尝试格式化 JSON
    try {
        $jsonResponse = $response.Content | ConvertFrom-Json
        Write-Host ($jsonResponse | ConvertTo-Json -Depth 10) -ForegroundColor White
    } catch {
        Write-Host $response.Content -ForegroundColor White
    }
    
} catch {
    # 错误处理
    Write-Host "✗ 请求失败!" -ForegroundColor Red
    Write-Host ""
    
    if ($_.Exception.Response) {
        $statusCode = $_.Exception.Response.StatusCode.value__
        Write-Host "状态码: $statusCode" -ForegroundColor Red
        Write-Host ""
        
        # 读取错误响应内容
        try {
            $stream = $_.Exception.Response.GetResponseStream()
            $reader = New-Object System.IO.StreamReader($stream)
            $responseBody = $reader.ReadToEnd()
            $reader.Close()
            $stream.Close()
            
            Write-Host "错误响应:" -ForegroundColor Yellow
            try {
                $errorJson = $responseBody | ConvertFrom-Json
                Write-Host ($errorJson | ConvertTo-Json -Depth 10) -ForegroundColor White
            } catch {
                Write-Host $responseBody -ForegroundColor White
            }
        } catch {
            Write-Host "无法读取错误响应内容" -ForegroundColor Red
        }
    } else {
        Write-Host "错误信息: $($_.Exception.Message)" -ForegroundColor Red
    }
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "测试完成" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan

