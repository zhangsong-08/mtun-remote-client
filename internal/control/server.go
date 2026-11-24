// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"mtun/internal/access"
	"mtun/internal/config"
	"mtun/internal/nodepass"
	"mtun/internal/pending"
	"mtun/internal/secure"
	"mtun/internal/state"
	"mtun/internal/tunnel"
	"mtun/pkg/proto"
)

// Server 是控制面：TCP + TLS 1.3 + yamux。
// 默认使用全局共享接入密码；配置了节点独立密码后，每个节点必须使用自己的密码。
type Server struct {
	addr      string
	token     string
	cfgPath   string
	reg       *state.Registry
	tunnels   *tunnel.Manager
	access    *access.Store
	pending   *pending.Store
	passwords *nodepass.Store
	log       *slog.Logger

	mu    sync.Mutex
	conns map[string]*clientConn
}

// clientConn 封装单个节点的控制流写入，避免状态回执与 peers 推送并发写同一编码器。
type clientConn struct {
	enc *json.Encoder
	mu  sync.Mutex
	os  string
	ip  string
}

func (c *clientConn) send(kind string, payload any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return proto.Send(c.enc, kind, payload)
}

func NewServer(addr, token, cfgPath string, reg *state.Registry, tunnels *tunnel.Manager, log *slog.Logger) *Server {
	s := &Server{
		addr:    addr,
		token:   token,
		cfgPath: cfgPath,
		reg:     reg,
		tunnels: tunnels,
		log:     log,
		conns:   map[string]*clientConn{},
	}
	if cfgPath != "" {
		accessPath := cfgPath + ".access.json"
		if st, err := access.Load(accessPath); err != nil {
			log.Warn("读取访问策略失败，使用空策略", "path", accessPath, "err", err)
			st, _ = access.Load("")
			s.access = st
		} else {
			s.access = st
		}
		pendingPath := cfgPath + ".rules.json"
		if st, err := pending.Load(pendingPath); err != nil {
			log.Warn("读取待同步规则失败，使用空队列", "path", pendingPath, "err", err)
			st, _ = pending.Load("")
			s.pending = st
		} else {
			s.pending = st
		}
		passPath := cfgPath + ".nodepass.json"
		if st, err := nodepass.Load(passPath); err != nil {
			log.Warn("读取节点接入密码失败，使用空集合", "path", passPath, "err", err)
			st, _ = nodepass.Load("")
			s.passwords = st
		} else {
			s.passwords = st
		}
	} else {
		s.access, _ = access.Load("")
		s.pending, _ = pending.Load("")
		s.passwords, _ = nodepass.Load("")
	}
	return s
}

