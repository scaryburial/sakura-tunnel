#!/usr/bin/env bash
# ============================================================================
#  樱隧 Sakura Tunnel · 服务端一键安装脚本（Linux）
#
#  用法：
#    curl -fsSL https://raw.githubusercontent.com/<owner>/sakura-tunnel/main/install.sh | sudo bash
#
#  可选参数：
#    --bind-port <端口>     客户端接入端口        （默认 7443）
#    --web-port  <端口>     控制台端口            （默认 7500）
#    --admin-user <用户名>  控制台管理员          （默认 admin）
#    --admin-pass <密码>    控制台密码            （默认随机生成）
#    --dir <目录>           安装目录              （默认 /opt/sakura）
#    --version <版本>       例如 v1.0.0 / latest  （默认 latest）
#    --mirror <前缀>        下载镜像前缀，如 https://gh-proxy.com/
#    --jobs <N>             下载线程数（默认 4）
    --dry-run              只打印将要执行的动作
#
#  环境变量：SAKURA_REPO / SAKURA_VERSION / SAKURA_MIRROR
# ============================================================================
set -euo pipefail

REPO="${SAKURA_REPO:-scaryburial/sakura-tunnel}"
VERSION="${SAKURA_VERSION:-latest}"
MIRROR="${SAKURA_MIRROR:-}"
JOBS="${SAKURA_JOBS:-4}"
INSTALL_DIR="/opt/sakura"
BIND_PORT=7443
WEB_PORT=7500
ADMIN_USER="admin"
ADMIN_PASS=""
DRY_RUN=0

SERVICE_NAME="sakura"
BIN_NAME="sakura"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
blue()  { printf '\033[36m%s\033[0m\n' "$*"; }
warn()  { printf '\033[33m%s\033[0m\n' "$*"; }

usage() { sed -n '2,25p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    --bind-port)  BIND_PORT="${2:?}"; shift 2 ;;
    --web-port)   WEB_PORT="${2:?}"; shift 2 ;;
    --admin-user) ADMIN_USER="${2:?}"; shift 2 ;;
    --admin-pass) ADMIN_PASS="${2:?}"; shift 2 ;;
    --dir)        INSTALL_DIR="${2:?}"; shift 2 ;;
    --version)    VERSION="${2:?}"; shift 2 ;;
    --mirror)     MIRROR="${2:?}"; shift 2 ;;
    --jobs)       JOBS="${2:?}"; shift 2 ;;
    --dry-run)    DRY_RUN=1; shift ;;
    -h|--help)    usage ;;
    *) warn "忽略未知参数: $1"; shift ;;
  esac
done


# 多线程分段下载（curl --range），失败自动回退单线程
fetch_parallel() {
  local url="$1" out="$2" jobs="${3:-4}"
  [ "$jobs" -le 1 ] && { $DL "$out" "$url"; return $?; }

  local size
  size=$(curl -fsSIL "$url" 2>/dev/null | awk 'tolower($1)=="content-length:"{v=$2} END{gsub(/\r/,"",v); print v}')
  if [ -z "$size" ] || [ "$size" -lt 4194304 ]; then
    $DL "$out" "$url"; return $?
  fi

  local chunk=$(( (size / jobs) / 1048576 * 1048576 ))
  [ "$chunk" -le 0 ] && { $DL "$out" "$url"; return $?; }

  echo "  分段下载：$(( size/1048576 ))MB / ${jobs} 线程"
  local i=0 s e pids=()
  for (( i=0; i<jobs; i++ )); do
    s=$(( i * chunk ))
    e=$(( s + chunk - 1 ))
    [ "$i" -eq $(( jobs - 1 )) ] && e=$(( size - 1 ))
    curl -fsSL --retry 2 --range "${s}-${e}" "$url" -o "${out}.part${i}" &
    pids+=($!)
  done
  local fail=0
  for (( i=0; i<jobs; i++ )); do wait "${pids[$i]}" || fail=1; done
  if [ "$fail" = "1" ]; then
    rm -f "${out}".part*; $DL "$out" "$url"; return $?
  fi
  : > "$out"
  for (( i=0; i<jobs; i++ )); do
    dd if="${out}.part${i}" of="$out" bs=1M seek=$(( i * chunk / 1048576 )) conv=notrunc 2>/dev/null \
      || { rm -f "${out}".part* "$out"; $DL "$out" "$url"; return $?; }
  done
  rm -f "${out}".part*
  [ "$(wc -c < "$out")" -eq "$size" ] || { $DL "$out" "$url"; return $?; }
}
run() {
  if [ "$DRY_RUN" = "1" ]; then
    echo "[dry-run] $*"
  else
    eval "$@"
  fi
}

# ---------- 环境检查 ----------
[ "$(uname -s)" = "Linux" ] || { red "这个脚本只支持 Linux；Windows 请用 install.ps1"; exit 1; }

if [ "$(id -u)" != "0" ] && [ "$DRY_RUN" = "0" ]; then
  red "需要 root 权限：请用 sudo bash 运行"; exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7l|armv6l) ARCH="armv7" ;;
  *) red "不支持的架构: $(uname -m)"; exit 1 ;;
esac

if   command -v curl >/dev/null 2>&1; then DL="curl -fsSL --retry 3 -o"
elif command -v wget >/dev/null 2>&1; then DL="wget -q -O"
else red "需要 curl 或 wget"; exit 1
fi

# ---------- 下载地址 ----------
if [ "$VERSION" = "latest" ]; then
  URL_PATH="https://github.com/${REPO}/releases/latest/download/sakura-linux-${ARCH}"
