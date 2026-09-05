package prerequisites

import (
	"context"
	"errors"
)

// EnsureRuntime installs only when missing and requires a successful post-install check.
func EnsureRuntime(ctx context.Context, check func() error, install func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check() == nil {
		return nil
	}
	if err := install(ctx); err != nil {
		return err
	}
	if check() != nil {
		return errors.New("WebView2 is still unavailable. Restart Windows and run MobileEgressSetup.exe again to finish setup.")
	}
	return nil
}
