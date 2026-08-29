//go:build !windows

package securestore

import "errors"

type DPAPIStore struct{}

func NewDPAPIStore(string) (*DPAPIStore, error) {
	return nil, errors.New("Windows DPAPI is unavailable on this platform")
}
