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
- [ ] Shared contracts, billing/access and lifecycle tests.
- [ ] Customer/Admin panels, pricing and downloads.
- [ ] Durable TestFlight signup/invitation/recovery.
- [ ] R2 publisher integrity tests and compatible signed Android preparation.
- [ ] Accepted architecture, operations, configuration and frontend-convention documentation.
- [ ] Billing/backend/frontend/release/manifest validation and independent review.

Implementation does not authorize production deployment, opening sales or stable promotion. Record signed artifact, Apple review and physical-device blockers explicitly. Final evidence and rulings must be synchronized here and in the sibling implementation summary.

## Implementation evidence, 2026-10-05

The R2 publisher and credential-safe PowerShell wrapper are implemented. Fourteen offline publisher tests and the wrapper tests pass, covering immutable conflicts, interrupted uploads, exact source/hash provenance and public-byte verification before catalog promotion. Independent review found no remaining publisher findings. Existing Windows/Mac v2.0.0 frozen bytes remain unchanged; the publisher selects platforms explicitly and never inherits an unverified download.

Android is being prepared as a separate 2.0.1 release, versionCode 25, using the established signer. The Android component gate passed, including 316 unit tests, lint, debug assembly, manifest and release contracts. The version-sensitive release test was updated alongside the version bump. Guarded signed release preparation and final freeze evidence follow in the final validation record; no asset is appended to the immutable v2.0.0 release.

The sibling Inevitable implementation has passed a full workspace build, 600 frontend tests and 2,445 backend tests (including real PostgreSQL billing/pilot scenarios); twelve unrelated environment-gated suites/128 tests were skipped. The safe local billing baseline passed four checks with external Discord calls disabled and no orders created. Additional UI cases passed in a focused 76-test run. Independent review found billing recovery/isolation issues being repaired before completion. Sales, prices, invitation automation and production configuration remain unchanged.
