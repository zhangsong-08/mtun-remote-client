#!/usr/bin/env bash
# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT
#
# MTUN 客户端安装脚本（Linux / systemd）
# 最后更新: 2026-11-28
#
# 用法:
#   sudo bash install-client.sh -s 203.0.113.10:10000 -k 接入密码
#   sudo bash install-client.sh -s IP:10000 -k 密码 --panel 0.0.0.0:10005   # 面板允许公网访问
#   sudo bash install-client.sh -u                                          # 卸载
#
# 说明: 安装过程中 Ctrl+C 不会中断脚本，避免把系统停在半安装状态。

set -Eeuo pipefail

# ---------- 元信息 ----------
readonly APP="MTUN"
readonly ROLE_NAME="客户端"
readonly COPYRIGHT="Copyright (c) 2026 song08 · SPDX-License-Identifier: MIT"
readonly LAST_UPDATED="2026-11-28"
readonly SCRIPT_REV="2026.11.28"
readonly REPO="zhangsong-08/mtun-remote-client"
readonly SYSTEMD_SERVICE="mtun-client"
readonly TOTAL_STEPS=8

# ---------- 默认值 ----------
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
ASSUME_YES=false

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
    printf '  服务端地址:   %s\n' "${SERVER_ADDR:-（必填，未提供）}"
    printf '  接入密码:     %s\n' "$([ -n "$JOIN_KEY" ] && echo '已提供（不回显）' || echo '（必填，未提供）')"
    printf '  节点名称:     %s\n' "${NODE_NAME:-（默认使用主机名 $(hostname 2>/dev/null || echo unknown)）}"
    printf '  面板地址:     %s\n' "$PANEL_ADDR"
    printf '  安装版本:     %s\n' "${VERSION:-自动获取最新 release}"
    printf '  下载源:       %s\n' "${MIRROR:-自动（镜像 → 直连）}"
    printf '  离线文件:     %s\n' "${LOCAL_FILE:-不使用}"
    printf '  面板密码:     %s\n' "$([ -n "$ADMIN_PASSWORD" ] && echo '已填写' || echo '交互询问 / 自动生成')"
    printf '  服务名:       %s\n' "$SYSTEMD_SERVICE"
}

usage() {
    print_banner
    cat <<EOF

${C_B}用法${C_R}
  sudo bash install-client.sh [选项]

  不指定 -y 时进入交互式配置：逐项询问安装选项，直接回车即使用方括号内的默认值。
  -s / -k 也可以在交互提问时再填写；管道/非终端环境不会询问，必须用参数提供。

${C_B}必填${C_R}
  -s, --server HOST:PORT   服务端控制面地址，如 203.0.113.10:10000
  -k, --key PASSWORD       接入密码（服务端管理员提供，或用「加密」页为节点单独生成的密码）
                           也可用环境变量 MTUN_JOIN_TOKEN

${C_B}安装选项${C_R}（其余均可省略，使用默认值）
  -n, --node NAME          节点名称          默认: 主机名
  -d, --dir DIR            安装目录          默认: /opt/mtun-client
  --panel ADDR             面板监听地址      默认: 127.0.0.1:10005
                           需要远程访问面板时用 0.0.0.0:10005
  -p, --password PW        面板登录密码（至少 8 位）；留空则交互询问
  -v, --version TAG        安装指定版本      默认: 最新 release
  -m, --mirror URL         GitHub 加速前缀，如 https://ghfast.top/
  -f, --file PATH          离线安装：使用本地 linux 二进制
  -y, --yes                跳过交互询问，全部使用参数与默认值
  -u, --uninstall          卸载（停服务、删 systemd unit、删安装目录）
  -h, --help               显示本帮助

${C_B}环境变量${C_R}
  MTUN_JOIN_TOKEN       同 -k
  MTUN_MIRROR           同 -m
  MTUN_ADMIN_PASSWORD   同 -p
  MTUN_PANEL            同 --panel
  MTUN_NONINTERACTIVE=1 同 -y（跳过交互询问）

${C_B}示例${C_R}
  sudo bash install-client.sh                          # 交互式填写，回车取默认
  sudo bash install-client.sh -s 1.2.3.4:10000 -k '接入密码'   # 仍会询问其余选项
  sudo bash install-client.sh -s 1.2.3.4:10000 -k '接入密码' -y  # 全参数、不询问
  sudo bash install-client.sh -s 1.2.3.4:10000 -k '接入密码' -n nas --panel 0.0.0.0:10005 -f ./mtun-linux-amd64 -y
  sudo bash install-client.sh -u

${C_B}提示${C_R}
  安装过程中 Ctrl+C 会被忽略，脚本会继续执行，避免中断造成半安装状态。
  如确需中止，请关闭终端窗口，或另开终端执行: sudo kill -TERM <脚本PID>

EOF
}

