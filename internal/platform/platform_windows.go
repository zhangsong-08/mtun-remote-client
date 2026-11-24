// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Windows 进程创建标志
const (
	createBreakawayFromJob = 0x01000000
	detachedProcess        = 0x00000008
)

func ConfigDir() string {
	return filepath.Join(os.Getenv("ProgramData"), "mtun")
}

// DetachProcAttr 让子进程脱离父进程所在的控制台与作业对象（Job），
// 否则父进程退出时整个作业树会被系统清理，重启助手来不及拉起新版本。
// breakaway 为 false 时只脱离控制台，用于作业对象不允许 breakaway 的降级场景。
func DetachProcAttr(breakaway bool) *syscall.SysProcAttr {
	flags := uint32(detachedProcess)
	if breakaway {
		flags |= createBreakawayFromJob
	}
	return &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
}

// ProcessAlive 判断进程是否仍在运行（用于重启助手等待旧进程退出）。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const synchronize = 0x00100000
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	const waitTimeout = 0x00000102 // WAIT_TIMEOUT
	event, err := syscall.WaitForSingleObject(h, 0)
	if err != nil {
		return false
	}
	return event == waitTimeout
}

func DataDir() string {
	return ConfigDir()
}

// IsSystemdManaged 在 Windows 上恒为 false。
func IsSystemdManaged() bool { return false }

// ServiceUnit 在 Windows 上返回空字符串。
func ServiceUnit() string { return "" }

func ServiceHint() string {
	return fmt.Sprintf(`Windows 自动启动:
  推荐使用安装脚本（管理员 PowerShell）:
    powershell.exe -ExecutionPolicy Bypass -File scripts\install.ps1
  服务端和客户端也可在各自管理面板中开关自动启动。
  没有 NSSM 服务时使用计划任务：管理员为 SYSTEM/开机启动，普通用户为当前用户/登录启动。

配置目录: %s`, ConfigDir())
}
