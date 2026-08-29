# Mobile Egress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a portable relay, Windows tray SOCKS client, and Android cellular egress agent for selective personal mobile proxying.

**Architecture:** A Go relay persists enrollment and CA state, validates public destinations, and joins authenticated streams. A Windows Wails app hosts the loopback SOCKS5 endpoint; a Kotlin Android app maintains a user-visible, cellular-bound foreground tunnel.

**Tech Stack:** Go 1.26, SQLite, WebSocket/TLS, Docker Compose, Wails v2, React/TypeScript, Kotlin, Jetpack Compose, Gradle.

**Spec:** `docs/superpowers/specs/2026-08-29-mobile-egress-design.md`

## Global Constraints

- Keep this project independent from Inevitable Proxies production systems.
- Permit only one active Android agent, four streams per Windows client, and eight streams agent-wide by default.
- Support SOCKS5 `CONNECT` only, with local loopback binding and authentication.
- Block non-public destinations at relay and Android boundaries.
- Never persist destinations, payloads, proxy credentials, pairing codes, or private keys.
- Android must bind relay and target traffic to cellular and must not fall back to Wi-Fi.
- Use documentation-first updates whenever behavior or operations change.

---

### Task 1: Relay domain primitives

**Files:** `go.mod`, `relay/internal/policy/*`, `relay/internal/enrollment/*`, `relay/internal/protocol/*`, and matching Go tests.

- [ ] Write failing Go tests for public-address validation, one-use/expiry pairing codes, certificate revocation, and protocol envelope validation.
- [ ] Implement minimal policy, enrollment, certificate, and protocol packages until the tests pass.
- [ ] Run `go test ./...` and commit the task.

### Task 2: Relay service and Docker deployment

**Files:** `relay/cmd/relay/*`, `relay/internal/service/*`, `relay/Dockerfile`, `deploy/docker-compose.yml`, and tests.

- [ ] Write failing HTTP/WebSocket service tests covering readiness, enrollment rejection, role checks, stream limits, and redacted metrics.
- [ ] Implement the relay command, persistent state, TLS/config validation, session lifecycle, and Docker deployment.
- [ ] Run relay tests/build and commit the task.

### Task 3: Windows local SOCKS engine and application shell

**Files:** `windows-client/*` and matching tests.

- [ ] Write failing tests for SOCKS authentication, `CONNECT`, loopback-only bind, unsupported-command rejection, and local credential persistence abstraction.
- [ ] Implement the Go SOCKS engine and relay session client.
- [ ] Add Wails desktop/tray screens for pair, start/stop, status, proxy-line copy, and owner controls.
- [ ] Run Go/frontend tests and commit the task.

### Task 4: Android cellular agent

**Files:** `android/*` and unit tests.

- [ ] Write failing Kotlin tests for pairing state, public-address verification, cellular-required behavior, and foreground state transitions.
- [ ] Implement Compose screens, secure key storage, pairing flow, WebSocket session, cellular-bound socket adapter, and foreground service.
- [ ] Run Android unit tests when JDK 17 and SDK prerequisites are available; otherwise run Gradle wrapper/version validation and document the blocker.
- [ ] Commit the task.

### Task 5: Packaging, operations, and integration checks

**Files:** `scripts/*`, deployment docs, release docs, and test fixtures.

- [ ] Add reproducible checks for Go, Node/WebView2, JDK/Android SDK, Docker, and signing-secret absence.
- [ ] Add Windows installer and signed-APK release instructions that never commit secrets.
- [ ] Add physical-device end-to-end runbook and final quality checks.
- [ ] Run all locally available test suites and commit the task.
