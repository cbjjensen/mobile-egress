# Mac build server over SSH

This runbook covers the Windows-orchestrated macOS Desktop build/sign/notary path and preserves a separate development path for the future Mobile Egress iOS Agent. Windows remains the main editing/publishing workstation; the Apple Silicon Mac produces the macOS PKG and, in the future, iOS builds.

Production Desktop distribution requires Apple Developer Program membership, approved Developer ID Application/Installer identities, a matching distribution profile, and notary credentials. The future iOS section still begins with free simulator/local proof work; TestFlight, App Store, Ad Hoc distribution, and production entitlements require the applicable paid enrollment.

## Known hosts

Desktop releases use the ignored/untracked PowerShell data file `.local/mac-build-server/release-desktop.psd1`. It has exactly eight keys: `SshTarget`, `SshKeyPath`, `RepositoryPath`, `TeamID`, `ApplicationIdentity`, `InstallerIdentity`, `NotaryKeychainProfile`, and `ProvisioningProfilePath`. `ssh` and `scp` use the configured key and the standard OpenSSH `known_hosts`.

The following values are retained for the separate future iOS development path:

- Windows workstation: `Raidmax-Fix`
- Windows Wi-Fi address on `Rockchalk`: `10.0.0.55`
- Mac build host: `Y9YD7JN54M.local`
- Mac LAN address: `10.0.0.77`
- Mac user: `diana`
- SSH target: `diana@10.0.0.77`
- Project-local ignored SSH key: `.local/mac-build-server/id_ed25519`
- SSH setup skill: `.agents/skills/mobile-egress-mac-build-server`

## Secret boundary

The Desktop PSD1 and SSH private key live under ignored `.local/` and must remain untracked. Do not commit, paste, release, or log them. Developer ID/iOS signing private keys, provisioning profiles, Apple/notary credentials, and export options with team-specific values are also private.

Before using the project-local key, verify:

```powershell
git check-ignore -q -- .local/mac-build-server/release-desktop.psd1 .local/mac-build-server/id_ed25519
if ($LASTEXITCODE -ne 0) { throw 'Mac Desktop config or SSH private key is not ignored.' }
git ls-files -- .local/mac-build-server/release-desktop.psd1 .local/mac-build-server/id_ed25519
```

The second command must print nothing.

## One-time SSH setup

From the repository root on Windows, create or inspect the project-local key:

```powershell
& .\.agents\skills\mobile-egress-mac-build-server\scripts\setup-windows-ssh-key.ps1
```

The script prints the public key and a Mac-side command that appends it to `~/.ssh/authorized_keys`.

On the Mac, Remote Login must be enabled:

```bash
sudo systemsetup -setremotelogin on
```

Verify from Windows:

```powershell
Test-Connection -ComputerName 10.0.0.77 -Count 2
Test-NetConnection -ComputerName 10.0.0.77 -Port 22 -InformationLevel Detailed
ssh -i .\.local\mac-build-server\id_ed25519 diana@10.0.0.77 hostname
```

Expected SSH output:

```text
Y9YD7JN54M.local
```

## Mac prerequisites

### Desktop release

The Mac must be Apple Silicon. The release bootstrap installs the pinned user-local Go 1.26.7, Node 24.20.0, and Wails 2.14.0 toolchain from `windows-client/macos/toolchain.lock` under the dedicated build root without Homebrew.

The Mac Keychain must contain the configured Developer ID Application and Installer identities. The distribution profile must authorize `com.cbjjensen.mobile-egress.controller` and its Keychain access group, and the configured `notarytool` profile must work. These production prerequisites require Apple Developer Program enrollment.

### Future iOS development

Install full Xcode on the Mac, not just Command Line Tools. After installation, open Xcode once and approve any additional component prompts.

The selected developer directory must point at full Xcode:

```bash
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
sudo xcodebuild -license accept
xcodebuild -version
```

Install at least one iOS simulator runtime in Xcode:

