// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

// Package nodepass 管理服务端「按节点独立」的接入密码。
//
// 与全局共享的接入密码（<配置>.join）不同，每个节点可以拥有自己的一套密码：
//   - 管理员先为节点随机生成密码，用户拿到后再接入；
//   - 已接入过的节点可以随时重置密码；
//   - 没有独立密码的节点走全局密码，是否允许由 allow_default 开关控制。
//
// 数据保存在 <配置>.nodepass.json（0600）。文件中只保存密码的本地哈希（seed），
// 不保存明文；明文只在「随机生成」时返回给管理员一次。
package nodepass

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mtun/internal/secret"
)

var (
	// ErrPasswordMismatch 表示本次连接使用的密码与节点要求的不一致。
	ErrPasswordMismatch = errors.New("接入密码不正确")
	// ErrDefaultDisabled 表示节点未设置独立密码，且服务端已关闭默认密码接入。
	ErrDefaultDisabled = errors.New("该节点需要专属接入密码，请联系管理员获取")
)

// Entry 是单个节点的独立接入密码记录，只保存哈希。
type Entry struct {
	Seed      string    `json:"seed"`
	UpdatedAt time.Time `json:"updated_at"`
}

type fileData struct {
	// AllowDefault 用指针区分「字段缺失」与「显式 false」：
	// 旧版本没有该文件或字段时应当继续允许默认密码。
	AllowDefault *bool `json:"allow_default,omitempty"`
	// AllowRequest 控制是否允许没有密码的客户端提交「申请接入」，默认开启。
	AllowRequest *bool            `json:"allow_request,omitempty"`
	Nodes        map[string]Entry `json:"nodes,omitempty"`
}

// Store 是并发安全的节点密码集合，变更后原子写回磁盘。
type Store struct {
	mu           sync.Mutex
	path         string
	allowDefault bool
	allowRequest bool
	nodes        map[string]Entry
}

// Load 读取节点密码文件。文件不存在或 path 为空时返回空集合，并默认允许默认密码。
func Load(path string) (*Store, error) {
	s := &Store{path: path, allowDefault: true, allowRequest: true, nodes: map[string]Entry{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	var f fileData
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析节点接入密码失败: %w", err)
	}
	if f.AllowDefault != nil {
		s.allowDefault = *f.AllowDefault
	}
	if f.AllowRequest != nil {
		s.allowRequest = *f.AllowRequest
	}
	if f.Nodes != nil {
		s.nodes = f.Nodes
	}
	return s, nil
}

// Get 返回节点密码记录。
func (s *Store) Get(nodeID string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.nodes[strings.TrimSpace(nodeID)]
	return e, ok
}

// List 返回全部节点密码记录副本。
func (s *Store) List() map[string]Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Entry, len(s.nodes))
	for k, v := range s.nodes {
		out[k] = v
	}
	return out
}

// AllowDefault 返回是否允许未设置独立密码的节点使用默认密码接入。
func (s *Store) AllowDefault() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowDefault
}

// SetAllowDefault 切换默认密码接入开关。
func (s *Store) SetAllowDefault(allow bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.allowDefault
	s.allowDefault = allow
	if err := s.saveLocked(); err != nil {
		s.allowDefault = prev
		return err
	}
	return nil
}

// AllowRequest 返回是否允许无密码客户端提交接入申请。
func (s *Store) AllowRequest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowRequest
}

// SetAllowRequest 切换「允许无密码申请接入」开关。
func (s *Store) SetAllowRequest(allow bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.allowRequest
	s.allowRequest = allow
	if err := s.saveLocked(); err != nil {
		s.allowRequest = prev
		return err
	}
	return nil
}

// Set 为节点指定接入密码，仅保存密码哈希。
func (s *Store) Set(nodeID, password string) (Entry, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return Entry{}, errors.New("缺少节点 ID")
	}
	if err := ValidatePassword(password); err != nil {
		return Entry{}, err
	}
	return s.setSeed(nodeID, secret.Seed(password))
}

// SetRandom 为节点生成一个随机接入密码并保存其哈希。
// 返回的明文密码只在此刻出现一次，之后无法还原。
func (s *Store) SetRandom(nodeID string) (string, Entry, error) {
	pw, err := Generate()
	if err != nil {
		return "", Entry{}, err
	}
	entry, err := s.Set(nodeID, pw)
	if err != nil {
		return "", Entry{}, err
	}
	return pw, entry, nil
}

// Clear 删除节点的独立密码，使其回落到默认密码策略。
func (s *Store) Clear(nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("缺少节点 ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.nodes[nodeID]
	if !had {
		return nil
	}
	delete(s.nodes, nodeID)
	if err := s.saveLocked(); err != nil {
		s.nodes[nodeID] = prev
		return err
	}
	return nil
}

// CandidateSeeds 返回 TLS 层可接受的哈希集合：
// 允许默认密码时包含 defaultSeed，另外始终包含所有节点密码哈希。
func (s *Store) CandidateSeeds(defaultSeed string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]struct{}, len(s.nodes)+1)
	out := make([]string, 0, len(s.nodes)+1)
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if s.allowDefault {
		add(defaultSeed)
	}
	for _, e := range s.nodes {
		add(e.Seed)
	}
	return out
}

// Authorize 判定某节点能否用 usedSeed 接入。
// usedSeed 是服务端从 TLS 握手中反查到的实际哈希，因此可信。
func (s *Store) Authorize(nodeID, usedSeed, defaultSeed string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("缺少节点 ID")
	}
	s.mu.Lock()
	entry, has := s.nodes[nodeID]
	allowDefault := s.allowDefault
	s.mu.Unlock()

	if has {
		// 有独立密码时必须以它为准，忽略默认密码开关。
		if !secret.Equal(entry.Seed, usedSeed) {
			return ErrPasswordMismatch
		}
		return nil
	}
	if !allowDefault {
		return ErrDefaultDisabled
	}
	if !secret.Equal(defaultSeed, usedSeed) {
		return ErrPasswordMismatch
	}
	return nil
}

// Generate 生成 32 位 base32 随机密码（仅含 A-Z2-7，便于抄写）。
func Generate() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机密码失败: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// ValidatePassword 校验节点接入密码长度。
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("接入密码至少需要 8 位")
	}
	if len(password) > 128 {
		return errors.New("接入密码不能超过 128 位")
	}
	return nil
}

func (s *Store) setSeed(nodeID, seed string) (Entry, error) {
	e := Entry{Seed: seed, UpdatedAt: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.nodes[nodeID]
	s.nodes[nodeID] = e
	if err := s.saveLocked(); err != nil {
		if had {
			s.nodes[nodeID] = prev
		} else {
			delete(s.nodes, nodeID)
		}
		return Entry{}, err
	}
	return e, nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	allow := s.allowDefault
	allowReq := s.allowRequest
	data, err := json.MarshalIndent(fileData{AllowDefault: &allow, AllowRequest: &allowReq, Nodes: s.nodes}, "", "  ")
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
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
