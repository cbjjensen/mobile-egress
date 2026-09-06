package tailscale

import (
	"context"
	"time"
)

const controllerVerificationTTL = 60 * time.Second

// The gate protects the retained guard through the entire CLI operation, so
// replacement and shutdown cannot close descriptors while a command uses them.
type controllerVerification struct {
	gate         chan struct{}
	now          func() time.Time
	installation DarwinInstallation
	verifiedAt   time.Time
	closed       bool
}

func (cache *controllerVerification) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case cache.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			cache.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (cache *controllerVerification) release() { <-cache.gate }

func (cache *controllerVerification) discard() error {
	guard := cache.installation.guard
	cache.installation = DarwinInstallation{}
	cache.verifiedAt = time.Time{}
	if guard != nil && guard.Close() != nil {
		return errTailscaleAppCleanup
	}
	return nil
}

// Close releases retained verification resources after any active operation.
// A closed resolver controller cannot dispatch further commands.
func (controller *Controller) Close() error {
	if controller == nil || controller.verification == nil || controller.guard != nil {
		return nil
	}
	cache := controller.verification
	_ = cache.acquire(context.Background())
	defer cache.release()
	cache.closed = true
	return cache.discard()
}
