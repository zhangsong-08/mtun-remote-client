package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mtun/internal/version"
	"mtun/pkg/proto"
)

func TestNormalizeVisibility(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		list     []string
		wantMode string
		wantList []string
		wantErr  bool
	}{
		{"默认所有人", "", nil, proto.VisibleAll, nil, false},
		{"仅自己", "self", []string{"x"}, proto.VisibleSelf, nil, false},
		{"指定节点去重", "nodes", []string{" b ", "a", "b", ""}, proto.VisibleNodes, []string{"b", "a"}, false},
		{"指定节点为空", "nodes", nil, "", nil, true},
		{"未知模式", "friends", nil, "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, list, err := NormalizeVisibility(tc.input, tc.list)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if mode != tc.wantMode || !reflect.DeepEqual(list, tc.wantList) {
				t.Fatalf("got (%q, %#v), want (%q, %#v)", mode, list, tc.wantMode, tc.wantList)
			}
		})
	}
}

func TestLoadMigratesLegacyUpdateRepo(t *testing.T) {
	oldRepo := version.Repo
	version.Repo = LegacyUpdateRepo
	defer func() { version.Repo = oldRepo }()

	cases := []struct {
		name     string
		role     Role
		repoLine string
		want     string
	}{
		{"服务端显式旧仓库", RoleServer, "update_repo: " + LegacyUpdateRepo, ServerUpdateRepo},
		{"客户端显式旧仓库", RoleClient, "update_repo: " + LegacyUpdateRepo, ClientUpdateRepo},
		{"客户端空值继承旧构建", RoleClient, "", ClientUpdateRepo},
		{"自定义仓库保持不变", RoleServer, "update_repo: someone/private-mtun", "someone/private-mtun"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			content := "role: " + string(tc.role) + "\n"
			if tc.repoLine != "" {
				content += tc.repoLine + "\n"
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := Load(path, tc.role); err != nil {
				t.Fatalf("Load: %v", err)
			}
			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read migrated config: %v", err)
			}
			if !strings.Contains(string(saved), "update_repo: "+tc.want) {
				t.Fatalf("saved config 未包含 update_repo=%s:\n%s", tc.want, saved)
			}
			if got := ReadUpdateRepo(path); got != tc.want {
				t.Fatalf("ReadUpdateRepo = %q, want %q", got, tc.want)
			}
		})
	}
}
