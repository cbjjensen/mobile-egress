package desktop

import (
	"context"
	"errors"
	"sync"
	"time"

	"mobile-egress/windows-client/internal/tailscale"
)

type SetupProgress struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

type setupAction struct {
	cancel   context.CancelFunc
	progress SetupProgress
}

// GetSetupProgress is a memory-only read suitable for UI polling.
func (app *DesktopApp) GetSetupProgress() SetupProgress {
	app.mu.RLock()
	defer app.mu.RUnlock()
	if app.setup == nil {
		return SetupProgress{}
	}
	return app.setup.progress
}

// CancelSetup keeps the action slot occupied until native work has returned,
// preventing cancellation from accidentally launching a second installer.
func (app *DesktopApp) CancelSetup() {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.setup != nil {
		app.setup.cancel()
		app.setup.progress = SetupProgress{Stage: "cancelling", Message: "Cancelling setup. Close any installer or approval window if it remains open."}
	}
}

func (app *DesktopApp) beginSetup(timeout time.Duration, stage, message string) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(app.operationContext(), timeout)
	action := &setupAction{cancel: cancel, progress: SetupProgress{Stage: stage, Message: message}}
	app.mu.Lock()
	if app.setup != nil {
		app.mu.Unlock()
		cancel()
		return nil, nil, errors.New("Setup is already in progress. Wait for the current action to finish.")
	}
	app.setup = action
	app.mu.Unlock()
	ctx = tailscale.WithSetupProgress(ctx, func(stage, message string) {
		app.mu.Lock()
		defer app.mu.Unlock()
		if app.setup == action && ctx.Err() == nil {
			action.progress = SetupProgress{Stage: stage, Message: message}
		}
	})
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			app.mu.Lock()
			if app.setup == action {
				app.setup = nil
			}
			app.mu.Unlock()
		})
	}, nil
}
