//go:build darwin

package tailscale

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestDarwinStagedMacPKGVerifierUsesSystemTrust(t *testing.T) {
	var verifier func(context.Context, *stagedMacPKG) error = verifyStagedMacPKGOnDarwin
	if verifier == nil {
		t.Fatal("fixed Darwin staged-package verifier is nil")
	}
}

func TestDarwinPackageTrustInvocationIsFixedToAppleToolsAndMinimalEnvironment(t *testing.T) {
	valid := packageTrustCommandInvocation{
		Path: packageTrustPKGUtilPath, Arguments: []string{"--check-signature", "/private/stage/Tailscale.pkg"},
		Environment: newPackageTrustEnvironment(), OutputLimit: maximumPackageTrustOutput,
	}
	if !validDarwinPackageTrustInvocation(valid) {
		t.Fatal("exact pkgutil invocation rejected")
	}
	tests := []struct {
		name   string
		mutate func(*packageTrustCommandInvocation)
	}{
		{name: "relative tool", mutate: func(value *packageTrustCommandInvocation) { value.Path = "pkgutil" }},
		{name: "shell", mutate: func(value *packageTrustCommandInvocation) { value.Path = "/bin/sh" }},
		{name: "wrong argument", mutate: func(value *packageTrustCommandInvocation) { value.Arguments[0] = "--expand" }},
		{name: "relative package", mutate: func(value *packageTrustCommandInvocation) { value.Arguments[1] = "Tailscale.pkg" }},
		{name: "inherited path", mutate: func(value *packageTrustCommandInvocation) {
			value.Environment = append(value.Environment, "HOME=/Users/operator")
		}},
		{name: "loader override", mutate: func(value *packageTrustCommandInvocation) { value.Environment[0] = "DYLD_LIBRARY_PATH=/tmp" }},
		{name: "wrong cap", mutate: func(value *packageTrustCommandInvocation) { value.OutputLimit++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := packageTrustCommandInvocation{
				Path: valid.Path, Arguments: append([]string(nil), valid.Arguments...),
				Environment: append([]string(nil), valid.Environment...), OutputLimit: valid.OutputLimit,
			}
			test.mutate(&value)
			if validDarwinPackageTrustInvocation(value) {
				t.Fatal("mutated native trust invocation accepted")
			}
		})
	}
}

func TestDarwinPackageTrustRunnerBoundsDescendantHeldPipesOnTimeoutAndOverflow(t *testing.T) {
	invocation := packageTrustCommandInvocation{
		Path: packageTrustPKGUtilPath, Arguments: []string{"--check-signature", "/private/stage/Tailscale.pkg"},
		Environment: newPackageTrustEnvironment(), OutputLimit: maximumPackageTrustOutput,
	}
	tests := []struct {
		name    string
		script  string
		timeout time.Duration
	}{
		{
			name:    "context timeout kills descendant holding inherited pipes",
			script:  `(trap '' TERM; sleep 30) & wait`,
			timeout: 100 * time.Millisecond,
		},
		{
			name:    "aggregate overflow kills producing descendant",
			script:  `(while :; do printf '0123456789abcdef0123456789abcdef'; done) & wait`,
			timeout: 10 * time.Second,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			runner := packageTrustDarwinCommandRunner{newCommand: func(commandContext context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(commandContext, "/bin/sh", "-c", test.script)
			}}
			started := time.Now()
			if _, err := runner.Run(ctx, invocation); err == nil {
				t.Fatal("bounded package trust runner accepted failed helper execution")
			}
			if elapsed := time.Since(started); elapsed > 8*time.Second {
				t.Fatalf("descendant cleanup took %s, want at most 8s", elapsed)
			}
		})
	}
}
