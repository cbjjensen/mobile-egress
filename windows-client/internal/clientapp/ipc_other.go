//go:build !windows && !darwin

package clientapp

import (
	"context"
	"errors"
	"net"
)

func ListenLocal(string) (net.Listener, error) {
	return nil, errors.New("Client service supports Windows and macOS")
}
func dialLocal(context.Context) (net.Conn, error) {
	return nil, errors.New("Client service supports Windows and macOS")
}
