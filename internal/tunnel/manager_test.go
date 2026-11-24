// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package tunnel

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func newTestManager() *Manager {
	return NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), "10100-10999")
}

func TestBanState(t *testing.T) {
	m := newTestManager()
	if _, _, banned := m.BanState("n"); banned {
		t.Fatal("初始不应封禁")
	}
	until := time.Now().Add(time.Hour)
	m.SetBan("n", until, false)
	if !m.Banned("n") {
		t.Fatal("应处于封禁期")
	}
	gotUntil, permanent, banned := m.BanState("n")
	if !banned || permanent || gotUntil.Unix() != until.Unix() {
		t.Fatalf("BanState 不符: %v %v %v", gotUntil, permanent, banned)
	}
	m.SetBan("n", time.Time{}, true)
	if _, permanent, banned := m.BanState("n"); !banned || !permanent {
		t.Fatal("应为永久封禁")
	}
	m.Unban("n")
	if m.Banned("n") {
		t.Fatal("Unban 后不应封禁")
	}
	m.SetBan("old", time.Now().Add(-time.Second), false)
	if m.Banned("old") {
		t.Fatal("过期封禁应自动失效")
	}
}

func TestApplyRulesOffline(t *testing.T) {
	m := newTestManager()
	if _, err := m.ApplyRules("missing", nil); err == nil {
		t.Fatal("离线节点应返回错误")
	}
	if specs := m.RuleSpecs("missing"); specs != nil {
		t.Fatal("离线节点不应有规则")
	}
}
