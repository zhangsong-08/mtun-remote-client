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
	"time"

	"github.com/hashicorp/yamux"

	"mtun/internal/secure"
	"mtun/internal/state"
	"mtun/internal/tunnel"
	"mtun/pkg/proto"
)

// Server 是控制面：TCP + TLS 1.3 + yamux。
// 接入密码由双方共享，服务端不保存客户端列表以外的任何凭据。
type Server struct {
	addr    string
	token   string
	reg     *state.Registry
	tunnels *tunnel.Manager
	log     *slog.Logger
}

func NewServer(addr, token string, reg *state.Registry, tunnels *tunnel.Manager, log *slog.Logger) *Server {
	return &Server{addr: addr, token: token, reg: reg, tunnels: tunnels, log: log}
}

func (s *Server) Run(ctx context.Context) error {
	tlsCfg, err := secure.ServerConfig(s.token)
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
	enc := json.NewEncoder(stream)

	nodeID := ""
	defer func() {
		if nodeID == "" {
			return
		}
		s.tunnels.Remove(nodeID, sess)
		s.log.Info("node disconnected", "node", nodeID, "from", remote)
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
			if s.tunnels.Banned(msg.NodeID) {
				s.log.Warn("reject banned node", "node", msg.NodeID, "from", remote)
				_ = proto.Send(enc, proto.TypeError, proto.Error{Message: "节点已被管理员临时封禁，请稍后重连"})
				return
			}
			nodeID = msg.NodeID
			path := msg.Path
			if path == "" {
				path = "relay"
			}
			s.reg.Upsert(state.Update{
				ID:        msg.NodeID,
				Path:      path,
				Endpoints: []string{remote},
				Publish:   msg.Publish,
				Now:       time.Now(),
			})
			statuses, err := s.tunnels.Register(msg.NodeID, sess, msg.Publish)
			if err != nil {
				s.log.Warn("register rejected", "node", msg.NodeID, "from", remote, "err", err)
				_ = proto.Send(enc, proto.TypeError, proto.Error{Message: err.Error()})
				return
			}
			s.log.Info("node registered", "node", msg.NodeID, "from", remote, "rules", len(msg.Publish))
			if err := s.sendStatus(enc, msg.NodeID, statuses); err != nil {
				return
			}

		case proto.TypeHeartbeat:
			if nodeID == "" {
				return
			}
			s.reg.Upsert(state.Update{ID: nodeID, Path: "relay", Now: time.Now()})
			if err := s.sendStatus(enc, nodeID, s.tunnels.Statuses(nodeID)); err != nil {
				return
			}

		default:
			s.log.Warn("unknown control message", "type", env.Type, "from", remote)
		}
	}
}

func (s *Server) sendStatus(enc *json.Encoder, nodeID string, rules []proto.RuleStatus) error {
	st := proto.Status{Self: s.selfInfo(nodeID), Rules: rules}
	if st.Rules == nil {
		st.Rules = []proto.RuleStatus{}
	}
	if err := proto.Send(enc, proto.TypeStatus, st); err != nil {
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
