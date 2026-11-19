// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package autostart

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestNewRoleAndAbsolutePath(t *testing.T) {
	for _, role := range []string{"server", "client"} {
		manager, err := New(role, filepath.Join("configs", role+".yaml"))
		if err != nil {
			t.Fatalf("New(%q) returned error: %v", role, err)
		}
		if manager.role != role {
			t.Fatalf("role = %q, want %q", manager.role, role)
		}
		if !filepath.IsAbs(manager.cfgPath) {
			t.Fatalf("config path is not absolute: %q", manager.cfgPath)
		}
	}
	for _, role := range []string{"", "Server", "service", " clientx"} {
		if _, err := New(role, "config.yaml"); err == nil {
			t.Fatalf("New(%q) unexpectedly succeeded", role)
		}
	}
	if _, err := New("server", ""); err == nil {
		t.Fatal("New with an empty config path unexpectedly succeeded")
	}
}

func TestStatusJSONFields(t *testing.T) {
	data, err := json.Marshal(Status{
		Supported: true,
		Enabled:   true,
		Mode:      "scheduled_task",
		Trigger:   "AtStartup",
		Conflict:  true,
		Message:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"supported":true,"enabled":true,"mode":"scheduled_task","trigger":"AtStartup","conflict":true,"message":"test"}`
	if string(data) != want {
		t.Fatalf("JSON = %s, want %s", data, want)
	}
}
