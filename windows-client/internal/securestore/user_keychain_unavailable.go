//go:build !darwin || !cgo || bindings

package securestore

import "errors"

func newPlatformUserKeychainNative() (keychainNative, error) {
	return nil, errors.New("signed macOS user Keychain is unavailable on this platform")
}
