#!/usr/bin/env bash
# mtun 服务端一键安装脚本（Linux）
#
# 用法:
#   sudo bash install.sh                          # 自动选可用源（直连 → 国内镜像）
#   sudo bash install.sh -m https://ghfast.top/   # 指定加速镜像
#   sudo bash install.sh --file ./mtun-linux-amd64  # 离线安装（先把二进制传上来）
#   sudo bash install.sh -u                       # 卸载
set -euo pipefail

REPO="zhangsong-08/mtun"
VERSION=""          # 留空自动取 latest
INSTALL_DIR="/opt/mtun"
MIRROR="${MTUN_MIRROR:-}"
LOCAL_FILE=""
ADMIN_PASSWORD="${MTUN_ADMIN_PASSWORD:-}"
PANEL_ADDR="${MTUN_PANEL:-127.0.0.1:10004}"
UNINSTALL=false
SYSTEMD_SERVICE="mtun-server"

# 内置可用镜像（按顺序尝试）。直连放最后，因为国内多数网络直连 GitHub 会超时。
DEFAULT_MIRRORS=("https://ghfast.top/" "https://gh-proxy.com/" "")

# ---------- 解析参数 ----------
while [[ $# -gt 0 ]]; do
    case "$1" in
        -v|--version)  VERSION="$2";  shift 2 ;;
        -d|--dir)      INSTALL_DIR="$2"; shift 2 ;;
        -m|--mirror)   MIRROR="$2"; shift 2 ;;
        -f|--file)     LOCAL_FILE="$2"; shift 2 ;;
        -p|--password) ADMIN_PASSWORD="$2"; shift 2 ;;
        --panel)       PANEL_ADDR="$2"; shift 2 ;;
        -u|--uninstall) UNINSTALL=true; shift ;;
        -h|--help)
            cat <<EOF
mtun 服务端一键安装脚本（Linux / systemd）

用法: sudo bash install.sh [选项]

选项:
  -v, --version TAG    指定版本（默认最新 release）
  -d, --dir DIR        安装目录（默认 /opt/mtun）
  -m, --mirror URL     GitHub 加速前缀，如 https://ghfast.top/
                       也可用环境变量 MTUN_MIRROR
  -f, --file PATH      离线安装：使用本地已有的 linux 二进制，不联网下载
  -p, --password PW    直接设置 Web 控制台管理密码（至少 8 位），不交互
                       也可用环境变量 MTUN_ADMIN_PASSWORD
  --panel ADDR         面板监听地址，默认 127.0.0.1:10004
                       需要远程访问面板时用 0.0.0.0:10004
                       也可用环境变量 MTUN_PANEL
  -u, --uninstall      卸载（停服务、删 systemd unit、删目录）
  -h, --help           本帮助

国内服务器访问不了 GitHub 时，推荐：
  sudo bash install.sh -m https://ghfast.top/
  # 或本机下载后 scp 上传，再离线安装：
  #   scp mtun-linux-amd64 root@服务器:/tmp/
  #   sudo bash install.sh --file /tmp/mtun-linux-amd64
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
    # 清理手动启动的残留进程（只匹配本安装目录下的 mtun）
    $SUDO pkill -f "$INSTALL_DIR/bin/mtun" 2>/dev/null || true
    sleep 1
    $SUDO rm -rf "$INSTALL_DIR"
    echo "已卸载：服务、unit、$INSTALL_DIR 均已删除"
    exit 0
fi

# ---------- 架构 ----------
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)  BIN_ARCH="amd64" ;;
    aarch64|arm64) BIN_ARCH="arm64" ;;
    *) echo "不支持的架构: $ARCH" >&2; exit 1 ;;
esac
ASSET="mtun-linux-${BIN_ARCH}"

# ---------- 镜像列表 ----------
if [ -n "$MIRROR" ]; then
    MIRRORS=("$MIRROR")
else
    MIRRORS=("${DEFAULT_MIRRORS[@]}")
fi

# ---------- 下载 ----------
# fetch_github <github 绝对 URL> <输出文件>
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

