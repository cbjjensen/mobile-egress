# Current cellular IP mobile pilot publication

The owner approved publishing Android and iOS after reviewing screenshots on 2026-10-28. Publish Android 2.0.11 (code 28) through the existing guarded Android-only GitHub prerelease and R2 pilot catalog. Preserve the exact existing Windows/Mac catalog entries. Publish iOS 2.0.11 (build 9) through the existing external-capable archive/export and TestFlight workflow, preserving signing identities, profiles, bundle/storage identities, tester memberships and public-link settings.

Source includes the reviewed current-cellular-IP feature and completed-rotation comparisons. Both apps show current cellular IPv4/IPv6 before Start or rotation, offer Refresh IP, and discard old results on path changes, suspension or rotation. No protocol, gateway, usage/accounting, desktop build or website deployment is part of this release.

## Gates

- Android: increased version/code, original signer, component tests/lint/release contracts, immutable source/tag/artifact freeze, exact uploaded asset hash, and public R2 byte/header/catalog verification.
- iOS: increased version/build, native tests and simulator build, original Apple Distribution signer, existing manual profiles, app/extension entitlements/privacy/encryption, external-capable export, exact IPA transfer/hash, Apple validation/upload/processing, release-specific test notes, automatic-notification gate before external assignment/submission, and exact-build internal/external group readback.
- Reuse the existing Mobile Egress owner pilot, ZFNF Friends Internal, ZFNF Friends and Inevitable Proxies Group. Existing members need no new invitation. Apple review and device auto-install settings remain outside publisher control.
- Physical cellular/carrier, signed upgrade and sustained multi-Client acceptance remain separate pilot gates. Publication does not promote stable or submit an App Store production version.

## Progress

- [ ] Publish and independently verify Android GitHub/R2 artifacts and complete catalog.
- [ ] Upload verified iOS archive, verify notifications, assign existing groups and submit beta review as needed.
- [ ] Record exact source, public hashes, Apple processing/review/access state and remaining acceptance.
