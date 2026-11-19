// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"mtun/internal/config"
	"mtun/internal/control"
	"mtun/pkg/proto"
)

// Status 是客户端控制台展示的全部状态，只包含本客户端的规则，
// 以及与「网络」页相关的、按可见性过滤后的其他节点。
type Status struct {
	Version          string           `json:"version"`
	Node             string           `json:"node"`
	Server           string           `json:"server"`
	Dashboard        string           `json:"dashboard"`
	HasAccess        bool             `json:"has_access"`
	Connected        bool             `json:"connected"`
	Error            string           `json:"error,omitempty"`
	NodeID           string           `json:"node_id,omitempty"`
	OverlayIP        string           `json:"overlay_ip,omitempty"`
	Path             string           `json:"path,omitempty"`
	ConnectedSeconds int64            `json:"connected_seconds"`
	Rules            []RuleStatus     `json:"rules"`
	Peers            []proto.PeerNode `json:"peers"`
}

type RuleStatus struct {
	Name       string   `json:"name"`
	Proto      string   `json:"proto"`
	Listen     int      `json:"listen"`
	Target     string   `json:"target"`
	State      string   `json:"state"`
	Message    string   `json:"message,omitempty"`
	Visibility string   `json:"visibility"`
	VisibleTo  []string `json:"visible_to,omitempty"`
}

// Agent 监管客户端与服务端的连接：配置变化后持久化并重连。
type Agent struct {
	mu       sync.Mutex
	cfg      *config.Config
	cfgPath  string
	joinPass string
	version  string
	log      *slog.Logger
	restart  chan struct{}
	up       bool
	lastErr  string
	self     proto.SelfInfo
	statuses []proto.RuleStatus
	peers    []proto.PeerNode
	since    time.Time
}

func New(cfg *config.Config, cfgPath, joinPass, version string, log *slog.Logger) *Agent {
	return &Agent{
		cfg:      cfg,
		cfgPath:  cfgPath,
		joinPass: joinPass,
		version:  version,
		log:      log,
		restart:  make(chan struct{}, 1),
	}
}

func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	const resetAfter = time.Minute

	var lastSuccess time.Time

	for {
		if ctx.Err() != nil {
			return nil
		}

		server, token, rules := a.params()
		switch {
		case server == "":
			a.setDown("尚未配置服务端地址，请在本机控制台添加")
			if !a.waitRestart(ctx) {
				return nil
			}
			backoff = time.Second
			continue
		case token == "":
			a.setDown("尚未配置接入密码，请在本机控制台添加")
			if !a.waitRestart(ctx) {
				return nil
			}
			backoff = time.Second
			continue
		}

		runCtx, cancel := context.WithCancel(ctx)
		client := control.NewClient(server, token, a.cfg.Node, a.version, rules,
			a.applyStatus, a.applyPeers, a.applyAcl, a.log)
		done := make(chan error, 1)
		go func() { done <- client.Run(runCtx) }()

		select {
		case <-ctx.Done():
			cancel()
			<-done
			return nil
		case <-a.restart:
			cancel()
			<-done
			backoff = time.Second // 配置变更后立即重连
			continue
		case err := <-done:
			cancel()
			if err != nil {
				a.setDown(err.Error())
				a.log.Warn("控制面连接中断（指数退避重连）", "err", err, "backoff", backoff)
			} else {
				a.setDown("连接已断开")
			}
			// 上次成功连接已过去一段时间，重置退避
			if !lastSuccess.IsZero() && time.Since(lastSuccess) > resetAfter {
				backoff = time.Second
			}
			if a.up {
				lastSuccess = time.Now()
				backoff = time.Second // 刚连上又断，重置
			} else {
				// 指数退避 + 抖动
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
			}
			select {
			case <-ctx.Done():
				return nil
			case <-a.restart:
				backoff = time.Second
				continue
			case <-time.After(backoff):
			}
		}
	}
}

