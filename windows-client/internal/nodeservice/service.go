package nodeservice

import (
	"context"
	"io"
	"mobile-egress/windows-client/internal/relayclient"
	"sync"
)

type Tunnel interface {
	Done() <-chan struct{}
	Healthy() bool
	OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error)
	Close() error
}

type switchingTunnel struct {
	mu     sync.RWMutex
	tunnel Tunnel
}

func (opener *switchingTunnel) Healthy() bool {
	tunnel := opener.current()
	return tunnel != nil && tunnel.Healthy()
}

func (opener *switchingTunnel) OpenStream(ctx context.Context, host string, port uint16) (io.ReadWriteCloser, error) {
	tunnel := opener.current()
	if tunnel == nil || !tunnel.Healthy() {
		return nil, relayclient.ErrRelayUnavailable
	}
	return tunnel.OpenStream(ctx, host, port)
}

func (opener *switchingTunnel) current() Tunnel {
	opener.mu.RLock()
	defer opener.mu.RUnlock()
	return opener.tunnel
}

func (opener *switchingTunnel) swap(replacement Tunnel) {
	opener.mu.Lock()
	previous := opener.tunnel
	opener.tunnel = replacement
	opener.mu.Unlock()
	if previous != nil && previous != replacement {
		_ = previous.Close()
	}
}
