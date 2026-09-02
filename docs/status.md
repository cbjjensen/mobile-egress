# Current status

## Implemented

- Loopback-only Windows relay service, direct CSR Owner bootstrap, Owner-authorized Client CSR provisioning, endpoint leaf rotation, one-use Agent migration, revocation, and multi-Client routing.
- First-come 32-stream per-Client and 256-stream aggregate enforcement with bounded session state and backpressure.
- Self-contained Windows controller flow with distinct absent, installed/offline, and online Tailscale states; duplicate-MSI suppression; connect-only browser/unattended setup; raw TCP Funnel; UAC relay lifecycle; DPAPI Owner/AWS/node state; IAM Identity Center; EC2 inventory; guarded SSM IAM preparation; signed node install/update/repair; a default Refract proxy-line action; and a SOCKS5 fallback action.
- Shared React/Wails and Go desktop controller with a thin Darwin root, native menu-bar lifecycle, unchanged four-tab/backend contract, Security.framework Keychain storage, Service Management relay states/gates, strict relay-admin IPC, verified macOS Tailscale PKG/app onboarding, and macOS `tailscale up` behavior.
- Headless Windows Client service with on-node P-256/X25519 keys, sealed/replay-protected configuration, outbound reconnect, Windows SCM support, and authenticated loopback SOCKS5 plus ordinary-HTTP/HTTPS-CONNECT application opt-ins on the same EC2 node. All modes and two retained idle HTTP streams share one 32-slot relay session; none is a controller-host, system-wide, VPN, public, UDP, or QUIC proxy.
- Android cellular-only foreground Agent with strict enrollment/migration QRs, Android Keystore identity retention, bounded fair queues, 256-stream aggregate admission, guided non-root cellular IP rotation, ZFNF OLED status presentation, safe copied diagnostics, and separate cellular/relay visibility.
- iOS/iPadOS 17+ Agent with VisionKit scanning, Secure Enclave/shared-Keychain identity retention, cellular-required pinned/mTLS relay and target transports, an app-managed on-demand packet tunnel with no included routes, bounded runtime, guided Control Center cellular-IP rotation, ZFNF OLED dashboard/assets, safe copied diagnostics, separate cellular/relay visibility, and the same QR/protocol 32-stream per-Client and 256-stream aggregate limits as Android.
- Versioned mobile parity manifest with tracked Android/iOS source and test evidence for every recorded user-facing capability.
- Windows signing plus deterministic Apple Silicon/macOS 13 staging, Developer ID/notary packaging machinery, strict local verification record, and coupled Desktop release orchestration. The normal public Desktop assets are the Windows controller ZIP, Windows EC2 Client, and macOS PKG at one version. The immutable v1.1.0 exception is exactly Windows and Android; the explicitly approved v1.1.1 proxy hotfix is exactly Windows, reuses the published v1.1.0 Android APK in managed notes, and marks macOS unavailable.
- Windows-to-Mac SSH build-server runbook for Desktop PKG production and separate iOS Agent exact-tree verification.

## Automated validation

The full local gate covers Go unit/integration tests and vet, Windows builds, frontend typecheck/build, Android unit tests/lint/debug APK, PowerShell operation-script tests, strict protocol/crypto cases, AWS/IAM guards, single-controller enforcement, atomic node-capacity reservations/cancellation, partial-install and endpoint-rotation retry, encrypted-state migration, secret redaction, service command construction, hidden background Tailscale CLI launches, stream bounds/fairness, endpoint migration, cellular IP-rotation transitions, public-IP parsing/failure isolation, one-shot settings launch, and diagnostic redaction. Component release gates run the same relevant checks while omitting unrelated Android work from Windows releases and unrelated Windows work from Android releases.

`scripts/test-ios.ps1` runs portable Swift tests on Windows through Docker and then reports Xcode validation as unsupported unless `-UseMacBuildServer` is selected. That selected path requires a clean committed tree, transfers a disposable bundle for that exact commit to the Mac, and runs both Swift suites (including warnings as errors), Xcode project/workspace listing, an unsigned iPhoneOS host-plus-extension build, and Xcode-hosted package tests. Only a known final-package-test `testmanagerd` invalidation is retried, once; a persistent retry failure remains a failed Mac environment result.

The full and mobile component gates validate [the mobile feature manifest](mobile-feature-manifest.json), so a missing platform, unsupported status, or untracked evidence path blocks the gate.

Portable Mac-focused tests cover Keychain policy/adapters, relay framing/authorization/peer-credential selection, Service Management state gates, Tailscale package/app parsing/trust/arguments/cleanup, packaging fixtures, frontend platform copy, release-record validation, and coupled Desktop release contracts. Darwin selection/cross-compilation catches build-tag/composition errors available from the Windows host. These tests do not prove live Security.framework, Service Management/Login Items, root ACL/socket behavior, authentic current Apple/Tailscale chains, Apple Installer, Developer ID/notary service access, SSH transport, or real Funnel/cellular traffic.

Go's race detector is not available in the current Windows environment unless CGO and a supported C compiler are installed. Normal Go tests are still run. See the latest commit/CI output for release evidence.

## Remaining for v1.1.1

The Windows proxy hotfix implementation and focused release/operations contracts are complete. Application proxy listeners and copy values use `127.0.0.2:1080/1081`; managed nodes require a signed Client update to `1.1.1` or later and a fresh copy. The relay/Funnel listener remains `127.0.0.1:8443`. The remaining work is release execution and recorded acceptance:

