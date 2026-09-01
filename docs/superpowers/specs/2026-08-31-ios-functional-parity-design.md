# iOS Functional Parity Design

**Status:** Approved for implementation on 2026-08-31.

## Goal

The Apple Agent must expose every user-facing Android Agent capability, using an Apple-native equivalent when iOS does not permit the same mechanism. This increment adds guided cellular-IP rotation, ZFNF branding, an accessible OLED dashboard, safe diagnostics, equivalent cellular and relay visibility, and an automated guard against future platform drift.

## Non-goals and constraints

- Relay APIs, enrollment and migration QR formats, provider error codes, and the WebSocket protocol do not change.
- Existing mTLS, Secure Enclave, shared Keychain, cellular-only transport, public-target policy, 32-stream bound, and bounded queues remain intact.
- iOS 17+, Swift 6, Xcode 16+, the project Mac build server, and TestFlight remain the supported Apple baseline.
- Functional equivalence is required; identical Android mechanics and pixel-perfect UI cloning are not.
- No signing, provisioning, Apple-account, TestFlight, or App Store state is changed by this work.

## Parity contract

A versioned repository manifest identifies every user-facing mobile feature. Each Android and iOS entry must use `implemented` or `native-equivalent`, cite tracked source and test evidence, and explain native equivalence when selected. A validator rejects duplicate IDs, missing or untracked evidence, invalid statuses, and one-platform-only capabilities. The validator runs in the full repository gate and the mobile component gates.

## Apple presentation

The Apple app uses the ZFNF names `ZFNF Mobile Egress`, `ZFNF Mobile Egress Agent`, `ZFNF MOBILE EGRESS`, and `ZFNF Mobile Egress status`. The checked-in ZFNF logo source generates the iOS AppIcon and header asset alongside existing generated assets.

The SwiftUI root becomes an OLED dashboard with a branded header, pairing card, distinct cellular and relay tiles, stream and byte metrics, finite error presentation, Agent action, rotation controls, and safe diagnostic-copy action. Native navigation, Dynamic Type, VoiceOver, reduced motion, and system confirmation dialogs are preserved. A pure core presentation model owns labels, badges, tones, and action availability so those decisions are testable without SwiftUI.

## Guided cellular-IP rotation

Portable reducer types own rotation phases, events, effects, results, failures, stale-event rejection, and countdown/timeout policy. Rotation is offered only while the Agent is enrolled and running, cellular is available, and no attempt is active. Active streams require confirmation. Normal attempts use a 10-second hold; retries after an unchanged result use 30 seconds. Cellular loss and return time out after two and three minutes respectively.

`CellularPublicIPProbe` uses concurrent IPv4 and IPv6 `NWConnection` requests with cellular required and Wi-Fi/wired prohibited. It uses system TLS, an eight-second timeout, a 128-byte response limit, strict literal validation, and family isolation. Changed/unchanged/unverified compares only families that succeed both before and after.

A main-actor coordinator pauses the packet tunnel and on-demand reconnect, observes cellular path loss and return, guides the user to Control Center without private URLs or false automation claims, resumes observation when the app activates, and restores the Agent after completion, cancellation, or recoverable failure. A five-minute App Group checkpoint prevents suspension from stranding the Agent; it is terminally cleared and never copied or logged. Notification permission is requested on first rotation use only, and denial remains non-blocking.

## Status, diagnostics, and logging

Cellular availability is tracked independently of relay connection. Safe copied status contains branding, enrollment, Agent state, cellular and relay state, stream/byte totals, finite error class, and rotation phase/result. It excludes public addresses, relay origins, certificates, capabilities, and raw errors.

Unified logging records only finite probe and relay classifications, plus HTTP status when present. It never records exception messages, hostnames, URLs, or addresses.

## Verification architecture

On Windows, `scripts/test-ios.ps1 -UseMacBuildServer` sends the exact committed tree to a disposable detached checkout and runs both Swift suites, an unsigned iPhoneOS app/extension build, and Xcode-hosted package tests directly on the Mac without first requiring Docker. Windows-only portable verification continues to require Docker. Only the final Xcode package-test launch may retry once, and only for `com.apple.testmanagerd.control` invalidation.
