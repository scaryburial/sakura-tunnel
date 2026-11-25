# ============================================================================
#  樱隧 Sakura Tunnel · 构建脚本（Windows / PowerShell）
#  用法：pwsh -File build\build.ps1 [-NoUPX] [-UpxPath <upx.exe>]
#  产物：dist\ 与 dist\clients\
# ============================================================================
param(
    [switch]$NoUPX,
    [string]$UpxPath = "upx",
    [string[]]$Targets = @("linux/amd64", "linux/arm64", "windows/amd64")
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $dist = Join-Path $root "dist"
    $clients = Join-Path $dist "clients"
    New-Item -ItemType Directory -Force -Path $dist, $clients | Out-Null

    $env:CGO_ENABLED = "0"
    foreach ($t in $Targets) {
        $goos, $goarch = $t.Split("/")
        $name = "sakura-$goos-$goarch"
        if ($goos -eq "windows") { $name += ".exe" }
        Write-Host "[build] $goos/$goarch -> $name"
        $env:GOOS = $goos; $env:GOARCH = $goarch
        & go build -trimpath -buildvcs=false -tags "noweb" -ldflags "-s -w" -o (Join-Path $dist $name) ./cmd/sakura
        if ($LASTEXITCODE -ne 0) { throw "build failed: $goos/$goarch" }
    }
    $env:GOOS = ""; $env:GOARCH = ""

    foreach ($f in Get-ChildItem $dist -Filter "sakura-*" -File | Where-Object { $_.Name -notmatch "-upx" }) {
        Copy-Item $f.FullName (Join-Path $clients $f.Name) -Force
    }

    if (-not $NoUPX) {
        $upxCmd = $null
        if (Test-Path $UpxPath) { $upxCmd = $UpxPath }
        elseif (Get-Command upx -ErrorAction SilentlyContinue) { $upxCmd = (Get-Command upx).Source }
        if ($upxCmd) {
            foreach ($f in Get-ChildItem $dist -Filter "sakura-*" -File | Where-Object { $_.Name -notmatch "-upx" }) {
                $packed = Join-Path $dist ($f.BaseName + "-upx" + $f.Extension)
                Copy-Item $f.FullName $packed -Force
                Write-Host "[upx ] $($f.Name) -> $([IO.Path]::GetFileName($packed))"
                try { & $upxCmd --best --lzma -q --force $packed | Out-Null } catch { Write-Warning "upx failed: $($f.Name)" }
            }
        } else {
            Write-Warning "未找到 upx，跳过压缩"
        }
    }

    Write-Host ""
    Write-Host "=========== 产物 ==========="
    Get-ChildItem $dist -File | Sort-Object Length |
        Select-Object Name, @{n = "大小"; e = { "{0:N2} MB" -f ($_.Length / 1MB) } } |
        Format-Table -AutoSize | Out-String -Width 100 | Write-Host
    Write-Host "客户端分发目录: $clients"
}
finally {
    Pop-Location
}
