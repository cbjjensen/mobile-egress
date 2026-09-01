//go:build darwin && cgo && macintegration

package adminservice

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinPeerCredentialsUseRealUnixKernelIdentityAndCopyGroups(t *testing.T) {
	requireDarwinSocketIntegrationBase(t)
	if !enterDarwinSocketIntegrationUmask(t) {
		return
	}
	root := newAuthorizedDarwinSocketTestRoot(t, false)
	socketPath := filepath.Join(root.spelled, "peer.sock")
	listener := root.listenUnix(t, socketPath)
	listener.SetUnlinkOnClose(false)
	defer func() {
		_ = listener.Close()
		root.removeIfExists(t, socketPath)
	}()

	accepted := make(chan *net.UnixConn, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			acceptErrors <- acceptErr
			return
		}
		accepted <- connection
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var server *net.UnixConn
	select {
	case server = <-accepted:
	case err := <-acceptErrors:
		t.Fatal(err)
	}
	defer server.Close()

	peer, err := DarwinPeer(server)
	if err != nil {
		t.Fatal(err)
	}
	if peer.UID() != uint32(os.Geteuid()) {
		t.Fatalf("peer UID = %d, want current effective UID %d", peer.UID(), os.Geteuid())
	}
	groups := peer.Groups()
	if len(groups) > 16 {
		t.Fatalf("peer groups length = %d, want at most 16", len(groups))
	}
	if len(groups) > 0 {
		original := groups[0]
		groups[0]++
		if peer.Groups()[0] != original {
			t.Fatal("peer retained a caller-owned group slice")
		}
	}
}

func TestDarwinPeerCredentialsRejectNonUnixConnections(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if peer, err := DarwinPeer(left); err == nil {
		t.Fatalf("DarwinPeer(net.Pipe) = UID %d groups %v, want error", peer.UID(), peer.Groups())
	}
}