```text
Xcode -> Settings -> Platforms
```

For physical-device builds, add an Apple ID in Xcode:

```text
Xcode -> Settings -> Accounts
```

A free Apple ID is enough for simulator work and may be enough for local installs to your own device. TestFlight, App Store, Ad Hoc sharing, and some entitlements require paid Apple Developer Program membership.

## Readiness check from Windows

Desktop readiness is checked by `release-desktop.ps1` from the PSD1. The commands below remain useful for future iOS diagnostics:

```powershell
$mac = 'diana@10.0.0.77'
$key = '.\.local\mac-build-server\id_ed25519'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 $mac 'hostname; sw_vers; xcode-select -p; xcodebuild -version'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 $mac 'xcrun --sdk iphoneos --show-sdk-version; xcrun --sdk iphonesimulator --show-sdk-version; xcrun simctl list runtimes available | head -n 40'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 $mac 'xcrun simctl list devices available | head -n 80'
ssh -i $key -o BatchMode=yes -o ConnectTimeout=5 $mac 'security find-identity -v -p codesigning 2>/dev/null'
```

Simulator build readiness requires:

- SSH succeeds without a password prompt.
- `xcode-select -p` prints `/Applications/Xcode.app/Contents/Developer`.
- `xcodebuild -version` prints the installed Xcode version.
- `iphoneos` and `iphonesimulator` SDK versions print successfully.
- `simctl list runtimes available` shows at least one iOS runtime.
- `simctl list devices available` shows at least one iPhone simulator.

Device, TestFlight, App Store, or Ad Hoc readiness additionally requires a valid Apple signing identity and provisioning path.

## Current verified state

As of the initial iOS setup, Windows reached the Mac with the project-local key, Xcode 26.6 was selected, iOS SDK 26.5 was present, and the iOS 26.5 simulator runtime listed devices. This is not current Desktop release evidence.

The remaining known gaps from the last check were:

- no valid code-signing identities reported by `security find-identity -v -p codesigning`.

That state is enough for future iOS source/simulator work. It does not prove Developer ID identities, the distribution profile, or notarization readiness.

## Source sync model

### Desktop release

The Windows build freezes the source commit and raw Windows node manifest first. The Desktop orchestrator transfers one exact-commit Git bundle plus that manifest, builds the detached Mac checkout at the same commit, and invokes `scripts/release-macos.sh`.

### Future iOS development

For non-production iOS development, use Git as the source-of-truth transfer mechanism. The Mac should have its own checkout of this repository, on the same branch and commit as the Windows worktree.

Create the initial Mac checkout:

```powershell
$mac = 'diana@10.0.0.77'
$key = '.\.local\mac-build-server\id_ed25519'
ssh -i $key $mac 'mkdir -p ~/workspace'
ssh -i $key $mac 'cd ~/workspace && git clone https://github.com/cbjjensen/mobile-egress.git mobile-egress'
```

After Windows work is committed and pushed, update the Mac checkout:

```powershell
$branch = git branch --show-current
$commit = git rev-parse HEAD
$mac = 'diana@10.0.0.77'
$key = '.\.local\mac-build-server\id_ed25519'
ssh -i $key $mac "cd ~/workspace/mobile-egress && git fetch origin && git checkout $branch && git pull --ff-only && git rev-parse HEAD"
```

The printed Mac commit should match `$commit`. If it does not, stop before building.

If the branch is local-only and cannot be pushed yet, create a patch bundle or use `git bundle`; do not copy ad hoc source directories over SSH because that makes builds hard to reproduce.

## Desktop release orchestration

The public entry point is:

```powershell
& .\scripts\release-desktop.ps1 -ReleaseVersion '1.1.0'
```

Without `-Publish`, this command still builds/signs both platforms and freezes a local annotated tag. Add `-Publish` only to push source/tag state and change GitHub.

The Windows stage passes its exact node manifest and source commit. The Mac returns:

