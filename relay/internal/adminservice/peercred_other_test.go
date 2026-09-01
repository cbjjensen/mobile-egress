//go:build !darwin

package adminservice

import (
	"net"
	"testing"
	"time"
)

func TestDarwinPeerFailsClosedOffDarwin(t *testing.T) {
	for _, connection := range []net.Conn{nil, inertPeerTestConn{}} {
		peer, err := DarwinPeer(connection)
		if err == nil {
			t.Fatalf("DarwinPeer(%T) = UID %d groups %v, want error", connection, peer.UID(), peer.Groups())
		}
	}
}

type inertPeerTestConn struct{}

func (inertPeerTestConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (inertPeerTestConn) Write([]byte) (int, error)        { return 0, net.ErrClosed }
func (inertPeerTestConn) Close() error                     { return nil }
func (inertPeerTestConn) LocalAddr() net.Addr              { return nil }
func (inertPeerTestConn) RemoteAddr() net.Addr             { return nil }
func (inertPeerTestConn) SetDeadline(time.Time) error      { return nil }
func (inertPeerTestConn) SetReadDeadline(time.Time) error  { return nil }
func (inertPeerTestConn) SetWriteDeadline(time.Time) error { return nil }
