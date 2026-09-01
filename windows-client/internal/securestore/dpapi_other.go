//go:build !windows

package securestore

import (
	"context"
	"errors"
)

var errDPAPIUnavailable = errors.New("Windows DPAPI is unavailable on this platform")

type DPAPIStore struct{}

func NewDPAPIStore(string) (*DPAPIStore, error) {
	return nil, errDPAPIUnavailable
}

func (*DPAPIStore) Put(context.Context, string, []byte) error {
	return errDPAPIUnavailable
}

func (*DPAPIStore) Get(context.Context, string) ([]byte, error) {
	return nil, errDPAPIUnavailable
}

func (*DPAPIStore) Delete(context.Context, string) error {
	return errDPAPIUnavailable
}
