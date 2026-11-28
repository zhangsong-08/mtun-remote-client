#!/usr/bin/env bash
# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT
#
# MTUN 服务端安装脚本（Linux / systemd）
# 最后更新: 2026-11-28
#
# 用法:
#   sudo bash install.sh                          # 自动选可用源（直连 → 国内镜像）
#   sudo bash install.sh -m https://ghfast.top/   # 指定加速镜像
#   sudo bash install.sh --file ./mtun-linux-amd64  # 离线安装（先把二进制传上来）
#   sudo bash install.sh -u                       # 卸载
#
# 说明: 安装过程中 Ctrl+C 不会中断脚本，避免把系统停在半安装状态。

set -Eeuo pipefail

# ---------- 元信息 ----------
readonly APP="MTUN"
readonly ROLE_NAME="服务端"
readonly COPYRIGHT="Copyright (c) 2026 song08 · SPDX-License-Identifier: MIT"
readonly LAST_UPDATED="2026-11-28"
readonly SCRIPT_REV="2026.11.28"
readonly REPO="zhangsong-08/mtun-remote-server"
readonly SYSTEMD_SERVICE="mtun-server"
readonly TOTAL_STEPS=9

# ---------- 默认值 ----------
VERSION=""
INSTALL_DIR="/opt/mtun"
MIRROR="${MTUN_MIRROR:-}"
LOCAL_FILE=""
ADMIN_PASSWORD="${MTUN_ADMIN_PASSWORD:-}"
JOIN_PASSWORD="${MTUN_JOIN_PASSWORD:-}"
PANEL_ADDR="${MTUN_PANEL:-127.0.0.1:10004}"
CONTROL_ADDR="${MTUN_CONTROL:-:10000}"
UNINSTALL=false

DEFAULT_MIRRORS=("https://ghfast.top/" "https://gh-proxy.com/" "")

# ---------- 输出样式 ----------
if [ -t 1 ]; then
    C_R=$'\033[0m'; C_B=$'\033[1m'; C_DIM=$'\033[2m'
    C_G=$'\033[32m'; C_Y=$'\033[33m'; C_C=$'\033[36m'; C_RED=$'\033[31m'
else
    C_R=""; C_B=""; C_DIM=""; C_G=""; C_Y=""; C_C=""; C_RED=""
fi

STEP=0
ts() { date '+%H:%M:%S'; }
step() {
    STEP=$((STEP + 1))
    printf '\n%s[%s] [%d/%d] %s%s\n' "$C_C" "$(ts)" "$STEP" "$TOTAL_STEPS" "$*" "$C_R"
}
info()   { printf '        · %s\n' "$*"; }
detail() { printf '        %s%s%s\n' "$C_DIM" "$*" "$C_R"; }
ok()     { printf '        %s[OK]%s %s\n' "$C_G" "$C_R" "$*"; }
warn()   { printf '        %s[!]%s %s\n' "$C_Y" "$C_R" "$*"; }
fail()   { printf '        %s[失败]%s %s\n' "$C_RED" "$C_R" "$*"; }

print_banner() {
    printf '\n%s================================================================%s\n' "$C_C" "$C_R"
    printf '%s\n' "${C_C}  __  __ _____ _   _ _   _${C_R}"
    printf '%s\n' "${C_C} |  \/  |_   _| | | | \ | |${C_R}"
    printf '%s\n' "${C_C} | |\/| | | | | | | |  \| |${C_R}"
    printf '%s\n' "${C_C} | |  | | | | | |_| | |\  |${C_R}"
    printf '%s\n' "${C_C} |_|  |_| |_|  \___/|_| \_|${C_R}"
    printf '%s\n' "${C_C}================================================================${C_R}"
    printf '  %s%s%s 内网穿透 · %s安装脚本\n' "$C_B" "$APP" "$C_R" "$ROLE_NAME"
    printf '  %s%s%s\n' "$C_DIM" "$COPYRIGHT" "$C_R"
    printf '  最后更新: %s    脚本版本: %s\n' "$LAST_UPDATED" "$SCRIPT_REV"
    printf '%s================================================================%s\n' "$C_C" "$C_R"
}

