package selfupdate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"dev", "1.0.0", -1},
		{"v1.0.0", "1.0.1", -1},
		{"1.2.0", "1.2.0", 0},
		{"2.0.0", "1.9.9", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	name := "mtun-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(target, []byte("new-binary")); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-binary" {
		t.Fatalf("内容未替换: %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatal("新文件应具有可执行权限")
		}
	}
}

func TestFetchRetriesTruncatedBody(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			// 声明完整长度却提前断开，模拟 unexpected EOF。
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "short")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, strings.Repeat("x", 100))
	}))
	defer srv.Close()

	u := &Updater{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), client: srv.Client()}
	data, err := u.fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(data) != 100 {
		t.Fatalf("len(data)=%d, want 100", len(data))
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2（应重试一次）", attempts)
	}
}

func TestFetchStopsOnNotFound(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	u := &Updater{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), client: srv.Client()}
	if _, err := u.fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("预期 404 返回错误")
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d, want 1（404 不应重试）", attempts)
	}
}

func TestPickAsset(t *testing.T) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	rel := &Release{Assets: []Asset{
		{Name: "checksums.txt"},
		{Name: fmt.Sprintf("mtun-%s-%s%s", runtime.GOOS, runtime.GOARCH, suffix)},
	}}
	if _, ok := pickAsset(rel); !ok {
		t.Fatalf("应匹配 %s/%s 的产物", runtime.GOOS, runtime.GOARCH)
	}
	if _, ok := pickAsset(&Release{Assets: []Asset{{Name: "other"}}}); ok {
		t.Fatal("不应匹配不存在的产物")
	}
}
