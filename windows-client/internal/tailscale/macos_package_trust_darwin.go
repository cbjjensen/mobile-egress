//go:build darwin

package tailscale

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"
)

func verifyStagedMacPKGOnDarwin(ctx context.Context, stage *stagedMacPKG) error {
	return verifyStagedMacPKGWithDependencies(ctx, stage, darwinStagedMacPKGTrustDependencies())
}

func darwinStagedMacPKGTrustDependencies() stagedMacPKGTrustDependencies {
	return stagedMacPKGTrustDependencies{
		loadRoots:    loadEmbeddedAppleRoots,
		newEvaluator: newDarwinPackageChainTrustEvaluator,
		runner:       packageTrustDarwinCommandRunner{},
		now:          time.Now,
		verify:       verifyStagedMacPKG,
	}
}

type packageTrustDarwinCommandRunner struct {
	newCommand func(context.Context, string, ...string) *exec.Cmd
}

func (runner packageTrustDarwinCommandRunner) Run(ctx context.Context, invocation packageTrustCommandInvocation) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || !validDarwinPackageTrustInvocation(invocation) {
		return nil, errMacPackageTrust
	}
	output, err := runDarwinBoundedCommand(
		ctx,
		runner.commandFactory(),
		invocation.Path,
		invocation.Arguments,
		invocation.Environment,
		int(invocation.OutputLimit),
	)
	if err != nil || ctx.Err() != nil {
		return nil, errMacPackageTrust
	}
	return output, nil
}

func (runner packageTrustDarwinCommandRunner) commandFactory() func(context.Context, string, ...string) *exec.Cmd {
	if runner.newCommand != nil {
		return runner.newCommand
	}
	return exec.CommandContext
}

func validDarwinPackageTrustInvocation(invocation packageTrustCommandInvocation) bool {
	canonicalEnvironment := newPackageTrustEnvironment()
	if invocation.OutputLimit != maximumPackageTrustOutput || len(invocation.Environment) != len(canonicalEnvironment) ||
		len(invocation.Arguments) == 0 || !filepath.IsAbs(invocation.Arguments[len(invocation.Arguments)-1]) {
		return false
	}
	for index := range canonicalEnvironment {
		if invocation.Environment[index] != canonicalEnvironment[index] {
			return false
		}
	}
	switch invocation.Path {
	case packageTrustPKGUtilPath:
		return len(invocation.Arguments) == 2 && invocation.Arguments[0] == "--check-signature"
	case packageTrustSPCTLPath:
		return len(invocation.Arguments) == 4 && invocation.Arguments[0] == "--assess" &&
			invocation.Arguments[1] == "--type" && invocation.Arguments[2] == "install"
	default:
		return false
	}
}

var _ packageTrustCommandRunner = packageTrustDarwinCommandRunner{}
