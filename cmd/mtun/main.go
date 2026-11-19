// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"mtun/internal/adminapi"
	"mtun/internal/app"
	"mtun/internal/autostart"
	"mtun/internal/config"
	"mtun/internal/platform"
	"mtun/internal/selfupdate"
	"mtun/internal/version"
	"mtun/internal/webx"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	if len(args) == 0 {
		usage()
		return nil
	}

	selfupdate.CleanupOld()

	switch args[0] {
	case "server":
		fs := flag.NewFlagSet("server", flag.ExitOnError)
		cfgPath := fs.String("c", "configs/server.example.yaml", "配置文件路径")
		_ = fs.Parse(args[1:])
		cfg, err := config.Load(*cfgPath, config.RoleServer)
		if err != nil {
			return err
		}
		applyUpdateRepo(cfg)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return app.RunServer(ctx, cfg, *cfgPath, logger)

	case "client":
		fs := flag.NewFlagSet("client", flag.ExitOnError)
		cfgPath := fs.String("c", "configs/client.example.yaml", "配置文件路径")
		_ = fs.Parse(args[1:])
		cfg, err := config.Load(*cfgPath, config.RoleClient)
		if err != nil {
			return err
		}
		applyUpdateRepo(cfg)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return app.RunClient(ctx, cfg, *cfgPath, logger)

	case "status":
		return runStatus(args[1:])

	case "watch":
		return runWatch(args[1:])

	case "nodes":
		return runNodes(args[1:])

	case "kick":
		return runKick(args[1:])

	case "connect":
		return runConnect(args[1:])

	case "passwd":
		return runPasswd(args[1:])

	case "access":
		return runAccess(args[1:])

	case "rule":
		return runRule(args[1:])

	case "web":
		fs := flag.NewFlagSet("web", flag.ExitOnError)
		url := fs.String("url", "http://127.0.0.1:10004/", "控制台地址")
		_ = fs.Parse(args[1:])
		if err := openBrowser(*url); err != nil {
			fmt.Println(*url)
			return nil
		}
		fmt.Println("已打开", *url)
		return nil

	case "service":
		fmt.Println(platform.ServiceHint())
		return nil

	case "update":
		return runUpdate(args[1:], logger)

	case "default":
		return runDefault(args[1:])

	case "check":
		return runCheck(args[1:])

	case "__restart":
		return selfupdate.RunRestartHelper(logger)

	case "__autostart":
		return runAutostart(args[1:])

	case "version", "-v", "--version":
		fmt.Println("mtun", version.Version)
		return nil

	case "help", "-h", "--help":
		usage()
		return nil

	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Print(`mtun - 内网穿透

三个独立密码:
  mtun passwd -c configs/server.example.yaml   服务端管理密码（管理员在终端设置，启动前必须）
  mtun passwd -c configs/client.example.yaml   客户端登录密码（也可首次打开控制台时设置）
  mtun access -c configs/server.example.yaml   客户端接入密码（客户端连接服务端时填写）

运行:
  mtun server -c configs/server.example.yaml
  mtun client -c configs/client.example.yaml

终端管理（通过本地回环控制台）:
  mtun status  [-url 地址] [-c 配置文件]                查看运行状态
  mtun watch   [-url 地址] [-c 配置文件] [-interval 2s] 实时刷新状态/节点（Ctrl+C 退出）
  mtun nodes   [-url 地址] [-c 配置文件]                查看服务端节点
  mtun kick    -id 节点ID [-ban 60s] [-c 服务端配置]    强制下线并临时封禁该节点
  mtun connect -server host:port [-key 接入密码] [-c 客户端配置]
  mtun rule add -name 名称 -listen 10101 -target 127.0.0.1:5000 [-proto tcp] [-c 客户端配置]
  mtun rule list   [-c 客户端配置]
  mtun rule delete -name 名称 [-c 客户端配置]

其他:
  mtun default server|client          输出默认配置（安装脚本使用）
  mtun check -c 配置文件 [-role server|client]   校验配置是否有效
  mtun update [-check] [-c 配置文件]   从 GitHub 在线更新（不修改配置文件）
  mtun web         打开本地控制台（服务端 10004，客户端 10005）
  mtun service     输出当前平台的服务安装说明
  mtun version     输出版本

在线更新仓库（owner/name）按以下优先级确定：
  环境变量 MTUN_UPDATE_REPO > 配置文件 update_repo > 构建注入的 version.Repo

终端管理命令使用 <配置文件>.apitoken（0600）作为本机 API 凭据；
未指定 -url 时按 -c 配置中的 dashboard 或 10004/10005 自动探测。
`)
}

