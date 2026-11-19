#!/usr/bin/env bash
# mtun Linux 客户端一键安装脚本
#
# 用法:
#   sudo bash install-client.sh -s 203.0.113.10:10000 -k 接入密码
#   sudo bash install-client.sh -s IP:10000 -k 密码 --panel 0.0.0.0:10005   # 面板允许公网访问
#   sudo bash install-client.sh -u                                          # 卸载
set -euo pipefail

REPO="zhangsong-08/mtun-remote-client"
VERSION=""
INSTALL_DIR="/opt/mtun-client"
MIRROR="${MTUN_MIRROR:-}"
LOCAL_FILE=""
SERVER_ADDR=""
JOIN_KEY="${MTUN_JOIN_TOKEN:-}"
NODE_NAME=""
PANEL_ADDR="${MTUN_PANEL:-127.0.0.1:10005}"
ADMIN_PASSWORD="${MTUN_ADMIN_PASSWORD:-}"
UNINSTALL=false
SYSTEMD_SERVICE="mtun-client"

DEFAULT_MIRRORS=("https://ghfast.top/" "https://gh-proxy.com/" "")

while [[ $# -gt 0 ]]; do
    case "$1" in
        -s|--server)   SERVER_ADDR="$2"; shift 2 ;;
        -k|--key)      JOIN_KEY="$2"; shift 2 ;;
        -n|--node)     NODE_NAME="$2"; shift 2 ;;
        -v|--version)  VERSION="$2"; shift 2 ;;
        -d|--dir)      INSTALL_DIR="$2"; shift 2 ;;
        -m|--mirror)   MIRROR="$2"; shift 2 ;;
        -f|--file)     LOCAL_FILE="$2"; shift 2 ;;
        --panel)       PANEL_ADDR="$2"; shift 2 ;;
        -p|--password) ADMIN_PASSWORD="$2"; shift 2 ;;
        -u|--uninstall) UNINSTALL=true; shift ;;
        -h|--help)
            cat <<EOF
mtun Linux 客户端安装脚本（systemd）

用法: sudo bash install-client.sh -s 服务端地址 -k 接入密码 [选项]

必填:
  -s, --server HOST:PORT   服务端控制面地址，如 203.0.113.10:10000
  -k, --key PASSWORD       客户端接入密码（服务端 server.yaml.join 里的内容）
                           也可用环境变量 MTUN_JOIN_TOKEN

可选:
  -n, --node NAME          节点名称，默认使用主机名
  -v, --version TAG        指定版本，默认最新 release
  -d, --dir DIR            安装目录，默认 /opt/mtun-client
  -m, --mirror URL         GitHub 加速前缀，如 https://ghfast.top/
  -f, --file PATH          离线安装：使用本地 linux 二进制
  --panel ADDR             面板监听地址，默认 127.0.0.1:10005（仅本机）
                           需要公网访问面板时用 0.0.0.0:10005
  -p, --password PW        设置客户端 Web 登录密码（至少 8 位）
                           不填时：有终端则交互输入，无终端则自动生成并打印
  -u, --uninstall          卸载（停服务、删 unit、删目录）
  -h, --help               本帮助

示例:
  sudo bash install-client.sh -s 203.0.113.10:10000 -k 'eb2af...'
  sudo bash install-client.sh -s 1.2.3.4:10000 -k 'eb2af...' --panel 0.0.0.0:10005
EOF
            exit 0 ;;
        *) echo "未知参数: $1" >&2; exit 1 ;;
    esac
done

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        echo "请用 root 或 sudo 执行" >&2
        exit 1
    fi
fi

# ---------- 卸载 ----------
if [ "$UNINSTALL" = true ]; then
    echo "==> 卸载 mtun 客户端"
    $SUDO systemctl stop "$SYSTEMD_SERVICE" 2>/dev/null || true
    $SUDO systemctl disable "$SYSTEMD_SERVICE" 2>/dev/null || true
    $SUDO rm -f "/etc/systemd/system/${SYSTEMD_SERVICE}.service"
    $SUDO systemctl daemon-reload
    $SUDO pkill -f "$INSTALL_DIR/bin/mtun" 2>/dev/null || true
    sleep 1
    $SUDO rm -rf "$INSTALL_DIR"
    echo "已卸载：服务、unit、$INSTALL_DIR 均已删除"
    exit 0
fi

# ---------- 参数校验 ----------
if [ -z "$SERVER_ADDR" ]; then
    echo "缺少 -s 服务端地址，例如：-s 203.0.113.10:10000" >&2
    exit 1
fi
if [ -z "$JOIN_KEY" ]; then
    echo "缺少 -k 接入密码（服务端 server.yaml.join 的内容）" >&2
    exit 1
fi

