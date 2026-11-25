// Copyright 2026 Sakura Tunnel Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package onetap generates the self-contained install scripts that a tenant
// pastes into a shell: one HTTP fetch, zero configuration on the client side.
package onetap

import (
	"fmt"
	"strings"
)

// Params describes everything a generated script needs.
type Params struct {
	// Host is the public host (or IP) the client reaches the server on.
	Host string
	// Port is the server bind port used by the tunnel control connection.
	Port int
	// DashboardPort is the port serving the dashboard and the download links.
	DashboardPort int
	// Scheme is http or https, used for the download links.
	Scheme string
	// User and Token identify the tenant.
	User  string
	Token string
	// BinaryName is the download path segment, e.g. "sakura".
	BinaryName string
	// DefaultRemotePort is used when the caller does not pass --remote-port.
	DefaultRemotePort int
}

func (p Params) baseURL() string {
	scheme := p.Scheme
	if scheme == "" {
		scheme = "http"
	}
	port := p.DashboardPort
	if port == 0 {
		port = p.Port
	}
	if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
		return fmt.Sprintf("%s://%s", scheme, p.Host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, p.Host, port)
}

func (p Params) binaryName() string {
	if p.BinaryName == "" {
		return "sakura"
	}
	return p.BinaryName
}

// BaseURL is the dashboard origin the client downloads from.
func (p Params) BaseURL() string { return p.baseURL() }

// LinuxURLForArch points at the self-hosted linux client binary for one arch.
func (p Params) LinuxURLForArch(arch string) string {
	if arch == "" {
		arch = "amd64"
	}
	return p.baseURL() + "/dl/" + p.binaryName() + "-linux-" + arch
}

// WindowsURL points at the self-hosted windows client binary.
func (p Params) WindowsURL() string {
	return p.baseURL() + "/dl/" + p.binaryName() + "-windows-amd64.exe"
}

// LinuxURL points at the default (amd64) linux client binary.
func (p Params) LinuxURL() string { return p.LinuxURLForArch("amd64") }

// LinuxCommand is the one line the tenant runs on a Linux box.
func (p Params) LinuxCommand() string {
	return fmt.Sprintf("curl -fsSL %s/i/%s | bash", p.baseURL(), p.Token)
}

// WindowsCommand is the one line the tenant runs in PowerShell.
func (p Params) WindowsCommand() string {
	return fmt.Sprintf(`powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr -useb %s/w/%s | iex"`, p.baseURL(), p.Token)
}

// DockerCommand runs the client in a container.
func (p Params) DockerCommand() string {
	return fmt.Sprintf(
		`docker run -d --name %s --restart=always --network host `+
			`-e SERVER=%s -e PORT=%d -e USER=%s -e TOKEN=%s -e REMOTE_PORT=%d %s/%s:latest`,
		"tunnel-"+sanitize(p.User), p.Host, p.Port, p.User, p.Token, p.DefaultRemotePort, "sakura", "client")
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "user"
	}
	return b.String()
}

func (p Params) remotePort() int {
	if p.DefaultRemotePort <= 0 {
		return 6000
	}
	return p.DefaultRemotePort
}

func (p Params) clientConfig(remotePort int, localHost string, localPort int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "serverAddr = %q\n", p.Host)
	fmt.Fprintf(&b, "serverPort = %d\n", p.Port)
	fmt.Fprintf(&b, "user = %q\n", p.User)
	fmt.Fprintf(&b, "loginFailExit = false\n")
	fmt.Fprintf(&b, "log.to = \"console\"\n")
	fmt.Fprintf(&b, "log.level = \"info\"\n")
	fmt.Fprintf(&b, "\n[auth]\n")
	fmt.Fprintf(&b, "method = \"user\"\n")
	fmt.Fprintf(&b, "token = %q\n", p.Token)
	fmt.Fprintf(&b, "\n[transport.tls]\n")
	fmt.Fprintf(&b, "enable = true\n")
	fmt.Fprintf(&b, "\n[[proxies]]\n")
	fmt.Fprintf(&b, "name = \"%s-ssh\"\n", sanitize(p.User))
	fmt.Fprintf(&b, "type = \"tcp\"\n")
	fmt.Fprintf(&b, "localIP = %q\n", localHost)
	fmt.Fprintf(&b, "localPort = %d\n", localPort)
	fmt.Fprintf(&b, "remotePort = %d\n", remotePort)
	return b.String()
}

