---
name: mobile-egress-windows-signing
description: Use when building, restoring, verifying, distributing, or troubleshooting Mobile Egress signed Windows releases, Authenticode, MobileEgressSetup.exe, publisher trust, SmartScreen, or EC2 Client trust bootstrap.
---

# Mobile Egress Windows signing

## Core invariant

Every published Windows release must reuse the established `CN=Mobile Egress Local Publisher` identity. Its tracked public files are:

- `windows-signing\mobile-egress-code-signing.cer`
- `windows-signing\release-signing-certificate.txt`

Its ignored private recovery pair is:

- `windows-signing\mobile-egress-code-signing.pfx`
- `windows-signing\signing.properties`

Never regenerate the identity, replace either tracked public file, or run `setup-windows-signing.ps1 -Initialize` to fix missing state, a build failure, expiry, or a mismatch. Never print, commit, upload, attach, copy into logs, or distribute the PFX, password, or properties contents.

## Publisher workstation

Run from the repository root. Validate before a routine release, then let the guarded build discover the tracked identity:

```powershell
& .\scripts\setup-windows-signing.ps1 -ValidateOnly
if ($LASTEXITCODE -ne 0) { throw 'Windows publisher validation failed.' }
& .\scripts\build-windows.ps1 -ReleaseVersion '<semantic-version>'
if ($LASTEXITCODE -ne 0) { throw 'Signed Windows release failed.' }
```

The build must produce a self-contained `windows-client\build\release\mobile-egress-windows-<version>.zip`, require valid timestamped Authenticode on all five executables, and bind node-release manifest v2 to the tracked certificate and `mobile-egress-client.exe` hash. `-CodeSigningThumbprint` is only an optional equality assertion, not a way to select another signer. Any missing/invalid signature or signer mismatch makes the entire archive unusable: stop, do not distribute it, and tell anyone who received it not to run it.

On a replacement workstation, first restore both original ignored files to their exact paths through the secure backup process; do not display their contents. Then run:

```powershell
& .\scripts\setup-windows-signing.ps1 -Restore
if ($LASTEXITCODE -ne 0) { throw 'Windows publisher restore failed.' }
& .\scripts\setup-windows-signing.ps1 -ValidateOnly
if ($LASTEXITCODE -ne 0) { throw 'Restored Windows publisher validation failed.' }
```

If restore rejects the PFX, password, ACL, private key, certificate match, or tracked record, stop and recover the original pair. Do not weaken the validator or initialize a substitute.

## Friend setup

Give a friend only the signed ZIP/setup and the SHA-256 certificate fingerprint from `windows-signing\release-signing-certificate.txt` through a separate trusted channel. Never separately send a CER/DER, recovery file, or verifier bundle.

After extracting it, they may double-click `MobileEgressSetup.exe` directly. Inspecting **Properties → Digital Signatures** or using trusted system Windows PowerShell is useful but optional; never require a verifier script from the ZIP as the launcher. An optional PowerShell check may report `NotTrusted` on a fresh PC before setup and must report `Valid` after trust is installed, always with the exact tracked signer. On the first run, **Unknown publisher** and SmartScreen **More info → Run anyway** may still appear because self-signing does not create SmartScreen reputation; these are acceptable only when setup carries that exact signer. Disabling SmartScreen or signature checks is not supported.

Setup displays the fingerprint, requires explicit **Yes**, verifies and locks its exact signed executable through elevation, installs trust and signed siblings, and launches the controller unelevated only after bound success.

## EC2 trust failures

Use the controller's **Install Client**, **Update**, or **Repair** flow. Controller/SSM bootstrap uses only signed manifest-v2 embedded public DER, its certificate fingerprints, and the pinned artifact hash. For rejection before SSM, check manifest v2, the tracked CER, certificate validity/self-signature/EKU/CA=false, both fingerprints, and release metadata; rebuild with the established identity. For rejection on a node, check SSM/outbound HTTPS, the pinned artifact hash, exact pre-trust signer, `LocalMachine\Root` and `LocalMachine\TrustedPublisher`, and post-trust `Valid` status; correct the release or SSM health and retry.

Never transmit or import a separately sourced CER/DER or private value through SSM. Do not edit the manifest, clear certificate stores, bypass Authenticode, or run overlapping installers. The bootstrap is mutex-serialized and attempt-scoped rollback removes only exact trust it added.

## Loss, compromise, and expiry

Back up the original `windows-signing\mobile-egress-code-signing.pfx` and original `windows-signing\signing.properties` together in encrypted storage separate from the workstation, without displaying or extracting their contents; test restore before relying on the backup. If the key is lost, compromised, or expired, stop affected releases; preserve restricted evidence; warn recipients not to trust new artifacts under that identity; and require a separately reviewed publisher replacement, a new out-of-band fingerprint, and explicit old-trust removal on every friend PC and EC2 node. A self-signed certificate has no public-CA revocation service, and timestamps do not authorize new releases after expiry.

## Common mistakes

| Mistake | Required response |
|---|---|
| Private files are missing | Restore both originals; never regenerate. |
| Build reports a signer mismatch | Preserve the tracked public record and recover the matching private identity. |
| Fresh PC says `NotTrusted` or `Unknown publisher` | Use the separately shared fingerprint and supported setup flow; do not disable protections. |
| EC2 install fails after download | Fix SSM/release integrity and retry through the controller; do not alter stores manually. |
| Private values appear in output | Stop, restrict the evidence, and treat exposure as a potential signing-key incident. |