func (s *Server) Run(ctx context.Context) error {
	tlsCfg, err := secure.ServerConfigFor(s.pickToken)
	if err != nil {
		return err
	}
	ln, err := tls.Listen("tcp", s.addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("control listen %s: %w", s.addr, err)
	}
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	s.log.Info("control listening", "addr", ln.Addr().String(), "tls", "1.3")

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

// yamuxConfig 每次新建，避免多个会话共享同一个可变配置。
func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 10 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.StreamOpenTimeout = 15 * time.Second
	cfg.StreamCloseTimeout = 2 * time.Minute
	return cfg
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()

	// 从 TLS 握手拿到客户端实际使用的接入密码，后续用它校验节点身份。
	usedToken := ""
	if tc, ok := conn.(*tls.Conn); ok {
		if err := tc.HandshakeContext(ctx); err != nil {
			s.log.Warn("tls handshake failed", "from", remote, "err", err)
			return
		}
		usedToken = s.tokenForSNI(tc.ConnectionState().ServerName)
		if usedToken == "" {
			s.log.Warn("无法识别本次连接使用的接入密码", "from", remote)
			return
		}
	}

	sess, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		s.log.Warn("yamux handshake failed", "from", remote, "err", err)
		return
	}
	defer sess.Close()

	// 客户端建立会话后的第一条流固定是控制流。
	stream, err := sess.AcceptStream()
	if err != nil {
		return
	}
	defer stream.Close()

	dec := json.NewDecoder(stream)
	cc := &clientConn{enc: json.NewEncoder(stream)}

	nodeID := ""
	defer func() {
		if nodeID == "" {
			return
		}
		current := s.removeConn(nodeID, cc)
		s.tunnels.Remove(nodeID, sess)
		if current {
			s.reg.MarkOffline(nodeID)
		}
		s.log.Info("node disconnected", "node", nodeID, "from", remote)
		s.broadcastPeers()
	}()

	for {
		env, err := proto.Receive(dec)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, io.EOF) {
				s.log.Debug("control stream closed", "node", nodeID, "err", err)
			}
			return
		}
		switch env.Type {
		case proto.TypeRegister:
			var msg proto.Register
			if err := json.Unmarshal(env.Payload, &msg); err != nil {
				s.log.Warn("bad register payload", "from", remote, "err", err)
				return
			}
			if msg.NodeID == "" {
				s.log.Warn("register without node id", "from", remote)
				return
			}
			host, _, splitErr := net.SplitHostPort(remote)
			if splitErr != nil {
				host = remote
			}
			if s.tunnels.Banned(msg.NodeID) {
				s.log.Warn("reject banned node", "node", msg.NodeID, "from", remote)
				_ = cc.send(proto.TypeError, proto.Error{Message: "节点已被管理员临时封禁，请稍后重连"})
				return
			}
			// 校验本次连接使用的密码是否属于该节点（节点独立密码或允许的默认密码）。
			if err := s.passwords.Authorize(msg.NodeID, usedToken, s.token); err != nil {
				s.log.Warn("reject by node password", "node", msg.NodeID, "from", remote, "err", err)
				_ = cc.send(proto.TypeError, proto.Error{Message: err.Error()})
				return
			}
			if err := s.access.Check(msg.NodeID, msg.OS, host, time.Now()); err != nil {
				s.log.Warn("reject by access policy", "node", msg.NodeID, "from", remote, "err", err)
				_ = cc.send(proto.TypeError, proto.Error{Message: err.Error()})
				return
			}
			nodeID = msg.NodeID
			cc.os = msg.OS
			cc.ip = host
			path := msg.Path
			if path == "" {
				path = "relay"
			}
			rules := msg.Publish
			ops := s.pending.Take(msg.NodeID)
			if len(ops) > 0 {
				var applyErrs []error
				rules, applyErrs = pending.Apply(rules, ops)
				for _, e := range applyErrs {
					s.log.Warn("应用待同步规则失败", "node", msg.NodeID, "err", e)
				}
			}
			s.reg.Upsert(state.Update{
				ID:        msg.NodeID,
				OS:        msg.OS,
				ClientIP:  host,
				Path:      path,
				Endpoints: []string{remote},
				Publish:   rules,
				Now:       time.Now(),
			})
			statuses, err := s.tunnels.Register(msg.NodeID, sess, rules)
			if err != nil {
				s.log.Warn("register rejected", "node", msg.NodeID, "from", remote, "err", err)
				_ = cc.send(proto.TypeError, proto.Error{Message: err.Error()})
				return
			}
			s.addConn(msg.NodeID, cc)
			s.log.Info("node registered", "node", msg.NodeID, "from", remote, "rules", len(rules))
			if err := s.sendStatus(cc, msg.NodeID, statuses); err != nil {
				return
			}
			if len(ops) > 0 {
				if err := cc.send(proto.TypeRulesSync, proto.RulesSync{Rules: rules}); err != nil {
					return
				}
			}
			s.broadcastPeers()

		case proto.TypeHeartbeat:
			if nodeID == "" {
				return
			}
			if err := s.access.Check(nodeID, cc.os, cc.ip, time.Now()); err != nil {
				s.log.Warn("heartbeat rejected by access policy", "node", nodeID, "err", err)
				_ = cc.send(proto.TypeError, proto.Error{Message: err.Error()})
				return
			}
			s.reg.Upsert(state.Update{ID: nodeID, Path: "relay", Now: time.Now()})
			if err := s.sendStatus(cc, nodeID, s.tunnels.Statuses(nodeID)); err != nil {
				return
			}

		default:
			s.log.Warn("unknown control message", "type", env.Type, "from", remote)
		}
	}
}

func (s *Server) sendStatus(cc *clientConn, nodeID string, rules []proto.RuleStatus) error {
	st := proto.Status{Self: s.selfInfo(nodeID), Rules: rules}
	if st.Rules == nil {
		st.Rules = []proto.RuleStatus{}
	}
	if err := cc.send(proto.TypeStatus, st); err != nil {
		s.log.Warn("send status", "node", nodeID, "err", err)
		return err
	}
	return nil
}

