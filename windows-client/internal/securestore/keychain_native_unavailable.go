//go:build !darwin || !cgo || bindings

package securestore

import "errors"

func newPlatformKeychainNative() (keychainNative, string, string, error) {
	return nil, "", "", errors.New("macOS Security.framework Keychain is unavailable on this platform")
}
