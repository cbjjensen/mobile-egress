package nodeservice

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

func TestServiceOwnsLoopbackSOCKSAndStopsCleanly(t *testing.T) {
	repository := configuredRepository(t)
	tunnel := &fakeTunnel{healthy: true}
	dialer := &fakeDialer{results: []dialResult{{err: errors.New("temporarily offline")}, {tunnel: tunnel}}}
	service := NewService(repository, dialer)
	service.retryInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		status := service.Status()
		if status.Running && status.Address == "127.0.0.1:1080" && status.Connected {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("service did not become ready: %#v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	connection, err := net.DialTimeout("tcp4", "127.0.0.1:1080", time.Second)
	if err != nil {
		cancel()
		t.Fatalf("loopback SOCKS listener is unavailable: %v", err)
	}
	_ = connection.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Service.Run() returned an error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Service.Run() did not stop")
	}
	if !tunnel.closed {
		t.Fatal("Service.Run() did not close the relay tunnel")
	}
	if _, err := net.DialTimeout("tcp4", "127.0.0.1:1080", 100*time.Millisecond); err == nil {
		t.Fatal("SOCKS listener remained open after service stop")
	}
}

func TestServiceRejectsMissingConfiguration(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	if _, err := repository.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := NewService(repository, &fakeDialer{})
	if err := service.Run(context.Background()); err == nil {
		t.Fatal("Service.Run() accepted an unconfigured node")
	}
}

type dialResult struct {
	tunnel Tunnel
	err    error
}

type fakeDialer struct {
	mu      sync.Mutex
	results []dialResult
}

func (dialer *fakeDialer) Dial(_ context.Context, _ relayclient.Identity) (Tunnel, error) {
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	if len(dialer.results) == 0 {
		return nil, errors.New("offline")
	}
	result := dialer.results[0]
	dialer.results = dialer.results[1:]
	return result.tunnel, result.err
}

type fakeTunnel struct {
	mu      sync.Mutex
	healthy bool
	closed  bool
}

func (tunnel *fakeTunnel) Healthy() bool {
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return tunnel.healthy && !tunnel.closed
}

func (tunnel *fakeTunnel) OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error) {
	return nil, errors.New("not used")
}

func (tunnel *fakeTunnel) Close() error {
	tunnel.mu.Lock()
	tunnel.closed = true
	tunnel.mu.Unlock()
	return nil
}

func configuredRepository(t *testing.T) *Repository {
	t.Helper()
	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	configuration := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "service-user", "service-password")
	applyNodeConfig(t, repository, bootstrap.ConfigurationPublicKey, configuration)
	return repository
}
