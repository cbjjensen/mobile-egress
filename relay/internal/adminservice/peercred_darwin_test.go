//go:build darwin

package adminservice

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDarwinPeerRejectsNilAndNonUnixConnections(t *testing.T) {
	for _, connection := range []net.Conn{nil, inertDarwinPeerTestConn{}} {
		peer, err := DarwinPeer(connection)
		if err == nil {
			t.Fatalf("DarwinPeer(%T) = UID %d groups %v, want error", connection, peer.UID(), peer.Groups())
		}
	}
}

func TestDarwinPeerRejectsUnavailableSyscallConnection(t *testing.T) {
	peer, err := DarwinPeer(&net.UnixConn{})
	if err == nil {
		t.Fatalf("DarwinPeer(zero UnixConn) = UID %d groups %v, want error", peer.UID(), peer.Groups())
	}
}

func TestPeerFromRawConnFailsClosedAtEveryBoundary(t *testing.T) {
	controlErr := errors.New("control failed")
	getterErr := errors.New("getsockopt failed")

	tests := []struct {
		name   string
		raw    syscall.RawConn
		getter xucredGetter
		is     error
	}{
		{
			name: "control",
			raw:  &darwinPeerRawConn{controlErr: controlErr},
			getter: func(int, int, int) (*unix.Xucred, error) {
				return &unix.Xucred{}, nil
			},
			is: controlErr,
		},
		{
			name: "getsockopt",
			raw:  &darwinPeerRawConn{},
			getter: func(int, int, int) (*unix.Xucred, error) {
				return nil, getterErr
			},
			is: getterErr,
		},
		{
			name: "nil xucred",
			raw:  &darwinPeerRawConn{},
			getter: func(int, int, int) (*unix.Xucred, error) {
				return nil, nil
			},
		},
		{
			name: "invalid xucred",
			raw:  &darwinPeerRawConn{},
			getter: func(int, int, int) (*unix.Xucred, error) {
				return &unix.Xucred{Version: 1}, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer, err := peerFromRawConn(test.raw, test.getter)
			if err == nil {
				t.Fatalf("peerFromRawConn = UID %d groups %v, want error", peer.UID(), peer.Groups())
			}
			if test.is != nil && !errors.Is(err, test.is) {
				t.Fatalf("error %v does not preserve %v", err, test.is)
			}
		})
	}
}

func TestPeerFromRawConnUsesLocalPeerCredOnceInsideControlAndCopies(t *testing.T) {
	xucred := &unix.Xucred{Version: 0, Uid: 501, Ngroups: 2}
	xucred.Groups[0] = 20
	xucred.Groups[1] = 80
	raw := &darwinPeerRawConn{fd: 37, mutateAfterControl: func() {
		xucred.Uid = 999
		xucred.Groups[0] = 998
	}}
	getterCalls := 0
	getter := func(fd, level, option int) (*unix.Xucred, error) {
		getterCalls++
		if !raw.inControl {
			t.Fatal("getsockopt called outside RawConn.Control")
		}
		if fd != 37 || level != unix.SOL_LOCAL || option != unix.LOCAL_PEERCRED {
			t.Fatalf("getsockopt(%d, %d, %d), want (37, SOL_LOCAL, LOCAL_PEERCRED)", fd, level, option)
		}
		return xucred, nil
	}

	peer, err := peerFromRawConn(raw, getter)
	if err != nil {
		t.Fatalf("peerFromRawConn: %v", err)
	}
	if getterCalls != 1 {
		t.Fatalf("getsockopt calls = %d, want 1", getterCalls)
	}
	if peer.UID() != 501 {
		t.Fatalf("UID = %d, want copied 501", peer.UID())
	}
	groups := peer.Groups()
	if len(groups) != 2 || groups[0] != 20 || groups[1] != 80 {
		t.Fatalf("groups = %v, want copied [20 80]", groups)
	}
	groups[0] = 777
	if got := peer.Groups(); got[0] != 20 {
		t.Fatalf("peer retained returned group slice: %v", got)
	}
}

type darwinPeerRawConn struct {
	fd                 uintptr
	controlErr         error
	inControl          bool
	mutateAfterControl func()
}

func (connection *darwinPeerRawConn) Control(callback func(uintptr)) error {
	if connection.controlErr != nil {
		return connection.controlErr
	}
	connection.inControl = true
	callback(connection.fd)
	connection.inControl = false
	if connection.mutateAfterControl != nil {
		connection.mutateAfterControl()
	}
	return nil
}

func (*darwinPeerRawConn) Read(func(uintptr) bool) error  { return errors.New("unexpected Read") }
func (*darwinPeerRawConn) Write(func(uintptr) bool) error { return errors.New("unexpected Write") }

type inertDarwinPeerTestConn struct{}

func (inertDarwinPeerTestConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (inertDarwinPeerTestConn) Write([]byte) (int, error)        { return 0, net.ErrClosed }
func (inertDarwinPeerTestConn) Close() error                     { return nil }
func (inertDarwinPeerTestConn) LocalAddr() net.Addr              { return nil }
func (inertDarwinPeerTestConn) RemoteAddr() net.Addr             { return nil }
func (inertDarwinPeerTestConn) SetDeadline(time.Time) error      { return nil }
func (inertDarwinPeerTestConn) SetReadDeadline(time.Time) error  { return nil }
func (inertDarwinPeerTestConn) SetWriteDeadline(time.Time) error { return nil }
