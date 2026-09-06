package tailscale

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func systemTrustTestParts() (*red5PackageTrustRunner, *red5PackageTrustGuard) {
	return &red5PackageTrustRunner{
		outputs: map[string][]byte{packageTrustPKGUtilPath: []byte(red5TailscaleTrustedPKGUtilOutput)},
		errors:  map[string]error{},
	}, &red5PackageTrustGuard{path: "/private/stage/Tailscale.pkg", current: "admitted"}
}

func TestSystemPackageTrustPreservesToolOrderPathGuardsAndFixedCommands(t *testing.T) {
	runner, guard := systemTrustTestParts()
	events := &red5PackageTrustEventLog{}
	runner.events, guard.events = events, events
	if err := verifyMacPackageSystemTrust(context.Background(), guard, runner); err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{"revalidate", "pkgutil", "revalidate", "revalidate", "spctl", "revalidate"}
	if !reflect.DeepEqual(events.values, wantEvents) {
		t.Fatalf("phase order = %v, want %v", events.values, wantEvents)
	}
	want := []packageTrustCommandInvocation{
		{Path: "/usr/sbin/pkgutil", Arguments: []string{"--check-signature", guard.path}, Environment: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, OutputLimit: 4 << 20},
		{Path: "/usr/sbin/spctl", Arguments: []string{"--assess", "--type", "install", guard.path}, Environment: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, OutputLimit: 4 << 20},
	}
	if !reflect.DeepEqual(runner.invocations, want) {
		t.Fatalf("commands = %#v, want %#v", runner.invocations, want)
	}
}

func TestSystemPackageTrustStopsAtEveryFailureWithoutLeakingDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func(*red5PackageTrustRunner, *red5PackageTrustGuard)
		commands int
	}{
		{"before pkgutil", func(_ *red5PackageTrustRunner, g *red5PackageTrustGuard) { g.failAt = 1 }, 0},
		{"pkgutil exit", func(r *red5PackageTrustRunner, _ *red5PackageTrustGuard) {
			r.errors[packageTrustPKGUtilPath] = errors.New("raw /private/fixture failure")
		}, 1},
		{"after pkgutil", func(_ *red5PackageTrustRunner, g *red5PackageTrustGuard) { g.failAt = 2 }, 1},
		{"bad pkgutil output", func(r *red5PackageTrustRunner, _ *red5PackageTrustGuard) {
			r.outputs[packageTrustPKGUtilPath] = []byte("trusted lookalike")
		}, 1},
		{"pkgutil overflow", func(r *red5PackageTrustRunner, _ *red5PackageTrustGuard) {
			r.outputs[packageTrustPKGUtilPath] = bytes.Repeat([]byte{'x'}, (4<<20)+1)
		}, 1},
		{"before spctl", func(_ *red5PackageTrustRunner, g *red5PackageTrustGuard) { g.failAt = 3 }, 1},
		{"spctl exit", func(r *red5PackageTrustRunner, _ *red5PackageTrustGuard) {
			r.errors[packageTrustSPCTLPath] = errors.New("raw revoked /private/fixture")
		}, 2},
		{"spctl overflow", func(r *red5PackageTrustRunner, _ *red5PackageTrustGuard) {
			r.outputs[packageTrustSPCTLPath] = bytes.Repeat([]byte{'x'}, (4<<20)+1)
		}, 2},
		{"after spctl", func(_ *red5PackageTrustRunner, g *red5PackageTrustGuard) { g.failAt = 4 }, 2},
		{"persistent path replacement", func(r *red5PackageTrustRunner, g *red5PackageTrustGuard) {
			r.hook = func(packageTrustCommandInvocation) { g.current = "replacement" }
		}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner, guard := systemTrustTestParts()
			test.mutate(runner, guard)
			if err := verifyMacPackageSystemTrust(context.Background(), guard, runner); err != errMacPackageTrust {
				t.Fatalf("error = %v, want fixed trust error", err)
			}
			if len(runner.invocations) != test.commands {
				t.Fatalf("command count = %d, want %d", len(runner.invocations), test.commands)
			}
		})
	}
}

func TestSystemPackageTrustFreshEnvironmentForEachCommand(t *testing.T) {
	runner, guard := systemTrustTestParts()
	runner.hook = func(invocation packageTrustCommandInvocation) { invocation.Environment[0] = "DYLD_LIBRARY_PATH=/tmp" }
	if err := verifyMacPackageSystemTrust(context.Background(), guard, runner); err != nil {
		t.Fatal(err)
	}
	for _, invocation := range runner.invocations {
		if !reflect.DeepEqual(invocation.Environment, []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}) {
			t.Fatalf("environment mutated across commands: %v", invocation.Environment)
		}
	}
}

func TestSystemPackageTrustRejectsNilAndCancelledInputsBeforeCommands(t *testing.T) {
	runner, guard := systemTrustTestParts()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{
		verifyMacPackageSystemTrust(nil, guard, runner),
		verifyMacPackageSystemTrust(context.Background(), nil, runner),
		verifyMacPackageSystemTrust(context.Background(), guard, nil),
		verifyMacPackageSystemTrust(ctx, guard, runner),
	} {
		if err != errMacPackageTrust {
			t.Fatalf("error = %v, want fixed trust error", err)
		}
	}
	if len(runner.invocations) != 0 {
		t.Fatal("invalid input reached a command runner")
	}
}
