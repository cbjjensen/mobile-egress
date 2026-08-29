# Task 2 Report: Relay service and Docker deployment

## Status

Complete. The relay now provides atomic initialization, persistent certificate/enrollment/identity/metrics state, TLS and mTLS endpoints, Owner controls, WebSocket stream routing with destination policy and limits, aggregate-only health, and a Docker Compose deployment.

## Commits

- `e239756 feat(relay): implement secure tunnel service`

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