print_options() {
    printf '\n%s安装选项%s\n' "$C_B" "$C_R"
    printf '  安装目录:     %s\n' "$INSTALL_DIR"
    printf '  面板地址:     %s\n' "$PANEL_ADDR"
    printf '  控制面端口:   %s\n' "$CONTROL_ADDR"
    printf '  安装版本:     %s\n' "${VERSION:-自动获取最新 release}"
    printf '  下载源:       %s\n' "${MIRROR:-自动（镜像 → 直连）}"
    printf '  离线文件:     %s\n' "${LOCAL_FILE:-不使用}"
    printf '  管理密码:     %s\n' "$([ -n "$ADMIN_PASSWORD" ] && echo '由 -p 指定' || echo '交互输入 / 非交互自动生成')"
    printf '  接入密码:     %s\n' "$([ -n "$JOIN_PASSWORD" ] && echo '由 --join-password 指定' || echo '保留已有 / 自动生成')"
    printf '  服务名:       %s\n' "$SYSTEMD_SERVICE"
}

usage() {
    print_banner
    cat <<EOF

${C_B}用法${C_R}
  sudo bash install.sh [选项]

${C_B}安装选项${C_R}（全部可省略，使用默认值）
  -d, --dir DIR            安装目录                    默认: /opt/mtun
  --panel ADDR             面板监听地址                默认: 127.0.0.1:10004
                           需要远程访问面板时用 0.0.0.0:10004
  --control ADDR           客户端接入的控制面监听地址  默认: :10000
  -v, --version TAG        安装指定版本                默认: 最新 release
  -m, --mirror URL         GitHub 加速前缀，如 https://ghfast.top/
  -f, --file PATH          离线安装：使用本地 linux 二进制，不联网下载
  -p, --password PW        面板管理密码（至少 8 位），不交互
  --join-password PW       默认接入密码（至少 8 位）；省略则保留已有或自动生成
  -u, --uninstall          卸载（停服务、删 systemd unit、删安装目录）
  -h, --help               显示本帮助

${C_B}环境变量${C_R}
  MTUN_MIRROR           同 -m
  MTUN_ADMIN_PASSWORD   同 -p
  MTUN_JOIN_PASSWORD    同 --join-password
  MTUN_PANEL            同 --panel
  MTUN_CONTROL          同 --control

${C_B}示例${C_R}
  sudo bash install.sh -p '你的面板密码'
  sudo bash install.sh -m https://ghfast.top/
  sudo bash install.sh --panel 0.0.0.0:10004 --control :10000
  scp mtun-linux-amd64 root@服务器:/tmp/ && sudo bash install.sh --file /tmp/mtun-linux-amd64
  sudo bash install.sh -u

${C_B}提示${C_R}
  安装过程中 Ctrl+C 会被忽略，脚本会继续执行，避免中断造成半安装状态。
  如确需中止，请关闭终端窗口，或另开终端执行: sudo kill -TERM <脚本PID>

EOF
}

# ---------- 参数解析 ----------
while [[ $# -gt 0 ]]; do
    case "$1" in
        -v|--version)     VERSION="$2"; shift 2 ;;
        -d|--dir)         INSTALL_DIR="$2"; shift 2 ;;
        -m|--mirror)      MIRROR="$2"; shift 2 ;;
        -f|--file)        LOCAL_FILE="$2"; shift 2 ;;
        -p|--password)    ADMIN_PASSWORD="$2"; shift 2 ;;
        --join-password)  JOIN_PASSWORD="$2"; shift 2 ;;
        --panel)          PANEL_ADDR="$2"; shift 2 ;;
        --control)        CONTROL_ADDR="$2"; shift 2 ;;
        -u|--uninstall)   UNINSTALL=true; shift ;;
        -h|--help)        usage; exit 0 ;;
        *) printf '未知参数: %s（用 -h 查看帮助）\n' "$1" >&2; exit 1 ;;
    esac
done

# ---------- Ctrl+C 防误触 ----------
# 忽略 SIGINT：脚本与其子进程（curl 等）都不会被 Ctrl+C 中断，
# 避免安装到一半退出导致 systemd / 配置处于半成品状态。
trap '' INT

# ---------- 临时文件 ----------
TMP_FILES=()
new_tmp() {
    local t
    t="$(mktemp)"
    TMP_FILES+=("$t")
    printf '%s' "$t"
}
cleanup() {
    local f
    for f in "${TMP_FILES[@]:-}"; do
        [ -n "$f" ] && rm -f "$f" 2>/dev/null || true
    done
}
trap cleanup EXIT