# ---------- 参数解析 ----------
while [[ $# -gt 0 ]]; do
    case "$1" in
        -s|--server)    SERVER_ADDR="$2"; shift 2 ;;
        -k|--key)       JOIN_KEY="$2"; shift 2 ;;
        -n|--node)      NODE_NAME="$2"; shift 2 ;;
        -v|--version)   VERSION="$2"; shift 2 ;;
        -d|--dir)       INSTALL_DIR="$2"; shift 2 ;;
        -m|--mirror)    MIRROR="$2"; shift 2 ;;
        -f|--file)      LOCAL_FILE="$2"; shift 2 ;;
        --panel)        PANEL_ADDR="$2"; shift 2 ;;
        -p|--password)  ADMIN_PASSWORD="$2"; shift 2 ;;
        -u|--uninstall) UNINSTALL=true; shift ;;
        -y|--yes)       ASSUME_YES=true; shift ;;
        -h|--help)      usage; exit 0 ;;
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

# ---------- 交互式选项 ----------
INTERACTIVE=true
if [ "$ASSUME_YES" = true ] || [ -n "${MTUN_NONINTERACTIVE:-}" ] || [ ! -t 0 ]; then
    INTERACTIVE=false
fi

trim() {
    local s="$1"
    s="${s#"${s%%[![:space:]]*}"}"
    s="${s%"${s##*[![:space:]]}"}"
    printf '%s' "$s"
}

# ask_value <变量名> <标签> <默认值>：回车保留默认值
ask_value() {
    local var="$1" label="$2" def="$3" input="" hint
    if [ "$INTERACTIVE" != true ]; then
        printf -v "$var" '%s' "$def"
        return 0
    fi
    if [ -n "$def" ]; then hint="$def"; else hint="留空=默认"; fi
    printf '  %s [%s]: ' "$label" "$hint"
    IFS= read -r input || input=""
    input="$(trim "$input")"
    if [ -z "$input" ]; then
        printf -v "$var" '%s' "$def"
    else
        printf -v "$var" '%s' "$input"
    fi
}

# ask_secret <变量名> <标签> <默认说明>：输入不回显，回车保留当前值（为空则自动生成）
ask_secret() {
    local var="$1" label="$2" hint="$3" input="" current
    current="${!var}"
    if [ "$INTERACTIVE" != true ]; then
        return 0
    fi
    if [ -n "$current" ]; then hint="已设置，回车保持不变"; fi
    printf '  %s [%s]: ' "$label" "$hint"
    IFS= read -rs input || input=""
    printf '\n'
    if [ -n "$input" ]; then
        printf -v "$var" '%s' "$input"
    fi
}

prompt_options() {
    if [ "$INTERACTIVE" != true ]; then
        info "未检测到交互终端或已指定 -y，使用当前参数与默认值"
        return 0
    fi
    printf '\n%s请填写安装选项（直接按回车使用方括号内的默认值）%s\n' "$C_B" "$C_R"
    ask_value INSTALL_DIR "安装目录" "$INSTALL_DIR"
    ask_value SERVER_ADDR "服务端地址（如 1.2.3.4:10000）" "$SERVER_ADDR"
    ask_secret JOIN_KEY "接入密码（必填）" "必填"
    ask_value NODE_NAME "节点名称（留空=主机名）" "$NODE_NAME"
    ask_value PANEL_ADDR "面板监听地址" "$PANEL_ADDR"
    ask_secret ADMIN_PASSWORD "面板登录密码（留空自动生成）" "自动生成"
    ask_value VERSION "安装版本（如 v0.5.1）" "$VERSION"
    ask_value MIRROR "GitHub 加速前缀（如 https://ghfast.top/）" "$MIRROR"
    printf '\n'
}

# ---------- 主流程 ----------
print_banner
if [ "$UNINSTALL" != true ]; then
    prompt_options
fi
print_options
warn "已禁用 Ctrl+C：安装过程不会因误按而中断（确需中止请关闭终端）"

# ---------- 参数预检（-u 卸载不需要地址与密码）----------
if [ "$UNINSTALL" != true ]; then
    if [ -z "$SERVER_ADDR" ]; then
        fail "缺少服务端地址"
        detail "用法: sudo bash install-client.sh -s 203.0.113.10:10000 -k '接入密码'"
        exit 1
    fi
    if [ -z "$JOIN_KEY" ]; then
        fail "缺少接入密码"
        detail "接入密码由服务端管理员提供；多节点场景建议在服务端「加密」页为每个节点单独生成"
        exit 1
    fi
fi

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
    step "卸载 MTUN 客户端"
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

# ---------- 参数校验 ----------
step "检查安装参数"
ok "服务端: $SERVER_ADDR"
ok "接入密码: 已提供（${#JOIN_KEY} 位，不在终端回显）"
ok "节点名称: ${NODE_NAME:-$(hostname 2>/dev/null || echo 未指定)}"

# ---------- 架构 ----------
info "系统: $(uname -s) $(uname -r)"
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)  BIN_ARCH="amd64" ;;
    aarch64|arm64) BIN_ARCH="arm64" ;;
    *) fail "不支持的架构: $ARCH"; exit 1 ;;