| Status | Gate | Completion evidence |
|---|---|---|
| Complete | v1.1.1 implementation and focused regression | Runtime and managed-copy tests cover the exact `.2` endpoints and Client version floor; release tests cover the Windows-only scope, two-artifact set, v1.1.0 Android fallback, and macOS unavailability. |
| Deferred | Mac builder preflight | Apple Developer Program enrollment, Developer ID Application/Installer identities, distribution profile, notary profile, and the ignored eight-key `release-desktop.psd1` are not configured. The first Mac artifact must use a version later than v1.1.1. |
| Pending | Native Mac validation | Run the signed [Keychain continuity harness](macos-keychain-integration.md) and exercise native Security.framework, Service Management/Login Items, relay socket ownership/peer authorization, launchd restart, Tailscale package/app validation, and Apple Installer behavior. |
| Approved | Signed Windows hotfix candidate | Run `& .\scripts\release-all.ps1 -ReleaseVersion '1.1.1' -Components Windows` without `-Publish`; verify the Windows ZIP, EC2 Client, hashes, signers, and frozen local tag. Android remains at v1.1.0. |
| Separate approval required | Prerelease publication | Only after explicit publication approval, publish those exact two Windows artifacts with `-Publish`. Release notes must link the published v1.1.0 Android APK and mark macOS unavailable. |
| Pending | v1.1.1 physical acceptance | Complete the Windows/Android two-node regression using the v1.1.1 Windows artifacts and published v1.1.0 Android APK, then record it in the [physical acceptance template](templates/physical-acceptance-record.md). Mac rows are not applicable. |
| Deferred | Future Mac physical acceptance | For the later Mac-bearing version, complete quarantined install, the private upgrade fixture, daemon approval/restart, Keychain continuity, Tailscale login/Funnel, Android pairing, one real EC2 Client, HTTP/CONNECT and SOCKS traffic, rotation/update/repair, reboot recovery, and logout fail-closed behavior. |
| Pending | Stable promotion | Promote the exact prerelease only after every required acceptance row passes. A failure requires a new version; never replace assets or move an existing release tag. |

The detailed commands, stop conditions, and promotion procedure remain in the [release and deployment runbook](deployment.md).

## Required external acceptance

The repository cannot automatically prove real Tailscale browser/Funnel authorization, real AWS IAM/SSM behavior, Windows UAC/service ACLs on clean machines, Android or iOS radio behavior on physical hardware, carrier egress, iOS active-stream rotation and foreground recovery, iOS provisioning, TestFlight upload, or empty-route packet-tunnel acceptance. Android's 256-stream/15-minute physical gates are `PENDING` separately for Windows-hosted and macOS-hosted bridges. iOS capacity remains `unverified—no device`, and TestFlight promotion is deferred.

Mac production acceptance is deferred to a release later than v1.1.1: the Developer ID-signed/notarized exact-commit PKG and private verification record, quarantined install on the available macOS 26.2 Apple-Silicon Mac, Service Management approval/restart, signed [Keychain continuity](macos-keychain-integration.md), Tailscale install/login/Funnel, mobile Agent pairing, one real EC2 Client, HTTP/CONNECT and SOCKS proxy traffic, rotation/update/repair, reboot recovery, and logout fail-closed behavior remain pending/unrun.

Follow the [signed-release and physical-acceptance runbook](deployment.md), preserve the Windows/Android two-node regression, follow the [iOS real-device checklist](../ios/README.md#real-device-acceptance) when iOS is selected, and save a sanitized copy of the applicable [acceptance record](templates/physical-acceptance-record.md) before stable promotion. Complete the Mac one-node checks only for a later Mac-bearing release.

## Known limits

- One operator computer, one relay, and one active Android or iOS Agent are availability dependencies. A Mac additionally requires the controlling administrator to remain logged in with Keychain/per-user Tailscale available; logout fails closed.
- At most ten managed EC2 nodes, 32 streams per Client identity, and 256 total streams through the active Agent.
- Windows 10/11 or Apple Silicon macOS 13+ controller; x86-64 Windows Server 2019 nodes in `us-east-1` only.
- Funnel is beta, requires browser approval, uses public `*.ts.net` names, and has non-configurable bandwidth limits. Personal-plan use must comply with Tailscale terms; commercial/bulk use needs a supported ingress arrangement.
- No automatic GitHub updater. The operator deliberately downloads a signed controller bundle/PKG; node update/repair uses release metadata embedded in that signed controller.
- No Intel/universal Mac build, Windows-to-Mac private-state migration, Mac headless Client, or ZFNF Mac App Store distribution. The first later Mac-bearing release is clean-install-only for a new Mac bridge; same-Mac signed upgrade/repair preserves identities and remains a physical acceptance requirement for that release.
- The Mac build host produces the Desktop PKG and supports iOS simulator/local proof work. TestFlight, App Store, or Ad Hoc distribution requires the applicable paid Apple enrollment.
- Guided IP rotation cannot guarantee carrier reassignment. Android and iOS both require manual Airplane Mode interaction; iOS guides Control Center without changing Airplane Mode or opening a private Settings URL.
- Endpoint migration preserves the CA and identities; it is not recovery from relay-state/CA compromise.
- The app does not create/terminate EC2, open inbound rules, guarantee a carrier IP change, or route all OS traffic.
