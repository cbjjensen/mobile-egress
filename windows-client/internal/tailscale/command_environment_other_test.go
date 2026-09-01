//go:build !darwin

package tailscale

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestNonDarwinCommandEnvironmentPolicyIsANoop(t *testing.T) {
	t.Parallel()

	withNilEnvironment := exec.Command("unused")
	configureTailscaleCommand(withNilEnvironment)
	if withNilEnvironment.Env != nil {
		t.Fatalf("nil command environment became %#v", withNilEnvironment.Env)
	}

	withExplicitEnvironment := exec.Command("unused")
	withExplicitEnvironment.Env = []string{"FIRST=1", "TAILSCALE_BE_CLI=0", "LAST=2"}
	configureTailscaleCommand(withExplicitEnvironment)
	want := []string{"FIRST=1", "TAILSCALE_BE_CLI=0", "LAST=2"}
	if !reflect.DeepEqual(withExplicitEnvironment.Env, want) {
		t.Fatalf("explicit command environment = %#v, want %#v", withExplicitEnvironment.Env, want)
	}
}
