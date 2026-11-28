// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package control

import (
	"io"
	"log/slog"
	"testing"

	"mtun/internal/nodepass"
	"mtun/pkg/proto"
)

func newJoinTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := nodepass.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		passwords: st,
		joinReqs:  map[string]*joinTicket{},
	}
}

func TestJoinQueueApprove(t *testing.T) {
	s := newJoinTestServer(t)
	ticket, err := s.addJoinTicket(proto.Register{NodeID: "n1", Version: "v0.5.1", OS: "linux"}, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s.JoinRequests()); got != 1 {
		t.Fatalf("应有 1 条待批准申请，得到 %d", got)
	}

	pw, node, err := s.ApproveJoin(ticket.ID)
	if err != nil {
		t.Fatalf("批准失败: %v", err)
	}
	if node != "n1" || len(pw) < 8 {
		t.Fatalf("批准结果异常: node=%q pw=%q", node, pw)
	}
	select {
	case d := <-ticket.done:
		if !d.Approved || d.Password != pw {
			t.Fatalf("下发给客户端的结果异常: %+v", d)
		}
	default:
		t.Fatal("批准后应向等待的连接下发结果")
	}
	if _, ok := s.NodePassword("n1"); !ok {
		t.Fatal("批准后应为节点保存独立密码")
	}
	if len(s.JoinRequests()) != 1 {
		t.Fatal("申请应在客户端收到结果后由处理协程移除")
	}
	s.dropJoin(ticket.ID)
	if len(s.JoinRequests()) != 0 {
		t.Fatal("dropJoin 后应清空")
	}

	if _, _, err := s.ApproveJoin("missing"); err == nil {
		t.Fatal("批准不存在的申请应报错")
	}
}

func TestJoinQueueRejectAndDedup(t *testing.T) {
	s := newJoinTestServer(t)
	first, err := s.addJoinTicket(proto.Register{NodeID: "n1"}, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	// 同一节点重复申请复用原记录，避免面板重复条目。
	again, err := s.addJoinTicket(proto.Register{NodeID: "n1"}, "1.2.3.5")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatal("同一节点应复用同一条申请")
	}

	if err := s.RejectJoin(first.ID, "不允许"); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-first.done:
		if d.Approved || d.Message != "不允许" {
			t.Fatalf("拒绝结果异常: %+v", d)
		}
	default:
		t.Fatal("拒绝后应下发结果")
	}
	if len(s.JoinRequests()) != 0 {
		t.Fatal("拒绝后应移出队列")
	}
}

func TestJoinRequestPerIPLimit(t *testing.T) {
	s := newJoinTestServer(t)
	for i := 0; i < joinMaxPendingPerIP; i++ {
		node := proto.Register{NodeID: "n" + string(rune('a'+i))}
		if _, err := s.addJoinTicket(node, "9.9.9.9"); err != nil {
			t.Fatalf("第 %d 条申请不应被拒绝: %v", i+1, err)
		}
	}
	if _, err := s.addJoinTicket(proto.Register{NodeID: "extra"}, "9.9.9.9"); err == nil {
		t.Fatal("超过同 IP 上限应拒绝")
	}
}
