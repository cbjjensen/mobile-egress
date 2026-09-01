package relayservice

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"mobile-egress/internal/relayadmin"
)

func TestDarwinRelayAdminClientUsesFixedSocketAndLimits(t *testing.T) {
	dialFailure := errors.New("test dial stopped")
	dialer := &recordingRelayAdminDialer{err: dialFailure}
	client := newRelayAdminClient(dialer)

	if client.OperationLimit != relayadmin.OperationTimeout {
		t.Fatalf("OperationLimit = %s, want %s", client.OperationLimit, relayadmin.OperationTimeout)
	}
	if client.IOLimit != relayadmin.OperationTimeout {
		t.Fatalf("IOLimit = %s, want %s", client.IOLimit, relayadmin.OperationTimeout)
	}
	if client.Random != nil {
		t.Fatal("production relay-admin client overrides the secure default random source")
	}
	if _, err := client.Dial(context.Background()); !errors.Is(err, dialFailure) {
		t.Fatalf("Dial error = %v, want test sentinel", err)
	}
	if dialer.network != "unix" || dialer.address != relayadmin.DarwinAdminSocketPath {
		t.Fatalf("DialContext = (%q, %q), want (%q, %q)", dialer.network, dialer.address, "unix", relayadmin.DarwinAdminSocketPath)
	}
}

func TestRelayAdminVerticalExchange(t *testing.T) {
	listener := newRelayAdminUnixListener(t)
	server := &relayadmin.Server{
		Authorize:      func(context.Context, relayadmin.Peer, relayadmin.Operation) bool { return true },
		Handler:        adminClientTestHandler{},
		Replay:         relayadmin.NewMemoryReplayStore(relayadmin.MemoryReplayConfig{}),
		OperationLimit: 2 * time.Second,
		IOLimit:        2 * time.Second,
	}
	serverDone := serveAdminClientTestConnections(listener, server, 4)

	dialer := &redirectingRelayAdminDialer{address: listener.Addr().String()}
	client := newRelayAdminClient(dialer)
	client.Random = bytes.NewReader([]byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4,
	})

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	wantStatus := relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0", Initialized: true, RelayRunning: true}
	if !reflect.DeepEqual(status, wantStatus) {
		t.Fatalf("Status = %#v, want %#v", status, wantStatus)
	}

	setup, err := client.Setup(context.Background(), relayadmin.SetupRequest{
		PublicName:  "relay.example.ts.net",
		PublicURL:   "https://relay.example.ts.net:8443",
		OwnerCSRPEM: "owner-csr",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	wantSetup := relayadmin.OwnerBootstrapResult{CertificatePEM: "owner-certificate", CACertificatePEM: "ca-certificate", Serial: "A1B2", Role: "owner"}
	if !reflect.DeepEqual(setup, wantSetup) {
		t.Fatalf("Setup = %#v, want %#v", setup, wantSetup)
	}

	rotate, err := client.Rotate(context.Background(), relayadmin.RotateRequest{
		PublicName: "next.example.ts.net",
		PublicURL:  "https://next.example.ts.net:8443",
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	wantRotate := relayadmin.EndpointRotationResult{PublicURL: "https://next.example.ts.net:8443", Serial: "C3D4"}
	if !reflect.DeepEqual(rotate, wantRotate) {
		t.Fatalf("Rotate = %#v, want %#v", rotate, wantRotate)
	}

	repair, err := client.Repair(context.Background())
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	wantRepair := relayadmin.RepairResult{Ready: true, Restarting: true}
	if repair != wantRepair {
		t.Fatalf("Repair = %#v, want %#v", repair, wantRepair)
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("relay-admin server: %v", err)
	}
	if dialer.calls != 4 {
		t.Fatalf("dial calls = %d, want 4", dialer.calls)
	}
	if dialer.lastNetwork != "unix" || dialer.lastAddress != relayadmin.DarwinAdminSocketPath {
		t.Fatalf("production dial target = (%q, %q), want (%q, %q)", dialer.lastNetwork, dialer.lastAddress, "unix", relayadmin.DarwinAdminSocketPath)
	}
}

type recordingRelayAdminDialer struct {
	network string
	address string
	err     error
}

func (dialer *recordingRelayAdminDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	dialer.network = network
	dialer.address = address
	return nil, dialer.err
}

type redirectingRelayAdminDialer struct {
	address     string
	calls       int
	lastNetwork string
	lastAddress string
}

func (dialer *redirectingRelayAdminDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer.calls++
	dialer.lastNetwork = network
	dialer.lastAddress = address
	var networkDialer net.Dialer
	return networkDialer.DialContext(ctx, "unix", dialer.address)
}

func newRelayAdminUnixListener(t *testing.T) net.Listener {
	t.Helper()
	file, err := os.CreateTemp("", "mobile-egress-admin-*.sock")
	if err != nil {
		t.Fatalf("create temporary Unix socket path: %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("close temporary Unix socket path: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("prepare temporary Unix socket path: %v", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on temporary Unix socket: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(path)
	})
	return listener
}

func serveAdminClientTestConnections(listener net.Listener, server *relayadmin.Server, count int) <-chan error {
	done := make(chan error, 1)
	go func() {
		for index := 0; index < count; index++ {
			connection, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			server.ServeConn(context.Background(), connection, relayadmin.NewPeer(501, []uint32{80}))
		}
		done <- nil
	}()
	return done
}

type adminClientTestHandler struct{}

func (adminClientTestHandler) Status(context.Context, relayadmin.Peer) (relayadmin.StatusResult, error) {
	return relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0", Initialized: true, RelayRunning: true}, nil
}

func (adminClientTestHandler) Setup(_ context.Context, _ relayadmin.Peer, _ relayadmin.Mutation, request relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error) {
	if request != (relayadmin.SetupRequest{PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", OwnerCSRPEM: "owner-csr"}) {
		return relayadmin.OwnerBootstrapResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorInvalidRequest}
	}
	return relayadmin.OwnerBootstrapResult{CertificatePEM: "owner-certificate", CACertificatePEM: "ca-certificate", Serial: "A1B2", Role: "owner"}, nil
}

func (adminClientTestHandler) Rotate(_ context.Context, _ relayadmin.Peer, _ relayadmin.Mutation, request relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error) {
	if request != (relayadmin.RotateRequest{PublicName: "next.example.ts.net", PublicURL: "https://next.example.ts.net:8443"}) {
		return relayadmin.EndpointRotationResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorInvalidRequest}
	}
	return relayadmin.EndpointRotationResult{PublicURL: request.PublicURL, Serial: "C3D4"}, nil
}

func (adminClientTestHandler) Repair(context.Context, relayadmin.Peer, relayadmin.Mutation) (relayadmin.RepairResult, error) {
	return relayadmin.RepairResult{Ready: true, Restarting: true}, nil
}
