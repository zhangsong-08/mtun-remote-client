// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package secret

import "testing"

func TestSeedIsStableAndNotPlaintext(t *testing.T) {
	const pw = "Q5bSojEJHuHjYzOS3gFL7Ax3"
	a := Seed(pw)
	b := Seed(pw)
	if a != b {
		t.Fatal("同一密码应得到相同 seed")
	}
	if a == pw || len(a) == 0 {
		t.Fatalf("seed 不应等于明文: %q", a)
	}
	if !IsSeed(a) {
		t.Fatalf("seed 格式异常: %q", a)
	}
	if Seed(pw+"x") == a {
		t.Fatal("不同密码应得到不同 seed")
	}
	if IsSeed(pw) {
		t.Fatal("明文不应被误判为 seed")
	}
}

func TestNormalizeAndEqual(t *testing.T) {
	const pw = "password-123456"
	seed := Seed(pw)
	if got := Normalize(pw); got != seed {
		t.Fatalf("明文规范化结果不一致: %q", got)
	}
	if got := Normalize(seed); got != seed {
		t.Fatalf("seed 规范化应保持不变: %q", got)
	}
	if !Equal(seed, Seed(pw)) {
		t.Fatal("相同密码的 seed 应相等")
	}
	if Equal(seed, Seed(pw+"x")) {
		t.Fatal("不同密码的 seed 不应相等")
	}
	if Equal("", "") {
		t.Fatal("空值不应被视为相等")
	}
}
