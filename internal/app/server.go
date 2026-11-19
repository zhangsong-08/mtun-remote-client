package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"mtun/internal/config"
	"mtun/internal/control"
	"mtun/internal/publish"
	"mtun/internal/relay"
	"mtun/internal/state"
	"mtun/internal/tunnel"
	"mtun/internal/version"
	"mtun/internal/web"
)

func RunServer(ctx context.Context, cfg *config.Config, cfgPath string, log *slog.Logger) error {
	joinPass, err := config.LoadJoinPassword(cfgPath)
	if err != nil {
		return err
	}
	if err := config.ValidateJoinPassword(joinPass); err != nil {
		return fmt.Errorf("服务端未设置客户端接入密码（%v）；请先运行: mtun access -c %s", err, cfgPath)
	}

	reg := state.New(cfg.Overlay.ServerIP, cfg.Overlay.CIDR)
	tunnels := tunnel.NewManager(log, cfg.Publish.PortRange)
	ctl := control.NewServer(cfg.Control, joinPass, reg, tunnels, log)
	webSrv, err := web.New(cfg, reg, tunnels, ctl, version.Version, cfgPath, log)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	start := func(name string, fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				log.Error("component stopped", "component", name, "err", err)
				cancel()
			}
		}()
	}

	start("control", ctl.Run)
	start("relay", relay.New(cfg.Relay, log).Run)

	for _, rule := range cfg.Publish.Rules {
		if rule.Listen <= 0 {
			continue
		}
		proxy, err := publish.NewTCPProxy(rule, log)
		if err != nil {
			return err
		}
		start("publish/"+rule.Name, proxy.Run)
	}

	start("web", webSrv.Run)

	log.Info("mtun server started",
		"control", cfg.Control,
		"relay", cfg.Relay,
		"dashboard", "http://"+cfg.Dashboard,
	)

	<-ctx.Done()
	tunnels.Close()
	wg.Wait()
	log.Info("mtun server stopped")
	return nil
}
