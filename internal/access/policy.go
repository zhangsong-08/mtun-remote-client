// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

// Package access 保存服务端的节点访问策略（封禁、时段、IP 段、系统限制）。
package access

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Window 是每日允许接入的时段，格式为 HH:MM；Start 大于 End 时表示跨午夜。
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Policy 描述单个节点的接入限制，零值表示不限制。
type Policy struct {
	Permanent bool      `json:"permanent,omitempty"`
	Until     time.Time `json:"until,omitempty"`
	Daily     []Window  `json:"daily,omitempty"`
	AllowCIDR []string  `json:"allow_cidrs,omitempty"`
	DenyCIDR  []string  `json:"deny_cidrs,omitempty"`
	AllowOS   []string  `json:"allow_os,omitempty"`
}

type fileData struct {
	Nodes map[string]Policy `json:"nodes"`
}

// Store 是并发安全的策略集合，变更后原子写回 <配置>.access.json。
type Store struct {
	mu    sync.Mutex
	path  string
	nodes map[string]Policy
}

// Load 读取策略文件，文件不存在或 path 为空时返回空集合。
func Load(path string) (*Store, error) {
	s := &Store{path: path, nodes: map[string]Policy{}}
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
		return nil, fmt.Errorf("解析访问策略失败: %w", err)
	}
	if f.Nodes != nil {
		s.nodes = f.Nodes
	}
	return s, nil
}

// Get 返回节点策略副本。
func (s *Store) Get(id string) Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nodes[id]
}

// Set 校验并保存节点策略。
func (s *Store) Set(id string, p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[id] = p
	return s.saveLocked()
}

// Clear 删除节点策略。
func (s *Store) Clear(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[id]; !ok {
		return nil
	}
	delete(s.nodes, id)
	return s.saveLocked()
}

// List 返回全部策略副本。
func (s *Store) List() map[string]Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Policy, len(s.nodes))
	for k, v := range s.nodes {
		out[k] = v
	}
	return out
}

// Validate 校验时段、CIDR 与系统名。
func (p *Policy) Validate() error {
	for _, w := range p.Daily {
		if _, _, err := parseWindow(w); err != nil {
			return err
		}
	}
	for _, c := range p.AllowCIDR {
		if _, err := parsePrefix(c); err != nil {
			return fmt.Errorf("允许的 IP 段 %q 不合法", c)
		}
	}
	for _, c := range p.DenyCIDR {
		if _, err := parsePrefix(c); err != nil {
			return fmt.Errorf("拒绝的 IP 段 %q 不合法", c)
		}
	}
	for _, o := range p.AllowOS {
		if !slices.Contains([]string{"windows", "linux", "darwin"}, o) {
			return fmt.Errorf("不支持的系统 %q", o)
		}
	}
	return nil
}

// Check 判断节点当前是否允许接入。
func (s *Store) Check(id, osName, ip string, now time.Time) error {
	p := s.Get(id)
	if p.Permanent {
		return fmt.Errorf("节点已被永久封禁")
	}
	if !p.Until.IsZero() && now.Before(p.Until) {
		return fmt.Errorf("节点已被封禁，解封时间 %s", p.Until.Local().Format("2006-01-02 15:04"))
	}
	if len(p.Daily) > 0 {
		ok, err := inWindows(p.Daily, now)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("当前时间不在允许接入时段")
		}
	}
	addr, err := netip.ParseAddr(strings.Split(strings.TrimSpace(ip), "%")[0])
	if err != nil {
		return fmt.Errorf("无法识别来源 IP %q", ip)
	}
	for _, c := range p.DenyCIDR {
		prefix, err := parsePrefix(c)
		if err != nil {
			return err
		}
		if prefix.Contains(addr) {
			return fmt.Errorf("来源 IP %s 已被拒绝", ip)
		}
	}
	if len(p.AllowCIDR) > 0 {
		allowed := false
		for _, c := range p.AllowCIDR {
			prefix, err := parsePrefix(c)
			if err != nil {
				return err
			}
			if prefix.Contains(addr) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("来源 IP %s 不在允许范围内", ip)
		}
	}
	if len(p.AllowOS) > 0 {
		if osName == "" {
			return fmt.Errorf("客户端未上报系统信息，请升级客户端")
		}
		if !slices.Contains(p.AllowOS, osName) {
			return fmt.Errorf("客户端系统 %s 不允许接入", osName)
		}
	}
	return nil
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

func parseWindow(w Window) (start, end int, err error) {
	start, err = parseClock(w.Start)
	if err != nil {
		return 0, 0, fmt.Errorf("时段开始时间 %q 不合法", w.Start)
	}
	end, err = parseClock(w.End)
	if err != nil {
		return 0, 0, fmt.Errorf("时段结束时间 %q 不合法", w.End)
	}
	return start, end, nil
}

func parseClock(v string) (int, error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("时间格式应为 HH:MM")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("时间格式应为 HH:MM")
	}
	return h*60 + m, nil
}

func inWindows(windows []Window, now time.Time) (bool, error) {
	cur := now.Hour()*60 + now.Minute()
	for _, w := range windows {
		start, end, err := parseWindow(w)
		if err != nil {
			return false, err
		}
		switch {
		case start == end:
			return true, nil
		case start < end:
			if cur >= start && cur < end {
				return true, nil
			}
		default: // 跨午夜
			if cur >= start || cur < end {
				return true, nil
			}
		}
	}
	return false, nil
}

func parsePrefix(v string) (netip.Prefix, error) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "/") {
		return netip.ParsePrefix(v)
	}
	addr, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