esac
ASSET="mtun-linux-${BIN_ARCH}"
ok "架构 $ARCH → $ASSET"
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
    detail "请用 -v 指定版本，或使用 -f 离线安装"
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
        detail "国内网络可加镜像: -m https://ghfast.top/"
        detail "或本机下载后 scp 上传，再用 -f 离线安装"
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
if systemctl is-active --quiet "$SYSTEMD_SERVICE" 2>/dev/null; then
    info "停止正在运行的旧实例"
    $SUDO systemctl stop "$SYSTEMD_SERVICE"
fi
info "目标: $BIN"
$SUDO mv "$TMP" "$BIN"
$SUDO chmod +x "$BIN"
"$BIN" version
ok "二进制就绪"

# ---------- 配置 ----------
step "写入服务端地址与接入密码"
CFG="$INSTALL_DIR/configs/client.yaml"
$SUDO mkdir -p "$INSTALL_DIR/configs"
if [ ! -f "$CFG" ]; then
    info "生成默认配置: $CFG"
    TMP_CFG="$(new_tmp)"
    if ! "$BIN" default client > "$TMP_CFG" 2>/dev/null || [ ! -s "$TMP_CFG" ]; then
        cat > "$TMP_CFG" <<EOF
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
    $SUDO cp "$TMP_CFG" "$CFG"
fi

# 通过 mtun connect 写入服务端地址与接入密码（内部保存哈希，不落明文）
if ! OUT="$("$BIN" connect -c "$CFG" -server "$SERVER_ADDR" -key "$JOIN_KEY" 2>&1)"; then
    fail "写入服务端地址或接入密码失败"
    printf '%s\n' "$OUT" | sed 's/^/        /'
    exit 1
fi
ok "服务端地址已写入"
ok "接入密码已保存为哈希: $CFG.join"

apply_scalar() {
    local key="$1" value="$2"
    if grep -qE "^${key}:" "$CFG"; then
        $SUDO sed -i "s|^${key}:.*|${key}: \"${value}\"|" "$CFG"
    else
        printf '%s: "%s"\n' "$key" "$value" | $SUDO tee -a "$CFG" >/dev/null
    fi
}

# 客户端专用更新仓库（与服务端分仓发布）
apply_scalar update_repo "$REPO"
if [ -n "$NODE_NAME" ]; then
    info "节点名称 → $NODE_NAME"
    apply_scalar node "$NODE_NAME"
