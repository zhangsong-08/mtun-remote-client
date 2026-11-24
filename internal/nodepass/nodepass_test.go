// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package nodepass

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtun/internal/secret"
)

func TestLoadEmptyAllowsDefault(t *testing.T) {
	s, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.AllowDefault() {
		t.Fatal("空存储应默认允许默认密码")
	}
	if len(s.List()) != 0 {
		t.Fatal("空存储不应包含节点密码")
	}
}

func TestSetRandomStoresOnlySeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.nodepass.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	pw, entry, err := s.SetRandom("node-a")
	if err != nil {
		t.Fatalf("SetRandom: %v", err)
	}
	if len(pw) < 8 {
		t.Fatalf("随机密码过短: %q", pw)
	}
	if entry.Seed == pw || strings.Contains(entry.Seed, pw) {
		t.Fatal("记录中不应保存明文密码")
	}
	if entry.Seed != secret.Seed(pw) {
		t.Fatal("记录应保存密码的本地哈希")
	}
	if !secret.IsSeed(entry.Seed) {
		t.Fatalf("seed 格式异常: %q", entry.Seed)
	}

	pwB, entryB, err := s.SetRandom("node-b")
	if err != nil {
		t.Fatalf("SetRandom b: %v", err)
	}
	if pw == pwB {
		t.Fatal("两次随机密码不应相同")
	}

	const def = "default-shared-pass"
	defSeed := secret.Seed(def)

	tests := []struct {
		name    string
		node    string
		used    string
		wantErr error
	}{
		{"节点密码正确", "node-a", secret.Seed(pw), nil},
		{"节点密码错误用默认", "node-a", defSeed, ErrPasswordMismatch},
		{"节点密码错误用别的节点", "node-a", entryB.Seed, ErrPasswordMismatch},
		{"无独立密码用默认", "node-c", defSeed, nil},
		{"无独立密码用节点密码", "node-c", entry.Seed, ErrPasswordMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Authorize(tc.node, tc.used, defSeed)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Authorize(%q) = %v, want %v", tc.node, err, tc.wantErr)
			}
		})
	}

	// 关闭默认密码后，无独立密码的节点无法接入。
	if err := s.SetAllowDefault(false); err != nil {
		t.Fatalf("SetAllowDefault: %v", err)
	}
	if err := s.Authorize("node-c", defSeed, defSeed); !errors.Is(err, ErrDefaultDisabled) {
		t.Fatalf("关闭默认密码后应拒绝: %v", err)
	}
	if err := s.Authorize("node-a", secret.Seed(pw), defSeed); err != nil {
		t.Fatalf("独立密码节点应仍可接入: %v", err)
	}

	// 落盘内容不得包含明文。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件: %v", err)
	}
	for _, plain := range []string{pw, pwB, def} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("文件中出现了明文密码 %q", plain)
		}
	}

	// 重新加载校验持久化。
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AllowDefault() {
		t.Fatal("allow_default=false 应被持久化")
	}
	got, ok := reloaded.Get("node-a")
	if !ok || got.Seed != entry.Seed {
		t.Fatalf("节点密码未正确持久化: %+v", got)
	}
}

func TestClearAndCandidateSeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.nodepass.json")
	s, _ := Load(path)
	const def = "default-shared-pass"
	defSeed := secret.Seed(def)

	entry, err := s.Set("node-x", "password-123456")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	seeds := s.CandidateSeeds(defSeed)
	if len(seeds) != 2 || seeds[0] != defSeed || seeds[1] != entry.Seed {
		t.Fatalf("候选哈希不符合预期: %v", seeds)
	}

	if err := s.SetAllowDefault(false); err != nil {
		t.Fatalf("SetAllowDefault: %v", err)
	}
	seeds = s.CandidateSeeds(defSeed)
	if len(seeds) != 1 || seeds[0] != entry.Seed {
		t.Fatalf("关闭默认密码后候选集应只剩节点密码: %v", seeds)
	}

	if err := s.Clear("node-x"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok := s.Get("node-x"); ok {
		t.Fatal("Clear 后不应再返回节点密码")
	}
	if s.AllowDefault() {
		t.Fatal("Clear 不应改变默认密码开关")
	}
}

func TestSetValidation(t *testing.T) {
	s, _ := Load("")
	if _, err := s.Set("node", "short"); err == nil {
		t.Fatal("过短的密码应被拒绝")
	}
	if _, err := s.Set("", "password-123456"); err == nil {
		t.Fatal("缺少节点 ID 应被拒绝")
	}
	if _, err := s.Set("node", "password-123456"); err != nil {
		t.Fatalf("合法密码应通过: %v", err)
	}
	if err := ValidatePassword("12345678"); err != nil {
		t.Fatalf("8 位密码应合法: %v", err)
	}
}
