// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package web

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtun/internal/config"
	"mtun/internal/webx"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.yaml")
	cfg := &config.Config{Role: config.RoleServer, Dashboard: "127.0.0.1:10004", Control: ":10000"}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:     cfg,
		cfgPath: cfgPath,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	s.restartFn = func() error { return nil }
	s.exitFn = func() {}
	return s, dir
}

func postJSON(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestRequirePINStates(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.requirePIN("123456"); !errors.Is(err, errPINNotSet) {
		t.Fatalf("未设置时应返回 errPINNotSet: %v", err)
	}
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}
	if err := s.requirePIN("123456"); err != nil {
		t.Fatalf("正确 PIN 应通过: %v", err)
	}
	for i := 0; i < pinMaxFailures-1; i++ {
		if err := s.requirePIN("000000"); err == nil || errors.Is(err, errPINLocked) {
			t.Fatalf("第 %d 次错误不应锁定: %v", i+1, err)
		}
	}
	if err := s.requirePIN("000000"); !errors.Is(err, errPINLocked) {
		t.Fatalf("第 %d 次错误应锁定: %v", pinMaxFailures, err)
	}
	if err := s.requirePIN("123456"); !errors.Is(err, errPINLocked) {
		t.Fatal("锁定期间正确 PIN 也应被拒绝")
	}
}

func TestHandlePINSetupAndChange(t *testing.T) {
	s, _ := newTestServer(t)
	if rec := postJSON(t, s.handlePIN, `{"pin":"123456","confirm":"123456"}`); rec.Code != http.StatusOK {
		t.Fatalf("首次设置失败: %d %s", rec.Code, rec.Body.String())
	}
	if !webx.PINConfigured(s.pinPath()) {
		t.Fatal("PIN 文件未生成")
	}
	if rec := postJSON(t, s.handlePIN, `{"pin":"654321","confirm":"654321"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("未带旧 PIN 修改应返回 401: %d", rec.Code)
	}
	if rec := postJSON(t, s.handlePIN, `{"current_pin":"123456","pin":"654321","confirm":"654321"}`); rec.Code != http.StatusOK {
		t.Fatalf("修改 PIN 失败: %d %s", rec.Code, rec.Body.String())
	}
	ok, _ := webx.VerifyPINFile(s.pinPath(), "654321")
	if !ok {
		t.Fatal("新 PIN 未生效")
	}
}

func TestHandlePanelSecureRestart(t *testing.T) {
	s, _ := newTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}
	called := false
	s.restartFn = func() error { called = true; return nil }
	rec := postJSON(t, s.handlePanelSecure, `{"pin":"123456","dashboard":"127.0.0.1:10014","control":":12000"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("敏感设置失败: %d %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("应触发服务端重启")
	}
	if s.cfg.Dashboard != "127.0.0.1:10014" || s.cfg.Control != ":12000" {
		t.Fatalf("配置未更新: %+v", s.cfg)
	}
	loaded, err := config.Load(s.cfgPath, config.RoleServer)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Control != ":12000" {
		t.Fatalf("配置未落盘: control=%s", loaded.Control)
	}
}

func TestHandlePanelSecurePasswordOnly(t *testing.T) {
	s, _ := newTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}
	s.restartFn = func() error {
		t.Fatal("仅改密码不应重启服务端")
		return nil
	}
	rec := postJSON(t, s.handlePanelSecure, `{"pin":"123456","admin_password":"newpassword123"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("修改密码失败: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(s.cfgPath + ".passwd"); err != nil {
		t.Fatalf("密码文件未生成: %v", err)
	}
}

func TestHandlePanelSecureNeedSetup(t *testing.T) {
	s, _ := newTestServer(t)
	rec := postJSON(t, s.handlePanelSecure, `{"pin":"123456","control":":12000"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("未设置 PIN 应返回 409: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "need_setup") {
		t.Fatalf("响应应包含 need_setup: %s", rec.Body.String())
	}
}

func TestHandlePanelSecureBadControl(t *testing.T) {
	s, _ := newTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, s.handlePanelSecure, `{"pin":"123456","control":"bad"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法接入端口应返回 400: %d", rec.Code)
	}
}
