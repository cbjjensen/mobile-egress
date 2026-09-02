# Release, deployment, and physical acceptance

This is the operator runbook for producing the signed artifacts that friends download and proving them on real Windows and macOS controllers, an Android or iOS cellular Agent device, and Windows Server 2019 EC2 nodes. Normal friends do not perform these steps; they follow the root README after an accepted release is published. Android APK packaging and cellular-IP rotation are covered here. iOS TestFlight signing, upload, and real-device acceptance are separate release work described in [the iOS Agent guide](../ios/README.md); `release-all.ps1` does not sign or publish iOS artifacts.

The former EC2-relay Docker Compose deployment is removed. The supported controller runs on Windows 10/11 or Apple Silicon macOS 13+ with a platform-local relay behind Tailscale Funnel, an Android or iOS cellular Agent, and SSM-managed x86-64 Windows Server 2019 EC2 Clients. A normal public **Desktop** release couples the Windows controller ZIP, EC2 Client, and macOS PKG at one version; Android remains independently selectable. The approved interim v1.1.0 prerelease is a narrow exception containing Windows, the EC2 Client, and Android while macOS awaits Apple Developer Program enrollment.

## Routine release commands

Commit the intended code on clean `main` and choose the smallest compatible scope.

> Running the Desktop command **without** `-Publish` still signs both platforms and freezes a local annotated tag. `-Publish` separately authorizes pushing source/tag state and changing GitHub. Do not run the bare Desktop command as a read-only check.

For a controller, relay, or EC2 Client change, use the coupled Desktop path:

```powershell
& .\scripts\release-desktop.ps1 -ReleaseVersion '<version>'
& .\scripts\release-desktop.ps1 -ReleaseVersion '<version>' -Publish
```

Legacy `release-windows.ps1` continues to fail closed with migration guidance. The guarded orchestrator supports an explicitly selected `Windows` component for an approved interim release; macOS-only selection remains unsupported.

For an Android-only change, first set Android `versionName` to the release version and increase `versionCode`, then use the Android path. Go, the Windows frontend, and Authenticode packaging are not run:

```powershell
& .\scripts\release-android.ps1 -ReleaseVersion '<version>'
& .\scripts\release-android.ps1 -ReleaseVersion '<version>' -Publish
```

Use the full path when a protocol/shared compatibility change requires Desktop plus Android:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '<version>' -Components Desktop,Android
& .\scripts\release-all.ps1 -ReleaseVersion '<version>' -Components Desktop,Android -Publish
```

For the explicitly approved v1.1.0 Windows-and-Android interim prerelease while macOS signing is unavailable:

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '1.1.0' -Components Windows,Android
& .\scripts\release-all.ps1 -ReleaseVersion '1.1.0' -Components Windows,Android -Publish
```

That release contains no macOS artifact. Published assets are immutable; the signed/notarized Mac controller must use a later version.

All paths use the deterministic orchestrator. Desktop means the Windows ZIP, EC2 Client, and macOS PKG from one tag; the narrow Windows selector is restricted to the exact v1.1.0 Windows-and-Android exception, and Android may otherwise be selected separately. The orchestrator runs only the matching gates, validates established signing identities, records the exact source, scope, artifact names, and SHA-256 digests locally, freezes exact artifacts, and publishes only an immutable prerelease after explicit `-Publish` approval. An interrupted/unknown Mac or GitHub operation must be reconciled from the exact local/remote outputs before retrying; never rerun blindly, clobber, delete, or rebuild tagged evidence.

Parts 1–5 below document prerequisites, invariants, and low-level recovery evidence. Do not manually reconstruct them when a component release entry point is available. Parts 6–7 remain required physical acceptance and stable-promotion work.

## Part 1: Prepare the release workstations

Use the Windows publisher computer with the repository checked out on the exact commit to release. GitHub CLI authentication to `cbjjensen/mobile-egress` is required for publication. Each selected component also requires its established signing identity and only its own toolchain:

- **Windows or Desktop:** Go and Node.js versions accepted by `scripts\preflight.ps1`, WebView2, and the established local Mobile Egress Authenticode publisher identity with an accessible private key. Desktop additionally requires ignored/untracked `.local\mac-build-server\release-desktop.psd1`, its configured key, preapproved standard OpenSSH host trust, and the authorized Mac prerequisites below. The release entry point exposes no host, key, credential, verifier, or cleanup override; see [Mac build server over SSH](ios-build-server.md).
- **Android:** JDK 17 or later, Android SDK Platform 35, Android Build-Tools 35, and the established Android release keystore/properties described in Part 4.

Open PowerShell at the repository root and keep the same session for Parts 1–5. Run only the preflight matching the intended component scope:

```powershell
# Windows-only or Desktop
& .\scripts\preflight.ps1 -Components Go, Node, WebView2
if ($LASTEXITCODE -ne 0) { throw 'Windows release workstation prerequisites are incomplete.' }

# Android-only; set these first when they are not already configured for this user
$env:JAVA_HOME = '<absolute path to JDK 17 or later>'
$env:ANDROID_HOME = '<absolute path to the Android SDK>'
$env:ANDROID_SDK_ROOT = $env:ANDROID_HOME
& .\scripts\preflight.ps1 -Components Android
if ($LASTEXITCODE -ne 0) { throw 'Android release workstation prerequisites are incomplete.' }

# Coordinated Desktop plus Android
& .\scripts\preflight.ps1 -Components Go, Node, WebView2, Android
if ($LASTEXITCODE -ne 0) { throw 'Coordinated release workstation prerequisites are incomplete.' }
```