```text
mobile-egress-macos-<version>-arm64.pkg
mobile-egress-macos-<version>-arm64.verification.json
```

Windows validates the returned record and compares the remote/local PKG SHA-256. Only the PKG is a GitHub asset; the verification JSON stays private/local.

## Future iOS build commands

The iOS project does not exist yet. When it is added, keep it under `ios/` and update this section with the real workspace, scheme, bundle identifier, and simulator destination.

Expected free simulator build shape:

```powershell
$mac = 'diana@10.0.0.77'
$key = '.\.local\mac-build-server\id_ed25519'
ssh -i $key $mac 'cd ~/workspace/mobile-egress && xcodebuild -workspace ios/<MobileEgress>.xcworkspace -scheme <MobileEgress> -configuration Debug -sdk iphonesimulator -destination "platform=iOS Simulator,name=<iPhone Simulator Name>" build'
```

If the project uses `.xcodeproj` instead of `.xcworkspace`:

```powershell
ssh -i $key $mac 'cd ~/workspace/mobile-egress && xcodebuild -project ios/<MobileEgress>.xcodeproj -scheme <MobileEgress> -configuration Debug -sdk iphonesimulator -destination "platform=iOS Simulator,name=<iPhone Simulator Name>" build'
```

Expected local device build shape with a free Personal Team:

```powershell
ssh -i $key $mac 'cd ~/workspace/mobile-egress && xcodebuild -project ios/<MobileEgress>.xcodeproj -scheme <MobileEgress> -configuration Debug -destination "generic/platform=iOS" -allowProvisioningUpdates build'
```

Archive/export shape for paid distribution, only after explicit approval and signing setup:

```powershell
ssh -i $key $mac 'cd ~/workspace/mobile-egress && xcodebuild -project ios/<MobileEgress>.xcodeproj -scheme <MobileEgress> -configuration Release -destination "generic/platform=iOS" archive -archivePath build/ios/MobileEgress.xcarchive'
ssh -i $key $mac 'cd ~/workspace/mobile-egress && xcodebuild -exportArchive -archivePath build/ios/MobileEgress.xcarchive -exportPath build/ios/export -exportOptionsPlist ios/ExportOptions.plist'
```

Do not commit `ExportOptions.plist` if it contains team-specific signing values or provisioning profile names that should remain local.

## What can be proved before paying Apple

Before buying Apple Developer Program membership, use simulator and local development builds to prove the risky iOS Agent questions:

- pairing QR parsing and enrollment payload compatibility;
- relay protocol compatibility and mTLS identity handling;
- secure local identity storage;
- whether iOS can bind the relay path to cellular using native networking APIs;
- how long the app can keep the relay session alive under foreground, locked-screen, and background conditions;
- whether the architecture needs Network Extension entitlements.

Only pay after those questions justify friend distribution or advanced entitlements.

## Troubleshooting

`Y9YD7JN54M.local` does not resolve:

Use `10.0.0.77`, then check the Mac's current address:

```bash
ipconfig getifaddr en0
```

SSH says `Permission denied`:

Run the setup script again on Windows and make sure the printed public key is present in the Mac user's `~/.ssh/authorized_keys`.

`xcode-select` points at `/Library/Developer/CommandLineTools`:

Run this on the Mac:

```bash
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
```

`xcodebuild` says the license is not accepted:

Run this on the Mac:

```bash
sudo xcodebuild -license accept
```

`simctl` is missing or no devices are listed:

Open Xcode on the Mac, approve additional components, then install an iOS simulator runtime in `Xcode -> Settings -> Platforms`.

`security find-identity` reports `0 valid identities found`:

Simulator builds can continue. For device or distribution builds, add an Apple ID in Xcode settings and configure a development team. TestFlight, App Store, and Ad Hoc distribution require paid Apple Developer Program membership.

The Mac checkout is stale:

Do not build stale source. Fetch, checkout the intended branch, and require the Mac commit to match the Windows commit before running `xcodebuild`.