func (a *Agent) params() (string, string, []proto.PublishRule) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Server, a.joinPass, append([]proto.PublishRule(nil), a.cfg.Publish.Rules...)
}

func (a *Agent) applyStatus(st proto.Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.up {
		a.since = time.Now()
	}
	a.up = true
	a.lastErr = ""
	a.self = st.Self
	a.statuses = st.Rules
}

// applyPeers 保存服务端推送的可见节点与共享规则，供「网络」页展示。
func (a *Agent) applyPeers(p proto.Peers) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.peers = append([]proto.PeerNode(nil), p.Nodes...)
}

// applyAcl 接收服务端下发的可见范围修改并落盘，避免重连后回退。
func (a *Agent) applyAcl(u proto.AclUpdate) {
	visibility, visibleTo, err := config.NormalizeVisibility(u.Visibility, u.VisibleTo)
	if err != nil {
		a.log.Warn("忽略非法的可见范围下发", "rule", u.Name, "err", err)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.cfg.Publish.Rules {
		if a.cfg.Publish.Rules[i].Name != u.Name {
			continue
		}
		prevVis := a.cfg.Publish.Rules[i].Visibility
		prevTo := a.cfg.Publish.Rules[i].VisibleTo
		a.cfg.Publish.Rules[i].Visibility = visibility
		a.cfg.Publish.Rules[i].VisibleTo = visibleTo
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			a.cfg.Publish.Rules[i].Visibility = prevVis
			a.cfg.Publish.Rules[i].VisibleTo = prevTo
			a.log.Warn("保存服务端下发的可见范围失败", "rule", u.Name, "err", err)
			return
		}
		a.log.Info("服务端已修改规则可见范围", "rule", u.Name, "visibility", visibility)
		return
	}
}

func (a *Agent) setDown(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.up = false
	a.lastErr = msg
	a.self = proto.SelfInfo{}
	a.statuses = nil
	a.peers = nil
}

func (a *Agent) waitRestart(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-a.restart:
		return true
	}
}

func (a *Agent) trigger() {
	select {
	case a.restart <- struct{}{}:
	default:
	}
}

// SetServer 保存服务端地址与接入密码；password 为空表示不修改。
func (a *Agent) SetServer(server, password string) error {
	server = strings.TrimSpace(server)
	password = strings.TrimSpace(password)
	if server == "" {
		return fmt.Errorf("服务端地址不能为空，格式如 1.2.3.4:10000")
	}
	if _, _, err := net.SplitHostPort(server); err != nil {
		return fmt.Errorf("服务端地址需为 host:port：%w", err)
	}
	if password != "" {
		if err := config.ValidateJoinPassword(password); err != nil {
			return err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if password == "" && a.joinPass == "" {
		return fmt.Errorf("首次配置必须填写接入密码（由服务端管理员通过终端设置）")
	}
	a.cfg.Server = server
	if password != "" {
		a.joinPass = password
	}
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		return err
	}
	if password != "" {
		if err := config.SaveJoinPassword(a.cfgPath, a.joinPass); err != nil {
			return err
		}
	}
	a.log.Info("服务端配置已更新", "server", server)
	a.trigger()
	return nil
}

// AddRule 校验并保存一条转发规则，随后重连让服务端开始监听端口。
func (a *Agent) AddRule(rule proto.PublishRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Proto = strings.ToLower(strings.TrimSpace(rule.Proto))
	rule.Target = strings.TrimSpace(rule.Target)
	if rule.Proto == "" {
		rule.Proto = "tcp"
	}
	visibility, visibleTo, err := config.NormalizeVisibility(rule.Visibility, rule.VisibleTo)
	if err != nil {
		return err
	}
	rule.Visibility = visibility
	rule.VisibleTo = visibleTo
	if err := config.ValidateRule(rule); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, r := range a.cfg.Publish.Rules {
		if r.Name == rule.Name {
			return fmt.Errorf("规则名称 %q 已存在", rule.Name)
		}
		if r.Listen == rule.Listen {
			return fmt.Errorf("公网端口 %d 已被规则 %q 使用", rule.Listen, r.Name)
		}
	}
	a.cfg.Publish.Rules = append(a.cfg.Publish.Rules, rule)
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		a.cfg.Publish.Rules = a.cfg.Publish.Rules[:len(a.cfg.Publish.Rules)-1]
		return err
	}
	a.log.Info("转发规则已添加", "name", rule.Name, "listen", rule.Listen, "target", rule.Target)
	a.trigger()
	return nil
}

