//go:build !darwin || !cgo || bindings

package securestore

import "errors"

func newPlatformSystemKeychainNative() (keychainNative, error) {
	return nil, errors.New("signed root macOS System Keychain is unavailable on this platform")
}
