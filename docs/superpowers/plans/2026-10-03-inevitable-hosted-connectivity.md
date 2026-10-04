# Mobile Egress on Inevitable Infrastructure

Approved 2026-10-03. Implement in the existing checkouts on `feature/mobile-egress-inevitable-gateway`; do not merge, deploy, or publish releases. Inevitable requirements and implementation records live under `technical-requirement-docs/2026-10-03-inevitable-hosted-connectivity/` in the sibling repository. Keep both records synchronized.

## Contract

Existing Inevitable proxy infrastructure and code are the reference implementation. Preserve existing authentication, routing, provider selection, configuration, usage submissions, accounting, billing, health and shutdown behavior except the two explicitly authorized transport repairs below. Keep those repairs independently reviewable from Mobile Egress. Do not redesign existing handlers or create a competing framework.

Applications use a loopback Client proxy. The Client and phone both connect outbound on TCP 443 through the existing Inevitable gateway machines; traffic exits through the owner's phone cellular connection. The phone-to-Client TLS connection stays end-to-end encrypted, using the existing Client authority and non-exportable phone key. Gateways see connection metadata but receive neither inner credentials nor plaintext traffic.

Gateway mode is the default; direct mode remains an explicit Advanced choice, usable without Inevitable activation. No automatic switching or NAT traversal. Preserve Windows x64 10/11 and Server 2019+, Apple Silicon macOS 13+, existing local proxy addresses/ports, one phone per Client, ten saved Clients per phone, and shared phone budgets (8,192 frames/64 MiB per direction, 32 frames per stream). Android retains its foreground service; iOS shares only while active, with keep-awake enabled by default. Locking/switching apps still pauses iOS.

Mobile Egress access is entitlement-based, initially admin-granted pilot access. Future purchases/subscriptions may grant the same entitlement; pricing and checkout are deferred. Mobile Egress must not submit traffic usage, destinations, customer byte counts or accounting records. No usage ledger, data quota, overage, usage dashboard, fixed Mbps throttle or arbitrary application-stream ceiling. Other proxy usage reporting and billing remain unchanged. Operational health/errors/CPU/memory/buffer pressure/connection counts and controlled test benchmarks are permitted.

## Task 1 — Preserve, commit, document

- [x] Preserve and commit existing Mobile Egress baseline on main before hosted changes: `14c9d35e1bfbc43fc9608050beca1850cd52088b`, `feat: complete direct clients and guided setup`.
- [x] Fresh baseline `scripts/test-all.ps1 -Components Windows,Android` passed; evidence in ignored `.local/hosted-baseline-gate.log`.
- [x] Create feature branches in both existing checkouts; preserve local benchmark/recovery artifacts.
- [x] Save this approved plan before implementation.
- [x] Create Inevitable analysis/plan/implementation records; read and follow complete `ai-instructions.txt`, scoped instructions and accepted architecture documents.
- [x] Execute in independently testable phases; update decisions, checks, expected files, rollback and commit boundaries as work proceeds.

## Task 2 — Existing gateway repairs

- [x] Add failing regression for discarded hijacked buffered CONNECT bytes; forward buffered and subsequent bytes once and in order.
- [x] Add failing regression for reverse-response truncation after request EOF; half-close only the completed direction and let the reverse response drain.
- [x] Ensure wrappers forward half-close; verify trailing bytes, simultaneous EOF, slow readers, write errors, cancellation, idle/max-duration and shutdown termination.
- [x] Preserve usage byte-count/accounting semantics; run existing gateway tests and commit these repairs independently.

## Task 3 — Minimal hosted transport

