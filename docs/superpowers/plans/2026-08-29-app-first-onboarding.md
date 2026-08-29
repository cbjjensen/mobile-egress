# App-First Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one Windows application own a manually installed relay, pair an Android agent by QR code, and proxy selected Windows applications without normal-use scripts.

**Architecture:** The Windows DPAPI store holds independent Owner and Client identities. Owner bootstrap enrolls the supplied Owner invitation, self-issues a Client invitation, and enrolls it locally. Android receives only a short-lived Agent QR code and uses the existing strict pairing/enrollment path.

**Tech Stack:** Go 1.26, Wails v2, React/TypeScript, Kotlin, Jetpack Compose, CameraX, ZXing, Android Keystore, Windows DPAPI.

**Spec:** `docs/superpowers/specs/2026-08-29-app-first-onboarding-design.md`

## Global Constraints

- Keep the relay manually installed on owner-operated EC2; do not add AWS credentials or provisioning to the Windows app.
- Preserve certificate-backed enrollment, TLS pinning, loopback-only SOCKS, and cellular-only Android routing.
- Never persist, log, display, or copy raw Agent invitations after QR issuance.
- Keep phone IP rotation out of scope.
- Use test-first development for application behavior and run the full repository verification gate before completion.

---

### Task 1: Dual Windows identity persistence and bootstrap

**Files:** `windows-client/internal/client/*` and its Go tests.

- [ ] Write focused failing tests for Owner bootstrap creating a Client identity, retrying Client setup after a failure, and migrating existing single-role identity state.
- [ ] Add independent Owner and Client identity persistence to the DPAPI generation format, including backward-compatible migration.
- [ ] Implement Owner bootstrap, Client setup retry, Client-only proxy use, and Owner-only issuance/revocation in the core.
- [ ] Run the focused Go tests, then `go test ./...`.

### Task 2: Windows setup and Agent QR experience

**Files:** `windows-client/internal/desktop/*`, `windows-client/frontend/src/*`, and Windows client tests.

- [ ] Write failing core/desktop tests for redacted setup status and QR issuance expiry behavior.
- [ ] Add Wails bindings for Owner bootstrap, Client setup retry, and Agent QR issuance that return only image data and expiry metadata.
- [ ] Replace role-separated UI with setup, phone QR, proxy, and owner-control states; remove raw Agent invitation display and clipboard actions.
- [ ] Update tray state to use Client readiness for proxy actions.
- [ ] Run Go tests plus frontend typecheck and production build.

### Task 3: Android QR scanner pairing

**Files:** `android/app/build.gradle.kts`, Android manifest, UI/view-model/scanner classes, and JVM tests.

- [ ] Write failing JVM tests for scanner input pairing, invalid/expired rejection, and clearing scanned data before enrollment.
- [ ] Add CameraX and QR decoding dependencies, camera permission, and a lifecycle-aware scanner screen.
- [ ] Route a decoded value directly into the existing strict pairing path; remove the editable pairing-bundle field.
- [ ] Present generic, redacted scanner and enrollment failure states.
- [ ] Run Android unit tests, lint, and debug assembly.

### Task 4: Documentation and full verification

**Files:** `README.md`, `docs/deployment.md`, `docs/operations.md`, and documentation above.

- [ ] Document the one-time relay-administrator handoff and the ordinary no-script Windows/Android flow.
- [ ] Document manual application installation, Agent QR pairing, and the intentionally unsupported IP rotation behavior.
- [ ] Run `scripts/test-all.ps1`, inspect the debug APK, and perform the physical-device checklist.
