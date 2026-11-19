// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build !windows

package autostart

import (
	"context"
	"fmt"
)

func (m *Manager) Status(context.Context) (Status, error) {
	return Status{Supported: false, Mode: "none", Message: "当前平台不支持自动启动管理"}, nil
}

func (m *Manager) Set(context.Context, bool) (Status, error) {
	return Status{Supported: false, Mode: "none"}, fmt.Errorf("%w on this platform", ErrUnsupported)
}

func (m *Manager) Remove(context.Context) error {
	return fmt.Errorf("%w on this platform", ErrUnsupported)
}

func (m *Manager) Start(context.Context) error {
	return fmt.Errorf("%w on this platform", ErrUnsupported)
}
