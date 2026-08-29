//go:build windows

package securestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DPAPIStore encrypts each value with Windows DPAPI for the current user.
type DPAPIStore struct {
	directory string
}

func NewDPAPIStore(directory string) (*DPAPIStore, error) {
	if directory == "" {
		return nil, errors.New("secure store directory is required")
	}
	directory = filepath.Clean(directory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create secure store directory: %w", err)
	}
	return &DPAPIStore{directory: directory}, nil
}

func (store *DPAPIStore) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" || len(value) == 0 {
		return errors.New("secure store key and value are required")
	}
	ciphertext, err := protect(value)
	if err != nil {
		return fmt.Errorf("protect secure value: %w", err)
	}
	temporary, err := os.CreateTemp(store.directory, ".dpapi-write-")
	if err != nil {
		return fmt.Errorf("create secure value staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect secure value permissions: %w", err)
	}
	if _, err := temporary.Write(ciphertext); err != nil {
		temporary.Close()
		return fmt.Errorf("write secure value: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("flush secure value: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close secure value: %w", err)
	}
	from, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(store.path(key))
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("atomically replace secure value: %w", err)
	}
	return nil
}

func (store *DPAPIStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ciphertext, err := os.ReadFile(store.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read secure value: %w", err)
	}
	plaintext, err := unprotect(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("unprotect secure value: %w", err)
	}
	return plaintext, nil
}

func (store *DPAPIStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(store.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (store *DPAPIStore) path(key string) string {
	hash := sha256.Sum256([]byte(key))
	return filepath.Join(store.directory, hex.EncodeToString(hash[:])+".dpapi")
}

func protect(plaintext []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(plaintext)), Data: &plaintext[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func unprotect(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("empty DPAPI ciphertext")
	}
	input := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
