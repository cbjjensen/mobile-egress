//go:build !windows && !darwin

package tailscale

import (
	"reflect"
	"testing"
)

var testPlatformUpArguments = []string{"up"}

const testPlatformUpFailure = "Tailscale login or setup failed"

func TestOtherPlatformUpPolicyUsesFlaglessUp(t *testing.T) {
	t.Parallel()

	if got := upArguments(); !reflect.DeepEqual(got, []string{"up"}) {
		t.Fatalf("upArguments() = %#v, want %#v", got, []string{"up"})
	}
	if got := upFailureMessage(); got != "Tailscale login or setup failed" {
		t.Fatalf("upFailureMessage() = %q", got)
	}
}
