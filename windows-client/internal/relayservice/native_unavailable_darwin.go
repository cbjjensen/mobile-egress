//go:build darwin && (!cgo || bindings)

package relayservice

import "errors"

func NewDarwin(string) (Controller, RelayAdminClient, error) {
	return nil, nil, errors.New("native macOS relay service management is unavailable in this build")
}
