---
name: mobile-egress-release
description: Use when preparing, publishing, or verifying a Mobile Egress Desktop, macOS PKG/DMG, or Android GitHub release from the Windows publisher workstation.
---

# Mobile Egress release

## Core rule

Choose the smallest compatible guarded entry point. Do not reconstruct signing, tagging, upload, or verification manually:

- `scripts\release-desktop.ps1 -ReleaseVersion ...` for coupled Windows and Apple Silicon Mac Client installers. Desktop 2.0.2–2.0.5 contains `InevitableMobileRelaySetup.exe` and `inevitable-mobile-relay-macos-<version>-arm64.pkg`; from 2.0.6 it also requires `inevitable-mobile-relay-macos-<version>-arm64.dmg`, sharing one version/tag. Frozen older releases retain their exact contract and filenames. No controller, relay, or raw EC2 asset is built or published.
- `scripts\release-android.ps1 -ReleaseVersion ...` for Android-only changes.
- `scripts\release-all.ps1 -Components Desktop,Android` for protocol/shared compatibility or coordinated Desktop/Android changes.
- `scripts\release-all.ps1 -Components Windows,Android` for normal non-Apple releases when Windows and Android should ship while macOS/iOS are handled separately.
- `scripts\release-all.ps1 -ReleaseVersion 1.1.0 -Components Windows,Android` only when reproducing the immutable v1.1.0 interim release scope.
- `scripts\release-all.ps1 -ReleaseVersion 1.1.1 -Components Windows` only for the explicitly approved v1.1.1 Windows proxy hotfix. Android remains on the published v1.1.0 APK and macOS remains unavailable.

Legacy `release-windows.ps1` is a fail-closed migration shim, not a publication path. The deterministic orchestrator supports `Windows,Android` for non-Apple releases; bare `Windows` remains reserved for the approved v1.1.1 hotfix. macOS-only selection remains unsupported.

The current 2.x release contract uses the self-contained `InevitableMobileRelaySetup.exe` as the only Windows download from 2.0.2, with no legacy filename alias. Android uses `inevitable-mobile-relay-android-<version>.apk` from 2.0.2; frozen 2.0.1 keeps its `zfnf-mobile-egress-android-2.0.1.apk` name and bytes. Advance Android versionName/versionCode before building branded release source; never rebuild a frozen version. The displayed product name is Inevitable Mobile Relay; internal identities, signing keys and output roots remain unchanged. Android fallback links must remain within the same direct major version. Historical 1.x artifact contracts and published assets remain immutable; rebuilding historical controller releases requires their original source checkout. `payload-verification.zip` and Mac verification JSON remain private validation evidence. Updating source or these instructions does not publish an installer.

**REQUIRED SUB-SKILLS:** Use `mobile-egress-windows-signing` and `mobile-egress-android-signing` for identity recovery or signer failures. Never regenerate an established key to unblock a release.

## Before running

For the user DMG, follow [the guarded format procedure](../../../docs/macos-user-dmg.md).
Preserve the established GUI bundle/executable identifiers; verify signed
`MobileEgressRuntimeMode=app` against actual binary `--runtime-mode`, clean
embedded source/version and arm64/macOS 13. Require app and image signatures,
accepted notarization, both staples/Gatekeeper checks and mounted executable
hash before exclusive promotion. Retain `.dmg.verification.json` privately;
never upload it. Desktop transfer validates both PKG and DMG before either is
promoted. Schema 1 R2 `macos` selection expands both formats from 2.0.6 and
retains PKG metadata with `alternatives.dmg`; all selected public bytes must
verify before catalog promotion. A standalone fresh-prerelease DMG validation
build signs/notarizes but does not freeze, tag or publish Windows/Desktop.

The owner authorized the 2026-10-07 DMG pilot release without waiting for tester
confirmation. Keep unperformed standard-user/physical-phone acceptance marked
unverified; do not remove automated/native release checks or promote stable.

Require:

- explicit user approval before any run that selects Desktop, because even without `-Publish` it contacts the Mac, signs Windows artifacts, Developer ID-signs and notarizes the Mac PKG, and freezes a local tag;
- explicit user approval before a `Windows,Android` or approved v1.1.1 Windows-only run, because even without `-Publish` it signs artifacts and freezes a local tag;
- separate explicit user approval before `-Publish`, because publication pushes source/tag state and changes GitHub;
- the intended code committed on clean `main`;
- Android `versionName` matching the release and an increased `versionCode` only when Android is selected;
- the established ignored signing inputs for each selected component on the publisher workstation;
- ignored/untracked `.local\mac-build-server\release-desktop.psd1`, its configured key, standard OpenSSH host trust, and working Mac Developer ID/notary API prerequisites when Desktop is selected (2.x does not require the retired controller provisioning profile or node manifest); and
- origin `cbjjensen/mobile-egress`.

The orchestrator resolves and validates only the selected component toolchains, runs the matching gate, signs only the selected component artifacts, and verifies their identities and hashes.

## Commands

Coupled Desktop build/verification and approved publication:

```powershell
& .\scripts\release-desktop.ps1 -ReleaseVersion '<version>'
& .\scripts\release-desktop.ps1 -ReleaseVersion '<version>' -Publish
```

Android-only build/verification and approved publication:

```powershell
& .\scripts\release-android.ps1 -ReleaseVersion '<version>'
& .\scripts\release-android.ps1 -ReleaseVersion '<version>' -Publish
```

Coordinated build/verification and approved publication:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '<version>' -Components Desktop,Android
& .\scripts\release-all.ps1 -ReleaseVersion '<version>' -Components Desktop,Android -Publish
```

Normal Windows/Android build and publication with Apple platforms released separately:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '<version>' -Components Windows,Android
& .\scripts\release-all.ps1 -ReleaseVersion '<version>' -Components Windows,Android -Publish
```

Explicitly approved v1.1.1 Windows proxy-hotfix build and publication with Android unchanged and macOS unavailable:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '1.1.1' -Components Windows
& .\scripts\release-all.ps1 -ReleaseVersion '1.1.1' -Components Windows -Publish
```

The Windows/Android-scoped release notes must mark macOS outside the release scope. The v1.1.1 notes link its current Windows artifacts and fall back to the published v1.1.0 Android APK; do not select, rebuild, or version-bump Android for this hotfix. Never add a Mac asset to a Windows-scoped tag; use a later version for macOS/Desktop once Apple signing/notarization is ready.

The non-publishing path freezes a local tag only after artifact verification and writes an ignored local record binding the source commit, component scope, asset names, and SHA-256 digests. The publish path requires that exact record, pushes the verified source/tag, creates an empty draft, starts missing asset uploads in parallel, waits for GitHub's `uploaded` state and matching SHA-256 digest for every asset, and only then exposes the prerelease.

If an operation is interrupted, inspect the exact local, Mac, or GitHub output before retrying. Resume only when the source commit, artifacts, and hashes agree.

## Stop conditions

| Condition | Response |
|---|---|
| Missing/mismatched signer | Recover the established private pair; do not initialize or replace it. |
| Desktop PSD1, SSH, signing/notary prerequisite, PKG/required DMG, verification record, or hash is invalid | Stop and repair the exact prerequisite; do not upload the invalid or incomplete Desktop set. |
| Known Gradle lint-cache deletion lock | Let the script stop Gradle daemons and retry once. |
| Any other build failure or repeated lock | Stop and diagnose; do not skip gates or kill unrelated Java. |
| Tagged release lacks exact local artifacts | Stop; never rebuild or replace a tagged release. |
| Draft has duplicate, unexpected, or mismatched assets | Leave it unpublished; never use `--clobber`. |
| Published release already exists | Accept only the exact verified asset set; published assets remain immutable. |

## After publication

Report the prerelease URL and public hashes for every selected artifact. Retain the Mac verification JSON as private/local evidence whenever Desktop is selected, never as a GitHub asset. Complete the acceptance applicable to the published component scope before stable promotion; the script intentionally does not declare a release stable.

When the website's `MOBILE_EGRESS_DOWNLOAD_CATALOG_ENABLED` flag is enabled,
its backend follows the verified R2 catalog with a five-minute cache. Publishing
ordinary new version URLs then needs no website deployment or per-version env
edit. After guarded catalog publication, verify actual product/page links after
refresh. Preserve configured URLs as initial outage fallbacks and keep iPhone
distribution separate. Adding a new website format or changing the catalog flag
still uses the scoped source/configuration rollout procedure in `docs/r2-downloads.md`.
