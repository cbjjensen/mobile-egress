// Package enrollment manages short-lived enrollment capabilities and revoked
// certificate serial numbers.
package enrollment

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

const capabilityLifetime = 10 * time.Minute

var (
	ErrInvalidRole       = errors.New("invalid enrollment role")
	ErrInvalidCapability = errors.New("invalid enrollment capability")
	ErrExpiredCapability = errors.New("expired enrollment capability")
	ErrRoleMismatch      = errors.New("enrollment capability role mismatch")
	ErrEmptySerial       = errors.New("certificate serial is required")
)

// Role identifies the role permitted by an enrollment capability.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAgent  Role = "agent"
	RoleClient Role = "client"
)

// Clock makes expiration deterministic in callers and tests.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

type capability struct {
	role      Role
	expiresAt time.Time
}

// Manager retains one-time enrollment capabilities and certificate revocations.
// Persistence is deliberately left to the relay service layer.
type Manager struct {
	clock Clock

	mu           sync.Mutex
	capabilities map[string]capability
	revoked      map[string]struct{}
}

// NewManager creates a manager that uses clock for capability expiry. A nil
// clock uses the system clock.
func NewManager(clock Clock) *Manager {
	if clock == nil {
		clock = systemClock{}
	}

	return &Manager{
		clock:        clock,
		capabilities: make(map[string]capability),
		revoked:      make(map[string]struct{}),
	}
}

// Issue creates a high-entropy capability for role that can be redeemed once
// during the next ten minutes.
func (manager *Manager) Issue(role Role) (string, error) {
	if !isValidRole(role) {
		return "", ErrInvalidRole
	}

	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(bytes)

	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.capabilities[code] = capability{
		role:      role,
		expiresAt: manager.clock.Now().Add(capabilityLifetime),
	}

	return code, nil
}

// Redeem verifies code for role and consumes it. A role mismatch does not
// consume a valid capability, allowing its intended device to enroll.
func (manager *Manager) Redeem(code string, role Role) error {
	if !isValidRole(role) {
		return ErrInvalidRole
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	capability, exists := manager.capabilities[code]
	if !exists {
		return ErrInvalidCapability
	}
	if !manager.clock.Now().Before(capability.expiresAt) {
		delete(manager.capabilities, code)
		return ErrExpiredCapability
	}
	if capability.role != role {
		return ErrRoleMismatch
	}

	delete(manager.capabilities, code)
	return nil
}

// Revoke records serial as revoked. It is safe to call repeatedly for the
// same serial.
func (manager *Manager) Revoke(serial string) error {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return ErrEmptySerial
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.revoked[serial] = struct{}{}
	return nil
}

// IsRevoked reports whether serial has been revoked.
func (manager *Manager) IsRevoked(serial string) bool {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return false
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	_, revoked := manager.revoked[serial]
	return revoked
}

func isValidRole(role Role) bool {
	return role == RoleOwner || role == RoleAgent || role == RoleClient
}
