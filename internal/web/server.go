package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"mtun/internal/config"
	"mtun/internal/selfupdate"
	"mtun/internal/state"
	"mtun/internal/sysinfo"
	"mtun/internal/tunnel"
	"mtun/internal/webx"
	"mtun/pkg/proto"
)

//go:embed dist
var distFS embed.FS

// Server 是只监听回环地址的服务端管理控制台。
type Server struct {
	cfg     *config.Config
	cfgPath string
	reg     *state.Registry
	tunnels *tunnel.Manager
	version string
	auth    *webx.Auth
	sys     *sysinfo.Collector
	log     *slog.Logger

	httpSrv *http.Server
	ctx     context.Context
	mu      sync.Mutex
}

func New(cfg *config.Config, reg *state.Registry, tunnels *tunnel.Manager, version, cfgPath string, log *slog.Logger) (*Server, error) {
	ttl := time.Duration(cfg.Panel.SessionMinutes) * time.Minute
	auth, err := webx.NewAuth(log, webx.Options{
		PasswordFile: cfgPath + ".passwd",
		APITokenFile: cfgPath + ".apitoken",
		Title:        "mtun 服务端控制台",
		Hint:         "管理员密码由终端设置（mtun passwd）；请输入用户名和密码。",
		AllowSetup:   false,
		SessionTTL:   ttl,
	})
	if err != nil {
		return nil, fmt.Errorf("%w；请先运行: mtun passwd -c %s", err, cfgPath)
	}
	return &Server{
		cfg:     cfg,
		cfgPath: cfgPath,
		reg:     reg,
		tunnels: tunnels,
		version: version,
		log:     log,
		auth:    auth,
		sys:     sysinfo.NewCollector(log),
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()

	ln, err := net.Listen("tcp", s.cfg.Dashboard)
	if err != nil {
		return fmt.Errorf("dashboard listen %s: %w", s.cfg.Dashboard, err)
	}
	s.log.Info("web dashboard listening", "url", "http://"+ln.Addr().String())
	go s.sys.Run(ctx)

	srv := s.newHTTPServer()
	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) newHTTPServer() *http.Server {
	return &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/nodes", s.handleNodes)
	mux.HandleFunc("/api/nodes/kick", s.handleKick)
	mux.HandleFunc("/api/system", s.handleSystem)
	mux.HandleFunc("/api/panel", s.handlePanel)
	mux.HandleFunc("/api/panel/restart", s.handlePanelRestart)
	mux.HandleFunc("/api/update", s.handleUpdate)
	mux.HandleFunc("/", s.handleIndex)

	staticFS, err := fs.Sub(distFS, "dist/static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	return s.auth.Wrap(mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleSystem(w http.ResponseWriter, _ *http.Request) {
	webx.WriteJSON(w, s.sys.Get())
}

// handlePanel 读取或保存面板设置；保存后需重启面板生效。
func (s *Server) handlePanel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		webx.WriteJSON(w, map[string]any{
			"dashboard":       s.cfg.Dashboard,
			"session_minutes": s.cfg.Panel.SessionMinutes,
			"page_size":       s.cfg.Panel.PageSize,
			"version":         s.version,
			"config_path":     s.cfgPath,
		})
	case http.MethodPost:
		var req struct {
			Dashboard      string `json:"dashboard"`
			SessionMinutes int    `json:"session_minutes"`
			PageSize       int    `json:"page_size"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			webx.WriteError(w, http.StatusBadRequest, fmt.Errorf("请求格式错误: %w", err))
			return
		}
		dash := strings.TrimSpace(req.Dashboard)
		if dash == "" {
			dash = s.cfg.Dashboard
		}
		if err := config.ValidateDashboard(dash); err != nil {
			webx.WriteError(w, http.StatusBadRequest, err)
			return
		}
		if req.SessionMinutes < 5 || req.SessionMinutes > 10080 {
			webx.WriteError(w, http.StatusBadRequest, errors.New("会话时长需在 5 到 10080 分钟之间"))
			return
		}
		if req.PageSize < 5 || req.PageSize > 200 {
			webx.WriteError(w, http.StatusBadRequest, errors.New("分页大小需在 5 到 200 之间"))
			return
		}
		s.cfg.Dashboard = dash
		s.cfg.Panel.SessionMinutes = req.SessionMinutes
		s.cfg.Panel.PageSize = req.PageSize
		if err := config.Save(s.cfgPath, s.cfg); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
		s.log.Info("面板设置已保存", "dashboard", dash, "session_minutes", req.SessionMinutes, "page_size", req.PageSize)
		webx.WriteJSON(w, map[string]any{"saved": true, "restart_required": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handlePanelRestart 在进程内重启管理面板：重新读取配置、重建监听与会话。
func (s *Server) handlePanelRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// 先返回响应，再异步重启，避免请求自身阻塞 Shutdown。
	webx.WriteJSON(w, map[string]any{"restarting": true, "relogin": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := s.restartDashboard(); err != nil {
			s.log.Error("重启面板失败", "err", err)
		}
	}()
}

// restartDashboard 关闭旧监听后按最新配置重新启动管理面板。
// 新地址绑定失败时自动恢复原地址，避免面板彻底不可用。
func (s *Server) restartDashboard() error {
	s.mu.Lock()

	cfg, err := config.Load(s.cfgPath, config.RoleServer)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("重新读取配置失败: %w", err)
	}
	auth, err := webx.NewAuth(s.log, webx.Options{
		PasswordFile: s.cfgPath + ".passwd",
		APITokenFile: s.cfgPath + ".apitoken",
		Title:        "mtun 服务端控制台",
		Hint:         "管理员密码由终端设置（mtun passwd）；请输入用户名和密码。",
		AllowSetup:   false,
		SessionTTL:   time.Duration(cfg.Panel.SessionMinutes) * time.Minute,
	})
	if err != nil {
		s.mu.Unlock()
		return err
	}

	oldAddr := s.cfg.Dashboard
	oldSrv := s.httpSrv

	// 先关闭旧 HTTP server（异步 Shutdown）
	if oldSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		go func() {
			_ = oldSrv.Shutdown(shutdownCtx)
			cancel()
		}()
	}

	// 锁外尝试绑定新地址
	s.mu.Unlock()
	var newLn net.Listener
	if cfg.Dashboard != oldAddr || true { // 总尝试用新配置 listener
		newLn, err = net.Listen("tcp", cfg.Dashboard)
	}
	if err != nil {
		// 新地址绑定失败，尝试恢复原地址
		reln, relErr := net.Listen("tcp", oldAddr)
		if relErr != nil {
			// 两个地址都绑不上，放弃
			if auth != nil {
				auth.Close()
			}
			return fmt.Errorf("监听 %s 失败且无法恢复 %s: %w", cfg.Dashboard, oldAddr, err)
		}
		// 用恢复的 listener
		newLn = reln
		cfg.Dashboard = oldAddr
		s.log.Warn("新面板地址绑定失败，已恢复原地址", "new", cfg.Dashboard, "old", oldAddr)
	}

	s.mu.Lock()
	s.cfg = cfg
	s.auth = auth
	srv := s.newHTTPServer()
	s.httpSrv = srv
	s.mu.Unlock()

	go s.serve(srv, newLn)
	s.log.Info("面板已重启", "addr", cfg.Dashboard, "session_minutes", cfg.Panel.SessionMinutes)
	return nil
}

func (s *Server) serve(srv *http.Server, ln net.Listener) {
	ctx := s.ctx
	if ctx != nil {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.log.Error("面板服务退出", "err", err)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.reg.Stats()
	webx.WriteJSON(w, map[string]any{
		"version":        s.version,
		"uptime_seconds": int64(st.Uptime.Seconds()),
		"nodes":          st.Total,
		"offline":        st.Offline,
		"direct":         st.Direct,
		"relay":          st.Relay,
		"publish_rules":  st.PublishRules,
	})
}

type nodeView struct {
	ID              string             `json:"id"`
	OverlayIP       string             `json:"overlay_ip"`
	Path            string             `json:"path"`
	Online          bool               `json:"online"`
	Endpoints       []string           `json:"endpoints"`
	Publish         int                `json:"publish"`
	LastSeenSeconds int64              `json:"last_seen_seconds"`
	Rules           []proto.RuleStatus `json:"rules"`
}

func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID         string `json:"id"`
		BanSeconds int    `json:"ban_seconds"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webx.WriteError(w, http.StatusBadRequest, errors.New("请求格式错误"))
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		webx.WriteError(w, http.StatusBadRequest, errors.New("缺少节点 ID"))
		return
	}
	ban := req.BanSeconds
	if ban <= 0 {
		ban = 60
	}
	if ban > 3600 {
		ban = 3600
	}
	if err := s.tunnels.Kick(req.ID, time.Duration(ban)*time.Second); err != nil {
		webx.WriteError(w, http.StatusNotFound, err)
		return
	}
	s.reg.MarkOffline(req.ID)
	webx.WriteJSON(w, map[string]any{"ok": true, "node": req.ID, "ban_seconds": ban})
}

// handleUpdate 支持查询与执行在线更新，更新后自动重启。
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	u := selfupdate.New(s.log)
	switch r.Method {
	case http.MethodGet:
		rel, available, err := u.Check(r.Context())
		if err != nil {
			webx.WriteError(w, http.StatusBadGateway, err)
			return
		}
		webx.WriteJSON(w, map[string]any{
			"current":   u.Current,
			"latest":    rel.Version,
			"available": available,
			"notes":     rel.Notes,
			"repo":      u.Repo,
		})
	case http.MethodPost:
		rel, available, err := u.Check(r.Context())
		if err != nil {
			webx.WriteError(w, http.StatusBadGateway, err)
			return
		}
		if !available {
			webx.WriteJSON(w, map[string]any{"updated": false, "current": u.Current, "latest": rel.Version})
			return
		}
		s.log.Info("开始在线更新", "from", u.Current, "to", rel.Version)
		exe, err := u.Apply(r.Context(), rel)
		if err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
		// 先确认重启助手能启动；失败时保留当前进程，避免服务直接消失。
		if err := selfupdate.ScheduleRestart(exe, s.log); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, fmt.Errorf(
				"已更新到 %s，但自动重启失败：%v；请手动重启服务（程序文件已是新版本）", rel.Version, err))
			return
		}
		webx.WriteJSON(w, map[string]any{
			"updated": true, "from": u.Current, "to": rel.Version, "restarting": true,
		})
		go func() {
			time.Sleep(time.Second)
			os.Exit(0)
		}()
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleNodes(w http.ResponseWriter, _ *http.Request) {
	nodes := s.reg.List()
	out := make([]nodeView, 0, len(nodes))
	now := time.Now()
	for _, n := range nodes {
		out = append(out, nodeView{
			ID:              n.ID,
			OverlayIP:       n.OverlayIP,
			Path:            n.Path,
			Online:          n.Online,
			Endpoints:       n.Endpoints,
			Publish:         len(n.Publish),
			LastSeenSeconds: int64(now.Sub(n.LastSeen).Seconds()),
			Rules:           s.nodeRules(n),
		})
	}
	webx.WriteJSON(w, out)
}

// nodeRules 返回节点当前的规则明细：在线取隧道实时状态，离线回退到上次上报。
func (s *Server) nodeRules(n state.Node) []proto.RuleStatus {
	rules := s.tunnels.Statuses(n.ID)
	if len(rules) > 0 {
		return rules
	}
	out := make([]proto.RuleStatus, 0, len(n.Publish))
	for _, p := range n.Publish {
		st := proto.RuleActive
		msg := ""
		if !n.Online {
			st = "offline"
			msg = "节点离线，规则未生效"
		}
		out = append(out, proto.RuleStatus{
			Name:    p.Name,
			Proto:   p.Proto,
			Listen:  p.Listen,
			Target:  p.Target,
			State:   st,
			Message: msg,
		})
	}
	return out
}
