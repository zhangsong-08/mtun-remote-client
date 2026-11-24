// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build linux

package autostart

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"mtun/internal/platform"
)

// systemctl is-enabled 的返回值含义。
var enabledStates = map[string]bool{
	"enabled":           true,
	"enabled-runtime":   true,
	"alias":             true,
	"static":            true, // 静态 unit 也允许自启
	"generated":         true,
	"indirect":          true,
	"disabled":          false,
	"masked":            false,
	"masked-runtime":    false,
	"bad":               false,
	"not-found":         false,
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

	stdout, stderr, err := systemctl(ctx, "is-enabled", unit)
	if err != nil {
		return Status{}, fmt.Errorf("systemctl is-enabled: %w: %s", classifySystemctlError(stderr), stderr)
	}
	enabled := strings.TrimSpace(stdout)
	isEnabled, ok := enabledStates[enabled]
	if !ok {
		isEnabled = false
	}

	stdout, _, _ = systemctl(ctx, "is-active", unit)
	active := strings.TrimSpace(stdout)

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
		// enable --now：设置开机自启 + 立即启动（若当前未运行）。
		stdout, stderr, err := systemctl(ctx, "enable", "--now", unit)
		if err != nil {
			return Status{}, fmt.Errorf("systemctl enable --now: %w: %s", classifySystemctlError(stderr), stdout+stderr)
		}
	} else {
		// disable 只禁用开机自启，不强制停服务。
		// 同时尝试 stop，stop 失败不认为整体失败（进程本来就在退出）。
		_, _, _ = systemctl(ctx, "stop", unit)
		stdout, stderr, err := systemctl(ctx, "disable", unit)
		if err != nil {
			return Status{}, fmt.Errorf("systemctl disable: %w: %s", classifySystemctlError(stderr), stdout+stderr)
		}
	}
	return m.Status(ctx)
}

func (m *Manager) Remove(ctx context.Context) error {
	if !platform.IsSystemdManaged() {
		return nil // 非 systemd 环境，静默成功
	}
	unit := platform.ServiceUnit()
	_, stderr, err := systemctl(ctx, "disable", "--now", unit)
	if err != nil {
		return fmt.Errorf("systemctl disable --now: %w: %s", classifySystemctlError(stderr), stderr)
	}
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	unit := platform.ServiceUnit()
	_, stderr, err := systemctl(ctx, "start", unit)
	if err != nil {
		return fmt.Errorf("systemctl start: %w: %s", classifySystemctlError(stderr), stderr)
	}
	return nil
}

// systemctl 执行 systemctl 子命令，返回 stdout、stderr 和执行错误。
func systemctl(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// classifySystemctlError 根据 systemctl stderr 归类为具体错误类型。
func classifySystemctlError(stderr string) error {
	lower := strings.ToLower(strings.TrimSpace(stderr))
	switch {
	case strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "must be root") ||
		strings.Contains(lower, "authorization required"):
		return fmt.Errorf("%w（需要 root 权限执行 systemctl）", ErrPermission)
	case strings.Contains(lower, "already running") ||
		strings.Contains(lower, "already enabled"):
		return nil // 幂等成功
	default:
		return nil // 保留原始 err 给上层
	}
}
