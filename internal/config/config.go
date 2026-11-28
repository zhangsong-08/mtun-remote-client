// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"mtun/internal/secret"
	"mtun/internal/version"
	"mtun/pkg/proto"
)

// 在线更新仓库。旧版统一发布在 LegacyUpdateRepo，
// 过渡版本会把更新来源迁移到按角色拆分的新仓库。
const (
	LegacyUpdateRepo = "zhangsong-08/mtun"
	ServerUpdateRepo = "zhangsong-08/mtun-remote-server"
	ClientUpdateRepo = "zhangsong-08/mtun-remote-client"
)

type Role string

const (
	RoleServer Role = "server"
	RoleClient Role = "client"
)

type Config struct {
	Role      Role   `yaml:"role"`
	Node      string `yaml:"node"`
	Server    string `yaml:"server"`
	Control   string `yaml:"control"`
	WG        string `yaml:"wg"`
	Relay     string `yaml:"relay"`
	STUN      string `yaml:"stun"`
	Dashboard string `yaml:"dashboard"`
	// UpdateRepo 是在线更新所用的 GitHub 仓库（owner/name）。
	// 留空时回退到构建注入的 version.Repo 或环境变量 MTUN_UPDATE_REPO。
	UpdateRepo string    `yaml:"update_repo"`
	Panel      Panel     `yaml:"panel"`
	Overlay    Overlay   `yaml:"overlay"`
	Publish    Publish   `yaml:"publish"`
	Routes     []string  `yaml:"routes"`
	ACL        []ACLRule `yaml:"acl"`
}

// Panel 是管理面板的偏好设置，保存后需重启面板生效。
type Panel struct {
	SessionMinutes int `yaml:"session_minutes"`
	PageSize       int `yaml:"page_size"`
}

type Overlay struct {
	CIDR     string `yaml:"cidr"`
	ServerIP string `yaml:"server_ip"`
}

type Publish struct {
	PortRange string              `yaml:"port_range"`
	Rules     []proto.PublishRule `yaml:"rules"`
}

type ACLRule struct {
	Src   string `yaml:"src"`
	Dst   string `yaml:"dst"`
	Ports []int  `yaml:"ports"`
}

// DefaultYAML 返回指定角色的默认配置（YAML 文本），供 mtun default 生成初始配置。
func DefaultYAML(want Role) ([]byte, error) {
	c := &Config{}
	c.applyDefaults(want)
	return yaml.Marshal(c)
}

// Load 读取配置并按角色补齐默认值。want 决定默认 dashboard 与管理面校验。
func Load(path string, want Role) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults(want)
	if cfg.Role != want {
		return nil, fmt.Errorf("config role is %q, expected %q", cfg.Role, want)
	}
	if err := cfg.validate(want); err != nil {
		return nil, err
	}
	// 过渡升级：旧版安装配置的 update_repo 指向旧仓库，
	// 启动时自动迁移到按角色拆分的新仓库并落盘，保证后续在线更新可用。
	if resolved := ResolveUpdateRepo(cfg.UpdateRepo, want); resolved != "" && resolved != strings.TrimSpace(cfg.UpdateRepo) {
		cfg.UpdateRepo = resolved
		_ = Save(path, &cfg)
	}
	return &cfg, nil
}

// ResolveUpdateRepo 返回角色对应的在线更新仓库。
// 旧仓库 "zhangsong-08/mtun" 会映射到分仓后的新仓库；
// 配置为空时仅在当前二进制仍由旧仓库构建（version.Repo 为旧仓库）时迁移，
// 避免影响自行构建或其他来源的程序。
func ResolveUpdateRepo(repo string, role Role) string {
	target := ServerUpdateRepo
	if role == RoleClient {
		target = ClientUpdateRepo
	}
	repo = strings.TrimSpace(repo)
	switch {
	case repo == "":
		if strings.EqualFold(strings.TrimSpace(version.Repo), LegacyUpdateRepo) {
			return target
		}
		return ""
	case strings.EqualFold(repo, LegacyUpdateRepo):
		return target
	default:
		return repo
	}
}

// ReadDashboard 只读取配置文件中的 dashboard 地址，供命令行工具定位本地控制台。
func ReadDashboard(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var partial struct {
		Dashboard string `yaml:"dashboard"`
	}
	if err := yaml.Unmarshal(data, &partial); err != nil {
		return ""
	}
	return strings.TrimSpace(partial.Dashboard)
}

// ReadUpdateRepo 只读取配置文件中的 update_repo，供命令行更新检查使用。
// 与 Load 保持同一套旧仓库迁移规则。
func ReadUpdateRepo(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var partial struct {
		Role       Role   `yaml:"role"`
		UpdateRepo string `yaml:"update_repo"`
	}
	if err := yaml.Unmarshal(data, &partial); err != nil {
		return ""
	}
	return ResolveUpdateRepo(partial.UpdateRepo, partial.Role)
}

