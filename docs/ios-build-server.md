# iOS build server over SSH

Windows is the Mobile Egress editing and orchestration workstation. The configured Mac performs every iOS compile, simulator, device, signing, archive, TestFlight, and App Store action. Do not attempt to produce iOS artifacts on Windows.

The current goal is reproducible development verification. A paid Apple Developer Program membership is not required for the portable Swift suites or unsigned compilation. It is required for TestFlight, App Store, Ad Hoc distribution, and likely any production-grade entitlement path.

## Known hosts

Current local setup:

- Windows workstation: `Raidmax-Fix`
- Windows Wi-Fi address on `Rockchalk`: `10.0.0.55`
- Mac build host: `Y9YD7JN54M.local`
- Mac LAN address: `10.0.0.77`
- Mac user: `diana`
- SSH target: `diana@10.0.0.77`
- Project-local ignored SSH key: `.local/mac-build-server/id_ed25519`
- SSH setup skill: `.agents/skills/mobile-egress-mac-build-server`

Local IP addresses can change. Prefer `Y9YD7JN54M.local` when it resolves; otherwise use the current Mac LAN address.

## Secret boundary

The SSH private key may live in the repository checkout under `.local/`, but `.local/` is ignored and must remain untracked. Never commit, print, copy, release, or place the private key in a report. Xcode signing identities, provisioning profiles, Apple credentials, and team-specific export options are local secrets too.

Before any SSH use, run this from the checkout that owns the key:

```powershell
git check-ignore -q -- .local/mac-build-server/id_ed25519
if ($LASTEXITCODE -ne 0) { throw 'Mac build-server SSH private key is not ignored.' }
git ls-files -- .local/mac-build-server/id_ed25519
```

The final command must print nothing. Do not use the key until both checks pass.

## One-time SSH setup

From the repository root on Windows, create or inspect the project-local key:

```powershell
& .\.agents\skills\mobile-egress-mac-build-server\scripts\setup-windows-ssh-key.ps1
```

The script prints the public key and a Mac-side command that appends it to `~/.ssh/authorized_keys`. On the Mac, Remote Login must be enabled:

```bash
sudo systemsetup -setremotelogin on
```

Verify reachability from Windows:

```powershell
Test-Connection -ComputerName 10.0.0.77 -Count 2
Test-NetConnection -ComputerName 10.0.0.77 -Port 22 -InformationLevel Detailed
ssh -i .\.local\mac-build-server\id_ed25519 -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes diana@10.0.0.77 hostname
```

Expected SSH output:

```text
Y9YD7JN54M.local
```

## Mac prerequisites

Install full Xcode on the Mac, not just Command Line Tools. After installation, open Xcode once and approve any additional component prompts. The selected developer directory must point at full Xcode:

```bash
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
sudo xcodebuild -license accept
xcodebuild -version
```

Install at least one iOS simulator runtime in `Xcode -> Settings -> Platforms`. For physical-device builds, add an Apple ID in `Xcode -> Settings -> Accounts` only after a separate decision to configure signing. A free Apple ID is sufficient for simulator work and may be sufficient for local installs to your own device. TestFlight, App Store, Ad Hoc sharing, and some entitlements require paid Apple Developer Program membership.

## Readiness check from Windows

The tracked verifier defaults to the configured local Mac host and account, but accepts `-MacHost`, `-MacUser`, and `-SshKeyPath` when the network changes. Its first remote action requires noninteractive SSH with the ignored key, `IdentitiesOnly=yes`, and a matching existing `known_hosts` entry through `StrictHostKeyChecking=yes`. Verify the Mac host key out of band before trusting it. The Mac needs full Xcode and the iPhoneOS SDK. The tracked package-test destination is `platform=macOS`, so this gate does not select a simulator model or require a simulator boot.

The following checks inspect readiness only. Do not install software, change the selected Xcode developer directory, accept licenses, add signing identities, change Apple account state, or publish a build as part of verification:

```powershell
$mac = 'diana@10.0.0.77'
$key = '.\.local\mac-build-server\id_ed25519'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes $mac 'hostname; sw_vers; xcode-select -p; xcodebuild -version'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes $mac 'xcrun --sdk iphoneos --show-sdk-version; xcrun --sdk iphonesimulator --show-sdk-version; xcrun simctl list runtimes available | head -n 40'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes $mac 'xcrun simctl list devices available | head -n 80'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes $mac 'security find-identity -v -p codesigning 2>/dev/null'
```

Simulator build readiness requires passwordless SSH, `/Applications/Xcode.app/Contents/Developer`, a working `xcodebuild -version`, both iPhone SDK versions, an available iOS runtime, and an available iPhone simulator. Device, TestFlight, App Store, or Ad Hoc readiness additionally requires a valid Apple signing identity and provisioning path; unsigned compilation evidence is not signing or TestFlight evidence.

## Current verified state

