// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

// Package pending 保存服务端对离线节点尚未同步的规则变更。
package pending

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"mtun/pkg/proto"
)

// Op 是单条规则操作。
type Op struct {
	Op   string            `json:"op"`
	Rule proto.PublishRule `json:"rule"`
}

type nodeQueue struct {
	Rev int  `json:"rev"`
	Ops []Op `json:"ops"`
}

type fileData struct {
	Nodes map[string]nodeQueue `json:"nodes"`
}

// Store 是并发安全的待同步队列，变更后原子写回 <配置>.rules.json。
type Store struct {
	mu    sync.Mutex
	path  string
	nodes map[string]nodeQueue
}

// Load 读取队列文件，文件不存在或 path 为空时返回空队列。
func Load(path string) (*Store, error) {
	s := &Store{path: path, nodes: map[string]nodeQueue{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f fileData
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析待同步规则失败: %w", err)
	}
	if f.Nodes != nil {
		s.nodes = f.Nodes
	}
	return s, nil
}

// Add 追加一条操作。
func (s *Store) Add(nodeID string, op Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.nodes[nodeID]
	q.Rev++
	q.Ops = append(q.Ops, op)
	s.nodes[nodeID] = q
	return s.saveLocked()
}

// Peek 返回节点待同步操作副本。
func (s *Store) Peek(nodeID string) []Op {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.nodes[nodeID]
	return append([]Op(nil), q.Ops...)
}

// Take 取出并清空节点待同步操作。
func (s *Store) Take(nodeID string) []Op {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.nodes[nodeID]
	if !ok {
		return nil
	}
	delete(s.nodes, nodeID)
	if err := s.saveLocked(); err != nil {
		// 写盘失败时保留内存队列，等待下次重试。
		s.nodes[nodeID] = q
	}
	return append([]Op(nil), q.Ops...)
}

// Count 返回节点待同步操作数量。
func (s *Store) Count(nodeID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.nodes[nodeID].Ops)
}

// Counts 返回所有节点的待同步数量。
func (s *Store) Counts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.nodes))
	for id, q := range s.nodes {
		out[id] = len(q.Ops)
	}
	return out
}

// Apply 按顺序把操作应用到规则列表，返回新列表与失败原因。
func Apply(rules []proto.PublishRule, ops []Op) ([]proto.PublishRule, []error) {
	out := append([]proto.PublishRule(nil), rules...)
	var errs []error
	for _, op := range ops {
		switch op.Op {
		case "add":
			if findRule(out, op.Rule.Name) >= 0 {
				errs = append(errs, fmt.Errorf("规则 %q 已存在", op.Rule.Name))
				continue
			}
			out = append(out, op.Rule)
		case "update":
			i := findRule(out, op.Rule.Name)
			if i < 0 {
				errs = append(errs, fmt.Errorf("规则 %q 不存在", op.Rule.Name))
				continue
			}
			out[i] = op.Rule
		case "delete":
			i := findRule(out, op.Rule.Name)
			if i < 0 {
				errs = append(errs, fmt.Errorf("规则 %q 不存在", op.Rule.Name))
				continue
			}
			out = append(out[:i], out[i+1:]...)
		default:
			errs = append(errs, fmt.Errorf("未知操作 %q", op.Op))
		}
	}
	return out, errs
}

func findRule(rules []proto.PublishRule, name string) int {
	for i := range rules {
		if rules[i].Name == name {
			return i
		}
	}
	return -1
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(fileData{Nodes: s.nodes}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
