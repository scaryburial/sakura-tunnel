#!/usr/bin/env bash
# ============================================================================
#  樱隧 Sakura Tunnel · 构建脚本（Linux / macOS）
#  用法：bash build/build-release.sh [--no-upx] [--targets "linux/amd64,..."]
#  产物：dist/ 与 dist/clients/
# ============================================================================
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

TARGETS="linux/amd64,linux/arm64,windows/amd64"
NO_UPX=0

while [ $# -gt 0 ]; do
  case "$1" in
    --no-upx) NO_UPX=1; shift ;;
    --targets) TARGETS="$2"; shift 2 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

command -v go >/dev/null 2>&1 || { echo "需要 Go 1.26+"; exit 1; }
echo "go: $(go version)"

mkdir -p dist/clients

IFS=',' read -ra LIST <<< "$TARGETS"
for t in "${LIST[@]}"; do
  GOOS="${t%%/*}"
  GOARCH="${t##*/}"
  NAME="sakura-${GOOS}-${GOARCH}"
  [ "$GOOS" = "windows" ] && NAME="${NAME}.exe"
  echo "[build] $GOOS/$GOARCH -> $NAME"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -buildvcs=false -tags noweb -ldflags "-s -w" -o "dist/$NAME" ./cmd/sakura
done

for f in dist/sakura-*; do
  case "$f" in *-upx*) continue ;; esac
  cp -f "$f" "dist/clients/$(basename "$f")"
done

if [ "$NO_UPX" = "0" ] && command -v upx >/dev/null 2>&1; then
  for f in dist/sakura-*; do
    case "$f" in *-upx*) continue ;; esac
    base="${f%.exe}"; ext=""
    [ "${f##*.}" = "exe" ] && ext=".exe"
    out="${base}-upx${ext}"
    cp -f "$f" "$out"
    echo "[upx ] $(basename "$out")"
    upx --best --lzma -q --force "$out" >/dev/null
  done
else
  [ "$NO_UPX" = "0" ] && echo "未找到 upx，跳过压缩"
fi

echo
echo "=========== 产物 ==========="
ls -lh dist | awk 'NR>1 {printf "%-34s %s\n", $9, $5}'
echo "客户端分发目录: dist/clients/"
