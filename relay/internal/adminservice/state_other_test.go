//go:build !darwin

package adminservice

import "testing"

func TestOtherPlatformStateGuardFailsClosed(t *testing.T) {
	t.Parallel()

	guard, err := newNativeStatePathGuard(nativeStatePathGuardConfig{
		ProductDir:       `C:\nonexistent\product`,
		StateDir:         `C:\nonexistent\product\Relay`,
		TrustedAncestors: []string{`C:\nonexistent`},
	})
	if err == nil || guard != nil {
		t.Fatalf("newNativeStatePathGuard() = %#v, %v; want nil fail-closed result", guard, err)
	}
}
