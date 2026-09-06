# Controller status measurements

## Native measurement setup

Measured on the existing Apple M2 Pro build Mac, macOS 26.2, with its installed
Go 1.26.7 darwin/arm64 toolchain and Xcode 26.6. No software installation, signing,
release publication, Tailscale connection, or account changes were performed.
Source was transferred as a Git bundle into a disposable checkout. The baseline
is `perf/transport-latency` at `1d0b09ce0be1c8c6a62253edace59683efe0ae49`.
The measured updated implementation is `14576142ce04725624865f6b60b3dd99bf327ba5`.
Broad validation used `895a9046414d42ad1fac3a6844a01f9a6d57d6a7`, followed by
final desktop revalidation at `05032e2d81527ae916e71070205429df0179b14d`.
The measured Tailscale cache implementation did not change between these revisions.
Both production checkouts were verified clean before adding the same untracked
benchmark file; no production code was patched on the Mac.

The opt-in `BenchmarkNativeControllerStatus` uses the real installed
`/Applications/Tailscale.app`, production descriptor-based verification,
`codesign`/`spctl`, and the real read-only Tailscale status CLI. Counters wrap the
existing dependency seams; they do not substitute trust or CLI results. Both
commits run the same harness with ten status reads, separated by one second.

The Mac's Tailscale was disconnected throughout the baseline run. This is native
component evidence for the disconnected status path, not connected-idle desktop
CPU or an end-to-end Owner relay-health measurement. Offline results are counted
explicitly. Verification failures still fail the benchmark.

## Results

| Metric, ten reads | Baseline | Updated |
| --- | ---: | ---: |
| Tailscale CLI processes | 10 | 10 |
| Trust processes | 30 | 3 |
| Successful executable hash observations | 120 | 30 |
| Offline results | 10 | 10 |
| Status median | 390.4 ms | 81.18 ms |
| Status maximum | 430.0 ms | 350.4 ms |
| Harness process CPU | 957.0 ms | 329.8 ms |
| Child process CPU | 1822 ms | 681.5 ms |
| Retained-controller close | Not available on baseline | 0.027 ms |

The updated cache retained the verified application across the benign
disconnected results. Over this short sample, trust subprocesses fell 90%, hash
observations fell 75%, and median call latency fell about 79%. These results
include the first full verification and do not cover the 60-second trust expiry.

CPU values are aggregate user plus system CPU from `getrusage`, taken around the
ten-read loop. They exclude compilation and do not represent full desktop CPU.
Successful descriptor observations each execute a full executable hash in the
unchanged native verifier. Counts include the initial descriptor observation.
Benchmark `ns/op` includes deliberate one-second waits; use the separately
reported status latencies for call duration. The benchmark's automatic one-read
calibration is excluded from the reported ten-read counters.

## Native validation

On the `895a904` validation checkout, all 41 frontend tests and the TypeScript/Vite
production build passed using Node 24.20.0 and cached dependencies
(`npm ci --offline --ignore-scripts`). The full native Go test command ran on
the measured revision but failed: this Mac does not have the application's
`127.0.0.2` loopback alias, so
existing SOCKS/HTTP CONNECT listeners and their client/node/relay integration
tests could not bind. The controller's desktop, cloud, and Tailscale suites passed
within that run. No network configuration was changed to work around this.
The same listener failure was reproduced with
`TestCoreStartsStopsAndExposesOnlyRedactedStatusForStoredClient` on the baseline.

`go vet ./...` and `go build ./...` passed with native CGO enabled on `895a904`.
Full native desktop and Tailscale race suites passed, as did the
alternate `-tags bindings` desktop suite. Earlier native race checks also passed
for the complete cloud package, `TestOwnerSnapshot`, `TestHealthClient`, and the
existing `TestRelayHealth` test.

Actual Wails 2.14.0 `generate module -nocolour` completed successfully on the
`895a904` native checkout. Generated `ControllerSnapshot.components` is
`Record<string, ComponentStatus>` and `lastSuccess` is an optional string.
`OwnerSnapshot` is absent from generated bindings. The frontend build passed
again after generation. Wails printed `Not found: time.Time` while generating
the existing model set; it did not prevent successful generation or build.
Native linking also emitted the existing duplicate `-lobjc` warning.

After the small initial-helper-failure fix, the exact clean `05032e2` checkout
passed the complete native desktop race suite and `-tags bindings` desktop suite,
including `TestFirstHelperFailurePreservesRelayStateContract`. Unchanged frontend
and Tailscale measurements were not repeated.

## Reproduction

Use the existing pinned Go toolchain and cached modules, set `GOTOOLCHAIN=local`
and `GOPROXY=off`, and enable CGO with the repository's macOS 13.0 deployment
flags. After checking out the requested exact commit, apply the identical
benchmark file to the baseline only if it predates the harness.

```sh
MOBILE_EGRESS_NATIVE_STATUS_BENCH=1 go test ./windows-client/internal/tailscale \
  -run '^$' -bench '^BenchmarkNativeControllerStatus$' -benchtime=10x -count=1
```

Full UI idle subprocess/CPU measurements, connected Funnel status, Owner relay
health connection reuse against a live relay, and native application quit timing
remain unmeasured. The benchmark's controller `Close` measures descriptor cleanup,
not application quit. Portable tests must not be substituted for those results.

## Windows validation

On Windows with Go 1.26.3, `go test ./...`, `go vet ./...`, and
`go build ./...` passed. Race checks passed for desktop, Tailscale, Core client,
cloud repository, and relay client using CGO with GCC 16.2.0; the desktop race
suite was repeated after integration fixes. All 41 frontend tests, TypeScript
check, and Vite build passed with the existing Node 23.9.0 installation.

Two initial full-suite runs hit the unchanged relay admin authorization test's
two-second deadline. That test passed five isolated repetitions and the final
full-suite command passed. No authorization logic or test timeout was changed
for that test. These Windows results are correctness evidence, not Mac CPU or
latency measurements.
