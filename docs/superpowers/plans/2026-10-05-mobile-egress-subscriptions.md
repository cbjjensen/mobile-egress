# Mobile Egress subscriptions, R2 downloads and iPhone pilot

Approved 2026-10-05; implement on main, preserving existing work. The corresponding executable plan, analysis and validation record are in sibling Inevitable `technical-requirement-docs/2026-10-05-mobile-egress-subscriptions/`.

## Approved behavior

- One account subscription, monthly/yearly USD. Admin prices start unset; sales stay closed. No new seats, traffic reporting, data quota, proxy allocation or changes to commercial proxy accounting.
- Reuse existing software billing. Verified paid invoices grant hosted access; pilot grants remain independent. Preserve all activation/gateway authorization checks, saved pairing and Advanced direct. Period-end cancellation retains paid access. No billing-period switch until the paid term ends.
- Mirror verified signed Android APK, Windows EXE and Apple Silicon Mac PKG through existing public R2 under mobile-egress/<version>/. Never change historical releases, signers or Order Tracker objects. Missing compatible artifacts stay unavailable; pilot is not stable.
- Admin-approved free pilot accounts supply TestFlight invitation email/consent. Backend automation enrolls only the configured external group, using an explicit compatible approved build. No Apple login/password/team role, paid-pilot coupling, delivery claims or unrelated membership changes.
- Focused website panels expose subscription/billing, platform downloads, iPhone enrollment and computer management. Preserve Android background versus iOS active/open/unlocked lifecycle guidance.

## Checklist

- [x] Save synchronized plan before code.
- [x] Shared contracts, billing/access and lifecycle tests.
- [x] Customer/Admin panels, pricing and downloads.
- [x] Durable TestFlight signup/invitation/recovery.
- [x] R2 publisher integrity tests and compatible signed Android preparation.
- [x] Accepted architecture, operations, configuration and frontend-convention documentation.
- [x] Billing/backend/frontend/release/manifest validation and independent review; external rollout gates recorded below.

Implementation does not authorize production deployment, opening sales or stable promotion. Record signed artifact, Apple review and physical-device blockers explicitly. Final evidence and rulings must be synchronized here and in the sibling implementation summary.

## Implementation evidence, 2026-10-05

The R2 publisher and credential-safe PowerShell wrapper are implemented. Fourteen offline publisher tests and the wrapper tests pass, covering immutable conflicts, interrupted uploads, exact source/hash provenance and public-byte verification before catalog promotion. Independent review found no remaining publisher findings. Existing Windows/Mac v2.0.0 frozen bytes remain unchanged; the publisher selects platforms explicitly and never inherits an unverified download.

Android is frozen as a separate 2.0.1 release, versionCode 25, using the established signer. The full guarded Android release gate passed, including 316 unit tests, lint, debug/release assembly, manifest, release contracts and signer verification. The version-sensitive release test was updated alongside the version bump. Local tag `v2.0.1` identifies source `f0751a03ca35587e9f7b07e158f9325f81dc36c7`; no asset was appended to immutable v2.0.0. APK `zfnf-mobile-egress-android-2.0.1.apk` is 11,864,967 bytes with SHA-256 `ce548dbbd4fb8b7d127b49fd51a5928ad165a41c8e7178905a432afee07f2efa`. Signer SHA-256 `b628ab4d8053ebf012e4e270c0b9f22ab1c1abceb2421cd8f51a493805509eb7` matches the established identity. Frozen bytes, record, log and test evidence are preserved under `G:/codex-build-cache/mobile-egress-hosted-20261004/android-2.0.1-freeze/`. A disk-full interruption was resolved by moving only inventoried generated output to that drive using the existing junction procedure; signing inputs and source were preserved.

The sibling Inevitable implementation has passed a full workspace build and 608 frontend tests. The safe local billing baseline passed four checks with external Discord calls disabled and no orders created. Real PostgreSQL tests cover billing/pilot behavior and signed webhook regressions. Independent review repairs cover uncertain Checkout recovery, existing proxy refund isolation, generic customer errors, lost-dispute scope, a concurrent first-invoice/refund race, resend readiness, exhausted retries and outbox fairness. Both accepted architecture documents, environment examples, operations and frontend conventions are updated. Final source test totals are recorded in the sibling implementation summary and synchronized below.

## Rollout and remaining external acceptance

No new GitHub release, R2 object, production deployment or invitation was published by this implementation. The new Android tag remains local. R2 mirroring is implemented and tested offline; public links remain unset until the frozen Android release is published through the guarded path and all selected platform bytes are mirrored/verified. Historical signed Desktop v2.0.0 artifacts are unchanged. Prices remain unset, sales closed and new invitations disabled.

Live Stripe test-mode Checkout/portal verification, R2 conditional-write/CDN verification, the exact compatible externally approved Apple build, and physical signed installation/pairing/HTTP/CONNECT/SOCKS acceptance remain rollout gates. No new Mac/iOS runtime behavior was introduced; the mobile manifest's explicit iOS active-foreground lifecycle exception is unchanged. Closing sales/invitations is the rollback; preserve reconciliation, existing access and immutable downloads.

Final source validation: Inevitable backend **201 suites / 2,458 tests passed**, 12 environment-gated suites / 128 tests skipped; frontend **64 suites / 608 tests passed**, followed by 21 Mobile page tests after visual field styling; full workspace build passed. Thirty-six local documentation links and the mobile feature manifest passed. The complete three-platform R2 dry-run verified the actual Desktop 2.0.0 and Android 2.0.1 freeze/tag/hash evidence without remote requests. Independent billing, pilot and publisher reviews have no remaining actionable findings. Source phases are separately committed; the synchronized Inevitable implementation summary records commit IDs, review repairs and external gates.
