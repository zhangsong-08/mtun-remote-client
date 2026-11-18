//go:build !linux && !windows

package platform

import (
	"os"
	"path/filepath"
)

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mtun")
}

func DataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "mtun")
}

func ServiceHint() string {
	return "当前平台请使用系统自带的进程管理方式运行 mtun。"
}
