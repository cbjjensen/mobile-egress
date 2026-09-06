# Maintenance simplification

The owner approved all four follow-up simplifications. Keep the personal-computer relay and Funnel topology, wire formats, mobile behavior, and release restrictions intact.

1. Extract the common SOCKS/HTTP CONNECT pre-open reader lifecycle into one small helper. Preserve buffering, cancellation, deadlines, and protocol-specific replies; exercise both listeners and race checks.
2. Put the Go tunnel envelope, JSON validation, payload limits, and negotiated serialization in `internal/tunnelwire`. Retain relay-facing aliases and Client session authorization checks. Use the relay's existing strict field contract on both peers; malformed duplicate, missing, null, or alternate-cased fields must be rejected. Preserve valid legacy and binary fixtures.
3. Express existing release exceptions in a small data table. Preserve component scopes, fallback downloads, and rejection behavior; run release policy tests without executing a release.
4. Remove the unused custom Mac package-verification stack after tracing production callers. Preserve the active native signature, signer-identity, assessment, and staging checks; verify portable tests and Darwin compilation/native tests where available.

Integration: review the combined diff, run the Go suite, vet/build, targeted race checks and release script tests, and record exact results and any native validation limits here. No publishing or installed-app replacement is part of this work.

## Completed implementation and validation

All four changes are implemented. The Mac production constructor already used the native system verifier; the removed custom parser, certificate evaluator, embedded roots, and dependency factory had no live production callers. The active `pkgutil`/`spctl` path remains intact, with additional tests on that path replacing obsolete custom-verifier tests.

- `go test ./...`, `go vet ./...`, and `go build ./...` passed on Windows.
- Race checks passed for the shared tunnel codec, relay service, Client relay session, pre-open helper, SOCKS, HTTP CONNECT, and Tailscale package.
- `scripts/test-release-all.ps1` passed. A temporary differential comparison of the old and new release functions matched 888 scope/download cases, including error strings. Script tests use fake release operations; no release was executed.
- Darwin arm64 Tailscale test cross-compilation with CGO disabled passed. Native Mac `go test ./windows-client/internal/tailscale -count=1` passed in 4.025 seconds using the existing build server and Go 1.26.7. This is the relevant Go package check; the earlier unrelated iOS Xcode test-runner limitation is not claimed resolved.
- Existing codec fixture tests moved into the shared package. Client regressions for duplicate, missing, null, alternate-cased fields and blank stream IDs failed before consolidation and pass afterward. Mixed-version/session tests pass. Independent codec and proxy reviews found no actionable correctness defects.
- `git diff --check` passed. Documentation records the shared ownership and active Mac verification path.

## Parser cost

A temporary single-CPU benchmark compared the previous Client parser with the shared strict parser (one second per case, three runs). Median small legacy data parsing rose from 3.06 to 6.03 microseconds; small ping parsing rose from 3.60 to 6.60 microseconds. The shared JSON parser adds 47 allocations and 768 bytes per message. The 32 KiB data case was noisy (323.8 versus 286.6 microseconds), so it does not establish an improvement. Negotiated binary parsing is unchanged. This small legacy-only cost buys consistent strict validation; this maintenance cleanup makes no end-to-end latency claim. Temporary benchmark code was removed after measurement.
