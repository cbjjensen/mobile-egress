//go:build !darwin

package relayservice

import "testing"

func TestNewDarwinFailsClosedOutsideNativeMacBuild(t *testing.T) {
	controller, client, err := NewDarwin("1.1.0")
	if controller != nil || client != nil || err == nil {
		t.Fatalf("NewDarwin() = (%v, %v, %v), want nil/nil/fixed error", controller, client, err)
	}
}
