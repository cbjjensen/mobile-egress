# Bring Android’s Visual Design to iPhone

Approved 2026-10-05. Work on main and preserve concurrent changes. Android is the visual reference; use the owner's selected simpler iPhone dashboard. Keep connection, pairing, storage, protocols, credentials, entitlement and sharing behavior unchanged.

## Design and implementation checklist

- [x] Match Android's black background, dark rounded cards, mint primary actions, colored status badges, subtle borders, typography and existing ZFNF branding. Use 20-point outer spacing, generous padding, 44-point minimum actions, Dynamic Type, VoiceOver, increased contrast and reduced motion.
- [x] Arrange header/status, full-width Start/Stop, iPhone availability/keep-awake, current-session activity, Client cards/Add Client, then secondary rotation/status/update tools. Empty installations show Pair your first computer instead of activity.
- [x] Add a pure current multi-Client presentation model; do not use the legacy singleton presenter as live state. Distinguish stopped, connecting, connected, partially connected and attention states using real lifecycle, cellular observations and authenticated sessions. Stop remains accessible during busy operations/retries.
- [x] Move scan/paste/file import into an Add Client sheet, with scan primary and manual actions under Other options. Client sheets contain enable/disable, retry, connection details, update import and confirmed removal. Keep errors contextual and retain a dashboard notice after dismissal.
- [x] Keep rotation compact until active, then expose instructions/countdown/cancellation and existing confirmations. Preserve one AgentViewModel/scene owner; in-app sheets do not recreate or stop the runtime.
- [x] Display only existing current-session connection/transfer counters. No persistent history, usage reporting, billing or quotas.
- [x] Add tests for empty/pending/stopped/connecting/connected/mixed/disabled/removal/cellular/recovery presentation, aggregation, action guards, errors and one-time import delivery.
- [x] Render actual SwiftUI on small/large iPhone layouts and accessibility text, compare to Android, and inspect screenshots. Preview fixtures remain isolated from production dependencies and secure storage.
- [x] Run native Swift tests, iPhone/simulator builds, project checks and manifest validation.
- [ ] Physical acceptance: sheet-preserved traffic, scanning/manual/file imports, Client controls, rotation, safe copying, Stop/lock/background/keep-awake and VoiceOver on an actual signed iPhone installation. Device unavailable; no claim of physical completion.
- [x] Update iOS guidance and manifest evidence from the live screen, correcting the obsolete visual-parity claim. Preserve the approved lifecycle exception and synchronize Inevitable's pilot record.
- [x] After verification, create a signed TestFlight build using existing identities and next unused build number, preserving tester access and external-review requirements. No Android redesign, website changes, stable promotion or infrastructure deployment.

## Interfaces and acceptance

Only presentation snapshots/types and SwiftUI components are added. Existing service, repository, lifecycle and protocol interfaces remain authoritative. Successful signing/building alone does not establish visual or physical acceptance. Native renders and behavior tests are required before delivery; physical limitations remain explicit.

## Execution record

Baseline `3d0f20f` includes the newer chunked-response pairing fix and build 6. Working tree was clean when implementation began. Preserve this fix throughout archive/delivery. The approved plan is the implementation authority. Use the existing main checkout as explicitly requested; no new worktree is required.

Ruling: build the live screen from a new direct-runtime presenter, retaining legacy presentation helpers only for existing compatibility tests. UI snapshots carry no invitation, certificate, credential or endpoint secrets. Metrics describe currently retained sessions and reset when those sessions end.

### Implementation checkpoint

Implemented the pure direct-runtime presenter and 12 native regression tests, reusable visual components, dashboard composition, Add Client/details sheets, contextual errors, bounded file reading, synchronous import admission and one-shot code delivery. The live view model and scene owner remain single instances. No Android runtime/UI, protocol, storage, gateway or entitlement changes.

Native tests were red before presenter implementation and before contextual-error support, then green. The full native suite passes 390 tests, zero failures, with two existing opt-in physical Secure Enclave/entitled Keychain tests skipped. Project source registration checks pass. The simulator app and cleanup extension build successfully. Initial visual review found narrow three-column metrics; changed to a connections row plus two transfer tiles, stacking at accessibility sizes. Native UIKit-hosted SwiftUI captures replace ImageRenderer captures because the latter omit the native toggle. Tall accessibility views are captured as actual simulator viewports instead of overlarge blank snapshot surfaces.