- [x] Separate small Go service beside existing gateway processes on existing baseline/replacement machines, with independently scoped credentials, resource budgets, health, deployment and rollback.
- [x] Dedicated TCP 443 NLB listener/target group, leaving existing listener/targets/ASG health ownership intact. Mobile failure must not replace healthy Core nodes.
- [x] One outbound TLS connection per Client, using standalone `go-yamux/v5` for bounded virtual connections. Supply the Client's existing TLS/HTTP server with a virtual `net.Listener`.
- [x] Stable Client route hostname; bounded TLS ClientHello/SNI inspection; forward original phone TLS bytes unchanged. Reject malformed/unknown/incompatible requests. No arbitrary forwarding or public administration.
- [x] Mobile-only route-owner records in existing PostgreSQL, through the existing backend. Atomically bind route to registered gateway node and process/session generation; conditional cleanup cannot delete replacement ownership.
- [x] Follow existing heartbeat, config freshness/cache and retry patterns. No Redis rendezvous, distributed lock service, custom node certificate issuer or separate timed authorization framework.
- [x] Narrow private bridge when NLB connections arrive at different nodes, protected by deployment-managed TLS and scoped service credentials. Validate peer endpoint and exact owner generation; reconnect/re-resolve stale routes without byte replay.
- [x] Keep data traffic out of backend. Bound handshakes, buffers, memory and descriptors; reserve control capacity, preserve backpressure, cancellation, fair progress and existing drain conventions.

## Task 4 — Entitlement and applications

- [x] Shared contracts first for browser activation (existing expiring PKCE device-link pattern), pilot grants, scoped Client credentials, list/status/revoke, gateway configuration and route ownership.
- [x] Reuse existing account identity and auth/configuration patterns; keep infrastructure and commercial proxy secrets out of apps. Distinguish gateway access revocation from local phone unpairing.
- [x] No Mobile Egress usage APIs/tables/dispatch/accounting bypass flags; mixed ordinary proxy accounting must remain intact.
- [x] Client setup: Install → activate Inevitable → pair phone → verify → copy proxies. Hosted mode opens no inbound workload listener or firewall rule and performs no public-IP discovery.
- [x] Protected service storage/IPC owns credentials and asynchronous activation. Preserve Windows DPAPI/ownership and Mac LaunchDaemon/file-based System Keychain, repair and upgrades.
- [x] Separate activation, gateway connection, pairing and authenticated phone connection states; only live authenticated phone connection verifies setup.
- [x] Explicit transport metadata in invitations/records/signed updates on Go, Android and iOS. Retain native phone TLS, cellular-only sockets, limits/budgets/lifecycle and independent recovery.
- [x] Existing records default direct until explicit migration. Preserve CA, Client/pairing identity, proxy credentials and revocation state; issue hosted hostname server certificate. Signed updates bind mode/endpoint/identity/generation with live and QR/file recovery and durable acknowledgement.
- [x] Reject stale/conflicting updates, trust substitution, incompatible versions and legacy relay configuration.

## Task 5 — Documentation, verification, completion

- [x] Synchronize product requirements, README/onboarding, architecture/security/protocol/operations/troubleshooting/physical acceptance and manifest source/test evidence. Keep router/provider ingress guidance only for Advanced direct mode.
- [x] Update Inevitable accepted architecture docs. Historical releases/benchmarks remain immutable; record hosted results separately.
- [ ] Test Core behavior before/after with Mobile disabled/enabled/overloaded/restarting/unavailable; only authorized CONNECT behavior changes are permitted. Prove ordinary usage submissions continue and Mobile usage submissions are absent.
- [x] Cover activation expiry/replay, cross-owner isolation, revocation, spoofing, stale ownership, outages, secret-safe diagnostics; TLS rejection, pairing persistence/recovery, fragmented ClientHello, exact bytes/EOF, slow readers, cancellation, budgets/fairness, cross-node forwarding and cleanup.
- [ ] Cover node loss/reconnect/scale-in/drain, TLS renewal, offline mode migration, platform lifecycle and legacy configuration rejection.
- [x] Run Go/race, backend/contracts/full frontend build, installer/release contracts, Android, native Mac/iOS, doc-link and mobile-manifest checks where available. Explicit unavailable PostgreSQL, signing, deployment and device gates are recorded below.
- [ ] Physical Windows/Mac + Android/iPhone HTTP/CONNECT/SOCKS without router changes, including cellular IPv6/NAT64, iOS keep-awake and lifecycle. Signed installation/repair/upgrade acceptance remains mandatory before release readiness.
- [ ] Sustained one-/ten-Client throughput/latency/fairness/memory/CPU/thermal measurements, no correctness/new Core overload failures, at most 5% Core throughput/p95 setup regression under agreed combined load. No promised Mbps rate.
- [x] Update `ai-instructions.txt` with actual frontend patterns: typed access states, focused queries, terminal polling, thin routes, feature-owned controls, safe diagnostics, correct CSS ownership. Preserve existing conventions.
- [ ] Record commits, evidence, rollback and blockers; push reviewed feature branches after applicable checks. Keep infra off by default. No merge, deployment or release publication.

