# macOS Full Controller Parity Implementation Plan

## Specification

Implement the approved macOS Full Controller Parity design for ZFNF Mobile Egress. The first release is `v1.1.0`, clean-install-only, Apple Silicon, macOS 13+, and is coupled to the same-version Windows desktop/EC2 Client release.

## Global Constraints

- The macOS product replaces only the operator controller. Android Agent behavior and Windows Server 2019 EC2 Clients remain unchanged.
- Keep the implementation under the existing `windows-client` tree. Do not perform a repository-wide rename.
- Preserve every current Wails binding, including hidden DesktopAPI methods. Do not remove or rename existing fields or methods.
- Preserve all current Windows behavior, DPAPI storage, Windows Tailscale behavior, remote Windows provisioning, signing, and regression gates.
- `BridgeStatus.platform` is exactly `"windows" | "macos"`.
- `BridgeStatus.relayServiceState` is exactly `"not-required" | "not-registered" | "approval-required" | "enabled" | "version-mismatch" | "unavailable"`. Windows always reports `not-required`.
- macOS secure storage uses Security.framework Keychain APIs, service `com.cbjjensen.mobile-egress.controller`, SHA-256-hashed account names, generic-password items that are device-only and non-synchronizing, and no plaintext or file-encryption fallback.
- The relay bundle identifier/launchd label is `com.cbjjensen.mobile-egress.relay`; the controller bundle identifier is `com.cbjjensen.mobile-egress.controller`.
- The relay listens only on `127.0.0.1:8443`. State lives at `/Library/Application Support/ZFNF Mobile Egress/Relay` with root-only `0700` permissions. Admin IPC is `/var/run/com.cbjjensen.mobile-egress.relay.sock`, owned `root:admin`, mode `0660`.
- Relay admin protocol version is `1`. Frames are strict JSON, at most 512 KiB, with 128-bit request IDs, five-minute deadlines, unknown-field rejection, exact response-ID matching, peer-UID authorization, and allowlisted error codes. Only `status`, `setup`, `rotate`, and `repair` are exposed. Private Owner keys, AWS credentials, node metadata, raw daemon errors, and relay CA private keys never cross IPC.
- First relay setup binds the requesting administrator UID; later management accepts only that UID or root. Repair and upgrade preserve CA, identities, and state.
- macOS Tailscale guided install accepts only stable `Tailscale-<version>-macos.pkg` metadata from `https://pkgs.tailscale.com`, requires the companion SHA-256, enforces a 250 MiB bound, and verifies Team ID `W5364U7YZB` before opening Apple Installer. Installed standalone and App Store variants are accepted only at their fixed path with their expected bundle ID, Team ID, and valid signature. Guided install always uses standalone.
- Set `TAILSCALE_BE_CLI=1`; never pass Windows-only `--unattended` on macOS. Preserve status, login, official Funnel approval URL filtering, raw TCP port 8443, and endpoint rotation.
- The macOS app is arm64, minimum deployment target 13.0, hardened runtime, no App Sandbox, signed and notarized as a PKG named `mobile-egress-macos-1.1.0-arm64.pkg`.
- Pin Go 1.26.7, Node 24.20.0, and Wails 2.14.0 with URLs and SHA-256 hashes. Install under a dedicated user-local Mac build directory without Homebrew.
- Sign nested relay code, then app, then PKG using Developer ID Application/Installer identities. Notarize with `notarytool`; staple and verify with `codesign`, `pkgutil`, `spctl`, and `stapler`.
- Release orchestration builds/signs Windows controller and EC2 Client first, transfers that exact node manifest to the same-commit Mac checkout, retrieves the PKG and strict verification record over SSH, compares SHA-256, and uploads both desktop platforms together. Desktop platform selection is all-or-nothing. Preserve resumable drafts, immutable tags/assets, prerelease-first publication, and explicit `-Publish`.
- Production signing, notarization, publishing, software installation on the Mac, and physical acceptance are external/security-sensitive operations and must not be performed without explicit operator authorization and credentials.
- All behavioral production changes use test-driven development: add a focused test, capture the expected failure, implement minimally, and capture the passing result.

## Task 1: Extract the shared desktop controller and extend the UI contract

Move `DesktopApp`, all current Wails-bound methods, shared controller construction, AWS/node logic, QR flows, release-manifest handling, and lifecycle-independent behavior out of the Windows-tagged runner into platform-neutral Go files. Keep thin Windows and Darwin composition roots under `windows-client`. Add platform abstractions only where native window/tray/dialog/lifecycle behavior differs. Retain Windows behavior exactly.

