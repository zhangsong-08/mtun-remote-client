# mtun API 与使用文档

适用于 mtun v0.4.0。本文面向使用和对接 mtun 的用户，包含安装接入、控制台 API 与常见问题。

> 版权：© 2026 song08 · MIT License

---

## 1. 概览

mtun 是中继式异地组网工具，不安装 TUN 驱动，由服务端和客户端两个角色组成：

| 角色 | 作用 | 仓库 |
| --- | --- | --- |
| 服务端 | 控制面、节点注册、端口转发监听、Web 管理面板 | `zhangsong-08/mtun-remote-server` |
| 客户端 | 连接服务端、上报转发规则、Web 管理面板 | `zhangsong-08/mtun-remote-client` |

### 1.1 默认端口

| 端口 | 用途 | 说明 |
| --- | --- | --- |
| 10000/TCP | 控制面 | 客户端注册、心跳、状态下发，TLS 1.3 |
| 10001 | WG 预留 | 当前版本未启用 TUN |
| 10002/TCP | 中继 | 直连失败时走中继 |
| 10003 | STUN 预留 | 当前版本未启用 |
| 10004/TCP | 服务端管理面板 | 默认仅 `127.0.0.1` |
| 10005/TCP | 客户端管理面板 | 默认仅 `127.0.0.1` |
| 10100-10999/TCP | 转发端口 | 由服务端 `publish.port_range` 控制，可修改 |

公网部署服务端时，至少需要放行 `10000/TCP` 和转发端口范围。

### 1.2 三组密码

| 密码 | 用途 | 存储位置 |
| --- | --- | --- |
| 管理密码 | 登录 Web 控制台，至少 8 位 | `<配置文件>.passwd`（仅保存哈希） |
| 接入密码 | 客户端连接服务端时校验 | 服务端 `<server.yaml>.join` |
| API Token | 终端命令和自动化调用 API | `<配置文件>.apitoken`（0600） |

此外，服务端提供 6 位数字 PIN（`<server.yaml>.pin`，只保存哈希），用于面板中修改面板端口、接入端口、Web 密码和接入密码时的二次验证。PIN 在面板“安全设置”中首次设置，也可在终端重置：

```bash
mtun pin -c configs/server.yaml -new 123456
```

连续 5 次输入错误会锁定 60 秒。

> 当前的接入密码是全服务端共享的一个值。按用户单独设置接入密码的功能尚未实现，规划中。

---

## 2. 快速开始

### 2.1 Windows

服务端（管理员 PowerShell，会自动配置开机启动）：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

客户端：

```powershell
powershell -ExecutionPolicy Bypass -File .\install-client.ps1 `
  -Server 203.0.113.10:10000 -Key <接入密码>
```

常用参数：

| 参数 | 说明 |
| --- | --- |
| `-Version v0.3.3` | 指定版本，默认最新 release |
| `-InstallDir D:\mtun` | 指定安装目录 |
| `-Mirror https://ghfast.top/` | 使用 GitHub 加速镜像 |
| `-LocalFile .\mtun-windows-amd64.exe` | 离线安装 |
| `-AdminPassword 密码` | 非交互设置管理密码 |
| `-Uninstall` | 卸载并清理自启动项 |

重装会保留已有配置、接入密码和管理密码；安装结束默认启用开机自启动并启动进程。

### 2.2 Linux

服务端（systemd）：

```bash
sudo bash install.sh
```

客户端：

```bash
sudo bash install-client.sh -s 203.0.113.10:10000 -k <接入密码>
```

常用参数：`-v` 版本、`-d` 目录、`-m` 镜像、`-f` 离线文件、`-p` 管理密码、`--panel` 面板地址、`-u` 卸载。

### 2.3 手动运行

```bash
mtun server -c configs/server.yaml
mtun client -c configs/client.yaml
```

首次启动客户端前需要设置管理密码：

```bash
mtun passwd -c configs/client.yaml
```

服务端接入密码：

```bash
mtun access -c configs/server.yaml -password <新接入密码>
```

### 2.4 客户端首次连接

```bash
mtun connect -c configs/client.yaml -server 203.0.113.10:10000 -key <接入密码>
```