func (s *Server) selfInfo(nodeID string) proto.SelfInfo {
	n, ok := s.reg.Get(nodeID)
	if !ok {
		return proto.SelfInfo{NodeID: nodeID, Path: "relay"}
	}
	return proto.SelfInfo{NodeID: n.ID, OverlayIP: n.OverlayIP, Path: n.Path}
}

func (s *Server) addConn(nodeID string, cc *clientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[nodeID] = cc
}

// removeConn 仅在当前连接仍是该节点的主连接时才删除，避免旧连接误删新连接。
func (s *Server) removeConn(nodeID string, cc *clientConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns[nodeID] != cc {
		return false
	}
	delete(s.conns, nodeID)
	return true
}

func (s *Server) connFor(nodeID string) *clientConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[nodeID]
}

// broadcastPeers 按请求者过滤后，向所有在线客户端推送节点与共享规则。
func (s *Server) broadcastPeers() {
	s.mu.Lock()
	targets := make(map[string]*clientConn, len(s.conns))
	for id, cc := range s.conns {
		targets[id] = cc
	}
	s.mu.Unlock()

	for id, cc := range targets {
		if err := cc.send(proto.TypePeers, proto.Peers{Nodes: s.reg.PeersFor(id)}); err != nil {
			s.log.Debug("push peers", "node", id, "err", err)
		}
	}
}

// SetRuleVisibility 修改指定节点规则的可见范围，并下发到该客户端落盘。
// 只允许在线节点，确保客户端配置与服务端内存状态一致。
func (s *Server) SetRuleVisibility(nodeID, name, visibility string, visibleTo []string) error {
	cc := s.connFor(nodeID)
	if cc == nil {
		return fmt.Errorf("节点 %s 不在线，无法修改可见范围", nodeID)
	}
	if err := s.tunnels.SetVisibility(nodeID, name, visibility, visibleTo); err != nil {
		return err
	}
	if err := s.reg.SetRuleVisibility(nodeID, name, visibility, visibleTo); err != nil {
		return err
	}
	if err := cc.send(proto.TypeAclUpdate, proto.AclUpdate{
		Name:       name,
		Visibility: visibility,
		VisibleTo:  visibleTo,
	}); err != nil {
		return fmt.Errorf("下发可见范围失败: %w", err)
	}
	s.log.Info("规则可见范围已修改", "node", nodeID, "rule", name, "visibility", visibility, "visible_to", len(visibleTo))
	s.broadcastPeers()
	return nil
}

// ApplyRuleChange 对指定节点增删改一条规则；在线节点立即生效并下发落盘，
// 离线节点写入待同步队列，下次注册时应用。
func (s *Server) ApplyRuleChange(nodeID string, op pending.Op) ([]proto.PublishRule, bool, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, false, errors.New("缺少节点 ID")
	}
	n, ok := s.reg.Get(nodeID)
	if !ok {
		return nil, false, fmt.Errorf("节点 %s 不存在", nodeID)
	}
	online := s.connFor(nodeID) != nil
	current := n.Publish
	if online {
		if specs := s.tunnels.RuleSpecs(nodeID); specs != nil {
			current = specs
		}
	}
	final, applyErrs := pending.Apply(current, []pending.Op{op})
	if len(applyErrs) > 0 {
		return nil, online, applyErrs[0]
	}
	for _, rule := range final {
		if err := config.ValidateRule(rule); err != nil {
			return nil, online, err
		}
	}

	if !online {
		if err := s.reg.SetPublish(nodeID, final); err != nil {
			return nil, false, err
		}
		if err := s.pending.Add(nodeID, op); err != nil {
			return nil, false, err
		}
		s.log.Info("规则变更已排队等待节点上线", "node", nodeID, "op", op.Op, "rule", op.Rule.Name)
		s.broadcastPeers()
		return final, false, nil
	}

	statuses, err := s.tunnels.ApplyRules(nodeID, final)
	if err != nil {
		return nil, true, err
	}
	if err := s.reg.SetPublish(nodeID, final); err != nil {
		return nil, true, err
	}
	cc := s.connFor(nodeID)
	if cc == nil {
		// 连接刚断开，转为离线排队。
		if err := s.pending.Add(nodeID, op); err != nil {
			return nil, false, err
		}
		return final, false, nil
	}
	if err := cc.send(proto.TypeRulesSync, proto.RulesSync{Rules: final}); err != nil {
		// 客户端未落盘时重新排队，保证下次注册应用。
		if qerr := s.pending.Add(nodeID, op); qerr != nil {
			s.log.Warn("回填待同步规则失败", "node", nodeID, "err", qerr)
		}
		return nil, true, fmt.Errorf("下发规则失败: %w", err)
	}
	if err := s.sendStatus(cc, nodeID, statuses); err != nil {
		s.log.Warn("下发规则状态失败", "node", nodeID, "err", err)
	}
	s.log.Info("规则已更新并下发", "node", nodeID, "op", op.Op, "rule", op.Rule.Name)
	s.broadcastPeers()
	return final, true, nil
}

