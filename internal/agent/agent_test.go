// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package agent

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"mtun/internal/config"
	"mtun/pkg/proto"
)

func TestApplyRulesPersists(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "client.yaml")
	cfg := &config.Config{
		Role: config.RoleClient,
		Node: "n1",
		Publish: config.Publish{Rules: []proto.PublishRule{
			{Name: "old", Proto: "tcp", Listen: 10100, Target: "127.0.0.1:80"},
		}},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg, cfgPath, "", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.applyRules(proto.RulesSync{Rules: []proto.PublishRule{
		{Name: "new", Proto: "http", Listen: 10101, Target: "127.0.0.1:8080"},
	}})
	loaded, err := config.Load(cfgPath, config.RoleClient)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Publish.Rules) != 1 || loaded.Publish.Rules[0].Name != "new" {
		t.Fatalf("规则未落盘: %+v", loaded.Publish.Rules)
	}
	select {
	case <-a.restart:
		t.Fatal("applyRules 不应触发重连")
	default:
	}
}
