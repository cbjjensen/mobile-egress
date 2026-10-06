# Inevitable Mobile Relay branding

Approved 2026-10-05. Implement on main in Mobile Egress and sibling Inevitable Proxies. Rename all current customer-facing product names, including ZFNF Mobile Egress, to **Inevitable Mobile Relay**. This is a branding change, not a return to the retired personal-computer relay architecture.

## Implementation checklist

- [x] Record approved plan and sibling analysis/implementation ledger before code.
- [x] Website and billing: rename customer/Admin navigation, titles, activation, download/pilot/consent/help/error text. New purchase snapshots and Checkout product names use the new name. Accept both exact old/new billing names without rewriting historical snapshots, pending/idempotent requests, prices or invoices. Advance only the managed portal configuration branding revision; preserve billing behavior and message sanitization.
- [x] Desktop: rename UI, installer/service display text and Windows shortcuts. Mac GUI becomes `/Applications/Inevitable Mobile Relay.app`; safely migrate only verified old application/shortcut objects during upgrade, with conflict handling and recovery. Preserve service ownership, protected state, keys and pairing.
- [x] Mobile: rename Android/iOS app display names, screens, notification and permission/help/support text together. Preserve the approved lifecycle exception. Update tracked parity evidence/tests; retain legacy names only where recovery identifies an old VPN/profile.
- [x] Release/download contract: future branded assets are `InevitableMobileRelaySetup.exe`, `inevitable-mobile-relay-macos-<version>-arm64.pkg`, and `inevitable-mobile-relay-android-<version>.apk`. Preserve published/frozen assets, including Android 2.0.1, and old verification contracts. Use 2.0.2 as the first new branding contract version; historical versions remain immutable. This task does not publish builds or deploy.
- [x] Rename the existing App Store Connect/TestFlight app in place if its editable metadata and credentials allow; retain app identity, testers and groups. Record Apple availability/review blockers accurately.
- [x] Current documentation, accepted architecture terminology and frontend instructions; remaining-old-name audit; relevant tests/builds and independent review. Commit/push records appear in the sibling implementation ledger.

## Compatibility and defaults

Keep package/bundle IDs, protocol identifiers, server/API/dashboard URLs, configuration keys, database names, service IDs, executable/internal storage paths, signing identities, pricing/access controls and pilot group membership unchanged. Existing icon artwork remains unchanged. The desktop/mobile primary display name is exactly **Inevitable Mobile Relay**. Historical releases/plans/benchmarks retain their original labels. Exact old names in verified recovery instructions and signer subjects are intentional compatibility evidence.

New visible Mac bundle/Windows shortcut paths require targeted upgrade handling; do not rename protected state directories or introduce a second service. Unknown or conflicting filesystem objects must not be overwritten. New artifact names start at 2.0.2, not by renaming frozen 2.0.0/2.0.1 artifacts. No binary rebuilding, signing, publication, stable promotion or production deployment is implied by source changes.

## Acceptance

Verify brand text across website and mobile/desktop surfaces; old/new billing terms and pending retries; new Checkout name and portal revision; customer error handling; old/new installer upgrade/repair and failure recovery preserving pairing; release/R2 historical and branded contracts; native tests where available; manifest and local documentation links. Record unavailable signing, Apple or physical checks rather than claiming acceptance.

## Progress and validation

Canonical implementation ledger: sibling `inevitable-proxies/technical-requirement-docs/2026-10-05-inevitable-mobile-relay-branding/implementation-summary.md`. Source baseline: Mobile `85708ac`, Inevitable `79a76dbf`, both clean main. Plan recorded before code in Mobile `e1b3f1e` and Inevitable `617531d1`.

Completed source implementation includes verified legacy shortcut/bundle migration, version-specific artifact contracts and guards against rebuilding historical releases from renamed source. Independent review prompted an Android header layout correction and a shared constant-aware Android version parser; regression fixtures now permit future version bumps without weakening frozen-version checks. Exact old branding remains only in technical identities/ownership markers, historical terms, recovery guidance and frozen evidence; retained artwork is intentional.

Validation passed: Go full suite/vet/build plus Client installer build-tag tests; 71 Client frontend tests; native Mac `macosrelease`/`clientapp` (61 top-level tests, no skips/failures); Android 316 unit tests and lint; Swift warnings-as-errors 393 tests (two hardware/entitlement skips); unsigned iPhoneOS and simulator builds; release orchestration, branded/historical contracts, 16 R2 publisher tests and wrapper; mobile manifest/schema/asset checks; 68 changed-document local links. Inevitable full backend: 202 suites / 2,469 tests passed, 12 suites / 128 tests skipped; full workspace build, 92 focused frontend tests and four-check safe billing baseline passed. Frozen Android 2.0.1 SHA-256 was rechecked unchanged.

Apple's existing app name and its current en-US TestFlight description now use Inevitable Mobile Relay; both PATCH operations returned HTTP 200 and separate reads verified the results. App/bundle identity, testers, group names, build/review/distribution state were not modified.

Remaining acceptance: Xcode package test execution could not start because `testmanagerd` was unavailable after one retry; native Swift and unsigned app builds passed. Signed installer upgrades/repair and physical Android/iPhone testing were not performed. Existing lint/compiler/bundle-size warnings remain. No new binaries were signed or published, no R2 objects changed, and no website/backend deployment or stable promotion occurred. Release preparation must increase Android versionName/versionCode before building 2.0.2 or later; installed older apps retain their original name until updated.
