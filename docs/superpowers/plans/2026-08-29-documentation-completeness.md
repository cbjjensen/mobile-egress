# Documentation Completeness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the mobile-egress documentation accurate, navigable, safe, and explicit about current implementation limits.

**Architecture:** Maintain canonical documents by responsibility: README for navigation; architecture, security, and protocol for reference; deployment and operations for procedures; component READMEs for platform behavior; CONTRIBUTING for local development; and status for validation and known limitations. Document only behavior the code/UI performs and label missing workflows as limitations.

**Tech Stack:** Markdown, PowerShell, repository source search, Go/Wails/Android/Docker Compose documentation.

**Spec:** `docs/superpowers/specs/2026-08-29-documentation-completeness-design.md`

## Global Constraints

- Documentation only: do not implement product APIs, identity discovery, CI, release automation, or other missing behavior.
- Do not publish invitations, private keys, SOCKS credentials, real relay origins, or secret-bearing output.
- Public relay traffic is TLS relay traffic, not public SOCKS; distinguish bootstrap/health from authenticated control/tunnel routes.
- Android cellular routing is socket-scoped and non-VPN; it does not control unrelated phone traffic or the default route.
- Label physical-device, carrier, Wails runtime, installer, and signed-release validation as manual acceptance where automation does not exist.
- Do not invent a recovery command for Agent revocation, Owner renewal, additional Windows Client enrollment, image rollback, or CA/state cutover.
- Preserve dated designs/plans as historical records and use relative Markdown links.

---

### Task 1: Establish navigation, terminology, status, and historical boundaries

**Files:** Create `docs/status.md`; modify `README.md`, `docs/analysis.md`, `docs/plan.md`, `docs/superpowers/plans/2026-08-29-mobile-egress.md`, and `docs/superpowers/plans/2026-08-29-app-first-onboarding.md`.

**Interfaces:** Produces the terminology, task index, and current validation/limitations document used by every later task.

- [ ] **Step 1: Inventory duplicate active-looking material.** Run `rg -n "First-time|re-pair|recovery|Current state|No existing|Task [0-9]|Validation" README.md docs android\README.md windows-client\README.md`; identify headings that must link to canonical deployment, operations, and status documents instead of repeating procedures.
- [ ] **Step 2: Turn the README into an entry point.** Add a glossary defining Owner, Client, Agent, relay administrator, Agent QR code, and local SOCKS5 proxy. Add links named Deploy the relay, Operate or recover, Understand security, Implement the protocol, Develop Windows, Develop Android, and Contribute. Correct the relay summary so authenticated Agent/Client sessions are TLS/enrolled while enrollment and read-only health are bootstrap/observability surfaces.
- [ ] **Step 3: Create `docs/status.md`.** Add a three-column validation table for `scripts/test-all.ps1`, Android JVM/debug coverage, and guarded release scripts, with the matching manual acceptance still required. Add a known-limitations table for unsupported additional Windows Client enrollment, missing Agent serial discovery/targeted UI revocation, no Owner self-service renewal, source-checkout-only Compose rollback, four Windows streams versus eight Agent/relay-wide streams, and no CI/publishing. Link each limitation to its relevant runbook/component guide.
- [ ] **Step 4: Mark historical files accurately.** Replace `docs/analysis.md` implementation-state claims with a historical notice linking to status, architecture, and current source. Add a notice to `docs/plan.md` and both completed dated plans that unchecked boxes are historical, not current work status.
- [ ] **Step 5: Validate and commit.** Run `rg -n "No existing mobile|No existing relay|No existing desktop|unchecked boxes are not current|Known limitations" README.md docs` and `git diff --check`; obsolete claims must be absent and notices present. Commit with `docs: establish canonical navigation and status`.

### Task 2: Correct system architecture and normative protocol reference

**Files:** Modify `docs/architecture.md` and `docs/protocol.md`.

**Interfaces:** Consumes Task 1 terminology/status; produces canonical system and wire references cited by security, operations, and component guides.