// LinuxScript returns a fully self-contained bash installer.
func (p Params) LinuxScript() string {
	cfg := p.clientConfig(p.remotePort(), "127.0.0.1", 22)
	return fmt.Sprintf(`#!/usr/bin/env bash
# ============================================================
#  %s 一键接入脚本（自动生成，请勿外传）
#  用法: curl -fsSL ... | bash
#  可选参数: --remote-port <N>  --local <host:port>  --dir <安装目录>
# ============================================================
set -euo pipefail

SERVER_HOST=%q
SERVER_PORT=%d
DL_BASE=%q
USER_NAME=%q
USER_TOKEN=%q
BIN_NAME=%q

REMOTE_PORT=%d
LOCAL_ADDR="127.0.0.1:22"
INSTALL_DIR="${HOME}/.%s"

while [ $# -gt 0 ]; do
  case "$1" in
    --remote-port) REMOTE_PORT="$2"; shift 2 ;;
    --local)       LOCAL_ADDR="$2"; shift 2 ;;
    --dir)         INSTALL_DIR="$2"; shift 2 ;;
    -h|--help)     sed -n '2,8p' "$0" 2>/dev/null || true; exit 0 ;;
    *) shift ;;
  esac
done

LOCAL_HOST="${LOCAL_ADDR%%%%:*}"
LOCAL_PORT="${LOCAL_ADDR##*:}"
if [ -z "$LOCAL_HOST" ]; then LOCAL_HOST="127.0.0.1"; fi

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH_SUFFIX="amd64" ;;
  aarch64|arm64) ARCH_SUFFIX="arm64" ;;
  armv7l) ARCH_SUFFIX="arm" ;;
  *) ARCH_SUFFIX="amd64" ;;
esac

mkdir -p "$INSTALL_DIR"
BIN="$INSTALL_DIR/%s"

if [ -x "$BIN" ]; then
  echo "[i] 已存在客户端，跳过下载"
else
  echo "[i] 正在下载客户端 ($ARCH_SUFFIX) ..."
  DL_URL="${DL_BASE}/dl/${BIN_NAME}-linux-${ARCH_SUFFIX}"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$DL_URL" -o "$BIN"
  else
    wget -qO "$BIN" "$DL_URL"
  fi
  chmod +x "$BIN"
fi

cat > "$INSTALL_DIR/client.toml" <<'EOF'
%s
EOF

# 用实际参数覆写默认转发
sed -i "s#^localIP = .*#localIP = \"$LOCAL_HOST\"#" "$INSTALL_DIR/client.toml" 2>/dev/null || true
sed -i "s#^localPort = .*#localPort = $LOCAL_PORT#" "$INSTALL_DIR/client.toml" 2>/dev/null || true
sed -i "s#^remotePort = .*#remotePort = $REMOTE_PORT#" "$INSTALL_DIR/client.toml" 2>/dev/null || true

echo "[i] 安装完成，目录: $INSTALL_DIR"

if command -v systemctl >/dev/null 2>&1 && [ "$(id -u)" = "0" ]; then
  cat > /etc/systemd/system/%s-client.service <<EOF2
[Unit]
Description=%s client ($USER_NAME)
After=network.target

[Service]
Type=simple
ExecStart=$BIN -c $INSTALL_DIR/client.toml
Restart=always
RestartSec=5
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF2
  systemctl daemon-reload
  systemctl enable --now %s-client
  echo "[√] 已注册为系统服务并启动: systemctl status %s-client"
else
  pkill -f "$BIN -c $INSTALL_DIR/client.toml" 2>/dev/null || true
  nohup "$BIN" -c "$INSTALL_DIR/client.toml" > "$INSTALL_DIR/client.log" 2>&1 &
  sleep 1
  echo "[√] 已在后台启动，日志: $INSTALL_DIR/client.log"
fi

echo "[√] 转发规则: $LOCAL_ADDR  ->  $SERVER_HOST:$REMOTE_PORT"
`,
		p.binaryName(),
		p.Host, p.Port, p.BaseURL(), p.User, p.Token, p.binaryName(),
		p.remotePort(), p.binaryName(), p.binaryName(),
		cfg,
		p.binaryName(), p.binaryName(), p.binaryName(), p.binaryName(),
	)
}

// PowerShellScript returns a Windows installer that runs the client silently.
func (p Params) PowerShellScript() string {
	cfg := p.clientConfig(p.remotePort(), "127.0.0.1", 22)
	return fmt.Sprintf(`# ============================================================
#  %s 一键接入脚本（自动生成，请勿外传）
#  用法: powershell -c "iwr -useb <本地址> | iex"
# ============================================================
$ErrorActionPreference = "Stop"
$ServerHost = "%s"
$ServerPort = %d
$DownloadUrl = "%s"
$UserName   = "%s"
$UserToken  = "%s"
$RemotePort = %d

$InstallDir = Join-Path $env:LOCALAPPDATA "%s"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$Bin = Join-Path $InstallDir "%s.exe"

if (-not (Test-Path $Bin)) {
  Write-Host "[i] 正在下载客户端 ..."
  Invoke-WebRequest -Uri $DownloadUrl -OutFile $Bin -UseBasicParsing
}

$ConfigPath = Join-Path $InstallDir "client.toml"
@'
%s
'@ | Set-Content -Path $ConfigPath -Encoding UTF8

Write-Host "[i] 安装完成: $InstallDir"

# 注册计划任务实现开机自启
$TaskName = "%s-client"
$ClientArgs = '-c "{0}"' -f $ConfigPath
$action = New-ScheduledTaskAction -Execute $Bin -Argument $ClientArgs
$trigger = New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null

Start-Process -FilePath $Bin -ArgumentList $ClientArgs -WindowStyle Hidden
Write-Host "[√] 已启动客户端，本机 127.0.0.1:22 -> ${ServerHost}:${RemotePort}"
Write-Host "[√] 关闭: Stop-ScheduledTask -TaskName $TaskName"
`,
		p.binaryName(),
		p.Host, p.Port, p.WindowsURL(), p.User, p.Token, p.remotePort(),
		p.binaryName(), p.binaryName(),
		cfg,
		p.binaryName(),
	)
}
