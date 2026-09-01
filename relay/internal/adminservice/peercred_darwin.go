//go:build darwin

package adminservice

import (
	"errors"
	"fmt"
	"net"
	"syscall"

	"mobile-egress/internal/relayadmin"

	"golang.org/x/sys/unix"
)

type xucredGetter func(fd, level, option int) (*unix.Xucred, error)

func DarwinPeer(connection net.Conn) (relayadmin.Peer, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok || unixConnection == nil {
		return relayadmin.Peer{}, errInvalidAdminPeerCredentials
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return relayadmin.Peer{}, fmt.Errorf("access relay admin connection credentials: %w", err)
	}
	return peerFromRawConn(raw, unix.GetsockoptXucred)
}

func peerFromRawConn(raw syscall.RawConn, getter xucredGetter) (relayadmin.Peer, error) {
	if raw == nil || getter == nil {
		return relayadmin.Peer{}, errInvalidAdminPeerCredentials
	}
	var peer relayadmin.Peer
	var inspectErr error
	controlCalls := 0
	controlErr := raw.Control(func(fd uintptr) {
		controlCalls++
		credentials, err := getter(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			inspectErr = fmt.Errorf("read relay admin peer credentials: %w", err)
			return
		}
		if credentials == nil {
			inspectErr = errInvalidAdminPeerCredentials
			return
		}
		snapshot := xucredSnapshot{
			Version: credentials.Version,
			UID:     credentials.Uid,
			NGroups: credentials.Ngroups,
			Groups:  credentials.Groups,
		}
		peer, inspectErr = peerFromXucred(snapshot)
	})
	if controlCalls != 1 {
		inspectErr = errors.Join(inspectErr, errInvalidAdminPeerCredentials)
	}
	if controlErr != nil || inspectErr != nil {
		return relayadmin.Peer{}, errors.Join(
			inspectErr,
			func() error {
				if controlErr == nil {
					return nil
				}
				return fmt.Errorf("control relay admin connection: %w", controlErr)
			}(),
		)
	}
	return peer, nil
}
