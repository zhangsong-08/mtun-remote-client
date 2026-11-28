# mtun 内网穿透

轻量级内网穿透工具：一个服务端 + 任意多个客户端节点，把内网设备的端口发布到公网，并提供 Web 控制台与命令行管理。

- 服务端：部署在公网服务器（Linux / Windows），负责节点注册、公网端口监听与转发
- 客户端：安装在内网设备（NAS、办公电脑、树莓派、工控机……），把本机服务暴露到公网
- 控制面走 TLS 1.3，所有密码只保存 PBKDF2-SHA256 哈希，明文不落盘

> **关于两个仓库**
> `mtun-remote-server` 与 `mtun-remote-client` 来自同一套代码，Release 中的二进制功能完全一致，只是内置的「在线更新仓库」不同：
> 部署服务端请用 server 仓库的资源，安装内网设备请用 client 仓库的资源。
> 两个仓库现已按角色拆分：各自只保留本端的 README、安装脚本与配置示例（`scripts/publish-split.ps1` 负责生成并推送），Go 源码因共用同一模块而完整保留。

---

## 目录

- [1. 组件与端口](#1-组件与端口)
- [2. 快速开始](#2-快速开始)
- [3. 密码体系](#3-密码体系)
- [4. 多端接入（核心）](#4-多端接入核心)
- [5. 发布内网服务](#5-发布内网服务)
- [6. Web 控制台](#6-web-控制台)
- [7. 命令行速查](#7-命令行速查)
- [8. 升级](#8-升级)
- [9. 常见问题](#9-常见问题)
- [10. 安全建议](#10-安全建议)
- [11. 校验下载文件](#11-校验下载文件)
- [12. 从源码构建](#12-从源码构建)

---

## 1. 组件与端口

| 组件 | 默认地址 | 说明 |
| --- | --- | --- |
| 服务端控制面 | `:10000` | 客户端连接入口，TLS 1.3，必须对公网开放 |
| 服务端控制台 | `127.0.0.1:10004` | 仅本机访问，管理员使用 |
| 客户端控制台 | `127.0.0.1:10005` | 仅本机访问，设备所有者使用 |
| 公网发布端口 | `10100-10999` | 客户端添加转发规则时使用，可在 `publish.port_range` 调整 |

`wg / relay / stun`（10001-10003）为预留端口，当前版本不是必需。

Release 资源（两个仓库一致）：

| 文件 | 适用平台 |
| --- | --- |
| `mtun-linux-amd64` | Linux x86_64 |
| `mtun-linux-arm64` | Linux ARM64（树莓派、ARM 云服务器） |
| `mtun-windows-amd64.exe` | Windows x64 |
| `mtun-windows-arm64.exe` | Windows ARM64 |
| `checksums.txt` | 以上文件的 SHA256 |

---

## 2. 快速开始

### 2.1 服务端（Linux，一键脚本）

```bash
curl -fsSL https://raw.githubusercontent.com/zhangsong-08/mtun-remote-server/main/scripts/install.sh -o install.sh
sudo bash install.sh
```

脚本会**逐项询问安装选项**（安装目录、面板地址、控制面端口、默认接入密码、管理密码、版本、下载源），每项后面方括号里就是默认值，**直接回车即使用默认**。之后：下载最新版本 → 安装到 `/opt/mtun` → 生成 `server.yaml` → 设置密码 → 注册并启动 systemd 服务 `mtun-server`。

不想交互时加 `-y`（或设 `MTUN_NONINTERACTIVE=1`），全部走参数与默认值；`curl | bash` 这类非终端环境会自动跳过询问。

常用参数：

```bash
sudo bash install.sh -y                              # 全默认、不询问
sudo bash install.sh -m https://ghfast.top/          # 国内服务器走加速镜像
sudo bash install.sh --file ./mtun-linux-amd64       # 离线安装（先 scp 上传）
sudo bash install.sh --panel 0.0.0.0:10004           # 允许远程访问面板（需自行做防火墙限制）
sudo bash install.sh --control :10000                # 控制面监听端口（默认 :10000）
sudo bash install.sh --join-password '接入密码'       # 直接指定默认接入密码（省略则自动生成）
sudo bash install.sh -u                              # 卸载
```

脚本头部会打印 MTUN 横幅、版权、最后更新时间与全部安装选项的生效值；安装按 `[步骤/总数]` 打印，下载显示百分比、已下载/总大小与实时速度。安装过程中 Ctrl+C 会被忽略，避免误触中断造成半安装状态（确需中止请关闭终端）。

### 2.2 服务端（Windows，一键脚本）

管理员 PowerShell：

```powershell
curl.exe -fsSL https://raw.githubusercontent.com/zhangsong-08/mtun-remote-server/main/scripts/install.ps1 -o install.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

同样会逐项询问选项，回车取默认值；加 `-Yes` 可跳过询问。默认安装到 `C:\Program Files\mtun`（非管理员为 `%USERPROFILE%\mtun`），并注册开机自启。

参数：`-AdminPassword`、`-JoinPassword`、`-Panel`、`-Control`、`-InstallDir`、`-Version`、`-Mirror`、`-LocalFile`、`-Yes`、`-Uninstall`、`-Help`。
运行 `-Help` 可查看全部选项与默认值。

### 2.3 客户端（Linux，一键脚本）

```bash
curl -fsSL https://raw.githubusercontent.com/zhangsong-08/mtun-remote-client/main/scripts/install-client.sh -o install-client.sh
sudo bash install-client.sh
```

不填参数时也可以直接运行：脚本会依次询问**服务端地址、接入密码、节点名称、安装目录、面板地址、面板密码、版本、下载源**，回车即用默认值。

- `-s`：服务端控制面地址（**必填**，也可交互输入）
- `-k`：接入密码（**必填**，由服务端管理员提供，见第 3 节，也可交互输入）
- `-n`：节点名称，默认取主机名，多台设备请保证名字好辨认
- 安装到 `/opt/mtun-client`，注册 systemd 服务 `mtun-client`
- `-u` 卸载；`--panel 0.0.0.0:10005` 允许远程访问客户端面板
- `-p` 直接指定面板登录密码；`-f` 离线安装；`-y` 跳过交互；`-h` 查看全部选项与默认值

安装过程同样会打印 MTUN 横幅、版权、最后更新时间、选项清单，以及分步进度与下载百分比；Ctrl+C 被忽略，不会中断安装。非终端环境（如 `curl | bash`）会自动跳过询问，此时必须用 `-s` 和 `-k` 提供地址与密码。

### 2.4 客户端（Windows，一键脚本）

管理员 PowerShell：

```powershell
curl.exe -fsSL https://raw.githubusercontent.com/zhangsong-08/mtun-remote-client/main/scripts/install-client.ps1 -o install-client.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install-client.ps1
```

运行时逐项询问，回车取默认；也可以一次性传参（`-Server`、`-Key`、`-Node` …）并加 `-Yes` 跳过询问。

默认安装到 `C:\Program Files\mtun-client`（非管理员为 `%USERPROFILE%\mtun-client`），并注册开机自启。

参数：`-Server`、`-Key`、`-Node`、`-Panel`、`-AdminPassword`、`-InstallDir`、`-Version`、`-Mirror`、`-LocalFile`、`-Yes`、`-Uninstall`、`-Help`。

### 2.5 手动运行（任意平台，不用脚本）

```bash
# 服务端
mtun default server > server.yaml
mtun passwd -c server.yaml            # 设置面板管理密码（至少 8 位）
mtun access -c server.yaml            # 设置默认接入密码（客户端连接时用）
mtun server -c server.yaml

# 客户端
mtun default client > client.yaml
mtun passwd -c client.yaml            # 设置客户端面板登录密码
mtun connect -c client.yaml -server 服务器IP:10000 -key '接入密码'
mtun client -c client.yaml
```

打开控制台：服务端 `http://127.0.0.1:10004`，客户端 `http://127.0.0.1:10005`（或执行 `mtun web` 自动打开）。

---

## 3. 密码体系

共 5 类凭据，职责互不重叠：

| 凭据 | 作用 | 设置方式 | 存储位置 |
| --- | --- | --- | --- |
| 服务端管理密码 | 登录服务端控制台 | `mtun passwd -c server.yaml` 或安装脚本 `-p` | `server.yaml.passwd`（哈希） |
| 客户端登录密码 | 登录客户端控制台 | `mtun passwd -c client.yaml`，或首次打开控制台时设置 | `client.yaml.passwd`（哈希） |
| 默认接入密码 | 未设置独立密码的节点用它接入 | `mtun access -c server.yaml`，或服务端控制台「面板管理」 | `server.yaml.join`（只存哈希） |
| 节点独立接入密码 | 指定节点专用的接入密码 | 服务端控制台「加密」页 | `server.yaml.nodepass.json`（只存哈希） |
| 敏感操作 PIN | 面板敏感操作二次验证（6 位数字） | `mtun pin -c server.yaml -new 123456` 或控制台 | `server.yaml.pin`（哈希） |

要点：

1. 密码明文只在**设置或随机生成的那一刻**出现，之后任何页面、接口、文件都无法再读回；忘记只能重置。
2. `.join`、`.nodepass.json`、`.passwd`、`.pin` 均为 `0600` 权限，只保存哈希。
3. 旧版本（v0.4.x）的明文 `.join` 会在首次以 v0.5.0+ 启动时自动迁移为哈希，**不可逆**。
4. 服务端与客户端必须使用同一版本线（v0.5.0 起密码派生方式变更，新旧版本无法混连）。

---

## 4. 多端接入（核心）

### 4.1 整体结构

```
                    公网
                     │  10000 (TLS)
        ┌────────────┴────────────┐
        │      服务端 1.2.3.4      │
        │  控制台 127.0.0.1:10004  │
        └────────────┬────────────┘
                     │ 加密通道
      ┌──────────────┼──────────────┐
      │              │              │
   家里 NAS       办公电脑        树莓派
   节点 nas      节点 office-pc   节点 pi
   面板 :10005   面板 :10005      面板 :10005
```

- 一个服务端可接入任意多台设备，每台设备是一个「节点」
- 每台设备只需配置：服务端地址 + 接入密码 + 节点名
- 节点名建议手动指定（`-n` / `-Node`），否则默认取主机名，重名时不易区分

一次装好两台设备的示例：

```bash
# 家里 NAS
sudo bash install-client.sh -s 1.2.3.4:10000 -k '接入密码' -n nas

# 树莓派
sudo bash install-client.sh -s 1.2.3.4:10000 -k '接入密码' -n pi
```

```powershell
# 办公电脑（Windows）
powershell -File .\install-client.ps1 -Server 1.2.3.4:10000 -Key '接入密码' -Node office-pc
```

### 4.2 给某个节点单独设置接入密码（推荐）

默认密码一泄露，所有节点都会受影响；给每台设备配独立密码可以单独吊销。

1. **先让设备接入一次**（用默认接入密码即可），否则服务端节点列表里还没有它
2. 在服务端执行 `ssh -L 10004:127.0.0.1:10004 root@1.2.3.4`，本机打开 `http://127.0.0.1:10004`
3. 进入左侧「加密」页，在「操作 PIN」里输入 6 位 PIN
4. 在「节点接入密码」表格中找到目标节点，点「选择」
5. 在「设置接入密码」中把新密码**留空**，点「保存密码」→ 系统会随机生成 32 位密码并只显示这一次，点「复制」
6. 到该设备上更新密码：

   ```bash
   # 客户端正在运行：热更新并自动重连
   mtun connect -c /opt/mtun-client/client.yaml -key '刚复制的密码'

   # 或修改客户端控制台「服务端」页里的接入密码
   ```

7. 该设备执行 `mtun status` 确认「已连接」

设置独立密码后，这个节点就**只认**自己的密码，默认接入密码对它无效。

### 4.3 重置已接入节点的密码

在「加密」页对同一节点再次「保存密码」（留空即重新随机），旧密码立即失效；设备下次重连前会断开，用新密码 `mtun connect` 后恢复。

### 4.4 关闭默认密码接入

「加密」页顶部「允许默认密码接入」开关：

- **开启**：未设置独立密码的节点可用默认接入密码进入
- **关闭**：未设置独立密码的节点在 TLS 握手阶段就会被拒绝；已设置独立密码的节点不受影响

建议所有节点都配好独立密码后再关闭。关闭前请确认自己还留有可用的节点密码，否则只能通过服务端本机重置。

### 4.5 无密码申请接入

客户端没有接入密码时，不再直接报错，而是自动向服务端提交申请：

1. 服务端「加密」页保持「允许无密码申请接入」开启（默认开启）
2. 客户端只填服务端地址、不填密码启动，控制台显示「等待批准」
3. 服务端「加密 → 接入申请」出现该节点（节点名、来源 IP、版本、申请时间）
4. 点「同意」：立即生成该节点的独立密码、保存为哈希并下发客户端，客户端自动接入；密码在面板显示一次供留档
5. 点「拒绝」：客户端显示原因，30 秒后自动重试

申请只在内存保留 10 分钟，同一 IP 最多 3 条待处理；「加密」页可整体关闭申请通道。

### 4.6 远程访问控制台

两个控制台默认只监听 `127.0.0.1`，远程请用 SSH 隧道（安全）：

```bash
ssh -L 10004:127.0.0.1:10004 root@1.2.3.4   # 服务端面板 → 本机 http://127.0.0.1:10004
ssh -L 10005:127.0.0.1:10005 user@内网设备   # 客户端面板 → 本机 http://127.0.0.1:10005
```

确实需要直接暴露时，安装脚本加 `--panel 0.0.0.0:10004`，并务必配合防火墙与强密码。

### 4.7 多端命令行管理（不需要打开网页）

```bash
mtun status -c server.yaml          # 服务端概况：在线/离线节点数、直连/中继
mtun nodes  -c server.yaml          # 节点列表：ID、覆盖 IP、在线状态、直连/中继、发布规则、最后在线
mtun watch  -c server.yaml          # 每 2 秒刷新，实时盯节点
mtun kick   -c server.yaml -id 节点ID   # 踢下线并临时封禁
```

这些命令通过本机控制台的 `.apitoken` 鉴权，所以要在服务端本机执行。

---

## 5. 发布内网服务

在**客户端**配置「公网端口 → 本机地址」的映射，服务端会自动监听公网端口并转发：

- 客户端控制台「端口转发」页 → 新增：公网端口 `10180`、目标 `127.0.0.1:80`、协议 `tcp`（或 `http`）
- 或命令行：

  ```bash
  mtun rule add -c client.yaml -name nas-web -listen 10180 -target 127.0.0.1:80 [-proto tcp]
  mtun rule list -c client.yaml
  mtun rule delete -c client.yaml -name nas-web
  ```

之后访问 `http://服务器IP:10180` 即可到达该设备的 `127.0.0.1:80`。

- 公网端口需 ≥ 10000，且在服务端 `publish.port_range`（默认 `10100-10999`）范围内
- 云服务器安全组/防火墙要放行对应公网端口
- 服务端自身也可以配置本地规则（`server.yaml` 的 `publish.rules`），用于把服务端本机服务发布出去

---

## 6. Web 控制台

服务端（`127.0.0.1:10004`）：

| 页面 | 作用 |
| --- | --- |
| 系统状态 | 服务状态、版本、运行时长、连接数、网络速度、累计流量 |
| 节点 | 在线节点详情、封禁与访问限制、待下发规则 |
| 转发规则 | 查看/下发各节点的发布规则 |
| 加密 | 操作 PIN、默认接入密码开关、节点独立接入密码 |
| 面板管理 | 常规设置、安全设置（需 PIN）、端口与密码、开机自启、安全信息 |
| 版本更新 | 检查并安装新版本，自动重启 |

客户端（`127.0.0.1:10005`）：

| 页面 | 作用 |
| --- | --- |
| 概览 | 连接状态、节点 ID、覆盖 IP、连接时长 |
| 服务端 | 服务端地址与接入密码（修改后自动重连） |
| 端口转发 | 增删改本机发布规则 |
| 网络 | 可见的其他节点与共享规则 |
| 版本更新 | 检查并安装新版本 |

---

## 7. 命令行速查

```text
mtun server   -c server.yaml          启动服务端
mtun client   -c client.yaml          启动客户端
mtun web                              打开本机控制台
mtun status   -c <配置>               查看状态
mtun watch    -c <配置> [-interval 2s] 实时刷新
mtun nodes    -c <服务端配置>         节点列表
mtun kick     -c <服务端配置> -id 节点ID [-ban 60s]
mtun connect  -c <客户端配置> -server host:port [-key 接入密码]
mtun rule add|list|delete -c <客户端配置> ...   # add 支持 -proto tcp|http
mtun passwd   -c <配置>               设置面板登录密码
mtun access   -c <服务端配置>         设置默认接入密码
mtun pin      -c <服务端配置> -new 123456
mtun update   [-check] [-c <配置>]    在线更新
mtun check    -c <配置> [-role server|client]   校验配置
mtun service                          输出当前平台服务安装提示
mtun default  server|client           输出默认配置
mtun version
```

`-c` 不写时默认 `configs/server.example.yaml` / `configs/client.example.yaml`，生产环境请显式指定路径。

---

## 8. 升级

```bash
mtun update -c /opt/mtun/server.yaml --check   # 只看有没有新版本
sudo mtun update -c /opt/mtun/server.yaml      # 下载替换并自动重启
```

- 升级只替换程序，不修改配置文件
- 控制台「版本更新」页可以一键升级并自动重启
- 更新来源优先级：`MTUN_UPDATE_REPO` 环境变量 > 配置 `update_repo` > 构建注入的仓库
- **v0.5.0 起**：首次启动会把旧的明文 `.join` 迁移成哈希，迁移后无法还原明文，请提前记录

---

## 9. 常见问题

**客户端一直提示「接入密码不匹配」**

1. 版本不一致：服务端与客户端必须同为 v0.5.0 或更高，旧客户端连不上新服务端
2. 密码不对：确认设备使用的是服务端当前默认密码，或该节点的独立密码
3. 该节点被设置了独立密码：默认密码对它无效，需要用 `mtun connect -key` 换成独立密码
4. 服务端关闭了「允许默认密码接入」：该节点必须先设置独立密码

**控制台打不开**

控制台只监听 `127.0.0.1`。远程请用第 4.5 节的 SSH 隧道；在服务器本机可直接 `mtun web`。

**公网端口访问不了**

- 规则是否在客户端「端口转发」里且状态为运行中
- 端口是否在 `publish.port_range` 内
- 云服务器安全组、系统防火墙是否放行
- 目标服务是否真的监听在 `127.0.0.1:端口`

**Linux 自启没生效**

```bash
systemctl status mtun-server      # 或 mtun-client
systemctl restart mtun-server
journalctl -u mtun-server -n 100 --no-pager
```

**忘记密码**

- 面板登录密码：`mtun passwd -c <配置>` 重设
- 默认接入密码：`mtun access -c server.yaml` 重设（需重启服务端，所有用默认密码的节点要同步更新）
- 节点独立密码：控制台「加密」页重新生成
- PIN：`mtun pin -c server.yaml -new 123456`（终端本地执行）

---

## 10. 安全建议

- 只对公网开放 `10000` 和确实要用的发布端口，控制台不要暴露公网
- 管理密码、接入密码至少 8 位，不要复用其他系统密码
- 多设备场景优先使用「节点独立接入密码」，一台设备泄露不影响其他设备
- 定期在「加密」页轮换密码，离职设备用 `mtun kick` + 重置密码处理
- 下载后核对 `checksums.txt`，确认二进制未被篡改

---

## 11. 校验下载文件

```bash
# Linux
sha256sum -c checksums.txt

# Windows PowerShell
Get-FileHash .\mtun-windows-amd64.exe -Algorithm SHA256
```

与 `checksums.txt` 中对应行比对即可。

---

## 12. 从源码构建

需要 Go 1.26+：

```bash
git clone https://github.com/zhangsong-08/mtun-remote-server.git
cd mtun-remote-server
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w \
  -X mtun/internal/version.Version=v0.5.1 \
  -X mtun/internal/version.Repo=zhangsong-08/mtun-remote-server" \
  -o mtun ./cmd/mtun
```

交叉编译：

```bash
GOOS=linux   GOARCH=arm64 go build -o mtun-linux-arm64 ./cmd/mtun
GOOS=windows GOARCH=amd64 go build -o mtun-windows-amd64.exe ./cmd/mtun
```

接口文档见 [docs/api.md](docs/api.md)。

---

## License

MIT © 2026 song08
