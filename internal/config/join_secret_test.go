// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtun/internal/secret"
)

func TestLoadJoinSecretMigratesPlaintext(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.yaml")
	const plain = "Q5bSojEJHuHjYzOS3gFL7Ax3"

	if err := os.WriteFile(JoinPasswordPath(cfgPath), []byte(plain+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	seed, err := LoadJoinSecret(cfgPath)
	if err != nil {
		t.Fatalf("LoadJoinSecret: %v", err)
	}
	if !secret.IsSeed(seed) {
		t.Fatalf("应返回本地哈希而非明文: %q", seed)
	}
	if seed != secret.Seed(plain) {
		t.Fatal("seed 与明文不一致")
	}

	raw, err := os.ReadFile(JoinPasswordPath(cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), plain) {
		t.Fatalf("迁移后文件仍包含明文: %q", string(raw))
	}

	// 再次读取应保持稳定。
	again, err := LoadJoinSecret(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if again != seed {
		t.Fatalf("重复读取 seed 不稳定: %q vs %q", again, seed)
	}
}

func TestSaveJoinPasswordStoresOnlySeed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.yaml")
	const plain = "another-password-123"

	if err := SaveJoinPassword(cfgPath, plain); err != nil {
		t.Fatalf("SaveJoinPassword: %v", err)
	}
	raw, err := os.ReadFile(JoinPasswordPath(cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), plain) {
		t.Fatal("落盘内容不应包含明文")
	}
	got, err := LoadJoinSecret(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret.Seed(plain) {
		t.Fatal("保存后再读取的 seed 不一致")
	}
}

func TestLoadJoinSecretMissing(t *testing.T) {
	seed, err := LoadJoinSecret(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("缺失文件不应报错: %v", err)
	}
	if seed != "" {
		t.Fatalf("缺失文件应返回空串: %q", seed)
	}
}