As an alternative to Android environment variables, the ignored `android\local.properties` may contain an escaped absolute `sdk.dir`. Android-selected release scripts intentionally stop with remediation instead of silently skipping Android when no SDK root is configured. Desktop-only runs do not require or validate Android tooling.

### Windows local publisher workflow

Mobile Egress uses one locally generated, self-signed Authenticode publisher identity. Its subject is `CN=Mobile Egress Local Publisher`; it is RSA-4096, SHA-256, Code Signing EKU only, CA=false, and valid for ten years. It belongs in `Cert:\CurrentUser\My` on the publisher workstation and must expose its private key to PowerShell `Set-AuthenticodeSignature`.

Initialize this identity once, on the publisher workstation:

```powershell
& .\scripts\setup-windows-signing.ps1 -Initialize
if ($LASTEXITCODE -ne 0) { throw 'Windows publisher initialization failed.' }
```

It creates an exportable private key and encrypted local recovery files at `windows-signing\mobile-egress-code-signing.pfx` and `windows-signing\signing.properties`, and creates the public `windows-signing\mobile-egress-code-signing.cer` plus `windows-signing\release-signing-certificate.txt`. The PFX and properties are private, ignored, and untracked; the public certificate and public identity record are safe to distribute. Never commit, attach to a release, paste into logs, or otherwise share private signing material or its password.

The publisher must reuse this exact identity for every release. Before every release, validate it, then use the resulting established thumbprint in Part 3:

```powershell
& .\scripts\setup-windows-signing.ps1 -ValidateOnly
if ($LASTEXITCODE -ne 0) { throw 'Windows publisher validation failed.' }
```

Do not generate a replacement certificate to work around a release error, lost local state, or a failed build. To move to a replacement workstation, restore the established encrypted PFX and its password through the supported restore workflow, validate the restored certificate identity, and only then resume releases:

```powershell
& .\scripts\setup-windows-signing.ps1 -Restore
if ($LASTEXITCODE -ne 0) { throw 'Windows publisher restore failed.' }
& .\scripts\setup-windows-signing.ps1 -ValidateOnly
if ($LASTEXITCODE -ne 0) { throw 'Restored Windows publisher validation failed.' }
```

The SHA-256 certificate fingerprint in `windows-signing\release-signing-certificate.txt` supports an independent pre-trust identity check. Share that exact value separately with each friend through a trusted out-of-band channel. A fingerprint shown on the GitHub release, inside the ZIP, or by an executable being installed is a reminder and does not replace the separately shared value. The approved convenience model still supports directly double-clicking setup without an independent signature inspection; no external verifier or launcher is mandatory. Friends should download only from the official GitHub Releases source. Malicious substitution of that source before first trust is outside this relaxed boundary.

Friends can inspect the exact setup signer through **Properties → Digital Signatures** or by opening the system **Windows PowerShell** from the Start menu (never a shell/script from the ZIP). The optional check below requires signer thumbprint `85F220C1BF05A5D3A86B5DD408787EC1B122ECB7`, Status exactly `NotTrusted` on a fresh PC or `Valid` on an already-trusted PC, and the tracked certificate SHA-256. `HashMismatch`, `NotSigned`, `UnknownError`, and every other status are hard failures. Compare the certificate identity Windows reports with the value received through the separate channel.

```powershell
$setupPath = (Resolve-Path '.\MobileEgressSetup.exe').Path
$expectedThumbprint = '85F220C1BF05A5D3A86B5DD408787EC1B122ECB7'
$expectedCertificateSha256 = '9FE214C350D7CE04C8EE7F71E169281B50FF0B2A7C5669A348AC10616FB7061F'
$signature = Get-AuthenticodeSignature -LiteralPath $setupPath
$status = [string]$signature.Status
if ($status -notin @('NotTrusted', 'Valid')) { throw "Reject setup: Authenticode status is $status." }
if ($null -eq $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint.ToUpperInvariant() -ne $expectedThumbprint) { throw 'Reject setup: signer thumbprint differs.' }
$sha256 = [Security.Cryptography.SHA256]::Create()
try { $certificateSha256 = ([BitConverter]::ToString($sha256.ComputeHash($signature.SignerCertificate.RawData))).Replace('-', '') } finally { $sha256.Dispose() }
if ($certificateSha256 -ne $expectedCertificateSha256) { throw 'Reject setup: signer certificate SHA-256 differs.' }
$certificateSha256
```

Double-click `MobileEgressSetup.exe` after any desired Windows signature inspection. The initial Windows dialog can identify setup as **Unknown publisher** and SmartScreen can require **More info → Run anyway**; self-signing does not create SmartScreen reputation. Setup displays the tracked fingerprint, requires explicit **Yes**, locks its own exact executable against write/delete/replacement, verifies its exact signer, hashes it after confirmation, and holds the lock while waiting indefinitely for actual elevated-child completion. The child independently checks the request-bound digest and signature before trust, then acquires the bounded fixed `Global\MobileEgressSetupTransaction` mutex before trust mutation and holds it through trust, signed-sibling verification, installation, and rollback. Timeout fails before trust changes; abandoned ownership is recovered; cleanup releases the mutex on success and failure. Existing files and the Start Menu shortcut are backed up under a SYSTEM/Administrators-only ACL. Backup state is removed only after successful promotion or successful rollback. A restore failure preserves recovery state and returns redacted `install_rollback_failed` guidance to stop, avoid rerunning setup, and contact the publisher. The parent launches only when child exit is zero and the bound result reports success.

