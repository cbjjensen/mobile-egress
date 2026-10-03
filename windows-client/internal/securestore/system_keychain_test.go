package securestore

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func TestSystemKeychainUnavailableFailsClosed(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("native signed daemon acceptance runs separately")
	}
	store, err := NewSystemKeychainStore()
	if err == nil || store != nil {
		t.Fatal("unsupported system storage must fail without a fallback")
	}
}

func TestSystemKeychainUsesSeparateServiceAndStableAccount(t *testing.T) {
	native := &systemKeychainProbe{}
	store := newSystemKeychainStore(native)
	if err := store.Put(context.Background(), "private-key", []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	if native.service != systemKeychainService || native.service == keychainService || native.account != keychainAccountName("private-key") {
		t.Fatal("daemon item identity was not isolated and stable")
	}
	if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing key must map to ErrNotFound")
	}
}

type systemKeychainProbe struct{ service, account string }

func (p *systemKeychainProbe) Add(_, service, account string, _ []byte) keychainStatus {
	p.service, p.account = service, account
	return keychainStatusSuccess
}
func (*systemKeychainProbe) Update(string, string, string, []byte) keychainStatus {
	return keychainStatusItemNotFound
}
func (*systemKeychainProbe) Get(string, string, string) ([]byte, keychainStatus) {
	return nil, keychainStatusItemNotFound
}
func (*systemKeychainProbe) Delete(string, string, string) keychainStatus {
	return keychainStatusItemNotFound
}
