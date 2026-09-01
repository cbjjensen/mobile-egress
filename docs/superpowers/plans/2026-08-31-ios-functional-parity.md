# iOS Functional Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILLS: use `superpowers:subagent-driven-development`, `superpowers:test-driven-development`, and `.agents/skills/mobile-egress-mac-build-server` as applicable. Work only in the existing `codex/ios-agent` worktree.

**Goal:** Make the iOS/iPadOS Agent functionally equivalent to the Android Agent for user-facing behavior while using supported Apple-native mechanisms.

**Spec:** `docs/superpowers/specs/2026-08-31-ios-functional-parity-design.md`

## Global constraints

- Preserve current relay/wire interfaces and the existing Apple security and transport boundaries.
- Write failing behavior tests before production code.
- Keep temporary public addresses out of clipboard output and logs.
- Do not mutate Apple signing, provisioning, account, TestFlight, or Mac software state.
- Verify the exact clean commit through the repository Mac-build-server skill.

## Task 1: Parity contract and documentation guard

- Add a schema-v1 machine-readable mobile feature manifest with unique feature IDs, per-platform status, native-equivalence notes, and tracked source/test evidence.
- Add a PowerShell behavior validator and tests for missing evidence, duplicates, unsupported statuses, invalid native-equivalence notes, and single-platform implementation.
- Wire it into full and mobile component gates.
- Add and pressure-test a repository skill for future cross-platform mobile feature work.

## Task 2: Portable Apple rotation and presentation domain

- Add `PublicIPSnapshot` and the complete rotation state/event/effect/result/failure/transition/reducer model.
- Cover availability, active-stream confirmation, 10/30-second paths, two/three-minute timeouts, cancellation, stale/duplicate events, early return, comparison, recovery, and tunnel-resume failure.
- Add branding constants, safe status formatting/redaction, and a pure OLED presentation model with separate cellular and relay health.

## Task 3: Apple platform adapters and lifecycle

- Add the cellular-only dual-family `CellularPublicIPProbe` with bounded strict parsing and sanitized finite logging.
- Add the main-actor rotation coordinator, cellular path observation, foreground resume, App Group checkpoint, first-use notification cue, and tunnel pause/resume support.
- Publish cellular health and rotation actions through `AgentViewModel`.

## Task 4: OLED UI and generated assets

- Extend the checked-in brand generator to emit iOS AppIcon/header assets from the selected ZFNF source.
- Replace the root SwiftUI `List` with the accessible OLED dashboard, system confirmations, rotation guidance/controls, and diagnostic copy action.
- Preserve Dynamic Type, VoiceOver, reduced motion, and native navigation behavior.

## Task 5: Mac verifier

- Make `-UseMacBuildServer` run Swift/Xcode verification directly on the Mac without a Windows Docker prerequisite.
- Retain Docker for Windows-only portable verification and exact-commit disposable checkout behavior.
- Retry the final Xcode package-test launch once only for known `com.apple.testmanagerd.control` invalidation.

## Task 6: Verification and acceptance handoff

- Run parity-validator tests, full repository gate, Android unit/lint/APK checks, Swift suites with warnings as errors, unsigned iPhoneOS build, and Xcode-hosted package tests.
- Perform exact-clean-commit Mac verification. Treat persistent `testmanagerd` failure as a failed environment.
- Update Apple documentation and the physical-iPhone acceptance record for active-stream rotation, 10/30-second outcomes, cancellation, foreground recovery, and no-Wi-Fi fallback.
