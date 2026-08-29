# Task 2 Report: Relay service and Docker deployment

## Status

Complete. The relay now provides atomic initialization, persistent certificate/enrollment/identity/metrics state, TLS and mTLS endpoints, Owner controls, WebSocket stream routing with destination policy and limits, aggregate-only health, and a Docker Compose deployment.

## Commits

- `e239756 feat(relay): implement secure tunnel service`
- `6b09ca1 docs: record relay task verification`
- `0df179b fix(relay): close revocation and deadline races`

## Changed files

- `go.mod`, `go.sum`
- `relay/Dockerfile`
- `relay/cmd/relay/main.go`, `relay/cmd/relay/main_test.go`
- `relay/internal/policy/policy.go`, `relay/internal/policy/policy_test.go`
- `relay/internal/service/enrollment.go`, `relay/internal/service/enrollment_test.go`
- `relay/internal/service/init.go`
- `relay/internal/service/service.go`, `relay/internal/service/service_test.go`
- `relay/internal/service/session.go`, `relay/internal/service/session_test.go`
- `relay/internal/service/store.go`
- `deploy/docker-compose.yml`, `deploy/.env.example`
- `docs/operations.md`

## Red/green evidence

The implementation used real service tests first in discrete batches:

- RED `go test ./relay/internal/service`: failed to compile with undefined `Initialize`, `InitOptions`, `openStore`, `Open`, and `Service`.
- GREEN `go test ./relay/internal/service`: initialization persistence, invalid-state refusal, generated TLS, and redacted readiness passed.
- RED `go test ./relay/internal/service -run 'TestEnroll|TestOnlyActiveOwner' -count=1`: failed because persistent `identityStatus` and enrollment controls did not exist.
- GREEN same command: invalid, reused, expired, and role-mismatched capability rejection; CSR/public-key enrollment; Owner-only pairing; and revocation passed.
- RED `go test ./relay/internal/service -run 'TestSession|TestClientOpen|TestAggregateMetrics' -count=1`: all session tests failed with `404 Not Found` at `/v1/session`.
- GREEN same command: mTLS roles, one-Agent enforcement, immediate session revocation, owning-stream routing, per-Client and Agent-wide limits, policy rejection, and destination/payload-free metrics passed.
- RED `go test ./relay/internal/policy -run TestValidatePublicTCPAddress -count=1`: failed with undefined `ValidatePublicTCPAddress`.
- GREEN same command: the required public-address API passed while retaining the Task 1 compatibility name.
- RED `go test ./relay/internal/service -run TestOpenRejectsMissingOrInvalidInitializedState -count=1`: proved `Open` recreated a missing SQLite file.
- GREEN same command: initialized state is now opened strictly without creating or repairing missing state.
- RED `go test ./relay/cmd/relay -count=1`: failed with undefined `run`.
- GREEN same command: `init` persistence/one-time output and `serve` invalid-state refusal passed.
- RED `go test ./relay/internal/service -run TestHealthzFailureStillReturnsOnlyRedactedAggregateFields -count=1`: degraded health returned non-JSON text.
- GREEN `go test ./relay/internal/service -run 'TestHealthz' -count=1`: both ready and degraded health preserve the exact aggregate-only JSON shape.

Final verification on the completed tree:

- `go test ./... -count=1` — PASS across relay command, enrollment, policy, protocol, and service packages.
- `go vet ./...` — PASS with no findings.
- `go build ./relay/cmd/relay` — PASS.
- `docker compose -f deploy/docker-compose.yml config` — PASS; renders only the TLS tunnel port `8443/tcp`, the health check, and `./data:/var/lib/mobile-egress`.
- `git diff --cached --check` — PASS before the implementation commit; only Windows line-ending notices were emitted.

## Concerns

- No blocking implementation concern remains.
- The required Compose configuration was validated, but the Docker image was not built or started as part of the specified verification commands, so the container health check has not been runtime-smoked on this machine.
- Windows does not expose Unix permission bits reliably; the private-key mode assertion is enforced on non-Windows systems, including the Alpine relay container.

## Review fix round

Status: complete. Commit `0df179b` closes the reviewed revocation, admission, writer-backpressure, and late-`opened` races.

Focused RED evidence:

- The initial focused test command failed to compile because the controlled DNS seam did not exist: `fixture.service.lookupNetIP undefined`.
- After adding only the production-default `net.DefaultResolver.LookupNetIP` seam, `go test ./relay/internal/service -run 'TestRevocationDuringResolution|TestSessionRegistrationRevalidates|TestStreamExpirationDoesNotWait|TestRevocationDoesNotWait|TestAgentOpenedAfterStoredDeadline' -count=1` failed all five behavioral regressions:
  - Agent received `open` after Client revocation completed.
  - A revoked Agent received `101 Switching Protocols` after stale authentication.
  - Stream expiration waited on a blocked WebSocket writer.
  - Revocation waited on a blocked stream notification.
  - A post-deadline `opened` frame was forwarded instead of producing `opening_timeout`.

Focused GREEN evidence:

- The same five-test command passed after the correction.
- The focused command passed ten consecutive runs with `-count=10`.
- `go test ./relay/internal/service -count=1` passed the complete service suite.

Fix details:

- Client identity and registration are revalidated after DNS resolution, under the same service lock that commits and forwards the stream.
- Session admission revalidates the identity under the registration lock. Production revocation now mutates SQLite and detaches session/stream state under that same lock.
- Expiration and revocation remove state before dispatching asynchronous best-effort notifications. Every WebSocket data notification has a one-second writer-lock/write deadline, while close control frames use their existing one-second deadline and do not wait on the data-writer mutex.
- Agent `opened` checks the stored opening deadline directly and closes the stream with `opening_timeout` even before the periodic sweep.

Post-fix required verification:

- `go test ./... -count=1` — PASS across all five Go packages.
- `go vet ./...` — PASS with no findings.
- `go build ./relay/cmd/relay` — PASS.
- `docker compose -f deploy/docker-compose.yml config` — PASS.

Additional concern: `go test -race ./relay/internal/service -count=1` could not start because this environment has CGO disabled (`-race requires cgo`). The deterministic interleaving tests above pass repeatedly, but race-detector coverage remains to be run in a CGO-enabled environment.
