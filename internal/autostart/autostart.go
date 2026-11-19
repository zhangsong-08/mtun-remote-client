// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package autostart

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var (
	ErrPermission  = errors.New("autostart permission denied")
	ErrConflict    = errors.New("autostart conflict")
	ErrUnsupported = errors.New("autostart unsupported")
)

type Status struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode"`
	Trigger   string `json:"trigger"`
	Conflict  bool   `json:"conflict"`
	Message   string `json:"message"`
}

type Manager struct {
	role    string
	cfgPath string
}

func New(role, cfgPath string) (*Manager, error) {
	role = strings.TrimSpace(role)
	if role != "server" && role != "client" {
		return nil, fmt.Errorf("invalid autostart role %q: must be server or client", role)
	}
	if cfgPath == "" {
		return nil, errors.New("autostart config path is required")
	}
	absPath, err := filepath.Abs(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("resolve autostart config path: %w", err)
	}
	return &Manager{role: role, cfgPath: absPath}, nil
}
