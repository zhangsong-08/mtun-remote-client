// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package webx

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestPINFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml.pin")
	if err := SetPINFile(path, "123456"); err != nil {
		t.Fatal(err)
	}
	if !PINConfigured(path) {
		t.Fatal("PIN 应已配置")
	}
	ok, err := VerifyPINFile(path, "123456")
	if err != nil || !ok {
		t.Fatalf("正确 PIN 校验失败: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPINFile(path, "654321")
	if err != nil || ok {
		t.Fatalf("错误 PIN 不应通过: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if err := SetPINFile(path, bad); err == nil {
			t.Fatalf("PIN %q 不应通过格式校验", bad)
		}
	}
	missing := filepath.Join(t.TempDir(), "none.pin")
	if _, err := VerifyPINFile(missing, "123456"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失文件应返回 os.ErrNotExist: %v", err)
	}
	if PINConfigured(missing) {
		t.Fatal("缺失文件不应视为已配置")
	}
}

func TestSetCredentialsInvalidatesSessions(t *testing.T) {
	auth, err := NewAuth(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{PasswordFile: filepath.Join(t.TempDir(), "c.passwd"), AllowSetup: true})
	if err != nil {
		t.Fatal(err)
	}
	defer auth.Close()
	auth.mu.Lock()
	auth.sessions["old"] = session{}
	auth.mu.Unlock()
	if err := auth.SetCredentials("admin", "newpassword123"); err != nil {
		t.Fatal(err)
	}
	auth.mu.Lock()
	n := len(auth.sessions)
	auth.mu.Unlock()
	if n != 0 {
		t.Fatalf("修改密码后会话应清空，剩余 %d", n)
	}
}
