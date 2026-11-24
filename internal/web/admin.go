// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mtun/internal/access"
	"mtun/internal/config"
	"mtun/internal/pending"
	"mtun/internal/webx"
	"mtun/pkg/proto"
)

var (
	errPINNotSet = errors.New("尚未设置 PIN，请先在安全设置中设置")
	errPINLocked = errors.New("PIN 连续错误，已临时锁定")
)

const (
	pinMaxFailures = 5
	pinLockWindow  = 60 * time.Second
)

func (s *Server) pinPath() string {
	return s.cfgPath + ".pin"
}

// requirePIN 校验敏感操作 PIN，并处理失败锁定。
func (s *Server) requirePIN(pin string) error {
	s.pinMu.Lock()
	defer s.pinMu.Unlock()

	if !webx.PINConfigured(s.pinPath()) {
		return errPINNotSet
	}
	if now := time.Now(); now.Before(s.pinLockedTo) {
		return fmt.Errorf("%w，请 %d 秒后重试", errPINLocked, int(s.pinLockedTo.Sub(now).Seconds())+1)
	}
	ok, err := webx.VerifyPINFile(s.pinPath(), pin)
	if err != nil {
		return err
	}
	if !ok {
		s.pinFailures++
		if s.pinFailures >= pinMaxFailures {
			s.pinFailures = 0
			s.pinLockedTo = time.Now().Add(pinLockWindow)
			return fmt.Errorf("%w，请 %d 秒后重试", errPINLocked, int(pinLockWindow.Seconds()))
		}
		return fmt.Errorf("PIN 错误，还可尝试 %d 次", pinMaxFailures-s.pinFailures)
	}
	s.pinFailures = 0
	return nil
}

func (s *Server) writePINError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPINNotSet):
		webx.WriteJSONStatus(w, http.StatusConflict, map[string]any{"error": err.Error(), "need_setup": true})
	case errors.Is(err, errPINLocked):
		webx.WriteJSONStatus(w, http.StatusLocked, map[string]any{"error": err.Error(), "retry_after": int(pinLockWindow.Seconds())})
	default:
		webx.WriteError(w, http.StatusUnauthorized, err)
	}
}

