// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package config

import "testing"

func TestValidateControl(t *testing.T) {
	valid := []string{"", ":10000", "0.0.0.0:10000", "127.0.0.1:12000", "[::]:10000"}
	for _, addr := range valid {
		if err := ValidateControl(addr); err != nil {
			t.Fatalf("%q 应通过: %v", addr, err)
		}
	}
	invalid := []string{"10000", ":0", ":70000", "host:port"}
	for _, addr := range invalid {
		if err := ValidateControl(addr); err == nil {
			t.Fatalf("%q 应失败", addr)
		}
	}
}
