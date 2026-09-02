// Package proxyendpoint defines the desktop-local application proxy endpoint.
package proxyendpoint

import (
	"net"
	"strconv"
)

const (
	Host            = "127.0.0.2"
	SOCKSPort       = uint16(1080)
	HTTPConnectPort = uint16(1081)
)

const (
	SOCKSAddress       = Host + ":1080"
	HTTPConnectAddress = Host + ":1081"
)

func Address(port uint16) string {
	return net.JoinHostPort(Host, strconv.Itoa(int(port)))
}

func IP() net.IP {
	return net.ParseIP(Host)
}
