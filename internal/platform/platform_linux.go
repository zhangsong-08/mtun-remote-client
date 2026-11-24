// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// DetachProcAttr 在 Linux 下通过独立会话脱离父进程；
// 忽略 breakaway 参数（该概念仅 Windows 作业对象需要）。
func DetachProcAttr(bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// ProcessAlive 判断进程是否仍在运行。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func ConfigDir() string { return "/etc/mtun" }

func DataDir() string { return "/var/lib/mtun" }

// IsSystemdManaged 判断当前进程是否由 systemd 管理。
// systemd 会在进程启动时注入非空的 INVOCATION_ID 环境变量，
// 这是最可靠的信号（不依赖 /proc/cgroup 的具体格式）。
func IsSystemdManaged() bool {
	id := strings.TrimSpace(os.Getenv("INVOCATION_ID"))
	return id != ""
}

// ServiceUnit 返回当前进程对应的 systemd unit 名（不带 .service 后缀）。
// 探测优先级：
//  1. MTUN_SYSTEMD_UNIT 环境变量（显式指定，用户自定义 unit 名时可用）
//  2. systemctl show --property=Unit --value $PPID / $PID（自动探测最可靠）
//  3. 读取 /proc/<pid>/cgroup 回退解析
//  4. 最后回退到默认值 "mtun"
//
// 用户完全不用配置环境变量——只要进程由 systemd 启动，就能自动识别。
func ServiceUnit() string {
	// 显式环境变量优先（向后兼容）。
	if v := strings.TrimSpace(os.Getenv("MTUN_SYSTEMD_UNIT")); v != "" {
		return v
	}

	// 方式 1：systemctl show 直接查 unit。
	if unit := detectUnitViaSystemctl(); unit != "" {
		return unit
	}

	// 方式 2：解析 /proc/self/cgroup，回退探测。
	if unit := detectUnitViaCgroup(); unit != "" {
		return unit
	}

	return "mtun"
}

// detectUnitViaSystemctl 用 "systemctl show --property=Unit --value <pid>" 获取当前进程所属 unit。
func detectUnitViaSystemctl() string {
	pid := os.Getpid()
	cmd := exec.Command("systemctl", "show", "--property=Unit", "--value", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	unit := strings.TrimSpace(string(out))
	if unit == "" || unit == "not-found" || strings.HasPrefix(unit, "@") {
		return ""
	}
	return unitName(unit)
}

// detectUnitViaCgroup 解析 /proc/self/cgroup 里的 systemd cgroup。
func detectUnitViaCgroup() string {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		// v2: 0::/system.slice/mtun.service/...
		// v1: 1:name=systemd:/system.slice/mtun.service/...
		fields := strings.SplitN(line, ":", 3)
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		if idx := strings.Index(path, ".service"); idx >= 0 {
			// 取 .service 之前的部分
			start := strings.LastIndex(path[:idx], "/")
			if start < 0 {
				start = -1
			}
			raw := path[start+1 : idx]
			if raw != "" && !strings.HasPrefix(raw, "@") {
				return raw
			}
		}
	}
	return ""
}

// unitName 去掉 unit 的 .service 后缀，保持一致。
func unitName(unit string) string {
	unit = strings.TrimSpace(unit)
	unit = strings.TrimSuffix(unit, ".service")
	return unit
}

// RenderServiceFile 生成一个完整的 systemd unit 文件内容。
// unit：unit 名（不含 .service）。exe：mtun 二进制绝对路径。
// role：server 或 client。cfg：配置文件绝对路径。
//
// 生成的 service 具备：Restart=always、自动拉起、网络就绪后启动、
// 低权限运行（NoNewPrivileges=true）等安全加固。
func RenderServiceFile(unit, exe, role, cfg string) string {
	var sb strings.Builder
	sb.WriteString("[Unit]\n")
	sb.WriteString(fmt.Sprintf("Description=mtun %s (%s)\n", role, unit))
	sb.WriteString("After=network-online.target\n")
	sb.WriteString("Wants=network-online.target\n")
	sb.WriteString("\n")
	sb.WriteString("[Service]\n")
	sb.WriteString(fmt.Sprintf("ExecStart=%s %s -c %s\n", exe, role, cfg))
	sb.WriteString("Restart=always\n")
	sb.WriteString("RestartSec=3\n")
	sb.WriteString("AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE\n")
	sb.WriteString("NoNewPrivileges=true\n")
	sb.WriteString("ProtectSystem=full\n")
	sb.WriteString("ProtectHome=true\n")
	sb.WriteString("PrivateDevices=true\n")
	sb.WriteString("ProtectKernelTunables=true\n")
	sb.WriteString("ProtectKernelModules=true\n")
	sb.WriteString("ProtectControlGroups=true\n")
	sb.WriteString("MemoryMax=512M\n")
	sb.WriteString("\n")
	sb.WriteString("[Install]\n")
	sb.WriteString("WantedBy=multi-user.target\n")
	return sb.String()
}

func ServiceHint() string {
	return `Linux systemd:
  sudo install -m 0755 mtun /usr/local/bin/mtun
  sudo mkdir -p /etc/mtun /var/lib/mtun
  # 将 server.yaml / client.yaml 放入 /etc/mtun
  # 写入 /etc/systemd/system/mtun.service:
[Unit]
Description=mtun
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/mtun server -c /etc/mtun/server.yaml
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
NoNewPrivileges=true
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target

  sudo systemctl daemon-reload
  sudo systemctl enable --now mtun

说明:
  - Restart=always 保证进程异常退出后自动拉起
  - 在线更新时自动调用 systemctl restart，用已替换的新二进制
  - 进程启动与重启时自动检测当前 systemd unit（零配置）`
}
