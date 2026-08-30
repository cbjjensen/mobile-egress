---
name: mobile-egress-release
description: Use when preparing, resuming, publishing, or verifying a Mobile Egress GitHub release from the Windows publisher workstation, especially after stale JAVA_HOME, Gradle lint-cache locks, or incomplete GitHub draft uploads.
---

# Mobile Egress release

## Core rule

Routine releases go through `scripts\release-all.ps1`. Do not reconstruct the signing, tagging, upload, or verification sequence manually.

**REQUIRED SUB-SKILLS:** Use `mobile-egress-windows-signing` and `mobile-egress-android-signing` for identity recovery or signer failures. Never regenerate an established key to unblock a release.

## Before running

Require:

- explicit user approval before `-Publish`;
- the intended code and Android `versionName`/increased `versionCode` committed on clean `main`;
- the established ignored signing inputs on the publisher workstation; and
- origin `cbjjensen/mobile-egress`.

The script imports persistent user or machine JDK/SDK paths into its process, validates both signing identities, runs the full gate, signs both platforms, and verifies the resulting identities and hashes.

## Commands

Build and verify locally without GitHub mutation:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '1.0.4'
```

After explicit publication approval, publish a prerelease:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '1.0.4' -Publish
```

The publish path tags only verified artifacts, creates an empty draft, uploads each asset sequentially, waits for GitHub's `uploaded` state and matching SHA-256 digest, and only then exposes the prerelease.

If an approved run is interrupted, rerun the same `-Publish` command. It may resume only when the exact tag, commit, local signed artifacts, draft names, and remote digests agree.

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
