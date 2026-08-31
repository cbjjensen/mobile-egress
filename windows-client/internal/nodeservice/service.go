package nodeservice

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"mobile-egress/windows-client/internal/httpconnect"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/socks"
)

type Tunnel interface {
	Healthy() bool
	OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error)
	Close() error
}

type Dialer interface {
	Dial(context.Context, relayclient.Identity) (Tunnel, error)
}

type DefaultDialer struct{}

func (DefaultDialer) Dial(ctx context.Context, identity relayclient.Identity) (Tunnel, error) {
	return relayclient.DialSession(ctx, identity)
}

type ServiceStatus struct {
	Running     bool   `json:"running"`
	Connected   bool   `json:"connected"`
	Address     string `json:"address"`
	HTTPAddress string `json:"httpAddress"`
	Serial      string `json:"serial,omitempty"`
}

type Service struct {
	repository    *Repository
	dialer        Dialer
	retryInterval time.Duration

	mu     sync.RWMutex
	status ServiceStatus
}

func NewService(repository *Repository, dialer Dialer) *Service {
	return &Service{repository: repository, dialer: dialer, retryInterval: 2 * time.Second}
}

func (service *Service) Run(ctx context.Context) error {
	if service == nil || service.repository == nil || service.dialer == nil {
		return errors.New("node repository and relay dialer are required")
	}
	runtime, err := service.repository.Runtime(ctx)
	if err != nil {
		return err
	}
	opener := &switchingTunnel{}
	proxy := socks.NewServer(socks.Config{
		Username: runtime.Username, Password: runtime.Password, Opener: opener,
	})
	if err := proxy.Start(runtime.Port); err != nil {
		return err
	}
	httpProxy := httpconnect.NewServer(httpconnect.Config{
		Username: runtime.Username, Password: runtime.Password, Opener: opener,
	})
	if err := httpProxy.Start(1081); err != nil {
		_ = proxy.Stop()
		return err
	}
	service.setStatus(ServiceStatus{
		Running: true, Address: "127.0.0.1:1080", HTTPAddress: "127.0.0.1:1081", Serial: runtime.Identity.Serial,
	})
	defer func() {
		_ = httpProxy.Stop()
		_ = proxy.Stop()
		opener.swap(nil)
		service.setStatus(ServiceStatus{})
	}()

	retryInterval := service.retryInterval
	if retryInterval <= 0 {
		retryInterval = 2 * time.Second
	}
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		current := opener.current()
		if current == nil || !current.Healthy() {
			if current != nil {
				opener.swap(nil)
			}
			service.updateConnected(false)
			tunnel, err := service.dialer.Dial(ctx, runtime.Identity)
			if err == nil {
				opener.swap(tunnel)
				service.updateConnected(true)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (service *Service) Status() ServiceStatus {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.status
}

func (service *Service) setStatus(status ServiceStatus) {
	service.mu.Lock()
	service.status = status
	service.mu.Unlock()
}

func (service *Service) updateConnected(connected bool) {
	service.mu.Lock()
	service.status.Connected = connected
	service.mu.Unlock()
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
