---
name: mobile-egress-android-signing
description: Use when building, verifying, backing up, restoring, or troubleshooting Mobile Egress Android release APK signing, keystore.properties, JKS files, Gradle signing, or signer-certificate mismatches.
---

# Mobile Egress Android signing

## Core invariant

Android updates must use the same signing key as every previously installed release. This workspace's publisher identity is:

- ignored private key: `android/mobile-egress-release.jks`
- ignored credentials: `android/keystore.properties`
- tracked public identity: `android/release-signing-certificate.txt`

If the ignored files exist, reuse them without changing their contents, alias, or passwords. If either is missing after any APK has been distributed, stop and recover both from the encrypted backup. A new key with the same alias or package name is a different identity and cannot update existing installations.

Create a replacement only when the user explicitly confirms no release signed by the old key remains installed or needs an update. Never print signing properties, pass passwords literally in tool calls, or stage/copy private signing material without explicit authorization.

## Release workflow

From the repository root:

```powershell
$privateFiles = @('android/mobile-egress-release.jks', 'android/keystore.properties')
$tracked = @(git ls-files -- $privateFiles)
if ($tracked.Count -ne 0) { throw 'Android signing secrets are tracked by Git.' }
foreach ($path in $privateFiles) {
    git check-ignore -q -- $path
    if ($LASTEXITCODE -ne 0) { throw "$path is not ignored." }
}
& .\scripts\release-android.ps1 -ValidateOnly
if ($LASTEXITCODE -ne 0) { throw 'Signing validation failed.' }
& .\scripts\release-android.ps1
if ($LASTEXITCODE -ne 0) { throw 'Signed release failed.' }
Get-FileHash -Algorithm SHA256 -LiteralPath '.\android\app\build\outputs\apk\release\app-release.apk'
```

The release script verifies the APK and rejects a signer whose SHA-256 certificate digest differs from the tracked public identity. Report only the APK path/hash, verification result, and public signer fingerprint.

## Quick reference

| Situation | Action |
|---|---|
| Routine release | Reuse both ignored files and run the guarded script. |
| New clone or replacement PC | Restore both ignored files from the encrypted backup. |
| Password/signing error | Preserve the files; diagnose configuration or recover credentials. |
| Friends installing the app | Give them the signed APK; never give them signing files. |
| First-ever publisher setup | Obtain explicit approval, generate once, record the public fingerprint, and make an encrypted external backup. |

## Common mistakes

- Regenerating because the alias matches: aliases do not preserve key identity.
- Treating `.gitignore` as proof: also confirm `git ls-files` returns neither secret.
- Comparing only to the current JKS: compare the APK to the tracked fingerprint or a previously distributed release.
- Keeping the only copy in this checkout: back up the JKS and properties together in encrypted storage outside the computer.
- On Windows, `clean` may report that the lint cache cannot be deleted while Gradle daemons hold it. Confirm with `android\gradlew.bat --status`, run `android\gradlew.bat --stop`, and retry once; do not terminate unrelated Java processes.
