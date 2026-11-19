// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build windows

package autostart

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

type commandResult struct {
	stdout string
	stderr string
}

var executeCommand = func(ctx context.Context, name string, args []string, env []string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandResult{stdout: stdout.String(), stderr: stderr.String()}, err
}

type inspection struct {
	ServiceExists    bool   `json:"service_exists"`
	ServiceStartMode string `json:"service_start_mode"`
	TaskExists       bool   `json:"task_exists"`
	TaskEnabled      bool   `json:"task_enabled"`
	TaskTrigger      string `json:"task_trigger"`
	StartupLink      bool   `json:"startup_link"`
}

func (m *Manager) taskName() string { return "mtun-" + m.role }

func (m *Manager) Status(ctx context.Context) (Status, error) {
	in, err := m.inspect(ctx)
	if err != nil {
		return Status{}, err
	}
	return statusFromInspection(in), nil
}

func (m *Manager) Set(ctx context.Context, enabled bool) (Status, error) {
	if !enabled {
		if err := m.mutate(ctx, disableScript); err != nil {
			return Status{}, err
		}
		return m.Status(ctx)
	}

	in, err := m.inspect(ctx)
	if err != nil {
		return Status{}, err
	}
	if in.ServiceExists {
		if err := m.mutate(ctx, enableServiceScript); err != nil {
			return Status{}, err
		}
		return m.Status(ctx)
	}

	env, wrapper, err := m.taskEnvironment()
	if err != nil {
		return Status{}, err
	}
	data := wrapperScript(m.role, m.cfgPath, env.exe, env.root)
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		return Status{}, normalizeFileError(err)
	}
	if err := os.MkdirAll(filepath.Join(env.root, "logs"), 0o755); err != nil {
		return Status{}, normalizeFileError(err)
	}
	if err := os.WriteFile(wrapper, data, 0o644); err != nil {
		return Status{}, normalizeFileError(err)
	}
	if err := m.mutateWithEnv(ctx, enableTaskScript, env.values); err != nil {
		return Status{}, err
	}
	return m.Status(ctx)
}

func (m *Manager) Remove(ctx context.Context) error {
	return m.mutate(ctx, removeScript)
}

func (m *Manager) Start(ctx context.Context) error {
	in, err := m.inspect(ctx)
	if err != nil {
		return err
	}
	if inspectionConflict(in) {
		return fmt.Errorf("%w: multiple automatic startup entries exist; disable and enable again", ErrConflict)
	}
	if in.ServiceExists {
		if strings.EqualFold(in.ServiceStartMode, "Disabled") {
			return errors.New("automatic startup service is disabled")
		}
		return m.mutate(ctx, startServiceScript)
	}
	if in.TaskExists {
		if !in.TaskEnabled {
			return errors.New("automatic startup task is disabled")
		}
		return m.mutate(ctx, startTaskScript)
	}
	return errors.New("no automatic startup service or scheduled task exists")
}

type taskEnv struct {
	exe    string
	root   string
	values []string
}

func (m *Manager) baseEnvironment() []string {
	return []string{
		"MTUN_AUTOSTART_NAME=" + m.taskName(),
		"MTUN_AUTOSTART_ROLE=" + m.role,
		"MTUN_AUTOSTART_CONFIG=" + m.cfgPath,
	}
}

func (m *Manager) taskEnvironment() (taskEnv, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return taskEnv{}, "", fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return taskEnv{}, "", fmt.Errorf("resolve executable: %w", err)
	}
	root := installRoot(m.cfgPath)
	wrapper := filepath.Join(root, "mtun-"+m.role+"-autostart.ps1")
	env := taskEnv{exe: exe, root: root}
	env.values = append(m.baseEnvironment(),
		"MTUN_AUTOSTART_EXE="+exe,
		"MTUN_AUTOSTART_ROOT="+root,
		"MTUN_AUTOSTART_WRAPPER="+wrapper,
	)
	return env, wrapper, nil
}

func (m *Manager) inspect(ctx context.Context) (inspection, error) {
	var in inspection
	output, err := m.runPowerShell(ctx, statusScript, m.baseEnvironment())
	if err != nil {
		return in, err
	}
	if err := json.Unmarshal([]byte(output), &in); err != nil {
		return in, fmt.Errorf("parse autostart status JSON: %w", err)
	}
	return in, nil
}

