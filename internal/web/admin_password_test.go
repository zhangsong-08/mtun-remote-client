// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mtun/internal/config"
	"mtun/internal/control"
	"mtun/internal/state"
	"mtun/internal/tunnel"
	"mtun/internal/webx"
)

func newPasswordTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.yaml")
	cfg := &config.Config{Role: config.RoleServer, Dashboard: "127.0.0.1:10004", Control: ":10000"}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctl := control.NewServer("127.0.0.1:0", "default-password",
		cfgPath, state.New("10.0.0.1", "10.0.0.0/24"), tunnel.NewManager(log, ""), log)
	s := &Server{cfg: cfg, cfgPath: cfgPath, log: log, ctl: ctl}
	s.restartFn = func() error { return nil }
	s.exitFn = func() {}
	return s
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestNodePasswordRequiresPINSetup(t *testing.T) {
	s := newPasswordTestServer(t)
	rec := postJSON(t, s.handleNodePassword, `{"pin":"123456","node":"n1","action":"random"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("未设置 PIN 时应返回 409，得到 %d: %s", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["need_setup"] != true {
		t.Fatalf("应提示先设置 PIN: %v", body)
	}
}

func TestNodePasswordRandomSetShowClear(t *testing.T) {
	s := newPasswordTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}

	// 随机生成。
	rec := postJSON(t, s.handleNodePassword, `{"pin":"123456","node":"n1","action":"random"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("随机生成失败: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	pw, _ := body["password"].(string)
	if len(pw) < 8 || body["set"] != true {
		t.Fatalf("随机密码异常: %v", body)
	}

	// 查看现有：只返回是否已设置，不再返回密码。
	rec = postJSON(t, s.handleNodePassword, `{"pin":"123456","node":"n1","action":"show"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("查看失败: %d", rec.Code)
	}
	shown := decodeBody(t, rec)
	if shown["set"] != true {
		t.Fatalf("应标记为已设置: %v", shown)
	}
	if _, hasPassword := shown["password"]; hasPassword {
		t.Fatal("show 不应再返回密码（明文或哈希）")
	}

	// 已设置 → 节点视图应标记 has_password。
	if !s.ctl.HasNodePassword("n1") {
		t.Fatal("控制面应记录节点独立密码")
	}

	// 指定过短密码 → 400。
	rec = postJSON(t, s.handleNodePassword, `{"pin":"123456","node":"n1","action":"set","password":"short"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("短密码应返回 400，得到 %d", rec.Code)
	}

	// 清除。
	rec = postJSON(t, s.handleNodePassword, `{"pin":"123456","node":"n1","action":"clear"}`)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["set"] != false {
		t.Fatalf("清除失败: %d %s", rec.Code, rec.Body.String())
	}
	newPasswordTestServerPasswordCleared(t, s)
}

func newPasswordTestServerPasswordCleared(t *testing.T, s *Server) {
	t.Helper()
	if s.ctl.HasNodePassword("n1") {
		t.Fatal("清除后不应再有独立密码")
	}
}

func TestNodePasswordWrongPIN(t *testing.T) {
	s := newPasswordTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, s.handleNodePassword, `{"pin":"000000","node":"n1","action":"random"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 PIN 应返回 401，得到 %d", rec.Code)
	}
}

func TestDefaultPasswordToggle(t *testing.T) {
	s := newPasswordTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}

	get := func() bool {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		s.handleDefaultPassword(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET 失败: %d", rec.Code)
		}
		body := decodeBody(t, rec)
		v, _ := body["allow_default"].(bool)
		return v
	}

	if !get() {
		t.Fatal("默认应允许默认密码接入")
	}
	rec := postJSON(t, s.handleDefaultPassword, `{"pin":"123456","allow":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("关闭开关失败: %d %s", rec.Code, rec.Body.String())
	}
	if get() {
		t.Fatal("关闭后应返回 false")
	}
	// 错误 PIN 不应改变开关。
	rec = postJSON(t, s.handleDefaultPassword, `{"pin":"999999","allow":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 PIN 应 401，得到 %d", rec.Code)
	}
	if get() {
		t.Fatal("错误 PIN 不应修改开关")
	}
}
