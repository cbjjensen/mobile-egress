package securestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	keychainBundleIdentifier = "com.cbjjensen.mobile-egress.controller"
	keychainService          = keychainBundleIdentifier
)

type keychainStatus int32

const (
	keychainStatusSuccess       keychainStatus = 0
	keychainStatusDuplicateItem keychainStatus = -25299
	keychainStatusItemNotFound  keychainStatus = -25300
)

type keychainNative interface {
	Add(accessGroup, service, account string, value []byte) keychainStatus
	Update(accessGroup, service, account string, value []byte) keychainStatus
	Get(accessGroup, service, account string) ([]byte, keychainStatus)
	Delete(accessGroup, service, account string) keychainStatus
}

// KeychainStore persists secrets as generic-password items in the current
// user's macOS data-protection Keychain.
type KeychainStore struct {
	native      keychainNative
	accessGroup string
}

func NewKeychainStore() (*KeychainStore, error) {
	native, applicationIdentifier, teamIdentifier, err := newPlatformKeychainNative()
	if err != nil {
		return nil, err
	}
	return newKeychainStore(native, applicationIdentifier, teamIdentifier)
}

func newKeychainStore(native keychainNative, applicationIdentifier, teamIdentifier string) (*KeychainStore, error) {
	accessGroup, err := keychainAccessGroup(applicationIdentifier, teamIdentifier)
	if err != nil {
		return nil, err
	}
	return &KeychainStore{native: native, accessGroup: accessGroup}, nil
}

func (store *KeychainStore) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" || len(value) == 0 {
		return errors.New("secure store key and value are required")
	}

	account := keychainAccountName(key)
	status := store.native.Update(store.accessGroup, keychainService, account, value)
	switch status {
	case keychainStatusSuccess:
		return nil
	case keychainStatusItemNotFound:
		status = store.native.Add(store.accessGroup, keychainService, account, value)
		if status == keychainStatusSuccess {
			return nil
		}
		if status == keychainStatusDuplicateItem {
			status = store.native.Update(store.accessGroup, keychainService, account, value)
			if status == keychainStatusSuccess {
				return nil
			}
			return keychainOperationError("replace secure value after concurrent add", status)
		}
		return keychainOperationError("add secure value", status)
	default:
		return keychainOperationError("replace secure value", status)
	}
}

func (store *KeychainStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errors.New("secure store key is required")
	}

	value, status := store.native.Get(store.accessGroup, keychainService, keychainAccountName(key))
	switch status {
	case keychainStatusSuccess:
		return append([]byte(nil), value...), nil
	case keychainStatusItemNotFound:
		return nil, ErrNotFound
	default:
		return nil, keychainOperationError("get secure value", status)
	}
}

func (store *KeychainStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("secure store key is required")
	}

	status := store.native.Delete(store.accessGroup, keychainService, keychainAccountName(key))
	switch status {
	case keychainStatusSuccess, keychainStatusItemNotFound:
		return nil
	default:
		return keychainOperationError("delete secure value", status)
	}
}

func keychainAccountName(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

func keychainAccessGroup(applicationIdentifier, teamIdentifier string) (string, error) {
	if len(teamIdentifier) != 10 {
		return "", errors.New("signed macOS team identifier must contain exactly 10 uppercase letters or digits")
	}
	for _, character := range teamIdentifier {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return "", errors.New("signed macOS team identifier must contain exactly 10 uppercase letters or digits")
		}
	}
	expected := teamIdentifier + "." + keychainBundleIdentifier
	if applicationIdentifier != expected {
		return "", fmt.Errorf("signed macOS application identifier must equal team identifier plus %s", keychainBundleIdentifier)
	}
	return applicationIdentifier, nil
}

type keychainStatusError struct {
	status keychainStatus
}

func (err keychainStatusError) Error() string {
	return fmt.Sprintf("macOS Keychain status %d", err.status)
}

func keychainOperationError(operation string, status keychainStatus) error {
	return fmt.Errorf("%s: %w", operation, keychainStatusError{status: status})
}
