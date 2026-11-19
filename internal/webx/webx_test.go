package webx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func tempFiles(t *testing.T) (passwordFile, apiTokenFile string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "cfg.passwd"), filepath.Join(dir, "cfg.apitoken")
}

func newTestAuth(t *testing.T, allowSetup bool) (*Auth, string, string) {
	t.Helper()
	pf, af := tempFiles(t)
	a, err := NewAuth(testLogger(), Options{
		PasswordFile: pf,
		APITokenFile: af,
		Title:        "测试控制台",
		Hint:         "hint",
		AllowSetup:   allowSetup,
	})
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	return a, pf, af
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
}

func postForm(t *testing.T, h http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:10005"+path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func getWithCookies(t *testing.T, h http.Handler, rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:10005/", nil)
	for _, ck := range rec.Result().Cookies() {
		req.AddCookie(ck)
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	return out
}

func TestServerRequiresPassword(t *testing.T) {
	if _, err := NewAuth(testLogger(), Options{
		PasswordFile: filepath.Join(t.TempDir(), "x.passwd"),
		APITokenFile: filepath.Join(t.TempDir(), "x.apitoken"),
		AllowSetup:   false,
	}); err == nil {
		t.Fatal("服务端未设置密码时应报错")
	}
}

func TestPasswordFileAndVerify(t *testing.T) {
	pf, af := tempFiles(t)
	if err := SetPasswordFile(pf, "admin", "s3cret-pass"); err != nil {
		t.Fatalf("SetPasswordFile: %v", err)
	}
	a, err := NewAuth(testLogger(), Options{PasswordFile: pf, APITokenFile: af})
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	if !a.verify("admin", "s3cret-pass") {
		t.Fatal("正确密码应校验通过")
	}
	if a.verify("admin", "wrong-pass") || a.verify("other", "s3cret-pass") {
		t.Fatal("错误用户名或密码不应通过")
	}

	data, err := os.ReadFile(af)
	if err != nil {
		t.Fatalf("API Token 文件未生成: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatal("API Token 不能为空")
	}
}

func TestSetupFlow(t *testing.T) {
	a, _, _ := newTestAuth(t, true)
	h := a.Wrap(okHandler())

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:10005/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/setup") {
		t.Fatalf("未设置密码时应跳转 /setup，得到 %q", loc)
	}

	rec = postForm(t, h, "/setup", url.Values{
		"user":     {"admin"},
		"password": {"first-pass"},
		"confirm":  {"first-pass"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("设置密码应重定向，得到 %d", rec.Code)
	}
	if out := getWithCookies(t, h, rec); out.Code != http.StatusOK || out.Body.String() != "ok" {
		t.Fatalf("设置密码后应直接登录成功: %d %q", out.Code, out.Body.String())
	}

	// 已设置后再次提交 setup 不应覆盖密码
	rec = postForm(t, h, "/setup", url.Values{
		"user":     {"admin"},
		"password": {"hacked-pass"},
		"confirm":  {"hacked-pass"},
	})
	if !strings.Contains(rec.Header().Get("Location"), "/login") {
		t.Fatalf("已设置密码后应跳转登录页，得到 %q", rec.Header().Get("Location"))
	}
	if !a.verify("admin", "first-pass") {
		t.Fatal("密码不应被二次设置覆盖")
	}
}

func TestLoginAndLockout(t *testing.T) {
	pf, af := tempFiles(t)
	if err := SetPasswordFile(pf, "admin", "first-pass"); err != nil {
		t.Fatal(err)
	}
	a, err := NewAuth(testLogger(), Options{PasswordFile: pf, APITokenFile: af})
	if err != nil {
		t.Fatal(err)
	}
	h := a.Wrap(okHandler())

	rec := postForm(t, h, "/login", url.Values{"user": {"admin"}, "password": {"wrong-pass"}})
	if !strings.Contains(rec.Header().Get("Location"), "error=1") {
		t.Fatalf("错误密码应返回 error=1，得到 %q", rec.Header().Get("Location"))
	}

	rec = postForm(t, h, "/login", url.Values{"user": {"admin"}, "password": {"first-pass"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("正确密码应重定向，得到 %d", rec.Code)
	}
	if out := getWithCookies(t, h, rec); out.Code != http.StatusOK {
		t.Fatalf("登录会话应可访问页面，得到 %d", out.Code)
	}

	for i := 0; i < maxLoginFailures; i++ {
		postForm(t, h, "/login", url.Values{"user": {"admin"}, "password": {"wrong-pass"}})
	}
	rec = postForm(t, h, "/login", url.Values{"user": {"admin"}, "password": {"first-pass"}})
	if !strings.Contains(rec.Header().Get("Location"), "error=2") {
		t.Fatalf("连续失败后应被临时锁定，得到 %q", rec.Header().Get("Location"))
	}
}

func TestBearerAPIToken(t *testing.T) {
	a, _, af := newTestAuth(t, true)
	h := a.Wrap(okHandler())

	data, err := os.ReadFile(af)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(data))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:10005/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("API Token 应放行: %d %q", rec.Code, rec.Body.String())
	}
}

func TestOriginGuard(t *testing.T) {
	a, _, _ := newTestAuth(t, true)
	h := a.Wrap(okHandler())

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:10005/api/server", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨站 Origin 应被拒绝，得到 %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:10005/api/server", strings.NewReader("{}"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 Origin 的请求应放行到认证环节，得到 %d", rec.Code)
	}

	// 通过代理/端口转发访问时，浏览器会带 Sec-Fetch-Site: same-origin
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:10005/api/server", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://mtun.example.com")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("浏览器同源代理请求应放行到认证环节，得到 %d", rec.Code)
	}
}

func TestAllowedHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:10004":    true,
		"localhost:10005":    true,
		"[::1]:10004":        true,
		"192.168.1.10:10005": true,  // 面板可绑定内网/公网地址
		"panel.example.com":  true,  // 远程访问时 Host 为域名
		"":                   false, // 缺少 Host 一律拒绝
	}
	for host, want := range cases {
		if got := allowedHost(host); got != want {
			t.Errorf("allowedHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestAllowedOrigin(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		host    string
		origin  string
		sfs     string
		fwdHost string
		want    bool
	}{
		{name: "读请求直接放行", method: http.MethodGet, host: "1.2.3.4:10004", origin: "https://evil.com", want: true},
		{name: "命令行无 Origin", method: http.MethodPost, host: "1.2.3.4:10004", want: true},
		{name: "Origin 为 null", method: http.MethodPost, host: "1.2.3.4:10004", origin: "null", want: true},
		{name: "Origin 为 NULL 大写", method: http.MethodPost, host: "1.2.3.4:10004", origin: "NULL", want: true},
		{name: "同源直连", method: http.MethodPost, host: "1.2.3.4:10004", origin: "http://1.2.3.4:10004", want: true},
		{name: "浏览器同源标记", method: http.MethodPost, host: "1.2.3.4:10004", origin: "http://1.2.3.4:10004", sfs: "same-origin", want: true},
		{name: "反代保留外部 Host", method: http.MethodPost, host: "127.0.0.1:10004", origin: "https://panel.example.com", fwdHost: "panel.example.com", want: true},
		{name: "反代改写 Host 无 SFS", method: http.MethodPost, host: "127.0.0.1:10004", origin: "https://panel.example.com", want: false},
		{name: "端口映射同主机", method: http.MethodPost, host: "1.2.3.4:10004", origin: "http://1.2.3.4:8080", want: true},
		{name: "跨站来源拒绝", method: http.MethodPost, host: "1.2.3.4:10004", origin: "https://evil.example.com", want: false},
		{name: "IPv6 同源", method: http.MethodPost, host: "[2408:1::1]:10004", origin: "http://[2408:1::1]:10004", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+"/api/panel", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.sfs != "" {
				r.Header.Set("Sec-Fetch-Site", tc.sfs)
			}
			if tc.fwdHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.fwdHost)
			}
			if got := allowedOrigin(r); got != tc.want {
				t.Errorf("allowedOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}
