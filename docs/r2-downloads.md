# Inevitable Mobile Relay pilot downloads on R2

The download publisher mirrors existing, frozen, signed Inevitable Mobile Relay 2.x release bytes into the existing Order Tracker public R2 bucket. It never builds or signs an app, changes an established identity, modifies GitHub releases, or writes Order Tracker objects. It is a separate, explicitly invoked distribution step after the guarded release workflow.

## Scope and integrity

- Bucket: `order-tracker-downloads`.
- Public origin: `https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev`.
- Immutable artifacts: `mobile-egress/<version>/<canonical asset name>`.
- Mutable operator catalog: `mobile-egress/downloads.json`.
- Only the `pilot` channel and canonical `2.x.y` releases are supported. This does not promote stable downloads.

Starting at 2.0.2, canonical names are `InevitableMobileRelaySetup.exe`, `inevitable-mobile-relay-macos-<version>-arm64.pkg`, and `inevitable-mobile-relay-android-<version>.apk`. Versions 2.0.0 and 2.0.1 retain `MobileEgressClientSetup.exe`, `mobile-egress-client-macos-<version>-arm64.pkg`, and `zfnf-mobile-egress-android-<version>.apk`. Frozen artifacts are never renamed or rebuilt. The R2 `mobile-egress/` prefix remains unchanged. Controller executables, certificate/recovery files, private verification records, historical 1.x packages, and arbitrary filenames are rejected.

The existing `windows-client/build/release/mobile-egress-<version>.freeze.json` binds the source, tag, components, artifact names and SHA-256 digests after the guarded release's signing checks. The publisher checks that record, the local tag, and each selected local artifact. Publishing additionally verifies the actual remote Git tag and the published GitHub prerelease's uploaded asset sizes/digests. It does not accept a new hash-only manifest in place of frozen signing evidence. Missing or overwritten local artifacts must be recovered as the exact original verified bytes, never rebuilt under the existing tag.

Every selected immutable R2 object is checked before any write. An existing object must agree in size and SHA-256 metadata and pass a public byte/header verification. New objects use conditional `If-None-Match: *` writes; a concurrent object is never clobbered. After upload, the publisher verifies R2 metadata and streams the public HTTPS response to check its SHA-256, byte count, content type, attachment filename and immutable cache header. Requests reject redirects and encoded bodies, have a two-minute timeout, and cannot exceed the expected byte count. Artifacts are bounded to 1 GiB.

Only after all selected public downloads verify does the publisher replace the catalog, using the previously read ETag as a conditional write. A concurrent catalog update stops promotion. The catalog uses `Cache-Control: no-store`; its public bytes and headers are checked afterward. A failure after this final write can leave the new catalog in place: inspect it before retrying. No command claims rollback or deletes uploaded objects after an interruption.

## Select the complete catalog

Desktop contracts from 2.0.6 add `inevitable-mobile-relay-macos-<version>-arm64.dmg`
beside the existing PKG. Selecting `macos` expands to both frozen artifacts;
missing or changed DMG bytes stop preparation, and both public formats must
verify before catalog promotion. Earlier frozen Desktop contracts stay intact.
Catalog schema 1 preserves the PKG fields at `platforms.macos` and adds optional
`platforms.macos.alternatives.dmg` with its own complete `version`, `sourceCommit`,
`url`, `sha256` and `size`. Private `.dmg.verification.json` remains local evidence.
The website keeps `downloads.macos` / `MOBILE_EGRESS_MACOS_DOWNLOAD_URL` for PKG
and uses `downloads.macosDmg` / `MOBILE_EGRESS_MACOS_DMG_DOWNLOAD_URL` for the
optional user app. See [Mac DMG release and acceptance](macos-user-dmg.md).

Create a local JSON plan with the **complete intended set of available platforms**. Every included platform needs its own verified frozen release. An absent platform remains unavailable; existing remote catalog entries are never inherited. This allows Windows/Mac and Android to use different compatible 2.x versions without pretending they came from the same source.

For the published three-platform `v2.0.2` pilot:

```json
{
  "schemaVersion": 1,
  "channel": "pilot",
  "releases": [
    {
      "version": "2.0.2",
      "sourceCommit": "464113dfa7e12ff496d10297d3836e80304d5f7b",
      "platforms": ["windows", "macos", "android"]
    }
  ]
}
```

This plan explicitly includes all three platforms from one frozen source. A future independent platform release needs its own actual frozen version/source entry; do not reuse a historical APK or silently choose another version. Cross-platform interoperability and physical acceptance still require their existing gates; sharing a major version alone is not proof of acceptance.