Evidence workspace: `G:/codex-build-cache/mobile-egress-hosted-20261004/ios-visual-parity-20261005/`. Debug fixtures use sanitized values and no live dependencies; the release entry point cannot select them. Apple currently lists build 6 as the highest used number; build 7 is intended after final verification and a fresh number check.

Remaining delivery gates at this checkpoint: final rendered/accessibility inspection, independent review, manifest/documentation checks, signed device archive/export, Apple upload/readback and synchronized pilot record. No physical iPhone is attached to this workstation: real camera pairing, sheet-preserved traffic, Stop, lock/app-switch, keep-awake, VoiceOver and signed-upgrade acceptance remain explicit device checks and will not be claimed from the simulator.

### Final code and visual verification

Independent review found one P2: a Client-specific update action could update another saved Client. Fixed in one pass with red/green routing tests and a real Go-signed update regression through the existing repository: selected-Client imports pass its expected ID and cannot accept invitations. Global Add Client retains its existing pairing/update behavior. No other actionable findings or deferred minor items. Reviewer's device-dependent exclusions remain the physical acceptance blockers above; no inference of successful traffic or hardware behavior is made from screenshots.

The final native suite passes **393 tests, zero failures, two existing physical/security skips**. Final simulator build passes; mobile-manifest validation and `git diff --check` pass. Twenty full native SwiftUI/UIKit captures cover ten states at 375/440-point widths; actual iPhone 17e and iPhone 17 Pro Max simulator screenshots cover the viewport, accessibility text and increased contrast. Activity and long recovery messages expand without clipping; native toggle rendering is verified. Invalid preliminary ImageRenderer/oversized snapshots are not acceptance evidence. Final captures live in `visual-parity-native/`, with `activity-accessibility-contrast.png` and `client-accessibility-contrast.png` alongside it. No motion is required, and no custom animation was introduced.

Android's existing theme and presentation tests/evidence were rechecked as the unchanged reference. The manifest now cites the live direct presenter/components and new tests instead of the unused legacy presenter. Version 2.0.0/build **7** was selected after an independent App Store Connect read showed 6 as the highest used build. Signing/delivery follow from this verified code checkpoint.

### Signed build and delivery

Exact source `de8e9ab014d1edd68d7071f74da6c0587458687a` produced signed iPhone **2.0.0 (7)**. Native Release archive/export passed with the existing Apple Distribution certificate and both existing manual profiles. Both app/cleanup-extension signatures, identities, Keychain/App Group/Network Extension capabilities, versions, privacy manifests and external-capable metadata verify. Debug-only fixture selection is absent from the release executable. IPA SHA-256: `de4534d8f9b759da958f7977b107feb613258d497f2a72fe8cb2e48a8f59cfa7`; 2,595,998 bytes.

Apple validation and upload succeeded without errors; delivery UUID `fd6c0dc5-601d-4d7e-9491-68e92a8c0ca0`. Processing/group readback is pending at this checkpoint. Six local documentation links and manifest validation pass. Existing pairings and the build-6 HTTP response fix are retained. Inevitable's production pilot record is synchronized; its runtime, architecture responsibilities and frontend patterns are unchanged, so accepted architecture and `ai-instructions.txt` require no changes for this UI-only work.

Final Apple readback confirms build 7 **VALID**, unexpired, **IN_BETA_TESTING**, with verified assignment to **ZFNF Friends Internal** and **Mobile Egress owner pilot**. Existing group membership/settings are unchanged. Users can update in place through TestFlight without intentionally deleting pairings. Actual device installation/traffic acceptance remains unverified. External state is **READY_FOR_BETA_SUBMISSION**, not approved or distributed externally. The existing build-5 external review remains **WAITING_FOR_REVIEW** and was not cancelled or altered; a separate review/distribution step is still required for build 7. Physical acceptance above and external approval are the remaining release blockers. The two capture simulators were shut down after restoring their contrast settings.