// AccessPolicy 返回节点访问策略。
func (s *Server) AccessPolicy(id string) access.Policy {
	return s.access.Get(id)
}

// AccessPolicies 返回全部访问策略。
func (s *Server) AccessPolicies() map[string]access.Policy {
	return s.access.List()
}

// SetAccessPolicy 保存节点访问策略。
func (s *Server) SetAccessPolicy(id string, p access.Policy) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("缺少节点 ID")
	}
	return s.access.Set(id, p)
}

// ClearAccessPolicy 清除节点访问策略。
func (s *Server) ClearAccessPolicy(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("缺少节点 ID")
	}
	return s.access.Clear(id)
}

// PendingCount 返回节点待同步的规则操作数量。
func (s *Server) PendingCount(id string) int {
	return s.pending.Count(id)
}

// pickToken 在 TLS 握手阶段根据 SNI 选择该连接应使用的接入密码。
// 旧客户端（SNI 为空或固定 "mtun"）在允许默认密码时回退到全局密码。
func (s *Server) pickToken(sni string) (string, bool) {
	t := s.tokenForSNI(sni)
	if t == "" {
		return "", false
	}
	return t, true
}

// tokenForSNI 把 SNI 指纹反查成实际密码；空字符串表示无法识别。
func (s *Server) tokenForSNI(sni string) string {
	if sni == "" || sni == "mtun" {
		if s.passwords.AllowDefault() {
			return s.token
		}
		return ""
	}
	for _, t := range s.passwords.CandidateSeeds(s.token) {
		if secure.TokenFingerprint(t) == sni {
			return t
		}
	}
	return ""
}

// NodePasswords 返回默认密码开关与全部节点独立密码。
func (s *Server) NodePasswords() (bool, map[string]nodepass.Entry) {
	return s.passwords.AllowDefault(), s.passwords.List()
}

// NodePassword 返回单个节点的独立密码。
func (s *Server) NodePassword(nodeID string) (nodepass.Entry, bool) {
	return s.passwords.Get(nodeID)
}

// HasNodePassword 判断节点是否设置了独立密码。
func (s *Server) HasNodePassword(nodeID string) bool {
	_, ok := s.passwords.Get(nodeID)
	return ok
}

// SetNodePassword 为节点指定接入密码。
func (s *Server) SetNodePassword(nodeID, password string) (nodepass.Entry, error) {
	return s.passwords.Set(nodeID, password)
}

// SetNodeRandomPassword 为节点生成随机接入密码，明文只在返回值中出现一次。
func (s *Server) SetNodeRandomPassword(nodeID string) (string, nodepass.Entry, error) {
	pw, entry, err := s.passwords.SetRandom(nodeID)
	if err == nil {
		s.log.Info("已为节点生成随机接入密码（仅保存哈希）", "node", strings.TrimSpace(nodeID))
	}
	return pw, entry, err
}

// ClearNodePassword 删除节点的独立密码，使其回落到默认密码。
func (s *Server) ClearNodePassword(nodeID string) error {
	err := s.passwords.Clear(nodeID)
	if err == nil {
		s.log.Info("已清除节点独立接入密码", "node", strings.TrimSpace(nodeID))
	}
	return err
}

// SetAllowDefaultPassword 切换「允许默认密码接入」开关。
func (s *Server) SetAllowDefaultPassword(allow bool) error {
	err := s.passwords.SetAllowDefault(allow)
	if err == nil {
		s.log.Info("默认密码接入开关已更新", "allow", allow)
	}
	return err
}
