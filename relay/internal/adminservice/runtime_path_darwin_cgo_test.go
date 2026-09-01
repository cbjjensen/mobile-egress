//go:build darwin && cgo

package adminservice

import (
	"reflect"
	"testing"
)

func TestNewDarwinStatePathGuardOwnsExactProductionPath(t *testing.T) {
	t.Parallel()

	prepared, err := NewDarwinStatePathGuard()
	if err != nil || prepared == nil {
		t.Fatalf("NewDarwinStatePathGuard() = %#v, %v", prepared, err)
	}
	guard, ok := prepared.(*statePathGuard)
	if !ok {
		t.Fatalf("guard type = %T", prepared)
	}
	wantAncestors := []string{"/", "/Library", "/Library/Application Support"}
	if guard.productDir != "/Library/Application Support/ZFNF Mobile Egress" ||
		guard.stateDir != DarwinRelayStateDir || !reflect.DeepEqual(guard.trustedAncestors, wantAncestors) {
		t.Fatalf("guard paths = product %q state %q ancestors %v", guard.productDir, guard.stateDir, guard.trustedAncestors)
	}
}
