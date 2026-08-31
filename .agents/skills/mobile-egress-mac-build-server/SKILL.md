---
name: mobile-egress-mac-build-server
description: Use when connecting this Windows Mobile Egress workspace to the local Mac build server for iOS development, SSH setup, Xcode builds, or troubleshooting Windows-to-macOS build access.
---

# Mobile Egress Mac build server

## Current local topology

Use the Mac for all iOS compile, signing, simulator, device, TestFlight, and App Store work. Windows can edit and orchestrate, but iOS artifacts must be produced on macOS with Xcode.

Known hosts from the initial setup:

- Windows dev workstation: `Raidmax-Fix`
- Windows Wi-Fi address on `Rockchalk`: `10.0.0.55`
- Mac build host: `Y9YD7JN54M`
- Mac mDNS/DNS name: `Y9YD7JN54M.local`
- Mac LAN address: `10.0.0.77`
- Mac user: `diana`
- SSH target: `diana@10.0.0.77`
- Project-local ignored SSH key: `.local/mac-build-server/id_ed25519`

Network addresses can change. Prefer the host name when it resolves, but fall back to the current IP during setup.

## Safety rules

The private SSH key and any Xcode signing credentials are local secrets. They may live under the ignored `.local/` directory in this checkout, but they must never be staged, committed, pasted into logs, or copied into tracked docs.

Before using the key or signing material, verify Git will ignore it:

```powershell
git check-ignore -q -- .local/mac-build-server/id_ed25519
if ($LASTEXITCODE -ne 0) { throw 'Mac build-server SSH private key is not ignored.' }
git ls-files -- .local/mac-build-server/id_ed25519
```

The second command must print nothing.

## Setup

From the repository root on Windows, create or inspect the project-local key:

```powershell
& .\.agents\skills\mobile-egress-mac-build-server\scripts\setup-windows-ssh-key.ps1
```

Add the printed public key to the Mac user's `~/.ssh/authorized_keys`. If password SSH is disabled, paste the command printed by the script into Terminal on the Mac.

On the Mac, Remote Login must be enabled:

```bash
sudo systemsetup -setremotelogin on
```

## Verification

From Windows, verify reachability:

```powershell
Test-Connection -ComputerName 10.0.0.77 -Count 2
Test-NetConnection -ComputerName 10.0.0.77 -Port 22 -InformationLevel Detailed
ssh -i .\.local\mac-build-server\id_ed25519 diana@10.0.0.77 hostname
```

Expected SSH output:

```text
Y9YD7JN54M.local
```

If `Y9YD7JN54M.local` resolves from Windows, this target is also acceptable:

```powershell
ssh -i .\.local\mac-build-server\id_ed25519 diana@Y9YD7JN54M.local hostname
```

## Build workflow

Keep source changes in Git and use SSH to trigger Mac-side commands. Clone or update the same branch on the Mac before building. Do not try to produce iOS release artifacts on Windows.

For build automation, add repo scripts that SSH to the Mac using the project-local key. Keep those scripts parameterized enough to tolerate IP changes, but default to the known local host above.

Stop and ask before installing software on the Mac, changing Xcode signing identities, changing Apple developer account state, or publishing anything to TestFlight/App Store.