# latest_version：API 优先，失败则解析 releases/latest 的 302 跳转
latest_version() {
    local v prefix
    if command -v gh >/dev/null 2>&1; then
        v="$(gh release view --repo "$REPO" --json tagName -q .tagName 2>/dev/null || true)"
        [ -n "$v" ] && { echo "$v"; return 0; }
    fi
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

# ---------- 取版本 ----------
if [ -z "$VERSION" ]; then
    echo "==> 查询最新 release"
    VERSION="$(latest_version || true)"
fi
if [ -z "$VERSION" ]; then
    echo "无法获取版本号（网络不通）。请用 -v 指定，例如：" >&2
    echo "  sudo bash install.sh -v v0.2.1 -m https://ghfast.top/" >&2
    echo "或改用离线安装：" >&2
    echo "  sudo bash install.sh --file /tmp/mtun-linux-amd64" >&2
    exit 1
fi
echo "==> 使用版本 $VERSION，架构 linux-${BIN_ARCH}"

$SUDO mkdir -p "$INSTALL_DIR"/{bin,configs,logs}
BIN="$INSTALL_DIR/bin/mtun"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

# ---------- 获取二进制 ----------
if [ -n "$LOCAL_FILE" ]; then
    echo "==> 离线安装，使用本地文件 $LOCAL_FILE"
    [ -f "$LOCAL_FILE" ] || { echo "文件不存在: $LOCAL_FILE" >&2; exit 1; }
    cp "$LOCAL_FILE" "$TMP"
else
    echo "==> 下载 $ASSET"
    if ! fetch_github "https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}" "$TMP"; then
        echo "" >&2
        echo "下载失败。国内服务器常见处理方式：" >&2
        echo "  1) 指定加速镜像:  sudo bash install.sh -m https://ghfast.top/" >&2
        echo "  2) 离线安装:      本机下载 mtun-linux-${BIN_ARCH}，scp 上传后" >&2
        echo "                    sudo bash install.sh --file /path/to/mtun-linux-${BIN_ARCH}" >&2
        exit 1
    fi
    size="$(wc -c < "$TMP" | tr -d ' ')"
    echo "  已下载 $((size / 1024 / 1024)) MB"
fi

# ---------- 校验 ----------
if [ -z "$LOCAL_FILE" ] && command -v sha256sum >/dev/null 2>&1; then
    SUM_TMP="$(mktemp)"
    if fetch_github "https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt" "$SUM_TMP" 2>/dev/null; then
        want="$(awk -v f="$ASSET" '$2==f || $2=="*"f {print $1; exit}' "$SUM_TMP")"
        got="$(sha256sum "$TMP" | awk '{print $1}')"
        if [ -n "$want" ] && [ "$want" = "$got" ]; then
            echo "==> checksum 校验通过"
        else
            echo "==> 警告：checksum 不匹配（期望 ${want:-空}，实际 $got），请核实下载来源"
        fi
    else
        echo "==> 跳过 checksum（下载失败）"
    fi
    rm -f "$SUM_TMP"
fi

# ---------- 安装 ----------
$SUDO mv "$TMP" "$BIN"
trap - EXIT
$SUDO chmod +x "$BIN"
"$BIN" version

# ---------- 配置 ----------
CFG="$INSTALL_DIR/configs/server.yaml"
if [ ! -f "$CFG" ]; then
    echo "==> 生成默认配置"
    # 优先用二进制自带的默认配置生成，保证与程序结构一致
    if ! $BIN default server > "$CFG" 2>/dev/null || [ ! -s "$CFG" ]; then
        cat > "$CFG" <<EOF
role: server
control: ":10000"
wg: ":10001"
relay: ":10002"
stun: ":10003"
dashboard: "127.0.0.1:10004"
update_repo: "$REPO"
overlay:
  cidr: "100.64.0.0/10"
  server_ip: "100.64.0.1"
publish:
  port_range: "10100-10999"
EOF
    fi
    # 生成后立即校验，配置有误就明确报错，避免服务反复重启
    if ! $BIN check -c "$CFG" -role server >/dev/null 2>&1; then
        echo "配置文件校验失败：" >&2
        $BIN check -c "$CFG" -role server >&2 || true
        echo "请手动修正 $CFG 后重试" >&2
        exit 1
    fi
    echo "已生成 $CFG"
fi

# 应用面板监听地址（默认回环，可传 --panel 0.0.0.0:10004 开放远程访问）
if [ "$PANEL_ADDR" != "127.0.0.1:10004" ]; then
    if grep -qE '^dashboard:' "$CFG"; then
        $SUDO sed -i "s|^dashboard:.*|dashboard: \"$PANEL_ADDR\"|" "$CFG"
    else
        printf 'dashboard: "%s"\n' "$PANEL_ADDR" | $SUDO tee -a "$CFG" >/dev/null
    fi
    if ! $BIN check -c "$CFG" -role server >/dev/null 2>&1; then
        echo "面板地址 $PANEL_ADDR 无效：" >&2
        $BIN check -c "$CFG" -role server >&2 || true
        exit 1
    fi
    echo "面板监听地址已设为 $PANEL_ADDR"
fi

# ---------- 接入密码 ----------
JOIN="$CFG.join"
if [ ! -f "$JOIN" ] || [ ! -s "$JOIN" ]; then
    PASSWORD="$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    $BIN access -c "$CFG" -password "$PASSWORD"
    echo "==> 已生成客户端接入密码"
    echo "    ─────────────────────────────────────────"
    echo "    $PASSWORD"
    echo "    ─────────────────────────────────────────"
    echo "    已保存到 $JOIN"
fi

# ---------- 管理密码 ----------
if [ ! -f "$CFG.passwd" ]; then
    echo
    if [ -n "$ADMIN_PASSWORD" ]; then
        if [ "${#ADMIN_PASSWORD}" -lt 8 ]; then
            echo "管理密码至少 8 位" >&2
            exit 1
        fi
        echo "==> 设置管理密码（-p 指定）"
        $BIN passwd -c "$CFG" -password "$ADMIN_PASSWORD"
    elif [ -t 0 ]; then
        echo "==> 设置服务端 Web 控制台登录密码（至少 8 位）"
        $BIN passwd -c "$CFG"
    else
        # 非交互（如 curl | bash）时自动生成，避免卡住或失败
        ADMIN_PASSWORD="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
        $BIN passwd -c "$CFG" -password "$ADMIN_PASSWORD"
        echo "==> 已自动生成管理密码（非交互模式）"
        echo "    ─────────────────────────────────────────"
        echo "    $ADMIN_PASSWORD"
        echo "    ─────────────────────────────────────────"
        echo "    可用 -p 指定自定义密码，或之后 mtun passwd -c $CFG 修改"
    fi
fi

# ---------- systemd ----------
UNIT="/etc/systemd/system/${SYSTEMD_SERVICE}.service"
echo "==> 写入 systemd unit $UNIT"
cat <<EOF | $SUDO tee "$UNIT" >/dev/null
[Unit]
Description=mtun server
After=network.target
# 启动失败时不要无限重启刷屏
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
ExecStart=$BIN server -c $CFG
Restart=on-failure
RestartSec=2
WorkingDirectory=$INSTALL_DIR
# 输出到 journal，systemd 219（CentOS 7）也兼容，出问题可直接 journalctl 查看
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
    JOIN_PW="$(cat "$JOIN" 2>/dev/null || echo '(见上方)')"
    echo
    echo "✅ mtun 已启动"
    echo ""
    echo "  安装目录:       $INSTALL_DIR"
    echo "  配置文件:       $CFG"
    echo "  控制台:         http://${PANEL_ADDR}（默认仅本机；改为 0.0.0.0 后可从外部访问）"
    echo "  控制面端口:     :10000 (TLS 1.3)"
    echo "  转发端口范围:   10100-10999"
    echo "  客户端接入密码: $JOIN_PW"
    echo ""
    echo "  查看状态:       systemctl status $SYSTEMD_SERVICE"
    echo "  查看日志:       journalctl -u $SYSTEMD_SERVICE -f"
    echo "  重启服务:       systemctl restart $SYSTEMD_SERVICE"
    echo "  在线更新:       MTUN_UPDATE_REPO=$REPO $BIN update"
    echo "  离线更新:       sudo bash install.sh --file 新二进制"
    echo "  卸载:           sudo bash install.sh -u"
    echo ""
    echo "  公网访问还需放行防火墙：10000/tcp、10100-10999/tcp"
    echo "  远程看面板：ssh -L 10004:127.0.0.1:10004 用户@服务器IP"
else
    echo "❌ 启动失败，以下是最近日志：" >&2
    journalctl -u "$SYSTEMD_SERVICE" -n 30 --no-pager >&2 || true
    exit 1
fi
