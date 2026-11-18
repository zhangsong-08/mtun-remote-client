package wg

import "log/slog"

type Stats struct {
	ReceiveBytes  uint64
	TransmitBytes uint64
}

// Device 是数据面的抽象边界，后续用 wireguard-go + tun 实现。
type Device interface {
	Up() error
	Close() error
	SetPeerEndpoint(publicKey, endpoint string) error
	Stats() Stats
}

// Noop 让骨架在无 TUN 权限时也能跑通控制面。
type Noop struct {
	log *slog.Logger
}

func NewNoop(log *slog.Logger) *Noop {
	return &Noop{log: log}
}

func (n *Noop) Up() error {
	n.log.Info("wireguard disabled in skeleton: TUN will be enabled in M1")
	return nil
}

func (n *Noop) Close() error {
	return nil
}

func (n *Noop) SetPeerEndpoint(publicKey, endpoint string) error {
	n.log.Debug("wireguard endpoint ignored", "peer", publicKey, "endpoint", endpoint)
	return nil
}

func (n *Noop) Stats() Stats {
	return Stats{}
}
