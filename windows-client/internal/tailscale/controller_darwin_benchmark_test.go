//go:build darwin

package tailscale

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This opt-in benchmark reads the real installed Tailscale app. It never runs
// connect, install, Funnel enable, or any other configuration-changing command.
// Run identical copies against both commits with -benchtime=10x -count=1.
func BenchmarkNativeControllerStatus(b *testing.B) {
	if os.Getenv("MOBILE_EGRESS_NATIVE_STATUS_BENCH") != "1" {
		b.Skip("requires explicit native status measurement opt-in")
	}
	var trustCommands, hashes, offlineReads int
	runner := &nativeStatusCountingRunner{}
	resolve := func(ctx context.Context) (DarwinInstallation, error) {
		return findDarwinInstallation(ctx, func(ctx context.Context, bundle, executable, requirement string) (verifiedDarwinApp, error) {
			return verifyDarwinAppWithDependencies(ctx, bundle, executable, requirement,
				func(ctx context.Context, bundle, executable string) (identityAppPathState, identityAppObservation, error) {
					state, observation, err := openIdentityDarwinAppPathState(ctx, bundle, executable)
					if err == nil {
						hashes++
					}
					if state == nil {
						return state, observation, err
					}
					return &nativeStatusCountingState{identityAppPathState: state, hashes: &hashes}, observation, err
				}, identityDarwinTrustRunner{newCommand: func(ctx context.Context, path string, args ...string) *exec.Cmd {
					trustCommands++
					return exec.CommandContext(ctx, path, args...)
				}})
		})
	}
	controller := newResolverController(resolve, runner)
	defer func() {
		if closer, ok := any(controller).(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()
	latencies := make([]float64, 0, b.N)
	var selfBefore, childBefore, selfAfter, childAfter unix.Rusage
	_ = unix.Getrusage(unix.RUSAGE_SELF, &selfBefore)
	_ = unix.Getrusage(unix.RUSAGE_CHILDREN, &childBefore)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		started := time.Now()
		status, err := controller.Status(ctx)
		latencies = append(latencies, float64(time.Since(started).Microseconds())/1000)
		cancel()
		if err != nil && (!errors.Is(err, ErrNotOnline) || !status.Installed || status.Online) {
			b.Fatalf("native status failed: %v", err)
		}
		if err != nil {
			offlineReads++
		}
		if i+1 < b.N {
			time.Sleep(time.Second)
		}
	}
	b.StopTimer()
	_ = unix.Getrusage(unix.RUSAGE_SELF, &selfAfter)
	_ = unix.Getrusage(unix.RUSAGE_CHILDREN, &childAfter)
	sort.Float64s(latencies)
	b.ReportMetric(float64(trustCommands), "trust-processes")
	b.ReportMetric(float64(runner.calls), "CLI-processes")
	b.ReportMetric(float64(hashes), "executable-hashes")
	b.ReportMetric(float64(offlineReads), "offline-reads")
	b.ReportMetric(latencies[len(latencies)/2], "status-median-ms")
	b.ReportMetric(latencies[len(latencies)-1], "status-max-ms")
	b.ReportMetric(nativeStatusCPU(selfAfter)-nativeStatusCPU(selfBefore), "self-CPU-ms")
	b.ReportMetric(nativeStatusCPU(childAfter)-nativeStatusCPU(childBefore), "child-CPU-ms")
	if closer, ok := any(controller).(interface{ Close() error }); ok {
		started := time.Now()
		if err := closer.Close(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(started).Microseconds())/1000, "close-ms")
	}
}

type nativeStatusCountingRunner struct{ calls int }

func (runner *nativeStatusCountingRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	runner.calls++
	return (ExecRunner{}).Run(ctx, executable, args...)
}

type nativeStatusCountingState struct {
	identityAppPathState
	hashes *int
}

func (state *nativeStatusCountingState) Observe(ctx context.Context) (identityAppObservation, error) {
	observation, err := state.identityAppPathState.Observe(ctx)
	if err == nil {
		*state.hashes++
	}
	return observation, err
}

func nativeStatusCPU(usage unix.Rusage) float64 {
	return float64(usage.Utime.Sec+usage.Stime.Sec)*1000 + float64(usage.Utime.Usec+usage.Stime.Usec)/1000
}
