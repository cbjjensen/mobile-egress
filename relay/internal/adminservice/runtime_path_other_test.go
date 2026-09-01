//go:build !darwin

package adminservice

import "testing"

func TestNewDarwinStatePathGuardFailsClosedOffDarwin(t *testing.T) {
	t.Parallel()

	guard, err := NewDarwinStatePathGuard()
	if err == nil || guard != nil {
		t.Fatalf("NewDarwinStatePathGuard() = %#v, %v, want nil/error", guard, err)
	}
}