func (m *Manager) mutate(ctx context.Context, script string) error {
	return m.mutateWithEnv(ctx, script, m.baseEnvironment())
}

func (m *Manager) mutateWithEnv(ctx context.Context, script string, env []string) error {
	_, err := m.runPowerShell(ctx, script, env)
	return err
}

func (m *Manager) runPowerShell(ctx context.Context, script string, env []string) (string, error) {
	args := []string{
		"-NoLogo", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePowerShellCommand(script),
	}
	result, err := executeCommand(ctx, "powershell.exe", args, env)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", classifyCommandError(result, err)
	}
	return strings.TrimSpace(result.stdout), nil
}

func encodePowerShellCommand(script string) string {
	encoded := utf16.Encode([]rune(script))
	data := make([]byte, len(encoded)*2)
	for i, value := range encoded {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func installRoot(cfgPath string) string {
	dir := filepath.Dir(cfgPath)
	if strings.EqualFold(filepath.Base(dir), "configs") {
		return filepath.Dir(dir)
	}
	return dir
}

func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func wrapperScript(role, cfgPath, exe, root string) []byte {
	logDir := filepath.Join(root, "logs")
	lines := []string{
		"Set-Location -LiteralPath " + quotePowerShell(root) + " -ErrorAction Stop",
		"New-Item -ItemType Directory -Force -Path " + quotePowerShell(logDir) + " -ErrorAction Stop | Out-Null",
		"[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)",
		"$ErrorActionPreference = 'Continue'",
		"& " + quotePowerShell(exe) + " " + quotePowerShell(role) + " '-c' " + quotePowerShell(cfgPath) +
			" 1>> " + quotePowerShell(filepath.Join(logDir, role+".log")) +
			" 2>> " + quotePowerShell(filepath.Join(logDir, role+".err")),
		"exit $LASTEXITCODE",
		"",
	}
	return append([]byte{0xef, 0xbb, 0xbf}, []byte(strings.Join(lines, "\r\n"))...)
}

func statusFromInspection(in inspection) Status {
	st := Status{Supported: true, Mode: "none", Message: "未配置自动启动"}
	switch {
	case in.ServiceExists:
		st.Mode = "nssm"
		st.Trigger = "AtStartup"
		st.Enabled = strings.EqualFold(in.ServiceStartMode, "Auto") || strings.EqualFold(in.ServiceStartMode, "Automatic")
		st.Message = "NSSM 服务已禁用"
		if st.Enabled {
			st.Message = "NSSM 服务已启用"
		}
	case in.TaskExists:
		st.Mode = "scheduled_task"
		st.Trigger = in.TaskTrigger
		st.Enabled = in.TaskEnabled
		st.Message = "计划任务已禁用"
		if st.Enabled {
			st.Message = "计划任务已启用"
		}
	case in.StartupLink:
		st.Mode = "startup_link"
		st.Trigger = "AtLogOn"
		st.Enabled = true
		st.Message = "检测到旧版启动项"
	}
	st.Conflict = inspectionConflict(in)
	if st.Conflict {
		st.Message = "检测到多个自动启动配置，请禁用后重新启用以清理冲突"
	}
	return st
}

func inspectionConflict(in inspection) bool {
	return (in.ServiceExists && (in.TaskEnabled || in.StartupLink)) ||
		(in.TaskExists && in.StartupLink)
}

func normalizeFileError(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w: %v", ErrPermission, err)
	}
	return err
}

func classifyCommandError(result commandResult, err error) error {
	detail := strings.TrimSpace(result.stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.stdout)
	}
	lower := strings.ToLower(detail)
	permissionHints := []string{
		"access is denied", "access denied", "unauthorizedaccess", "requires elevation",
		"administrator privileges", "0x80070005", "拒绝访问", "需要管理员", "权限不足",
	}
	for _, hint := range permissionHints {
		if strings.Contains(lower, strings.ToLower(hint)) {
			return fmt.Errorf("%w: %s", ErrPermission, detail)
		}
	}
	if strings.Contains(lower, "conflict") || strings.Contains(detail, "冲突") {
		return fmt.Errorf("%w: %s", ErrConflict, detail)
	}
	if detail != "" {
		return fmt.Errorf("autostart command: %w: %s", err, detail)
	}
	return fmt.Errorf("autostart command: %w", err)
}
