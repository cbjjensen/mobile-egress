package httpconnect

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/relayclient"
)

type fakeOpener struct {
	mu          sync.Mutex
	healthy     bool
	openGate    <-chan struct{}
	openErr     error
	targetHost  string
	targetPort  uint16
	remoteConns []net.Conn
}

func (opener *fakeOpener) Healthy() bool { return opener.healthy }

func (opener *fakeOpener) OpenStream(ctx context.Context, host string, port uint16) (io.ReadWriteCloser, error) {
	if opener.openGate != nil {
		select {
		case <-opener.openGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if opener.openErr != nil {
		return nil, opener.openErr
	}
	client, remote := net.Pipe()
	opener.mu.Lock()
	opener.targetHost = host
	opener.targetPort = port
	opener.remoteConns = append(opener.remoteConns, remote)
	opener.mu.Unlock()
	return client, nil
}

func (opener *fakeOpener) latestRemote(t *testing.T) net.Conn {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		opener.mu.Lock()
		if len(opener.remoteConns) > 0 {
			remote := opener.remoteConns[len(opener.remoteConns)-1]
			opener.mu.Unlock()
			return remote
		}
		opener.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("relay stream was not opened")
	return nil
}

func TestServerBindsOnlyIPv4Loopback(t *testing.T) {
	t.Parallel()

	server := NewServer(Config{Username: "user", Password: "password", Opener: &fakeOpener{healthy: true}})
	if err := server.Start(0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })

	address := server.Addr()
	if address == nil || address.IP.String() != "127.0.0.1" {
		t.Fatalf("listener address = %v, want 127.0.0.1", address)
	}
}

func TestConnectAuthenticatesWaitsForRelayAndRoutesBytes(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	opener := &fakeOpener{healthy: true, openGate: gate}
	server := NewServer(Config{Username: "user", Password: "password", Opener: opener})
	if err := server.Start(0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })

	connection, err := net.DialTimeout("tcp4", server.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	request := "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("HTTP success was sent before the relay stream opened")
	}
	_ = connection.SetReadDeadline(time.Time{})
	close(gate)

	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", response.StatusCode)
	}
	_ = response.Body.Close()
	opener.mu.Lock()
	host, port := opener.targetHost, opener.targetPort
	opener.mu.Unlock()
	if host != "example.test" || port != 443 {
		t.Fatalf("relay target = %s:%d, want example.test:443", host, port)
	}

	remote := opener.latestRemote(t)
	defer remote.Close()
	if _, err := io.WriteString(connection, "upstream"); err != nil {
		t.Fatal(err)
	}
	readEqual(t, remote, []byte("upstream"))
	if _, err := io.WriteString(remote, "downstream"); err != nil {
		t.Fatal(err)
	}
	readEqual(t, connection, []byte("downstream"))
}

func TestPlainHTTPRequestIsForwardedThroughRelay(t *testing.T) {
	t.Parallel()

	opener := &fakeOpener{healthy: true}
	server := startTestServer(t, opener, 30*time.Second)
	connection := dialTestServer(t, server)
	defer connection.Close()
	request := "POST http://example.test/status?source=refract HTTP/1.1\r\nHost: example.test\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\nContent-Length: 12\r\n\r\nrequest-body"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}

	remote := opener.latestRemote(t)
	defer remote.Close()
	forwarded, err := http.ReadRequest(bufio.NewReader(remote))
	if err != nil {
		t.Fatal(err)
	}
	defer forwarded.Body.Close()
	if forwarded.Method != http.MethodPost || forwarded.URL.RequestURI() != "/status?source=refract" || forwarded.Host != "example.test" {
		t.Fatalf("forwarded request = %s %s host %q", forwarded.Method, forwarded.URL.RequestURI(), forwarded.Host)
	}
	forwardedBody, err := io.ReadAll(forwarded.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(forwardedBody) != "request-body" {
		t.Fatalf("forwarded body = %q, want request-body", forwardedBody)
	}
	if forwarded.Header.Get("Proxy-Authorization") != "" {
		t.Fatal("forwarded request exposed proxy credentials to the destination")
	}
	opener.mu.Lock()
	host, port := opener.targetHost, opener.targetPort
	opener.mu.Unlock()
	if host != "example.test" || port != 80 {
		t.Fatalf("relay target = %s:%d, want example.test:80", host, port)
	}
	if _, err := io.WriteString(remote, "HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nplain-ok"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "plain-ok" {
		t.Fatalf("proxy response = %d/%q, want 200/plain-ok", response.StatusCode, body)
	}
}

func TestServerReturnsProtocolSpecificFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opener   *fakeOpener
		request  string
		wantCode int
	}{
		{
			name: "authentication required", opener: &fakeOpener{healthy: true},
			request: "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n", wantCode: http.StatusProxyAuthRequired,
		},
		{
			name: "target requires port", opener: &fakeOpener{healthy: true},
			request: "CONNECT example.test HTTP/1.1\r\nHost: example.test\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusBadRequest,
		},
		{
			name: "agent unavailable", opener: &fakeOpener{healthy: false},
			request: "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusServiceUnavailable,
		},
		{
			name: "destination open failed", opener: &fakeOpener{healthy: true, openErr: errors.New("destination rejected")},
			request: "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusBadGateway,
		},
		{
			name: "existing stream capacity reached", opener: &fakeOpener{healthy: true, openErr: relayclient.ErrStreamLimit},
			request: "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusServiceUnavailable,
		},
		{
			name: "plain HTTP requires absolute target", opener: &fakeOpener{healthy: true},
			request: "GET /status HTTP/1.1\r\nHost: example.test\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusBadRequest,
		},
		{
			name: "plain HTTP agent unavailable", opener: &fakeOpener{healthy: false},
			request: "GET http://example.test/status HTTP/1.1\r\nHost: example.test\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusServiceUnavailable,
		},
		{
			name: "plain HTTP destination open failed", opener: &fakeOpener{healthy: true, openErr: errors.New("destination rejected")},
			request: "GET http://example.test/status HTTP/1.1\r\nHost: example.test\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusBadGateway,
		},
		{
			name: "plain HTTP stream capacity reached", opener: &fakeOpener{healthy: true, openErr: relayclient.ErrStreamLimit},
			request: "GET http://example.test/status HTTP/1.1\r\nHost: example.test\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n", wantCode: http.StatusServiceUnavailable,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := NewServer(Config{Username: "user", Password: "password", Opener: test.opener})
			if err := server.Start(0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Stop() })
			connection, err := net.DialTimeout("tcp4", server.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if _, err := io.WriteString(connection, test.request); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.wantCode {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantCode)
			}
		})
	}
}

