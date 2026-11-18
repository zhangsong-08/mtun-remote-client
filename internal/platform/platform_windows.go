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

func ServiceHint() string {
	exe := filepath.Join(ConfigDir(), "mtun.exe")
	cfg := filepath.Join(ConfigDir(), "server.yaml")
	return fmt.Sprintf(`Windows 服务（管理员 PowerShell）:
  sc.exe create mtun binPath= "%s server -c %s" start= auto
  sc.exe start mtun
  sc.exe delete mtun

说明: 当前骨架未内置 SCM 集成，正式版用 golang.org/x/sys/windows/svc 实现。
配置目录: %s`, exe, cfg, ConfigDir())
}
