package desktop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestShutdownBoundsUninterruptibleComponentAndDiscardsLateResult(t *testing.T) {
	for _, key := range controllerComponents {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			entered, release := make(chan struct{}), make(chan struct{})
			var canceled atomic.Bool
			var cleaned atomic.Bool
			m := newStatusMonitor(platformMacOS, monitorChecks{key: func(ctx context.Context) (componentResult, error) {
				close(entered)
				<-ctx.Done()
				canceled.Store(true)
				<-release
				return componentResult{helper: relayServiceEnabled, relayReady: true}, nil
			}})
			m.cleanup = func() { cleaned.Store(true) }
			app := &DesktopApp{monitor: m}
			m.start(context.Background())
			<-entered
			start := time.Now()
			app.shutdownApp()
			elapsed := time.Since(start)
			if elapsed > 1500*time.Millisecond {
				t.Fatalf("shutdown took %s", elapsed)
			}
			if !canceled.Load() {
				t.Fatal("check did not receive cancellation")
			}
			if cleaned.Load() {
				t.Fatal("resource closed while native worker still using it")
			}
			m.mu.Lock()
			running := m.components[key].running
			m.mu.Unlock()
			if !running {
				t.Fatal("uninterruptible operation lost its worker slot")
			}
			close(release)
			select {
			case <-m.done:
			case <-time.After(time.Second):
				t.Fatal("worker cleanup did not complete")
			}
			if !cleaned.Load() {
				t.Fatal("cleanup omitted")
			}
			if !m.snapshot().Components[key].Checking {
				t.Fatal("late result was published")
			}
		})
	}
}

func TestMonitorParentCancellationCannotStartQueuedWork(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	m := newStatusMonitor(platformWindows, monitorChecks{componentMetadata: func(context.Context) (componentResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return componentResult{}, nil
	}})
	m.start(ctx)
	<-entered
	m.request(componentMetadata)
	cancel()
	close(release)
	m.stop(time.Second)
	if calls.Load() != 1 {
		t.Fatal("queued work started after lifetime cancellation")
	}
}
