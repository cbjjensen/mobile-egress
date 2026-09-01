package nodeservice

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/httpconnect"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/socks"
)

func TestServiceOwnsLoopbackSOCKSAndHTTPConnectAndStopsCleanly(t *testing.T) {
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
		if status.Running && status.Address == "127.0.0.1:1080" && status.HTTPAddress == "127.0.0.1:1081" && status.Connected {
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
	connection, err = net.DialTimeout("tcp4", "127.0.0.1:1081", time.Second)
	if err != nil {
		cancel()
		t.Fatalf("loopback HTTP CONNECT listener is unavailable: %v", err)
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
	if _, err := net.DialTimeout("tcp4", "127.0.0.1:1081", 100*time.Millisecond); err == nil {
		t.Fatal("HTTP CONNECT listener remained open after service stop")
	}
}

func TestServiceRollsBackSOCKSWhenHTTPConnectPortIsUnavailable(t *testing.T) {
	occupied, err := net.Listen("tcp4", "127.0.0.1:1081")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	service := NewService(configuredRepository(t), &fakeDialer{})
	service.retryInterval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := service.Run(ctx); err == nil {
		t.Fatal("Service.Run() accepted a partial startup with HTTP CONNECT unavailable")
	}
	if status := service.Status(); status.Running || status.Address != "" || status.HTTPAddress != "" {
		t.Fatalf("status after partial startup = %#v, want stopped", status)
	}
	if _, err := net.DialTimeout("tcp4", "127.0.0.1:1080", 100*time.Millisecond); err == nil {
		t.Fatal("SOCKS listener remained open after HTTP CONNECT startup failed")
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

func TestSwitchingTunnelReportsTheSharedRelayUnavailableError(t *testing.T) {
	t.Parallel()

	_, err := (&switchingTunnel{}).OpenStream(context.Background(), "example.test", 443)
	if !errors.Is(err, relayclient.ErrRelayUnavailable) {
		t.Fatalf("OpenStream() error = %v, want relayclient.ErrRelayUnavailable", err)
	}
}

func TestSOCKSHTTPConnectAndPooledHTTPShareThirtyTwoRelaySessionSlots(t *testing.T) {
	tunnel := &capacityTunnel{healthy: true}
	opener := &switchingTunnel{}
	opener.swap(tunnel)
	defer opener.swap(nil)

	socksServer := socks.NewServer(socks.Config{Username: "user", Password: "password", Opener: opener})
	if err := socksServer.Start(0); err != nil {
		t.Fatal(err)
	}
	defer socksServer.Stop()
	httpServer := httpconnect.NewServer(httpconnect.Config{Username: "user", Password: "password", Opener: opener})
	if err := httpServer.Start(0); err != nil {
		t.Fatal(err)
	}
	defer httpServer.Stop()

	socksClients := make([]net.Conn, 0, 16)
	for index := 0; index < 16; index++ {
		client := authenticatedSOCKSConnection(t, socksServer.Addr().String())
		writeServiceTestAll(t, client, serviceConnectDomainRequest("socks.example", 443))
		readServiceTestEqual(t, client, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		socksClients = append(socksClients, client)
	}
	defer func() {
		for _, client := range socksClients {
			_ = client.Close()
		}
	}()

	connectClients := make([]net.Conn, 0, 14)
	for index := 0; index < 14; index++ {
		client, err := net.DialTimeout("tcp4", httpServer.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		request := "CONNECT connect.example:443 HTTP/1.1\r\nHost: connect.example:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n"
		if _, err := io.WriteString(client, request); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodConnect})
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("CONNECT status = %d, want 200", response.StatusCode)
		}
		_ = response.Body.Close()
		connectClients = append(connectClients, client)
	}
	defer func() {
		for _, client := range connectClients {
			_ = client.Close()
		}
	}()

	proxy, err := url.Parse("http://user:password@" + httpServer.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	for _, host := range []string{"idle-one.example", "idle-two.example"} {
		response, requestErr := client.Get("http://" + host + "/")
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
	}

	if active := tunnel.Active(); active != 32 {
		t.Fatalf("shared relay session active streams = %d, want 32", active)
	}
	if _, err := opener.OpenStream(context.Background(), "over-capacity.example", 443); !errors.Is(err, relayclient.ErrStreamLimit) {
		t.Fatalf("33rd shared relay stream error = %v, want ErrStreamLimit", err)
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

type capacityTunnel struct {
	mu      sync.Mutex
	healthy bool
	active  int
}

func (tunnel *capacityTunnel) Healthy() bool {
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return tunnel.healthy
}

func (tunnel *capacityTunnel) OpenStream(_ context.Context, _ string, port uint16) (io.ReadWriteCloser, error) {
	tunnel.mu.Lock()
	if !tunnel.healthy {
		tunnel.mu.Unlock()
		return nil, relayclient.ErrRelayUnavailable
	}
	if tunnel.active >= relayclient.MaxConcurrentStreams {
		tunnel.mu.Unlock()
		return nil, relayclient.ErrStreamLimit
	}
	tunnel.active++
	tunnel.mu.Unlock()

	client, remote := net.Pipe()
	stream := &capacityStream{ReadWriteCloser: client, release: func() {
		tunnel.mu.Lock()
		tunnel.active--
		tunnel.mu.Unlock()
		_ = remote.Close()
	}}
	if port == 80 {
		go func() {
			request, err := http.ReadRequest(bufio.NewReader(remote))
			if err != nil {
				return
			}
			_ = request.Body.Close()
			_, _ = fmt.Fprint(remote, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
		}()
	}
	return stream, nil
}

func (tunnel *capacityTunnel) Close() error {
	tunnel.mu.Lock()
	tunnel.healthy = false
	tunnel.mu.Unlock()
	return nil
}

func (tunnel *capacityTunnel) Active() int {
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return tunnel.active
}

type capacityStream struct {
	io.ReadWriteCloser
	once    sync.Once
	release func()
}

func (stream *capacityStream) Close() error {
	err := stream.ReadWriteCloser.Close()
	stream.once.Do(stream.release)
	return err
}

func authenticatedSOCKSConnection(t *testing.T, address string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	writeServiceTestAll(t, connection, []byte{5, 1, 2})
	readServiceTestEqual(t, connection, []byte{5, 2})
	writeServiceTestAll(t, connection, []byte{1, 4, 'u', 's', 'e', 'r', 8, 'p', 'a', 's', 's', 'w', 'o', 'r', 'd'})
	readServiceTestEqual(t, connection, []byte{1, 0})
	return connection
}

func serviceConnectDomainRequest(host string, port uint16) []byte {
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, host...)
	return append(request, byte(port>>8), byte(port))
}

func writeServiceTestAll(t *testing.T, writer io.Writer, value []byte) {
	t.Helper()
	if _, err := writer.Write(value); err != nil {
		t.Fatal(err)
	}
}

func readServiceTestEqual(t *testing.T, reader io.Reader, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("read = %v, want %v", got, want)
	}
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
