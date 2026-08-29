package client

import (
	"context"
	"errors"
	"io"
	"sync"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/socks"
)

type Tunnel interface {
	Healthy() bool
	OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error)
	Status() relayclient.SessionStatus
	Close() error
}

type Gateway interface {
	Enroll(context.Context, pairing.Bundle) (relayclient.Identity, error)
	DialSession(context.Context, relayclient.Identity) (Tunnel, error)
	IssuePairing(context.Context, relayclient.Identity, string) (relayclient.PairingCode, error)
	Revoke(context.Context, relayclient.Identity, string) error
}

type DefaultGateway struct{}

func (DefaultGateway) Enroll(ctx context.Context, bundle pairing.Bundle) (relayclient.Identity, error) {
	return relayclient.Enroll(ctx, bundle)
}

func (DefaultGateway) DialSession(ctx context.Context, identity relayclient.Identity) (Tunnel, error) {
	return relayclient.DialSession(ctx, identity)
}

func (DefaultGateway) IssuePairing(ctx context.Context, identity relayclient.Identity, role string) (relayclient.PairingCode, error) {
	return relayclient.IssuePairing(ctx, identity, role)
}

func (DefaultGateway) Revoke(ctx context.Context, identity relayclient.Identity, serial string) error {
	return relayclient.Revoke(ctx, identity, serial)
}

type Core struct {
	operations  sync.Mutex
	mu          sync.RWMutex
	repository  *Repository
	gateway     Gateway
	credentials Credentials
	owner       *relayclient.Identity
	client      *relayclient.Identity
	port        uint16
	tunnel      Tunnel
	proxy       *socks.Server
}

func NewCore(ctx context.Context, store securestore.Store, gateway Gateway) (*Core, error) {
	if store == nil || gateway == nil {
		return nil, errors.New("secure store and relay gateway are required")
	}
	repository := NewRepository(store)
	credentials, err := repository.LoadOrCreateCredentials(ctx)
	if err != nil {
		return nil, err
	}
	core := &Core{repository: repository, gateway: gateway, credentials: credentials, port: 1080}
	owner, _, err := repository.LoadOwnerIdentity(ctx)
	if err == nil {
		core.owner = &owner
	} else if !errors.Is(err, securestore.ErrNotFound) {
		return nil, err
	}
	clientIdentity, port, err := repository.LoadClientIdentity(ctx)
	if err == nil {
		core.client = &clientIdentity
		if port != 0 {
			core.port = port
		}
	} else if !errors.Is(err, securestore.ErrNotFound) {
		return nil, err
	}
	return core, nil
}

func (core *Core) Pair(ctx context.Context, bundle pairing.Bundle) error {
	return core.BootstrapOwner(ctx, bundle)
}

func (core *Core) BootstrapOwner(ctx context.Context, bundle pairing.Bundle) error {
	core.operations.Lock()
	defer core.operations.Unlock()
	if bundle.Role != "owner" {
		return errors.New("an owner invitation is required")
	}
	core.mu.RLock()
	hasOwner := core.owner != nil
	core.mu.RUnlock()
	if hasOwner {
		return errors.New("owner identity is already enrolled")
	}
	if err := core.stopProxy(); err != nil {
		return err
	}
	identity, err := core.gateway.Enroll(ctx, bundle)
	if err != nil {
		return err
	}
	if !validIdentityForRole(identity, "owner") {
		return errors.New("relay returned an invalid owner identity")
	}
	if err := core.repository.SaveOwnerIdentity(ctx, identity); err != nil {
		return err
	}
	core.mu.Lock()
	core.owner = &identity
	core.mu.Unlock()
	return core.setupClient(ctx)
}

func (core *Core) RetryClientSetup(ctx context.Context) error {
	core.operations.Lock()
	defer core.operations.Unlock()
	return core.setupClient(ctx)
}

func (core *Core) setupClient(ctx context.Context) error {
	core.mu.RLock()
	if core.owner == nil {
		core.mu.RUnlock()
		return errors.New("owner identity required")
	}
	if core.client != nil {
		core.mu.RUnlock()
		return nil
	}
	owner := *core.owner
	core.mu.RUnlock()
	issued, err := core.gateway.IssuePairing(ctx, owner, "client")
	if err != nil {
		return err
	}
	if issued.Role != "client" || issued.Code == "" {
		return errors.New("relay returned an invalid client pairing response")
	}
	clientIdentity, err := core.gateway.Enroll(ctx, pairing.Bundle{
		Version: pairing.Version, RelayURL: owner.RelayURL, CACertificatePEM: owner.CACertificatePEM,
		Capability: issued.Code, Role: "client", ExpiresAt: issued.ExpiresAt,
	})
	if err != nil {
		return err
	}
	if !validIdentityForRole(clientIdentity, "client") {
		return errors.New("relay returned an invalid client identity")
	}
	if err := core.repository.SaveClientIdentity(ctx, clientIdentity); err != nil {
		return err
	}
	core.mu.Lock()
	core.client = &clientIdentity
	core.mu.Unlock()
	return nil
}

