// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build !linux && !windows

package platform

import (
	"os"
	"path/filepath"
	"syscall"
)

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mtun")
}

// DetachProcAttr 在非 Linux/Windows 平台不设置额外属性。
func DetachProcAttr(bool) *syscall.SysProcAttr { return nil }

// ProcessAlive 判断进程是否仍在运行。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func DataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "mtun")
}

func ServiceHint() string {
	return "当前平台请使用系统自带的进程管理方式运行 mtun。"
}
