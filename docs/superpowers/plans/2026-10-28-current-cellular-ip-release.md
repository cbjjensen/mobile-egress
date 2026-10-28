# Current cellular IP mobile pilot publication

The owner approved publishing Android and iOS after reviewing screenshots on 2026-10-28. Publish Android 2.0.11 (code 28) through the existing guarded Android-only GitHub prerelease and R2 pilot catalog. Preserve the exact existing Windows/Mac catalog entries. Publish iOS 2.0.11 (build 9) through the existing external-capable archive/export and TestFlight workflow, preserving signing identities, profiles, bundle/storage identities, tester memberships and public-link settings.

Source includes the reviewed current-cellular-IP feature and completed-rotation comparisons. Both apps show current cellular IPv4/IPv6 before Start or rotation, offer Refresh IP, and discard old results on path changes, suspension or rotation. No protocol, gateway, usage/accounting, desktop build or website deployment is part of this release.

## Gates

- Android: increased version/code, original signer, component tests/lint/release contracts, immutable source/tag/artifact freeze, exact uploaded asset hash, and public R2 byte/header/catalog verification.
- iOS: increased version/build, native tests and simulator build, original Apple Distribution signer, existing manual profiles, app/extension entitlements/privacy/encryption, external-capable export, exact IPA transfer/hash, Apple validation/upload/processing, release-specific test notes, automatic-notification gate before external assignment/submission, and exact-build internal/external group readback.
- Reuse the existing Mobile Egress owner pilot, ZFNF Friends Internal, ZFNF Friends and Inevitable Proxies Group. Existing members need no new invitation. Apple review and device auto-install settings remain outside publisher control.
- Physical cellular/carrier, signed upgrade and sustained multi-Client acceptance remain separate pilot gates. Publication does not promote stable or submit an App Store production version.

## Progress

- [x] Publish and independently verify Android GitHub/R2 artifacts and complete catalog.
- [x] Upload verified iOS archive, verify notifications, assign existing groups and submit beta review as needed.
- [x] Record exact source, public hashes, Apple processing/review/access state and remaining acceptance.

## Android publication

Published [v2.0.11](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.11)
as an Android-only prerelease from frozen source
`8ce527720cb594c2e40913f0520c2e0a3a9eadfc`. APK code 28 is 11,922,431 bytes,
SHA-256 `4f4be44ab9b5093fad374fc670d7a221951de4522f36d5f4d93a6c9d97078dcb`.
The component gate, original signer and GitHub digest passed. The known Gradle
lint-cache lock was recovered by the guarded single daemon-stop/retry. R2
publisher tests (19), wrapper checks and public download verification passed.
Conditional complete-catalog promotion and independent readback preserved the
exact existing Windows 2.0.8 and Mac 2.0.10 entries. The deployed backend resolver
verified the new link with existing configuration; no deployment or environment
edit occurred. Authenticated rendered-page verification was not performed.

## iOS archive and upload

Final iPhone source is `5f98fef7beb51869904ec311060d46bbf8b71a21`, which differs
from the Android frozen source only by updating the native test's obsolete
release-version/build assertions. The first exact-source native run caught that
old expectation; after correction the entire gate passed: 399 Swift tests
(two physical/entitled acceptance skips), warnings-as-errors tests, unsigned
iPhoneOS/simulator builds and Xcode package tests. A fresh signed archive/export
used the corrected source; the earlier unused archive remains private and was
not uploaded.

The verified external-capable IPA is 3,845,278 bytes, SHA-256
`1891a86085afb545bcd39007c9e3048ac5ab63096c75966f573d5cc0b4439035`.
Both bundles retain the original Distribution signer, existing manual profiles,
bundle IDs, shared Keychain/App Group, cleanup-extension entitlement, privacy
manifests and exempt-encryption declaration. Apple package validation and upload
succeeded without errors, delivery `69810e0b-01d6-4a94-beb0-182a489e1bbc`.
Apple processing is `VALID`, unexpired and `APP_STORE_ELIGIBLE`. Exact build
`69810e0b-01d6-4a94-beb0-182a489e1bbc` is `IN_BETA_TESTING` internally, with
access verified in **Mobile Egress owner pilot** and **ZFNF Friends Internal**.
The tracked notification helper verified `autoNotifyEnabled=true` before each
external assignment and again before submission. The same build is assigned
to **ZFNF Friends** and **Inevitable Proxies Group**; all existing associations
and tester memberships are preserved, with no new invitations.

Beta review was submitted on **2026-10-28 at 15:19 America/Chicago**. Final
Apple readback reports external `WAITING_FOR_BETA_REVIEW` and review
`WAITING_FOR_REVIEW`. Internal testers can update in place; external access
requires Apple's approval. Automatic device installation remains governed by
each tester's TestFlight preferences. No App Store production version or
stable promotion was submitted. Physical acceptance remains as listed above.

Private publication evidence is under `.local/current-ip-release/` and
`G:/codex-build-cache/inevitable-mobile-relay-2.0.11/ios-final/`. No signing or API
secrets are tracked or published.
