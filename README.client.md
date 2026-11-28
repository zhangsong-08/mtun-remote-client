# MTUN 内网穿透 · 客户端

MTUN 的客户端程序：安装在内网设备上（Windows / Linux），把本机端口发布到公网服务端，并提供本机 Web 控制台。

- 无需公网 IP、无需路由器端口映射
- 接入服务端时，**有密码直接用；没密码可一键申请，管理员批准后自动接入**
- 本机控制台（默认 `127.0.0.1:10005`）：状态、服务端、端口转发、网络、版本更新
- 密码只保存 PBKDF2-SHA256 哈希，明文不落盘

> 服务端请到 [mtun-remote-server](https://github.com/zhangsong-08/mtun-remote-server) 下载部署。

---

## 一、安装

### Windows（推荐）

管理员 PowerShell：

```powershell
curl.exe -fsSL https://raw.githubusercontent.com/zhangsong-08/mtun-remote-client/main/scripts/install-client.ps1 -o install-client.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install-client.ps1
```

逐项询问：服务端地址、接入密码、节点名称、安装目录、面板地址、面板密码、版本、下载源，**回车即用默认值**。

```powershell
# 不填密码：安装后自动向服务端申请接入
powershell -File .\install-client.ps1 -Server 1.2.3.4:10000

# 一次性给全参数，不询问
powershell -File .\install-client.ps1 -Server 1.2.3.4:10000 -Key '接入密码' -Node office-pc -Yes

# 离线安装
powershell -File .\install-client.ps1 -LocalFile .\mtun-windows-amd64.exe -Server 1.2.3.4:10000

# 卸载
powershell -File .\install-client.ps1 -Uninstall
```

默认安装到 `C:\Program Files\mtun-client`（非管理员为 `%USERPROFILE%\mtun-client`），并注册开机自启。

### Linux

```bash
curl -fsSL https://raw.githubusercontent.com/zhangsong-08/mtun-remote-client/main/scripts/install-client.sh -o install-client.sh
sudo bash install-client.sh
```

```bash
sudo bash install-client.sh -s 1.2.3.4:10000            # 不填密码，启动后申请接入
sudo bash install-client.sh -s 1.2.3.4:10000 -k '密码' -n nas -y
sudo bash install-client.sh -s 1.2.3.4:10000 -k '密码' --panel 0.0.0.0:10005 -y
sudo bash install-client.sh -u
```

安装到 `/opt/mtun-client`，注册 systemd 服务 `mtun-client`。

### 手动运行

```bash
mtun default client > client.yaml
mtun connect -c client.yaml -server 1.2.3.4:10000        # 不带 -key 则自动申请
mtun client -c client.yaml
```

打开 `http://127.0.0.1:10005`。

---

## 二、两种接入方式

### 1. 有接入密码（直接接入）

密码由服务端管理员提供。填写后立即建立 TLS 连接并上线。

```bash
mtun connect -c client.yaml -server 1.2.3.4:10000 -key '接入密码'
```

也可以只在本机控制台「服务端」页填写，保存后自动重连。

### 2. 没有密码：申请接入

不填密码启动或保存后，客户端会自动向服务端提交申请：

1. 本机控制台「概览」显示 **等待批准**，并展示服务端返回的提示
2. 服务端管理员在「加密 → 接入申请」看到你，点「同意」
3. 服务端生成该节点独立密码并直接下发，客户端自动保存哈希并接入，**无需再输入任何密码**
4. 若被拒绝，会显示原因，30 秒后自动重试

前提：服务端开启了「允许无密码申请接入」。若对方已关闭，会提示联系管理员获取密码。

---

## 三、发布本机服务

客户端控制台「端口转发」新增规则：公网端口 `10180` → `127.0.0.1:80`，协议 `tcp`/`http`。

```bash
mtun rule add -c client.yaml -name web -listen 10180 -target 127.0.0.1:80
mtun rule list -c client.yaml
mtun rule delete -c client.yaml -name web
```

之后访问 `http://服务端IP:10180` 即到达本机 80 端口。端口需在服务端 `publish.port_range`（默认 `10100-10999`）内，并由服务端防火墙放行。

常见用法：

| 目标 | listen | target |
| --- | --- | --- |
| 远程桌面 | 10190 | `127.0.0.1:3389` |
| NAS 管理页 | 10181 | `127.0.0.1:5000` |
| SSH | 10122 | `127.0.0.1:22` |
| 摄像头 / NVR | 10182 | `127.0.0.1:8000` |

---

## 四、控制台

| 页面 | 作用 |
| --- | --- |
| 概览 | 连接状态、节点 ID、覆盖 IP、连接时长、规则状态 |
| 服务端 | 服务端地址与接入密码（留空密码 = 申请接入） |
| 端口转发 | 增删改本机发布规则 |
| 网络 | 可见的其他节点与共享规则 |
| 版本更新 | 一键升级并自动重启 |

---

## 五、命令行

```bash
mtun status  -c client.yaml        # 连接状态与规则
mtun watch   -c client.yaml        # 实时刷新
mtun connect -c client.yaml -server 1.2.3.4:10000 [-key 密码]
mtun rule add|list|delete -c client.yaml ...
mtun passwd  -c client.yaml        # 本机面板登录密码
mtun update  [-check] -c client.yaml
mtun version
```

---

## 六、常见问题

**一直显示「等待批准」**
服务端管理员还没处理申请，或「允许无密码申请接入」被关闭。申请 10 分钟内有效，超时会自动重新提交。

**提示「接入密码不匹配」**
密码不对，或服务端已为你的节点重置了独立密码。可在服务端「加密」页查看状态并重新生成，或清空密码改走申请流程。

**连接失败**
确认服务端地址是 `IP:10000`、本机可访问；确认服务端版本 ≥ v0.5.0（新旧版本不能混连）。

**面板打不开**
控制台只监听 `127.0.0.1`；本机可用 `mtun web` 打开，远程请用 SSH 隧道或安装时加 `--panel 0.0.0.0:10005`。

**Linux 自启**
`systemctl status mtun-client`、`journalctl -u mtun-client -n 100 --no-pager`。

---

## 七、从源码构建

需要 Go 1.26+：

```bash
git clone https://github.com/zhangsong-08/mtun-remote-client.git
cd mtun-remote-client
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w \
  -X mtun/internal/version.Version=v0.6.0 \
  -X mtun/internal/version.Repo=zhangsong-08/mtun-remote-client" \
  -o mtun ./cmd/mtun
```

---

## License

MIT © 2026 song08
