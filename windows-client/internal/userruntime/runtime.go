// Package userruntime owns the lifetime of the in-process Mac Client. It never
// installs a service or alters another process, proxy address, or firewall.
package userruntime

import (
	"context"
	"sync"
	"time"
)

type Lifetime struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// New starts one runtime at a time. Startup failures remain visible through
// Direct.Status while bounded retries allow local settings/storage repair.
func New(parent context.Context, run func(context.Context) error, retry time.Duration) *Lifetime {
	ctx, cancel := context.WithCancel(parent)
	lifetime := &Lifetime{cancel: cancel, done: make(chan struct{})}
	if retry <= 0 {
		retry = 2 * time.Second
	}
	go func() {
		defer close(lifetime.done)
		for ctx.Err() == nil {
			_ = run(ctx)
			if ctx.Err() != nil {
				return
			}
			timer := time.NewTimer(retry)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return lifetime
}

// Close returns after Direct has closed its listeners and active traffic.
// Calling it for window-close and native shutdown is safe.
func (l *Lifetime) Close() {
	l.once.Do(l.cancel)
	<-l.done
}
