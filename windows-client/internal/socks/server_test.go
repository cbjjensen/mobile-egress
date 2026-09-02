package socks

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeOpener struct {
	mu          sync.Mutex
	healthy     bool
	openGate    <-chan struct{}
	opened      int
	openCalls   int
	remoteConns []net.Conn
}

func (opener *fakeOpener) Healthy() bool { return opener.healthy }

func (opener *fakeOpener) OpenStream(ctx context.Context, host string, port uint16) (io.ReadWriteCloser, error) {
	opener.mu.Lock()
	opener.openCalls++
	opener.mu.Unlock()
	if opener.openGate != nil {
		select {
		case <-opener.openGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if !opener.healthy {
		return nil, ErrRelayUnavailable
	}
	client, remote := net.Pipe()
	opener.mu.Lock()
	opener.opened++
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
	t.Fatal("stream was not opened")
	return nil
}

func TestServerBindsOnlyApplicationProxyLoopbackAddress(t *testing.T) {
	t.Parallel()

	server := NewServer(Config{Username: "user", Password: "password", Opener: &fakeOpener{healthy: true}})
	if err := server.Start(0); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	address := server.Addr()
	if address == nil || address.IP.String() != "127.0.0.2" {
		t.Fatalf("listener address = %v, want 127.0.0.2", address)
	}
	if _, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(address.Port)), 100*time.Millisecond); err == nil {
		t.Fatal("SOCKS listener was reachable through 127.0.0.1")
	}
}

func TestServerCanBindApplicationProxyAddressWhenSamePortIsOccupiedOn127001(t *testing.T) {
	t.Parallel()

	occupied, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })

	server := NewServer(Config{Username: "user", Password: "password", Opener: &fakeOpener{healthy: true}})
	if err := server.Start(uint16(occupied.Addr().(*net.TCPAddr).Port)); err != nil {
		t.Fatalf("Start() with 127.0.0.1 occupied = %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	if address := server.Addr(); address == nil || address.IP.String() != "127.0.0.2" || address.Port != occupied.Addr().(*net.TCPAddr).Port {
		t.Fatalf("listener address = %v, want 127.0.0.2:%d", address, occupied.Addr().(*net.TCPAddr).Port)
	}
}

func TestServerRequiresRFC1929UsernamePasswordAuthentication(t *testing.T) {
	t.Parallel()

	server := startTestServer(t, &fakeOpener{healthy: true})
	defer server.Stop()

	connection := dialServer(t, server)
	defer connection.Close()
	writeAll(t, connection, []byte{5, 1, 0})
	readEqual(t, connection, []byte{5, 0xff})

	connection = dialServer(t, server)
	defer connection.Close()
	writeAll(t, connection, []byte{5, 1, 2})
	readEqual(t, connection, []byte{5, 2})
	writeAll(t, connection, authRequest("wrong", "password"))
	readEqual(t, connection, []byte{1, 1})

	connection = dialServer(t, server)
	defer connection.Close()
	writeAll(t, connection, []byte{5, 1, 2})
	readEqual(t, connection, []byte{5, 2})
	writeAll(t, connection, authRequest("user", "password"))
	readEqual(t, connection, []byte{1, 0})
}

func TestConnectWaitsForRelayOpenedThenRoutesBytesBothDirections(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	opener := &fakeOpener{healthy: true, openGate: gate}
	server := startTestServer(t, opener)
	defer server.Stop()
	connection := authenticatedConnection(t, server)
	defer connection.Close()

	writeAll(t, connection, connectDomainRequest(1, "example.test", 443))
	_ = connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	one := make([]byte, 1)
	if _, err := connection.Read(one); err == nil {
		t.Fatal("SOCKS success was sent before relay stream opened")
	}
	_ = connection.SetReadDeadline(time.Time{})
	close(gate)
	readEqual(t, connection, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})

	remote := opener.latestRemote(t)
	defer remote.Close()
	writeAll(t, connection, []byte("upstream"))
	readEqual(t, remote, []byte("upstream"))
	writeAll(t, remote, []byte("downstream"))
	readEqual(t, connection, []byte("downstream"))
}

