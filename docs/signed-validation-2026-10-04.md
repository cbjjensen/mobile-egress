# Local signed artifact validation — 2026-10-04

These are local validation artifacts for hosted application source `7430ba63ace5597d0453ac23d6eaeb8bcf155a83`. The initial build changed no installed application/service; the subsequent owner-approved Windows installation is recorded below. No release tag, public GitHub asset, trust store or publisher identity was changed. Hosted infrastructure was disabled at this initial checkpoint; the later owner-only deployment is recorded in [hosted acceptance](hosted-acceptance.md). Successful signing does not establish boot/logout, physical cellular or release acceptance.

## Windows

The established publisher passed `scripts/setup-windows-signing.ps1 -ValidateOnly`. `scripts/build-client-windows.ps1 -ReleaseVersion 2.0.0-hosted-validation.20261004` built the service, graphical Client and self-contained installer from the clean committed source. All three have valid timestamped Authenticode with the exact tracked publisher certificate.

The first full artifact verification exposed a verification bug: PowerShell captured no stdout from the Windows GUI-subsystem application's `--version`, although an explicitly redirected process returned the expected version. A real GUI executable regression failed before the correction. The version reader now uses explicit stdout/stderr pipes and a process timeout; exact version matching remains required. After repair, the complete existing `Assert-MobileEgressDirectWindowsArtifacts` verifier passed signatures, timestamps, exact version, clean source revision/platform provenance and embedded payload equality against the independently signed inputs.

Local directory: `windows-client/build/release/mobile-egress-client-windows-2.0.0-hosted-validation.20261004/`.

| File | SHA-256 |
|---|---|
| `MobileEgressClientSetup.exe` | `BE617B5D420262779508DB916424C4A93C0177A082C0C8FF6D85EB243A16ED82` |
| `mobile-egress-client.exe` | `9CA97420D94E05F9E938EF04E035850BB233446BB5529C4A7879EA22651EA353` |
| `mobile-egress-client-app.exe` | `BA07BD9DD533A63558704C66931C5E4D18E98E815DC6C5A1C89077BE3836871C` |

The private `payload-verification.zip` remains local verification evidence. A subsequent owner-approved installation placed these service/GUI binaries under `C:\Program Files\Mobile Egress Client`. Read-only verification on 2026-10-04 confirmed both installed SHA-256 values match the table and both Authenticode signatures are valid. `MobileEgressClient` is running with automatic startup as LocalSystem. This completes the local fresh-install check only; repair/upgrade/service-account DPAPI recovery, reboot, logout and physical cellular tests remain unperformed. The legacy relay service was not changed.

### Windows IPC repair artifact

The later owner pilot exposed missing named-pipe read-attributes access. The [repair plan](superpowers/plans/2026-10-04-windows-client-pipe-access.md) records the native failing/passing regression, full Windows gate and independent review. The guarded local build from clean source `b4eb10b` produced version `2.0.0-hosted-validation.20261004.1`; full artifact verification passed the same publisher, timestamps, clean source/platform provenance, versions and embedded payload equality. The original validation files above remain unchanged.

Local directory: `windows-client/build/release/mobile-egress-client-windows-2.0.0-hosted-validation.20261004.1/`.

| File | SHA-256 |
|---|---|
| `MobileEgressClientSetup.exe` | `E3FDBA85B935F48805B852CBE165A741D061E7792F2BC19642A690F0FC65344A` |
| `mobile-egress-client.exe` | `0B8872737EB26F3A3E76F598DDC9C88B6AB3A4D98CEF5D833B879C112A744666` |
| `mobile-egress-client-app.exe` | `CD1E5B3005EC0D6FC6CC5AC880A0603622B7111F572E1E14C30ECC028A6A1A9E` |

The supported installer was launched for the owner's confirmation. Installed repair, activation and phone traffic are not established by signing alone. No application release was published.

## Android

The low-level guarded `scripts/release-android.ps1 -ValidateOnly` verified ignored/untracked signing inputs; the unversioned build command then reused the established key and verified the signed release APK against the tracked public certificate. No version bump, release tag or publication occurred.

The initial build encountered the documented Gradle lint-cache file lock during `clean`. Stopping the project Gradle daemon and retrying once resolved it. `clean assembleRelease`, R8, release lint and APK signature verification then passed. The APK has one matching signer and APK signature scheme v3.

Local APK: `android/app/build/outputs/apk/release/zfnf-mobile-egress-android-2.0.0.apk`, version name `2.0.0`, version code `21`. Application source is unchanged from `7430ba6`; documentation and Windows verification-only edits were present during this build. This is not a frozen release artifact.

APK SHA-256: `C4C6007F26AF1F941B2A2DB144CCDF3DB0A3EECBD0153C8AD7C74CE5A8255FEE`.

No Android device was attached to ADB; no iPhone was visible to the Mac device tooling. No handset installation or physical cellular/lifecycle result is claimed.

## Mac and remaining gates

See [Mac signed validation](mac-signed-validation-2026-10-04.md): identity discovery and guarded native test compilation passed. An initial signing failure was resolved using the release workflow's existing configured login-keychain unlock; Developer ID signing and designated-requirement verification then passed. Root fixture execution still requires a password. No identity/ACL/account changes or substitute signing identity were used.

Logs remain under `G:/codex-build-cache/mobile-egress-hosted-20261004/`, including `windows-signed-build.log`, `android-signer-validation.log`, and `android-signed-build-retry.log`. Physical-device, signed installation and deployment gates remain in [hosted acceptance](hosted-acceptance.md).

## Android QR scanner update — code 22

The [orientation repair](superpowers/plans/2026-10-04-android-qr-orientation.md) produced a new local signed APK from clean source `ec619129e4f47b798df45aec6702a73dffdf6c26`. The guarded signing script passed clean release assembly, R8, release lint and verification against the original recorded certificate. The APK has one signer using signature scheme v3, package `com.mobileegress.agent`, version name `2.0.0` and version code `22`.

Immutable local file: `G:/codex-build-cache/mobile-egress-hosted-20261004/android-qr-orientation/zfnf-mobile-egress-android-2.0.0-code22.apk`, 5,314,211 bytes, SHA-256 `39392054E1E882DD493E81A6C80625B4CB16286BBB15A519F72E2D843EA302CD`. The canonical Android release build path now holds code 22; the prior code-21 APK above was hash-verified and preserved as `zfnf-mobile-egress-android-2.0.0-code21.apk` in the same private evidence directory.

All 298 Android tests, lint/debug assembly, manifest/schema, release contracts and independent review passed. The production decoder read the reported screenshot upright; no live QR payload is tracked. Workstation memory/disk failures and the verified generated-cache/output move to G are recorded in the repair plan. No ADB phone was attached, so installing the update, physical scanning and Mac cellular traffic remain owner acceptance. No public release, tag, deployment or main merge occurred.
