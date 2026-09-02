---
name: mobile-egress-release
description: Use when preparing, publishing, or verifying a Mobile Egress Desktop, macOS PKG, or Android GitHub release from the Windows publisher workstation.
---

# Mobile Egress release

## Core rule

Choose the smallest compatible guarded entry point. Do not reconstruct signing, tagging, upload, or verification manually:

- `scripts\release-desktop.ps1 -ReleaseVersion ...` for controller, setup, relay, or EC2 Client changes. Desktop is indivisible: the Windows bundle, Windows EC2 Client, and macOS PKG share one version/tag.
- `scripts\release-android.ps1 -ReleaseVersion ...` for Android-only changes.
- `scripts\release-all.ps1 -Components Desktop,Android` for protocol/shared compatibility or coordinated Desktop/Android changes.
- `scripts\release-all.ps1 -ReleaseVersion 1.1.0 -Components Windows,Android` only for the explicitly approved v1.1.0 interim release that defers macOS to a later immutable version.

Legacy `release-windows.ps1` is a fail-closed migration shim, not a publication path. The `Windows` selector is supported only through the deterministic orchestrator; macOS-only selection remains unsupported.

**REQUIRED SUB-SKILLS:** Use `mobile-egress-windows-signing` and `mobile-egress-android-signing` for identity recovery or signer failures. Never regenerate an established key to unblock a release.

## Before running

Require:

- explicit user approval before any run that selects Desktop, because even without `-Publish` it contacts the Mac, signs Windows artifacts, Developer ID-signs and notarizes the Mac PKG, and freezes a local tag;
- separate explicit user approval before `-Publish`, because publication pushes source/tag state and changes GitHub;
- the intended code committed on clean `main`;
- Android `versionName` matching the release and an increased `versionCode` only when Android is selected;
- the established ignored signing inputs for each selected component on the publisher workstation;
- ignored/untracked `.local\mac-build-server\release-desktop.psd1`, its configured key, standard OpenSSH host trust, and working Mac Developer ID/profile/notary prerequisites when Desktop is selected; and
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

Explicitly approved interim Windows/Android build and publication with macOS deferred:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '1.1.0' -Components Windows,Android
& .\scripts\release-all.ps1 -ReleaseVersion '1.1.0' -Components Windows,Android -Publish
```

The interim release notes must mark macOS unavailable pending Apple Developer Program enrollment. Never add a Mac asset to the published tag; use a later version for the first signed/notarized Mac release.

The non-publishing path freezes a local tag only after artifact verification and writes an ignored local record binding the source commit, component scope, asset names, and SHA-256 digests. The publish path requires that exact record, pushes the verified source/tag, creates an empty draft, uploads each asset sequentially, waits for GitHub's `uploaded` state and matching SHA-256 digest, and only then exposes the prerelease.

If an operation is interrupted, inspect the exact local, Mac, or GitHub output before retrying. Resume only when the source commit, artifacts, and hashes agree.

## Stop conditions

| Condition | Response |
|---|---|
| Missing/mismatched signer | Recover the established private pair; do not initialize or replace it. |
| Desktop PSD1, SSH, signing/notary prerequisite, PKG, verification record, or hash is invalid | Stop and repair the exact prerequisite; do not upload the PKG. |
| Known Gradle lint-cache deletion lock | Let the script stop Gradle daemons and retry once. |
| Any other build failure or repeated lock | Stop and diagnose; do not skip gates or kill unrelated Java. |
| Tagged release lacks exact local artifacts | Stop; never rebuild or replace a tagged release. |
| Draft has duplicate, unexpected, or mismatched assets | Leave it unpublished; never use `--clobber`. |
| Published release already exists | Accept only the exact verified asset set; published assets remain immutable. |

## After publication

Report the prerelease URL and public hashes for every selected artifact. Retain the Mac verification JSON as private/local evidence whenever Desktop is selected, never as a GitHub asset. Complete the acceptance applicable to the published component scope before stable promotion; the script intentionally does not declare a release stable.
