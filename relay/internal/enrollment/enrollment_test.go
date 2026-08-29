package enrollment

import (
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (clock *fakeClock) Now() time.Time {
	return clock.now
}

func TestManagerIssuesOneTimeEnrollmentCapabilities(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)}
	manager := NewManager(clock)

	for _, role := range []Role{RoleOwner, RoleAgent, RoleClient} {
		role := role
		t.Run(string(role), func(t *testing.T) {
			code, err := manager.Issue(role)
			if err != nil {
				t.Fatalf("Issue(%q) returned an error: %v", role, err)
			}
			if len(code) < 32 {
				t.Fatalf("Issue(%q) returned a short capability: %d characters", role, len(code))
			}

			if err := manager.Redeem(code, role); err != nil {
				t.Fatalf("Redeem() returned an error: %v", err)
			}
			if err := manager.Redeem(code, role); err == nil {
				t.Fatal("Redeem() accepted a previously redeemed capability")
			}
		})
	}
}

func TestManagerExpiresCapabilitiesAfterTenMinutes(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)}
	manager := NewManager(clock)
	code, err := manager.Issue(RoleAgent)
	if err != nil {
		t.Fatalf("Issue() returned an error: %v", err)
	}

	clock.now = clock.now.Add(10 * time.Minute)
	if err := manager.Redeem(code, RoleAgent); err == nil {
		t.Fatal("Redeem() accepted an expired capability")
	}
}

func TestManagerRejectsRoleMismatchWithoutConsumingCapability(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)}
	manager := NewManager(clock)
	code, err := manager.Issue(RoleClient)
	if err != nil {
		t.Fatalf("Issue() returned an error: %v", err)
	}

	if err := manager.Redeem(code, RoleAgent); err == nil {
		t.Fatal("Redeem() accepted a capability for the wrong role")
	}
	if err := manager.Redeem(code, RoleClient); err != nil {
		t.Fatalf("Redeem() did not preserve the capability after role mismatch: %v", err)
	}
}

func TestManagerTracksCertificateSerialRevocation(t *testing.T) {
	t.Parallel()

	manager := NewManager(&fakeClock{})
	const serial = "0A1B2C"

	if manager.IsRevoked(serial) {
		t.Fatal("IsRevoked() reported an unrevoked serial")
	}
	if err := manager.Revoke(serial); err != nil {
		t.Fatalf("Revoke() returned an error: %v", err)
	}
	if !manager.IsRevoked(serial) {
		t.Fatal("IsRevoked() did not report a revoked serial")
	}
	if err := manager.Revoke(""); err == nil {
		t.Fatal("Revoke() accepted an empty serial")
	}
}