Extend the backend and frontend `BridgeStatus` contract with `platform` and `relayServiceState` without removing or renaming anything. Windows returns `platform: "windows"` and `relayServiceState: "not-required"`. Add the Darwin Wails entrypoint/options and platform-aware Bridge UI copy/states for PKG installation, system-extension approval, Service Management, and Mac availability while preserving the four tabs, workflows, tray behavior, activity log, proxy-copy, and hidden APIs.

Add focused Go and frontend tests that fail before the extraction/contract change, then pass. Run package tests, frontend tests/typecheck/build, and the existing Windows regression suite before committing.

## Task 2: Add macOS Keychain secure storage

Implement the existing `securestore.Store` contract on Darwin through cgo and Security.framework APIs (not the `security` command). Use service `com.cbjjensen.mobile-egress.controller`; transform logical keys into lowercase SHA-256 hex account names. Store generic-password items with `kSecAttrSynchronizable=false` and a device-only accessibility class suitable for use while the operator session is available. Replace values atomically without deleting a valid old value before the replacement succeeds. Treat missing items as the store's established not-found result. Preserve stable signing identity access across controller upgrades and preserve DPAPI on Windows. Never fall back to plaintext or locally encrypted files.

Add Darwin integration tests for CRUD, missing values, non-synchronization attributes, atomic replacement, signing continuity, and cleanup, plus platform-neutral tests for account hashing/error mapping that can run on Windows. Capture RED/GREEN evidence and commit.

## Task 3: Add the privileged macOS relay service and versioned admin IPC

Implement internal relay-admin protocol version 1 with typed requests/responses for only `status`, `setup`, `rotate`, and `repair`. Enforce strict JSON/unknown-field rejection, 512 KiB maximum frames, 128-bit request IDs, exact ID matching, five-minute operation deadlines, replay resistance for in-flight/completed IDs, allowlisted error codes, and redacted public status/results.

Add the relay-side Unix socket server at `/var/run/com.cbjjensen.mobile-egress.relay.sock`. On Darwin authenticate peers with kernel peer credentials, require root or the recorded first-owner administrator UID, reject non-admin first setup, create state at `/Library/Application Support/ZFNF Mobile Egress/Relay` with `0700`, create the socket as `root:admin` `0660`, run the relay on `127.0.0.1:8443`, and preserve CA/identity/state through repair or version-compatible upgrades.

Bundle `mobile-egress-relay` and `com.cbjjensen.mobile-egress.relay.plist` in the app's Service Management locations. Add a Darwin controller registration adapter backed by `SMAppService` (macOS 13+) and map registration, approval, enabled, unavailable, and helper-version mismatch to the exact `relayServiceState` values. `SetupLocalBridge` registers first; when approval is pending it opens Login Items and returns without generating an Owner key. Setup resumes only after status reports enabled. Windows continues using the existing UAC/service path.

Add tests for every service state, helper mismatch, first-owner binding, root/admin/non-admin peers, malformed/unknown/oversized IPC, replay/mismatched IDs, timeouts, and redacted errors. Use Darwin-only integration tests where kernel/API behavior is required and portable protocol/state-machine tests elsewhere. Capture RED/GREEN evidence and commit.

## Task 4: Port Tailscale onboarding to macOS

Add a macOS stable-package metadata parser and installer flow. Accept only `https://pkgs.tailscale.com` package/checksum URLs and safe same-origin redirects, names matching `Tailscale-<version>-macos.pkg`, a companion SHA-256, and payloads no larger than 250 MiB. Verify the downloaded PKG signature and Team ID `W5364U7YZB`, open the verified package in Apple Installer, detect cancellation/completion with bounded polling, and validate `/Applications/Tailscale.app` before using its bundled CLI.

Discover the official standalone (`io.tailscale.ipn.macsys`) and App Store (`io.tailscale.ipn.macos`) variants only at fixed expected paths, requiring valid signatures and Team ID `W5364U7YZB`. Guided installation always selects standalone. Set `TAILSCALE_BE_CLI=1` for CLI execution. macOS `tailscale up` must omit `--unattended`. Preserve online/status parsing, browser login, official Funnel approval filtering, raw TCP `8443`, and endpoint rotation.