The output catalog has `schemaVersion: 1`, `channel: "pilot"`, and a `platforms` object keyed by `windows`, `macos`, or `android`. Each value contains `version`, `sourceCommit`, `url`, `sha256`, and `size`. When the website's `MOBILE_EGRESS_DOWNLOAD_CATALOG_ENABLED` flag is enabled, its backend reads this fixed public catalog and resolves links through the existing product response. A valid snapshot owns Windows, PKG, DMG and Android availability; an omitted platform is unavailable. iPhone configuration remains separate. This is not an installed-app updater or a mechanism to enable subscriptions or sales.

The backend validates canonical artifact metadata/URLs, caps catalog reads at
32 KiB and two seconds, shares concurrent refreshes, and caches successful
snapshots for five minutes. Refresh failure retains the last valid links and
retries no faster than every 30 seconds; before any successful refresh, configured
download URLs provide the fallback. A normal authorized release only publishes
the guarded catalog, then verifies the product/page links after the cache refresh.
It needs no per-version environment edit or website deployment. The browser's
existing product query remains unchanged; new visits/refetches see resolved links.

## Validate and publish

Requirements:

1. Node.js 20+ and PowerShell 7 on the existing publisher workstation; Git, authenticated GitHub CLI, and AWS CLI v2 for publication.
2. Local frozen records/tags and original selected artifacts, plus their matching published GitHub prereleases. Standard release signing/notarization evidence remains private and must be retained.
3. The existing ignored Order Tracker R2 configuration at `%USERPROFILE%/.order-tracker/desktop-downloads.json`, or an explicit `-ConfigPath`. It contains `r2AccountId`, `r2Bucket`, `r2PublicBaseUrl`, and the existing R2 S3 access credentials. Existing session `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` may supply credentials when the file omits them. Never copy credentials into a plan, source, output, or command-line argument.
4. Permission to read/write the Inevitable Mobile Relay prefix and a CLI/R2 endpoint supporting conditional PutObject. The publisher refuses older CLIs instead of using unconditional `s3 cp`. No new bucket, domain, signer, or Cloudflare service is provisioned.
5. Explicit authorization to publish. The implementation task itself does not authorize live R2 changes.

An offline dry-run is the default and reads no R2 credential file. It checks local freeze/tag/file evidence and prints the proposed catalog; it does **not** claim that remote objects or public downloads have been checked.

```powershell
& .\scripts\publish-local-mobile-egress-downloads.ps1 -PlanPath 'C:\path\mobile-egress-downloads.json' -DryRun
```

After approval, publication requires both switches:

```powershell
& .\scripts\publish-local-mobile-egress-downloads.ps1 -PlanPath 'C:\path\mobile-egress-downloads.json' -Publish -Pilot
```

The lower-level Node entry point accepts `--plan FILE --dry-run` or `--plan FILE --publish --pilot`. It uses the same `R2_ACCOUNT_ID`, `R2_BUCKET`, `R2_PUBLIC_BASE_URL`, `AWS_ACCESS_KEY_ID`, and `AWS_SECRET_ACCESS_KEY` environment contract as Order Tracker. Credential values are never arguments or logs. The PowerShell wrapper restores the previous environment even after failure. No build, signing, or existing release orchestration changes are introduced.

Keep the plan and successful output as local publication evidence. On interruption, rerun that exact plan: matching immutable objects are verified and reused. Stop on any conflict, unknown upload outcome, missing frozen source, public-header/hash mismatch, or concurrent catalog change. Resolve the evidence; never overwrite an immutable object to force a retry.

## Updating website links without unrelated deployments

Installer publication and website download-link updates have a narrow scope:
**do not deploy unrelated website changes just to update download links.**
Publishing GitHub/R2 assets does not itself change the website's configured URLs.

With dynamic catalog resolution enabled, existing format URL changes require
no website rollout: publish and verify the guarded catalog, allow its five-minute
backend cache to refresh, then confirm actual customer-visible links. Keep the
configured URLs as outage fallbacks. Use the scoped procedure below only when
enabling/disabling this feature, changing fallback settings, or adding website
support for a new format. Rollback of a selected release uses an explicit verified
catalog plan; all immutable artifacts remain unchanged.

1. Verify the actually deployed website source and production configuration.
   Compare the intended download settings with that baseline; preserve all other
   values, including sales, access, Apple automation and unrelated downloads.
   Keep secret-bearing comparisons/backups private and report only safe results.
2. Use the existing environment publisher and approved release workflow. Select
   the intended source explicitly; the workflow's `image_tag` names an image and
   does **not** select the Git revision to build. Do not dispatch current `main`
   unless its changes are within the separately authorized deployment scope.
   Preserve normal CI, environment protections and infrastructure gates.
3. Check active deployments again immediately before dispatch. A rerun of a
   previously successful release can retain the deployed source, but must not
   run after a newer deployment and undo it. Cancel only this task's redundant
   queued rollout when needed; do not cancel another operator's deployment.
