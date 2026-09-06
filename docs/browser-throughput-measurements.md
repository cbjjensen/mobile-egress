# Browser throughput validation

Baseline: `e3f2922` (merged main). Updated source: branch `perf/browser-throughput`.

## Automated evidence

| Check | Baseline | Updated |
| --- | --- | --- |
| Relay admission past 256 | New regression fails at open index 256 | 1,100 held streams plus another Client admitted; ownership and cleanup checked |
| Windows combined proxy streams | New regression fails at stream 257 | 1,100 combined SOCKS, CONNECT, ordinary HTTP and direct streams admitted |
| Android admission | New regressions fail at old cap | Admission, bridge and reactor tests exceed 1,024 live reservations; 300 real loopback target exchanges |
| iOS admission/status | Native regressions fail at old cap / status 257 | Native Swift tests cover 1,100 live streams, teardown and representable status counts |
| Windows health outage | New regression closes connected tunnel | Relay retained, established data flows, new admission remains gated |
| Android SDK queue debt | Old enqueue-release behavior fails new tests | FIFO completion accounting preserves data/control budgets through SDK buffering |

These are correctness observations, not throughput measurements.

## Physical browser measurements

**Not run.** The Android phone and Mac build server are reachable. A controlled public HTTPS fixture and isolated relay configuration have been requested; none has been supplied for this run. The installed Android app has not been replaced or its pairing state changed. No navigations per minute, latency percentiles, memory plateau, cellular throughput, qualifying browser level, or 30-minute soak result is claimed.

The isolated browser harness records the requested 1/10/25/50/100-context matrix, three baseline/updated pairs per profile, 60-second warm-up and 180-second measurement, with a 30-minute sustained run at the highest qualifying level. Use the same phone, fixture, connectivity and proxy topology for each pair. Report overload separately. A level qualifies only with at least 99% of navigations completed within 30 seconds, stable connectivity, no corruption or avoidable resets, and no sustained resource leak. Missing telemetry leaves qualification unverified.

Physical iOS throughput remains **unverified (no device)**. Native Swift and unsigned iOS compilation establish source/build compatibility only.

## Verification record

- `go test ./...`, `go vet ./...`, `go build ./...`: passed on Windows with Go 1.26.3.
- `go test -race` for relay service, relay Client, node service, SOCKS and HTTP CONNECT: passed using GCC 16.2.0.
- Tagged capacity harness, CLI and signed-wrapper tests, vet and build: passed. Harness tests exercise default and maximum finite developer run sizes independently of production admission.
- Android `testDebugUnitTest lintDebug assembleDebug`: passed; 208 tests, zero failures. The 81 session tests include 12 sender regressions. Debug artifacts were built locally, not installed on the phone.
- Mobile-feature parity validation: passed.
- Browser harness: eight Node tests passed, including explicitly trusted TLS with HTTP/1.1 and HTTP/2 connection-identity checks. Pinned Playwright 1.63.0 / Chromium 153.0.8010.12 rendered two loopback HTTP fixture navigations and 74 requests. This is a functional smoke test, not a proxy or phone performance run.
- Native Mac workflow (`scripts/test-ios.ps1 -UseMacBuildServer`) used exact commit `84eab58` with Xcode 26.6. Swift package tests, warnings-as-errors tests, unsigned iPhoneOS app/extension build and unsigned simulator build completed. Subsequent changes do not modify iOS source or tests.
- The final Xcode package test runner failed with exit 65 because `com.apple.testmanagerd.control` was unavailable. The workflow's retry encountered the same infrastructure error; the full native gate is therefore **not passing**. No Apple-account, signing or system-service changes were made to bypass it.

No publication or production deployment was performed.
