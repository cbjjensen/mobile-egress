//go:build !darwin

package adminservice

import (
	"errors"
	"net"

	"mobile-egress/internal/relayadmin"
)

var errDarwinPeerUnavailable = errors.New("Darwin peer credentials are unavailable on this platform")

func DarwinPeer(net.Conn) (relayadmin.Peer, error) {
	return relayadmin.Peer{}, errDarwinPeerUnavailable
}
