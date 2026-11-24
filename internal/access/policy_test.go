// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package access

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

func at(hour, minute int) time.Time {
	return time.Date(2026, 11, 24, hour, minute, 0, 0, time.Local)
}

func TestCheckBan(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "x.access.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("n1", Policy{Permanent: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("n1", "linux", "1.2.3.4", at(12, 0)); err == nil {
		t.Fatal("永久封禁应被拒绝")
	}
	if err := s.Set("n2", Policy{Until: at(13, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("n2", "linux", "1.2.3.4", at(12, 0)); err == nil {
		t.Fatal("封禁期内应被拒绝")
	}
	if err := s.Check("n2", "linux", "1.2.3.4", at(14, 0)); err != nil {
		t.Fatalf("封禁到期后应放行: %v", err)
	}
}

func TestCheckCIDR(t *testing.T) {
	s, _ := Load("")
	if err := s.Set("n", Policy{DenyCIDR: []string{"10.0.0.0/8"}, AllowCIDR: []string{"192.168.0.0/16", "2001:db8::/32"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("n", "", "10.1.2.3", at(12, 0)); err == nil {
		t.Fatal("拒绝段应被拒绝")
	}
	if err := s.Check("n", "", "172.16.0.1", at(12, 0)); err == nil {
		t.Fatal("不在允许段应被拒绝")
	}
	if err := s.Check("n", "", "192.168.1.5", at(12, 0)); err != nil {
		t.Fatalf("允许段应放行: %v", err)
	}
	if err := s.Check("n", "", "2001:db8::1", at(12, 0)); err != nil {
		t.Fatalf("IPv6 允许段应放行: %v", err)
	}
	if err := s.Check("n", "", "10.0.0.9", at(12, 0)); err == nil {
		t.Fatal("拒绝优先级应高于允许")
	}
}

func TestCheckDailyWindow(t *testing.T) {
	s, _ := Load("")
	if err := s.Set("n", Policy{Daily: []Window{{Start: "22:00", End: "06:00"}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{
		{at(23, 0), true},
		{at(5, 59), true},
		{at(6, 0), false},
		{at(12, 0), false},
	} {
		err := s.Check("n", "", "1.2.3.4", tc.t)
		if (err == nil) != tc.want {
			t.Fatalf("%s 期望放行=%v，实际 err=%v", tc.t.Format("15:04"), tc.want, err)
		}
	}
}

func TestCheckOS(t *testing.T) {
	s, _ := Load("")
	if err := s.Set("n", Policy{AllowOS: []string{"windows"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("n", "", "1.2.3.4", at(12, 0)); err == nil {
		t.Fatal("未上报系统应被拒绝")
	}
	if err := s.Check("n", "linux", "1.2.3.4", at(12, 0)); err == nil {
		t.Fatal("非允许系统应被拒绝")
	}
	if err := s.Check("n", "windows", "1.2.3.4", at(12, 0)); err != nil {
		t.Fatalf("允许系统应放行: %v", err)
	}
}

func TestPolicyValidate(t *testing.T) {
	for _, p := range []Policy{
		{Daily: []Window{{Start: "25:00", End: "06:00"}}},
		{Daily: []Window{{Start: "08:00", End: "bad"}}},
		{AllowCIDR: []string{"not-an-ip"}},
		{DenyCIDR: []string{"1.2.3.4/99"}},
		{AllowOS: []string{"plan9"}},
	} {
		if err := p.Validate(); err == nil {
			t.Fatalf("期望校验失败: %+v", p)
		}
	}
}

func TestStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.access.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{Permanent: true, DenyCIDR: []string{"10.0.0.0/8"}, AllowOS: []string{"linux"}}
	if err := s.Set("n1", p); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get("n1")
	if !got.Permanent || len(got.DenyCIDR) != 1 || len(got.AllowOS) != 1 {
		t.Fatalf("持久化结果不一致: %+v", got)
	}
	if err := s2.Clear("n1"); err != nil {
		t.Fatal(err)
	}
	s3, _ := Load(path)
	if len(s3.List()) != 0 {
		t.Fatal("Clear 后不应残留策略")
	}
}

func TestParsePrefixBareIP(t *testing.T) {
	p, err := parsePrefix("1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Contains(netip.MustParseAddr("1.2.3.4")) || p.Bits() != 32 {
		t.Fatalf("裸 IP 应转为 /32: %v", p)
	}
}