// DeleteRule 删除一条转发规则。
func (a *Agent) DeleteRule(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("规则名称不能为空")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	idx := -1
	for i, r := range a.cfg.Publish.Rules {
		if r.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("规则 %q 不存在", name)
	}
	removed := a.cfg.Publish.Rules[idx]
	a.cfg.Publish.Rules = append(a.cfg.Publish.Rules[:idx], a.cfg.Publish.Rules[idx+1:]...)
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		a.cfg.Publish.Rules = append(a.cfg.Publish.Rules, proto.PublishRule{})
		copy(a.cfg.Publish.Rules[idx+1:], a.cfg.Publish.Rules[idx:])
		a.cfg.Publish.Rules[idx] = removed
		return err
	}
	a.log.Info("转发规则已删除", "name", name)
	a.trigger()
	return nil
}

// SetRuleVisibility 修改本机规则的可见范围并落盘，随后重连让服务端刷新 peers。
func (a *Agent) SetRuleVisibility(name, visibility string, visibleTo []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("规则名称不能为空")
	}
	visibility, visibleTo, err := config.NormalizeVisibility(visibility, visibleTo)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.cfg.Publish.Rules {
		if a.cfg.Publish.Rules[i].Name != name {
			continue
		}
		prevVis := a.cfg.Publish.Rules[i].Visibility
		prevTo := a.cfg.Publish.Rules[i].VisibleTo
		a.cfg.Publish.Rules[i].Visibility = visibility
		a.cfg.Publish.Rules[i].VisibleTo = visibleTo
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			a.cfg.Publish.Rules[i].Visibility = prevVis
			a.cfg.Publish.Rules[i].VisibleTo = prevTo
			return err
		}
		a.log.Info("规则可见范围已更新", "rule", name, "visibility", visibility)
		a.trigger()
		return nil
	}
	return fmt.Errorf("规则 %q 不存在", name)
}

// Snapshot 汇总连接状态与规则状态，供客户端控制台轮询。
func (a *Agent) Snapshot() Status {
	a.mu.Lock()
	defer a.mu.Unlock()

	st := Status{
		Version:   a.version,
		Node:      a.cfg.Node,
		Server:    a.cfg.Server,
		Dashboard: a.cfg.Dashboard,
		HasAccess: a.joinPass != "",
		Connected: a.up,
		Error:     a.lastErr,
		NodeID:    a.self.NodeID,
		OverlayIP: a.self.OverlayIP,
		Path:      a.self.Path,
		Rules:     make([]RuleStatus, 0, len(a.cfg.Publish.Rules)),
		Peers:     append([]proto.PeerNode(nil), a.peers...),
	}
	if st.Peers == nil {
		st.Peers = []proto.PeerNode{}
	}
	if a.up {
		st.ConnectedSeconds = int64(time.Since(a.since).Seconds())
	}

	for _, spec := range a.cfg.Publish.Rules {
		view := RuleStatus{
			Name:       spec.Name,
			Proto:      spec.Proto,
			Listen:     spec.Listen,
			Target:     spec.Target,
			State:      proto.RulePending,
			Message:    "未连接服务端",
			Visibility: spec.VisibilityMode(),
			VisibleTo:  append([]string(nil), spec.VisibleTo...),
		}
		if a.up {
			view.Message = "等待服务端确认"
		}
		for _, rs := range a.statuses {
			if rs.Name == spec.Name {
				view.State = rs.State
				view.Message = rs.Message
				break
			}
		}
		st.Rules = append(st.Rules, view)
	}
	return st
}
