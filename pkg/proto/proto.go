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
}

type RuleStatus struct {
	Name    string `json:"name"`
	Proto   string `json:"proto"`
	Listen  int    `json:"listen"`
	Target  string `json:"target"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
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
