//go:build darwin

package tailscale

import (
	"context"
	"errors"
	"os"
)

func NewDarwinController(runner CommandRunner) *Controller {
	return newResolverController(resolveDarwinInstallation, runner)
}

func resolveDarwinInstallation(ctx context.Context) (DarwinInstallation, error) {
	if _, err := os.Lstat(fixedTailscaleBundlePath); errors.Is(err, os.ErrNotExist) {
		return DarwinInstallation{}, ErrNotInstalled
	}
	return findDarwinInstallation(ctx, verifyDarwinBundle)
}
