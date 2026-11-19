#!/usr/bin/env bash
# mtun 服务端一键安装脚本（Linux）
# 用法：curl -sSL https://raw.githubusercontent.com/zhangsong-08/mtun/main/scripts/install.sh | sudo bash
# 或先下载再执行：./install.sh [-v v0.2.1] [-d /opt/mtun]
set -euo pipefail

REPO="zhangsong-08/mtun"
VERSION=""          # 留空自动取 latest
INSTALL_DIR="/opt/mtun"
UNINSTALL=false
SYSTEMD_SERVICE="mtun-server"

# ---------- 解析参数 ----------
while [[ $# -gt 0 ]]; do
    case "$1" in
        -v|--version)  VERSION="$2";  shift 2 ;;
        -d|--dir)     INSTALL_DIR="$2"; shift 2 ;;
        -u|--uninstall) UNINSTALL=true; shift ;;
        -h|--help)
            cat <<EOF
mtun 服务端一键安装脚本（Linux / systemd）

用法: sudo bash install.sh [选项]

选项:
  -v, --version TAG    指定版本（默认最新 release）
  -d, --dir DIR        安装目录（默认 /opt/mtun）
  -u, --uninstall      卸载（停服务、删 systemd unit、删目录）
  -h, --help           本帮助

默认拉取 GitHub Release 的预编译二进制，不依赖 Go 编译环境。
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
    echo "==> 卸载 mtun"
    $SUDO systemctl stop "$SYSTEMD_SERVICE" 2>/dev/null || true
    $SUDO systemctl disable "$SYSTEMD_SERVICE" 2>/dev/null || true
    $SUDO rm -f "/etc/systemd/system/${SYSTEMD_SERVICE}.service"
    $SUDO systemctl daemon-reload
    $SUDO rm -rf "$INSTALL_DIR"
    echo "已卸载"
    exit 0
fi

# ---------- 架构 ----------
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64) BIN_ARCH="amd64" ;;
    aarch64|arm64) BIN_ARCH="arm64" ;;
    *) echo "不支持的架构: $ARCH" >&2; exit 1 ;;
esac

# ---------- 取版本 ----------
if [ -z "$VERSION" ]; then
    echo "==> 查询最新 release"
    if command -v gh >/dev/null 2>&1; then
        VERSION="$(gh release view --repo "$REPO" --json tagName -q .tagName)"
    else
        VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
            | grep -oE '"tag_name":\s*"[^"]+"' | head -1 | cut -d'"' -f4)"
    fi
fi
if [ -z "$VERSION" ]; then
    echo "获取版本失败" >&2; exit 1
fi
echo "==> 使用版本 $VERSION，架构 linux-${BIN_ARCH}"

# ---------- 下载 ----------
ASSET="mtun-linux-${BIN_ARCH}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
echo "==> 下载 $URL"
$SUDO mkdir -p "$INSTALL_DIR"/{bin,configs,logs}
TMP="$(mktemp)"
if command -v wget >/dev/null 2>&1; then
    wget -qO "$TMP" "$URL" || { echo "wget 下载失败，请检查网络" >&2; rm -f "$TMP"; exit 1; }
else
    curl -fsSL -o "$TMP" "$URL" || { echo "curl 下载失败，请检查网络" >&2; rm -f "$TMP"; exit 1; }
fi
# 校验 checksums（可选，不影响安装）
if command -v sha256sum >/dev/null 2>&1; then
    SUM_TMP="$(mktemp)"
    curl -fsSL "https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt" -o "$SUM_TMP" 2>/dev/null || true
    if [ -s "$SUM_TMP" ]; then
        cd "$(dirname "$TMP")"
        if sha256sum -c "$SUM_TMP" --ignore-matching-lines='^' 2>/dev/null | grep -q "OK"; then
            echo "==> 校验成功"
        else
            echo "==> 警告：checksum 校验失败，继续安装（可手动比对）"
        fi
        cd - >/dev/null
    fi
    rm -f "$SUM_TMP"
fi

# ---------- 安装 ----------
BIN="$INSTALL_DIR/bin/mtun"
$SUDO mv "$TMP" "$BIN"
$SUDO chmod +x "$BIN"
"$BIN" version

# ---------- 配置 ----------
CFG="$INSTALL_DIR/configs/server.yaml"
if [ ! -f "$CFG" ]; then
    echo "==> 生成默认配置"
    $BIN default server > "$CFG" 2>/dev/null || cat > "$CFG" <<EOF
overlay:
  cidr: 100.64.0.0/10
  server_ip: 100.64.0.1
publish:
  port_range: 10100-10999
control:
  addr: :10000
tls: true
relay:
  addr: :10002
dashboard: 127.0.0.1:10004
update_repo: $REPO
EOF
    echo "已生成 $CFG"
fi

# ---------- 接入密码 ----------
JOIN="$CFG.join"
if [ ! -f "$JOIN" ] || [ ! -s "$JOIN" ]; then
    PASSWORD="$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    $BIN access -c "$CFG" -password "$PASSWORD"
    echo "==> 已生成客户端接入密码"
    echo "    客户端连接服务端时使用此密码"
    echo "    ─────────────────────────────────────────"
    echo "    $PASSWORD"
    echo "    ─────────────────────────────────────────"
    echo "    已保存到 $JOIN"
fi

# ---------- 管理密码 ----------
if [ ! -f "$CFG.passwd" ]; then
    echo
    echo "==> 设置服务端 Web 控制台登录密码（至少 8 位）"
    echo "    这个密码只你自己用，别泄露。"
    $BIN passwd -c "$CFG"
fi

# ---------- systemd ----------
UNIT="/etc/systemd/system/${SYSTEMD_SERVICE}.service"
echo "==> 写入 systemd unit $UNIT"
cat <<EOF | $SUDO tee "$UNIT" >/dev/null
[Unit]
Description=mtun server
After=network.target

[Service]
Type=simple
ExecStart=$BIN server -c $CFG
Restart=on-failure
RestartSec=2
WorkingDirectory=$INSTALL_DIR
StandardOutput=append:$INSTALL_DIR/logs/server.log
StandardError=append:$INSTALL_DIR/logs/server.err

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
    echo "✅ mtun 已启动"
    echo ""
    echo "  安装目录:       $INSTALL_DIR"
    echo "  配置文件:       $CFG"
    echo "  控制台:         http://127.0.0.1:10004"
    echo "  控制面端口:     :10000 (TLS 1.3)"
    echo "  转发端口范围:   10100-10999"
    echo ""
    echo "  查看状态:       systemctl status $SYSTEMD_SERVICE"
    echo "  查看日志:       journalctl -u $SYSTEMD_SERVICE -f"
    echo "  重启服务:       systemctl restart $SYSTEMD_SERVICE"
    echo "  在线更新:       $BIN update"
    echo "  卸载:           bash $0 -u"
    echo ""
    echo "  重要：客户端接入密码已生成，别忘了填到客户端。"
    echo "  如需重新生成：$BIN access -c $CFG -password '新密码'"
else
    echo "❌ 启动失败，查看详情：" >&2
    echo "   journalctl -u $SYSTEMD_SERVICE -n 50" >&2
    exit 1
fi
