package securestore

const systemKeychainService = "com.zfnf.mobile-egress.client"

// NewSystemKeychainStore opens the machine System.keychain for the signed root
// Client daemon. It never uses a login/data-protection Keychain or disk fallback.
// Each item has an ACL bound to the daemon's designated signing requirement;
// same-signed upgrades retain access, while the GUI does not receive access.
func NewSystemKeychainStore() (Store, error) {
	native, err := newPlatformSystemKeychainNative()
	if err != nil {
		return nil, err
	}
	return newSystemKeychainStore(native), nil
}

func newSystemKeychainStore(native keychainNative) *KeychainStore {
	return &KeychainStore{native: native, service: systemKeychainService}
}
