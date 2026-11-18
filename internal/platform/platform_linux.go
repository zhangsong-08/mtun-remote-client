//go:build linux

package platform

func ConfigDir() string { return "/etc/mtun" }

func DataDir() string { return "/var/lib/mtun" }

func ServiceHint() string {
	return `Linux systemd:
  sudo install -m 0755 mtun /usr/local/bin/mtun
  sudo mkdir -p /etc/mtun /var/lib/mtun
  # 将 server.yaml / client.yaml 放入 /etc/mtun
  # 写入 /etc/systemd/system/mtun.service:
[Unit]
Description=mtun
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/mtun server -c /etc/mtun/server.yaml
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
NoNewPrivileges=true
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target

  sudo systemctl daemon-reload
  sudo systemctl enable --now mtun`
}
