// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"log/slog"
	"sync"

	"mtun/internal/agent"
	"mtun/internal/clientweb"
	"mtun/internal/config"
	"mtun/internal/punch"
	"mtun/internal/version"
	"mtun/internal/wg"
)

// RunClient 启动客户端：连接监管、本地控制台以及数据面占位模块。
func RunClient(ctx context.Context, cfg *config.Config, cfgPath string, log *slog.Logger) error {
	joinPass, err := config.LoadJoinPassword(cfgPath)
	if err != nil {
		return err
	}
	if joinPass == "" {
		log.Warn("尚未配置接入密码，请在客户端控制台添加服务端与接入密码")
	}

	log.Info("mtun client starting",
		"node", cfg.Node,
		"server", cfg.Server,
		"dashboard", "http://"+cfg.Dashboard,
	)

	device := wg.NewNoop(log)
	if err := device.Up(); err != nil {
		return err
	}
	defer device.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var group sync.WaitGroup
	start := func(name string, fn func(context.Context) error) {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				log.Error("component stopped", "component", name, "err", err)
				cancel()
			}
		}()
	}

	path := punch.NewManager(log)
	start("path", path.Run)

	ag := agent.New(cfg, cfgPath, joinPass, version.Version, log)
	webSrv, err := clientweb.New(cfg.Dashboard, ag, cfgPath, log)
	if err != nil {
		return err
	}
	start("agent", ag.Run)
	start("web", webSrv.Run)

	for _, route := range cfg.Routes {
		log.Info("route advertised", "cidr", route, "note", "子网路由将在 M4 生效")
	}

	<-ctx.Done()
	group.Wait()
	log.Info("mtun client stopped")
	return nil
}
