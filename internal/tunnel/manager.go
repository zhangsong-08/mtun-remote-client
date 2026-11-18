package tunnel

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"mtun/internal/netx"
	"mtun/pkg/proto"
)

// Manager 在服务端为每个客户端的每条规则监听公网端口。
// 外部连接到来时通过该客户端自己的 yamux 会话开一条数据流。
type Manager struct {
	log       *slog.Logger
	mu        sync.Mutex
	nodes     map[string]*node
	banned    map[string]time.Time
	portRange [2]int // [low, high]，0 表示不限制
}

type node struct {
	id    string
	sess  *yamux.Session
	rules map[string]*rule
	specs []proto.PublishRule
}

type rule struct {
	spec  proto.PublishRule
	ln    net.Listener
	state string
	msg   string
}

func NewManager(log *slog.Logger, portRange string) *Manager {
	m := &Manager{log: log, nodes: map[string]*node{}, banned: map[string]time.Time{}}
	if lo, hi, err := parsePortRange(portRange); err == nil {
		m.portRange = [2]int{lo, hi}
	}
	return m
}

// parsePortRange 解析 "10100-10999" 格式的端口范围。
func parsePortRange(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("port_range 格式应为 low-high，例如 10100-10999")
	}
	lo, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("port_range 下限非法: %w", err)
	}
	hi, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("port_range 上限非法: %w", err)
	}
	if lo <= 0 || hi > 65535 || lo > hi {
		return 0, 0, fmt.Errorf("port_range %d-%d 非法", lo, hi)
	}
	return lo, hi, nil
}

// validateRules 检查客户端上报的规则是否满足端口范围和跨节点不冲突约束。
func (m *Manager) validateRules(newNodeID string, specs []proto.PublishRule) error {
	// 1. 端口范围校验
	for _, s := range specs {
		if m.portRange[0] > 0 && (s.Listen < m.portRange[0] || s.Listen > m.portRange[1]) {
			return fmt.Errorf("规则 %q 端口 %d 不在服务端允许范围 %d-%d 内",
				s.Name, s.Listen, m.portRange[0], m.portRange[1])
		}
	}
	// 2. 跨节点端口冲突检测（跳过自己）
	for id, n := range m.nodes {
		if id == newNodeID {
			continue
		}
		for _, existing := range n.specs {
			for _, incoming := range specs {
				if existing.Listen == incoming.Listen {
					return fmt.Errorf("公网端口 %d 已被节点 %s 的规则 %q 占用",
						incoming.Listen, id, existing.Name)
				}
			}
		}
	}
	return nil
}

// Kick 强制断开节点当前会话，并在 banFor 时间内拒绝其重新注册。
func (m *Manager) Kick(id string, banFor time.Duration) error {
	m.mu.Lock()
	n, ok := m.nodes[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("节点 %s 不在线", id)
	}
	n.closeRules()
	sess := n.sess
	delete(m.nodes, id)
	if banFor > 0 {
		m.banned[id] = time.Now().Add(banFor)
	}
	m.mu.Unlock()

	if sess != nil {
		_ = sess.Close()
	}
	m.log.Warn("node kicked", "node", id, "ban", banFor.String())
	return nil
}

// Banned 返回节点是否处于临时封禁期。
func (m *Manager) Banned(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	until, ok := m.banned[id]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(m.banned, id)
		return false
	}
	return true
}

// Register 绑定节点会话并按新规则列表调整监听，返回该节点自己的规则状态。
// 校验失败会返回 error，此时节点不会生效。
func (m *Manager) Register(id string, sess *yamux.Session, specs []proto.PublishRule) ([]proto.RuleStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateRules(id, specs); err != nil {
		return nil, err
	}

	n, ok := m.nodes[id]
	if !ok {
		n = &node{id: id, rules: map[string]*rule{}}
		m.nodes[id] = n
	} else if n.sess != sess {
		n.closeRules()
	}
	n.sess = sess
	n.specs = append([]proto.PublishRule(nil), specs...)
	n.reconcile()
	return n.statuses(), nil
}

// Statuses 返回节点当前的规则状态（心跳时回包用）。
func (m *Manager) Statuses(id string) []proto.RuleStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[id]
	if !ok {
		return nil
	}
	return n.statuses()
}

// Remove 只在会话仍是当前会话时清理，避免旧连接误删新连接。
func (m *Manager) Remove(id string, sess *yamux.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[id]
	if !ok || n.sess != sess {
		return
	}
	n.closeRules()
	delete(m.nodes, id)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, n := range m.nodes {
		n.closeRules()
		delete(m.nodes, id)
	}
}

func (n *node) reconcile() {
	keep := make(map[string]bool, len(n.specs))
	for _, spec := range n.specs {
		keep[spec.Name] = true
		if r, ok := n.rules[spec.Name]; ok && r.spec == spec {
			continue
		}
		if r, ok := n.rules[spec.Name]; ok {
			r.close()
			delete(n.rules, spec.Name)
		}
		n.rules[spec.Name] = n.start(spec)
	}
	for name, r := range n.rules {
		if !keep[name] {
			r.close()
			delete(n.rules, name)
		}
	}
}

func (n *node) start(spec proto.PublishRule) *rule {
	r := &rule{spec: spec, state: proto.RuleActive}
	addr := fmt.Sprintf("0.0.0.0:%d", spec.Listen)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		r.state = proto.RuleError
		r.msg = err.Error()
		return r
	}
	r.ln = ln
	go n.serve(r)
	return r
}

func (n *node) serve(r *rule) {
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			return
		}
		go n.forward(r, conn)
	}
}

func (n *node) forward(r *rule, conn net.Conn) {
	defer conn.Close()

	stream, err := n.sess.OpenStream()
	if err != nil {
		return
	}
	defer stream.Close()

	if err := proto.WriteStreamHeader(stream, proto.StreamHeader{Rule: r.spec.Name}); err != nil {
		return
	}
	netx.Splice(conn, stream)
}

func (n *node) statuses() []proto.RuleStatus {
	out := make([]proto.RuleStatus, 0, len(n.specs))
	for _, spec := range n.specs {
		st := proto.RuleStatus{
			Name:   spec.Name,
			Proto:  spec.Proto,
			Listen: spec.Listen,
			Target: spec.Target,
			State:  proto.RuleError,
		}
		if r, ok := n.rules[spec.Name]; ok {
			st.State = r.state
			st.Message = r.msg
		}
		out = append(out, st)
	}
	return out
}

func (n *node) closeRules() {
	for name, r := range n.rules {
		r.close()
		delete(n.rules, name)
	}
}

func (r *rule) close() {
	if r.ln != nil {
		_ = r.ln.Close()
		r.ln = nil
	}
}