# ---------- 工具 ----------
fmt_size() {
    awk -v bytes="${1:-0}" 'BEGIN {
        split("B KB MB GB TB", unit, " ");
        i = 1;
        while (bytes >= 1024 && i < 5) { bytes /= 1024; i++ }
        if (i == 1) printf "%d %s", bytes, unit[i];
        else printf "%.1f %s", bytes, unit[i];
    }'
}

have() { command -v "$1" >/dev/null 2>&1; }

# file_size <路径>：快速取文件字节数（不存在返回 0）
file_size() {
    if [ ! -f "$1" ]; then
        printf '0'
        return 0
    fi
    if stat -c %s "$1" >/dev/null 2>&1; then
        stat -c %s "$1"
    else
        wc -c < "$1" | tr -d ' '
    fi
}

# remote_size <url>：尽力取 Content-Length，取不到返回 0
remote_size() {
    local size=""
    if have curl; then
        size="$(curl -fsSIL --connect-timeout 8 --max-time 25 "$1" 2>/dev/null \
            | awk 'BEGIN{IGNORECASE=1} /^content-length:/{v=$2} END{if(v)print v}' | tr -d '\r' || true)"
    elif have wget; then
        size="$(wget --spider -S --timeout=15 "$1" 2>&1 \
            | awk 'BEGIN{IGNORECASE=1} /content-length:/{v=$2} END{if(v)print v}' | tr -d '\r' || true)"
    fi
    case "$size" in
        ''|*[!0-9]*) printf '0' ;;
        *) printf '%s' "$size" ;;
    esac
}

draw_progress() {
    local done="$1" total="$2" speed="$3"
    local pct filled bar
    if [ "$total" -gt 0 ]; then
        pct=$((done * 100 / total))
        [ "$pct" -gt 100 ] && pct=100
        filled=$((pct * 24 / 100))
        bar="$(printf '%*s' "$filled" '' | tr ' ' '#')$(printf '%*s' $((24 - filled)) '' | tr ' ' '-')"
        printf '\r        [%s] %3d%%  %s / %s  %s/s   ' \
            "$bar" "$pct" "$(fmt_size "$done")" "$(fmt_size "$total")" "$(fmt_size "$speed")"
    else
        printf '\r        已下载 %s  %s/s   ' "$(fmt_size "$done")" "$(fmt_size "$speed")"
    fi
}

# download <github 绝对 url> <输出文件>
download() {
    local url="$1" out="$2"
    local prefix target total size speed start elapsed pid rc

    for prefix in "${MIRRORS[@]}"; do
        target="${prefix}${url}"
        info "下载源: ${prefix:-GitHub 直连}"
        total="$(remote_size "$target")"
        if [ "$total" -gt 0 ]; then
            detail "文件大小: $(fmt_size "$total")"
        else
            detail "文件大小: 未知（将按已下载量显示）"
        fi

        : > "$out"
        if have curl; then
            curl -fsSL --connect-timeout 10 --max-time 900 -o "$out" "$target" &
        else
            wget -q --timeout=30 --tries=1 -O "$out" "$target" &
        fi
        pid=$!
        start="$(date +%s)"
        speed=0
        while kill -0 "$pid" 2>/dev/null; do
            size="$(file_size "$out")"
            elapsed=$(( $(date +%s) - start ))
            [ "$elapsed" -lt 1 ] && elapsed=1
            speed=$((size / elapsed))
            draw_progress "$size" "$total" "$speed"
            sleep 1
        done
        rc=0
        wait "$pid" || rc=$?
        size="$(file_size "$out")"
        [ "$total" -gt 0 ] && [ "$size" -lt "$total" ] && rc=1
        if [ "$rc" -eq 0 ] && [ "$size" -gt 0 ]; then
            [ "$total" -le 0 ] && total="$size"
            elapsed=$(( $(date +%s) - start ))
            [ "$elapsed" -lt 1 ] && elapsed=1
            speed=$((size / elapsed))
            draw_progress "$size" "$total" "$speed"
            printf '\n'
            ok "下载完成: $(fmt_size "$size")，平均 $(fmt_size "$speed")/s"
            return 0
        fi
        printf '\n'
        warn "该源下载失败（退出码 $rc），切换下一个源"
        sleep 1
    done
    return 1
}

# 静默下载（checksums.txt 等小文件）
fetch_quiet() {
    local url="$1" out="$2" prefix
    for prefix in "${MIRRORS[@]}"; do
        if have curl; then
            curl -fsSL --connect-timeout 8 --max-time 40 -o "$out" "${prefix}${url}" && return 0
        elif have wget; then
            wget -q --timeout=25 --tries=1 -O "$out" "${prefix}${url}" && return 0
        fi
    done
    return 1
}

