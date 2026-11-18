package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