# ---------- 架构 ----------
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)  BIN_ARCH="amd64" ;;
    aarch64|arm64) BIN_ARCH="arm64" ;;
    *) echo "不支持的架构: $ARCH" >&2; exit 1 ;;
esac
ASSET="mtun-linux-${BIN_ARCH}"

if [ -n "$MIRROR" ]; then
    MIRRORS=("$MIRROR")
else
    MIRRORS=("${DEFAULT_MIRRORS[@]}")
fi

# ---------- 下载 ----------
fetch_github() {
    local url="$1" out="$2" prefix target
    for prefix in "${MIRRORS[@]}"; do
        target="${prefix}${url}"
        if command -v curl >/dev/null 2>&1; then
            if curl -fsSL --connect-timeout 10 --max-time 900 -o "$out" "$target"; then
                return 0
            fi
        else
            if wget -q --timeout=30 --tries=1 -O "$out" "$target"; then
                return 0
            fi
        fi
        echo "   源 ${prefix:-直连} 失败，切换下一个" >&2
    done
    return 1
}

latest_version() {
    local v prefix
    for prefix in "${MIRRORS[@]}"; do
        v="$(curl -fsSL --connect-timeout 8 --max-time 25 "${prefix}https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | grep -oE '"tag_name":\s*"[^"]+"' | head -1 | cut -d'"' -f4 || true)"
        [ -n "$v" ] && { echo "$v"; return 0; }
    done
    for prefix in "${MIRRORS[@]}"; do
        v="$(curl -fsSI --connect-timeout 8 --max-time 25 "${prefix}https://github.com/${REPO}/releases/latest" 2>/dev/null \
            | grep -iE '^location:' | tr -d '\r' | head -1 || true)"
        v="${v##*/}"
        [ -n "$v" ] && { echo "$v"; return 0; }
    done
    return 1
}

# ---------- 版本 ----------
if [ -z "$VERSION" ]; then
    echo "==> 查询最新 release"
    VERSION="$(latest_version || true)"
fi
if [ -z "$VERSION" ]; then
    echo "无法获取版本号，请用 -v 指定，例如：-v v0.2.8" >&2
    exit 1
fi
echo "==> 使用版本 $VERSION，架构 linux-${BIN_ARCH}"

$SUDO mkdir -p "$INSTALL_DIR"/{bin,logs}
BIN="$INSTALL_DIR/bin/mtun"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

if [ -n "$LOCAL_FILE" ]; then
    echo "==> 离线安装，使用本地文件 $LOCAL_FILE"
    [ -f "$LOCAL_FILE" ] || { echo "文件不存在: $LOCAL_FILE" >&2; exit 1; }
    cp "$LOCAL_FILE" "$TMP"
else
    echo "==> 下载 $ASSET"
    if ! fetch_github "https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}" "$TMP"; then
        echo "下载失败。可用 -m https://ghfast.top/ 或 -f 指定本地文件" >&2
        exit 1
    fi
    echo "  已下载 $(( $(wc -c < "$TMP") / 1024 / 1024 )) MB"
fi

if [ -z "$LOCAL_FILE" ] && command -v sha256sum >/dev/null 2>&1; then
    SUM_TMP="$(mktemp)"
    if fetch_github "https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt" "$SUM_TMP" 2>/dev/null; then
        want="$(awk -v f="$ASSET" '$2==f || $2=="*"f {print $1; exit}' "$SUM_TMP")"
        got="$(sha256sum "$TMP" | awk '{print $1}')"
        if [ -n "$want" ] && [ "$want" = "$got" ]; then
            echo "==> checksum 校验通过"
        else
            echo "==> 警告：checksum 不匹配，请核实下载来源"
        fi
    fi
    rm -f "$SUM_TMP"
fi

$SUDO mv "$TMP" "$BIN"
trap - EXIT
$SUDO chmod +x "$BIN"
"$BIN" version

# ---------- 配置 ----------
CFG="$INSTALL_DIR/configs/client.yaml"
$SUDO mkdir -p "$INSTALL_DIR/configs"
if [ ! -f "$CFG" ]; then
    echo "==> 生成客户端配置"
    if ! $BIN default client > "$TMP" 2>/dev/null || [ ! -s "$TMP" ]; then
        cat > "$TMP" <<EOF
role: client
server: ""
node: ""
control: ":10000"
wg: ":10001"
relay: ":10002"
stun: ":10003"
dashboard: "127.0.0.1:10005"
EOF
    fi
    $SUDO mv "$TMP" "$CFG"
    trap - EXIT
fi

# 写入客户端专用的在线更新仓库（与服务端分仓发布）
if grep -qE '^update_repo:' "$CFG"; then
    $SUDO sed -i "s|^update_repo:.*|update_repo: \"$REPO\"|" "$CFG"
