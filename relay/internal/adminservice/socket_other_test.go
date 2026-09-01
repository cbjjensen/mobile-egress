//go:build !darwin

package adminservice

import (
	"context"
	"testing"
)

func TestOpenDarwinAdminSocketFailsClosedOffDarwin(t *testing.T) {
	owner, err := OpenDarwinAdminSocket(context.Background(), 80)
	if err == nil || owner != nil {
		t.Fatalf("OpenDarwinAdminSocket = (%v, %v), want nil owner and error", owner, err)
	}
}
