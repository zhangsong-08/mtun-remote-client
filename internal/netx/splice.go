// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package netx

import (
	"io"
	"net"
)

// Splice 双向复制两条连接的数据，任一方向结束后关闭双方。
//
// yamux 流不支持 CloseWrite，因此对端以 Close 作为 EOF 信号；
// 这样外部连接断开时不会留下半开的数据流。
func Splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		signalEOF(a)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		signalEOF(b)
		done <- struct{}{}
	}()
	<-done
	<-done
	_ = a.Close()
	_ = b.Close()
}

func signalEOF(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}
