//go:build !windows && !darwin

package main

import (
	"errors"
	"mobile-egress/windows-client/internal/securestore"
)

func openServiceStore(string) (securestore.Store, error) {
	return nil, errors.New("Client service supports Windows and macOS")
}