type targetOpts struct {
	url   string
	token string
	cfg   string
}

func targetFlags(fs *flag.FlagSet) *targetOpts {
	o := &targetOpts{}
	fs.StringVar(&o.url, "url", "", "控制台地址，默认按配置或 10004/10005 自动探测")
	fs.StringVar(&o.token, "token", "", "API 凭据，默认读 MTUN_API_TOKEN 或 -c 对应的 .apitoken")
	fs.StringVar(&o.cfg, "c", "", "配置文件路径，用于定位控制台和读取 API 凭据")
	return o
}

func (o *targetOpts) resolve() (string, *adminapi.Client, error) {
	base, err := resolveBase(o.url, o.cfg)
	if err != nil {
		return "", nil, err
	}
	return base, adminapi.New(base, resolveAPIToken(o.token, o.cfg)), nil
}

func resolveBase(rawURL, cfgPath string) (string, error) {
	if rawURL != "" {
		return normalizeBase(rawURL), nil
	}
	if cfgPath != "" {
		if dash := config.ReadDashboard(cfgPath); dash != "" {
			return normalizeBase(dash), nil
		}
	}
	for _, cand := range []string{"http://127.0.0.1:10004", "http://127.0.0.1:10005"} {
		if reachable(cand) {
			return cand, nil
		}
	}
	return "", errors.New("未发现本地控制台：请先启动服务端或客户端，或用 -url 指定地址")
}

func normalizeBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return strings.TrimRight(raw, "/")
}

func reachable(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func resolveAPIToken(explicit, cfgPath string) string {
	if explicit != "" {
		return explicit
	}
	if v := cleanSecret(os.Getenv("MTUN_API_TOKEN")); v != "" {
		return v
	}
	if cfgPath != "" {
		if data, err := os.ReadFile(cfgPath + ".apitoken"); err == nil {
			return cleanSecret(string(data))
		}
	}
	return ""
}

func cleanSecret(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
}

func requestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	opts := targetFlags(fs)
	_ = fs.Parse(args)

	base, api, err := opts.resolve()
	if err != nil {
		return err
	}
	ctx, cancel := requestContext()
	defer cancel()

	data, err := api.Get(ctx, "/api/status")
	if err != nil {
		return err
	}
	fmt.Println("控制台:", base)
	return renderStatus(data)
}

// renderStatus 按返回内容自动识别客户端或服务端并打印。
func renderStatus(data []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("解析控制台返回: %w", err)
	}
	if _, isClient := probe["connected"]; isClient {
		var st adminapi.ClientStatus
		if err := json.Unmarshal(data, &st); err != nil {
			return err
		}
		printClientStatus(st)
		return nil
	}
	var st adminapi.ServerStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	printServerStatus(st)
	return nil
}

// runWatch 周期性刷新状态；服务端额外展示节点列表。
func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	opts := targetFlags(fs)
	interval := fs.Duration("interval", 2*time.Second, "刷新间隔")
	_ = fs.Parse(args)

	base, api, err := opts.resolve()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		fmt.Print("\x1b[H\x1b[2J")
		fmt.Printf("mtun 实时状态   控制台: %s   更新: %s   Ctrl+C 退出\n\n",
			base, time.Now().Format("15:04:05"))

		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		data, err := api.Get(reqCtx, "/api/status")
		cancel()
		if err != nil {
			fmt.Println("读取失败:", err)
		} else if err := renderStatus(data); err != nil {
			fmt.Println("解析失败:", err)
		} else {
			var probe map[string]json.RawMessage
			if json.Unmarshal(data, &probe) == nil {
				if _, isClient := probe["connected"]; !isClient {
					nodesCtx, cancelNodes := context.WithTimeout(ctx, 5*time.Second)
					nodes, err := api.Nodes(nodesCtx)
					cancelNodes()
					fmt.Println()
					if err != nil {
						fmt.Println("节点读取失败:", err)
					} else {
						printNodes(nodes)
					}
				}
			}
		}

		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-time.After(*interval):
		}
	}
}

