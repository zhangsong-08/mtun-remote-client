//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

func ConfigDir() string {
	return filepath.Join(os.Getenv("ProgramData"), "mtun")
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