latest_version() {
    local v prefix
    if have gh; then
        v="$(gh release view --repo "$REPO" --json tagName -q .tagName 2>/dev/null || true)"
        [ -n "$v" ] && { printf '%s' "$v"; return 0; }
    fi
    have curl || return 1
    for prefix in "${MIRRORS[@]}"; do
        v="$(curl -fsSL --connect-timeout 8 --max-time 25 \
            "${prefix}https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | grep -oE '"tag_name":\s*"[^"]+"' | head -1 | cut -d'"' -f4 || true)"
        [ -n "$v" ] && { printf '%s' "$v"; return 0; }
        v="$(curl -fsSI --connect-timeout 8 --max-time 25 \
            "${prefix}https://github.com/${REPO}/releases/latest" 2>/dev/null \
            | grep -iE '^location:' | tr -d '\r' | head -1 || true)"
        v="${v##*/}"
        [ -n "$v" ] && { printf '%s' "$v"; return 0; }
    done
    return 1
}

# ---------- 主流程 ----------
print_banner
print_options
warn "已禁用 Ctrl+C：安装过程不会因误按而中断（确需中止请关闭终端）"

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
    if have sudo; then
        SUDO="sudo"
    else
        fail "请用 root 或 sudo 执行"
        exit 1
    fi
fi
detail "执行权限: $([ -n "$SUDO" ] && echo 'sudo 提权' || echo 'root')"

# ---------- 卸载 ----------
if [ "$UNINSTALL" = true ]; then
    step "卸载 MTUN 服务端"
    info "停止服务 $SYSTEMD_SERVICE"
    $SUDO systemctl stop "$SYSTEMD_SERVICE" 2>/dev/null || true
    $SUDO systemctl disable "$SYSTEMD_SERVICE" 2>/dev/null || true
    info "删除 systemd unit"
    $SUDO rm -f "/etc/systemd/system/${SYSTEMD_SERVICE}.service"
    $SUDO systemctl daemon-reload
    info "结束残留进程"
    $SUDO pkill -f "$INSTALL_DIR/bin/mtun" 2>/dev/null || true
    sleep 1
    info "删除安装目录 $INSTALL_DIR"
    $SUDO rm -rf "$INSTALL_DIR"
    ok "已卸载：服务、unit、$INSTALL_DIR 均已删除"
    exit 0
fi

# ---------- 架构 ----------
step "检查运行环境"
info "系统: $(uname -s) $(uname -r)"
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)  BIN_ARCH="amd64" ;;
    aarch64|arm64) BIN_ARCH="arm64" ;;
    *) fail "不支持的架构: $ARCH"; exit 1 ;;
esac
ASSET="mtun-linux-${BIN_ARCH}"
ok "架构 $ARCH → $ASSET"
for cmd in curl wget; do
    have "$cmd" && detail "已安装: $cmd" || true
done
have curl || have wget || { fail "需要 curl 或 wget 之一"; exit 1; }

# ---------- 版本 ----------
step "确定安装版本"
if [ -n "$MIRROR" ]; then
    MIRRORS=("$MIRROR")
    detail "使用指定镜像: $MIRROR"
else
    MIRRORS=("${DEFAULT_MIRRORS[@]}")
    detail "镜像顺序: 国内加速 ×2 → GitHub 直连"
fi

if [ -z "$VERSION" ]; then
    info "查询最新 release"
    VERSION="$(latest_version || true)"
fi
if [ -z "$VERSION" ]; then
    fail "无法获取版本号（网络不通）"
    detail "请用 -v 指定版本，或使用 --file 离线安装"
    exit 1
fi
ok "目标版本: $VERSION"

# ---------- 下载 ----------
step "下载并校验二进制"
$SUDO mkdir -p "$INSTALL_DIR"/{bin,configs,logs}
BIN="$INSTALL_DIR/bin/mtun"
TMP="$(new_tmp)"

if [ -n "$LOCAL_FILE" ]; then
    info "离线安装，使用本地文件: $LOCAL_FILE"
    [ -f "$LOCAL_FILE" ] || { fail "文件不存在: $LOCAL_FILE"; exit 1; }
    cp "$LOCAL_FILE" "$TMP"
    ok "已复制 $(fmt_size "$(wc -c < "$TMP")")"