func validIdentityForRole(identity relayclient.Identity, role string) bool {
	return identity.Role == role && identity.RelayURL != "" && identity.Serial != "" && identity.PrivateKeyPEM != "" && identity.CertificatePEM != "" && identity.CACertificatePEM != ""
}

func (core *Core) StartProxy(port uint16) error {
	core.operations.Lock()
	defer core.operations.Unlock()
	if port == 0 {
		return errors.New("SOCKS port must be non-zero")
	}
	core.mu.RLock()
	if core.proxy != nil {
		core.mu.RUnlock()
		return errors.New("SOCKS proxy is already running")
	}
	if core.client == nil {
		core.mu.RUnlock()
		return errors.New("pair this Windows client first")
	}
	identity := *core.client
	core.mu.RUnlock()
	if identity.Role != "client" {
		return errors.New("only a client identity may start the SOCKS proxy")
	}
	tunnel, err := core.gateway.DialSession(context.Background(), identity)
	if err != nil {
		return err
	}
	proxy := socks.NewServer(socks.Config{
		Username: core.credentials.Username, Password: core.credentials.Password, Opener: tunnel,
	})
	if err := proxy.Start(port); err != nil {
		tunnel.Close()
		return err
	}
	if err := core.repository.SavePort(context.Background(), port); err != nil {
		proxy.Stop()
		tunnel.Close()
		return err
	}
	core.mu.Lock()
	core.port = port
	core.tunnel = tunnel
	core.proxy = proxy
	core.mu.Unlock()
	return nil
}

func (core *Core) StopProxy() error {
	core.operations.Lock()
	defer core.operations.Unlock()
	return core.stopProxy()
}

func (core *Core) stopProxy() error {
	core.mu.Lock()
	proxy := core.proxy
	tunnel := core.tunnel
	core.proxy = nil
	core.tunnel = nil
	core.mu.Unlock()
	var firstErr error
	if proxy != nil {
		firstErr = proxy.Stop()
	}
	if tunnel != nil {
		if err := tunnel.Close(); firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (core *Core) Status() Status {
	core.mu.RLock()
	owner := core.owner
	clientIdentity := core.client
	proxy := core.proxy
	tunnel := core.tunnel
	port := core.port
	credentials := core.credentials
	core.mu.RUnlock()
	status := Status{Relay: "offline", Port: port, OwnerReady: owner != nil, ClientReady: clientIdentity != nil}
	if clientIdentity != nil {
		status.Paired = true
		status.Proxy = ProxyEndpoint{Credentials: credentials, Port: port}.String()
	}
	if owner != nil || clientIdentity != nil {
		if clientIdentity != nil {
			status.Role = clientIdentity.Role
		} else {
			status.Role = owner.Role
		}
	}
	if proxy != nil {
		proxyStatus := proxy.Status()
		status.Running = proxyStatus.Running
		status.ActiveStreams = proxyStatus.ActiveStreams
		status.BytesUp = proxyStatus.BytesUp
		status.BytesDown = proxyStatus.BytesDown
	}
	if tunnel != nil {
		tunnelStatus := tunnel.Status()
		if tunnelStatus.Connected {
			status.Relay = "connected"
		}
		status.AgentAvailable = tunnelStatus.AgentAvailable
	}
	return status
}

func (core *Core) ProxyLine() (string, error) {
	core.mu.RLock()
	paired := core.client != nil
	endpoint := ProxyEndpoint{Credentials: core.credentials, Port: core.port}
	core.mu.RUnlock()
	if !paired {
		return "", errors.New("pair this Windows client first")
	}
	return endpoint.Reveal(), nil
}

func (core *Core) IssuePairing(ctx context.Context, role string) (pairing.Bundle, error) {
	core.mu.RLock()
	if core.owner == nil {
		core.mu.RUnlock()
		return pairing.Bundle{}, errors.New("owner identity required")
	}
	identity := *core.owner
	core.mu.RUnlock()
	if identity.Role != "owner" {
		return pairing.Bundle{}, errors.New("owner identity required")
	}
	issued, err := core.gateway.IssuePairing(ctx, identity, role)
	if err != nil {
		return pairing.Bundle{}, err
	}
	return pairing.Bundle{
		Version: pairing.Version, RelayURL: identity.RelayURL,
		CACertificatePEM: identity.CACertificatePEM, Capability: issued.Code,
		Role: issued.Role, ExpiresAt: issued.ExpiresAt,
	}, nil
}

func (core *Core) Revoke(ctx context.Context, serial string) error {
	core.mu.RLock()
	if core.owner == nil {
		core.mu.RUnlock()
		return errors.New("owner identity required")
	}
	identity := *core.owner
	core.mu.RUnlock()
	if identity.Role != "owner" {
		return errors.New("owner identity required")
	}
	return core.gateway.Revoke(ctx, identity, serial)
}

func (core *Core) Close() error { return core.StopProxy() }
