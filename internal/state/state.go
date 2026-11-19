package state

import (
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"mtun/pkg/proto"
)

// onlineWindow 内收到心跳的节点视为在线。TCP 断开后最多 30 秒转为离线。
const onlineWindow = 30 * time.Second

type Node struct {
	ID        string
	OverlayIP string
	PublicKey string
	Path      string
	Endpoints []string
	Publish   []proto.PublishRule
	LastSeen  time.Time
	Online    bool
}

type Update struct {
	ID        string
	PublicKey string
	Path      string
	Endpoints []string
	Publish   []proto.PublishRule
	Now       time.Time
}

type Stats struct {
	Total        int // 在线节点数
	Offline      int
	Direct       int
	Relay        int
	PublishRules int
	Uptime       time.Duration
}

// Registry 是服务端的内存节点表。正式版替换为持久化存储。
type Registry struct {
	mu      sync.RWMutex
	nodes   map[string]*Node
	usedIPs map[string]struct{} // O(1) IP 占用检查
	prefix  netip.Prefix
	nextIP  netip.Addr
	started time.Time
}

func New(serverIP, cidr string) *Registry {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		prefix = netip.MustParsePrefix("100.64.0.0/10")
	}
	base, err := netip.ParseAddr(serverIP)
	if err != nil {
		base = netip.MustParseAddr("100.64.0.1")
	}
	return &Registry{
		nodes:   map[string]*Node{},
		usedIPs: map[string]struct{}{base.String(): {}},
		prefix:  prefix,
		nextIP:  base.Next(),
		started: time.Now(),
	}
}

func (r *Registry) Upsert(in Update) {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	n, ok := r.nodes[in.ID]
	if !ok {
		n = &Node{ID: in.ID, OverlayIP: r.allocateLocked()}
		r.nodes[in.ID] = n
	}
	if in.PublicKey != "" {
		n.PublicKey = in.PublicKey
	}
	if in.Path != "" {
		n.Path = in.Path
	} else if n.Path == "" {
		n.Path = "relay"
	}
	if len(in.Endpoints) > 0 {
		n.Endpoints = append([]string(nil), in.Endpoints...)
	}
	if in.Publish != nil {
		n.Publish = append([]proto.PublishRule(nil), in.Publish...)
	}
	n.LastSeen = in.Now
}

func (r *Registry) Get(id string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n, ok := r.nodes[id]
	if !ok {
		return Node{}, false
	}
	return r.copyLocked(n), true
}

// MarkOffline 立即把节点标记为离线（强制下线后使用）。
func (r *Registry) MarkOffline(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		n.LastSeen = time.Now().Add(-onlineWindow - time.Second)
	}
}

// List 返回全部节点（含离线），供服务端面板使用。
func (r *Registry) List() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, r.copyLocked(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// SetRuleVisibility 更新某节点某条规则的可见范围（服务端管理员操作）。
func (r *Registry) SetRuleVisibility(id, name, visibility string, visibleTo []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, ok := r.nodes[id]
	if !ok {
		return fmt.Errorf("节点 %s 不存在", id)
	}
	for i := range n.Publish {
		if n.Publish[i].Name == name {
			n.Publish[i].Visibility = visibility
			n.Publish[i].VisibleTo = append([]string(nil), visibleTo...)
			return nil
		}
	}
	return fmt.Errorf("节点 %s 没有规则 %q", id, name)
}

// PeersFor 返回请求者可见的其他节点及其共享规则。
// 规则按各自的可见范围过滤；所有节点本身始终可见，便于互相发现。
func (r *Registry) PeersFor(nodeID string) []proto.PeerNode {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	out := make([]proto.PeerNode, 0, len(r.nodes))
	for id, n := range r.nodes {
		if id == nodeID {
			continue
		}
		online := now.Sub(n.LastSeen) <= onlineWindow
		pn := proto.PeerNode{
			NodeID:    n.ID,
			OverlayIP: n.OverlayIP,
			Path:      n.Path,
			Online:    online,
			Rules:     make([]proto.PeerRule, 0, len(n.Publish)),
		}
		for _, rule := range n.Publish {
			if !rule.VisibleToNode(nodeID, false) {
				continue
			}
			st := proto.RuleActive
			if !online {
				st = "offline"
			}
			pn.Rules = append(pn.Rules, proto.PeerRule{
				Name:   rule.Name,
				Proto:  rule.Proto,
				Listen: rule.Listen,
				State:  st,
			})
		}
		out = append(out, pn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

func (r *Registry) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	st := Stats{Uptime: time.Since(r.started)}
	now := time.Now()
	for _, n := range r.nodes {
		if now.Sub(n.LastSeen) > onlineWindow {
			st.Offline++
			continue
		}
		st.Total++
		if n.Path == "direct" {
			st.Direct++
		} else {
			st.Relay++
		}
		st.PublishRules += len(n.Publish)
	}
	return st
}

func (r *Registry) copyLocked(n *Node) Node {
	cp := *n
	cp.Endpoints = append([]string(nil), n.Endpoints...)
	cp.Publish = append([]proto.PublishRule(nil), n.Publish...)
	cp.Online = time.Since(n.LastSeen) <= onlineWindow
	return cp
}

func (r *Registry) allocateLocked() string {
	ip := r.nextIP
	for i := 0; i < 4096; i++ { // 最多扫描 4096 次，避免死循环
		if !r.prefix.Contains(ip) {
			ip = r.prefix.Addr()
		}
		if _, used := r.usedIPs[ip.String()]; !used {
			break
		}
		ip = ip.Next()
	}
	s := ip.String()
	r.usedIPs[s] = struct{}{}
	r.nextIP = ip.Next()
	return s
}