else
    if ! download "https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}" "$TMP"; then
        fail "下载失败"
        detail "国内服务器可加镜像: -m https://ghfast.top/"
        detail "或本机下载后 scp 上传，再用 --file 离线安装"
        exit 1
    fi
fi

if [ -z "$LOCAL_FILE" ] && have sha256sum; then
    SUM_TMP="$(new_tmp)"
    info "校验 SHA256"
    if fetch_quiet "https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt" "$SUM_TMP"; then
        want="$(awk -v f="$ASSET" '$2==f || $2=="*"f {print $1; exit}' "$SUM_TMP")"
        got="$(sha256sum "$TMP" | awk '{print $1}')"
        if [ -n "$want" ] && [ "$want" = "$got" ]; then
            ok "checksum 匹配: ${got:0:16}…"
        else
            warn "checksum 不匹配（期望 ${want:-空}，实际 ${got:0:16}…）"
            warn "请核实下载来源后再决定是否继续"
        fi
    else
        warn "未能下载 checksums.txt，跳过校验"
    fi
fi

# ---------- 安装二进制 ----------
step "安装二进制"
info "目标: $BIN"
$SUDO mv "$TMP" "$BIN"
$SUDO chmod +x "$BIN"
"$BIN" version
ok "二进制就绪"

# ---------- 配置 ----------
step "生成并校验配置"
CFG="$INSTALL_DIR/configs/server.yaml"
if [ ! -f "$CFG" ]; then
    info "生成默认配置: $CFG"
    TMP_CFG="$(new_tmp)"
    if ! "$BIN" default server > "$TMP_CFG" 2>/dev/null || [ ! -s "$TMP_CFG" ]; then
        cat > "$TMP_CFG" <<EOF
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
    $SUDO cp "$TMP_CFG" "$CFG"
    ok "已生成配置"
else
    info "检测到已有配置，保留原有内容: $CFG"
fi

apply_scalar() {
    local key="$1" value="$2"
    if grep -qE "^${key}:" "$CFG"; then
        $SUDO sed -i "s|^${key}:.*|${key}: \"${value}\"|" "$CFG"
    else
        printf '%s: "%s"\n' "$key" "$value" | $SUDO tee -a "$CFG" >/dev/null
    fi
}

if [ "$PANEL_ADDR" != "127.0.0.1:10004" ]; then
    info "面板监听地址 → $PANEL_ADDR"
    apply_scalar dashboard "$PANEL_ADDR"
fi
if [ "$CONTROL_ADDR" != ":10000" ]; then
    info "控制面端口 → $CONTROL_ADDR"
    apply_scalar control "$CONTROL_ADDR"
fi

if ! "$BIN" check -c "$CFG" -role server >/dev/null 2>&1; then
    fail "配置文件校验失败"
    "$BIN" check -c "$CFG" -role server >&2 || true
    detail "请手动修正 $CFG 后重试"
    exit 1
fi
ok "配置校验通过"

# ---------- 接入密码 ----------
step "配置客户端接入密码"
JOIN="$CFG.join"
if [ -n "$JOIN_PASSWORD" ]; then
    if [ "${#JOIN_PASSWORD}" -lt 8 ]; then
        fail "接入密码至少 8 位"
        exit 1
    fi
    if ! OUT="$("$BIN" access -c "$CFG" -password "$JOIN_PASSWORD" 2>&1)"; then
        fail "写入接入密码失败"
        printf '%s\n' "$OUT" | sed 's/^/        /'
        exit 1
    fi
    ok "已设置为 --join-password 指定的密码"
    JOIN_DISPLAY="$JOIN_PASSWORD"
elif [ -f "$JOIN" ] && [ -s "$JOIN" ]; then
    info "检测到已有接入密码，保持不变: $JOIN"
    JOIN_DISPLAY=""
else
    JOIN_PASSWORD="$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    if ! OUT="$("$BIN" access -c "$CFG" -password "$JOIN_PASSWORD" 2>&1)"; then
        fail "生成接入密码失败"
        printf '%s\n' "$OUT" | sed 's/^/        /'
        exit 1
    fi
    ok "已随机生成 36 位接入密码"
    JOIN_DISPLAY="$JOIN_PASSWORD"
fi
detail "密码文件: $JOIN（只保存哈希，明文不落盘）"