func TestServerRejectsBindAndUDPWithoutOpeningRelayStream(t *testing.T) {
	t.Parallel()

	for _, command := range []byte{2, 3} {
		command := command
		t.Run(strconv.Itoa(int(command)), func(t *testing.T) {
			opener := &fakeOpener{healthy: true}
			server := startTestServer(t, opener)
			defer server.Stop()
			connection := authenticatedConnection(t, server)
			defer connection.Close()

			writeAll(t, connection, connectDomainRequest(command, "blocked.example", 53))
			readEqual(t, connection, []byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
			opener.mu.Lock()
			calls := opener.openCalls
			opener.mu.Unlock()
			if calls != 0 {
				t.Fatalf("OpenStream calls = %d, want 0", calls)
			}
		})
	}
}

func TestServerRejectsConnectWhenRelayAgentIsUnavailable(t *testing.T) {
	t.Parallel()

	opener := &fakeOpener{healthy: false}
	server := startTestServer(t, opener)
	defer server.Stop()
	connection := authenticatedConnection(t, server)
	defer connection.Close()

	writeAll(t, connection, connectDomainRequest(1, "not-recorded.example", 443))
	readEqual(t, connection, []byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
	opener.mu.Lock()
	calls := opener.openCalls
	opener.mu.Unlock()
	if calls != 0 {
		t.Fatalf("OpenStream calls = %d, want 0 for unhealthy agent", calls)
	}
}

func TestServerUsesRelaySessionLimitForEarlyAdmission(t *testing.T) {
	server := &Server{listener: &net.TCPListener{}}
	for index := 0; index < 256; index++ {
		if !server.reserveStream() {
			t.Fatalf("reservation %d rejected, want 256 admitted", index+1)
		}
	}
	if server.reserveStream() {
		t.Fatal("reservation 257 admitted, want rejection")
	}
	for index := 0; index < 256; index++ {
		server.releaseStream()
	}
	if active := server.Status().ActiveStreams; active != 0 {
		t.Fatalf("active streams after release = %d, want 0", active)
	}
}

func TestServerStopClosesEveryConnection(t *testing.T) {
	t.Parallel()

	opener := &fakeOpener{healthy: true}
	server := startTestServer(t, opener)
	connections := make([]net.Conn, 0, 2)
	for index := 0; index < 2; index++ {
		connection := authenticatedConnection(t, server)
		writeAll(t, connection, connectDomainRequest(1, "stream.example", 443))
		readEqual(t, connection, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		connections = append(connections, connection)
	}

	if status := server.Status(); status.ActiveStreams != 2 {
		t.Fatalf("active streams = %d, want 2", status.ActiveStreams)
	}
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	if status := server.Status(); status.Running || status.ActiveStreams != 0 {
		t.Fatalf("status after Stop() = %#v", status)
	}
	for _, connection := range connections {
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := connection.Read(make([]byte, 1)); err == nil {
			t.Fatal("client socket remained open after Stop()")
		}
		_ = connection.Close()
	}
	for _, remote := range opener.remoteConns {
		_ = remote.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := remote.Read(make([]byte, 1)); err == nil {
			t.Fatal("relay stream remained open after Stop()")
		}
		_ = remote.Close()
	}
}

func TestAcceptedConnectionAfterStopIsClosedInsteadOfRegistered(t *testing.T) {
	server := startTestServer(t, &fakeOpener{healthy: true})
	server.mu.Lock()
	listener := server.listener
	server.mu.Unlock()
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	accepted, peer := net.Pipe()
	defer peer.Close()
	tracked := &trackedConnection{client: accepted}
	if server.registerAccepted(listener, tracked) {
		t.Fatal("connection accepted by a stopped listener was registered")
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection accepted while stopping was left open")
	}
}

func TestConcurrentStopAndAcceptNeverLeavesAuthenticationSocket(t *testing.T) {
	for iteration := 0; iteration < 40; iteration++ {
		server := startTestServer(t, &fakeOpener{healthy: true})
		address := server.Addr().String()
		var clientsMu sync.Mutex
		clients := make([]net.Conn, 0, 16)
		var dialers sync.WaitGroup
		for index := 0; index < 16; index++ {
			dialers.Add(1)
			go func() {
				defer dialers.Done()
				connection, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
				if err == nil {
					clientsMu.Lock()
					clients = append(clients, connection)
					clientsMu.Unlock()
				}
			}()
		}
		stopped := make(chan error, 1)
		go func() { stopped <- server.Stop() }()
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Stop hung with concurrent accepts on iteration %d", iteration)
		}
		dialers.Wait()
		clientsMu.Lock()
		for _, connection := range clients {
			_ = connection.Close()
		}
		clientsMu.Unlock()
	}
}

func TestAbandonedPreOpenConnectionsReleaseReservedSlots(t *testing.T) {
	gate := make(chan struct{})
	opener := &fakeOpener{healthy: true, openGate: gate}
	server := NewServer(Config{Username: "user", Password: "password", Opener: opener, OpenTimeout: 30 * time.Second})
	if err := server.Start(0); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	const reservations = 8
	clients := make([]net.Conn, 0, reservations)
	for index := 0; index < reservations; index++ {
		connection := authenticatedConnection(t, server)
		writeAll(t, connection, connectDomainRequest(1, "abandoned.example", 443))
		clients = append(clients, connection)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		opener.mu.Lock()
		calls := opener.openCalls
		opener.mu.Unlock()
		if calls == reservations {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for _, connection := range clients {
		_ = connection.Close()
	}
	deadline = time.Now().Add(2 * time.Second)
	for server.Status().ActiveStreams != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if active := server.Status().ActiveStreams; active != 0 {
		t.Fatalf("abandoned pre-open connections retained %d stream slots", active)
	}

	close(gate)
	fifth := authenticatedConnection(t, server)
	defer fifth.Close()
	writeAll(t, fifth, connectDomainRequest(1, "replacement.example", 443))
	readEqual(t, fifth, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
}

func TestPreOpenConnectionHasLocalOpeningDeadline(t *testing.T) {
	gate := make(chan struct{})
	server := NewServer(Config{
		Username: "user", Password: "password", Opener: &fakeOpener{healthy: true, openGate: gate},
		OpenTimeout: 75 * time.Millisecond,
	})
	if err := server.Start(0); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	connection := authenticatedConnection(t, server)
	defer connection.Close()
	writeAll(t, connection, connectDomainRequest(1, "timeout.example", 443))
	readEqual(t, connection, []byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
	if active := server.Status().ActiveStreams; active != 0 {
		t.Fatalf("timed-out open retained %d stream slots", active)
	}
}

func startTestServer(t *testing.T, opener *fakeOpener) *Server {
	t.Helper()
	server := NewServer(Config{Username: "user", Password: "password", Opener: opener})
	if err := server.Start(0); err != nil {
		t.Fatal(err)
	}
	return server
}

func dialServer(t *testing.T, server *Server) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", server.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func authenticatedConnection(t *testing.T, server *Server) net.Conn {
	t.Helper()
	connection := dialServer(t, server)
	writeAll(t, connection, []byte{5, 1, 2})
	readEqual(t, connection, []byte{5, 2})
	writeAll(t, connection, authRequest("user", "password"))
	readEqual(t, connection, []byte{1, 0})
	return connection
}

func authRequest(username, password string) []byte {
	request := []byte{1, byte(len(username))}
	request = append(request, username...)
	request = append(request, byte(len(password)))
	request = append(request, password...)
	return request
}

func connectDomainRequest(command byte, host string, port uint16) []byte {
	request := []byte{5, command, 0, 3, byte(len(host))}
	request = append(request, host...)
	request = append(request, byte(port>>8), byte(port))
	return request
}

func writeAll(t *testing.T, writer io.Writer, value []byte) {
	t.Helper()
	if _, err := writer.Write(value); err != nil {
		t.Fatal(err)
	}
}

func readEqual(t *testing.T, reader io.Reader, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(reader, got); err != nil {
		if errors.Is(err, io.EOF) {
			t.Fatalf("read EOF, want %v", want)
		}
		t.Fatal(err)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("read = %v, want %v", got, want)
		}
	}
}
