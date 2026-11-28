// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package nodepass

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestSetRandomStoresSeedAndPlaintext(t *testing.T) {
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
		t.Fatal("seed 字段不应包含明文密码")
	}
	if entry.Seed != secret.Seed(pw) {
		t.Fatal("记录应保存密码的本地哈希")
	}
	if !secret.IsSeed(entry.Seed) {
		t.Fatalf("seed 格式异常: %q", entry.Seed)
	}
	// 明文单独保存，供管理员与节点所有者随时查看（不限次数）。
	if entry.Password != pw {
		t.Fatalf("记录应保留明文供查看: %q", entry.Password)
	}
	if got, ok := s.Password("node-a"); !ok || got != pw {
		t.Fatalf("Password(node-a) = %q, %v", got, ok)
	}
	if owner := s.OwnerOfSeed(entry.Seed); owner != "node-a" {
		t.Fatalf("OwnerOfSeed = %q, want node-a", owner)
	}
	if owner := s.OwnerOfSeed(secret.Seed("default-shared-pass")); owner != "" {
		t.Fatalf("默认密码不应归属任何节点，得到 %q", owner)
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
		{"无独立密码复用其他节点密码", "node-c", entry.Seed, nil},
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
	// 关闭默认密码后，仍允许其他设备复用某个节点的独立密码（换设备场景）。
	if err := s.Authorize("node-c", entry.Seed, defSeed); err != nil {
		t.Fatalf("换设备复用节点密码应被允许: %v", err)
	}
	if err := s.Authorize("node-c", secret.Seed("unknown-password"), defSeed); !errors.Is(err, ErrDefaultDisabled) {
		t.Fatalf("未知密码在关闭默认密码后应被拒绝: %v", err)
	}

	// 文件中保留可读密码，因此在类 Unix 系统上必须只有本机可读。
	// Windows 只支持只读位，os.Chmod(0600) 会退化为 0666，故跳过该断言。
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("读取文件: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("密码文件权限应为 0600，得到 %o", perm)
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
	if got.Password != pw {
		t.Fatalf("重新加载后应仍可查看明文: %q", got.Password)
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