# ---------- 管理密码 ----------
step "配置面板管理密码"
if [ -f "$CFG.passwd" ]; then
    info "管理密码已存在，跳过: $CFG.passwd"
    ADMIN_DISPLAY=""
else
    if [ -n "$ADMIN_PASSWORD" ]; then
        if [ "${#ADMIN_PASSWORD}" -lt 8 ]; then
            fail "管理密码至少 8 位"
            exit 1
        fi
        info "使用 -p 指定的密码"
        if ! "$BIN" passwd -c "$CFG" -password "$ADMIN_PASSWORD" >/dev/null; then
            fail "写入管理密码失败"
            exit 1
        fi
        ADMIN_DISPLAY="$ADMIN_PASSWORD"
    elif [ -t 0 ]; then
        info "交互设置（至少 8 位）"
        "$BIN" passwd -c "$CFG"
        ADMIN_DISPLAY=""
    else
        ADMIN_PASSWORD="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
        if ! "$BIN" passwd -c "$CFG" -password "$ADMIN_PASSWORD" >/dev/null; then
            fail "写入管理密码失败"
            exit 1
        fi
        info "非交互模式，已自动生成管理密码"
        ADMIN_DISPLAY="$ADMIN_PASSWORD"
    fi
    ok "管理密码已保存: $CFG.passwd"
fi

# ---------- systemd ----------
step "注册 systemd 服务"
UNIT="/etc/systemd/system/${SYSTEMD_SERVICE}.service"
info "写入 $UNIT"
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

info "systemctl daemon-reload / enable / restart"
$SUDO systemctl daemon-reload
$SUDO systemctl enable "$SYSTEMD_SERVICE" >/dev/null 2>&1
$SUDO systemctl restart "$SYSTEMD_SERVICE"

# ---------- 启动确认 ----------
step "确认服务状态"
printf '        等待启动'
for i in {1..20}; do
    if systemctl is-active --quiet "$SYSTEMD_SERVICE"; then
        printf '\r        %s[OK]%s 服务已启动（%d 秒）\n' "$C_G" "$C_R" "$i"
        break
    fi
    printf '.'
    sleep 0.5
    if [ "$i" -eq 20 ]; then
        printf '\n'
        fail "启动失败，最近日志如下："
        journalctl -u "$SYSTEMD_SERVICE" -n 30 --no-pager >&2 || true
        exit 1
    fi
done

# ---------- 汇总 ----------
printf '\n%s================================================================%s\n' "$C_C" "$C_R"
printf '  %s安装完成%s\n' "$C_B" "$C_G"
printf '%s================================================================%s\n' "$C_C" "$C_R"
printf '  安装目录:       %s\n' "$INSTALL_DIR"
printf '  配置文件:       %s\n' "$CFG"
printf '  面板地址:       http://%s\n' "$PANEL_ADDR"
printf '  控制面端口:     %s（TLS 1.3）\n' "$CONTROL_ADDR"
printf '  转发端口范围:   10100-10999\n'
printf '  管理密码:       %s\n' "${ADMIN_DISPLAY:-（沿用原有密码）}"
printf '  接入密码:       %s\n' "${JOIN_DISPLAY:-（沿用原有密码）}"
if [ -z "$JOIN_DISPLAY" ]; then
    printf '                  %s已保存哈希，明文无法查看；需重置请执行 mtun access -c %s%s\n' "$C_DIM" "$CFG" "$C_R"
fi
printf '%s----------------------------------------------------------------%s\n' "$C_C" "$C_R"
printf '  查看状态:       systemctl status %s\n' "$SYSTEMD_SERVICE"
printf '  查看日志:       journalctl -u %s -f\n' "$SYSTEMD_SERVICE"
printf '  重启服务:       systemctl restart %s\n' "$SYSTEMD_SERVICE"
printf '  在线更新:       %s update -c %s\n' "$BIN" "$CFG"
printf '  离线更新:       sudo bash install.sh --file 新二进制\n'
printf '  卸载:           sudo bash install.sh -u\n'
printf '%s================================================================%s\n' "$C_C" "$C_R"
printf '\n  %s注意%s\n' "$C_B" "$C_R"
printf '  1) 公网访问还需放行防火墙/安全组：10000/tcp、10100-10999/tcp\n'
printf '  2) 面板默认只监听本机，远程查看请用: ssh -L 10004:127.0.0.1:10004 用户@服务器IP\n'
printf '  3) 新增设备时把「接入密码」填给客户端安装脚本 -k 参数\n\n'
