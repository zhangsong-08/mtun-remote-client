// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build linux

package autostart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"mtun/internal/platform"
)

// systemctl exit code 含义（man systemctl）。
const (
	systemctlOK             = 0  // 成功
	systemctlGenericError   = 1  // 通用错误
	systemctlInvalidArgs    = 2  // 无效参数
	systemctlNotActive      = 3  // unit 未激活（is-active 正常返回）
	systemctlNotFound       = 4  // unit 未找到（is-enabled/is-active 正常返回）
	systemctlBadSetting     = 5  // 配置错误
)

// enabledStates 把 systemctl is-enabled 的 stdout 映射为"是否应视为已启用开机自启"。
var enabledStates = map[string]bool{
	"enabled":          true,
	"enabled-runtime":  true,
	"alias":            true,
	"static":           true, // 静态 unit 会被 Wants/Requires 引入，也算可自启
	"generated":        true,
	"indirect":         true,
	"disabled":         false,
	"masked":           false,
	"masked-runtime":   false,
	"bad":              false,
	"not-found":        false, // exit code 4，unit 不存在
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	if !platform.IsSystemdManaged() {
		return Status{
			Supported: true,
			Mode:      "none",
			Message:   "未由 systemd 管理，请使用 systemd 启动 mtun（详见 mtun service）",
		}, nil
	}
	unit := platform.ServiceUnit()

	stdout, stderr, code, err := systemctl(ctx, "is-enabled", unit)
	if err != nil {
		return Status{}, fmt.Errorf("执行 systemctl 失败: %w: %s", err, strings.TrimSpace(stderr))
	}
	// exit code 4 (not-found) / 1 (generic error) 这里不视为 fatal ——
	// is-enabled 对不存在的 unit 会返回 exit 4 + stdout "not-found"。
	// 但我们需要 stderr 里没有 permission denied 才继续。
	if strings.Contains(strings.ToLower(stderr), "permission denied") ||
		strings.Contains(strings.ToLower(stderr), "must be root") {
		return Status{}, fmt.Errorf("%w: 执行 systemctl is-enabled 需要 root 权限", ErrPermission)
	}

	enabled := strings.TrimSpace(stdout)
	isEnabled, ok := enabledStates[enabled]
	if !ok {
		// exit code 2 (invalid args) 等未知返回 — 保守判断为未启用
		if code == systemctlNotFound {
			isEnabled = false
		} else {
			isEnabled = false
		}
	}

	stdout2, _, _, _ := systemctl(ctx, "is-active", unit)
	active := strings.TrimSpace(stdout2)

	msg := "systemd 未启用"
	if isEnabled {
		if active == "active" {
			msg = "systemd 已启用且运行中"
		} else {
			msg = fmt.Sprintf("systemd 已启用（当前状态：%s）", active)
		}
	} else {
		msg = fmt.Sprintf("systemd 未启用（状态：%s）", enabled)
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
	if !platform.IsSystemdManaged() {
		if enabled {
			return Status{}, fmt.Errorf("%w: 未由 systemd 管理", ErrPermission)
		}
		return m.Status(ctx)
	}
	unit := platform.ServiceUnit()

	if enabled {
		stdout, stderr, code, err := systemctl(ctx, "enable", "--now", unit)
		if err != nil {
			return Status{}, wrapSystemctlError(stderr, err)
		}
		if code != systemctlOK {
			return Status{}, fmt.Errorf("systemctl enable --now 失败 (exit %d): %s\n%s",
				code, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
		}
	} else {
		// disable 只禁用开机自启，不强制停服务。
		// 同时尝试 stop，stop 失败不认为整体失败（进程本来就在退出）。
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
	if !platform.IsSystemdManaged() {
		return nil // 非 systemd 环境，静默成功
	}
	unit := platform.ServiceUnit()
	_, stderr, _, err := systemctl(ctx, "disable", "--now", unit)
	if err != nil {
		return wrapSystemctlError(stderr, err)
	}
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	unit := platform.ServiceUnit()
	_, stderr, _, err := systemctl(ctx, "start", unit)
	if err != nil {
		return wrapSystemctlError(stderr, err)
	}
	return nil
}

// systemctl 执行 systemctl 子命令，返回 stdout、stderr、exit code 和 exec error。
// exec error 表示命令本身未能运行（systemctl 不存在、context 超时等），
// exit code 非 0 是 systemctl 的业务错误，两者要区分处理。
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

// wrapSystemctlError 把 systemctl 的 stderr 归类为具体错误类型，
// 然后连同原始 exec 错误一起返回。
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
		return execErr
	}
}
