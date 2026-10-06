# Inevitable Mobile Relay 2.0.2 pilot downloads

The owner authorized completing pilot publication after asking whether downloads were on R2. Work on main. This follows the completed branding change and authorizes a new signed Windows, Apple Silicon Mac and Android pilot release plus exact-byte R2 distribution. It does not authorize website/backend deployment, opening sales, stable promotion or a new iOS build.

## Ordered checklist

- [x] Inspect existing releases, signing inputs, Mac build server, R2 publisher and storage; preserve frozen 2.0.0/2.0.1 artifacts and all existing source work.
- [x] Record this plan before changing release inputs and synchronize the sibling implementation ledger.
- [x] Advance Android to versionName 2.0.2 / versionCode 26; commit the intended source on clean main.
- [x] Run the guarded Desktop + Android gates, build/sign with existing identities, notarize/staple Mac, and verify all artifacts before freezing v2.0.2.
- [x] Publish the verified three-asset GitHub prerelease through the existing orchestrator.
- [x] Back up frozen artifacts and private verification evidence, dry-run the complete three-platform R2 plan, then publish with conditional immutable writes.
- [x] Verify public bytes, SHA-256, sizes and download headers before catalog promotion; retain publication evidence and provide links.
- [x] Record validation, blockers and final commit/release references in both repositories.

## Interfaces and acceptance

Canonical assets: `InevitableMobileRelaySetup.exe`, `inevitable-mobile-relay-macos-2.0.2-arm64.pkg`, and `inevitable-mobile-relay-android-2.0.2.apk`. R2 uses the existing `mobile-egress/2.0.2/` prefix and `mobile-egress/downloads.json` pilot catalog. No signed URLs, new service, bucket or domain. Keep private verification records private.

Use `scripts/release-all.ps1 -ReleaseVersion 2.0.2 -Components Desktop,Android -Publish`, followed by `scripts/publish-local-mobile-egress-downloads.ps1` with the frozen source and all three platforms. Release tests, Go/frontend/Android gates, signer checks and Mac notarization must pass. Verify remote GitHub digests and public R2 downloads; merely uploading is insufficient.

Keep package/service/bundle identities, pairing, credentials and historical artifacts unchanged. Downloads alone grant no hosted entitlement. Purchasing remains closed. Android background sharing and the iOS active-app lifecycle exception remain unchanged; iPhone installation continues through TestFlight.

## Recovery and commit boundaries

Commit release inputs and this plan before guarded builds. Once frozen, never rebuild or replace v2.0.2 artifacts; inspect exact records before resuming interrupted publication. Reuse only matching R2 objects. Stop on conflicts and preserve evidence. A later documentation-only commit records publication. Rollback removes new catalog selection using a verified prior plan without deleting immutable objects or modifying previous releases.

## Progress and limitations

Source baseline: Mobile `b6b03551730dc87a100dc7ebc8907f72df379dcd`; sibling Inevitable `d4d0b69504cb360265820a263981d7af6247b742`. Existing public R2 Mobile paths returned 404 before this work. GitHub v2.0.0 and locally frozen Android v2.0.1 remain preserved.

Windows build storage was moved to `G:/codex-build-cache/inevitable-mobile-relay-2.0.2/windows-build` with a junction at its original ignored path. All 77 files were SHA-256/size verified before creating the junction. Signing/GitHub prerequisites and Mac notary authentication passed read-only preflight. Live R2 publication subsequently passed, including conditional writes and public-byte verification.

Signed upgrade/repair and physical Android/iPhone pairing/traffic acceptance remain separate uncompleted pilot gates. These downloads are pilot builds, not stable or fully release-ready.

## Publication evidence

- Release input/plan commit and frozen source: `464113dfa7e12ff496d10297d3836e80304d5f7b`; Android 2.0.2/code 26. Sibling authorization record: `918a12ad`.
- Guarded `Desktop,Android -Publish` completed successfully on 2026-10-05 (Mountain time). [GitHub v2.0.2](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.2) is published as a prerelease with exactly the three canonical assets and matching remote SHA-256 digests.
- Passed manifest/schema, release/Desktop/historical/branded/R2 contract gates, all Go tests/vet/build and Client setup tests, 71 frontend tests/syntax, Android unit tests/lint/debug build and signed release build. Existing compiler/native-library warnings remain; no failed gate was bypassed.
- Windows Authenticode and payload checks passed with the existing publisher. Android APK signature scheme v3 and the existing certificate matched. Mac application/daemon/package identity, hardened runtime, notarization, stapler, codesign, pkgutil and Gatekeeper checks passed.
- Offline R2 preparation verified the freeze/tag/bytes; live publication verified remote GitHub provenance, wrote immutable versioned objects conditionally, checked public SHA-256/size/type/attachment/cache headers, then conditionally published the pilot catalog. A separate public catalog GET returned HTTP 200 with `no-store` and matching source/version/hash/size/URL fields for all platforms.
- Public links, bytes and checksums are recorded in [R2 downloads](../../r2-downloads.md#published-2026-10-05-pilot). The catalog is [mobile-egress/downloads.json](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/downloads.json).
- Private logs, R2 plan/readback, frozen artifacts and verification records are retained under `G:/codex-build-cache/inevitable-mobile-relay-2.0.2/`; six backup files were hash-verified. The historical Android 2.0.1 backup hash remains `ce548dbbd4fb8b7d127b49fd51a5928ad165a41c8e7178905a432afee07f2efa`.

No website/backend deployment, sales/invitation configuration, iOS distribution, historical release or Order Tracker object changed. The three website download settings can be set to the verified URLs during a separately authorized rollout. This documentation-only completion commit does not change the frozen source or artifacts.