// Save 原子写回配置文件（客户端控制台修改服务端/规则后调用）。
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配置 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换配置 %s: %w", path, err)
	}
	return nil
}

// LoadJoinSecret 读取接入密码的本地哈希（seed）：
// 环境变量 MTUN_JOIN_TOKEN 优先，其次读取配置文件旁的 <config>.join。
//
// 兼容旧版明文文件：读到明文时先校验、转换成 seed 并原子写回，
// 之后磁盘上不再出现明文密码。
func LoadJoinSecret(cfgPath string) (string, error) {
	if v := cleanPassword(os.Getenv("MTUN_JOIN_TOKEN")); v != "" {
		if secret.IsSeed(v) {
			return v, nil
		}
		if err := ValidateJoinPassword(v); err != nil {
			return "", err
		}
		return secret.Seed(v), nil
	}
	path := JoinPasswordPath(cfgPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取接入密码: %w", err)
	}
	raw := cleanPassword(string(data))
	if raw == "" {
		return "", nil
	}
	if secret.IsSeed(raw) {
		return raw, nil
	}
	if err := ValidateJoinPassword(raw); err != nil {
		return "", err
	}
	seed := secret.Seed(raw)
	// 迁移失败不阻塞启动，下次启动会再次尝试。
	_ = writeJoinSecret(path, seed)
	return seed, nil
}

// SaveJoinPassword 校验明文密码后，仅把其本地哈希写入 <config>.join（0600）。
func SaveJoinPassword(cfgPath, password string) error {
	if err := ValidateJoinPassword(password); err != nil {
		return err
	}
	if err := writeJoinSecret(JoinPasswordPath(cfgPath), secret.Seed(password)); err != nil {
		return err
	}
	// 明文另存 0600，仅用于本机控制台「查看密码」；握手校验始终使用 .join 里的哈希。
	return os.WriteFile(JoinPlainPath(cfgPath), []byte(password+"\n"), 0o600)
}

// LoadJoinPlain 读取本机保存的接入密码明文（可能不存在，例如由随机哈希迁移来的配置）。
func LoadJoinPlain(cfgPath string) string {
	data, err := os.ReadFile(JoinPlainPath(cfgPath))
	if err != nil {
		return ""
	}
	return cleanPassword(string(data))
}

// JoinPlainPath 是接入密码明文文件路径（0600，仅本机可读）。
func JoinPlainPath(cfgPath string) string {
	return cfgPath + ".joinplain"
}

// SaveJoinSecret 直接保存已经计算好的 seed（例如随机生成密码后只保留哈希）。
func SaveJoinSecret(cfgPath, seed string) error {
	if !secret.IsSeed(seed) {
		return fmt.Errorf("接入密码哈希格式无效")
	}
	return writeJoinSecret(JoinPasswordPath(cfgPath), seed)
}

func writeJoinSecret(path, seed string) error {
	if err := os.WriteFile(path, []byte(seed+"\n"), 0o600); err != nil {
		return fmt.Errorf("保存接入密码: %w", err)
	}
	return nil
}

func JoinPasswordPath(cfgPath string) string {
	return cfgPath + ".join"
}

// ValidateJoinPassword 校验客户端接入服务端所用的共享密码。
func ValidateJoinPassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("接入密码至少需要 8 位")
	}
	if len(password) > 128 {
		return fmt.Errorf("接入密码不能超过 128 位")
	}
	return nil
}

// cleanPassword 去掉首尾空白与编辑器可能写入的 UTF-8 BOM。
func cleanPassword(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
}

// ValidateUpdateRepo 校验在线更新仓库地址，要求形如 owner/name。
func ValidateUpdateRepo(repo string) error {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil
	}
	if strings.ContainsAny(repo, " \t\r\n") {
		return fmt.Errorf("update_repo 不能包含空白字符")
	}
	if strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://") {
		return fmt.Errorf("update_repo 应为 owner/name，不要带协议前缀")
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("update_repo 格式应为 owner/name，例如 zhangsong-08/mtun")
	}
	return nil
}