The signed controller carries node-release manifest v2 with that same public publisher certificate DER, its SHA-1 thumbprint and SHA-256 fingerprint, and the exact Client URL/artifact SHA-256. Go validates the bounded X.509 DER, cryptographic self-signature, Code Signing EKU, CA=false constraint, current validity, fingerprints, and release metadata before constructing SSM commands. The SSM flow verifies the artifact hash and exact untrusted signer bytes before importing the embedded DER into EC2 `LocalMachine\Root` and `LocalMachine\TrustedPublisher`. Windows Server 2019 can return pre-trust Authenticode `UnknownError` for this self-signed publisher; the node flow permits it only after the pinned artifact hash and exact certificate bytes match. It always requires post-trust `Valid` with the same signer. EC2 nodes receive only public certificate/release material; they never receive the PFX, its password, or another private signing value.

Back up the PFX and its password together in an encrypted location separate from the publisher workstation. Test a restore before relying on that backup. A loss or compromise of the publisher identity is a release-path incident: stop distributing affected releases, preserve restricted evidence, tell recipients not to trust new artifacts under that identity, and perform a reviewed publisher replacement with a new out-of-band fingerprint and explicit old-trust removal.

Plan a reviewed publisher replacement before expiry. Do not treat an expired self-signed certificate as renewable in place or assume timestamped past artifacts make it safe to keep distributing new ones. Self-signed certificates have no public-CA revocation service: removing trust is a local action on every friend PC and EC2 node, cannot erase already downloaded files, and does not remove SmartScreen warnings or reputation state.

List candidate certificates without displaying private material:

```powershell
$codeSigningCertificates = Get-ChildItem -Path 'Cert:\CurrentUser\My', 'Cert:\LocalMachine\My' |
    Where-Object { $_.EnhancedKeyUsageList.ObjectId.Value -contains '1.3.6.1.5.5.7.3.3' } |
    Select-Object Subject, Thumbprint, NotAfter, HasPrivateKey
$codeSigningCertificates
```

The selected thumbprint must be 40 hexadecimal characters. Check that its expiry covers the release date and keep the private key accessible only while signing.

### macOS Developer ID and notarization prerequisites

The Mac builder must be Apple Silicon with Xcode command-line tools available. User-local Go 1.26.7, Node 24.20.0, and Wails 2.14.0 come from `windows-client/macos/toolchain.lock` under the dedicated build root; no Homebrew installation is required.

