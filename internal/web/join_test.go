// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"mtun/internal/webx"
)

func TestJoinRequestAllowToggle(t *testing.T) {
	s := newPasswordTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}

	get := func() (bool, map[string]any) {
		rec := httptest.NewRecorder()
		s.handleJoinRequests(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET 失败: %d %s", rec.Code, rec.Body.String())
		}
		body := decodeBody(t, rec)
		allow, _ := body["allow_request"].(bool)
		return allow, body
	}

	allow, body := get()
	if !allow {
		t.Fatal("默认应允许无密码申请接入")
	}
	if _, ok := body["requests"].([]any); !ok {
		t.Fatalf("应返回申请数组: %v", body)
	}

	rec := postJSON(t, s.handleJoinRequests, `{"pin":"123456","action":"allow","allow":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("关闭申请开关失败: %d %s", rec.Code, rec.Body.String())
	}
	if allow, _ := get(); allow {
		t.Fatal("关闭后应返回 false")
	}

	rec = postJSON(t, s.handleJoinRequests, `{"pin":"999999","action":"allow","allow":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 PIN 应返回 401，得到 %d", rec.Code)
	}
	if allow, _ := get(); allow {
		t.Fatal("错误 PIN 不应修改开关")
	}
}

func TestJoinRequestApproveMissing(t *testing.T) {
	s := newPasswordTestServer(t)
	if err := webx.SetPINFile(s.pinPath(), "123456"); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, s.handleJoinRequests, `{"pin":"123456","action":"approve","id":"nope"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("批准不存在的申请应返回 409，得到 %d %s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, s.handleJoinRequests, `{"pin":"123456","action":"reject","id":"nope"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("拒绝不存在的申请应返回 409，得到 %d", rec.Code)
	}
	rec = postJSON(t, s.handleJoinRequests, `{"pin":"123456","action":"unknown"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知操作应返回 400，得到 %d", rec.Code)
	}
}
