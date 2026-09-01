package securestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const keychainService = "com.cbjjensen.mobile-egress.controller"

type keychainStatus int32

const (
	keychainStatusSuccess       keychainStatus = 0
	keychainStatusDuplicateItem keychainStatus = -25299
	keychainStatusItemNotFound  keychainStatus = -25300
)

type keychainNative interface {
	Add(service, account string, value []byte) keychainStatus
	Update(service, account string, value []byte) keychainStatus
	Get(service, account string) ([]byte, keychainStatus)
	Delete(service, account string) keychainStatus
}

// KeychainStore persists secrets as generic-password items in the current
// user's macOS data-protection Keychain.
type KeychainStore struct {
	native keychainNative
}

func NewKeychainStore() (*KeychainStore, error) {
	native, err := newPlatformKeychainNative()
	if err != nil {
		return nil, err
	}
	return newKeychainStore(native), nil
}

func newKeychainStore(native keychainNative) *KeychainStore {
	return &KeychainStore{native: native}
}

func (store *KeychainStore) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" || len(value) == 0 {
		return errors.New("secure store key and value are required")
	}

	account := keychainAccountName(key)
	status := store.native.Update(keychainService, account, value)
	switch status {
	case keychainStatusSuccess:
		return nil
	case keychainStatusItemNotFound:
		status = store.native.Add(keychainService, account, value)
		if status == keychainStatusSuccess {
			return nil
		}
		if status == keychainStatusDuplicateItem {
			status = store.native.Update(keychainService, account, value)
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

	value, status := store.native.Get(keychainService, keychainAccountName(key))
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

	status := store.native.Delete(keychainService, keychainAccountName(key))
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

type keychainStatusError struct {
	status keychainStatus
}

func (err keychainStatusError) Error() string {
	return fmt.Sprintf("macOS Keychain status %d", err.status)
}

func keychainOperationError(operation string, status keychainStatus) error {
	return fmt.Errorf("%s: %w", operation, keychainStatusError{status: status})
}