The Mac Keychain must contain the approved Developer ID Application and Developer ID Installer identities. The configured Developer ID distribution profile must authorize bundle ID `com.cbjjensen.mobile-egress.controller` and its one private Keychain access group; the configured `notarytool` Keychain profile must be available. Private keys, profile contents, and notary credentials remain on the Mac and never enter logs or Windows. Public identity/authority records are verification inputs, not private signing material. There is no placeholder, ad-hoc, or self-signed Mac fallback. Apple Developer Program enrollment is required; see [Developer ID](https://developer.apple.com/support/developer-id/) and [notarization](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).

The signed access-group continuity test is documented in [signed macOS Keychain integration](macos-keychain-integration.md). Changing/installing tools, selecting Xcode, contacting the Mac, signing/notarizing, or publishing requires the appropriate explicit authorization.

## Part 2: Choose and freeze a version

Use one semantic version everywhere. This release uses `1.1.0`.

The first coupled Mac Desktop release will use a version later than `1.1.0`, after Apple Developer Program enrollment and production signing/notary setup are complete.

```powershell
$releaseVersion = '1.1.0'
$releaseTag = "v$releaseVersion"
```

When Android is selected, update `versionCode` and `versionName` in `android\app\build.gradle.kts`. `versionCode` must be higher than every APK previously installed or distributed; `versionName` must equal `$releaseVersion`. When Android is not selected, do not imply or manufacture a matching APK version.

Review and commit that version change, then run the full gate:

```powershell
git status --short
& .\scripts\test-all.ps1
if ($LASTEXITCODE -ne 0) { throw 'The full release gate failed.' }
```

`git status --short` must print nothing after the release commit. The guarded release orchestrator owns annotated-tag observation/creation, exact-object push, and publication reconciliation. Do not manually create or push the production tag. It refuses a conflicting tag or source commit. Never move/reuse a published tag; rebuilding requires a new version.

## Part 3: Build and verify the coupled Desktop release

The production Desktop entry point performs this Windows build as its first stage and captures the exact raw node manifest for the same-commit Mac build. The detailed Windows commands below remain useful for understanding and diagnosing that stage; do not substitute them for the coupled production entry point.

Set the exact certificate thumbprint shown in Part 1, then run the guarded build:

```powershell
$codeSigningThumbprint = '<40-hex-thumbprint>'
& .\scripts\build-windows.ps1 -ReleaseVersion $releaseVersion -CodeSigningThumbprint $codeSigningThumbprint
if ($LASTEXITCODE -ne 0) { throw 'The signed Windows build failed.' }
```

The script builds and verifies the setup application and four executables, reads the tracked CER, emits node-release manifest v2, embeds that manifest in the controller before signing it, and creates:

```text
windows-client\build\release\mobile-egress-windows-<version>.zip
windows-client\build\bin\MobileEgressSetup.exe
windows-client\build\bin\mobile-egress-windows.exe
windows-client\build\bin\mobile-egress-admin.exe
windows-client\build\bin\mobile-egress-relay.exe
windows-client\build\bin\mobile-egress-client.exe
windows-client\build\bin\release-manifest.json
```

The ZIP contains `MobileEgressSetup.exe`, all four sibling executables, the public publisher certificate, and the audit manifest. Friends run only `MobileEgressSetup.exe` after extracting the ZIP. The same standalone `mobile-egress-client.exe` must be uploaded with that exact filename to the matching GitHub tag because the signed controller embeds this URL:

```text
https://github.com/cbjjensen/mobile-egress/releases/download/v<version>/mobile-egress-client.exe
```

Verify the output again before upload:

```powershell
$windowsZip = ".\windows-client\build\release\mobile-egress-windows-$releaseVersion.zip"
$clientExecutable = '.\windows-client\build\bin\mobile-egress-client.exe'
$windowsExecutables = Get-ChildItem -LiteralPath '.\windows-client\build\bin' -Filter 'mobile-egress-*.exe'

$signatureResults = foreach ($executable in $windowsExecutables) {
    $signature = Get-AuthenticodeSignature -LiteralPath $executable.FullName
    [pscustomobject]@{
        File = $executable.Name
        Status = $signature.Status
        Subject = $signature.SignerCertificate.Subject
        Thumbprint = $signature.SignerCertificate.Thumbprint
    }
}
$signatureResults
if ($signatureResults | Where-Object { $_.Status -ne 'Valid' }) {
    throw 'A Windows release signature is invalid.'
}

Get-FileHash -Algorithm SHA256 -LiteralPath $windowsZip, $clientExecutable
Get-Content -Raw '.\windows-client\build\bin\release-manifest.json'
```

Every executable should report `Valid` and the same expected signer thumbprint. The manifest must report version `2`; its Client SHA-256 must match `Get-FileHash`; `signerCertificateBase64` must decode byte-for-byte to `windows-signing\mobile-egress-code-signing.cer`; and `signerCertificateSha256` must match the tracked public record in lowercase. It is safe to record artifact hashes and the release-signing subject/thumbprint; do not record any Mobile Egress relay or device certificate.

### macOS artifact and verification record

After the Windows stage freezes the exact source commit and raw node manifest, `release-desktop.ps1` transfers one exact-commit Git bundle plus that manifest and builds the detached same-commit Mac checkout. The Mac stages arm64 for minimum macOS 13.0, signs the nested relay, then app, then installer, notarizes and staples the PKG, and performs its `codesign`, `pkgutil`, `spctl`, and `stapler` checks.

The Windows publisher retrieves and validates:

```text
windows-client\build\release\mobile-egress-macos-<version>-arm64.pkg
windows-client\build\release\mobile-egress-macos-<version>-arm64.verification.json
```

The strict JSON binds the requested version/source commit, node-manifest SHA-256, artifact name/hash, controller/relay identity and packaging evidence. The PKG hash must match on Windows. The verification record is mandatory private/local evidence and is never uploaded to GitHub; only the PKG is a public Desktop asset. Final evidence is promoted record-last and existing outputs are not automatically overwritten.

An SSH timeout/disconnect can leave the result unknown. Preserve the finite failure and inspect the existing request/output state before retrying; never blindly rerun, clobber an output, or broadly clean the Mac repository/build roots.

## Part 4: Create and protect the Android signing key

Android updates must always use the same signing key. Losing it means existing installations cannot accept your next APK as an update. This publisher workstation keeps the reusable private files at `android\mobile-egress-release.jks` and `android\keystore.properties`; both are ignored. Back up both together in an encrypted location separate from the build computer.

The repository also tracks only the safe public identity in `android\release-signing-certificate.txt`. The guarded release script rejects an APK signed by any other key. Never replace that record to make a mismatched key pass; recover the original private files instead.

For a first-ever publisher setup only, create the ignored local keystore with the JDK `keytool`. The command prompts for passwords and certificate identity; do not put those values on the command line:

```powershell
keytool -genkeypair -verbose -keystore '.\android\mobile-egress-release.jks' -alias 'mobile-egress' -keyalg RSA -keysize 4096 -validity 10000
```

Copy the ignored template and edit only the local copy:

```powershell
Copy-Item -LiteralPath '.\android\keystore.properties.example' -Destination '.\android\keystore.properties'
notepad '.\android\keystore.properties'
```

Use the ignored repository-relative `storeFile` and the alias/passwords entered during key creation:

```properties
storeFile=mobile-egress-release.jks
storePassword=<local secret>
keyAlias=mobile-egress
keyPassword=<local secret>
```

Both private files must remain ignored and untracked. After initial creation, record the public SHA-256 signing-certificate fingerprint in `android\release-signing-certificate.txt` and commit only that public record. Future agents should use [the project signing skill](../.agents/skills/mobile-egress-android-signing/SKILL.md). Validate the guards, then build and verify the release APK:

```powershell
& .\scripts\release-android.ps1 -ValidateOnly
if ($LASTEXITCODE -ne 0) { throw 'Android signing-input validation failed.' }
& .\scripts\release-android.ps1
if ($LASTEXITCODE -ne 0) { throw 'The signed Android release failed.' }
```

The guarded output is:

```text
android\app\build\outputs\apk\release\zfnf-mobile-egress-android-<version>.apk
```

Record its SHA-256. The release script already runs Build-Tools 35 `apksigner verify`; use `--print-certs` when you need the public signer digest for the release record:

```powershell
$androidApk = ".\android\app\build\outputs\apk\release\zfnf-mobile-egress-android-$releaseVersion.apk"
. '.\scripts\operations-common.ps1'
$androidSdkRoot = Get-MobileEgressAndroidSdkRoot -RepositoryRoot (Get-Location).Path
if ([string]::IsNullOrWhiteSpace($androidSdkRoot)) {
    throw 'Set ANDROID_HOME, ANDROID_SDK_ROOT, or android\local.properties first.'
}
$buildTools35 = Get-ChildItem -LiteralPath (Join-Path $androidSdkRoot 'build-tools') -Directory |
    Where-Object { $_.Name -match '^35(\.|$)' } |
    Sort-Object Name -Descending |
    Select-Object -First 1
& (Join-Path $buildTools35.FullName 'apksigner.bat') verify --verbose --print-certs $androidApk
Get-FileHash -Algorithm SHA256 -LiteralPath $androidApk
```

## Part 5: Publish the exact artifacts as a prerelease

The non-publishing run creates or confirms the local annotated tag only after artifact verification. An explicitly approved `-Publish` run may then push source/tag state and change GitHub. It verifies the exact tag and commit, creates or reconciles the draft, uploads the expected assets in deterministic order, waits for each matching remote digest, and exposes only a prerelease. Do not issue direct `gh release create/edit/upload` commands for production publication.

A Desktop-scoped prerelease has exactly:

- `mobile-egress-windows-<version>.zip`;
- `mobile-egress-client.exe`; and
- `mobile-egress-macos-<version>-arm64.pkg`.

An interim Windows-scoped prerelease has exactly the Windows ZIP and EC2 Client; when Android is also selected, it additionally has `zfnf-mobile-egress-android-<version>.apk`. It never includes a macOS PKG. The Mac verification JSON is local/private evidence and is never uploaded.

Managed release notes always render four Downloads entries in order: Windows controller, EC2 Client, macOS controller PKG, Android Agent APK. A Windows-scoped release with no eligible Mac artifact marks macOS as deferred pending Apple Developer Program enrollment. A normal Desktop release keeps its first three assets on the same tag; Android may point to a different eligible release.

Existing uploaded assets must form the expected verified set; published tags/assets are immutable and never replaced or clobbered. If a GitHub outcome is unknown, inspect/reconcile the exact draft/assets before retrying. Use GitHub Releases to download the candidate on the acceptance Windows PC, Mac, and phone. Test those downloaded artifacts, not build-directory copies.

## Part 6: Required physical acceptance

### Windows/Android regression acceptance

Use a release candidate with:

- one Windows 10/11 controller PC;
- one Android 10+ phone with working cellular data;
- a Tailscale account allowed to enable Funnel;
- two running x86-64 Windows Server 2019 EC2 instances in `us-east-1`;
- SSM reachability and outbound HTTPS/DNS from both EC2 instances; and
- AWS permission to inventory the instances, run SSM commands, and perform the guarded IAM actions described below.

No relay EC2, public EC2 IP, inbound security-group rule, Elastic IP, router change, or local port-forward is required. Private EC2 nodes need NAT or appropriate VPC endpoints for SSM and the signed GitHub Client download.

Copy [the acceptance record template](templates/physical-acceptance-record.md) outside the source tree and fill it in as you go. Do not record QR contents, credentials, relay/device certificates, destinations, carrier/EC2 IP addresses, or traffic payloads.

#### 6.1 Verify and install the downloaded artifacts

On the controller PC:

1. Verify the ZIP hash against the release record.
2. Obtain the publisher's SHA-256 certificate fingerprint from the separately shared trusted channel; do not treat a value in the release itself as that identity check.
3. Extract it into one directory; do not separate the setup application or sibling executables.
4. Optionally inspect the exact setup's Windows signature through **Properties → Digital Signatures** or the trusted system PowerShell check above, and compare it with the separately shared publisher identity. Then double-click setup, answer **Yes** after comparing its fingerprint reminder, and approve the one UAC prompt. **Unknown publisher** and **More info → Run anyway** may appear; self-signing does not suppress SmartScreen.
5. Run `Get-AuthenticodeSignature` on every extracted `.exe` and require `Valid` with the recorded signer thumbprint after setup established the trusted publisher.

On Android:

1. Verify the APK hash and public signer digest against the release record.
2. Install the APK through your approved sideloading process.
3. Confirm Android identifies it as Mobile Egress and does not report a signing mismatch.

#### 6.2 Set up the local bridge and Agent

1. In **Bridge**, choose **Install Tailscale** only when the status is **Not installed**, then approve UAC. If Tailscale is already present, the controller shows **Installed · not connected** instead of offering another MSI installation.
2. Choose **Connect Tailscale** while installed/offline and finish browser login. This starts login and unattended mode without rerunning the installer. Once the status is **Online**, choose **Set up local bridge**. On the first Funnel setup, the controller automatically opens Tailscale's official Funnel approval page while the hidden CLI waits; approve it, then approve relay UAC. Require Funnel active, relay healthy, and a `https://<machine>.<tailnet>.ts.net:8443` public origin.
3. Confirm Windows Defender Firewall/router settings were not manually opened for port 8443.
4. In **Agent**, generate the Agent QR. Scan it in the compatible Android or iOS app and start the Agent.
5. Keep Wi-Fi enabled on the Agent device while cellular data is also enabled. Require the Agent UI or notification, as appropriate for the platform, to show cellular available and relay connected.
6. On Windows, confirm the relay is automatic/running and loopback-only:

```powershell
Get-Service -Name 'MobileEgressRelay' | Select-Object Name, Status, StartType
Get-NetTCPConnection -State Listen -LocalPort 8443 | Select-Object LocalAddress, LocalPort, OwningProcess
```

The only relay listener must be `127.0.0.1:8443`.

#### 6.3 Connect AWS and install two Clients

1. In **AWS Login**, use the default IAM user access-key path. Choose **Create IAM user** to open the `us-east-1` IAM user creation page. A beginner may sign in to the AWS Console as root only to create an IAM user named `mobile-egress`; root credentials are for console setup only.
2. Create an access key for the `mobile-egress` IAM user and paste that IAM user's access key into Mobile Egress. Credentials are encrypted with Windows DPAPI. Never create or paste root access keys.
3. IAM Identity Center remains available under **Advanced** for users who already have it. If it must be set up, use root only in the browser to enable IAM Identity Center, choose **Single-Region instance** in **US East (N. Virginia)**, create an Identity Center user, assign that user access to the AWS account, and copy the **AWS access portal URL** into Mobile Egress. Do not paste the root login, an EC2 console URL, or the Identity Center management URL into the Start URL field.
4. Refresh **EC2 Nodes** and confirm only the intended supported `us-east-1` instances appear.
5. For a profile-less node, choose **Prepare SSM** and allow the dedicated role/profile creation. The controller retries only AWS propagation-class attachment errors with bounded backoff for up to one minute, rechecking before every attempt that no other profile appeared. Permission and unrelated-profile errors stop immediately.
6. For an existing non-SSM role, read the displayed role name and explicitly approve adding only `AmazonSSMManagedInstanceCore`. Never approve profile replacement.
7. Let the controller check the selected node for 30 seconds. If SSM registers, it starts **Install Client** automatically. If registration remains absent, the node card explains that the already-running Agent may still have stale credentials and offers **Restart EC2 and continue**. Read the interruption warning and explicitly confirm; the app calls the EC2 reboot API for only that instance, then waits up to five minutes for an online SSM record whose last ping is newer than the restart request. It installs the Client only after that fresh ping. Reboot is never automatic and does not terminate, stop/start, or recreate the instance. The activity panel records whether SSM was unregistered, offline, stale after reboot, or ready, plus the Agent version and last-ping timestamp when AWS supplies them. On later runs, an online node shows **SSM ready** and bypasses profile setup. Use **Install Client** manually only to retry an interrupted install. The first successful install adds only the manifest-embedded exact publisher certificate to the node's LocalMachine Root and TrustedPublisher stores; subsequent install/update/repair is idempotent. A failed SSM release command exposes only an approved stage label in the banner, never raw PowerShell stderr.
8. Require distinct Client serials and credentials. Do not paste credentials into SSM, tickets, chat, or the acceptance record.

On each EC2 node, confirm the service and listener through an interactive administrative PowerShell session:

```powershell
Get-Service -Name 'MobileEgressClient' | Select-Object Name, Status, StartType
Get-NetTCPConnection -State Listen -LocalPort 1080,1081 | Select-Object LocalAddress, LocalPort, OwningProcess
```

The service must be automatic/running. Its only proxy listeners must be SOCKS5 at `127.0.0.1:1080` and HTTP forward/CONNECT at `127.0.0.1:1081`. They are application opt-ins on that EC2 node, not controller-host, system-wide, VPN, public, UDP, or QUIC proxies. Confirm the Windows system proxy, default route, and EC2 security groups are unchanged before and after setup.

#### 6.4 Prove opt-in cellular egress on both nodes

In the controller, choose **Copy proxy line** for node A. Client versions older than `1.0.24` show update guidance instead; choose **Update** and wait for the node card to refresh. Transfer the value only into that node's intended workload or private RDP clipboard. For short ordinary-HTTP and HTTPS-through-CONNECT curl tests, parse it from the clipboard so the secret is not written into PowerShell history:

```powershell
$proxyParts = (Get-Clipboard).Trim().Split(':', 4)
if ($proxyParts.Count -ne 4) { throw 'The copied proxy line is not IP:PORT:USERNAME:PASSWORD.' }
$nodeProxy = "http://$($proxyParts[2]):$($proxyParts[3])@$($proxyParts[0]):$($proxyParts[1])"
$previousAllProxy = $env:ALL_PROXY
$directAddress = (& curl.exe --fail --silent --show-error --noproxy '*' 'https://checkip.amazonaws.com').Trim()
if ($LASTEXITCODE -ne 0) { throw 'The direct egress check failed.' }
try {
    $env:ALL_PROXY = $nodeProxy
    $proxiedHTTPAddress = (& curl.exe --fail --silent --show-error 'http://checkip.amazonaws.com').Trim()
    if ($LASTEXITCODE -ne 0) { throw 'The ordinary HTTP proxy check failed.' }
    $proxiedHTTPSAddress = (& curl.exe --fail --silent --show-error 'https://checkip.amazonaws.com').Trim()
    if ($LASTEXITCODE -ne 0) { throw 'The HTTPS CONNECT proxy check failed.' }
    if ([string]::IsNullOrWhiteSpace($proxiedHTTPAddress) -or [string]::IsNullOrWhiteSpace($proxiedHTTPSAddress) -or
        $directAddress -eq $proxiedHTTPAddress -or $directAddress -eq $proxiedHTTPSAddress) {
        throw 'Both proxy modes must demonstrate cellular egress distinct from the direct route.'
    }
    Write-Host 'PASS: direct and proxied egress differ; values intentionally not printed.'
} finally {
    if ($null -eq $previousAllProxy) {
        Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
    } else {
        $env:ALL_PROXY = $previousAllProxy
    }
    Set-Clipboard -Value ''
    Remove-Variable proxyParts, nodeProxy, previousAllProxy, directAddress, proxiedHTTPAddress, proxiedHTTPSAddress -ErrorAction SilentlyContinue
}
```

Choose **Copy proxy line** again, paste it directly into a Refract proxy list on that same EC2 node, and run Refract's proxy test. For SOCKS regression coverage, choose **Copy SOCKS5 URL** and repeat the existing `socks5h://` curl check. Repeat with node B's own credentials. While both proxied requests work, run a direct request on each node and confirm it still uses its normal EC2 route. This proves per-application opt-in rather than controller-host, system-wide, VPN, public, UDP, or QUIC proxy behavior.

The two-node run proves simultaneous multi-Client routing, but two Client identities can open only 64 streams because each is capped at 32. The 256-stream aggregate is covered by automated tests. A physical 256-stream run requires eight holding Client identities with 32 held-open streams each, plus a ninth legitimate probe identity to attempt aggregate stream 257; treat it as a separate capacity gate, not a claim that two nodes can reach 256.

To test the per-Client cap physically, use a controlled HTTPS endpoint that deliberately holds connections. Start 32 transfers through one Client identity and hold them open; its 33rd concurrent stream must fail closed with `client_stream_limit`. Do not use an uncontrolled third-party large download or record the proxy URL. Stop the held transfers afterward.

### 6.4a Prove the 256-stream capacity gate on each desktop bridge

Record Windows-hosted and macOS-hosted bridge results separately and leave each `PENDING` unless it was actually run. Follow the [authenticated 256-stream acceptance runbook](capacity-acceptance.md) against a dedicated, resettable relay and a WebPKI-valid TLS 1.3 echo target. For each host, require eight legitimate holding Client identities with 32 streams each. Every stream must first verify an exact 16 KiB echo and then remain live for 15 minutes. With all 256 held, the ninth legitimate probe identity's first stream must fail with `agent_stream_limit`; after one held stream closes, one replacement must open and verify successfully. Capture the runbook's sanitized before/during/after process, memory, socket, queue-overflow, and relay-health observations; the final harness JSON alone is insufficient. Fail the gate for corruption, restart, queue overflow, continuously growing memory, or leaked sockets. This is a capacity/liveness gate, not a benchmark: there is no throughput floor. Senders prefer 16 KiB data frames, while valid 32 KiB data frames remain accepted.

iOS 256-stream physical acceptance remains `unverified—no device`; defer TestFlight promotion until it is run on signed hardware. Do not substitute package, unsigned-build, simulator, Archive, or upload evidence.

#### 6.5 Prove cellular-only fail-closed behavior

1. Leave phone Wi-Fi connected.
2. Disable cellular data on the phone without stopping Wi-Fi.
3. Require the Agent to report loss/offline, existing proxied streams to close, and new proxied requests to fail.
4. Confirm an ordinary direct EC2 request still works; this separates Mobile Egress failure from an EC2 outage.
5. Re-enable cellular, wait for the Agent to reconnect, and confirm both node proxies recover.

If proxy traffic succeeds over phone Wi-Fi while cellular is disabled, fail the release.

#### 6.6 Prove reboot and Repair recovery

Test one dependency at a time so the failed component is unambiguous:

1. Reboot the controller PC. Tailscale unattended mode and `MobileEgressRelay` must return automatically; reopen the controller UI and confirm the bridge becomes ready without new Owner/Agent/Client identities.
2. Reboot EC2 node A, then node B. `MobileEgressClient` must return automatically, restore both loopback listeners, and retain the same serial/proxy credentials.
3. Reboot the selected Agent device. On Android, the Agent is intentionally user-started and `START_NOT_STICKY`; open the app and tap **Start**. On iOS, reopen the app and start the configured Agent connection. In either case, confirm the same enrollment reconnects.
4. Stop `MobileEgressClient` on one test node, choose **Repair** in the controller, and require the signed executable/configuration reapply to restore the service without changing serial or credentials.

To prove **Update**, start the lab from an earlier signed candidate, then open the controller from this candidate and choose **Update**. For a first-ever release, create a lower-version acceptance prerelease from the same reviewed commit before installing the final candidate. Both versions need their own immutable tags/assets; never overwrite one release with the other.

#### 6.7 Prove endpoint migration

Tailscale derives the MagicDNS/Funnel FQDN from the device machine name. Use the supported rename control instead of deleting the Tailscale node:

1. Record only the original machine name, not QR/certificate data.
2. In the Tailscale admin **Machines** page, open the controller PC's menu, choose **Edit machine name**, disable automatic OS-hostname generation if shown, and add a temporary `-migration-test` suffix.
3. Return to Mobile Egress and wait for **Rotation required**.
4. Connect AWS, choose **Rotate endpoint safely**, and approve UAC.
5. Require both EC2 nodes to appear in the updated list. Use **Repair** for a failed node after SSM returns.
6. Stop the Agent, scan the distinct migration QR in the compatible app, and restart the Agent.
7. Confirm both workloads reconnect with unchanged Client serials, Agent identity, and proxy credentials.
8. Rename the Tailscale machine back to its original name and repeat the rotation/migration once more so the accepted release finishes on its intended FQDN.

Tailscale documents that editing a machine name changes its MagicDNS domain: [Machine names](https://tailscale.com/kb/1098/machine-names) and [MagicDNS](https://tailscale.com/docs/features/magicdns). Do not regenerate the tailnet DNS name or delete/re-enroll the node merely to test migration.

#### 6.8 Review redaction and cloud/network boundaries

Before signing off:

- inspect SSM command history and require only signed-release metadata, public publisher DER/fingerprints, public bootstrap CSR/key output, sealed ciphertext, and fixed success/error output;
- confirm no raw proxy password, private key, pairing capability, or plaintext node configuration appears;
- confirm no EC2 instance, Elastic IP, public IP, or inbound rule was created/changed by Mobile Egress;
- confirm Windows relay/node state directories remain ACL-restricted to SYSTEM and local Administrators; and
- save only aggregate pass/fail results in the acceptance record.

Inspect ACL entries without printing state contents. Run the relay command on the controller PC and the Client command on each EC2 node:

```powershell
(Get-Acl -LiteralPath 'C:\ProgramData\MobileEgress\Relay').Access |
    Select-Object IdentityReference, FileSystemRights, AccessControlType, IsInherited
(Get-Acl -LiteralPath 'C:\ProgramData\MobileEgress\Client').Access |
    Select-Object IdentityReference, FileSystemRights, AccessControlType, IsInherited
```

Each applicable directory must have inheritance disabled and grant access only to SYSTEM and local Administrators. The relay directory is expected only on the controller; the Client directory is expected only on an EC2 node.

### Future macOS controller acceptance

A future coupled Mac Desktop candidate requires the separate Mac suite on the available macOS 26.2 Apple-Silicon Mac with one real Windows Server 2019 EC2 Client. This does not replace or reduce the Windows/two-node regression above. Every Mac row in the [acceptance template](templates/physical-acceptance-record.md#future-macos-controller) starts `NOT RUN` and blocks promotion of that later Mac-bearing release until passed.

Use the quarantined PKG downloaded from GitHub, not a local build. Verify its filename/hash, Developer ID Installer signature, notarization/staple, arm64 controller and relay, macOS 13.0 deployment target, hardened runtime, identifiers, and app layout. Install normally with Apple Installer; do not bypass Gatekeeper.

Start with empty Mac controller/relay state and import no Windows private state. Exercise guided standalone Tailscale installation, system-extension/VPN approval, browser login, raw Funnel 8443, and an already-installed authentic standalone or App Store variant. Exercise `not-registered` and `approval-required`, confirm Login Items opens and no Owner exists before status reaches `enabled`, then complete Setup.

Prove Keychain and relay continuity across close/reopen, quit/relaunch, reboot/login, Repair, and a same-Mac upgrade. Build the private signed/notarized `1.0.999` fixture directly with `scripts/release-macos.sh` from the final source and node manifest, not through the Desktop release command; it is acceptance evidence only and receives no Git tag or GitHub asset. Clean-install-only excludes Windows-to-Mac migration, not this fixture-based upgrade check.

Pair Android, install one real Windows Server 2019 EC2 Client, and prove HTTP, HTTPS CONNECT, and SOCKS5 cellular egress while the direct route remains unchanged. Then prove cellular-loss fail closed/recovery, endpoint rotation, Client Update, relay Repair/restart, and identity/credential preservation. Logging out the controlling Mac account must fail traffic closed; logging back in and reopening must recover the same identities. Review sanitized UI/activity/IPC/SSM output for secret leakage.

## Part 7: Promote or reject the release

For the interim `v1.1.0` Windows-and-Android prerelease, promotion requires its exact immutable artifacts and the preserved Windows/Android regression record. A later Mac-bearing release separately requires the available-Mac controller record and matching production signing/notary/SSH/GitHub evidence. If every gate applicable to a release passes, attach the completed sanitized records to private release evidence and promote the exact tested prerelease without replacing its assets:

```powershell
gh release edit $releaseTag --repo 'cbjjensen/mobile-egress' --prerelease=false --latest
gh release view $releaseTag --repo 'cbjjensen/mobile-egress' --json tagName,isDraft,isPrerelease,url,assets
```

If a required item fails or remains `NOT RUN`, do not promote it. Record the finite failure class, fix the source, increment `versionCode`/version as applicable, create a new tag through the guarded orchestrator, rebuild, and repeat. Never reuse the failed tag or replace its published assets.

## Minimum AWS permissions

The selected identity needs these read/command actions, scoped to the account, region, and selected nodes where AWS supports resource constraints:

- `ec2:DescribeInstances`, `ec2:DescribeImages`, and `ec2:RebootInstances` (used only after explicit recovery confirmation);
- `ssm:DescribeInstanceInformation`, `ssm:SendCommand`, and `ssm:GetCommandInvocation`; and
- `iam:GetInstanceProfile`, `iam:GetRole`, `iam:ListAttachedRolePolicies`, and `iam:ListRolePolicies`.

If the app must prepare SSM for a profile-less instance, the identity also needs the narrowly scoped create/tag/add/associate actions used for the deterministic dedicated role/profile: `iam:CreateRole`, `iam:TagRole`, `iam:CreateInstanceProfile`, `iam:TagInstanceProfile`, `iam:AddRoleToInstanceProfile`, `iam:AttachRolePolicy`, `iam:PassRole`, and `ec2:AssociateIamInstanceProfile`. If an instance already has a non-SSM role, only the explicitly confirmed `iam:AttachRolePolicy` change is used; the app never replaces the profile.

Have the AWS account administrator translate this action list into the organization's resource ARNs, permission boundaries, SCPs, and session policy. The app itself is fixed to `us-east-1`, supported Windows instances, and `AmazonSSMManagedInstanceCore`.

## Rollback and state recovery

Normal code rollback preserves `C:\ProgramData\MobileEgress\Relay` and the relay CA. Install a previously accepted signed controller bundle and relay binary only after confirming protocol/schema compatibility. Never restore stale SQLite state as a code rollback because it can reverse revocation or capability consumption.

Back up the entire relay state directory as one unit while `MobileEgressRelay` is stopped, preserving ACLs. The backup contains the CA private key and is as sensitive as live state. If the CA/state, sole Owner identity, Android signing key, or Windows signing key is lost or compromised, stop the affected release/service path and perform a reviewed trust or signing-key recovery. Endpoint rotation is not compromise recovery.

On macOS, normal signed PKG update/repair preserves `/Library/Application Support/ZFNF Mobile Egress/Relay` plus the controlling user's Keychain items. Do not unregister/reregister merely to upgrade, restore stale relay state, export Keychain secrets to files, or import Windows DPAPI/relay private state. If relay CA/state, Owner Keychain state, or a Developer ID identity may be compromised, preserve restricted evidence and perform a reviewed trust reset; endpoint rotation and Tailscale relogin are insufficient.
