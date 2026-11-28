// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"time"

	"github.com/hashicorp/yamux"

	"mtun/internal/netx"
	"mtun/internal/secure"
	"mtun/pkg/proto"
)

// Client 使用服务端地址 + 客户端接入密码建立 TLS/yamux 会话。
// 服务端回包只包含本节点的状态与规则，以及按可见性过滤后的其他节点。
type Client struct {
	server   string
	token    string
	nodeID   string
	version  string
	rules    []proto.PublishRule
	onStatus func(proto.Status)
	onPeers  func(proto.Peers)
	onAcl    func(proto.AclUpdate)
	onRules  func(proto.RulesSync)
	log      *slog.Logger
}

func NewClient(server, token, nodeID, version string, rules []proto.PublishRule,
	onStatus func(proto.Status), onPeers func(proto.Peers), onAcl func(proto.AclUpdate),
	onRules func(proto.RulesSync), log *slog.Logger) *Client {
	return &Client{
		server:   server,
		token:    token,
		nodeID:   nodeID,
		version:  version,
		rules:    rules,
		onStatus: onStatus,
		onPeers:  onPeers,
		onAcl:    onAcl,
		onRules:  onRules,
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

	publish := c.rules
	if publish == nil {
		// 显式发送空数组，避免服务端把“已删除全部规则”误判为未上报。
		publish = []proto.PublishRule{}
	}
	if err := proto.Send(enc, proto.TypeRegister, proto.Register{
		NodeID:  c.nodeID,
		Version: c.version,
		OS:      runtime.GOOS,
		Path:    "relay",
		Publish: publish,
	}); err != nil {
		return err
	}
	c.log.Info("已连接服务端", "server", c.server, "node", c.nodeID, "rules", len(c.rules))

	rejected := make(chan string, 1)
	go c.acceptDataStreams(sess)
	go c.readStatus(dec, rejected)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case reason := <-rejected:
			return fmt.Errorf("服务端已断开: %s", reason)
		case <-ticker.C:
			if err := proto.Send(enc, proto.TypeHeartbeat, proto.Heartbeat{NodeID: c.nodeID, Path: "relay"}); err != nil {
				return fmt.Errorf("发送心跳失败: %w", err)
			}
		}
	}
}

func (c *Client) readStatus(dec *json.Decoder, rejected chan<- string) {
	for {
		env, err := proto.Receive(dec)
		if err != nil {
			return
		}
		if env.Type == proto.TypeError {
			var e proto.Error
			if err := json.Unmarshal(env.Payload, &e); err == nil && e.Message != "" {
				c.log.Warn("服务端拒绝连接", "reason", e.Message)
				// 如果是注册阶段被拒绝，通知主循环退出，让 agent 进入重连流程
				select {
				case rejected <- e.Message:
				default:
				}
				return
			}
			continue
		}
		if env.Type != proto.TypeStatus {
			switch env.Type {
			case proto.TypePeers:
				var p proto.Peers
				if err := json.Unmarshal(env.Payload, &p); err != nil {
					c.log.Warn("bad peers payload", "err", err)
					continue
				}
				if c.onPeers != nil {
					c.onPeers(p)
				}
			case proto.TypeAclUpdate:
				var u proto.AclUpdate
				if err := json.Unmarshal(env.Payload, &u); err != nil {
					c.log.Warn("bad acl payload", "err", err)
					continue
				}
				if c.onAcl != nil {
					c.onAcl(u)
				}
			case proto.TypeRulesSync:
				var u proto.RulesSync
				if err := json.Unmarshal(env.Payload, &u); err != nil {
					c.log.Warn("bad rules payload", "err", err)
					continue
				}
				if c.onRules != nil {
					c.onRules(u)
				}
			}
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

const maxConcurrentStreams = 256 // 同时处理的数据流上限，防止 goroutine 爆炸

func (c *Client) acceptDataStreams(sess *yamux.Session) {
	sem := make(chan struct{}, maxConcurrentStreams)
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			// 超过上限直接关闭，等新连接再试
			_ = stream.Close()
			c.log.Warn("数据流并发数超过限制，已丢弃")
			continue
		}
		go func() {
			defer func() { <-sem }()
			c.handleDataStream(stream)
		}()
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

// RequestAccess 在客户端没有接入密码时，通过公开的「申请通道」向服务端提交接入申请。
// 服务端批准后返回一次性明文密码，调用方应立即保存哈希并丢弃明文。
// onPending 在服务端登记申请后回调一次，用于界面展示「等待批准」。
func RequestAccess(ctx context.Context, server, nodeID, version string,
	onPending func(proto.JoinPending), log *slog.Logger) (string, error) {

	tlsCfg, err := secure.ClientConfig(secure.RequestSeed())
	if err != nil {
		return "", err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", server, tlsCfg)
	if err != nil {
		return "", fmt.Errorf("连接服务端 %s 失败: %w", server, err)
	}
	defer conn.Close()

	sess, err := yamux.Client(conn, yamuxConfig())
	if err != nil {
		return "", fmt.Errorf("建立申请会话: %w", err)
	}
	defer sess.Close()
	go func() {
		<-ctx.Done()
		_ = sess.Close()
	}()

	stream, err := sess.OpenStream()
	if err != nil {
		return "", err
	}
	defer stream.Close()

	enc := json.NewEncoder(stream)
	dec := json.NewDecoder(stream)

	if err := proto.Send(enc, proto.TypeJoinRequest, proto.Register{
		NodeID:  nodeID,
		Version: version,
		OS:      runtime.GOOS,
	}); err != nil {
		return "", err
	}
	log.Info("已提交接入申请，等待服务端批准", "server", server, "node", nodeID)

	// 等待期间发送心跳，保持 NAT 映射与服务端连接活跃。
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := proto.Send(enc, proto.TypeHeartbeat, proto.Heartbeat{NodeID: nodeID}); err != nil {
					return
				}
			}
		}
	}()

	for {
		env, err := proto.Receive(dec)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("申请会话已断开: %w", err)
		}
		switch env.Type {
		case proto.TypeJoinPending:
			var p proto.JoinPending
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return "", fmt.Errorf("解析申请回执失败: %w", err)
			}
			if onPending != nil {
				onPending(p)
			}
		case proto.TypeJoinDecision:
			var d proto.JoinDecision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return "", fmt.Errorf("解析审批结果失败: %w", err)
			}
			if d.Approved && d.Password != "" {
				return d.Password, nil
			}
			if d.Message == "" {
				d.Message = "服务端拒绝了本次接入申请"
			}
			return "", errors.New(d.Message)
		case proto.TypeError:
			var e proto.Error
			if err := json.Unmarshal(env.Payload, &e); err != nil {
				return "", fmt.Errorf("申请被拒绝")
			}
			return "", errors.New(e.Message)
		}
	}
}
