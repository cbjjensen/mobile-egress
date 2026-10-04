---
name: mobile-egress-mobile-parity
description: Use when adding or changing user-facing Android or iOS Mobile Egress Agent behavior, mobile parity evidence, mobile component gates, or cross-platform Agent documentation.
---

# Mobile Egress mobile parity

## Core invariant

User-facing mobile Agent capabilities are cross-platform unless the owner explicitly approves an exception recorded in a tracked decision. Android and iOS may use different native mechanisms, but the repository manifest must show the actual behavior with tracked source and test evidence.

## Workflow

1. Read `docs/mobile-feature-manifest.json` before changing Android or iOS Agent behavior.
2. Update both platform entries for a changed feature. Use `native-equivalent` only when the platform mechanism differs, and write the equivalence note in terms of user-visible behavior.
3. Cite only tracked files that exist in the current tree. Do not cite planned files, generated intentions, local-only artifacts, simulator/device notes, secrets, QR payloads, or raw addresses.
4. Run `& .\scripts\validate-mobile-feature-manifest.ps1` before claiming parity. The full, Android, and iOS gates also run it.
5. When an existing owner-approved decision permits different behavior, use schema 2 `approved-exception` with `exceptionNotes`, tracked `decisionEvidence`, and real source/test evidence. Preserve that approval across tasks; do not request it again. Without such a decision, keep the gap in the plan and ask for direction instead of claiming parity or inventing an exception.

## Quick reference

| Situation | Manifest action |
|---|---|
| Same behavior and same mechanism | `implemented`; cite source and tests. |
| Same behavior through platform-native mechanism | `native-equivalent`; include `nativeEquivalenceNotes`. |
| Explicitly approved difference | `approved-exception`; include `exceptionNotes`, `decisionEvidence`, source and tests. Do not attach an equivalence note. |
| Android-only or iOS-only work in progress | Do not mark parity complete; keep it in the implementation plan until both sides have tracked evidence. |
| Evidence moved or renamed | Update the manifest in the same change. |

## Common mistakes

- Treating Android behavior as enough for mobile parity.
- Using `native-equivalent` to hide missing behavior rather than explain a real platform mechanism.
- Describing iOS keep-awake as background execution. The approved direct-mode lifecycle exception is documented in `docs/superpowers/plans/2026-10-03-direct-client-phone.md`: Android foreground-service background sharing, iOS active-app sharing only.
- Citing future task files before they are tracked.
- Depending on prose reminders alone; the validator is the enforcement boundary.
