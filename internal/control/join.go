// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"mtun/pkg/proto"
)

const (
	// joinTTL 是申请在面板中的保留时长，超时后客户端需要重新申请。
	joinTTL = 10 * time.Minute
	// joinMaxPendingPerIP 限制同一来源 IP 的待批准申请数量，避免被刷屏。
	joinMaxPendingPerIP = 3
)

// joinTicket 表示一条等待管理员审批的接入申请。
type joinTicket struct {
	ID        string
	NodeID    string
	Version   string
	OS        string
	IP        string
	CreatedAt time.Time
	ExpiresAt time.Time
	done      chan proto.JoinDecision
}

// JoinRequestView 是控制台展示用的申请快照。
type JoinRequestView struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	Version   string    `json:"version,omitempty"`
	OS        string    `json:"os,omitempty"`
	IP        string    `json:"ip,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// serveJoin 处理「无密码申请接入」通道：只接受 TypeJoinRequest，
// 登记后阻塞等待审批结果或超时，握手本身不授予任何节点权限。
func (s *Server) serveJoin(cc *clientConn, dec *json.Decoder, remote, host string) {
	if !s.passwords.AllowRequest() {
		s.log.Warn("join request rejected: disabled", "from", remote)
		_ = cc.send(proto.TypeError, proto.Error{Message: "服务端未开放「申请接入」，请联系管理员获取接入密码"})
		return
	}

	for {
		env, err := proto.Receive(dec)
		if err != nil {
			return
		}
		switch env.Type {
		case proto.TypeJoinRequest:
			var msg proto.Register
			if err := json.Unmarshal(env.Payload, &msg); err != nil {
				s.log.Warn("bad join request payload", "from", remote, "err", err)
				return
			}
			msg.NodeID = strings.TrimSpace(msg.NodeID)
			if msg.NodeID == "" {
				_ = cc.send(proto.TypeError, proto.Error{Message: "申请缺少节点 ID"})
				return
			}
			if s.tunnels.Banned(msg.NodeID) {
				s.log.Warn("join request from banned node", "node", msg.NodeID, "from", remote)
				_ = cc.send(proto.TypeError, proto.Error{Message: "节点已被管理员临时封禁，请稍后重试"})
				return
			}
			ticket, err := s.addJoinTicket(msg, host)
			if err != nil {
				s.log.Warn("join request rejected", "node", msg.NodeID, "from", remote, "err", err)
				_ = cc.send(proto.TypeError, proto.Error{Message: err.Error()})
				return
			}
			if err := cc.send(proto.TypeJoinPending, proto.JoinPending{
				RequestID: ticket.ID,
				ExpiresAt: ticket.ExpiresAt.Unix(),
				Message:   "已提交接入申请，等待管理员批准",
			}); err != nil {
				s.dropJoin(ticket.ID)
				return
			}
			s.log.Info("join request queued", "node", ticket.NodeID, "from", remote, "id", ticket.ID)

			timer := time.NewTimer(time.Until(ticket.ExpiresAt))
			select {
			case d := <-ticket.done:
				timer.Stop()
				_ = cc.send(proto.TypeJoinDecision, d)
				s.dropJoin(ticket.ID)
				s.log.Info("join request finished", "node", ticket.NodeID, "approved", d.Approved)
			case <-timer.C:
				_ = cc.send(proto.TypeJoinDecision, proto.JoinDecision{Approved: false, Message: "申请已超时，请重新发起"})
				s.dropJoin(ticket.ID)
				s.log.Info("join request expired", "node", ticket.NodeID)
			}
			return

		case proto.TypeHeartbeat:
			// 等待期间允许客户端心跳，保持 NAT 映射。

		default:
			_ = cc.send(proto.TypeError, proto.Error{Message: "申请通道只接受接入申请"})
			return
		}
	}
}

func (s *Server) addJoinTicket(msg proto.Register, host string) (*joinTicket, error) {
	s.joinMu.Lock()
	defer s.joinMu.Unlock()

	// 同一节点重复申请时复用原记录，避免面板出现重复条目。
	for _, t := range s.joinReqs {
		if t.NodeID == msg.NodeID {
			return t, nil
		}
	}
	pending := 0
	for _, t := range s.joinReqs {
		if t.IP == host {
			pending++
		}
	}
	if pending >= joinMaxPendingPerIP {
		return nil, fmt.Errorf("同一来源的待批准申请过多（%d 条），请等待管理员处理", pending)
	}

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("生成申请编号失败: %w", err)
	}
	now := time.Now()
	t := &joinTicket{
		ID:        hex.EncodeToString(buf),
		NodeID:    msg.NodeID,
		Version:   msg.Version,
		OS:        msg.OS,
		IP:        host,
		CreatedAt: now,
		ExpiresAt: now.Add(joinTTL),
		done:      make(chan proto.JoinDecision, 1),
	}
	s.joinReqs[t.ID] = t
	return t, nil
}

func (s *Server) dropJoin(id string) {
	s.joinMu.Lock()
	defer s.joinMu.Unlock()
	delete(s.joinReqs, id)
}

// JoinRequests 返回当前待批准的申请列表（按申请时间升序）。
func (s *Server) JoinRequests() []JoinRequestView {
	s.joinMu.Lock()
	defer s.joinMu.Unlock()
	out := make([]JoinRequestView, 0, len(s.joinReqs))
	for _, t := range s.joinReqs {
		out = append(out, JoinRequestView{
			ID:        t.ID,
			NodeID:    t.NodeID,
			Version:   t.Version,
			OS:        t.OS,
			IP:        t.IP,
			CreatedAt: t.CreatedAt,
			ExpiresAt: t.ExpiresAt,
		})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].CreatedAt.After(out[j].CreatedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ApproveJoin 批准一条申请：为该节点随机生成独立接入密码并下发客户端。
// 返回的明文密码同时交给管理员留档，之后无法再查看。
func (s *Server) ApproveJoin(id string) (password, nodeID string, err error) {
	s.joinMu.Lock()
	t, ok := s.joinReqs[id]
	s.joinMu.Unlock()
	if !ok {
		return "", "", errors.New("申请不存在或已被处理")
	}
	pw, _, err := s.passwords.SetRandom(t.NodeID)
	if err != nil {
		return "", "", err
	}
	select {
	case t.done <- proto.JoinDecision{Approved: true, Password: pw, Message: "管理员已批准接入"}:
	default:
	}
	s.log.Info("join approved", "node", t.NodeID, "ip", t.IP)
	return pw, t.NodeID, nil
}

// RejectJoin 拒绝一条申请。
func (s *Server) RejectJoin(id, reason string) error {
	s.joinMu.Lock()
	t, ok := s.joinReqs[id]
	s.joinMu.Unlock()
	if !ok {
		return errors.New("申请不存在或已被处理")
	}
	if strings.TrimSpace(reason) == "" {
		reason = "管理员拒绝了本次接入申请"
	}
	select {
	case t.done <- proto.JoinDecision{Approved: false, Message: reason}:
	default:
	}
	s.dropJoin(id)
	s.log.Info("join rejected", "node", t.NodeID, "ip", t.IP)
	return nil
}

// AllowJoinRequest 返回是否允许无密码客户端申请接入。
func (s *Server) AllowJoinRequest() bool {
	return s.passwords.AllowRequest()
}

// SetAllowJoinRequest 切换「允许无密码申请接入」开关。
func (s *Server) SetAllowJoinRequest(allow bool) error {
	return s.passwords.SetAllowRequest(allow)
}

// joinHost 从 remote 中提取 IP，解析失败时原样返回。
func joinHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}