## Interfaces and execution record

Gateway public interfaces carry opaque TLS only, except the authenticated Client attachment. Protected backend contracts and wire details are pinned in the sibling implementation records before dependent code. New Mobile configuration/auth records remain separate from commercial proxy configuration/accounting. Reuse patterns without changing existing traffic contracts.

2026-10-03 baseline: removed an empty Git index lock older than two hours only after confirming no Git process; committed preserved work. Windows/Android integration gate passed. Native signing/physical acceptance is not implied by this run. Subsequent phase results and any deviations will be recorded here and in the Inevitable implementation summary.

### Implementation and verification — 2026-10-04

Tasks 1–4 are implemented. Core repairs remain independently committed as `43a66d43`; new service code is separate. Infrastructure stays disabled by default, on existing gateway machines, and preserves Core listener/health ownership. Mobile data has no usage ingestion, billing counter, destination reporting or quota path. Pilot access uses existing accounts and additive grants; checkout/pricing remains deferred.

Independent reviews drove red/green fixes for route claim/install ordering, authorization expiry during backend stalls, late rejected claims, old-session false verification during mode changes, real negative admission replies, and wizard continuation after approval. Final independent review found no remaining P1/P2 issues in the reviewed software scope. Confirmed credential rejection produces recovery guidance; unavailable or expired configuration does not falsely claim access was revoked.

Passed applicable software gates:

- Windows/Android aggregate tests, Go vet/build and installer/release contracts. Refreshed Windows gate after final review passed all 40 Client frontend tests.
- Android: 293 tests, zero failures/errors/skips; lint zero errors with 18 existing warnings; debug APK build.
- Native iOS: 366 tests, two hardware-dependent skips, zero failures, warnings-as-errors and Xcode package tests; unsigned iPhoneOS/simulator app and compatibility-extension builds.
- Native Apple Silicon Go 1.26.7: complete Mobile tests/vet/race, both gateway modules tests/vet/race/build and unsigned arm64 Mac daemon/GUI builds with minimum macOS 13.0. Final gateway wire fixes were refreshed and passed uncached native checks.
- Backend: 189 suites / 2,339 tests passed, 13 opt-in suites / 131 tests skipped. Root contracts/backend/UI build passed. Focused UI and existing accounting/no-Mobile-usage regressions passed; central-query-hook correction was followed by 47 relevant tests and another full build.
- Terraform fmt/validate plus two offline provider-mocked off/on plans; scoped registration/drain tests, existing scaling/release contracts and Bash parsing. Bounded SNI fuzzing completed 43,855 executions.
- Manifest/schema, changed-document relative links and whitespace checks. Native input hashes and local evidence paths are recorded in `docs/hosted-acceptance.md` and sibling implementation reports.

Validation limits: four real PostgreSQL concurrency tests could not run without a local database (Docker Desktop API unavailable). Signed Windows/Mac installation, physical Android/iPhone cellular/NAT64/lifecycle/keep-awake, actual enabled AWS/deployment/node-loss/certificate-renewal, sustained one-/ten-Client capacity/thermals and combined Core load within the agreed 5% bound remain explicit acceptance blockers. Mocked infrastructure and socket/unit tests do not satisfy these unchecked gates. Nothing was deployed, merged or released.

Execution deviations: existing feature-branch checkouts were used as requested. Native verification used isolated hashed snapshots and existing Mac toolchains. Disk exhaustion interrupted two new files; both were recovered from recorded patches and retested, with regenerable caches/build output moved or redirected to G:. Existing source, pairing/signing material and benchmark evidence were preserved. No new frontend architecture was introduced: contract-backed server state remains in Inevitable's `ui/src/api/queries.ts`, with thin routes and feature-owned panels. `ai-instructions.txt` records the actual patterns.
