package securestore

import "fmt"

const userKeychainService = "com.zfnf.mobile-egress.client.app.user"

// NewUserKeychainStore opens only the signed GUI user's login Keychain. Its
// namespace and native handle never overlap the installed daemon's store.
func NewUserKeychainStore() (Store, error) {
	native, err := newPlatformUserKeychainNative()
	if err != nil {
		return nil, fmt.Errorf("Cannot open the Client's user Keychain. Use the signed app and unlock your login Keychain in Keychain Access, then reopen the app: %w", err)
	}
	return newUserKeychainStore(native), nil
}

func newUserKeychainStore(native keychainNative) *KeychainStore {
	return &KeychainStore{native: native, service: userKeychainService}
}