客户端已运行时，该命令通过本地面板热更新并立即重连；未运行时直接写入配置文件。

### 2.5 添加转发规则

```bash
mtun rule add -c configs/client.yaml -name web -listen 10180 -target 127.0.0.1:80 -proto http
mtun rule list -c configs/client.yaml
mtun rule delete -c configs/client.yaml -name web
```

规则约束：

- 名称不能为空，最多 64 字符，同一客户端内唯一。
- `proto` 仅支持 `tcp`（默认）和 `http`。
- `listen` 需在 10000-65535；服务端还会校验是否落在 `publish.port_range` 内（默认 10100-10999）。
- `target` 必须是 `host:port` 形式的内网地址。

### 2.6 在线更新与自启动

```bash
mtun update -c configs/client.yaml          # 下载并替换程序，保留配置
mtun update -check -c configs/client.yaml   # 仅检查是否有新版本
```

Windows 自启动也可在服务端/客户端控制台中开关，只影响下次启动，不会停止当前进程。

---

## 3. API 通用约定

### 3.1 基地址

- 服务端：`http://127.0.0.1:10004`
- 客户端：`http://127.0.0.1:10005`

面板绑定其他地址时替换主机和端口。

### 3.2 认证

两种方式任选其一：

1. **API Token**（推荐用于脚本）：请求头 `Authorization: Bearer <token>`，
   token 内容即 `<配置文件>.apitoken`。
2. **登录会话**：浏览器访问 `/login` 输入用户名（默认 `admin`）和管理密码，
   服务端返回 `mtun_session` Cookie（HttpOnly、SameSite=Strict）。会话绝对有效期由
   `panel.session_minutes` 控制，空闲 30 分钟失效。连续 5 次失败锁定 30 秒。

未认证访问 `/api/*` 返回 `401 unauthorized`。

### 3.3 写请求

- 建议统一带 `Content-Type: application/json`。
- 客户端接口和服务端自启动接口会强制校验该 Header，否则返回 `415`。
- 浏览器跨站写请求（Origin 不匹配且 `Sec-Fetch-Site` 非 same-origin）返回 `403`；
  命令行无 Origin 的请求正常放行。
- 请求体上限 4KB-8KB，接口不接受未知字段（部分接口）。

### 3.4 响应格式

成功返回 JSON；失败返回：

```json
{"error":"具体错误信息"}
```

### 3.5 状态码

| 状态码 | 含义 |
| --- | --- |
| 200 | 成功 |
| 400 | 参数错误、平台不支持自启动 |
| 401 | 未认证 |
| 403 | 权限不足、跨站请求、Host 非法 |
| 405 | 方法不允许 |
| 409 | 资源冲突（节点离线、自启动入口冲突） |
| 415 | Content-Type 非 application/json |
| 423 | PIN 连续错误被临时锁定 |
| 500 | 服务端内部错误 |
| 502 | 在线更新检查失败（如网络或仓库不可达） |

---

## 4. 客户端 API

基地址示例：`http://127.0.0.1:10005`，Token 取自 `client.yaml.apitoken`。

PowerShell 示例：

```powershell
$base = 'http://127.0.0.1:10005'
$token = (Get-Content 'E:\mtun\client.yaml.apitoken' -Raw).Trim()
$h = @{ Authorization = "Bearer $token" }
Invoke-RestMethod -Uri "$base/api/status" -Headers $h
```

### 4.1 GET /api/status

返回客户端连接状态、本机规则与可见节点。

```json
{
  "version": "v0.4.0",
  "node": "office-pc",
  "server": "203.0.113.10:10000",
  "dashboard": "127.0.0.1:10005",
  "has_access": true,
  "connected": true,
  "node_id": "office-pc",
  "overlay_ip": "100.64.0.x",
  "path": "relay",
  "connected_seconds": 120,
  "rules": [
    {
      "name": "welcome",
      "proto": "http",
      "listen": 10180,
      "target": "127.0.0.1:80",
      "state": "active",
      "visibility": "all",
      "visible_to": []
    }
  ],
  "peers": [
    {
      "node_id": "PC-other",
      "version": "v0.4.0",
      "overlay_ip": "100.64.0.y",
      "path": "relay",
      "online": true,
      "rules": [{"name": "api", "proto": "tcp", "listen": 10181, "state": "active"}]
    }
  ]
}
```

