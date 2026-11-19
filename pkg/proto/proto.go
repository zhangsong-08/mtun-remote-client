// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package proto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	TypeRegister  = "register"
	TypeHeartbeat = "heartbeat"
	TypeStatus    = "status"
	TypeError     = "error"
	// TypePeers 由服务端推送给客户端：其他节点及其对本节点可见的规则。
	TypePeers = "peers"
	// TypeAclUpdate 由服务端下发给规则所属客户端：修改某条规则的可见范围。
	TypeAclUpdate = "acl_update"
)

// 规则可见范围。空值与 VisibleAll 等价，兼容旧配置。
const (
	VisibleAll   = "all"   // 所有客户端可见
	VisibleSelf  = "self"  // 仅规则所属客户端可见
	VisibleNodes = "nodes" // 仅 VisibleTo 中列出的节点可见
)

// Error 表示服务端拒绝或错误提示（如节点被临时封禁）。
type Error struct {
	Message string `json:"message"`
}

// 规则状态：由服务端本地监听结果决定，客户端只看到自己的规则。
const (
	RuleActive  = "active"
	RuleError   = "error"
	RulePending = "pending"
)

// Envelope 是控制流上的 JSON 消息封装。控制流本身由 TLS 1.3 保护。
type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Register struct {
	NodeID  string        `json:"node_id"`
	Version string        `json:"version"`
	Path    string        `json:"path,omitempty"`
	Publish []PublishRule `json:"publish,omitempty"`
}

type Heartbeat struct {
	NodeID string `json:"node_id"`
	Path   string `json:"path,omitempty"`
}

type PublishRule struct {
	Name   string `json:"name" yaml:"name"`
	Proto  string `json:"proto" yaml:"proto"`
	Listen int    `json:"listen" yaml:"listen"`
	Target string `json:"target" yaml:"target"`
	// Visibility 控制哪些其他客户端可以在“网络”中看到这条规则：
	// all（默认）、self（仅自己）、nodes（仅 VisibleTo 中的节点）。
	Visibility string `json:"visibility,omitempty" yaml:"visibility,omitempty"`
	// VisibleTo 仅在 Visibility=nodes 时生效，保存可见该规则的节点 ID。
	VisibleTo []string `json:"visible_to,omitempty" yaml:"visible_to,omitempty"`
}

// VisibilityMode 把空值归一为 all，避免旧配置出现未知模式。
func (r PublishRule) VisibilityMode() string {
	switch r.Visibility {
	case VisibleSelf, VisibleNodes:
		return r.Visibility
	default:
		return VisibleAll
	}
}

// VisibleToNode 判断某节点能否看到这条规则；规则所有节点自己始终可见。
func (r PublishRule) VisibleToNode(nodeID string, isOwner bool) bool {
	if isOwner {
		return true
	}
	switch r.VisibilityMode() {
	case VisibleSelf:
		return false
	case VisibleNodes:
		for _, id := range r.VisibleTo {
			if id == nodeID {
				return true
			}
		}
		return false
	default:
		return true
	}
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

type SelfInfo struct {
	NodeID    string `json:"node_id"`
	OverlayIP string `json:"overlay_ip"`
	Path      string `json:"path"`
}

// Status 是服务端对单个节点的回执，只包含该节点自身的信息。
type Status struct {
	Self  SelfInfo     `json:"self"`
	Rules []RuleStatus `json:"rules"`
}

// PeerRule 是共享给其他客户端的规则视图，不包含内网目标。
type PeerRule struct {
	Name   string `json:"name"`
	Proto  string `json:"proto"`
	Listen int    `json:"listen"`
	State  string `json:"state,omitempty"`
}

// PeerNode 是其他客户端在“网络”页看到的节点信息。
type PeerNode struct {
	NodeID    string     `json:"node_id"`
	Version   string     `json:"version,omitempty"`
	OverlayIP string     `json:"overlay_ip"`
	Path      string     `json:"path"`
	Online    bool       `json:"online"`
	Rules     []PeerRule `json:"rules"`
}

// Peers 是服务端按请求者过滤后推送的节点与共享规则。
type Peers struct {
	Nodes []PeerNode `json:"nodes"`
}

// AclUpdate 是服务端要求客户端修改某条规则的可见范围并落盘。
type AclUpdate struct {
	Name       string   `json:"name"`
	Visibility string   `json:"visibility"`
	VisibleTo  []string `json:"visible_to,omitempty"`
}

// StreamHeader 是数据流的前置头，用来告诉客户端该连接命中的规则。
type StreamHeader struct {
	Rule string `json:"rule"`
}

func Send(enc *json.Encoder, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return enc.Encode(Envelope{Type: kind, Payload: raw})
}

func Receive(dec *json.Decoder) (Envelope, error) {
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return env, err
	}
	if env.Type == "" {
		return env, fmt.Errorf("控制消息缺少 type")
	}
	return env, nil
}

// WriteStreamHeader 写入 4 字节大端长度 + JSON 头，避免 json.Decoder 读穿后续数据。
func WriteStreamHeader(w io.Writer, header StreamHeader) error {
	data, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > 4096 {
		return fmt.Errorf("流头长度非法: %d", len(data))
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(data)))
	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func ReadStreamHeader(r io.Reader) (StreamHeader, error) {
	var header StreamHeader
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return header, err
	}
	n := binary.BigEndian.Uint32(buf[:])
	if n == 0 || n > 4096 {
		return header, fmt.Errorf("流头长度非法: %d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return header, err
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return header, err
	}
	if header.Rule == "" {
		return header, fmt.Errorf("流头缺少规则名")
	}
	return header, nil
}