- [ ] **Step 1: Verify normative implementation facts.** Read `relay/internal/service/{service,session}.go`, `relay/internal/protocol/protocol.go`, `windows-client/internal/relayclient/{protocol,session}.go`, `android/app/src/main/java/com/mobileegress/agent/protocol/WireProtocol.kt`, and `android/app/src/main/java/com/mobileegress/agent/session/AgentSession.kt`. Confirm one JSON envelope per binary WebSocket message; Client-to-Relay `open` is `{host,port}`; Relay-to-Agent `open` is `{ip,port}`; Windows creates stream IDs; `/healthz` is anonymous/read-only; protected routes enforce active role in application code.
- [ ] **Step 2: Update architecture.** Describe separate Owner/Client identities, Client-only SOCKS tunnel use, Agent cellular socket binding, relay responsibilities, hard-coded service defaults rather than config knobs, and anonymous `/healthz` aggregate/non-Prometheus metrics. Link protocol for schemas, security for trust, and operations for procedures.
- [ ] **Step 3: Rewrite protocol sections.** Add an endpoint/auth/purpose table for `GET /healthz`, `POST /v1/enroll`, `POST /v1/pairing-codes`, `POST /v1/revoke`, and `GET /v1/session`. Define binary-message JSON framing, v1/version rejection, envelope/payload bounds, base64url handling, directional `open`, `opened`, `rejected`, `data`, `close`, `ping`, and `pong` rules, client-generated IDs, finite error codes, and implementation-specific tombstones. Do not claim a uniform 128-character stream-ID validator while relay/Windows accept a broader non-empty set.
- [ ] **Step 4: Add transport/recovery facts.** Document TLS 1.3, enrollment CA pinning, post-enrollment authorization, revocation rechecks, queue saturation closing streams/sessions, 30-second opening/target-connect boundaries, five-minute relay idle timeout, Android reconnect backoff, and Windows health/session behavior.
- [ ] **Step 5: Validate and commit.** Run `rg -n "length-delimited|relay creates a stream ID|client identity generated by relay|configuration values" docs/architecture.md docs/protocol.md`, `rg -n "GET /healthz|POST /v1/enroll|POST /v1/pairing-codes|POST /v1/revoke|GET /v1/session" docs/protocol.md`, and `git diff --check`. Obsolete claims must be absent and every endpoint documented. Commit with `docs: align architecture and protocol reference`.

### Task 3: Strengthen the security model and secret-handling boundaries

**Files:** Modify `docs/security-model.md` and `README.md`.

**Interfaces:** Consumes Tasks 1–2; produces security invariants used by deployment, operations, and component guides.

- [ ] **Step 1: Verify security-sensitive facts.** Read `relay/internal/service/{init,store,enrollment,session}.go`, `windows-client/internal/securestore/dpapi_windows.go`, `windows-client/internal/socks/server.go`, `windows-client/internal/client/core.go`, Android `security/PinnedTls.kt`, and `service/AgentForegroundService.kt`.
- [ ] **Step 2: Add identity/enrollment boundaries.** Define Owner-versus-Client separation, role-bound one-use short-lived enrollment capabilities stored as hashes, CA pinning, TLS bootstrap/health versus application-level active-role checks, and revocation scope. State that revocation cannot remediate copied CA/private-key or relay-state compromise.
- [ ] **Step 3: Add endpoint/local-adversary assumptions.** Explain public TLS enrollment and read-only health, no Internet-facing SOCKS, current-user-only DPAPI protection, filesystem/backup protection for relay CA and SQLite state, and loopback SOCKS as a network-exposure rather than same-user-malware boundary.
- [ ] **Step 4: Add QR/log/routing accuracy.** Require users not to screen-share or screenshot a valid Agent QR; explain regenerated display does not revoke earlier unexpired QR. Name the intentional secret-output exception for `relay init`. State Android binds its own sockets to cellular, is not a VPN, and does not alter unrelated phone traffic/default routing.
- [ ] **Step 5: Validate and commit.** Run `rg -n "Owner|Client|DPAPI|healthz|QR|not a VPN|default route|stdout|revocation" docs/security-model.md README.md`, `rg -n "BEGIN .*PRIVATE KEY|socks5://[^<]|capability=|invitation=" README.md docs windows-client\README.md android\README.md`, and `git diff --check`. No real secret-bearing example may exist. Commit with `docs: clarify security and trust boundaries`.

### Task 4: Make deployment and operations runbooks executable and honest

**Files:** Modify `docs/deployment.md` and `docs/operations.md`.

**Interfaces:** Consumes Tasks 1–3; produces task-oriented operator procedures plus a clear escalation boundary.

- [ ] **Step 1: Verify operational surface.** Read `deploy/docker-compose.yml`, `deploy/.env.example`, `scripts/{build-relay-image,build-windows,release-android,operations-common}.ps1`, `windows-client/internal/desktop/run_windows.go`, `windows-client/internal/client/core.go`, and `windows-client/frontend/src/App.tsx`.
- [ ] **Step 2: Restructure deployment.** Add relay host readiness: Docker/Compose, restricted persistent state, DNS/public-name and HTTPS-origin match, TLS ingress/firewall, and port 8443 as relay TLS—not SOCKS. Make CA/state custody and backups explicit. Describe source-checkout Compose accurately; do not provide a non-existent image-tag rollback. State that state/CA cutover and lost/expired sole Owner bootstrap need relay-administrator intervention when UI recovery is absent.
- [ ] **Step 3: Define artifact/device acceptance.** Make `scripts/release-android.ps1` the guarded signed Android release path; direct `assembleRelease` is insufficient for distribution. State Windows produces an NSIS installer but has no signing/publishing workflow. Require artifact version/hash plus manual device acceptance and link status.
- [ ] **Step 4: Build daily-use/recovery decisions.** Document startup order, tray/quit behavior, Android foreground start/no boot restart, health interpretation, and states for relay down, Agent offline, cellular unavailable, revoked/expired identity, Owner-ready/Client-missing, and Client replacement. Specify supported Client recovery: record serial, revoke with Owner authority, Replace Client, verify SOCKS. State routine Agent re-pair does not revoke old Agent and lost-phone targeted revocation is unavailable in shipped UI.
- [ ] **Step 5: Correct QR/capacity claims.** State QR replacement changes display only. Correct acceptance: one Windows Client supports four streams; eight is relay/Agent capacity and the shipped UI does not provide additional-Windows Client enrollment.
- [ ] **Step 6: Validate and commit.** Run `rg -n "additional Windows|eight active|rollback.*image|revoke the Android|new code.*replace" docs/deployment.md docs/operations.md`, `rg -n "not supported through the shipped UI|requires relay-administrator intervention|four local streams" docs/deployment.md docs/operations.md`, and `git diff --check`. Unsupported steps must be labeled. Commit with `docs: harden deployment and operations runbooks`.