// runKick 强制节点下线，并在封禁期内拒绝其重连。
func runKick(args []string) error {
	fs := flag.NewFlagSet("kick", flag.ExitOnError)
	opts := targetFlags(fs)
	id := fs.String("id", "", "节点 ID（见 mtun nodes）")
	ban := fs.Duration("ban", time.Minute, "封禁时长，0 表示允许立即重连")
	_ = fs.Parse(args)

	if strings.TrimSpace(*id) == "" {
		return errors.New("必须用 -id 指定节点 ID（可通过 mtun nodes 查看）")
	}
	_, api, err := opts.resolve()
	if err != nil {
		return err
	}
	ctx, cancel := requestContext()
	defer cancel()

	if err := api.Kick(ctx, strings.TrimSpace(*id), int(ban.Seconds())); err != nil {
		return err
	}
	if *ban > 0 {
		fmt.Printf("已踢下线节点 %s，%s 内拒绝重连\n", *id, *ban)
	} else {
		fmt.Printf("已踢下线节点 %s\n", *id)
	}
	return nil
}

func printClientStatus(st adminapi.ClientStatus) {
	fmt.Println("连接状态:", pick(st.Connected, "已连接", "未连接"))
	if st.Server != "" {
		fmt.Println("服务端:", st.Server)
	}
	id := st.NodeID
	if id == "" {
		id = st.Node
	}
	if id != "" {
		fmt.Println("节点:", id)
	}
	if st.OverlayIP != "" {
		fmt.Println("覆盖 IP:", st.OverlayIP)
	}
	if st.Connected {
		fmt.Println("连接时长:", fmtDuration(st.ConnectedSeconds))
	}
	if st.Error != "" {
		fmt.Println("错误:", st.Error)
	}
	fmt.Println()
	printRules(st.Rules)
}

func printServerStatus(st adminapi.ServerStatus) {
	fmt.Println("版本:", st.Version)
	fmt.Println("运行时长:", fmtDuration(st.UptimeSeconds))
	fmt.Printf("在线节点: %d  离线: %d\n", st.Nodes, st.Offline)
	fmt.Printf("直连: %d  中继: %d\n", st.Direct, st.Relay)
	fmt.Println("发布规则:", st.PublishRules)
}

func runNodes(args []string) error {
	fs := flag.NewFlagSet("nodes", flag.ExitOnError)
	opts := targetFlags(fs)
	_ = fs.Parse(args)

	base, api, err := opts.resolve()
	if err != nil {
		return err
	}
	ctx, cancel := requestContext()
	defer cancel()

	nodes, err := api.Nodes(ctx)
	if err != nil {
		return err
	}
	fmt.Println("控制台:", base)
	printNodes(nodes)
	return nil
}

func printNodes(nodes []adminapi.Node) {
	if len(nodes) == 0 {
		fmt.Println("（暂无节点注册）")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "节点\t覆盖 IP\t路径\t状态\t发布\t最后心跳\t端点")
	for _, n := range nodes {
		path := "中继"
		if n.Path == "direct" {
			path = "直连"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			n.ID, n.OverlayIP, path, pick(n.Online, "在线", "离线"),
			n.Publish, fmtDuration(n.LastSeenSeconds), strings.Join(n.Endpoints, ", "))
	}
	_ = w.Flush()
}

func runConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	opts := targetFlags(fs)
	server := fs.String("server", "", "服务端地址，如 1.2.3.4:10000")
	key := fs.String("key", "", "客户端接入密码；首次必填，之后留空表示不修改")
	_ = fs.Parse(args)

	if strings.TrimSpace(*server) == "" {
		return errors.New("必须用 -server 指定服务端地址，如 -server 1.2.3.4:10000")
	}
	serverAddr := strings.TrimSpace(*server)

	// 客户端已在运行：通过本地控制台热更新，立即触发重连
	if _, api, err := opts.resolve(); err == nil {
		ctx, cancel := requestContext()
		updateErr := api.SetServer(ctx, serverAddr, *key)
		cancel()
		if updateErr == nil {
			fmt.Println("已保存服务端配置，客户端将自动重连；可用 mtun status 查看结果")
			return nil
		}
	}

	// 客户端尚未启动：直接写入配置文件，启动后生效
	if opts.cfg == "" {
		return errors.New("本地控制台未运行。请先启动客户端，或加 -c client.yaml 直接写入配置")
	}
	if _, err := os.Stat(opts.cfg); os.IsNotExist(err) {
		data, derr := config.DefaultYAML(config.RoleClient)
		if derr != nil {
			return derr
		}
		if werr := os.WriteFile(opts.cfg, data, 0o600); werr != nil {
			return werr
		}
	}
	cfg, err := config.Load(opts.cfg, config.RoleClient)
	if err != nil {
		return err
	}
	cfg.Server = serverAddr
	if err := config.Save(opts.cfg, cfg); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if k := strings.TrimSpace(*key); k != "" {
		if err := config.SaveJoinPassword(opts.cfg, k); err != nil {
			return err
		}
	}
	fmt.Printf("客户端未运行，配置已写入 %s\n启动后生效：mtun client -c %s\n", opts.cfg, opts.cfg)
	return nil
}