// NormalizeVisibility 归一化规则可见范围，返回可持久化的值。
// 空值等价 all；nodes 模式要求至少一个节点 ID，并去重。
func NormalizeVisibility(visibility string, visibleTo []string) (string, []string, error) {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "", proto.VisibleAll:
		return proto.VisibleAll, nil, nil
	case proto.VisibleSelf:
		return proto.VisibleSelf, nil, nil
	case proto.VisibleNodes:
		seen := make(map[string]struct{}, len(visibleTo))
		out := make([]string, 0, len(visibleTo))
		for _, id := range visibleTo {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if len(id) > 128 {
				return "", nil, fmt.Errorf("可见节点 ID 不能超过 128 个字符")
			}
			if _, ok := seen[id]; ok {
				continue
			}
			if len(out) >= 100 {
				return "", nil, fmt.Errorf("可见节点不能超过 100 个")
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		if len(out) == 0 {
			return "", nil, fmt.Errorf("可见范围选择“指定节点”时至少填写一个节点 ID")
		}
		return proto.VisibleNodes, out, nil
	default:
		return "", nil, fmt.Errorf("可见范围仅支持 all/self/nodes，当前为 %q", visibility)
	}
}

// ValidateRule 校验客户端添加的转发规则。
func ValidateRule(rule proto.PublishRule) error {
	if rule.Name == "" {
		return fmt.Errorf("规则名称不能为空")
	}
	if len(rule.Name) > 64 {
		return fmt.Errorf("规则名称不能超过 64 个字符")
	}
	switch {
	case rule.Proto == "", rule.Proto == "tcp", rule.Proto == "http":
	default:
		return fmt.Errorf("仅支持 tcp/http，当前为 %q", rule.Proto)
	}
	if rule.Listen < 10000 || rule.Listen > 65535 {
		return fmt.Errorf("公网端口需在 10000-65535")
	}
	if rule.Target == "" {
		return fmt.Errorf("内网目标不能为空")
	}
	if _, _, err := net.SplitHostPort(rule.Target); err != nil {
		return fmt.Errorf("内网目标需为 host:port：%w", err)
	}
	if _, _, err := NormalizeVisibility(rule.Visibility, rule.VisibleTo); err != nil {
		return err
	}
	return nil
}

func (c *Config) applyDefaults(want Role) {
	if c.Role == "" {
		c.Role = want
	}
	if c.Control == "" {
		c.Control = ":10000"
	}
	if c.WG == "" {
		c.WG = ":10001"
	}
	if c.Relay == "" {
		c.Relay = ":10002"
	}
	if c.STUN == "" {
		c.STUN = ":10003"
	}
	if c.Overlay.CIDR == "" {
		c.Overlay.CIDR = "100.64.0.0/10"
	}
	if c.Overlay.ServerIP == "" {
		c.Overlay.ServerIP = "100.64.0.1"
	}
	if c.Publish.PortRange == "" {
		c.Publish.PortRange = "10100-10999"
	}
	switch want {
	case RoleServer:
		if c.Dashboard == "" {
			c.Dashboard = "127.0.0.1:10004"
		}
		if c.Panel.SessionMinutes <= 0 {
			c.Panel.SessionMinutes = 720
		}
		if c.Panel.PageSize <= 0 {
			c.Panel.PageSize = 25
		}
	case RoleClient:
		if c.Dashboard == "" {
			c.Dashboard = "127.0.0.1:10005"
		}
		if c.Node == "" {
			if host, err := os.Hostname(); err == nil {
				c.Node = host
			}
		}
	}
}

func (c *Config) validate(want Role) error {
	if _, err := netip.ParsePrefix(c.Overlay.CIDR); err != nil {
		return fmt.Errorf("overlay.cidr: %w", err)
	}
	if _, err := netip.ParseAddr(c.Overlay.ServerIP); err != nil {
		return fmt.Errorf("overlay.server_ip: %w", err)
	}
	if want == RoleClient && c.Node == "" {
		return fmt.Errorf("node is required for client")
	}
	if c.UpdateRepo != "" {
		if err := ValidateUpdateRepo(c.UpdateRepo); err != nil {
			return err
		}
	}
	if c.Dashboard != "" {
		if err := ValidateDashboard(c.Dashboard); err != nil {
			return err
		}
	}
	for _, rule := range c.Publish.Rules {
		if err := ValidateRule(rule); err != nil {
			return fmt.Errorf("publish %q: %w", rule.Name, err)
		}
	}
	return nil
}

// ValidateDashboard 校验管理面板地址。
// 默认监听回环；允许显式配置为 0.0.0.0:10004 或服务器 IP，
// 此时面板可从远程访问，请务必配合强密码与防火墙来源限制。
func ValidateDashboard(addr string) error {
	return validateListenAddr("dashboard", addr)
}

// ValidateControl 校验控制面监听地址。
func ValidateControl(addr string) error {
	return validateListenAddr("control", addr)
}

func validateListenAddr(field, addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %q: %w", field, addr, err)
	}
	if port == "" {
		return fmt.Errorf("%s %q: 缺少端口", field, addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return fmt.Errorf("%s %q: 端口非法", field, addr)
	}
	return nil
}
