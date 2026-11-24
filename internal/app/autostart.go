// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"log/slog"
	"time"

	"mtun/internal/autostart"
)

// ensureAutostart 在服务端或客户端启动后异步检查并（若未配置）自动注册自启动。
// 整个过程 best-effort：任何错误都只打 Warn 日志，绝不阻塞主流程。
// 已存在的自启动条目保持原样，不强制覆盖。
func ensureAutostart(role, cfgPath string, log *slog.Logger) {
	m, err := autostart.New(role, cfgPath)
	if err != nil {
		log.Warn("自动注册自启动：初始化失败", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := m.Status(ctx)
	if err != nil {
		log.Warn("自动注册自启动：查询状态失败", "err", err)
		return
	}
	if !st.Supported {
		return // 当前平台不支持，静默跳过
	}
	if st.Enabled {
		return // 已启用，跳过
	}
	log.Info("检测到未配置自启动，正在自动注册", "role", role, "mode", st.Mode)
	if _, err := m.Set(ctx, true); err != nil {
		log.Warn("自动注册自启动失败", "role", role, "err", err)
		return
	}
	// 注册后立即启动（计划任务 / NSSM 服务）。
	if err := m.Start(ctx); err != nil {
		log.Warn("自启动已注册但立即启动失败（重启后仍会生效）", "role", role, "err", err)
		return
	}
	log.Info("自启动已注册并启动", "role", role)
}
