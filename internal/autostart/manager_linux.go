// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build linux

package autostart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mtun/internal/platform"
)

// systemctl exit code 含义（man systemctl）。
const (
	systemctlOK       = 0
	systemctlNotFound = 4 // unit 未找到
)

// enabledStates 把 systemctl is-enabled 的 stdout 映射为"是否应视为已启用开机自启"。
var enabledStates = map[string]bool{
	"enabled":          true,
	"enabled-runtime":  true,
	"alias":            true,
	"static":           true,
	"generated":        true,
	"indirect":         true,
	"disabled":         false,
	"masked":           false,
	"masked-runtime":   false,
	"bad":              false,
	"not-found":        false,
}

// unitPath systemd unit 文件的标准位置。
func unitPath(unit string) string {
	return filepath.Join("/etc", "systemd", "system", unit+".service")
}

// unitExists 检查 unit 的 .service 文件是否存在。
func unitExists(unit string) bool {
	_, err := os.Stat(unitPath(unit))
	return err == nil
}

// detectUnitName 决定要用哪个 systemd unit 名。
// 优先使用 ServiceUnit() 的自动探测；如果探测失败（手动运行），
// 回退到默认值。role 只是给日志用。
func detectUnitName(_ string) string {
	if u := platform.ServiceUnit(); u != "" {
		return u
	}
	return "mtun"
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	unit := detectUnitName(m.role)
	unitFile := unitPath(unit)

	// 先看 unit 文件是否存在——这是自启动能否登记的前提。
	if !unitExists(unit) {
		return Status{
			Supported: true,
			Mode:      "systemd",
			Enabled:   false,
			Trigger:   "AtStartup",
			Message:   fmt.Sprintf("systemd unit %s.service 未创建（点启用自启动可自动生成）", unit),
		}, nil
	}

	// unit 文件存在 → 去查 is-enabled / is-active。
	stdout, stderr, code, err := systemctl(ctx, "is-enabled", unit)
	if err != nil {
		if strings.Contains(strings.ToLower(stderr), "permission denied") {
			return Status{}, fmt.Errorf("%w: 查询 systemctl is-enabled 需要 root 权限", ErrPermission)
		}
		return Status{}, fmt.Errorf("执行 systemctl 失败: %w: %s", err, strings.TrimSpace(stderr))
	}
	enabledStr := strings.TrimSpace(stdout)
	isEnabled, ok := enabledStates[enabledStr]
	if !ok {
		isEnabled = code == systemctlOK
	}

	stdout2, _, _, _ := systemctl(ctx, "is-active", unit)
	active := strings.TrimSpace(stdout2)

	msg := fmt.Sprintf("systemd unit: %s", unitFile)
	if isEnabled {
		if active == "active" {
			msg += " · 已启用且运行中"
		} else {
			msg += fmt.Sprintf(" · 已启用（当前状态：%s）", active)
		}
	} else {
		msg += fmt.Sprintf(" · 未启用（状态：%s）", enabledStr)
	}

	return Status{
		Supported: true,
		Enabled:   isEnabled,
		Mode:      "systemd",
		Trigger:   "AtStartup",
		Message:   msg,
	}, nil
}

func (m *Manager) Set(ctx context.Context, enabled bool) (Status, error) {
	unit := detectUnitName(m.role)

	if enabled {
		// Step 1: unit 文件不存在 → 自动创建。
		if !unitExists(unit) {
			if err := createUnitFile(unit, m.role, m.cfgPath); err != nil {
				return Status{}, err
			}
			if _, _, _, err := systemctl(ctx, "daemon-reload"); err != nil {
				return Status{}, wrapSystemctlError("", err)
			}
		}

		// Step 2: enable --now。
		stdout, stderr, code, err := systemctl(ctx, "enable", "--now", unit)
		if err != nil {
			return Status{}, wrapSystemctlError(stderr, err)
		}
		if code != systemctlOK {
			return Status{}, fmt.Errorf("systemctl enable --now 失败 (exit %d): %s\n%s",
				code, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
		}
	} else {
		// 禁用 → disable + stop。
		_, _, _, _ = systemctl(ctx, "stop", unit)
		stdout, stderr, code, err := systemctl(ctx, "disable", unit)
		if err != nil {
			return Status{}, wrapSystemctlError(stderr, err)
		}
		if code != systemctlOK {
			return Status{}, fmt.Errorf("systemctl disable 失败 (exit %d): %s\n%s",
				code, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
		}
	}
	return m.Status(ctx)
}

func (m *Manager) Remove(ctx context.Context) error {
	unit := detectUnitName(m.role)

	// 先 disable --now。
	if _, _, _, err := systemctl(ctx, "disable", "--now", unit); err != nil {
		return wrapSystemctlError("", err)
	}

	// 再删除 unit 文件。
	path := unitPath(unit)
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			if os.IsPermission(err) {
				return fmt.Errorf("%w: 删除 %s 需要 root 权限: %v", ErrPermission, path, err)
			}
			return fmt.Errorf("删除 unit 文件失败: %w", err)
		}
		if _, _, _, err := systemctl(ctx, "daemon-reload"); err != nil {
			return wrapSystemctlError("", err)
		}
	}
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	unit := detectUnitName(m.role)
	if !unitExists(unit) {
		return fmt.Errorf("systemd unit %s.service 不存在，请先启用自启动", unit)
	}
	_, stderr, _, err := systemctl(ctx, "start", unit)
	if err != nil {
		return wrapSystemctlError(stderr, err)
	}
	return nil
}

// createUnitFile 自动生成 /etc/systemd/system/<unit>.service。
// 需要 root 权限写入 /etc/systemd/system/。
func createUnitFile(unit, role, cfgPath string) error {
	// 确定二进制路径
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行文件路径失败: %w", err)
	}

	// 确定 config 参数：优先用 cfgPath，没有就从默认位置推。
	cfg := cfgPath
	if cfg == "" {
		cfg = platform.ConfigDir() + "/" + role + ".yaml"
	}

	content := platform.RenderServiceFile(unit, exe, role, cfg)
	path := unitPath(unit)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("%w: 无法写入 %s（请使用 sudo 运行，或手动执行下方命令）:\n  sudo mtun %s -c %s\n  sudo systemctl enable --now %s",
				ErrPermission, path, role, cfg, unit)
		}
		return fmt.Errorf("写入 unit 文件失败: %w", err)
	}
	return nil
}

// systemctl 执行 systemctl 子命令，返回 stdout、stderr、exit code 和 exec error。
func systemctl(ctx context.Context, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

// wrapSystemctlError 把 systemctl 的 stderr 归类为具体错误类型。
func wrapSystemctlError(stderr string, execErr error) error {
	lower := strings.ToLower(strings.TrimSpace(stderr))
	switch {
	case strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "must be root") ||
		strings.Contains(lower, "authorization required"):
		return fmt.Errorf("%w（需要 root 权限执行 systemctl）", ErrPermission)
	default:
		if strings.TrimSpace(stderr) != "" {
			return fmt.Errorf("%w: %s", execErr, strings.TrimSpace(stderr))
		}
		if execErr == nil {
			return nil
		}
		return execErr
	}
}
