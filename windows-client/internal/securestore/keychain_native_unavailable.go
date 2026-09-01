//go:build !darwin || !cgo || bindings

package securestore

import "errors"

func newPlatformKeychainNative() (keychainNative, error) {
	return nil, errors.New("macOS Security.framework Keychain is unavailable on this platform")
}