else
    printf 'update_repo: "%s"\n' "$REPO" | $SUDO tee -a "$CFG" >/dev/null
fi

# 写入服务端地址
if grep -qE '^server:' "$CFG"; then
    $SUDO sed -i "s|^server:.*|server: \"$SERVER_ADDR\"|" "$CFG"
else
    printf 'server: "%s"\n' "$SERVER_ADDR" | $SUDO tee -a "$CFG" >/dev/null
fi

# 写入节点名（默认主机名，由程序自动补齐）
if [ -n "$NODE_NAME" ]; then
    if grep -qE '^node:' "$CFG"; then
        $SUDO sed -i "s|^node:.*|node: \"$NODE_NAME\"|" "$CFG"
    else
        printf 'node: "%s"\n' "$NODE_NAME" | $SUDO tee -a "$CFG" >/dev/null
    fi
fi

# 写入面板监听地址（--panel 0.0.0.0:10005 即允许公网访问）
if grep -qE '^dashboard:' "$CFG"; then
    $SUDO sed -i "s|^dashboard:.*|dashboard: \"$PANEL_ADDR\"|" "$CFG"
else
    printf 'dashboard: "%s"\n' "$PANEL_ADDR" | $SUDO tee -a "$CFG" >/dev/null
fi

# 校验配置，避免带着错误配置注册服务
if ! $BIN check -c "$CFG" -role client >/dev/null 2>&1; then
    echo "配置文件校验失败：" >&2
    $BIN check -c "$CFG" -role client >&2 || true
    exit 1
fi

# ---------- 接入密码 ----------
printf '%s\n' "$JOIN_KEY" | $SUDO tee "$CFG.join" >/dev/null
$SUDO chmod 600 "$CFG.join"

# ---------- Web 登录密码 ----------
if [ ! -f "$CFG.passwd" ]; then
    echo
    if [ -n "$ADMIN_PASSWORD" ]; then
        if [ "${#ADMIN_PASSWORD}" -lt 8 ]; then
            echo "面板密码至少 8 位" >&2
            exit 1
        fi
        $BIN passwd -c "$CFG" -password "$ADMIN_PASSWORD"
    elif [ -t 0 ]; then
        echo "==> 设置客户端 Web 面板登录密码（至少 8 位）"
        $BIN passwd -c "$CFG"
    else
        ADMIN_PASSWORD="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
        $BIN passwd -c "$CFG" -password "$ADMIN_PASSWORD"
        echo "==> 已自动生成面板密码（非交互模式）"
        echo "    ─────────────────────────────────────────"
        echo "    $ADMIN_PASSWORD"
        echo "    ─────────────────────────────────────────"
    fi
fi

# ---------- systemd ----------
UNIT="/etc/systemd/system/${SYSTEMD_SERVICE}.service"
echo "==> 写入 systemd unit $UNIT"
cat <<EOF | $SUDO tee "$UNIT" >/dev/null
[Unit]
Description=mtun client
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
ExecStart=$BIN client -c $CFG
Restart=on-failure
RestartSec=2
WorkingDirectory=$INSTALL_DIR
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

$SUDO systemctl daemon-reload
$SUDO systemctl enable "$SYSTEMD_SERVICE"
$SUDO systemctl restart "$SYSTEMD_SERVICE"

echo
echo "==> 等待 3 秒确认启动"
sleep 3
if systemctl is-active --quiet "$SYSTEMD_SERVICE"; then
    echo
    echo "✅ mtun 客户端已启动"
    echo ""
    echo "  安装目录:     $INSTALL_DIR"
    echo "  配置文件:     $CFG"
    echo "  服务端:       $SERVER_ADDR"
    echo "  节点名称:     ${NODE_NAME:-$(hostname)}"
    echo "  控制台:       http://${PANEL_ADDR}"
    echo ""
    echo "  查看状态:     $BIN status -c $CFG"
    echo "  查看日志:     journalctl -u $SYSTEMD_SERVICE -f"
    echo "  重启服务:     systemctl restart $SYSTEMD_SERVICE"
    echo "  添加规则:     $BIN rule add -name web -listen 10101 -target 127.0.0.1:80 -c $CFG"
    echo "  在线更新:     $BIN update -c $CFG"
    echo "  卸载:         sudo bash $0 -u"
    echo ""
    echo "  ⚠️ 规则监听端口必须在服务端允许范围（默认 10100-10999）内"
    if [ "$PANEL_ADDR" != "127.0.0.1:10005" ]; then
        echo "  ⚠️ 面板已允许公网访问，请确保防火墙放行该端口，并使用强密码"
    fi
else
    echo "❌ 启动失败，以下是最近日志：" >&2
    journalctl -u "$SYSTEMD_SERVICE" -n 30 --no-pager >&2 || true
    exit 1
fi
