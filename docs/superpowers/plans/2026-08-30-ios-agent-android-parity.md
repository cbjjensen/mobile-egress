# iOS Agent Android-Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Use test-driven development for production behavior and record macOS-only verification that cannot run on Windows.

**Goal:** Build a native iOS 17+ Mobile Egress Agent suitable for TestFlight that preserves the Android agent's enrollment, trust, cellular-only, migration, and bounded tunnel behavior.

**Architecture:** A SwiftUI host app enrolls the device and manages an on-demand packet-tunnel Network Extension. A local Swift package owns strict wire parsing, Keychain/Secure Enclave identity management, pinned mTLS, Network.framework transports constrained to cellular, and bounded stream state. The relay APIs and QR formats remain unchanged.

**Tech Stack:** Swift 6, SwiftUI, Network.framework, NetworkExtension, Security, VisionKit, XCTest, Xcode 16+.

**Spec:** The user-approved `iOS Agent Android-Parity Plan` in the task conversation is authoritative; there is no separate spec file.

## Global Constraints

- Work only on branch `codex/ios-agent` in the isolated `mobile-egress-ios-agent` worktree.
- Minimum deployment target is iOS 17.0 and the first distribution target is TestFlight.
- Reuse `POST /v1/enroll`, `POST /v1/endpoint-migrations/consume`, and `GET /v1/session` without relay changes.
- Reuse strict version 1 enrollment and `agent-endpoint-migration` QR formats without wire changes.
- Require cellular paths for enrollment, migration, relay WebSocket, and target sockets; never fall back to Wi-Fi.
- Pin the QR-provided relay CA and use the enrolled client identity for mTLS after enrollment.
- Reject non-public targets at the iOS boundary.
- Bound the agent at 32 active streams and preserve finite error codes, bounded queues, tombstones, and idempotent close behavior.
- Keep private keys non-exportable in the Secure Enclave and persist identity material through Keychain access shared with the extension.
- Do not commit signing identities, provisioning profiles, Apple credentials, team IDs, or private keys.
- Write XCTest coverage before production behavior. Because this host is Windows, add and attempt the macOS `xcodebuild test` command, but do not claim it ran locally.

---

### Task 1: Swift package protocol and validation core

**Files:** `ios/Package.swift`, `ios/Sources/MobileEgressCore/{Pairing,Protocol,Policy,Session}/*`, `ios/Tests/MobileEgressCoreTests/*`.

- [ ] Add failing XCTest cases for strict unpadded base64url QR decoding, exact JSON fields, HTTPS origin validation, expiry, certificate-authority validation, migration CA mismatch, and migration recognition.
- [ ] Add failing XCTest cases for binary envelope parsing/encoding, role-compatible message types, payload bounds, finite error codes, public IP validation, the 32-stream admission limit, queue saturation, 32-entry tombstones, and close idempotency.
- [ ] Implement the minimal Foundation/Security primitives that satisfy those tests, matching Android limits and error-code vocabulary.
- [ ] Attempt `swift test`/`xcodebuild test` as available, record the Windows toolchain limitation, and commit the task.

### Task 2: Secure identity, enrollment, migration, and pinned cellular transport

**Files:** `ios/Sources/MobileEgressCore/{Identity,Network,Enrollment,Migration}/*`, matching XCTest files and fixtures.

- [ ] Add failing tests around enrollment replacement/rollback, identity retention, migration CA comparison, strict HTTP response handling, pinned trust decisions, mTLS identity injection, and required-cellular parameter construction.
- [ ] Implement non-exportable P-256 Secure Enclave key generation, PKIX public-key enrollment, certificate/metadata persistence in the shared Keychain access group, identity lookup, and old-key cleanup after durable replacement.
- [ ] Implement a bounded HTTP/1.1 client over Network.framework for enrollment and migration, with QR-provided CA pinning, optional client identity, strict response limits, and `.cellular` required interface type.
- [ ] Implement endpoint migration consumption that authenticates with the stored identity, requires byte-identical CA certificates, and persists only the relay origin change.
- [ ] Attempt the available tests, inspect for accidental secret material, and commit the task.

### Task 3: Relay WebSocket and bounded target-stream runtime

**Files:** `ios/Sources/MobileEgressCore/{Relay,Runtime}/*`, matching XCTest files.

- [ ] Add failing tests for binary-only WebSocket handling, ping/pong, open/opened/data/close flow, policy rejection, queue overflow, per-stream backpressure, duplicate close, tombstoned late frames, byte counters, and terminal session behavior.
- [ ] Implement a pinned mTLS Network.framework WebSocket connection to `/v1/session` with a required cellular path.
- [ ] Implement target connections with required cellular parameters, public-address enforcement, 32 KiB chunks, bounded per-stream queues, 30-second connect timeout, 32-stream admission, finite rejection codes, and deterministic cleanup.
- [ ] Expose a small runtime status snapshot containing connection state, active streams, uploaded/downloaded bytes, and a finite user-facing error class.
- [ ] Attempt the available tests and commit the task.

### Task 4: iOS host app, Network Extension, and Xcode project

**Files:** `ios/MobileEgressAgent.xcodeproj/*`, `ios/MobileEgressAgent/*`, `ios/MobileEgressTunnelExtension/*`, `ios/Configuration/*`, `ios/Assets/*`.

- [ ] Add the Xcode project with exactly two product targets: `MobileEgressAgent` and `MobileEgressTunnelExtension`, both consuming the local `MobileEgressCore` package.
- [ ] Add app/extension entitlements for packet tunnel, shared Keychain access, and App Group using build-setting placeholders rather than a committed Team ID.
- [ ] Implement `NEPacketTunnelProvider` lifecycle, on-demand `NETunnelProviderManager` configuration, provider status messages, and no-routes tunnel settings so Mobile Egress traffic remains the only tunneled workload.
- [ ] Implement the minimal SwiftUI app with VisionKit QR scanning, enrollment/migration routing, start/stop, connection status, active stream count, bytes up/down, and clear error states.
- [ ] Add complete Info.plists, privacy text, asset catalog placeholders/AppIcon metadata, and Debug/Release configuration files for iOS 17+.
- [ ] Run project-structure checks available on Windows, attempt `xcodebuild -list`/tests, and commit the task.

### Task 5: Controller wording, documentation, and verification entry points

**Files:** `windows-client/frontend/src/App.tsx`, `ios/README.md`, `docs/{architecture,protocol,status}.md`, `scripts/test-ios.ps1`, and related docs/tests.

- [ ] Change Android-specific owner UI wording for the protocol-compatible QR to platform-neutral Agent wording without changing the API call.
- [ ] Document macOS/Xcode prerequisites, bundle identifiers, App Group/Keychain placeholders, Apple Network Extension approval, true supervised Always-On versus on-demand TestFlight behavior, signing, TestFlight upload, and real-device acceptance steps.
- [ ] Add a macOS-oriented iOS verification entry point that runs Swift package tests and Xcode build/tests while returning an explicit unsupported-host result on Windows.
- [ ] Document real-device acceptance for enrollment, cellular-only operation while Wi-Fi is available, endpoint migration, restart/identity retention, stream limits, and clear error behavior.
- [ ] Run all existing repository checks, run the new iOS verifier as far as this host permits, review the full diff for secrets and protocol changes, and commit the task.

