# Goroutine 实时监控脚本 (PowerShell 版本)
# 用于 Windows 环境监控线上服务

param(
    [string]$ApiUrl = "http://localhost:3000",
    [int]$Interval = 5
)

Write-Host "================================" -ForegroundColor Cyan
Write-Host "  Goroutine 实时监控" -ForegroundColor Cyan
Write-Host "================================" -ForegroundColor Cyan
Write-Host "API地址: $ApiUrl"
Write-Host "刷新间隔: ${Interval}秒"
Write-Host "按 Ctrl+C 停止监控"
Write-Host "================================"
Write-Host ""

$startTime = Get-Date
$maxGoroutines = 0
$minGoroutines = 999999

while ($true) {
    try {
        # 获取监控数据
        $response = Invoke-RestMethod -Uri "$ApiUrl/api/monitor/health" -Method Get -TimeoutSec 5
        
        # 计算运行时长
        $elapsed = (Get-Date) - $startTime
        $elapsedMin = [math]::Floor($elapsed.TotalMinutes)
        $elapsedSec = $elapsed.Seconds
        
        # 获取数据
        $goroutines = $response.goroutines
        $allocMb = $response.memory.alloc_mb
        $sysMb = $response.memory.sys_mb
        $numGc = $response.memory.num_gc
        
        # 更新统计
        if ($goroutines -gt $maxGoroutines) {
            $maxGoroutines = $goroutines
        }
        if ($goroutines -lt $minGoroutines) {
            $minGoroutines = $goroutines
        }
        
        # 显示标题
        $currentTime = Get-Date -Format "HH:mm:ss"
        Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Gray
        Write-Host "  实时监控 - [$currentTime] 运行时长: ${elapsedMin}分${elapsedSec}秒" -ForegroundColor Gray
        Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Gray
        Write-Host ""
        
        # 根据goroutine数量显示不同颜色和状态
        if ($goroutines -gt 5000) {
            Write-Host "📊 Goroutines: $goroutines 🔴 危险" -ForegroundColor Red
        } elseif ($goroutines -gt 2000) {
            Write-Host "📊 Goroutines: $goroutines 🟡 警告" -ForegroundColor Yellow
        } else {
            Write-Host "📊 Goroutines: $goroutines 🟢 正常" -ForegroundColor Green
        }
        
        Write-Host "💾 内存分配: $allocMb MB" -ForegroundColor Cyan
        Write-Host "💽 系统内存: $sysMb MB" -ForegroundColor Cyan
        Write-Host "🔄 GC次数: $numGc" -ForegroundColor Cyan
        Write-Host ""
        
        Write-Host "📈 统计信息:" -ForegroundColor Magenta
        Write-Host "   最大值: $maxGoroutines"
        Write-Host "   最小值: $minGoroutines"
        Write-Host "   当前值: $goroutines"
        Write-Host ""
        
        # 显示健康建议
        if ($goroutines -gt 10000) {
            Write-Host "⚠️  严重警告: Goroutine 数量超过 10,000！" -ForegroundColor Red
            Write-Host "   建议立即检查是否有新的泄漏问题" -ForegroundColor Red
            Write-Host "   可能需要重启服务" -ForegroundColor Red
        } elseif ($goroutines -gt 5000) {
            Write-Host "⚠️  警告: Goroutine 数量超过 5,000" -ForegroundColor Yellow
            Write-Host "   请检查日志，可能存在异常" -ForegroundColor Yellow
        } elseif ($goroutines -gt 2000) {
            Write-Host "ℹ️  提示: Goroutine 数量略高，属于高负载情况" -ForegroundColor Yellow
        } else {
            Write-Host "✅ Goroutine 数量正常" -ForegroundColor Green
        }
        
    } catch {
        Write-Host "❌ 无法连接到API: $ApiUrl" -ForegroundColor Red
        Write-Host "   错误: $($_.Exception.Message)" -ForegroundColor Red
        Write-Host "   请检查：" -ForegroundColor Yellow
        Write-Host "   1. 服务是否运行" -ForegroundColor Yellow
        Write-Host "   2. URL是否正确" -ForegroundColor Yellow
        Write-Host "   3. 网络是否通畅" -ForegroundColor Yellow
    }
    
    Write-Host ""
    Write-Host "下次刷新: ${Interval}秒后... (Ctrl+C 停止)" -ForegroundColor Gray
    Write-Host ""
    
    # 等待下一次检查
    Start-Sleep -Seconds $Interval
}

<#
.SYNOPSIS
    监控 OneAPI 服务的 Goroutine 数量

.DESCRIPTION
    实时监控 OneAPI 服务的 Goroutine 数量和内存使用情况
    
.PARAMETER ApiUrl
    OneAPI 服务的地址，默认 http://localhost:3000
    
.PARAMETER Interval
    刷新间隔（秒），默认 5 秒

.EXAMPLE
    .\monitor_goroutines.ps1
    使用默认配置监控本地服务
    
.EXAMPLE
    .\monitor_goroutines.ps1 -ApiUrl "http://your-server:3000" -Interval 10
    监控远程服务器，每10秒刷新一次
    
.EXAMPLE
    .\monitor_goroutines.ps1 http://192.168.1.100:3000 3
    监控指定IP的服务，每3秒刷新一次
#>

