package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"mtun/pkg/proto"
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
	return &cfg, nil
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
func ReadUpdateRepo(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var partial struct {
		UpdateRepo string `yaml:"update_repo"`
	}
	if err := yaml.Unmarshal(data, &partial); err != nil {
		return ""
	}
	return strings.TrimSpace(partial.UpdateRepo)
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

// LoadJoinPassword 读取客户端接入密码（与服务端共享）：
// 环境变量 MTUN_JOIN_TOKEN 优先，其次读取配置文件旁的 <config>.join。
func LoadJoinPassword(cfgPath string) (string, error) {
	if v := cleanPassword(os.Getenv("MTUN_JOIN_TOKEN")); v != "" {
		return v, nil
	}
	data, err := os.ReadFile(JoinPasswordPath(cfgPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取接入密码: %w", err)
	}
	return cleanPassword(string(data)), nil
}

// SaveJoinPassword 保存接入密码到 <config>.join（0600），不写进 YAML。
func SaveJoinPassword(cfgPath, password string) error {
	if err := ValidateJoinPassword(password); err != nil {
		return err
	}
	if err := os.WriteFile(JoinPasswordPath(cfgPath), []byte(password+"\n"), 0o600); err != nil {
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
		if err := validateLoopback(c.Dashboard); err != nil {
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

// ValidateDashboard 校验管理面板地址：只允许回环地址。
func ValidateDashboard(addr string) error {
	return validateLoopback(addr)
}

// validateLoopback 强制管理面只能绑定回环地址。
func validateLoopback(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("dashboard %q: %w", addr, err)
	}
	if port == "" {
		return fmt.Errorf("dashboard %q: missing port", addr)
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	}
	return fmt.Errorf("dashboard must bind loopback (127.0.0.1), got %q", host)
}