4. If an independently authorized deployment is already proceeding, verify its
   configuration compatibility and when it reads the environment bundle. Let it
   carry the intended URL update when safe, then verify the actual result. Do not
   assume publishing a secret means a running job has consumed it.
5. Confirm the deployed source, actual backend download values, unchanged other
   settings, service health and authenticated page links. Record asset
   publication separately from website activation; if activation is blocked,
   report the specific pending step without broadening deployment scope.
6. Synchronize the protected local production configuration only after checking
   for concurrent edits. Rollback restores only the affected URL settings while
   preserving the currently deployed source and other settings; never restore a
   whole stale environment or older website revision just to roll back links.

The [2.0.4 publication record](superpowers/plans/2026-10-06-client-oled-theme.md#website-download-handoff)
documents this procedure, including cancellation of a queued older-source rerun
when a separate deployment started and subsequent live verification of its links.

## Implementation and validation record

The approved subscription plan is [2026-10-05-mobile-egress-subscriptions.md](superpowers/plans/2026-10-05-mobile-egress-subscriptions.md). Order Tracker's `desktop-release-downloads.mjs` and `publish-local-desktop-downloads.ps1` provided the existing R2/environment pattern; its source and objects are unchanged. The publisher adds only integrity requirements for frozen Inevitable Mobile Relay artifacts and the pilot catalog.

Implementation phases: define offline provenance/conflict tests; implement the publisher and conditional transport; provide a credential-safe local wrapper; run local dry-run and existing release-contract checks. Rollback means selecting an explicit previously verified catalog plan; do not delete immutable artifacts or change release tags. Native physical acceptance and stable promotion remain separate.

Run the offline tests with:

```powershell
node --test scripts/test-r2-downloads.mjs
& .\scripts\test-r2-downloads.ps1
```

Tests cover unsafe versions/names/origins, missing selected artifacts, frozen/remote source mismatch, different selected platform versions, local corruption, all-object conflicts, misleading matching metadata, interrupted uploads, conditional races, public hashes/sizes/headers, and catalog promotion. Live credentials, R2 writes, R2 conditional behavior, CDN responses, and installation acceptance are external prerequisites not established by these offline tests.

2026-10-05 validation: all 14 Node tests passed, and the isolated PowerShell wrapper checks passed (offline default, explicit publication confirmation, session credential fallback and environment restoration after failure). Existing `test-release-all.ps1`, `test-release-desktop.ps1`, and `test-direct-release.ps1` passed. The offline preparation function verified the actual frozen Desktop `v2.0.0` files and local tag, yielding Windows SHA-256 `480a26dc5fc35a7f6075b6dc4ac3b93408ccaaecacef00ed34373d20db6dab17` (25,677,088 bytes) and Mac SHA-256 `57d9378e42e26dda93c94a9b1a4115a3887128b04e99484b71b1e2d1461e00a1` (13,767,181 bytes). No production credentials were read, no GitHub/R2 calls were made, and no objects were published for this validation.

## Current pilot: Desktop 2.0.6, Android 2.0.3

The owner-authorized [Mac user DMG publication](superpowers/plans/2026-10-07-macos-user-dmg.md)
published Desktop **2.0.6** from frozen source
`46834f6a572a890c259edbf15f8f7f5e51f56942`. Windows EXE and both Mac formats
passed their guarded signing and artifact checks; the DMG app and image each
passed notarization, stapling and Gatekeeper, including mounted-payload checks.
GitHub and R2 public digests match the frozen files. R2 verified every selected
download before catalog promotion, and independent catalog readback matched.
The linked publication record contains exact hashes and byte counts.

- [Mac DMG, no administrator installation, keep app open](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.6/inevitable-mobile-relay-macos-2.0.6-arm64.dmg)
- [Mac PKG, administrator installation, background service](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.6/inevitable-mobile-relay-macos-2.0.6-arm64.pkg)
- [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.6/InevitableMobileRelaySetup.exe)
- [Unchanged Android APK](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.3/inevitable-mobile-relay-android-2.0.3.apk)

The owner explicitly waived waiting for standard-account/physical-phone
acceptance; those checks remain unverified and the release remains a pilot.
Website activation is recorded separately in the linked publication record.
iPhone distribution is unchanged.

Dynamic catalog resolution is enabled in production by
[rollout 37716750300](https://github.com/cbjjensen/inevitable-proxies/actions/runs/37716750300),
from reviewed website source `ecef2a3ddc39b86adc98e854e39c62df47dd5703`.
All 241 production settings matched the intended configuration. A read-only
check inside the deployed backend verified a real catalog fetch, all four links
and five-minute cache reuse; the authenticated live page showed those same
download targets. Normal future version links need no website deployment.

## Historical multi-phone pilot: Desktop 2.0.5, Android 2.0.3

The owner-authorized [multi-phone Client publication](superpowers/plans/2026-10-06-multiple-phones-release.md)
advanced Windows and Mac to **2.0.5**, from frozen source
`22c9db4b27f8201ef9660df5a1e044587aeb173b`. The guarded release verified
Windows signatures and Mac signing/notarization, published the two exact GitHub
assets, and verified public R2 bytes, digests, sizes and headers before catalog
promotion. Independent catalog readback matched all selected platform fields.

- [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.5/InevitableMobileRelaySetup.exe)
- [Apple Silicon Mac PKG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.5/inevitable-mobile-relay-macos-2.0.5-arm64.pkg)
- [Unchanged Android APK](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.3/inevitable-mobile-relay-android-2.0.3.apk)

The linked release record contains exact hashes and completed website activation
evidence. The authenticated live page links these verified versions; all other
production settings and the prior live website source were preserved.
These remain pilot downloads; physical signed upgrade/repair, ten-phone cellular
capacity and distributed phone interoperability remain separate acceptance gates.
iPhone distribution is unchanged.

## Historical OLED pilot: Desktop 2.0.4, Android 2.0.3

The owner-authorized [OLED Client publication](superpowers/plans/2026-10-06-client-oled-theme.md)
advanced Windows and Mac to **2.0.4**, from frozen source
`d9ac6d73971d0a6c39d5bdeee2d03146509e8608`. The signed Windows installer and
signed/notarized Mac PKG passed the guarded release and public GitHub/R2 hash
verification. The linked publication record contains hashes, byte counts and
website rollout evidence. Android remains the exact 2.0.3 APK (code 27); iPhone
distribution is unchanged.

- [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.4/InevitableMobileRelaySetup.exe)
- [Apple Silicon Mac PKG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.4/inevitable-mobile-relay-macos-2.0.4-arm64.pkg)
- [Android APK](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.3/inevitable-mobile-relay-android-2.0.3.apk)

These remain pilot downloads. Signed physical upgrade/repair and phone
interoperability acceptance are not established by publication.

## Historical Broadcast pilot: 2.0.3

The owner-authorized [2026-10-06 publication](superpowers/plans/2026-10-06-broadcast-release.md) advanced all three platforms and the verified public catalog to **2.0.3** (Android code 27), from frozen source `06b299df12c3a143aed6f3c97e488c989aadf599`. Original signatures and Mac notarization pass; GitHub/R2 public digests and download headers were verified. The linked publication record contains every SHA-256 and byte count.

- [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.3/InevitableMobileRelaySetup.exe)
- [Apple Silicon Mac PKG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.3/inevitable-mobile-relay-macos-2.0.3-arm64.pkg)
- [Android APK](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.3/inevitable-mobile-relay-android-2.0.3.apk)

These remain pilot downloads. Physical installation/interoperability gates are unchanged. iPhone 2.0.3 (8) uses TestFlight, with internal access verified and external Apple review pending at publication.

## Historical 2026-10-05 pilot

The separately authorized [2.0.2 publication plan](superpowers/plans/2026-10-05-inevitable-mobile-relay-pilot-downloads.md) completed. At that publication the pilot catalog contained these downloads, mirrored exactly from the [signed GitHub prerelease](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.2); their immutable files remain available:

| Platform / download | Bytes | SHA-256 |
|---|---:|---|
| [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.2/InevitableMobileRelaySetup.exe) | 25,684,768 | `b7251b576bcbbee568266643e43724b8662d7aa2cafe10d62beb4ad68b7cbed2` |
| [Apple Silicon Mac PKG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.2/inevitable-mobile-relay-macos-2.0.2-arm64.pkg) | 13,767,947 | `1ca6ffe32f063e57dbb19f912d0931dc6823429959e7c17b4f63bd55883271e2` |
| [Android APK](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.2/inevitable-mobile-relay-android-2.0.2.apk) | 11,864,996 | `2860fc5b9590366a5716b696515f4fd981be124851452394bcde7b8b23042952` |

All artifacts use version 2.0.2 and source `464113dfa7e12ff496d10297d3836e80304d5f7b`; Android code is 26. The release gate passed 16 R2 tests plus wrapper checks. Actual publication verified GitHub digests, conditional R2 writes, public artifact bytes and download headers before promoting the catalog; independent public catalog readback also passed. Mac notarization/stapling and the existing Windows/Android signatures passed. Signed installation upgrades/repair and physical phone interoperability remain pilot acceptance gates.

The website download environment variables remain deployment-controlled: set `MOBILE_EGRESS_WINDOWS_DOWNLOAD_URL`, `MOBILE_EGRESS_MACOS_DOWNLOAD_URL`, and `MOBILE_EGRESS_ANDROID_DOWNLOAD_URL` to the matching verified URLs during an authorized rollout. Publication does not deploy those settings, change access eligibility, open sales or promote stable. Historical releases and Order Tracker objects remain unchanged.
