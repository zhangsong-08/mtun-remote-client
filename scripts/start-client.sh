#!/usr/bin/env bash
# 客户端启动脚本：可自动配置服务端。
# 用法: bash scripts/start-client.sh [客户端配置] [服务端地址] [接入密码]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CONFIG="${1:-configs/client.example.yaml}"
SERVER="${2:-}"
KEY="${3:-${MTUN_JOIN_TOKEN:-}}"

EXE="bin/mtun-linux-amd64"
if [ ! -x "$EXE" ]; then
    echo "未找到 $EXE，正在编译..."
    CGO_ENABLED=0 go build -trimpath -o "$EXE" ./cmd/mtun
fi

if [ -z "$KEY" ] && [ -f configs/join.token ]; then
    KEY="$(tr -d ' \r\n' < configs/join.token)"
fi
if [ -n "$SERVER" ] && [ -z "$KEY" ]; then
    echo "首次配置需要客户端接入密码：请传第三个参数，或使用服务端生成的 configs/join.token" >&2
    exit 1
fi

echo
echo "客户端启动中"
echo "  配置文件:     $CONFIG"
echo "  控制台:       http://127.0.0.1:10005"
echo "  登录密码:     首次打开控制台时由你自己设置"
if [ -n "$SERVER" ]; then
    echo "  自动配置服务端: $SERVER"
else
    echo "  未指定服务端，可在控制台或使用 mtun connect 添加"
fi
echo

"$EXE" client -c "$CONFIG" &
CLIENT_PID=$!
trap 'kill "$CLIENT_PID" 2>/dev/null || true' EXIT

if [ -n "$SERVER" ]; then
    READY=0
    for _ in $(seq 1 75); do
        if (exec 3<>/dev/tcp/127.0.0.1/10005) 2>/dev/null; then
            exec 3>&-
            READY=1
            break
        fi
        sleep 0.2
    done
    if [ "$READY" -ne 1 ]; then
        echo "客户端控制台未在 10005 就绪" >&2
        exit 1
    fi
    if [ -n "$KEY" ]; then
        "$EXE" connect -server "$SERVER" -key "$KEY" -c "$CONFIG"
    else
        "$EXE" connect -server "$SERVER" -c "$CONFIG"
    fi
fi

wait "$CLIENT_PID"