fi
if [ "$PANEL_ADDR" != "127.0.0.1:10005" ]; then
    info "面板监听地址 → $PANEL_ADDR"
    apply_scalar dashboard "$PANEL_ADDR"
fi

$SUDO chmod 600 "$CFG.join" 2>/dev/null || true

if ! "$BIN" check -c "$CFG" -role client >/dev/null 2>&1; then
    fail "配置文件校验失败"
    "$BIN" check -c "$CFG" -role client >&2 || true
    detail "请手动修正 $CFG 后重试"
    exit 1
fi
ok "配置校验通过"

# ---------- 面板密码 ----------
step "配置面板登录密码"
if [ -f "$CFG.passwd" ]; then
    info "面板密码已存在，跳过: $CFG.passwd"
    ADMIN_DISPLAY=""
else
    if [ -n "$ADMIN_PASSWORD" ]; then
        if [ "${#ADMIN_PASSWORD}" -lt 8 ]; then
            fail "面板密码至少 8 位"
            exit 1
        fi
        info "使用交互输入的密码"
        if ! "$BIN" passwd -c "$CFG" -password "$ADMIN_PASSWORD" >/dev/null; then
            fail "写入面板密码失败"
            exit 1
        fi
        ADMIN_DISPLAY="$ADMIN_PASSWORD"
    else
        ADMIN_PASSWORD="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
        if ! "$BIN" passwd -c "$CFG" -password "$ADMIN_PASSWORD" >/dev/null; then
            fail "写入面板密码失败"
            exit 1
        fi
        info "未填写，已自动生成面板密码"
        ADMIN_DISPLAY="$ADMIN_PASSWORD"
    fi
    ok "面板密码已保存: $CFG.passwd"
fi

# ---------- systemd ----------
step "注册 systemd 服务"
UNIT="/etc/systemd/system/${SYSTEMD_SERVICE}.service"
info "写入 $UNIT"
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

# ---------- 连接确认 ----------
info "读取客户端连接状态"
sleep 2
if STATUS="$("$BIN" status -c "$CFG" 2>/dev/null)"; then
    printf '%s\n' "$STATUS" | sed 's/^/        /'
else
    warn "暂时无法读取状态，可稍后执行: $BIN status -c $CFG"
fi

# ---------- 汇总 ----------
printf '\n%s================================================================%s\n' "$C_C" "$C_R"
printf '  %s安装完成%s\n' "$C_B" "$C_G"
printf '%s================================================================%s\n' "$C_C" "$C_R"
printf '  安装目录:     %s\n' "$INSTALL_DIR"
printf '  配置文件:     %s\n' "$CFG"
printf '  服务端:       %s\n' "$SERVER_ADDR"
printf '  节点名称:     %s\n' "${NODE_NAME:-$(hostname 2>/dev/null || echo 未指定)}"
printf '  面板地址:     http://%s\n' "$PANEL_ADDR"
printf '  面板密码:     %s\n' "${ADMIN_DISPLAY:-（沿用原有密码）}"
printf '  接入密码:     已保存为哈希，无法回显\n'
printf '%s----------------------------------------------------------------%s\n' "$C_C" "$C_R"
printf '  查看状态:     %s status -c %s\n' "$BIN" "$CFG"
printf '  查看日志:     journalctl -u %s -f\n' "$SYSTEMD_SERVICE"
printf '  重启服务:     systemctl restart %s\n' "$SYSTEMD_SERVICE"
printf '  添加规则:     %s rule add -name web -listen 10101 -target 127.0.0.1:80 -c %s\n' "$BIN" "$CFG"
printf '  在线更新:     %s update -c %s\n' "$BIN" "$CFG"
printf '  卸载:         sudo bash install-client.sh -u\n'
printf '%s================================================================%s\n' "$C_C" "$C_R"
printf '\n  %s注意%s\n' "$C_B" "$C_R"
printf '  1) 转发端口必须落在服务端 publish.port_range（默认 10100-10999）内\n'
printf '  2) 服务端关闭「允许默认密码接入」后，本节点必须使用独立接入密码\n'
if [ "$PANEL_ADDR" != "127.0.0.1:10005" ]; then
    printf '  3) 面板已允许公网访问，请确保防火墙限制来源并使用强密码\n'
fi
printf '\n'