func TestConnectSupportsIPv4AndIPv6Targets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		target   string
		wantHost string
	}{
		{target: "198.51.100.8:443", wantHost: "198.51.100.8"},
		{target: "[2001:db8::8]:443", wantHost: "2001:db8::8"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.target, func(t *testing.T) {
			t.Parallel()
			opener := &fakeOpener{healthy: true}
			server := startTestServer(t, opener, 30*time.Second)
			connection := dialTestServer(t, server)
			defer connection.Close()
			request := "CONNECT " + test.target + " HTTP/1.1\r\nHost: " + test.target + "\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n"
			if _, err := io.WriteString(connection, request); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			opener.mu.Lock()
			host, port := opener.targetHost, opener.targetPort
			opener.mu.Unlock()
			if host != test.wantHost || port != 443 {
				t.Fatalf("relay target = %s:%d, want %s:443", host, port, test.wantHost)
			}
			_ = opener.latestRemote(t).Close()
		})
	}
}

func TestConnectPreservesDataSentWhileRelayIsOpening(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	opener := &fakeOpener{healthy: true, openGate: gate}
	server := startTestServer(t, opener, 30*time.Second)
	connection := dialTestServer(t, server)
	defer connection.Close()
	request := "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\nearly-tls-bytes"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	time.Sleep(75 * time.Millisecond)
	close(gate)
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	remote := opener.latestRemote(t)
	defer remote.Close()
	readEqual(t, remote, []byte("early-tls-bytes"))
}

func TestConnectUsesConfiguredRelayOpenTimeout(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	server := startTestServer(t, &fakeOpener{healthy: true, openGate: gate}, 75*time.Millisecond)
	connection := dialTestServer(t, server)
	defer connection.Close()
	request := "CONNECT timeout.test:443 HTTP/1.1\r\nHost: timeout.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("timeout status = %d, want 502", response.StatusCode)
	}
}

func TestConnectAcceptsLargeInitialHeadersWithoutLimitingTunnelPayload(t *testing.T) {
	t.Parallel()

	opener := &fakeOpener{healthy: true}
	server := startTestServer(t, opener, 30*time.Second)
	connection := dialTestServer(t, server)
	defer connection.Close()
	request := "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\nX-Refract-Metadata: " + strings.Repeat("a", 512<<10) + "\r\n\r\n"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	remote := opener.latestRemote(t)
	defer remote.Close()
	payload := []byte(strings.Repeat("p", 256<<10))
	writeDone := make(chan error, 1)
	go func() {
		_, err := connection.Write(payload)
		writeDone <- err
	}()
	readEqual(t, remote, payload)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

func TestStopClosesEstablishedClientAndRelayConnections(t *testing.T) {
	t.Parallel()

	opener := &fakeOpener{healthy: true}
	server := startTestServer(t, opener, 30*time.Second)
	connection := dialTestServer(t, server)
	request := "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	remote := opener.latestRemote(t)
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]net.Conn{"client": connection, "relay": remote} {
		_ = candidate.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := candidate.Read(make([]byte, 1)); err == nil {
			t.Fatalf("%s connection remained open after Stop", name)
		}
		_ = candidate.Close()
	}
}

func startTestServer(t *testing.T, opener *fakeOpener, openTimeout time.Duration) *Server {
	t.Helper()
	server := NewServer(Config{Username: "user", Password: "password", Opener: opener, OpenTimeout: openTimeout})
	if err := server.Start(0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	return server
}

func dialTestServer(t *testing.T, server *Server) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", server.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func readEqual(t *testing.T, reader io.Reader, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(reader, got); err != nil {
		if errors.Is(err, io.EOF) {
			t.Fatalf("read EOF, want %q", want)
		}
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("read = %q, want %q", got, want)
	}
}
