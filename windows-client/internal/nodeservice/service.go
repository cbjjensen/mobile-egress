package nodeservice

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"time"

	"mobile-egress/windows-client/internal/httpconnect"
	"mobile-egress/windows-client/internal/proxyendpoint"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/socks"
)

type Tunnel interface {
	Done() <-chan struct{}
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
	if err := httpProxy.Start(proxyendpoint.HTTPConnectPort); err != nil {
		_ = proxy.Stop()
		return err
	}
	service.setStatus(ServiceStatus{
		Running: true, Address: proxyendpoint.SOCKSAddress(), HTTPAddress: proxyendpoint.HTTPConnectAddress(), Serial: runtime.Identity.Serial,
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

	backoff := reconnectBackoff{base: retryInterval}
	for {
		if ctx.Err() != nil {
			return nil
		}
		tunnel, err := service.dialer.Dial(ctx, runtime.Identity)
		if err == nil {
			opener.swap(tunnel)
			service.updateConnected(true)
			stable := time.NewTimer(30 * time.Second)
			select {
			case <-ctx.Done():
				stable.Stop()
				return nil
			case <-tunnel.Done():
				stable.Stop()
			case <-stable.C:
				backoff.reset()
				select {
				case <-ctx.Done():
					return nil
				case <-tunnel.Done():
				}
			}
			service.updateConnected(false)
			opener.swap(nil)
		}
		timer := time.NewTimer(backoff.next(rand.Float64()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
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

// The nominal delays are 2, 4, 8, 16, 30 seconds. Equal jitter spreads
// reconnects over the latter half of each interval, never exceeding 30s.
type reconnectBackoff struct{ base, current time.Duration }

func (backoff *reconnectBackoff) reset() { backoff.current = 0 }
func (backoff *reconnectBackoff) next(random float64) time.Duration {
	if backoff.current == 0 {
		backoff.current = backoff.base
	} else {
		backoff.current *= 2
	}
	if backoff.current > 30*time.Second {
		backoff.current = 30 * time.Second
	}
	return backoff.current/2 + time.Duration(float64(backoff.current/2)*random)
}
