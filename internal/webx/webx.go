package webx

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed assets
var assets embed.FS

const loginAssetPath = "/__mtun/style.css"

const (
	defaultSessionIdle = 30 * time.Minute
	maxLoginFailures   = 5
	loginLock          = 30 * time.Second
	minPasswordLength  = 8
	pbkdf2Iterations   = 210000
	sessionCleanupInt  = 10 * time.Minute // 过期 session 清理周期
)

const passwordAlgo = "pbkdf2-sha256"

var (
	loginTmpl = template.Must(template.ParseFS(assets, "assets/login.html"))
	setupTmpl = template.Must(template.ParseFS(assets, "assets/setup.html"))
	loginCSS  = mustRead("assets/style.css")
)

type session struct {
	expires  time.Time
	lastSeen time.Time
}

// passwordFile 是 <config>.passwd 的内容，只保存密码哈希。
type passwordFile struct {
	User string `json:"user"`
	Algo string `json:"algo"`
	Iter int    `json:"iter"`
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

// Options 描述管理面认证所需的文件与文案。
type Options struct {
	PasswordFile string
	APITokenFile string
	Title        string
	Hint         string
	// AllowSetup 为 true 时允许在未设置密码时通过网页首次设置（客户端）；
	// 为 false 时必须在终端先设置密码才能启动管理面（服务端）。
	AllowSetup bool
	// SessionTTL 是登录会话的绝对有效期，<=0 时使用默认 12 小时。
	SessionTTL time.Duration
	// SessionIdle 是登录会话的空闲超时，<=0 时使用默认 30 分钟。
	SessionIdle time.Duration
}

// Auth 提供管理面的回环 Host 校验、安全响应头、账号密码登录与会话。
type Auth struct {
	log      *slog.Logger
	opts     Options
	apiToken string
	ttl      time.Duration
	idle     time.Duration

	mu       sync.Mutex
	creds    *passwordFile
	sessions map[string]session
	failures int
	lockedTo time.Time
	stopGC   chan struct{}
}

func NewAuth(log *slog.Logger, opt Options) (*Auth, error) {
	ttl := opt.SessionTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	idle := opt.SessionIdle
	if idle <= 0 {
		idle = defaultSessionIdle
	}
	a := &Auth{
		log:      log,
		opts:     opt,
		ttl:      ttl,
		idle:     idle,
		sessions: map[string]session{},
		stopGC:   make(chan struct{}),
	}

	creds, err := readPasswordFile(opt.PasswordFile)
	switch {
	case err == nil:
		a.creds = &creds
	case errors.Is(err, os.ErrNotExist):
		if !opt.AllowSetup {
			return nil, fmt.Errorf("管理密码未设置（%s）", opt.PasswordFile)
		}
	default:
		return nil, err
	}

	token, err := loadOrCreateAPIToken(opt.APITokenFile)
	if err != nil {
		return nil, err
	}
	a.apiToken = token

	// 启动后台 session 清理，防止长时间运行积累无效会话
	go a.sessionGC()

	return a, nil
}

// sessionGC 定期清理过期 session，每 sessionCleanupInt 运行一次。
func (a *Auth) sessionGC() {
	ticker := time.NewTicker(sessionCleanupInt)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopGC:
			return
		case <-ticker.C:
			a.mu.Lock()
			now := time.Now()
			for sid, s := range a.sessions {
				if now.After(s.expires) || now.Sub(s.lastSeen) > a.idle {
					delete(a.sessions, sid)
				}
			}
			a.mu.Unlock()
		}
	}
}

// Close 停止后台清理。
func (a *Auth) Close() {
	select {
	case <-a.stopGC:
	default:
		close(a.stopGC)
	}
}

// SetPasswordFile 写入管理密码哈希，供终端命令在服务未运行时设置密码。
func SetPasswordFile(path, user, password string) error {
	if user == "" {
		user = "admin"
	}
	if err := validateUser(user); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return err
	}
	rec := passwordFile{
		User: user,
		Algo: passwordAlgo,
		Iter: pbkdf2Iterations,
		Salt: base64.StdEncoding.EncodeToString(salt),
		Hash: base64.StdEncoding.EncodeToString(key),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

func (a *Auth) configured() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creds != nil
}

