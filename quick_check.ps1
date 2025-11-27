# 快速检查脚本 - 一键查看当前状态
param(
    [string]$ApiUrl = "http://localhost:3000"
)

Write-Host ""
Write-Host "🔍 OneAPI 健康检查" -ForegroundColor Cyan
Write-Host "==================" -ForegroundColor Cyan
Write-Host ""

try {
    # 获取监控数据
    $response = Invoke-RestMethod -Uri "$ApiUrl/api/monitor/health" -Method Get -TimeoutSec 5
    
    $goroutines = $response.goroutines
    $allocMb = $response.memory.alloc_mb
    $sysMb = $response.memory.sys_mb
    
    # 显示数据
    Write-Host "📊 Goroutines: " -NoNewline
    if ($goroutines -gt 5000) {
        Write-Host "$goroutines 🔴" -ForegroundColor Red
        $status = "危险"
        $statusColor = "Red"
    } elseif ($goroutines -gt 2000) {
        Write-Host "$goroutines 🟡" -ForegroundColor Yellow
        $status = "警告"
        $statusColor = "Yellow"
    } else {
        Write-Host "$goroutines 🟢" -ForegroundColor Green
        $status = "正常"
        $statusColor = "Green"
    }
    
    Write-Host "💾 内存使用: ${allocMb}MB / ${sysMb}MB" -ForegroundColor Cyan
    Write-Host "📈 状态: $status" -ForegroundColor $statusColor
    Write-Host ""
    
    # 健康评分
    if ($goroutines -lt 1000 -and $allocMb -lt 1024) {
        Write-Host "✅ 系统运行良好！" -ForegroundColor Green
        exit 0
    } elseif ($goroutines -lt 3000 -and $allocMb -lt 2048) {
        Write-Host "✅ 系统运行正常" -ForegroundColor Green
        exit 0
    } elseif ($goroutines -lt 5000) {
        Write-Host "⚠️ 系统负载较高，请持续观察" -ForegroundColor Yellow
        exit 0
    } else {
        Write-Host "🔴 系统状态异常，请检查日志！" -ForegroundColor Red
        exit 1
    }
    
} catch {
    Write-Host "❌ 无法连接到 API: $ApiUrl" -ForegroundColor Red
    Write-Host "   错误: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ""
    Write-Host "请检查：" -ForegroundColor Yellow
    Write-Host "1. 服务是否运行: docker ps | Select-String one-api" -ForegroundColor Yellow
    Write-Host "2. URL是否正确: $ApiUrl" -ForegroundColor Yellow
    Write-Host "3. 防火墙是否放行" -ForegroundColor Yellow
    Write-Host ""
    exit 1
}

<#
.SYNOPSIS
    快速检查 OneAPI 服务健康状态

.EXAMPLE
    .\quick_check.ps1
    
.EXAMPLE
    .\quick_check.ps1 -ApiUrl "http://192.168.1.100:3000"
#>

