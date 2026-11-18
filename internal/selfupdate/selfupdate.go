// Package selfupdate 实现在线更新：从 GitHub Release 下载对应平台的二进制，
// 校验 checksums.txt，替换当前可执行文件并拉起新进程。
// 只替换程序本身，不触碰任何配置文件。
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"mtun/internal/platform"
	"mtun/internal/version"
)

const maxDownload = 256 << 20 // 256MB

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Release struct {
	Tag     string
	Version string
	Notes   string
	URL     string
	Assets  []Asset
}

type Updater struct {
	Repo    string
	Current string
	Token   string
	Log     *slog.Logger
	client  *http.Client
}

func New(log *slog.Logger) *Updater {
	repo := strings.TrimSpace(os.Getenv("MTUN_UPDATE_REPO"))
	if repo == "" {
		repo = version.Repo
	}
	return &Updater{
		Repo:    repo,
		Current: version.Version,
		Token:   strings.TrimSpace(os.Getenv("MTUN_GITHUB_TOKEN")),
		Log:     log,
		client: &http.Client{
			// 更新包可能有十几 MB，弱网下 2 分钟不够；整体仍由调用方 context 兜底。
			Timeout: 15 * time.Minute,
		},
	}
}

// Check 查询最新 release，并判断是否比当前版本新。
func (u *Updater) Check(ctx context.Context) (*Release, bool, error) {
	if u.Repo == "" {
		return nil, false, errors.New("未配置更新仓库：请设置 MTUN_UPDATE_REPO=owner/repo，或构建时注入 version.Repo")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+u.Repo+"/releases/latest", nil)
	if err != nil {
		return nil, false, err
	}
	u.setHeaders(req)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, false, fmt.Errorf("仓库 %s 暂无 release", u.Repo)
	case http.StatusForbidden:
		return nil, false, errors.New("GitHub API 访问受限（可能是匿名请求频率超限），可设置 MTUN_GITHUB_TOKEN")
	default:
		return nil, false, fmt.Errorf("GitHub API 返回 %s", resp.Status)
	}

	var raw struct {
		TagName string  `json:"tag_name"`
		Body    string  `json:"body"`
		HTMLURL string  `json:"html_url"`
		Assets  []Asset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, false, fmt.Errorf("解析 release 失败: %w", err)
	}
	rel := &Release{
		Tag:     raw.TagName,
		Version: normalize(raw.TagName),
		Notes:   raw.Body,
		URL:     raw.HTMLURL,
		Assets:  raw.Assets,
	}
	return rel, compareVersions(u.Current, rel.Version) < 0, nil
}

// Apply 下载并替换当前可执行文件，返回可执行文件路径。
func (u *Updater) Apply(ctx context.Context, rel *Release) (string, error) {
	asset, ok := pickAsset(rel)
	if !ok {
		return "", fmt.Errorf("release %s 未提供 %s/%s 的构建产物", rel.Tag, runtime.GOOS, runtime.GOARCH)
	}
	data, err := u.download(ctx, asset.URL)
	if err != nil {
		return "", err
	}
	if sums, ok := findAsset(rel, "checksums.txt"); ok {
		want, err := u.checksum(ctx, sums.URL, asset.Name)
		if err != nil {
			return "", err
		}
		got := sha256.Sum256(data)
		if !strings.EqualFold(want, hex.EncodeToString(got[:])) {
			return "", errors.New("下载文件校验和不匹配，已取消更新")
		}
	} else {
		u.Log.Warn("release 未提供 checksums.txt，跳过完整性校验")
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前程序失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := replaceExecutable(exe, data); err != nil {
		return "", err
	}
	return exe, nil
}

func (u *Updater) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "mtun-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	if u.Token != "" {
		req.Header.Set("Authorization", "Bearer "+u.Token)
	}
}

func (u *Updater) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	u.setHeaders(req)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载更新包失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载更新包返回 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownload {
		return nil, errors.New("更新包超过大小限制")
	}
	return data, nil
}

func (u *Updater) checksum(ctx context.Context, url, name string) (string, error) {
	data, err := u.download(ctx, url)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt 中找不到 %s", name)
}

func pickAsset(rel *Release) (Asset, bool) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	names := []string{
		fmt.Sprintf("mtun-%s-%s%s", runtime.GOOS, runtime.GOARCH, suffix),
		fmt.Sprintf("mtun_%s_%s%s", runtime.GOOS, runtime.GOARCH, suffix),
	}
	for _, name := range names {
		if a, ok := findAsset(rel, name); ok {
			return a, true
		}
	}
	return Asset{}, false
}