// Wrap 包装业务 Handler：Host/安全头校验 → 首次设置/登录 → 会话认证。
func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if !allowedOrigin(r) {
			a.log.Warn("已拦截跨站写请求",
				"origin", r.Header.Get("Origin"),
				"host", r.Host,
				"forwarded_host", r.Header.Get("X-Forwarded-Host"),
				"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"),
				"method", r.Method,
				"path", r.URL.Path)
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; "+
				"object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")

		switch r.URL.Path {
		case loginAssetPath:
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write(loginCSS)
			return
		case "/setup":
			a.handleSetup(w, r)
			return
		case "/login":
			if !a.configured() && a.opts.AllowSetup {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
			a.handleLogin(w, r)
			return
		case "/logout":
			a.handleLogout(w, r)
			return
		}

		if !a.configured() {
			// API Token 由本机文件提供，未设置密码时也允许终端管理。
			if a.hasBearer(r) {
				next.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if a.opts.AllowSetup {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
			http.Error(w, "管理密码未设置", http.StatusServiceUnavailable)
			return
		}

		if !a.authenticated(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleSetup 只允许在尚无密码时设置一次（客户端首次使用）。
func (a *Auth) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !a.opts.AllowSetup {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if a.configured() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = setupTmpl.Execute(w, map[string]any{
			"Title": a.opts.Title,
			"Error": setupError(r.URL.Query().Get("error")),
		})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/setup?error=invalid", http.StatusSeeOther)
			return
		}
		user := strings.TrimSpace(r.FormValue("user"))
		password := r.FormValue("password")
		confirm := r.FormValue("confirm")
		if user == "" {
			user = "admin"
		}
		if password != confirm {
			http.Redirect(w, r, "/setup?error=mismatch", http.StatusSeeOther)
			return
		}
		if err := SetPasswordFile(a.opts.PasswordFile, user, password); err != nil {
			http.Redirect(w, r, "/setup?error=invalid", http.StatusSeeOther)
			return
		}
		creds, err := readPasswordFile(a.opts.PasswordFile)
		if err != nil {
			http.Error(w, "保存密码失败", http.StatusInternalServerError)
			return
		}
		a.mu.Lock()
		a.creds = &creds
		a.mu.Unlock()
		a.log.Info("管理密码已设置", "user", user)
		a.startSession(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user := "admin"
		a.mu.Lock()
		if a.creds != nil {
			user = a.creds.User
		}
		a.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = loginTmpl.Execute(w, map[string]any{
			"Title": a.opts.Title,
			"Hint":  a.opts.Hint,
			"User":  user,
			"Error": loginError(r.URL.Query().Get("error")),
		})
	case http.MethodPost:
		if a.locked() {
			http.Redirect(w, r, "/login?error=2", http.StatusSeeOther)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
			return
		}
		user := strings.TrimSpace(r.FormValue("user"))
		password := r.FormValue("password")
		if !a.verify(user, password) {
			a.recordFailure()
			time.Sleep(300 * time.Millisecond)
			http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
			return
		}
		a.recordSuccess()
		a.startSession(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *Auth) startSession(w http.ResponseWriter, r *http.Request) {
	sid := randomHex(32)
	now := time.Now()
	a.mu.Lock()
	a.sessions[sid] = session{expires: now.Add(a.ttl), lastSeen: now}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "mtun_session",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(a.ttl.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("mtun_session"); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "mtun_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *Auth) verify(user, password string) bool {
	a.mu.Lock()
	creds := a.creds
	a.mu.Unlock()
	if creds == nil {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(user), []byte(creds.User)) != 1 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(creds.Salt)
	if err != nil {
		return false
	}
	stored, err := base64.StdEncoding.DecodeString(creds.Hash)
	if err != nil {
		return false
	}
	computed, err := pbkdf2.Key(sha256.New, password, salt, creds.Iter, len(stored))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(computed, stored) == 1
}

func (a *Auth) hasBearer(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || a.apiToken == "" {
		return false
	}
	got := strings.TrimPrefix(header, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.apiToken)) == 1
}

func (a *Auth) authenticated(r *http.Request) bool {
	if a.hasBearer(r) {
		return true
	}

	cookie, err := r.Cookie("mtun_session")
	if err != nil {
		return false
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}
	if now.After(s.expires) || now.Sub(s.lastSeen) > a.idle {
		delete(a.sessions, cookie.Value)
		return false
	}
	s.lastSeen = now
	a.sessions[cookie.Value] = s
	return true
}

func (a *Auth) locked() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Before(a.lockedTo)
}

func (a *Auth) recordFailure() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures++
	if a.failures >= maxLoginFailures {
		a.failures = 0
		a.lockedTo = time.Now().Add(loginLock)
	}
}

func (a *Auth) recordSuccess() {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures = 0
	a.lockedTo = time.Time{}
	for sid, s := range a.sessions {
		if now.After(s.expires) || now.Sub(s.lastSeen) > a.idle {
			delete(a.sessions, sid)
		}
	}
}

func loginError(code string) string {
	switch code {
	case "1":
		return "用户名或密码不正确，请重试"
	case "2":
		return "尝试次数过多，请 30 秒后再试"
	default:
		return ""
	}
}

func setupError(code string) string {
	switch code {
	case "mismatch":
		return "两次输入的密码不一致"
	case "invalid":
		return "密码格式不正确（至少 8 位，用户名仅限字母、数字、点、下划线、短横线）"
	default:
		return ""
	}
}

func validatePassword(password string) error {
	if len(password) < minPasswordLength {
		return fmt.Errorf("密码至少需要 %d 位", minPasswordLength)
	}
	if len(password) > 128 {
		return errors.New("密码不能超过 128 位")
	}
	return nil
}

func validateUser(user string) error {
	if len(user) < 1 || len(user) > 32 {
		return errors.New("用户名长度需在 1-32 之间")
	}
	for _, c := range user {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '-':
		default:
			return fmt.Errorf("用户名包含非法字符 %q", c)
		}
	}
	return nil
}

func readPasswordFile(path string) (passwordFile, error) {
	var rec passwordFile
	data, err := os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("解析密码文件 %s: %w", path, err)
	}
	if rec.User == "" || rec.Hash == "" || rec.Salt == "" || rec.Iter <= 0 {
		return rec, fmt.Errorf("密码文件 %s 内容不完整", path)
	}
	if rec.Algo != passwordAlgo {
		return rec, fmt.Errorf("密码文件 %s 使用了不支持的算法 %q", path, rec.Algo)
	}
	return rec, nil
}

func loadOrCreateAPIToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if v := cleanToken(string(data)); v != "" {
			return v, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("读取 API Token 文件: %w", err)
	}
	token := randomHex(24)
	if err := writeFileAtomic(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写入 API Token 文件 %s: %w", path, err)
	}
	return token, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// allowedHost 校验请求 Host。
// 面板允许绑定公网地址后，这里不再限制回环地址；
// 写操作仍由 allowedOrigin 做同源校验，且访问需要密码登录。
func allowedHost(hostport string) bool {
	return strings.TrimSpace(hostport) != ""
}

// allowedOrigin 拒绝跨站发起的写操作；同源与命令行请求（无 Origin）放行。
func allowedOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	// 浏览器通过代理/端口转发访问时，Origin 可能不是回环地址，
	// 但 Sec-Fetch-Site 由浏览器保证同源，可用于放行这类合法请求。
	if site := r.Header.Get("Sec-Fetch-Site"); site == "same-origin" || site == "none" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	// 收集「本服务对外暴露的地址」：直连时是 Host，
	// 经过反向代理时外部地址在 X-Forwarded-Host 里，Host 会变成内网地址。
	candidates := []string{r.Host}
	for _, v := range r.Header.Values("X-Forwarded-Host") {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				candidates = append(candidates, h)
			}
		}
	}
	for _, h := range candidates {
		if strings.EqualFold(u.Host, h) {
			return true
		}
		// 端口映射场景：外部 8080 映射到内部 10004，主机名相同但端口不同。
		if strings.EqualFold(u.Hostname(), hostOnly(h)) {
			return true
		}
	}
	return isLoopbackHost(u.Hostname())
}

// hostOnly 从 host[:port] 或 [ipv6]:port 中取出主机名。
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(hostport, "[]")
}

func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func cleanToken(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func mustRead(name string) []byte {
	data, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}
