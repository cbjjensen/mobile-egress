//go:build darwin

package tailscale

import (
	"reflect"
	"strings"
	"testing"
)

var testPlatformUpArguments = []string{"up"}

const testPlatformUpFailure = "Tailscale login or setup failed"

func TestDarwinUpPolicyOmitsUnattendedMode(t *testing.T) {
	t.Parallel()

	if got := upArguments(); !reflect.DeepEqual(got, []string{"up"}) {
		t.Fatalf("upArguments() = %#v, want %#v", got, []string{"up"})
	}
	if got := upFailureMessage(); got != "Tailscale login or setup failed" {
		t.Fatalf("upFailureMessage() = %q", got)
	}
	if strings.Contains(strings.ToLower(upFailureMessage()), "unattended") {
		t.Fatalf("Darwin up failure mentions unattended mode: %q", upFailureMessage())
	}
}