// handlePIN 查询、首次设置或修改敏感操作 PIN。
func (s *Server) handlePIN(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		webx.WriteJSON(w, map[string]any{"set": webx.PINConfigured(s.pinPath())})
	case http.MethodPost:
		if !isJSON(r) {
			webx.WriteError(w, http.StatusUnsupportedMediaType, errors.New("请求需为 application/json"))
			return
		}
		var req struct {
			CurrentPIN string `json:"current_pin"`
			PIN        string `json:"pin"`
			Confirm    string `json:"confirm"`
		}
		if err := decodeJSONBody(r, &req); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
		if req.PIN != req.Confirm {
			webx.WriteError(w, http.StatusBadRequest, errors.New("两次输入的 PIN 不一致"))
			return
		}
		configured := webx.PINConfigured(s.pinPath())
		if configured {
			if err := s.requirePIN(strings.TrimSpace(req.CurrentPIN)); err != nil {
				s.writePINError(w, err)
				return
			}
		}
		if err := webx.SetPINFile(s.pinPath(), strings.TrimSpace(req.PIN)); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
		s.pinMu.Lock()
		s.pinFailures = 0
		s.pinLockedTo = time.Time{}
		s.pinMu.Unlock()
		s.log.Info("敏感操作 PIN 已更新")
		webx.WriteJSON(w, map[string]any{"saved": true, "set": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handlePanelSecure 修改面板端口、接入端口、Web 密码与接入密码，需 PIN 二次验证。
func (s *Server) handlePanelSecure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !isJSON(r) {
		webx.WriteError(w, http.StatusUnsupportedMediaType, errors.New("请求需为 application/json"))
		return
	}
	var req struct {
		PIN           string `json:"pin"`
		Dashboard     string `json:"dashboard"`
		Control       string `json:"control"`
		AdminPassword string `json:"admin_password"`
		JoinToken     string `json:"join_token"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.requirePIN(strings.TrimSpace(req.PIN)); err != nil {
		s.writePINError(w, err)
		return
	}

	dashboard := strings.TrimSpace(req.Dashboard)
	control := strings.TrimSpace(req.Control)
	adminPassword := req.AdminPassword
	joinToken := strings.TrimSpace(req.JoinToken)

	if dashboard != "" {
		if err := config.ValidateDashboard(dashboard); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
	}
	if control != "" {
		if err := config.ValidateControl(control); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
	}
	if adminPassword != "" {
		if err := webx.ValidatePassword(adminPassword); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
	}
	if joinToken != "" {
		if err := config.ValidateJoinPassword(joinToken); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
	}

	restart := false
	configChanged := false
	if dashboard != "" && dashboard != s.cfg.Dashboard {
		s.cfg.Dashboard = dashboard
		restart = true
		configChanged = true
	}
	if control != "" && control != s.cfg.Control {
		s.cfg.Control = control
		restart = true
		configChanged = true
	}
	if joinToken != "" {
		if err := config.SaveJoinPassword(s.cfgPath, joinToken); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
		restart = true
	}
	if configChanged {
		if err := config.Save(s.cfgPath, s.cfg); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
	}

	relogin := false
	if adminPassword != "" {
		if err := webx.SetPasswordFile(s.cfgPath+".passwd", "admin", adminPassword); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
		if s.auth != nil {
			if err := s.auth.SetCredentials("admin", adminPassword); err != nil {
				webx.WriteError(w, http.StatusInternalServerError, err)
				return
			}
		}
		relogin = true
	}

	s.log.Info("敏感设置已更新", "restart", restart, "password_changed", relogin)

	if !restart {
		webx.WriteJSON(w, map[string]any{"saved": true, "relogin": relogin})
		return
	}
	if err := s.restartFn(); err != nil {
		webx.WriteError(w, http.StatusInternalServerError, fmt.Errorf(
			"配置已保存，但自动重启失败：%v；请手动重启服务端", err))
		return
	}
	webx.WriteJSON(w, map[string]any{"saved": true, "restarting": true, "relogin": relogin})
	go func() {
		time.Sleep(time.Second)
		s.exitFn()
	}()
}

// handleRuleChange 服务端对任意节点增删改一条转发规则。
func (s *Server) handleRuleChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.ctl == nil {
		webx.WriteError(w, http.StatusInternalServerError, errors.New("控制面未初始化"))
		return
	}
	if !isJSON(r) {
		webx.WriteError(w, http.StatusUnsupportedMediaType, errors.New("请求需为 application/json"))
		return
	}
	var req struct {
		Node string            `json:"node"`
		Op   string            `json:"op"`
		Rule proto.PublishRule `json:"rule"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	req.Node = strings.TrimSpace(req.Node)
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	req.Rule.Name = strings.TrimSpace(req.Rule.Name)
	if req.Node == "" || req.Rule.Name == "" {
		webx.WriteError(w, http.StatusBadRequest, errors.New("缺少节点 ID 或规则名称"))
		return
	}
	rule := req.Rule
	vis, visibleTo, err := config.NormalizeVisibility(rule.Visibility, rule.VisibleTo)
	if err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	rule.Visibility = vis
	rule.VisibleTo = visibleTo
	switch req.Op {
	case "add", "update":
		if err := config.ValidateRule(rule); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
	case "delete":
		rule = proto.PublishRule{Name: req.Rule.Name}
	default:
		webx.WriteError(w, http.StatusBadRequest, errors.New("op 只能是 add/update/delete"))
		return
	}
	rules, online, err := s.ctl.ApplyRuleChange(req.Node, pending.Op{Op: req.Op, Rule: rule})
	if err != nil {
		webx.WriteError(w, http.StatusConflict, err)
		return
	}
	webx.WriteJSON(w, map[string]any{
		"ok":     true,
		"online": online,
		"queued": !online,
		"rules":  rules,
	})
}

// handleNodeAccess 设置节点封禁与访问限制。
func (s *Server) handleNodeAccess(w http.ResponseWriter, r *http.Request) {
	if s.ctl == nil {
		webx.WriteError(w, http.StatusInternalServerError, errors.New("控制面未初始化"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		webx.WriteJSON(w, map[string]any{"nodes": s.ctl.AccessPolicies()})
		return
	case http.MethodPost:
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !isJSON(r) {
		webx.WriteError(w, http.StatusUnsupportedMediaType, errors.New("请求需为 application/json"))
		return
	}
	var req struct {
		ID         string          `json:"id"`
		BanSeconds int             `json:"ban_seconds"`
		Permanent  bool            `json:"permanent"`
		Daily      []access.Window `json:"daily"`
		AllowCIDRs []string        `json:"allow_cidrs"`
		DenyCIDRs  []string        `json:"deny_cidrs"`
		AllowOS    []string        `json:"allow_os"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		webx.WriteError(w, http.StatusBadRequest, errors.New("缺少节点 ID"))
		return
	}
	if req.BanSeconds < 0 || req.BanSeconds > 365*24*3600 {
		webx.WriteError(w, http.StatusBadRequest, errors.New("封禁时长需在 0 到 365 天之间"))
		return
	}
	policy := access.Policy{
		Permanent: req.Permanent,
		Daily:     cleanWindows(req.Daily),
		AllowCIDR: cleanList(req.AllowCIDRs),
		DenyCIDR:  cleanList(req.DenyCIDRs),
		AllowOS:   cleanList(req.AllowOS),
	}
	if req.Permanent {
		policy.Until = time.Time{}
	} else if req.BanSeconds > 0 {
		policy.Until = time.Now().Add(time.Duration(req.BanSeconds) * time.Second)
	}
	if err := s.ctl.SetAccessPolicy(req.ID, policy); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	disconnected := false
	if policy.Permanent || !policy.Until.IsZero() {
		if err := s.tunnels.Kick(req.ID, 0); err == nil {
			s.reg.MarkOffline(req.ID)
			disconnected = true
		}
	}
	s.log.Info("节点访问策略已更新", "node", req.ID, "permanent", policy.Permanent, "until", policy.Until)
	webx.WriteJSON(w, map[string]any{"ok": true, "policy": policy, "disconnected": disconnected})
}

// handleUnban 清除节点访问策略与封禁状态。
func (s *Server) handleUnban(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.ctl == nil {
		webx.WriteError(w, http.StatusInternalServerError, errors.New("控制面未初始化"))
		return
	}
	if !isJSON(r) {
		webx.WriteError(w, http.StatusUnsupportedMediaType, errors.New("请求需为 application/json"))
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		webx.WriteError(w, http.StatusBadRequest, errors.New("缺少节点 ID"))
		return
	}
	if err := s.ctl.ClearAccessPolicy(req.ID); err != nil {
		webx.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	s.tunnels.Unban(req.ID)
	s.log.Info("节点访问策略已清除", "node", req.ID)
	webx.WriteJSON(w, map[string]any{"ok": true, "unbanned": true})
}

// handleNodePassword 查看、随机重置、指定或清除单个节点的独立接入密码。
// 这些操作会暴露或改变节点凭据，因此统一要求 PIN 二次验证。
func (s *Server) handleNodePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.ctl == nil {
		webx.WriteError(w, http.StatusInternalServerError, errors.New("控制面未初始化"))
		return
	}
	if !isJSON(r) {
		webx.WriteError(w, http.StatusUnsupportedMediaType, errors.New("请求需为 application/json"))
		return
	}
	var req struct {
		PIN      string `json:"pin"`
		Node     string `json:"node"`
		Action   string `json:"action"`
		Password string `json:"password"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.requirePIN(strings.TrimSpace(req.PIN)); err != nil {
		s.writePINError(w, err)
		return
	}
	node := strings.TrimSpace(req.Node)
	if node == "" {
		webx.WriteError(w, http.StatusBadRequest, errors.New("缺少节点 ID"))
		return
	}

	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "show":
		// 只返回是否已设置与更新时间；明文与哈希都不返回。
		entry, ok := s.ctl.NodePassword(node)
		if !ok {
			webx.WriteJSON(w, map[string]any{"ok": true, "node": node, "set": false})
			return
		}
		webx.WriteJSON(w, map[string]any{
			"ok":         true,
			"node":       node,
			"set":        true,
			"updated_at": entry.UpdatedAt,
		})
	case "random":
		pw, entry, err := s.ctl.SetNodeRandomPassword(node)
		if err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
		webx.WriteJSON(w, map[string]any{
			"ok":         true,
			"node":       node,
			"set":        true,
			"password":   pw,
			"updated_at": entry.UpdatedAt,
		})
	case "set":
		entry, err := s.ctl.SetNodePassword(node, req.Password)
		if err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
		webx.WriteJSON(w, map[string]any{
			"ok":         true,
			"node":       node,
			"set":        true,
			"updated_at": entry.UpdatedAt,
		})
	case "clear":
		if err := s.ctl.ClearNodePassword(node); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
		webx.WriteJSON(w, map[string]any{"ok": true, "node": node, "set": false})
	default:
		webx.WriteError(w, http.StatusBadRequest, errors.New("action 只能是 show/random/set/clear"))
	}
}

// handleDefaultPassword 查询或切换「允许使用默认密码接入」开关。
// 查询不需要 PIN；切换会改变全局接入策略，需要 PIN。
func (s *Server) handleDefaultPassword(w http.ResponseWriter, r *http.Request) {
	if s.ctl == nil {
		webx.WriteError(w, http.StatusInternalServerError, errors.New("控制面未初始化"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		allow, _ := s.ctl.NodePasswords()
		webx.WriteJSON(w, map[string]any{"allow_default": allow})
	case http.MethodPost:
		if !isJSON(r) {
			webx.WriteError(w, http.StatusUnsupportedMediaType, errors.New("请求需为 application/json"))
			return
		}
		var req struct {
			PIN   string `json:"pin"`
			Allow *bool  `json:"allow"`
		}
		if err := decodeJSONBody(r, &req); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.requirePIN(strings.TrimSpace(req.PIN)); err != nil {
			s.writePINError(w, err)
			return
		}
		if req.Allow == nil {
			webx.WriteError(w, http.StatusBadRequest, errors.New("缺少 allow 字段"))
			return
		}
		if err := s.ctl.SetAllowDefaultPassword(*req.Allow); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
		webx.WriteJSON(w, map[string]any{"ok": true, "allow_default": *req.Allow})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanWindows(in []access.Window) []access.Window {
	out := make([]access.Window, 0, len(in))
	for _, w := range in {
		start := strings.TrimSpace(w.Start)
		end := strings.TrimSpace(w.End)
		if start == "" && end == "" {
			continue
		}
		out = append(out, access.Window{Start: start, End: end})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isJSON(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

func decodeJSONBody(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(v); err != nil {
		return fmt.Errorf("请求格式错误: %w", err)
	}
	return nil
}