else
  URL_PATH="https://github.com/${REPO}/releases/download/${VERSION}/sakura-linux-${ARCH}"
fi
DOWNLOAD_URL="${MIRROR}${URL_PATH}"

# ---------- 准备目录 ----------
blue "[1/6] 准备目录 ${INSTALL_DIR}"
if [ "$DRY_RUN" = "0" ]; then
  mkdir -p "${INSTALL_DIR}/clients"
else
  echo "[dry-run] mkdir -p ${INSTALL_DIR}/clients"
fi

# ---------- 下载二进制 ----------
blue "[2/6] 下载 ${DOWNLOAD_URL}"
TMP_BIN="${INSTALL_DIR}/.${BIN_NAME}.download"
if [ "$DRY_RUN" = "0" ]; then
  if ! fetch_parallel "${DOWNLOAD_URL}" "${TMP_BIN}" "$JOBS"; then
    warn "直链下载失败，尝试 -upx 版本"
    if ! fetch_parallel "${DOWNLOAD_URL}-upx" "${TMP_BIN}" "$JOBS"; then
      red "下载失败。请检查版本号/网络，或加 --mirror https://gh-proxy.com/"
      exit 1
    fi
  fi
  chmod +x "${TMP_BIN}"
  mv -f "${TMP_BIN}" "${INSTALL_DIR}/${BIN_NAME}"
  "${INSTALL_DIR}/${BIN_NAME}" version || true
else
  echo "[dry-run] $DL ${TMP_BIN} ${DOWNLOAD_URL} && chmod +x && mv -> ${INSTALL_DIR}/${BIN_NAME}"
fi

# ---------- 生成配置 ----------
CONF="${INSTALL_DIR}/server.toml"
blue "[3/6] 生成配置 ${CONF}"
if [ -z "${ADMIN_PASS}" ]; then
  ADMIN_PASS="$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | head -c 16)"
fi

if [ -f "${CONF}" ]; then
  warn "配置已存在，保留原文件（如需重写请先删除）"
elif [ "$DRY_RUN" = "1" ]; then
  echo "[dry-run] 写入 ${CONF}（bindPort=${BIND_PORT} webServer.port=${WEB_PORT}）"
else
  cat > "${CONF}" <<EOF
# 樱隧 Sakura Tunnel · 服务端配置（install.sh 生成于 $(date '+%Y-%m-%d %H:%M:%S')）
bindPort = ${BIND_PORT}

webServer.addr = "0.0.0.0"
webServer.port = ${WEB_PORT}
webServer.user = "${ADMIN_USER}"
webServer.password = "${ADMIN_PASS}"

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
EOF
  chmod 600 "${CONF}"
fi

# ---------- systemd ----------
blue "[4/6] 安装 systemd 服务"
UNIT="/etc/systemd/system/${SERVICE_NAME}.service"
if [ "$DRY_RUN" = "1" ]; then
  echo "[dry-run] 写入 ${UNIT} 并 systemctl enable --now ${SERVICE_NAME}"
elif command -v systemctl >/dev/null 2>&1; then
  cat > "${UNIT}" <<EOF
[Unit]
Description=Sakura Tunnel Server
After=network.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BIN_NAME} -c ${CONF}
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}" >/dev/null 2>&1 || true
  systemctl restart "${SERVICE_NAME}"
  sleep 1
  systemctl is-active --quiet "${SERVICE_NAME}" && green "服务已启动" || warn "服务可能未正常启动，查看：journalctl -u ${SERVICE_NAME} -n 50"
else
  warn "没有 systemd，改为后台运行"
  nohup "${INSTALL_DIR}/${BIN_NAME}" -c "${CONF}" >> "${INSTALL_DIR}/sakura.out" 2>&1 &
fi

# ---------- 防火墙提示 ----------
blue "[5/6] 检查防火墙"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  warn "检测到 ufw 已开启，正在放行 ${BIND_PORT}/tcp 与 ${WEB_PORT}/tcp"
  run "ufw allow ${BIND_PORT}/tcp" || true
  run "ufw allow ${WEB_PORT}/tcp" || true
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  warn "检测到 firewalld，正在放行端口"
  run "firewall-cmd --add-port=${BIND_PORT}/tcp --permanent" || true
  run "firewall-cmd --add-port=${WEB_PORT}/tcp --permanent" || true
  run "firewall-cmd --reload" || true
else
  echo "未检测到启用的本机防火墙（云厂商安全组记得单独放行 ${BIND_PORT} 与 ${WEB_PORT}）"
fi

# ---------- 完成 ----------
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -z "$IP" ] && IP="<服务器IP>"

blue "[6/6] 安装完成"
cat <<EOF

  控制台：  http://${IP}:${WEB_PORT}/
  账号：    ${ADMIN_USER}
  密码：    ${ADMIN_PASS}
  接入端口：${BIND_PORT}（客户端连接用）

  下一步：
    1. 浏览器打开控制台 → 「用户」→ 新建租户 → 拿到 sk_ 开头的密钥
    2. 点该用户的「一键接入」→ 复制命令发给对方

  常用命令：
    systemctl status ${SERVICE_NAME}
    journalctl -u ${SERVICE_NAME} -f
    客户端二进制放到 ${INSTALL_DIR}/clients/ 后即可被 /dl 下发

EOF

# 首次安装把自身平台二进制放一份到 clients/，保证一键接入能自举
if [ "$DRY_RUN" = "0" ]; then
  cp -f "${INSTALL_DIR}/${BIN_NAME}" "${INSTALL_DIR}/clients/sakura-linux-${ARCH}" 2>/dev/null || true
fi

green "好了，去浏览器里建用户吧 🌸"