Add tests for package selection, origin/redirect/name rejection, checksum/signature/size failures, cancellation, both accepted bundle variants, forced CLI mode, macOS up arguments, and Funnel URL filtering. Preserve all existing Windows installer/controller tests. Capture RED/GREEN evidence and commit.

## Task 5: Add deterministic macOS build, signing, and PKG packaging

Add a tracked Mac toolchain lock containing exact URL/SHA-256 entries for Go 1.26.7, Node 24.20.0, and Wails 2.14.0. Add scripts that install these tools under a dedicated user-local build directory without Homebrew, verify hashes before extraction/use, and do not touch system-wide toolchains.

Add Darwin Wails/build configuration and branding assets, including an `.icns` app icon and PNG menu-bar icon derived from existing repository branding. Produce an arm64 app with deployment target 13.0, bundle ID `com.cbjjensen.mobile-egress.controller`, hardened runtime, and no App Sandbox. Bundle the signed relay executable/plist. Build a component PKG named from the requested version/architecture.

Add a signing/notarization script that verifies configured Developer ID Application and Installer identities, signs nested relay code then app then installer, submits with `notarytool`, staples, and emits a strict machine-readable verification record containing expected identities, architectures, deployment target, bundle IDs, plist layout, hardened runtime, PKG signature, notarization/staple results, and artifact SHA-256—never credentials/private material. Verification must run `codesign`, `pkgutil`, `spctl`, and `stapler` and fail closed.

Add unit/fixture tests for the lock parser, hash enforcement, bundle layout, signing-order plan, verification-record validation, and failure handling that run without real credentials. Capture RED/GREEN evidence and commit. Do not install tools or perform signing/notarization without explicit authorization.

## Task 6: Add coupled Windows/macOS desktop release orchestration

Add a Windows-orchestrated `release-desktop` path that builds/signs the Windows controller and EC2 Client first, passes the exact same-commit node manifest to the Mac checkout over the established SSH workflow, invokes the Mac build/sign/notarize path, retrieves the notarized PKG plus strict verification record, and compares local/remote SHA-256 before upload.

Extend `release-all.ps1` and its tests with macOS assets and desktop coupling. Selecting either Windows desktop or macOS desktop requires both at the same version; reject one-platform desktop releases. Keep Android selection independent where compatible. Preserve clean-main enforcement, immutable annotated tag/assets, resumable draft uploads with remote digest verification, prerelease-first publication, and explicit `-Publish`. The v1.1.0 desktop asset is exactly `mobile-egress-macos-1.1.0-arm64.pkg`.

Add fixture-driven PowerShell tests for selection rejection, manifest handoff, same-commit checks, verification-record validation, transfer hash mismatch, resume behavior, immutable assets, download entries, prerelease publication, and explicit publish gating. Capture RED/GREEN evidence and commit. Do not publish, push, tag, or invoke real signing without explicit authorization.

## Task 7: Update product, operations, security, and acceptance documentation

Update README quick starts/downloads and the architecture, security model, operations, deployment, status, Mac build-server, and physical-acceptance documentation for the macOS controller. Document Apple Silicon/macOS 13+, clean-install-only v1.1.0, root LaunchDaemon/SMAppService approval, Keychain boundary, logged-in-user Tailscale limitation and logout fail-closed behavior, Windows Server 2019 node continuity, coupled desktop release policy, GitHub PKG authority, signing/notary prerequisites, update/repair identity preservation, and no Windows-to-Mac migration/Intel/Mac App Store/automatic updates.

Add a recorded physical-acceptance template covering quarantined clean install and upgrade, daemon approval, Keychain persistence, Tailscale package/system extension/login/Funnel, Android pairing, one real EC2 node, HTTP/CONNECT and SOCKS traffic, endpoint rotation, update/repair, controller quit/relaunch, reboot/login recovery, and logout fail-closed behavior. Stable promotion remains blocked until the available macOS 26.2 Apple-Silicon Mac completes this record.

Verify links, commands, asset names, and cross-document consistency. Human-facing prose does not receive source-grep tests; validate executable examples through existing script help/fixture tests where applicable. Commit the documentation.

## Final Verification

Run all shared Go and frontend tests on Windows. Run the complete Darwin Go/frontend/build/packaging fixture gates on the Mac only after receiving authorization to install the pinned user-local toolchain. If signing identities/notary credentials are unavailable, report production signing/notarization and physical acceptance as explicit blocked acceptance gates rather than weakening verification. Request whole-branch code review, address its findings once, then use the development-branch finishing workflow without pushing or publishing.