func findAsset(rel *Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Asset{}, false
}

// replaceExecutable 写入临时文件后替换目标。
// Windows 上运行中的 exe 无法删除，但可以改名，因此先改名为 .old。
func replaceExecutable(target string, data []byte) error {
	dir := filepath.Dir(target)
	tmp := filepath.Join(dir, "."+filepath.Base(target)+".new")
	stale := target + ".old"

	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return fmt.Errorf("写入更新文件失败: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil && runtime.GOOS != "windows" {
		_ = os.Remove(tmp)
		return err
	}

	if runtime.GOOS == "windows" {
		_ = os.Remove(stale)
		if err := os.Rename(target, stale); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("替换程序失败（请确认有写入权限）: %w", err)
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Rename(stale, target)
			_ = os.Remove(tmp)
			return fmt.Errorf("写入新版本失败: %w", err)
		}
		return nil
	}

	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换程序失败: %w", err)
	}
	return nil
}

// CleanupOld 清理上次更新遗留的旧文件（Windows）。
func CleanupOld() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	_ = os.Remove(exe + ".old")
}

// ScheduleRestart 启动一个等待旧进程退出的助手进程，由它拉起新版本。
// 助手必须脱离当前进程的控制台与作业对象（Job），否则主进程退出时
// 会被系统连带清理，导致新版本永远不会被拉起。
func ScheduleRestart(exe string, log *slog.Logger) error {
	raw, err := json.Marshal(os.Args[1:])
	if err != nil {
		return fmt.Errorf("序列化启动参数失败: %w", err)
	}
	env := append(os.Environ(),
		"MTUN_RESTART_ARGS_JSON="+string(raw),
		"MTUN_RESTART_PARENT_PID="+strconv.Itoa(os.Getpid()),
	)

	start := func(detach bool) error {
		cmd := exec.Command(exe, "__restart")
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.SysProcAttr = platform.DetachProcAttr(detach)
		return cmd.Start()
	}

	if err := start(true); err != nil {
		// 作业对象未授予 BREAKAWAY 权限时退回仅脱离控制台，避免完全不重启。
		log.Warn("重启助手脱离作业对象失败，改用降级方式", "err", err)
		if err2 := start(false); err2 != nil {
			return fmt.Errorf("启动重启助手失败: %w", err2)
		}
	}
	log.Info("已安排自动重启", "exe", exe)
	return nil
}

// RunRestartHelper 是 __restart 的实现：等待旧进程退出后拉起新进程，
// 若新进程启动后立即退出（端口仍被占用）会自动重试。
func RunRestartHelper(log *slog.Logger) error {
	args := []string{}
	if raw := os.Getenv("MTUN_RESTART_ARGS_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return fmt.Errorf("解析重启参数失败: %w", err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// 必须等旧进程真正退出再启动，否则新进程会因端口仍被占用而立即退出。
	if raw := os.Getenv("MTUN_RESTART_PARENT_PID"); raw != "" {
		if pid, err := strconv.Atoi(raw); err == nil && pid > 0 {
			deadline := time.Now().Add(30 * time.Second)
			for platform.ProcessAlive(pid) && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
			}
		}
	}
	// 再留一点时间给监听端口释放。
	time.Sleep(400 * time.Millisecond)

	wd, _ := os.Getwd()
	for attempt := 1; attempt <= 15; attempt++ {
		cmd := exec.Command(exe, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Dir = wd
		cmd.SysProcAttr = platform.DetachProcAttr(false)
		if err := cmd.Start(); err != nil {
			log.Warn("重启新版本失败，准备重试", "err", err, "attempt", attempt)
			time.Sleep(time.Second)
			continue
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			log.Warn("新版本启动后退出，准备重试", "err", err, "attempt", attempt)
			time.Sleep(2 * time.Second)
		case <-time.After(4 * time.Second):
			return nil
		}
	}
	return errors.New("多次重试后仍无法启动新版本")
}

func normalize(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

// compareVersions 按 x.y.z 比较；无法解析时按“有更新”处理（返回 -1）。
func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		if strings.EqualFold(strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")) {
			return 0
		}
		return -1
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = normalize(v)
	if v == "" || strings.Contains(v, "dev") {
		return out, false
	}
	core := strings.SplitN(v, "-", 2)[0]
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
