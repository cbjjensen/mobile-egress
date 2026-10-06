# Mobile Egress subscriptions, R2 downloads and iPhone pilot

Approved 2026-10-05; implement on main, preserving existing work. The corresponding executable plan, analysis and validation record are in sibling Inevitable `technical-requirement-docs/2026-10-05-mobile-egress-subscriptions/`.

## Superseding invitation decision, 2026-10-06

The owner approved self-service free TestFlight invitations in sibling Inevitable `technical-requirement-docs/2026-10-06-mobile-relay-self-service-invitations/`. This replaces the original manual Admin/Discord approval gate: active authenticated account owners can request independently of payment or a complimentary service grant. Hosted connections still require a valid paid subscription or explicit complimentary tester grant; invitation delivery never creates entitlement. Product `pilotEligible` remains the complimentary hosted-service-grant flag. The website invitation form and setup link now require fresh enabled-and-eligible hosted access (complimentary pilot or valid paid term) plus `iosDistribution === testflight_pilot`. This presentation rule does not change invitation API authorization or revoke Apple enrollment on service expiry; grants cannot bypass unavailable/App Store states, and App Store setup uses its approved link while preserving enrollment records/API. Existing product visibility and sales launch rules are unchanged.

Requests, resends and worker reconciliation retain account/user suspension and ownership checks. Withdrawal and invalid account/owner status still remove only integration-owned Apple membership; ending a subscription or revoking a service grant does not revoke the separate invitation. Preserve Apple consent/privacy, selected-build review/expiry, capacity and disabled automation. Checkout completion can precede the verified invoice: use the success-return hint for bounded two-minute confirmation polling with timeout/retry, and refetch product/access when backend paid eligibility changes. Browser return parameters never authorize activation. No production settings change, deployment or real invitation is authorized by this correction, and no Apple approval for paid beta access is claimed. The original behavior and evidence below remain historical; native behavior and the mobile lifecycle exception are unchanged.

## Invitation automation enablement (2026-10-06)

Subsequent operational evidence: on 2026-10-06, Apple approved fixed iOS build 2.0.0 (7) for external testing after review metadata and ZFNF Friends build assignment were updated. The known-buggy build 5 was detached only from that external group; uploaded artifacts, internal memberships, existing testers and public-link settings were preserved. The sibling Inevitable `technical-requirement-docs/2026-10-06-mobile-relay-invitation-enablement/implementation-summary.md` records live backend readiness and local API/outbox automation enablement. Its real authenticated local form hides the disabled-automation notice. The live website, sales and stable-release promotion are unchanged; physical-device and actual new-customer invitation acceptance remain separate checks. No native behavior or mobile manifest evidence changed in this operational work.

## Automatic iPhone release following (2026-10-06)

Further owner-approved scope on 2026-10-06: enable the live Inevitable invitation flow and automatically follow newer compatible approved iOS builds released to the same external group. The sibling enablement plan records the implementation and deployment gates. Keep selection forward-only with a durable initial build floor, preserve uncertain invitation operations and membership ownership, and do not send another invitation just because a build changes. Uploads/pending review do not qualify; a breaking major version requires an explicit compatibility decision. Website sales remain closed. This changes distribution readiness only, not phone lifecycle or traffic behavior.

The owner also requested automatic updates for existing enrolled testers and merging all work into main. Existing external group membership already grants released builds without re-invitation. Add a tracked release preflight that verifies/enables Apple's exact-build automatic notification setting before release; preserve tester memberships and leave device auto-installation under TestFlight preferences. Current build 7 is already configured to notify automatically. This does not authorize unrelated tester/group changes or stable promotion.

Implemented the tracked `scripts/set-ios-external-auto-notify.ps1` gate and its Python helper. The default verifies without writes; authorized `-Apply` changes only the exact build's notification flag before beta submission, with readback. It does not mutate testers, groups, review submissions or public links. Eight offline regression tests and independent review passed; the Mac-backed wrapper verified current build 7 as `IN_BETA_TESTING`, notifications enabled, no change required and no mutation performed. Manifest validation and PowerShell syntax checks passed. The iOS guide and build-server release procedure require this gate for future external releases.

## Production invitation verification (2026-10-06)

The owner-started [Inevitable production release 37478130954](https://github.com/cbjjensen/inevitable-proxies/actions/runs/37478130954), main source `ef4c852a`, succeeded after the previous GitHub run stalled. Read-only production verification at 14:39 UTC confirmed the exact backend/outbox image SHA, both Mobile migrations, enabled invitation automation and release following, approved unexpired build 2.0.0 (7), a fresh healthy worker and closed sales. The real authenticated website displays the iPhone invitation form without the unavailable notice, alongside the verified 2.0.2 R2 downloads. The sibling enablement implementation summary records the workflow and SSM evidence.

No test invitation or purchase was submitted; actual customer email receipt and physical-device acceptance remain separate checks. Existing tester membership and automatic notification settings remain intact. Terraform apply and Core/Mobile gateway deployments were skipped. The earlier GitHub hold's internal cause remains unconfirmed; no account-plan or protection changes were made by this verification. Native lifecycle behavior and manifest evidence are unchanged. The completed release monitor is stopped.

## Original approved behavior (2026-10-05)

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