func runAutostart(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: mtun __autostart status|enable|disable|remove -role server|client -c 配置文件")
	}
	action := args[0]
	fs := flag.NewFlagSet("__autostart", flag.ContinueOnError)
	role := fs.String("role", "", "角色：server 或 client")
	cfgPath := fs.String("c", "", "配置文件路径")
	start := fs.Bool("start", false, "启用后立即启动")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("未知参数: %s", strings.Join(fs.Args(), " "))
	}
	if *start && action != "enable" {
		return errors.New("-start 仅可用于 enable")
	}
	manager, err := autostart.New(*role, *cfgPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var status autostart.Status
	switch action {
	case "status":
		status, err = manager.Status(ctx)
	case "enable":
		status, err = manager.Set(ctx, true)
		if err == nil && *start {
			err = manager.Start(ctx)
		}
	case "disable":
		status, err = manager.Set(ctx, false)
	case "remove":
		if err = manager.Remove(ctx); err == nil {
			return json.NewEncoder(os.Stdout).Encode(map[string]bool{"removed": true})
		}
	default:
		return fmt.Errorf("未知的 __autostart 子命令 %q", action)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(status)
}

// runDefault 输出指定角色的默认配置，便于脚本生成初始配置。
func runDefault(args []string) error {
	role := config.RoleServer
	if len(args) > 0 {
		switch args[0] {
		case "server":
			role = config.RoleServer
		case "client":
			role = config.RoleClient
		default:
			return fmt.Errorf("用法: mtun default server|client")
		}
	}
	data, err := config.DefaultYAML(role)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

// runCheck 校验配置文件能否被正确加载，不启动服务。
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	cfgPath := fs.String("c", "", "配置文件路径")
	role := fs.String("role", string(config.RoleServer), "角色：server 或 client")
	_ = fs.Parse(args)

	if strings.TrimSpace(*cfgPath) == "" {
		return errors.New("请用 -c 指定配置文件")
	}
	want := config.RoleServer
	if *role == string(config.RoleClient) {
		want = config.RoleClient
	}
	cfg, err := config.Load(*cfgPath, want)
	if err != nil {
		return err
	}
	fmt.Printf("配置有效\n  角色:     %s\n  控制面:   %s\n  中继:     %s\n  管理面板: %s\n  转发端口: %s\n",
		cfg.Role, cfg.Control, cfg.Relay, cfg.Dashboard, cfg.Publish.PortRange)
	return nil
}

// applyUpdateRepo 把配置中的 update_repo 注入环境变量，
// 供本进程内的在线更新读取；已显式设置的环境变量保持更高优先级。
func applyUpdateRepo(cfg *config.Config) {
	repo := strings.TrimSpace(cfg.UpdateRepo)
	if repo == "" {
		return
	}
	if strings.TrimSpace(os.Getenv("MTUN_UPDATE_REPO")) != "" {
		return
	}
	_ = os.Setenv("MTUN_UPDATE_REPO", repo)
}

// runUpdate 从 GitHub Release 检查并更新当前程序。
func runUpdate(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "只检查是否有新版本")
	cfgPath := fs.String("c", "", "配置文件路径，用于读取 update_repo（可选）")
	_ = fs.Parse(args)

	if *cfgPath != "" {
		if repo := config.ReadUpdateRepo(*cfgPath); repo != "" {
			_ = os.Setenv("MTUN_UPDATE_REPO", repo)
		}
	}

	u := selfupdate.New(logger)
	if u.Repo == "" {
		return errors.New("未配置更新仓库：请设置 MTUN_UPDATE_REPO=owner/repo（或构建时注入 version.Repo）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	rel, available, err := u.Check(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("当前版本: %s\n最新版本: %s\n", u.Current, rel.Version)
	if !available {
		fmt.Println("已是最新版本")
		return nil
	}
	if *checkOnly {
		fmt.Println("发现新版本，执行 mtun update 即可更新")
		return nil
	}

	fmt.Println("正在下载并替换程序...")
	exe, err := u.Apply(ctx, rel)
	if err != nil {
		return err
	}
	fmt.Printf("已更新到 %s：%s\n配置文件与规则均未变动；重启服务后生效\n", rel.Version, exe)
	return nil
}

// runPasswd 设置管理面登录密码（服务端必须、客户端可选）。
func runPasswd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	cfgPath := fs.String("c", "", "配置文件路径，如 configs/server.example.yaml")
	user := fs.String("user", "admin", "登录用户名")
	password := fs.String("password", "", "新密码；不填则交互输入（也可用 MTUN_NEW_PASSWORD）")
	_ = fs.Parse(args)

	if strings.TrimSpace(*cfgPath) == "" {
		return errors.New("请用 -c 指定配置文件，如 -c configs/server.example.yaml")
	}
	pw, err := newPassword(*password, "设置管理密码")
	if err != nil {
		return err
	}
	if err := webx.SetPasswordFile(*cfgPath+".passwd", *user, pw); err != nil {
		return err
	}
	fmt.Printf("已设置管理密码（用户 %s），文件: %s.passwd\n", *user, *cfgPath)
	return nil
}

// runAccess 设置客户端接入密码，客户端连接服务端时需填写同一密码。
func runAccess(args []string) error {
	fs := flag.NewFlagSet("access", flag.ExitOnError)
	cfgPath := fs.String("c", "", "服务端配置文件路径，如 configs/server.example.yaml")
	password := fs.String("password", "", "接入密码；不填则交互输入（也可用 MTUN_NEW_PASSWORD）")
	_ = fs.Parse(args)

	if strings.TrimSpace(*cfgPath) == "" {
		return errors.New("请用 -c 指定服务端配置文件，如 -c configs/server.example.yaml")
	}
	pw, err := newPassword(*password, "设置客户端接入密码")
	if err != nil {
		return err
	}
	if err := config.SaveJoinPassword(*cfgPath, pw); err != nil {
		return err
	}
	fmt.Println("接入密码已保存；客户端添加服务端时填写同一密码（服务端重启后生效）")
	return nil
}

// newPassword 读取新密码：参数 > MTUN_NEW_PASSWORD > 交互输入两次。
func newPassword(flagValue, label string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v := os.Getenv("MTUN_NEW_PASSWORD"); v != "" {
		return v, nil
	}
	pw, err := promptPassword(label + ": ")
	if err != nil {
		return "", err
	}
	confirm, err := promptPassword("再次输入: ")
	if err != nil {
		return "", err
	}
	if pw != confirm {
		return "", errors.New("两次输入的密码不一致")
	}
	return pw, nil
}

func promptPassword(label string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("当前不是交互终端，请使用 -password 或 MTUN_NEW_PASSWORD")
	}
	fmt.Print(label)
	data, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("读取密码: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func runRule(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: mtun rule add|list|delete ...")
	}
	switch args[0] {
	case "add":
		return runRuleAdd(args[1:])
	case "list":
		return runRuleList(args[1:])
	case "delete":
		return runRuleDelete(args[1:])
	default:
		return fmt.Errorf("未知的 rule 子命令 %q（可用: add/list/delete）", args[0])
	}
}

