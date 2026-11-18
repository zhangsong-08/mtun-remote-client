package clientweb

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"mtun/internal/agent"
	"mtun/internal/selfupdate"
	"mtun/internal/webx"
	"mtun/pkg/proto"
)

//go:embed dist
var distFS embed.FS

// Server 是客户端本地控制台：添加服务端、管理本机转发端口、查看运行状态。
// 只展示本客户端的数据，不提供任何其他客户端的信息。
type Server struct {
	addr    string
	ag      *agent.Agent
	auth    *webx.Auth
	log     *slog.Logger
	httpSrv *http.Server
}

func New(addr string, ag *agent.Agent, cfgPath string, log *slog.Logger) (*Server, error) {
	auth, err := webx.NewAuth(log, webx.Options{
		PasswordFile: cfgPath + ".passwd",
		APITokenFile: cfgPath + ".apitoken",
		Title:        "mtun 客户端控制台",
		Hint:         "请输入本机登录密码（首次访问会引导设置）。",
		AllowSetup:   true,
	})
	if err != nil {
		return nil, err
	}
	return &Server{addr: addr, ag: ag, auth: auth, log: log}, nil
}

func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("dashboard listen %s: %w", s.addr, err)
	}
	s.log.Info("client web dashboard listening", "url", "http://"+ln.Addr().String())

	s.httpSrv = &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutdownCtx)
	}()
	if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/server", s.handleServer)
	mux.HandleFunc("/api/rules", s.handleRule)
	mux.HandleFunc("/api/rules/delete", s.handleDeleteRule)
	mux.HandleFunc("/api/update", s.handleUpdate)
	mux.HandleFunc("/", s.handleIndex)

	staticFS, err := fs.Sub(distFS, "dist/static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	return s.auth.Wrap(mux)
}

// handleUpdate 支持查询与执行在线更新，更新后自动重启客户端。
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
		// 下载不绑定请求上下文：浏览器超时或断开时不应中断更新。
		dlCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		exe, err := u.Apply(dlCtx, rel)
		if err != nil {
			webx.WriteError(w, http.StatusInternalServerError, err)
			return
		}
		// 先确认重启助手能启动；失败时保留当前进程，避免服务直接消失。
		if err := selfupdate.ScheduleRestart(exe, s.log); err != nil {
			webx.WriteError(w, http.StatusInternalServerError, fmt.Errorf(
				"已更新到 %s，但自动重启失败：%v；请手动重启客户端（程序文件已是新版本）", rel.Version, err))
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

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	webx.WriteJSON(w, s.ag.Snapshot())
}

type serverReq struct {
	Server string `json:"server"`
	Key    string `json:"key"`
}

func (s *Server) handleServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req serverReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.ag.SetServer(req.Server, req.Key); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	webx.WriteJSON(w, map[string]bool{"ok": true})
}

type ruleReq struct {
	Name   string `json:"name"`
	Proto  string `json:"proto"`
	Listen int    `json:"listen"`
	Target string `json:"target"`
}

func (s *Server) handleRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req ruleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	rule := proto.PublishRule{Name: req.Name, Proto: req.Proto, Listen: req.Listen, Target: req.Target}
	if err := s.ag.AddRule(rule); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	webx.WriteJSON(w, map[string]bool{"ok": true})
}

type deleteReq struct {
	Name string `json:"name"`
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req deleteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.ag.DeleteRule(req.Name); err != nil {
		webx.WriteError(w, http.StatusBadRequest, err)
		return
	}
	webx.WriteJSON(w, map[string]bool{"ok": true})
}

// decodeJSON 限制请求体大小并要求 application/json，避免跨站表单直接提交。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		webx.WriteError(w, http.StatusUnsupportedMediaType, fmt.Errorf("请求需为 application/json"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		webx.WriteError(w, http.StatusBadRequest, fmt.Errorf("请求格式错误: %w", err))
		return false
	}
	return true
}
