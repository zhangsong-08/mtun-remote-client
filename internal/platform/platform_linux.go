// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build linux

package platform

import "syscall"

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
  sudo systemctl enable --now mtun`
}
