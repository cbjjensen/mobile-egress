package localbridge

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/windows-client/internal/securestore"
)

const pendingSetupKey = "pending-local-bridge-setup-v1"

// One secure record is enough to replay the existing relay transaction. Never
// regenerate its CSR or request ID after the relay may have committed it.
type pendingSetup struct {
	RequestID     string       `json:"requestId"`
	Request       SetupRequest `json:"request"`
	PrivateKeyDER []byte       `json:"privateKeyDer"`
}

func NewResumableManager(bridge TailscaleBridge, helper ElevatedHelper, owners OwnerSink, store securestore.Store) *Manager {
	manager := NewManager(bridge, helper, owners)
	manager.pendingStore = store
	return manager
}

func (manager *Manager) HasPendingSetup(ctx context.Context) (bool, error) {
	pending, err := manager.loadPendingSetup(ctx)
	if pending != nil {
		clear(pending.PrivateKeyDER)
	}
	return pending != nil, err
}

func (manager *Manager) loadPendingSetup(ctx context.Context) (*pendingSetup, error) {
	if manager.pendingStore == nil {
		return nil, nil
	}
	raw, err := manager.pendingStore.Get(ctx, pendingSetupKey)
	if errors.Is(err, securestore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Read pending bridge setup from secure storage: %w", err)
	}
	defer clear(raw)
	var pending pendingSetup
	if len(raw) > 64<<10 || json.Unmarshal(raw, &pending) != nil || relayadmin.ValidateRequestID(pending.RequestID) != nil || pending.Request.PublicURL == "" || pending.Request.PublicName == "" {
		clear(pending.PrivateKeyDER)
		return nil, errors.New("Saved bridge setup is invalid; it has been preserved for recovery")
	}
	if _, err := pending.privateKey(); err != nil {
		clear(pending.PrivateKeyDER)
		return nil, err
	}
	return &pending, nil
}

func (pending *pendingSetup) privateKey() (*ecdsa.PrivateKey, error) {
	invalid := errors.New("Saved bridge setup key does not match its request; setup has been preserved for recovery")
	parsed, err := x509.ParsePKCS8PrivateKey(pending.PrivateKeyDER)
	if err != nil {
		return nil, invalid
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, invalid
	}
	block, _ := pem.Decode([]byte(pending.Request.OwnerCSRPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, invalid
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return nil, invalid
	}
	expected, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	actual, _ := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if !bytes.Equal(expected, actual) {
		return nil, invalid
	}
	return key, nil
}

func (manager *Manager) savePendingSetup(ctx context.Context, pending *pendingSetup) error {
	if manager.pendingStore == nil {
		return nil
	}
	requestID, err := relayadmin.GenerateRequestID(nil)
	if err != nil {
		return errors.New("Generate bridge setup request ID")
	}
	pending.RequestID = requestID
	raw, err := json.Marshal(pending)
	if err != nil {
		return errors.New("Encode pending bridge setup")
	}
	defer clear(raw)
	if err := manager.pendingStore.Put(ctx, pendingSetupKey, raw); err != nil {
		return fmt.Errorf("Save pending bridge setup before contacting relay: %w", err)
	}
	return nil
}
