package tunnel

import (
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"mtun/internal/netx"
	"mtun/pkg/proto"
)

// Manager 在服务端为每个客户端的每条规则监听公网端口。
// 外部连接到来时通过该客户端自己的 yamux 会话开一条数据流。
type Manager struct {
	log    *slog.Logger
	mu     sync.Mutex
	nodes  map[string]*node
	banned map[string]time.Time
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

func NewManager(log *slog.Logger) *Manager {
	return &Manager{log: log, nodes: map[string]*node{}, banned: map[string]time.Time{}}
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
func (m *Manager) Register(id string, sess *yamux.Session, specs []proto.PublishRule) []proto.RuleStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	return n.statuses()
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
