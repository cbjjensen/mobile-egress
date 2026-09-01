//go:build windows

package tailscale

import (
	"reflect"
	"testing"
)

var testPlatformUpArguments = []string{"up", "--unattended=true"}

const testPlatformUpFailure = "Tailscale login or unattended setup failed"

func TestWindowsUpPolicyRetainsUnattendedMode(t *testing.T) {
	t.Parallel()

	if got := upArguments(); !reflect.DeepEqual(got, []string{"up", "--unattended=true"}) {
		t.Fatalf("upArguments() = %#v, want %#v", got, []string{"up", "--unattended=true"})
	}
	if got := upFailureMessage(); got != "Tailscale login or unattended setup failed" {
		t.Fatalf("upFailureMessage() = %q", got)
	}
}