字段说明：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | string | 客户端版本 |
| `node` | string | 节点名称 |
| `server` | string | 服务端控制面地址 |
| `dashboard` | string | 面板监听地址 |
| `has_access` | bool | 是否已保存接入密码 |
| `connected` | bool | 是否已连接服务端 |
| `error` | string | 最近一次连接错误，正常时省略 |
| `node_id` | string | 服务端分配的节点 ID |
| `overlay_ip` | string | 组网虚拟 IP |
| `path` | string | 当前链路，`direct` 或 `relay` |
| `connected_seconds` | int | 本次连接持续秒数 |
| `rules` | array | 本机转发规则及状态 |
| `peers` | array | 其他节点及对本节点可见的规则 |

`state` 取值：`active`（已生效）、`pending`（等待确认）、`error`（监听失败）、`offline`（节点离线）。

### 4.2 POST /api/server

更新服务端地址和接入密码，保存后自动重连。

```json
{"server":"203.0.113.10:10000","key":"接入密码"}
```

`key` 为空表示不修改接入密码。成功返回 `{"ok":true}`。

### 4.3 POST /api/rules

新增转发规则。

```json
{
  "name": "web",
  "proto": "http",
  "listen": 10180,
  "target": "127.0.0.1:80",
  "visibility": "nodes",
  "visible_to": ["PC-other"]
}
```

- `visibility`：`all`（所有人可见，默认）、`self`（仅自己可见）、`nodes`（仅 `visible_to` 中的节点可见）。
- 名称重复或端口被占用返回 `400`。
- 成功返回 `{"ok":true}`。

> 服务端管理员也可以通过 `POST /api/nodes/rules` 增删改本节点的规则；在线时客户端会收到
> `rules_sync` 并自动写入配置文件，无需重连。

### 4.4 POST /api/rules/delete

```json
{"name":"web"}
```

成功返回 `{"ok":true}`。

### 4.5 POST /api/rules/visibility

修改某条规则的可见范围，客户端会落盘保存。

```json
{"name":"web","visibility":"self","visible_to":[]}
```

成功返回 `{"ok":true}`。

### 4.6 GET /api/autostart

查询 Windows 开机自启动状态。

```json
{
  "supported": true,
  "enabled": true,
  "mode": "scheduled_task",
  "trigger": "AtLogOn",
  "conflict": false,
  "message": "计划任务已启用"
}
```

| 字段 | 说明 |
| --- | --- |
| `supported` | 当前平台是否支持（Linux 为 `false`） |
| `enabled` | 是否已启用 |
| `mode` | `scheduled_task`、`nssm`、`startup_link`、`none` |
| `trigger` | `AtStartup`（开机免登录）、`AtLogOn`（用户登录后） |
| `conflict` | 是否同时存在多个自启动入口 |
| `message` | 可直接展示的中文提示 |

### 4.7 POST /api/autostart

```json
{"enabled": true}
```

必须为 JSON 且包含布尔字段 `enabled`。操作只影响下次启动，不会停止当前客户端。

错误映射：权限不足 `403`、入口冲突 `409`、平台不支持 `400`。

### 4.8 GET /api/update

```json
{
  "current": "v0.3.3",
  "latest": "0.3.3",
  "available": false,
  "notes": "release notes",
  "repo": "zhangsong-08/mtun-remote-client"
}
```

### 4.9 POST /api/update

无可用更新时：

```json
{"updated": false, "current": "v0.3.3", "latest": "0.3.3"}
```

有新版本时下载替换程序，配置与规则不变，然后自动重启：

```json
{"updated": true, "from": "v0.3.2", "to": "0.3.3", "restarting": true}
```

---

## 5. 服务端 API

基地址示例：`http://127.0.0.1:10004`，Token 取自 `server.yaml.apitoken`。

### 5.1 GET /api/status

```json
{
  "version": "v0.4.0",
  "uptime_seconds": 3600,
  "nodes": 3,
  "offline": 1,
  "direct": 1,
  "relay": 1,
  "publish_rules": 4
}
```

