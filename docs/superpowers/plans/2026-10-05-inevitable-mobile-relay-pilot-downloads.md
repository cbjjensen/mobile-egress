# Inevitable Mobile Relay 2.0.2 pilot downloads

The owner authorized completing pilot publication after asking whether downloads were on R2. Work on main. This follows the completed branding change and authorizes a new signed Windows, Apple Silicon Mac and Android pilot release plus exact-byte R2 distribution. It does not authorize website/backend deployment, opening sales, stable promotion or a new iOS build.

## Ordered checklist

- [x] Inspect existing releases, signing inputs, Mac build server, R2 publisher and storage; preserve frozen 2.0.0/2.0.1 artifacts and all existing source work.
- [x] Record this plan before changing release inputs and synchronize the sibling implementation ledger.
- [ ] Advance Android to versionName 2.0.2 / versionCode 26; commit the intended source on clean main.
- [ ] Run the guarded Desktop + Android gates, build/sign with existing identities, notarize/staple Mac, and verify all artifacts before freezing v2.0.2.
- [ ] Publish the verified three-asset GitHub prerelease through the existing orchestrator.
- [ ] Back up frozen artifacts and private verification evidence, dry-run the complete three-platform R2 plan, then publish with conditional immutable writes.
- [ ] Verify public bytes, SHA-256, sizes and download headers before catalog promotion; retain publication evidence and provide links.
- [ ] Record validation, blockers and final commit/release references in both repositories.

## Interfaces and acceptance

Canonical assets: `InevitableMobileRelaySetup.exe`, `inevitable-mobile-relay-macos-2.0.2-arm64.pkg`, and `inevitable-mobile-relay-android-2.0.2.apk`. R2 uses the existing `mobile-egress/2.0.2/` prefix and `mobile-egress/downloads.json` pilot catalog. No signed URLs, new service, bucket or domain. Keep private verification records private.

Use `scripts/release-all.ps1 -ReleaseVersion 2.0.2 -Components Desktop,Android -Publish`, followed by `scripts/publish-local-mobile-egress-downloads.ps1` with the frozen source and all three platforms. Release tests, Go/frontend/Android gates, signer checks and Mac notarization must pass. Verify remote GitHub digests and public R2 downloads; merely uploading is insufficient.

Keep package/service/bundle identities, pairing, credentials and historical artifacts unchanged. Downloads alone grant no hosted entitlement. Purchasing remains closed. Android background sharing and the iOS active-app lifecycle exception remain unchanged; iPhone installation continues through TestFlight.

## Recovery and commit boundaries

Commit release inputs and this plan before guarded builds. Once frozen, never rebuild or replace v2.0.2 artifacts; inspect exact records before resuming interrupted publication. Reuse only matching R2 objects. Stop on conflicts and preserve evidence. A later documentation-only commit records publication. Rollback removes new catalog selection using a verified prior plan without deleting immutable objects or modifying previous releases.

## Progress and limitations

Source baseline: Mobile `b6b03551730dc87a100dc7ebc8907f72df379dcd`; sibling Inevitable `d4d0b69504cb360265820a263981d7af6247b742`. Existing public R2 Mobile paths returned 404 before this work. GitHub v2.0.0 and locally frozen Android v2.0.1 remain preserved.

Windows build storage was moved to `G:/codex-build-cache/inevitable-mobile-relay-2.0.2/windows-build` with a junction at its original ignored path. All 77 files were SHA-256/size verified before creating the junction. Signing/GitHub prerequisites and Mac notary authentication passed read-only preflight. Public R2 credential/conditional-write behavior remains to be verified during publication.

Signed upgrade/repair and physical Android/iPhone pairing/traffic acceptance remain separate uncompleted pilot gates. Publication must not describe these builds as stable or fully release-ready. Record actual build/publication outcomes below.
