// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package pending

import (
	"path/filepath"
	"testing"

	"mtun/pkg/proto"
)

func rule(name string, port int) proto.PublishRule {
	return proto.PublishRule{Name: name, Proto: "tcp", Listen: port, Target: "127.0.0.1:80"}
}

func TestApplyOps(t *testing.T) {
	base := []proto.PublishRule{rule("a", 10100), rule("b", 10101)}
	ops := []Op{
		{Op: "add", Rule: rule("c", 10102)},
		{Op: "update", Rule: proto.PublishRule{Name: "a", Proto: "http", Listen: 10110, Target: "127.0.0.1:8080"}},
		{Op: "delete", Rule: proto.PublishRule{Name: "b"}},
	}
	out, errs := Apply(base, ops)
	if len(errs) != 0 {
		t.Fatalf("不应有错误: %v", errs)
	}
	if len(out) != 2 || out[0].Name != "a" || out[0].Listen != 10110 || out[0].Proto != "http" || out[1].Name != "c" {
		t.Fatalf("应用结果不符: %+v", out)
	}
	if len(base) != 2 || base[0].Listen != 10100 {
		t.Fatal("不应修改调用方切片")
	}
}

func TestApplyErrors(t *testing.T) {
	base := []proto.PublishRule{rule("a", 10100)}
	for _, op := range []Op{
		{Op: "add", Rule: rule("a", 10101)},
		{Op: "update", Rule: rule("missing", 10102)},
		{Op: "delete", Rule: proto.PublishRule{Name: "missing"}},
		{Op: "rename", Rule: rule("x", 10103)},
	} {
		if _, errs := Apply(base, []Op{op}); len(errs) == 0 {
			t.Fatalf("期望操作失败: %+v", op)
		}
	}
}

func TestStoreQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.rules.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("n1", Op{Op: "add", Rule: rule("a", 10100)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("n1", Op{Op: "delete", Rule: proto.PublishRule{Name: "b"}}); err != nil {
		t.Fatal(err)
	}
	if s.Count("n1") != 2 {
		t.Fatalf("Count = %d", s.Count("n1"))
	}
	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Peek("n1")) != 2 {
		t.Fatalf("重载后队列长度 = %d", len(s2.Peek("n1")))
	}
	ops := s2.Take("n1")
	if len(ops) != 2 || ops[0].Op != "add" || ops[1].Op != "delete" {
		t.Fatalf("Take 结果不符: %+v", ops)
	}
	if s2.Count("n1") != 0 {
		t.Fatal("Take 后应清空")
	}
	s3, _ := Load(path)
	if len(s3.Peek("n1")) != 0 {
		t.Fatal("Take 应持久化清空")
	}
}
