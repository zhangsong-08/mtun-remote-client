package punch

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// Manager 负责 NAT 类型探测、候选收集与路径切换。
// 骨架阶段固定报告 relay，避免让调用方误以为已经直连。
type Manager struct {
	log  *slog.Logger
	path atomic.Value
}

func NewManager(log *slog.Logger) *Manager {
	m := &Manager{log: log}
	m.path.Store("relay")
	return m
}

func (m *Manager) Path() string {
	if v, ok := m.path.Load().(string); ok {
		return v
	}
	return "relay"
}

func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	m.log.Info("path manager started", "path", m.Path())
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.log.Debug("path check", "path", m.Path(), "note", "STUN/打洞尚未接入")
		}
	}
}