As of the initial setup, Windows can reach the Mac over SSH with the project-local key. Xcode 26.6 is installed and selected, iOS SDK 26.5 is present, and the iOS 26.5 simulator runtime has available iPhone/iPad devices. The last readiness check reported no valid code-signing identities from `security find-identity -v -p codesigning`.

That state supports source work and the unsigned checks for the tracked `ios/MobileEgressAgent.xcodeproj`. It does not produce signed device builds. Re-run the readiness check after any separately authorized signing configuration.

## Exact-tree verification

From a clean, committed Windows checkout:

```powershell
& .\scripts\test-ios.ps1 -UseMacBuildServer
```

The script requires a clean committed tree, records its exact `HEAD`, verifies the ignored key in its owning checkout, creates a temporary `git bundle` for that commit, transfers the bundle with strict host-key and identity selection, and creates a detached disposable checkout on the Mac. Mac-server mode runs both Swift suites directly in that checkout and does not require Windows Docker. The remote process verifies the checkout commit before running every phase:

1. `swift test`
2. `swift test -Xswiftc -warnings-as-errors`
3. `xcodebuild -list -project ios/MobileEgressAgent.xcodeproj`
4. An unsigned iPhoneOS `MobileEgressAgent` app-plus-extension build
5. `xcodebuild test -workspace . -scheme MobileEgressCore -destination platform=macOS` for the standalone package workspace

Only the final package-test phase retries, once, when output identifies a known `com.apple.testmanagerd.control` invalidation/unavailability and contains no concrete XCTest failure. A failed retry remains a failed Mac environment result. The transferred bundle and disposable checkout are removed after the command. Existing Mac checkouts, branches, and refs are not updated, so a local-only branch does not need a push for reproducible Mac verification.

## Source sync and project commands

Git remains the source-of-truth transfer mechanism for interactive Mac development. An optional persistent Mac checkout should be on the same branch and commit as the Windows worktree; stop before building when its `git rev-parse HEAD` differs from the Windows commit. For an exact local-only tree, prefer `scripts/test-ios.ps1 -UseMacBuildServer` instead of copying source directories over SSH.

The real tracked project is `ios/MobileEgressAgent.xcodeproj` with scheme `MobileEgressAgent`. The verification script uses this unsigned iPhoneOS command shape:

```bash
xcodebuild -project ios/MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build
```

The standalone Swift package remains at `ios/` and is checked with:

```bash
swift test
swift test -Xswiftc -warnings-as-errors
xcodebuild test -workspace . -scheme MobileEgressCore -destination "platform=macOS"
```

## Signing and distribution

This runbook does not authorize signing, Archive, TestFlight, App Store, device provisioning, or publishing. Those actions require a selected Apple team and explicit release approval. Follow [the iOS Agent guide](../ios/README.md) for entitlement, App ID/App Group/Keychain-group, Archive, TestFlight, and real-device acceptance requirements after that approval.

## What can be proved before paying Apple

Before buying Apple Developer Program membership, the portable tests can prove the platform-independent pairing, enrollment, protocol, policy, and bounded-runtime behavior covered by those suites. The unsigned Mac checks add compilation, build-parameter, target/scheme, package, and app-plus-extension configuration evidence. They do not prove entitled Secure Enclave or Keychain operation, actual cellular routing on iOS, a signed device build, TestFlight, or an App Store release.

## Troubleshooting

`Y9YD7JN54M.local` does not resolve:

Use `10.0.0.77`, then check the Mac's current address:

```bash
ipconfig getifaddr en0
```

SSH says `Permission denied`:

Run the setup script again on Windows and make sure the printed public key is present in the Mac user's `~/.ssh/authorized_keys`. Do not bypass the secret-boundary checks.

`xcode-select` points at `/Library/Developer/CommandLineTools`:

Run this on the Mac after confirming that changing the developer directory is authorized:

```bash
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
```

`xcodebuild` says the license is not accepted:

Run this on the Mac after confirming that accepting the license is authorized:

```bash
sudo xcodebuild -license accept
```

`simctl` is missing or no devices are listed:

Open Xcode on the Mac, approve additional components, then install an iOS simulator runtime in `Xcode -> Settings -> Platforms` after obtaining authorization to change the Mac.

`security find-identity` reports `0 valid identities found`:

Portable tests and unsigned builds can continue. Device or distribution work requires separate authorization to add an Apple ID, configure a development team, and establish the required signing path.

The final package test reports `com.apple.testmanagerd.control` invalidation:

The verifier retries that final phase once when no concrete XCTest failure is present. If the retry fails, record the Mac environment gate as failed and preserve the sanitized output. Do not restart or reconfigure Mac services as part of source verification.

The Mac checkout is stale:

Do not build it. Update it to the intended commit, or use `scripts/test-ios.ps1 -UseMacBuildServer`, which verifies an exact detached disposable checkout instead.
