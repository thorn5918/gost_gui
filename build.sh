#!/usr/bin/env bash
# S5Picker 构建脚本
# 用法:
#   ./build.sh              构建 Windows exe（默认）
#   ./build.sh linux        构建 Linux 测试版
# 首次构建会自动下载 gost 内核压缩包并 gzip 内嵌。
set -euo pipefail

cd "$(dirname "$0")"

GOST_VER=3.3.3
MIRROR_URL="https://v4.gh-proxy.org/https://github.com/go-gost/gost/releases/download/v${GOST_VER}"
DIRECT_URL="https://github.com/go-gost/gost/releases/download/v${GOST_VER}"

ensure_gost_windows() {
    local want="internal/gostbin/gost.exe.gz"
    [ -s "$want" ] && { echo "gost.exe.gz 已存在"; return; }
    mkdir -p /tmp/s5picker_dl internal/gostbin
    local f="gost_${GOST_VER}_windows_amd64.zip"
    for base in "$DIRECT_URL" "$MIRROR_URL"; do
        echo "下载 $base/$f ..."
        if curl -fL --retry 3 -o "/tmp/s5picker_dl/$f" "$base/$f"; then break; fi
    done
    unzip -o -q "/tmp/s5picker_dl/$f" gost.exe -d /tmp/s5picker_dl
    gzip -9 -c /tmp/s5picker_dl/gost.exe > "$want"
    echo "已生成 $want"
}

ensure_gost_linux() {
    local want="internal/gostbin/gost.gz"
    [ -s "$want" ] && { echo "gost.gz 已存在"; return; }
    mkdir -p /tmp/s5picker_dl internal/gostbin
    local f="gost_${GOST_VER}_linux_amd64.tar.gz"
    for base in "$DIRECT_URL" "$MIRROR_URL"; do
        echo "下载 $base/$f ..."
        if curl -fL --retry 3 -o "/tmp/s5picker_dl/$f" "$base/$f"; then break; fi
    done
    tar -xzf "/tmp/s5picker_dl/$f" -C /tmp/s5picker_dl gost
    gzip -9 -c /tmp/s5picker_dl/gost > "$want"
    echo "已生成 $want"
}

TARGET=${1:-windows}

if [ "$TARGET" = "windows" ]; then
    ensure_gost_windows
    # 生成资源（图标/manifest -> .syso），若 rsrc 不可用则跳过
    go run github.com/akavel/rsrc@v0.10.2 \
        -arch amd64 -manifest assets/s5picker.manifest -ico assets/app.ico \
        -o rsrc_windows_amd64.syso 2>/dev/null || \
        { [ -f rsrc_windows_amd64.syso ] || echo "警告: 未生成 .syso（无图标/manifest）"; }
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
        go build -trimpath -ldflags "-s -w -H windowsgui" -o build/s5picker.exe .
    echo "构建完成: build/s5picker.exe"
elif [ "$TARGET" = "linux" ]; then
    ensure_gost_linux
    CGO_ENABLED=0 go build -trimpath -o build/s5picker .
    echo "构建完成: build/s5picker（命令行自测: ./build/s5picker -test）"
else
    echo "未知目标: $TARGET（可选 windows/linux）"
    exit 1
fi
