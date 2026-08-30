---
name: mobile-egress-release
description: Use when preparing, resuming, publishing, or verifying a Mobile Egress GitHub release from the Windows publisher workstation, especially after stale JAVA_HOME, Gradle lint-cache locks, or incomplete GitHub draft uploads.
---

# Mobile Egress release

## Core rule

Choose the smallest compatible guarded entry point. Do not reconstruct signing, tagging, upload, or verification manually:

- `scripts\release-windows.ps1` for controller, setup, relay, or EC2 Client changes. The Windows bundle and Client stay together because the controller embeds the signed Client version/hash/URL.
- `scripts\release-android.ps1 -ReleaseVersion ...` for Android-only changes.
- `scripts\release-all.ps1` only for protocol, shared compatibility, or coordinated Windows/Android changes.

**REQUIRED SUB-SKILLS:** Use `mobile-egress-windows-signing` and `mobile-egress-android-signing` for identity recovery or signer failures. Never regenerate an established key to unblock a release.

## Before running

Require:

- explicit user approval before `-Publish`;
- the intended code committed on clean `main`;
- Android `versionName` matching the release and an increased `versionCode` only when Android is selected;
- the established ignored signing inputs for each selected component on the publisher workstation; and
- origin `cbjjensen/mobile-egress`.

The orchestrator resolves and validates only the selected component toolchains, runs the matching gate, signs only the selected component artifacts, and verifies their identities and hashes.

## Commands

Windows-only build/verification and approved publication:

```powershell
& .\scripts\release-windows.ps1 -ReleaseVersion '1.0.7'
& .\scripts\release-windows.ps1 -ReleaseVersion '1.0.7' -Publish
```

Android-only build/verification and approved publication:

```powershell
& .\scripts\release-android.ps1 -ReleaseVersion '1.0.8'
& .\scripts\release-android.ps1 -ReleaseVersion '1.0.8' -Publish
```

Coordinated build/verification and approved publication:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '1.0.9'
& .\scripts\release-all.ps1 -ReleaseVersion '1.0.9' -Publish
```

The publish path tags only verified artifacts, creates an empty draft, uploads each asset sequentially, waits for GitHub's `uploaded` state and matching SHA-256 digest, and only then exposes the prerelease.

If an approved run is interrupted, rerun the same entry point and `-Publish` command. It may resume only when the exact component scope, tag, commit, local signed artifacts, draft names, and remote digests agree.

## Stop conditions

| Condition | Response |
|---|---|
| Missing/mismatched signer | Recover the established private pair; do not initialize or replace it. |
| Known Gradle lint-cache deletion lock | Let the script stop Gradle daemons and retry once. |
| Any other build failure or repeated lock | Stop and diagnose; do not skip gates or kill unrelated Java. |
| Tagged release lacks exact local artifacts | Stop; never rebuild or replace a tagged release. |
| Draft has duplicate, unexpected, or mismatched assets | Leave it unpublished; never use `--clobber`. |
| Published release already exists | Accept only the exact verified asset set; published assets remain immutable. |

## After publication

Report the prerelease URL and public artifact hashes. Complete the physical phone/two-node acceptance in `docs\deployment.md` before any stable promotion; the script intentionally does not declare a release stable.
