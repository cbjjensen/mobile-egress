//go:build darwin

package tailscale

import "context"

func NewDarwinController(runner CommandRunner) *Controller {
	return newResolverController(resolveDarwinInstallation, runner)
}

func resolveDarwinInstallation(ctx context.Context) (DarwinInstallation, error) {
	return findDarwinInstallation(ctx, verifyDarwinBundle)
}