func runRuleAdd(args []string) error {
	fs := flag.NewFlagSet("rule add", flag.ExitOnError)
	opts := targetFlags(fs)
	name := fs.String("name", "", "规则名称")
	proto := fs.String("proto", "tcp", "协议：tcp 或 http")
	listen := fs.Int("listen", 0, "服务端公网端口（10000-65535）")
	target := fs.String("target", "", "内网目标，如 127.0.0.1:5000")
	_ = fs.Parse(args)

	if strings.TrimSpace(*name) == "" {
		return errors.New("必须用 -name 指定规则名称")
	}
	if *listen < 10000 || *listen > 65535 {
		return errors.New("公网端口需在 10000-65535")
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("必须用 -target 指定内网目标，如 127.0.0.1:5000")
	}
	_, api, err := opts.resolve()
	if err != nil {
		return err
	}
	ctx, cancel := requestContext()
	defer cancel()

	rule := adminapi.Rule{Name: *name, Proto: *proto, Listen: *listen, Target: *target}
	if err := api.AddRule(ctx, rule); err != nil {
		return err
	}
	fmt.Printf("已添加规则 %s：公网 %d -> %s，可用 mtun rule list 确认状态\n", *name, *listen, *target)
	return nil
}

func runRuleList(args []string) error {
	fs := flag.NewFlagSet("rule list", flag.ExitOnError)
	opts := targetFlags(fs)
	_ = fs.Parse(args)

	_, api, err := opts.resolve()
	if err != nil {
		return err
	}
	ctx, cancel := requestContext()
	defer cancel()

	data, err := api.Get(ctx, "/api/status")
	if err != nil {
		return err
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if _, isClient := probe["connected"]; !isClient {
		return errors.New("该地址是服务端控制台，没有转发规则；请用 -url http://127.0.0.1:10005 指向客户端")
	}
	var st adminapi.ClientStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	printRules(st.Rules)
	return nil
}

func runRuleDelete(args []string) error {
	fs := flag.NewFlagSet("rule delete", flag.ExitOnError)
	opts := targetFlags(fs)
	name := fs.String("name", "", "规则名称")
	_ = fs.Parse(args)

	if strings.TrimSpace(*name) == "" {
		return errors.New("必须用 -name 指定要删除的规则名称")
	}
	_, api, err := opts.resolve()
	if err != nil {
		return err
	}
	ctx, cancel := requestContext()
	defer cancel()

	if err := api.DeleteRule(ctx, *name); err != nil {
		return err
	}
	fmt.Println("已删除规则", *name)
	return nil
}

func printRules(rules []adminapi.Rule) {
	if len(rules) == 0 {
		fmt.Println("（暂无转发规则）")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "名称\t协议\t公网端口\t内网目标\t状态")
	for _, r := range rules {
		state := ruleStateText(r.State)
		if r.Message != "" && r.State != "active" {
			state += "（" + r.Message + "）"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", r.Name, r.Proto, r.Listen, r.Target, state)
	}
	_ = w.Flush()
}

func ruleStateText(state string) string {
	switch state {
	case "active":
		return "运行中"
	case "error":
		return "错误"
	case "pending":
		return "待确认"
	default:
		return state
	}
}

func fmtDuration(seconds int64) string {
	s := seconds
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%d 秒", s)
	case s < 3600:
		return fmt.Sprintf("%d 分钟", s/60)
	case s < 86400:
		return fmt.Sprintf("%d 小时", s/3600)
	default:
		return fmt.Sprintf("%d 天", s/86400)
	}
}

func pick(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
