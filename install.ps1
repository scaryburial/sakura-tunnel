# ============================================================================
#  樱隧 Sakura Tunnel · 服务端一键安装脚本（Windows）
#
#  用法（管理员 PowerShell）：
#    iwr -useb https://raw.githubusercontent.com/<owner>/sakura-tunnel/main/install.ps1 -OutFile install.ps1
#    powershell -ExecutionPolicy Bypass -File .\install.ps1
#
#  参数：
#    -BindPort 7443    客户端接入端口
#    -WebPort  7500    控制台端口
#    -AdminUser admin  控制台管理员
#    -AdminPass xxx    控制台密码（默认随机）
#    -Dir C:\sakura    安装目录
#    -Version latest   版本号，如 v1.0.0
#    -Repo owner/repo  GitHub 仓库
#    -DryRun           只打印不执行
# ============================================================================
[CmdletBinding()]
param(
    [int]$BindPort = 7443,
    [int]$WebPort = 7500,
    [string]$AdminUser = "admin",
    [string]$AdminPass = "",
    [string]$Dir = "C:\sakura",
    [string]$Version = "latest",
    [string]$Repo = "YOUR_GITHUB_NAME/sakura-tunnel",
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"

function Info($m) { Write-Host $m -ForegroundColor Cyan }
function Ok($m) { Write-Host $m -ForegroundColor Green }
function Warn2($m) { Write-Host $m -ForegroundColor Yellow }

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin -and -not $DryRun) {
    throw "需要管理员权限：右键 PowerShell → 以管理员身份运行"
}

Info "[1/6] 准备目录 $Dir"
if (-not $DryRun) {
    New-Item -ItemType Directory -Force -Path $Dir, (Join-Path $Dir "clients") | Out-Null
}

$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
if ($Version -eq "latest") {
    $url = "https://github.com/$Repo/releases/latest/download/sakura-windows-$arch.exe"
} else {
    $url = "https://github.com/$Repo/releases/download/$Version/sakura-windows-$arch.exe"
}

$exe = Join-Path $Dir "sakura.exe"
Info "[2/6] 下载 $url"
if (-not $DryRun) {
    $tmp = "$exe.download"
    try {
        Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing -TimeoutSec 300
    } catch {
        Warn2 "直链下载失败，尝试 -upx 版本"
        Invoke-WebRequest -Uri "$url-upx" -OutFile $tmp -UseBasicParsing -TimeoutSec 300
    }
    Move-Item -Force $tmp $exe
    & $exe version
} else {
    Write-Host "[dry-run] 下载 $url -> $exe"
}

$conf = Join-Path $Dir "server.toml"
Info "[3/6] 生成配置 $conf"
if (-not $AdminPass) {
    $bytes = New-Object byte[] 12
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $AdminPass = ([Convert]::ToBase64String($bytes) -replace '[/+=]', '').Substring(0, 14)
}

if (Test-Path $conf) {
    Warn2 "配置已存在，保留原文件"
} elseif (-not $DryRun) {
    $cfg = @"
# 樱隧 Sakura Tunnel · 服务端配置（install.ps1 生成于 $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')）
bindPort = $BindPort

webServer.addr = "0.0.0.0"
webServer.port = $WebPort
webServer.user = "$AdminUser"
webServer.password = "$AdminPass"

auth.method = "user"
auth.userFile = "./users.json"

transport.tls.force = true
transport.maxPoolCount = 20

allowPorts = [
  { start = 6000, end = 6999 },
  { start = 17000, end = 17999 }
]

[log]
to = "./sakura.log"
level = "info"
maxDays = 7
"@
    Set-Content -Path $conf -Value $cfg -Encoding UTF8
} else {
    Write-Host "[dry-run] 写入 $conf（bindPort=$BindPort webPort=$WebPort）"
}

Info "[4/6] 注册开机自启（计划任务，SYSTEM 权限）"
$taskName = "SakuraTunnel"
if (-not $DryRun) {
    $action = New-ScheduledTaskAction -Execute $exe -Argument "-c `"$conf`"" -WorkingDirectory $Dir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
        -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    Start-ScheduledTask -TaskName $taskName
    Start-Sleep -Seconds 2
} else {
    Write-Host "[dry-run] 注册计划任务 $taskName 并启动"
}

Info "[5/6] 放行防火墙端口"
if (-not $DryRun) {
    foreach ($p in @($BindPort, $WebPort)) {
        if (-not (Get-NetFirewallRule -DisplayName "Sakura Tunnel $p" -ErrorAction SilentlyContinue)) {
            New-NetFirewallRule -DisplayName "Sakura Tunnel $p" -Direction Inbound -Protocol TCP `
                -LocalPort $p -Action Allow | Out-Null
        }
    }
} else {
    Write-Host "[dry-run] 新建入站规则 TCP $BindPort / $WebPort"
}

Info "[6/6] 完成"
$ip = (Get-NetIPAddress -AddressFamily IPv4 |
    Where-Object { $_.IPAddress -notlike "127.*" -and $_.IPAddress -notlike "169.*" } |
    Select-Object -First 1).IPAddress
if (-not $ip) { $ip = "<服务器IP>" }

Write-Host ""
Write-Host "  控制台：  http://${ip}:${WebPort}/" -ForegroundColor White
Write-Host "  账号：    $AdminUser" -ForegroundColor White
Write-Host "  密码：    $AdminPass" -ForegroundColor White
Write-Host "  接入端口：$BindPort" -ForegroundColor White
Write-Host ""
Write-Host "  下一步：打开控制台 →「用户」新建租户 → 点「一键接入」复制命令" -ForegroundColor White
Write-Host "  常用：  Get-ScheduledTask SakuraTunnel | Start/Stop-ScheduledTask" -ForegroundColor DarkGray
Write-Host ""

if (-not $DryRun) {
    Copy-Item -Force $exe (Join-Path $Dir "clients\sakura-windows-$arch.exe") -ErrorAction SilentlyContinue
}
Ok "好了，去浏览器里建用户吧 🌸"
