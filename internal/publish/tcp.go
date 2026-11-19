// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package publish

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"mtun/pkg/proto"
)

// TCPProxy 是端口发布的最小实现：监听公网端口并把连接转发到目标。
// 正式版的目标应为覆盖网地址，由客户端为每条规则做回源。
type TCPProxy struct {
	name   string
	listen string
	target string
	log    *slog.Logger
}

func NewTCPProxy(rule proto.PublishRule, log *slog.Logger) (*TCPProxy, error) {
	switch rule.Proto {
	case "", "tcp", "http":
	default:
		return nil, fmt.Errorf("publish %q: proto %q not supported in skeleton", rule.Name, rule.Proto)
	}
	if rule.Target == "" {
		return nil, fmt.Errorf("publish %q: target is required", rule.Name)
	}
	return &TCPProxy{
		name:   rule.Name,
		listen: fmt.Sprintf("0.0.0.0:%d", rule.Listen),
		target: rule.Target,
		log:    log,
	}, nil
}

func (p *TCPProxy) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.listen)
	if err != nil {
		return fmt.Errorf("publish %s: listen %s: %w", p.name, p.listen, err)
	}
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	p.log.Info("publish tcp", "name", p.name, "listen", p.listen, "target", p.target)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go p.handle(conn)
	}
}

func (p *TCPProxy) handle(conn net.Conn) {
	defer conn.Close()

	upstream, err := net.DialTimeout("tcp", p.target, 5*time.Second)
	if err != nil {
		p.log.Warn("publish dial failed", "name", p.name, "target", p.target, "err", err)
		return
	}
	defer upstream.Close()

	p.log.Debug("publish connection", "name", p.name, "from", conn.RemoteAddr(), "to", p.target)

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		if tc, ok := upstream.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}