### 5.2 GET /api/nodes

返回全部节点及其规则（服务端可见所有规则）。

```json
[
  {
    "id": "office-pc",
    "overlay_ip": "100.64.0.x",
    "os": "windows",
    "ip": "1.2.3.4",
    "path": "relay",
    "online": true,
    "endpoints": ["1.2.3.4:41234"],
    "publish": 1,
    "last_seen_seconds": 2,
    "banned": false,
    "queued_rules": 0,
    "access": {"permanent": false, "until": "0001-01-01T00:00:00Z"},
    "rules": [
      {
        "name": "welcome",
        "proto": "http",
        "listen": 10180,
        "target": "127.0.0.1:80",
        "state": "active",
        "visibility": "all",
        "visible_to": []
      }
    ]
  }
]
```

| 字段 | 说明 |
| --- | --- |
| `os` | 客户端上报的系统（旧客户端为空） |
| `ip` | 客户端来源 IP |
| `banned` / `ban_until` | 是否封禁；`ban_until` 为 `permanent` 或 RFC3339 时间 |
| `queued_rules` | 离线节点待同步的规则操作数量 |
| `access` | 当前访问策略（`permanent`、`until`、`daily`、`allow_cidrs`、`deny_cidrs`、`allow_os`） |

> 注意：服务端返回的 `target` 是规则归属客户端的内网地址，只应展示给管理员。

### 5.3 POST /api/nodes/kick

强制节点下线并临时封禁。

```json
{"id":"office-pc","ban_seconds":60}
```

`ban_seconds` 缺省 60，最大 3600。节点不存在返回 `404`。

### 5.4 POST /api/rules/visibility

修改指定节点某条规则的可见范围，并下发到该客户端落盘。

```json
{
  "node": "office-pc",
  "name": "welcome",
  "visibility": "nodes",
  "visible_to": ["PC-other"]
}
```

节点离线或下发失败返回 `409`。

### 5.5 GET /api/system

返回本机运行指标：

`cpu_percent`、`mem_percent`、`mem_used`、`mem_total`、`swap_percent`、`swap_used`、`swap_total`、
`disk_percent`、`disk_used`、`disk_total`、`uptime_seconds`、`process_rss`、`threads`、`goroutines`、
`net_sent`、`net_recv`、`net_up_bps`、`net_down_bps`、`ipv4`、`ipv6`、`tcp_count`、`udp_count`。

### 5.6 GET/POST /api/panel

读取面板设置：

```json
{
  "dashboard": "127.0.0.1:10004",
  "control": ":10000",
  "session_minutes": 720,
  "page_size": 25,
  "version": "v0.4.0",
  "config_path": "/opt/mtun/configs/server.yaml",
  "pin_set": true
}
```

保存设置：

```json
{"dashboard":"0.0.0.0:10004","session_minutes":720,"page_size":25}
```

限制：`session_minutes` 5-10080，`page_size` 5-200。返回 `{"saved":true,"restart_required":true}`。

### 5.7 POST /api/panel/restart

在本进程内重启面板使新设置生效，返回 `{"restarting":true,"relogin":true}`，会话失效需重新登录。

### 5.8 GET/POST /api/autostart

与客户端一致，见 4.6、4.7。

### 5.9 GET/POST /api/update

与客户端一致，见 4.8、4.9。更新成功后服务端进程自动重启。

### 5.10 POST /api/nodes/rules

服务端对任意节点增删改一条转发规则（普通功能，不需要 PIN）。在线节点立即生效并通过
`rules_sync` 下发落盘；离线节点写入 `<server.yaml>.rules.json`，下次上线自动应用。

```json
{"node":"office-pc","op":"add","rule":{"name":"api","proto":"tcp","listen":10181,"target":"127.0.0.1:8080","visibility":"self"}}
```

- `op`：`add`、`update`、`delete`；`delete` 只需 `rule.name`。
- 规则校验同客户端，端口需在 `publish.port_range` 内，名称或端口冲突返回 `409`。
- 响应：`{"ok":true,"online":true,"queued":false,"rules":[...]}`。

### 5.11 POST /api/nodes/access

设置节点访问策略并立即断开在线会话（普通功能，不需要 PIN）。

