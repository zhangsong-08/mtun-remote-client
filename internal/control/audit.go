// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package control

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"mtun/pkg/proto"
)

// passwordAlert 记录「某节点独立密码被另一台设备使用」的事件。
// 客户端不会被强制下线，只在服务端面板提示。
type passwordAlert struct {
	Key      string    `json:"key"`
	Owner    string    `json:"owner"`
	UsedBy   string    `json:"used_by"`
	Count    int       `json:"count"`
	FirstAt  time.Time `json:"first_at"`
	LastAt   time.Time `json:"last_at"`
	Synced   int       `json:"synced_rules"`
	LastNote string    `json:"last_note,omitempty"`
}

// PasswordAlertView 供控制台展示。
type PasswordAlertView struct {
	Key      string    `json:"key"`
	Owner    string    `json:"owner"`
	UsedBy   string    `json:"used_by"`
	Count    int       `json:"count"`
	FirstAt  time.Time `json:"first_at"`
	LastAt   time.Time `json:"last_at"`
	Synced   int       `json:"synced_rules"`
	LastNote string    `json:"last_note,omitempty"`
}

// NodePasswordPlain 返回节点独立密码的明文（控制台查看用）。
func (s *Server) NodePasswordPlain(nodeID string) (string, bool) {
	return s.passwords.Password(nodeID)
}

// noteSeedUse 检测「同一密码被不同设备使用」。
// 返回是否命中复用、密码所属节点，以及为同步配置需要下发的规则。
// 仅对「节点独立密码」告警：默认密码本身就是多设备共用，不视为异常。
func (s *Server) noteSeedUse(seed, nodeID string, incoming []proto.PublishRule) (bool, string, []proto.PublishRule) {
	owner := s.passwords.OwnerOfSeed(seed)
	if owner == "" || owner == nodeID {
		// 默认密码，或密码所有者本人，正常接入。
		s.auditMu.Lock()
		if s.seedUses[seed] == nil {
			s.seedUses[seed] = map[string]time.Time{}
		}
		s.seedUses[seed][nodeID] = time.Now()
		s.auditMu.Unlock()
		return false, owner, nil
	}

	now := time.Now()
	key := alertKey(seed, owner, nodeID)

	s.auditMu.Lock()
	if s.seedUses[seed] == nil {
		s.seedUses[seed] = map[string]time.Time{}
	}
	s.seedUses[seed][nodeID] = now
	a, ok := s.alerts[key]
	if !ok {
		a = &passwordAlert{Key: key, Owner: owner, UsedBy: nodeID, FirstAt: now}
		s.alerts[key] = a
	}
	a.Count++
	a.LastAt = now
	count := a.Count
	s.auditMu.Unlock()

	s.log.Warn("检测到同一接入密码被不同设备使用（客户端保持在线）",
		"owner", owner, "used_by", nodeID, "count", count)

	// 换设备时把原节点的转发规则同步给新设备，实现「按密码同步配置」。
	// 原节点离线时没有活动监听，退回注册表里最后一次上报的规则。
	var sync []proto.PublishRule
	if len(incoming) == 0 {
		sync = s.tunnels.RuleSpecs(owner)
		if len(sync) == 0 {
			if n, ok := s.reg.Get(owner); ok {
				sync = n.Publish
			}
		}
	}
	s.auditMu.Lock()
	if cur, ok := s.alerts[key]; ok {
		cur.Synced = len(sync)
	}
	s.auditMu.Unlock()
	return true, owner, sync
}

// devicesForSeed 返回使用过该密码哈希的其他节点（排除 exclude）。
// 这些节点共享同一个「逻辑设备」，公网端口由最新接入的设备接管。
func (s *Server) devicesForSeed(seed, exclude string) []string {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	out := make([]string, 0, len(s.seedUses[seed]))
	for id := range s.seedUses[seed] {
		if id != exclude {
			out = append(out, id)
		}
	}
	return out
}

// PasswordAlerts 返回全部密码复用告警（按最近时间倒序）。
func (s *Server) PasswordAlerts() []PasswordAlertView {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	out := make([]PasswordAlertView, 0, len(s.alerts))
	for _, a := range s.alerts {
		out = append(out, PasswordAlertView{
			Key: a.Key, Owner: a.Owner, UsedBy: a.UsedBy, Count: a.Count,
			FirstAt: a.FirstAt, LastAt: a.LastAt, Synced: a.Synced, LastNote: a.LastNote,
		})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].LastAt.Before(out[j].LastAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ClearPasswordAlerts 清空告警列表（不影响客户端连接）。
func (s *Server) ClearPasswordAlerts(key string) int {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if key != "" {
		delete(s.alerts, key)
		return 1
	}
	n := len(s.alerts)
	s.alerts = map[string]*passwordAlert{}
	return n
}

// alertKey 用哈希生成稳定 key，避免把密码哈希直接带到前端。
func alertKey(seed, owner, usedBy string) string {
	sum := sha256.Sum256([]byte(seed + "|" + owner + "|" + usedBy))
	return hex.EncodeToString(sum[:6])
}
