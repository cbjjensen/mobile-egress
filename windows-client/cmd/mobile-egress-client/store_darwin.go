//go:build darwin

package main

import (
	"errors"
	"mobile-egress/windows-client/internal/securestore"
	"os"
)

func openServiceStore(string) (securestore.Store, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("Client service state must be opened by the installed system daemon")
	}
	return securestore.NewSystemKeychainStore()
}