```json
{
  "id": "office-pc",
  "ban_seconds": 3600,
  "permanent": false,
  "daily": [{"start":"08:00","end":"22:00"}],
  "allow_cidrs": ["1.2.3.0/24"],
  "deny_cidrs": ["5.6.7.8"],
  "allow_os": ["windows", "linux"]
}
```

- `ban_seconds` 为 0 且 `permanent=false` 时仅应用访问限制，不封禁。
- `daily` 支持跨午夜（如 `22:00-06:00`），按服务器本地时区计算，心跳时也会校验。
- `deny_cidrs` 优先于 `allow_cidrs`；`allow_os` 为空表示不限系统。
- 策略持久化到 `<server.yaml>.access.json`，重启后仍生效。

`POST /api/nodes/unban`：`{"id":"office-pc"}`，清除策略并解除封禁。

`GET /api/nodes/access`：返回全部节点策略，形如 `{"nodes":{"PC-1":{...}}}`。

### 5.12 GET/POST /api/pin 与 POST /api/panel/secure

- `GET /api/pin` → `{"set":true|false}`。
- `POST /api/pin`：
  - 首次设置：`{"pin":"123456","confirm":"123456"}`。
  - 修改：`{"current_pin":"123456","pin":"654321","confirm":"654321"}`。
- `POST /api/panel/secure`：四类敏感操作，必须携带 PIN。
  ```json
  {"pin":"123456","dashboard":"0.0.0.0:10004","control":":10000",
   "admin_password":"NewStrongPass123","join_token":"new-join-token"}
  ```
  - 字段均可选，只处理传入项。
  - 修改 `dashboard`、`control` 或 `join_token` 后返回 `{"saved":true,"restarting":true}`，服务端自动重启使新配置生效。
  - 仅修改 `admin_password` 时不重启，返回 `{"saved":true,"relogin":true}`，需要重新登录。
  - PIN 未设置返回 `409 {"need_setup":true}`；错误 `401`；连续 5 次错误 `423 {"retry_after":60}`。

---

## 6. 规则可见范围

| 取值 | 含义 |
| --- | --- |
| `all` 或留空 | 所有客户端在网络页可见 |
| `self` | 仅规则所属客户端自己可见 |
| `nodes` | 仅 `visible_to` 中列出的节点可见 |

规则所属客户端始终能看到自己的规则。服务端管理员能看到所有规则，并可通过
`POST /api/rules/visibility` 修改可见范围。

---

## 7. 命令行参考

| 命令 | 说明 |
| --- | --- |
| `mtun server -c 配置` | 启动服务端 |
| `mtun client -c 配置` | 启动客户端 |
| `mtun status [-url 地址] [-c 配置]` | 查看运行状态 |
| `mtun watch [-interval 2s] [-c 配置]` | 实时刷新状态与节点 |
| `mtun nodes [-c 配置]` | 查看服务端节点 |
| `mtun kick -id 节点ID [-ban 60s] [-c 配置]` | 强制节点下线并临时封禁 |
| `mtun connect -server host:port [-key 密码] [-c 配置]` | 配置服务端与接入密码 |
| `mtun passwd -c 配置 [-password 密码]` | 设置管理密码 |
| `mtun access -c 配置 [-password 密码]` | 设置客户端接入密码 |
| `mtun pin -c 配置 -new 123456` | 设置或重置服务端敏感操作 PIN |
| `mtun rule add/list/delete` | 管理转发规则 |
| `mtun web [-url 地址]` | 打开本地控制台 |
| `mtun service` | 查看当前平台自启动说明 |
| `mtun update [-check] [-c 配置]` | 在线更新 |
| `mtun default server\|client` | 输出默认配置 |
| `mtun check -c 配置 [-role server\|client]` | 校验配置 |
| `mtun version` | 输出版本 |

---

## 8. 配置文件

`server.yaml` / `client.yaml` 主要字段：

