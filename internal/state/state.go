package state

import (
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
	for {
		if !r.prefix.Contains(ip) {
			ip = r.prefix.Addr().Next()
		}
		used := false
		for _, n := range r.nodes {
			if n.OverlayIP == ip.String() {
				used = true
				break
			}
		}
		if !used {
			break
		}
		ip = ip.Next()
	}
	r.nextIP = ip.Next()
	return ip.String()
}
