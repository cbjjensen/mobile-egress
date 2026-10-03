//go:build darwin

package clientapp

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const localSocket = "/var/run/mobile-egress-client/control.sock"

type ownerListener struct {
	net.Listener
	uid uint32
}

func (listener *ownerListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(connection)
		if err == nil && (uid == 0 || uid == listener.uid) {
			return connection, nil
		}
		connection.Close()
	}
}
func peerUID(connection net.Conn) (uint32, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return 0, errors.New("invalid local socket")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *unix.Xucred
	var credentialErr error
	if err = raw.Control(func(fd uintptr) {
		credentials, credentialErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credentialErr != nil {
		return 0, credentialErr
	}
	return credentials.Uid, nil
}
func protectedRootPath(path string, directory bool) error {
	return protectedPathOwnedBy(path, directory, 0)
}

func protectedPathOwnedBy(path string, directory bool, uid uint32) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uid || info.Mode()&0o022 != 0 || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory {
		return errors.New("Client control path is not protected")
	}
	return nil
}

// ListenLocal enforces the root service account before preparing its runtime
// directory. Validate ownership before adjusting access permissions.
func prepareSocketDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if err := protectedPathOwnedBy(directory, true, uint32(os.Geteuid())); err != nil {
		return err
	}
	// launchd starts this daemon with umask 077, which masks MkdirAll(0755)
	// to 0700. Restore traversal explicitly after verifying service ownership;
	// socket peer credentials continue to enforce access to actual requests.
	return os.Chmod(directory, 0o755)
}
func ListenLocal(stateDir string) (net.Listener, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("Client daemon must run as root")
	}
	ownerPath := filepath.Join(stateDir, "owner.uid")
	if err := protectedRootPath(stateDir, true); err != nil {
		return nil, err
	}
	if err := protectedRootPath(ownerPath, false); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(ownerPath)
	if err != nil {
		return nil, err
	}
	uid, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
	if err != nil || uid == 0 {
		return nil, errors.New("Client owner is invalid; repair the installation")
	}
	directory := filepath.Dir(localSocket)
	if err := prepareSocketDirectory(directory); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(localSocket); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("Client socket path is occupied")
		}
		connection, dialErr := net.DialTimeout("unix", localSocket, time.Second)
		if dialErr == nil {
			connection.Close()
			return nil, errors.New("Client service is already running")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, errors.New("Client socket is unavailable")
		}
		if err := os.Remove(localSocket); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", localSocket)
	if err != nil {
		return nil, err
	}
	// Peer credentials, not the filesystem mode, enforce installer-owner access.
	if err := os.Chmod(localSocket, 0o666); err != nil {
		listener.Close()
		return nil, err
	}
	return &ownerListener{Listener: listener, uid: uint32(uid)}, nil
}
func dialLocal(ctx context.Context) (net.Conn, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", localSocket)
	if err != nil {
		return nil, err
	}
	uid, err := peerUID(connection)
	if err != nil || uid != 0 {
		connection.Close()
		return nil, errors.New("Client daemon identity is invalid")
	}
	return connection, nil
}