| 字段 | 说明 |
| --- | --- |
| `role` | `server` 或 `client` |
| `node` | 节点名称，客户端默认使用主机名 |
| `server` | 服务端地址，仅客户端使用 |
| `control` | 控制面监听，默认 `:10000` |
| `wg` / `stun` | 预留端口，默认 `:10001` / `:10003` |
| `relay` | 中继监听，默认 `:10002` |
| `dashboard` | 面板地址，服务端默认 `127.0.0.1:10004`，客户端默认 `127.0.0.1:10005` |
| `update_repo` | 在线更新仓库，留空时使用构建注入值 |
| `panel.session_minutes` | 会话有效期（分钟） |
| `panel.page_size` | 列表分页大小 |
| `overlay.cidr` | 组网网段，默认 `100.64.0.0/10` |
| `overlay.server_ip` | 服务端虚拟 IP，默认 `100.64.0.1` |
| `publish.port_range` | 转发端口范围，默认 `10100-10999` |
| `publish.rules` | 转发规则列表 |
| `routes` / `acl` | 预留字段 |

配套文件：

- `<配置>.passwd`：管理密码哈希，删除后需重新设置。
- `<配置>.apitoken`：API Token，自动生成，勿泄露。
- `<配置>.join`：接入密码明文（服务端为权威值，客户端为副本）。
- `<配置>.pin`：服务端敏感操作 PIN 哈希（仅服务端）。
- `<server.yaml>.access.json`：节点访问策略（封禁、时段、IP 段、系统）。
- `<server.yaml>.rules.json`：离线节点的待同步规则操作。

---

## 9. 常见问题

**401 unauthorized**
未登录或未带 Token。脚本请使用 `Authorization: Bearer <apitoken>`。

**403 forbidden origin / forbidden host**
浏览器跨站写请求被拦截。请从面板同源页面操作；命令行请求不应带 Origin。

**403 autostart permission denied**
当前用户无权修改自启动项。管理员可注册 SYSTEM 开机任务；普通用户只能注册自身登录启动。

**409 autostart conflict**
同时存在 NSSM 服务、计划任务或旧启动项。在面板中先禁用再启用一次即可清理。

**415 请求需为 application/json**
POST 请求补上 `Content-Type: application/json`。

**客户端连不上服务端**
确认服务端 `10000/TCP` 已放行、接入密码正确，且日志中出现“已连接服务端”。

**转发端口访问不通**
确认端口在 `publish.port_range` 内，防火墙已放行，内网目标可从客户端访问。

**在线更新失败**
检查服务器能否访问 GitHub；可使用 `-Mirror` 镜像或 `-LocalFile` 离线方式安装。
更新过程中断可重新执行 `mtun update`，配置和规则不受影响。

**忘记管理密码**
删除 `<配置>.passwd` 后重新执行 `mtun passwd -c <配置>`；客户端也可在未设置密码时通过网页首次设置。

**忘记 PIN**
在服务端终端执行 `mtun pin -c configs/server.yaml -new 123456` 重新设置；连续错误后请等待 60 秒。

**修改接入端口后客户端连不上**
确认防火墙已放行新端口，并在客户端重新执行 `mtun connect` 更新服务端地址。

**修改接入密码后客户端无法重连**
接入密码变更后所有客户端都需要更新：`mtun connect -c client.yaml -server 地址 -key 新密码`。

**封禁后节点一直重连失败**
在面板“节点 → 访问策略”中点击“解除限制”，或调用 `POST /api/nodes/unban`。

---

## 10. 安全建议

1. 面板默认只监听回环地址；确需公网访问时绑定后务必使用强管理密码。
2. `.passwd`、`.apitoken`、`.join` 均含敏感信息，不要提交到仓库或截图外发。
3. 公网服务端仅放行必要端口，转发端口按需开放。
4. 定期执行 `mtun update` 获取修复；更新会保留配置与规则。
5. 接入密码为全服务端共享，泄露后所有客户端都需更新；按用户独立接入密码正在规划中。

---

## 11. 版本

| 版本 | 主要变化 |
| --- | --- |
| v0.4.0 | 服务端面板支持 PIN 验证的端口/密码修改、规则增删改与节点访问策略 |
| v0.3.3 | 服务端与客户端支持 Windows 开机自启动 |
| v0.3.2 | 中继组网、规则可见范围、分仓更新与镜像下载 |
| v0.3.0 | 服务端/客户端拆分独立仓库发布 |
