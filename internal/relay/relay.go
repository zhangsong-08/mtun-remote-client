// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package relay

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"
)

// Relay 是服务端中继的入口骨架。阶段一只负责收包与统计，
// 阶段二在这里实现“按目标节点公钥盲转发密文”。
type Relay struct {
	addr string
	log  *slog.Logger
}

func New(addr string, log *slog.Logger) *Relay {
	return &Relay{addr: addr, log: log}
}

func (r *Relay) Run(ctx context.Context) error {
	conn, err := net.ListenPacket("udp", r.addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	r.log.Info("relay listening", "addr", conn.LocalAddr().String())

	buf := make([]byte, 64*1024)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		// TODO: 解析盲转发头，按目标公钥查表转发；当前仅记录。
		r.log.Debug("relay packet", "from", addr.String(), "bytes", n, "at", time.Now().Format(time.RFC3339))
	}
}
