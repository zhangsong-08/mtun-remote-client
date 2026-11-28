// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package secure

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// startServer 用给定候选密码集合启动一个只做握手的服务端。
func startServer(t *testing.T, allowed []string) (addr string, stop func()) {
	t.Helper()
	cfg, err := ServerConfigFor(func(sni string) (string, bool) {
		for _, tk := range allowed {
			if TokenFingerprint(tk) == sni {
				return tk, true
			}
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("ServerConfigFor: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.(*tls.Conn).Handshake()
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close(); <-done }
}

func dial(t *testing.T, addr, token string) error {
	t.Helper()
	cfg, err := ClientConfig(token)
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	d := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, cfg)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func TestServerConfigForAcceptsAnyConfiguredToken(t *testing.T) {
	addr, stop := startServer(t, []string{"default-password", "node-a-password"})
	defer stop()

	if err := dial(t, addr, "default-password"); err != nil {
		t.Fatalf("默认密码应能握手: %v", err)
	}
	if err := dial(t, addr, "node-a-password"); err != nil {
		t.Fatalf("节点密码应能握手: %v", err)
	}
	if err := dial(t, addr, "unknown-password"); err == nil {
		t.Fatal("未登记的密码不应握手成功")
	}
}

func TestServerConfigForRejectsWhenDefaultDisabled(t *testing.T) {
	// 只登记节点密码，模拟关闭默认密码后的候选集合。
	addr, stop := startServer(t, []string{"node-a-password"})
	defer stop()

	if err := dial(t, addr, "default-password"); err == nil {
		t.Fatal("关闭默认密码后默认密码不应握手成功")
	}
	if err := dial(t, addr, "node-a-password"); err != nil {
		t.Fatalf("节点密码应仍可握手: %v", err)
	}
}

func TestRequestChannelHandshake(t *testing.T) {
	// 申请通道使用公开口令，不依赖任何节点密码。
	addr, stop := startServer(t, []string{RequestSeed()})
	defer stop()

	cfg, err := ClientConfig(RequestSeed())
	if err != nil {
		t.Fatal(err)
	}
	d := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, cfg)
	if err != nil {
		t.Fatalf("申请通道应能握手: %v", err)
	}
	state := conn.ConnectionState()
	_ = conn.Close()
	if state.ServerName != RequestFingerprint() {
		t.Fatalf("申请通道 SNI 异常: %q", state.ServerName)
	}
	if RequestFingerprint() == TokenFingerprint("") {
		t.Fatal("申请通道指纹不应与空密码相同")
	}
}

func TestTokenFingerprintStableAndDistinct(t *testing.T) {
	a := TokenFingerprint("password-a")
	b := TokenFingerprint("password-b")
	if a != TokenFingerprint("password-a") {
		t.Fatal("同一密码的指纹应稳定")
	}
	if a == b {
		t.Fatal("不同密码的指纹不应相同")
	}
	if len(a) <= len("mtun-") {
		t.Fatalf("指纹格式异常: %q", a)
	}
}