### Task 5: Align Windows and Android component documentation

**Files:** Modify `windows-client/README.md` and `android/README.md`.

**Interfaces:** Consumes Tasks 1–4 and links canonical policies rather than duplicating them.

- [ ] **Step 1: Verify platform behavior.** Read `windows-client/internal/{socks/server.go,desktop/run_windows.go,client/core.go}`, `windows-client/frontend/src/App.tsx`, Android `AndroidManifest.xml`, `ui/MainActivity.kt`, `service/AgentForegroundService.kt`, `app/build.gradle.kts`, `scripts/release-android.ps1`, and `scripts/operations-common.ps1`.
- [ ] **Step 2: Update Windows guide.** Document distinct DPAPI Owner/Client identities, Client-only traffic, IPv4 loopback authenticated SOCKS5, CONNECT-only behavior, four local streams, tray behavior, Owner-ready/Client-missing retry, Client serial visibility, Owner-authenticated Replace Client, and unsupported additional-Windows app-first enrollment. Link security and operations rather than duplicate their policy.
- [ ] **Step 3: Update Android guide.** Add a permissions-purpose table, user-action camera request, QR values never rendered/copied/persisted while allowing transient enrollment memory, foreground start, `START_NOT_STICKY`, no boot receiver, task-removal/Stop behavior, cellular socket binding, non-VPN/default-route scope, and SDK resolution order `ANDROID_HOME`, `ANDROID_SDK_ROOT`, `local.properties`. Make `scripts/release-android.ps1` canonical.
- [ ] **Step 4: Consolidate device validation.** Keep a compact component checklist and link the canonical combined checklist in deployment. Identify camera/permission, carrier egress, Wi-Fi-present cellular loss, lifecycle, signed install, and stream fairness as device-only.
- [ ] **Step 5: Validate and commit.** Run `rg -n "Client only|127\.0\.0\.1|CONNECT|four|not supported|START_NOT_STICKY|not a VPN|ANDROID_SDK_ROOT|release-android" windows-client\README.md android\README.md` and `git diff --check`. Commit with `docs: align platform guides with runtime behavior`.

### Task 6: Add contributor guidance and validate the documentation set

**Files:** Create `CONTRIBUTING.md`; modify `README.md` and `docs/status.md`.

**Interfaces:** Consumes canonical map/commands from Tasks 1–5; produces reproducible contributor onboarding and final validation evidence.

- [ ] **Step 1: Verify contributor facts.** Read `scripts/{preflight,test-all,test-operations-scripts}.ps1`, `windows-client/wails.json`, `windows-client/scripts/dev.ps1`, `go.mod`, `android/app/build.gradle.kts`, and `.gitignore`.
- [ ] **Step 2: Write CONTRIBUTING.** Include repository map, documentation authority, Windows-versus-cross-platform scope, Go 1.26+/Node 22+/JDK 17+/SDK 35 prerequisites, `npm ci` in `windows-client/frontend`, SDK lookup precedence, `JAVA_HOME` precedence, validation matrix, generated/ignored files, Wails first-run network fetch, signing secret hygiene, and requirement to update source/tests/canonical docs together for cross-component protocol/security changes.
- [ ] **Step 3: Link contributor/status guidance.** Add Contribute and Current validation and limitations links to README. Do not call `scripts/test-all.ps1` release/physical-device proof or promise CI/automatic publishing.
- [ ] **Step 4: Check local Markdown targets.** Run `rg --files -g '*.md'` and inspect every changed relative Markdown link with `Test-Path` resolved from its containing file. Do not add a dependency; correct any broken target or use the correct relative path.
- [ ] **Step 5: Run final checks.** Run `rg -n "No existing mobile|No existing relay|No existing desktop|length-delimited JSON|public SOCKS|system-wide VPN" README.md docs android\README.md windows-client\README.md CONTRIBUTING.md`, `rg -n "BEGIN .*PRIVATE KEY|socks5://[^<]|capability=|invitation=" README.md docs android\README.md windows-client\README.md CONTRIBUTING.md`, `git diff --check`, then set `JAVA_HOME`, `ANDROID_HOME`, and `ANDROID_SDK_ROOT` to the installed JDK17/SDK35 paths and run `powershell -NoProfile -File scripts\test-all.ps1`. Forbidden stale/secret patterns must be absent and the application gate must pass.
- [ ] **Step 6: Commit final contributor/validation docs.** Commit with `docs: add contributor and validation guidance`.
