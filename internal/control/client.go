package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/hashicorp/yamux"

	"mtun/internal/netx"
	"mtun/internal/secure"
	"mtun/pkg/proto"
)

// Client 使用服务端地址 + 客户端接入密码建立 TLS/yamux 会话。
// 服务端回包只包含本节点的状态与规则。
type Client struct {
	server   string
	token    string
	nodeID   string
	version  string
	rules    []proto.PublishRule
	onStatus func(proto.Status)
	log      *slog.Logger
}

func NewClient(server, token, nodeID, version string, rules []proto.PublishRule, onStatus func(proto.Status), log *slog.Logger) *Client {
	return &Client{
		server:   server,
		token:    token,
		nodeID:   nodeID,
		version:  version,
		rules:    rules,
		onStatus: onStatus,
		log:      log,
	}
}

func (c *Client) Run(ctx context.Context) error {
	tlsCfg, err := secure.ClientConfig(c.token)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", c.server, tlsCfg)
	if err != nil {
		return fmt.Errorf("连接服务端 %s 失败（地址或密钥不正确）: %w", c.server, err)
	}
	defer conn.Close()

	sess, err := yamux.Client(conn, yamuxConfig())
	if err != nil {
		return fmt.Errorf("建立隧道会话: %w", err)
	}
	defer sess.Close()
	go func() {
		<-ctx.Done()
		_ = sess.Close()
	}()

	stream, err := sess.OpenStream()
	if err != nil {
		return err
	}
	defer stream.Close()

	enc := json.NewEncoder(stream)
	dec := json.NewDecoder(stream)

	if err := proto.Send(enc, proto.TypeRegister, proto.Register{
		NodeID:  c.nodeID,
		Version: c.version,
		Path:    "relay",
		Publish: c.rules,
	}); err != nil {
		return err
	}
	c.log.Info("已连接服务端", "server", c.server, "node", c.nodeID, "rules", len(c.rules))

	go c.acceptDataStreams(sess)
	go c.readStatus(dec)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := proto.Send(enc, proto.TypeHeartbeat, proto.Heartbeat{NodeID: c.nodeID, Path: "relay"}); err != nil {
				return fmt.Errorf("发送心跳失败: %w", err)
			}
		}
	}
}

func (c *Client) readStatus(dec *json.Decoder) {
	for {
		env, err := proto.Receive(dec)
		if err != nil {
			return
		}
		if env.Type == proto.TypeError {
			var e proto.Error
			if err := json.Unmarshal(env.Payload, &e); err == nil && e.Message != "" {
				c.log.Warn("服务端拒绝连接", "reason", e.Message)
			}
			continue
		}
		if env.Type != proto.TypeStatus {
			continue
		}
		var st proto.Status
		if err := json.Unmarshal(env.Payload, &st); err != nil {
			c.log.Warn("bad status payload", "err", err)
			continue
		}
		if c.onStatus != nil {
			c.onStatus(st)
		}
	}
}

func (c *Client) acceptDataStreams(sess *yamux.Session) {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go c.handleDataStream(stream)
	}
}

func (c *Client) handleDataStream(stream *yamux.Stream) {
	defer stream.Close()

	_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	header, err := proto.ReadStreamHeader(stream)
	_ = stream.SetReadDeadline(time.Time{})
	if err != nil {
		c.log.Warn("读取数据流头失败", "err", err)
		return
	}

	target := ""
	for _, rule := range c.rules {
		if rule.Name == header.Rule {
			target = rule.Target
			break
		}
	}
	if target == "" {
		c.log.Warn("收到未知规则的数据流", "rule", header.Rule)
		return
	}

	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		c.log.Warn("回源失败", "rule", header.Rule, "target", target, "err", err)
		return
	}
	defer upstream.Close()

	c.log.Debug("隧道数据转发", "rule", header.Rule, "target", target)
	netx.Splice(stream, upstream)
}
