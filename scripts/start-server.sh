#!/usr/bin/env bash
# 服务端启动脚本：接入密码由终端设置，管理密码首次启动时交互设置。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CONFIG="${1:-configs/server.example.yaml}"
EXE="bin/mtun-linux-amd64"
if [ ! -x "$EXE" ]; then
    echo "未找到 $EXE，正在编译..."
    CGO_ENABLED=0 go build -trimpath -o "$EXE" ./cmd/mtun
fi

TOKEN="${MTUN_JOIN_TOKEN:-}"
TOKEN_FILE="configs/join.token"
if [ -z "$TOKEN" ] && [ -f "$TOKEN_FILE" ]; then
    TOKEN="$(tr -d ' \r\n' < "$TOKEN_FILE")"
fi
if [ -z "$TOKEN" ]; then
    TOKEN="$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    umask 077
    printf '%s\n' "$TOKEN" > "$TOKEN_FILE"
    echo "已生成客户端接入密码: $TOKEN_FILE"
fi
if [ "${#TOKEN}" -lt 8 ]; then
    echo "客户端接入密码至少 8 位" >&2
    exit 1
fi
export MTUN_JOIN_TOKEN="$TOKEN"

# 客户端接入密码写入 <配置>.join
CURRENT_JOIN=""
if [ -f "$CONFIG.join" ]; then
    CURRENT_JOIN="$(tr -d ' \r\n' < "$CONFIG.join")"
fi
if [ "$CURRENT_JOIN" != "$TOKEN" ]; then
    "$EXE" access -c "$CONFIG" -password "$TOKEN"
fi

# 服务端管理密码必须由管理员在终端设置
if [ ! -f "$CONFIG.passwd" ]; then
    echo
    echo "首次启动需要设置服务端管理密码（Web 控制台登录用，至少 8 位）："
    "$EXE" passwd -c "$CONFIG"
fi

echo
echo "服务端启动中"
echo "  配置文件:       $CONFIG"
echo "  控制台:         http://127.0.0.1:10004"
echo "  管理密码文件:   $CONFIG.passwd（由终端 mtun passwd 设置）"
echo "  客户端接入密码: $TOKEN_FILE（客户端连接时填写）"
echo

exec "$EXE" server -c "$CONFIG"
