# 查询视频生成结果
# 使用方法: .\test_query_video.ps1

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "查询视频生成结果" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# 配置参数
$baseUrl = "http://127.0.0.1:3000"
$endpoint = "/v1/video/generations/result"
$token = "BSKIUyOcr0s1cdyWA40e6eF11c974b188aFdFc0693Ff0959"
$taskId = "333141730558392"
$responseFormat = "url"

# 构建完整的 URL（带查询参数）
$fullUrl = "$baseUrl$endpoint`?taskid=$taskId&response_format=$responseFormat"

# 请求头
$headers = @{
    'Authorization' = "Bearer $token"
}

# 显示请求信息
Write-Host "请求 URL: $fullUrl" -ForegroundColor Yellow
Write-Host "请求方法: GET" -ForegroundColor Yellow
Write-Host "Token: $token" -ForegroundColor Yellow
Write-Host "Task ID: $taskId" -ForegroundColor Yellow
Write-Host "Response Format: $responseFormat" -ForegroundColor Yellow
Write-Host ""
Write-Host "发送请求中..." -ForegroundColor Green
Write-Host ""

# 发送请求
try {
    $response = Invoke-WebRequest `
        -Uri $fullUrl `
        -Method GET `
        -Headers $headers `
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
        
        # 如果有 video_url，单独显示
        if ($jsonResponse.video_url) {
            Write-Host ""
            Write-Host "视频 URL:" -ForegroundColor Green
            Write-Host $jsonResponse.video_url -ForegroundColor Cyan
        }
        
        # 如果有 task_status，单独显示
        if ($jsonResponse.task_status) {
            Write-Host ""
            Write-Host "任务状态: $($jsonResponse.task_status)" -ForegroundColor Yellow
        }
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
Write-Host "查询完成" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan

