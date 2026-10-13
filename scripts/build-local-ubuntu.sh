#!/bin/bash
# Ubuntu 24.04+ / 26.04 本机构建（linux/amd64，webkit2gtk-4.1）
# 用法：在系统终端执行（需要 sudo 安装系统依赖）
#   chmod +x scripts/build-local-ubuntu.sh && ./scripts/build-local-ubuntu.sh

set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

echo "======================================"
echo "  go-stock 本机 Linux 构建"
echo "  $(uname -s)/$(uname -m)"
echo "======================================"

# ---- 环境 ----
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
# shellcheck disable=SC1091
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"

TOOLS="$ROOT/.tools"
export PATH="$TOOLS/go/bin:$TOOLS/gopath/bin:$HOME/go/bin:/usr/local/go/bin:$PATH"
export GOPATH="${GOPATH:-$TOOLS/gopath}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
export GOPROXY="${GOPROXY:-https://goproxy.io,https://proxy.golang.org,direct}"
export GO111MODULE=on

# ---- 系统依赖（Ubuntu 26.04 仅有 webkit2gtk-4.1）----
need_apt=0
pkg-config --exists gtk+-3.0 || need_apt=1
pkg-config --exists webkit2gtk-4.1 || need_apt=1
if [ "$need_apt" -eq 1 ]; then
  echo ""
  echo "[*] 安装系统依赖（需 sudo）..."
  sudo apt-get update
  sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
    libgtk-3-dev libwebkit2gtk-4.1-dev gcc pkg-config libglib2.0-dev build-essential curl
fi
echo "  ✓ GTK/WebKit 开发库就绪"

# ---- Go ----
if ! command -v go >/dev/null 2>&1; then
  echo ""
  echo "[*] 安装 Go 1.27 到 .tools/ ..."
  mkdir -p "$TOOLS"
  VER=go1.27.0
  curl -fL --retry 3 -o /tmp/go.tgz "https://dl.google.com/go/${VER}.linux-amd64.tar.gz"
  rm -rf "$TOOLS/go"
  tar -C "$TOOLS" -xzf /tmp/go.tgz
fi
echo "  ✓ $(go version)"

# ---- Wails CLI ----
if ! command -v wails >/dev/null 2>&1; then
  echo ""
  echo "[*] 安装 Wails CLI..."
  go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
fi
echo "  ✓ $(wails version 2>/dev/null | head -1 || echo wails)"

# ---- 前端 ----
echo ""
echo "[*] 安装并构建前端..."
cd frontend
npm install
npm run build
cd "$ROOT"

# ---- 打包（Ubuntu 26.04 必须带 webkit2_41）----
echo ""
echo "[*] wails build (linux/amd64, webkit2_41)..."
rm -rf build/bin
mkdir -p build/bin
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo local)"
VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
# 不注入正式 BuildKey：开放访问模式不需要赞助码解密
wails build \
  --platform linux/amd64 \
  -tags webkit2_41 \
  -ldflags "-X main.Version=${VERSION} -X main.VersionCommit=${COMMIT}" \
  -o go-stock

OUT=""
for p in build/bin/go-stock build/bin/linux/go-stock; do
  if [ -x "$p" ]; then OUT="$p"; break; fi
done
# wails 有时只输出到 build/bin/go-stock
if [ -z "$OUT" ]; then
  OUT="$(find build/bin -type f -name 'go-stock*' 2>/dev/null | head -1 || true)"
fi

echo ""
echo "======================================"
if [ -n "$OUT" ] && [ -f "$OUT" ]; then
  chmod +x "$OUT" || true
  ls -lh "$OUT"
  echo "  构建完成: $OUT"
  echo "  运行: $OUT"
else
  echo "  未找到产物，请检查上方 wails 日志"
  exit 1
fi
echo "======================================"
