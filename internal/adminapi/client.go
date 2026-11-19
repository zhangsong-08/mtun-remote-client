// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 通过本地回环控制台的管理 API 操作服务端或客户端。
type Client struct {
	base  string
	token string
	http  *http.Client
}

type ClientStatus struct {
	Version          string `json:"version"`
	Node             string `json:"node"`
	Server           string `json:"server"`
	Dashboard        string `json:"dashboard"`
	HasAccess        bool   `json:"has_access"`
	Connected        bool   `json:"connected"`
	Error            string `json:"error"`
	NodeID           string `json:"node_id"`
	OverlayIP        string `json:"overlay_ip"`
	Path             string `json:"path"`
	ConnectedSeconds int64  `json:"connected_seconds"`
	Rules            []Rule `json:"rules"`
}

type Rule struct {
	Name    string `json:"name"`
	Proto   string `json:"proto"`
	Listen  int    `json:"listen"`
	Target  string `json:"target"`
	State   string `json:"state"`
	Message string `json:"message"`
}

type ServerStatus struct {
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Nodes         int    `json:"nodes"`
	Offline       int    `json:"offline"`
	Direct        int    `json:"direct"`
	Relay         int    `json:"relay"`
	PublishRules  int    `json:"publish_rules"`
}

type Node struct {
	ID              string   `json:"id"`
	OverlayIP       string   `json:"overlay_ip"`
	Path            string   `json:"path"`
	Online          bool     `json:"online"`
	Endpoints       []string `json:"endpoints"`
	Publish         int      `json:"publish"`
	LastSeenSeconds int64    `json:"last_seen_seconds"`
}

func New(base, token string) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Get 返回原始 JSON，便于命令行按角色自行解析。
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *Client) ClientStatus(ctx context.Context) (ClientStatus, error) {
	var st ClientStatus
	return st, c.get(ctx, "/api/status", &st)
}

func (c *Client) ServerStatus(ctx context.Context) (ServerStatus, error) {
	var st ServerStatus
	return st, c.get(ctx, "/api/status", &st)
}

func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var nodes []Node
	return nodes, c.get(ctx, "/api/nodes", &nodes)
}

func (c *Client) SetServer(ctx context.Context, server, key string) error {
	return c.post(ctx, "/api/server", map[string]string{"server": server, "key": key})
}

func (c *Client) AddRule(ctx context.Context, rule Rule) error {
	return c.post(ctx, "/api/rules", map[string]any{
		"name":   rule.Name,
		"proto":  rule.Proto,
		"listen": rule.Listen,
		"target": rule.Target,
	})
}

func (c *Client) DeleteRule(ctx context.Context, name string) error {
	return c.post(ctx, "/api/rules/delete", map[string]string{"name": name})
}

func (c *Client) Kick(ctx context.Context, nodeID string, banSeconds int) error {
	return c.post(ctx, "/api/nodes/kick", map[string]any{"id": nodeID, "ban_seconds": banSeconds})
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (c *Client) post(ctx context.Context, path string, in any) error {
	_, err := c.do(ctx, http.MethodPost, path, in)
	return err
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接本地控制台失败（服务是否已启动？）: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return data, nil
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("API 凭据无效：请使用 -c 指定配置文件以读取 .apitoken，或使用 -token 指定")
	default:
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("控制台返回 %s", resp.Status)
	}
}
