# Current status

Reconciled on 2026-09-06 against source commit `209171a`, the recorded validation evidence, and GitHub's published release list. Source metadata, source validation, and released artifacts are reported separately below.

Personal-computer routing is a permanent requirement: keep the owner's local relay and Tailscale Funnel. Hosted/cloud relay alternatives and benchmarks are prohibited; see [AGENTS.md](../AGENTS.md) and [architecture](architecture.md#permanent-personal-computer-routing-requirement).

## Implemented

- Negotiated raw binary data with legacy-peer compatibility, stream-local rejection handling in the Windows Client, and bounded validated destination-address fallback on Android and iOS. Local mixed-traffic p95 improved from about 11 ms to 3.5 ms with eight bulk streams; this is not a cellular measurement. Go and Android checks passed. Exact-commit native Swift tests and unsigned iOS builds completed, but the final Xcode runner remains blocked by the existing `testmanagerd` infrastructure issue. Full evidence and limits are in [latency benchmarks](latency-benchmarks.md#negotiated-binary-framing-and-mixed-traffic-2026-09-05).

- Shared Go tunnel codec and SOCKS/HTTP CONNECT pre-open lifecycle, declarative historical release policy, and removal of the unused custom Mac package-verification stack. The active native `pkgutil`/`spctl` checks remain intact. See the [maintenance implementation and validation record](superpowers/plans/2026-09-06-maintenance-simplification.md).
- Loopback-only Windows relay service, direct CSR Owner bootstrap, Owner-authorized Client CSR provisioning, endpoint leaf rotation, one-use Agent migration, revocation, and multi-Client routing.
- No fixed active-stream ceiling across relay and Clients/Agents; DNS work remains bounded at 256 concurrent workers independently of live connections. Queues, timeouts and historical records remain bounded.
- Self-contained Windows controller flow with distinct absent, installed/offline, and online Tailscale states; duplicate-MSI suppression; connect-only browser/unattended setup; raw TCP Funnel; UAC relay lifecycle; DPAPI Owner/AWS/node state; IAM Identity Center; EC2 inventory; guarded SSM IAM preparation; signed node install/update/repair; a default Refract proxy-line action; and a SOCKS5 fallback action.
- Shared React/Wails and Go desktop controller with a thin Darwin root, native menu-bar lifecycle, unchanged four-tab/backend contract, Security.framework Keychain storage, Service Management relay states/gates, strict relay-admin IPC, verified macOS Tailscale PKG/app onboarding, and macOS `tailscale up` behavior.
- Headless Windows Client service with on-node P-256/X25519 keys, sealed/replay-protected configuration, outbound reconnect, Windows SCM support, and authenticated loopback SOCKS5 plus ordinary-HTTP/HTTPS-CONNECT application opt-ins on the same EC2 node. All modes and up to 16 retained idle HTTP streams (four per host, 60-second timeout) share one relay session without a fixed stream-count ceiling; inbound data allows 32 frames per stream within an 8,192-frame/64-MiB session budget; none is a controller-host, system-wide, VPN, public, UDP, or QUIC proxy.
- Android cellular-only foreground Agent with strict enrollment/migration QRs, Android Keystore identity retention, bounded fair queues, and a target-I/O reactor. Relay-bound and target-bound data each allow 32 frames per stream within separate 8,192-frame/64-MiB lanes, admission has no fixed stream-count ceiling, and data saturation closes only the contributing stream while required-control saturation or writer failure closes the session. The Agent also provides guided non-root cellular IP rotation, ZFNF OLED status presentation, safe copied diagnostics, and separate cellular/relay visibility.
- iOS/iPadOS 17+ Agent with VisionKit scanning, Secure Enclave/shared-Keychain identity retention, cellular-required pinned/mTLS relay and target transports, an app-managed on-demand packet tunnel with no included routes, and the same uncapped active-stream admission plus separate relay-bound/target-bound 32-frame-per-stream, 8,192-frame/64-MiB lane bounds as Android. Its state machine preserves contributing-stream-only data saturation, guided Control Center cellular-IP rotation, ZFNF OLED dashboard/assets, safe copied diagnostics, and separate cellular/relay visibility.
- Versioned mobile parity manifest with tracked Android/iOS source and test evidence for every recorded user-facing capability.
- Windows signing plus deterministic Apple Silicon/macOS 13 staging, Developer ID/notary packaging machinery, strict local verification record, coupled Desktop release orchestration, and a supported Windows-and-Android non-Apple release lane. Desktop assets are the Windows controller ZIP, Windows EC2 Client, and macOS PKG at one version. The Windows-and-Android lane publishes the current Windows ZIP, EC2 Client, and Android APK while marking macOS outside that immutable release scope. Historical exceptions remain encoded in `scripts/release-all.ps1`: v1.1.0 and v1.1.3 require Windows plus Android; v1.1.1 is Windows-only with its pinned v1.1.0 Android download fallback. These exceptions do not describe the current source versions.
- Windows-to-Mac SSH build-server runbook for Desktop PKG production and separate iOS Agent exact-tree verification.

## Automated validation

Latest recorded results:

| Source/change | Verified | Remaining limit |
|---|---|---|
| Transport work, `50802d2` | Go tests/vet/build and targeted race checks; 223 Android tests, lint and debug build; 308 native Swift tests with two expected skips; unsigned iPhoneOS and Simulator builds. | The final Xcode package test runner failed with `com.apple.testmanagerd.control` unavailable, including its retry. Full native iOS validation remains incomplete. |
| Maintenance cleanup, `209171a` | Full Go tests/vet/build; race checks for the shared codec, relay, Client, proxy listeners/helper and Tailscale; release-policy tests; Darwin arm64 Tailscale test cross-compilation and native Mac Tailscale package tests. | Native package tests do not establish real installer, signing, Keychain, service, or physical network acceptance. |
| Local transport benchmarks | Mixed-traffic p95 approximately 11 ms to 3.5 ms with eight bulk streams. | Local fixture only; personal-PC/Funnel/cellular performance remains unmeasured. |

Detailed evidence: [transport validation](superpowers/plans/2026-09-05-personal-pc-transport.md), [maintenance validation and parser cost](superpowers/plans/2026-09-06-maintenance-simplification.md), and [latency measurements](latency-benchmarks.md).

The full local gate covers Go unit/integration tests and vet, Windows builds, frontend typecheck/build, Android unit tests/lint/debug APK, PowerShell operation-script tests, strict protocol/crypto cases, AWS/IAM guards, single-controller enforcement, atomic node-capacity reservations/cancellation, partial-install and endpoint-rotation retry, encrypted-state migration, secret redaction, service command construction, hidden background Tailscale CLI launches, stream bounds/fairness, endpoint migration, cellular IP-rotation transitions, public-IP parsing/failure isolation, one-shot settings launch, and diagnostic redaction. Component release gates run the same relevant checks while omitting unrelated Android work from Windows releases and unrelated Windows work from Android releases.

`scripts/test-ios.ps1` runs portable Swift tests on Windows through Docker and then reports Xcode validation as unsupported unless `-UseMacBuildServer` is selected. That selected path requires a clean committed tree, transfers a disposable bundle for that exact commit to the Mac, and runs both Swift suites (including warnings as errors), Xcode project/workspace listing, an unsigned iPhoneOS host-plus-extension build, and Xcode-hosted package tests. Only a known final-package-test `testmanagerd` invalidation is retried, once; a persistent retry failure remains a failed Mac environment result.

The full and mobile component gates validate [the mobile feature manifest](mobile-feature-manifest.json), so a missing platform, unsupported status, or untracked evidence path blocks the gate.

Stream admission and queue accounting have automated regressions, including more than 1,024 live streams. See [browser throughput measurements](browser-throughput-measurements.md) for exact validation and pending physical workload evidence.

Portable Mac-focused tests cover Keychain policy/adapters, relay framing/authorization/peer-credential selection, Service Management state gates, Tailscale package/app parsing/trust/arguments/cleanup, packaging fixtures, frontend platform copy, release-record validation, and coupled Desktop release contracts. Darwin selection/cross-compilation catches build-tag/composition errors available from the Windows host. These tests do not prove live Security.framework, Service Management/Login Items, root ACL/socket behavior, authentic current Apple/Tailscale chains, Apple Installer, Developer ID/notary service access, SSH transport, or real Funnel/cellular traffic.

Go race checks have passed on this Windows workstation with CGO enabled and the existing GCC toolchain. The [reproduction commands](latency-benchmarks.md#validation) record the temporary compiler location; a compatible compiler and explicit environment setup are required when repeating them. Plain Go tests do not substitute for race checks.

## Source metadata and published releases

| Component | Tracked source metadata at `209171a` | Source |
|---|---|---|
| Windows controller | `1.1.7` | [Wails configuration](../windows-client/wails.json) |
| Android Agent | `1.1.6`, versionCode `20` | [Gradle configuration](../android/app/build.gradle.kts) |
| iOS Agent | `1.1.2`, build `3` | [shared Xcode configuration](../ios/Configuration/Shared.xcconfig) |

GitHub's latest published release at this check is [v1.1.6](https://github.com/cbjjensen/mobile-egress/releases/tag/v1.1.6), published on 2026-09-02 as a **prerelease**. Its assets are `mobile-egress-windows-1.1.6.zip`, `mobile-egress-client.exe`, and `zfnf-mobile-egress-android-1.1.6.apk`; it has no macOS or iOS asset. v1.1.2 is already published and is not a pending release task. No v1.1.7 release appears in the published list.

The September 5–6 transport and maintenance changes are source work after those published artifacts. Current source versions differ across components and do not establish a prepared or verified coordinated release. Before a new release, select the applicable scope, align its version metadata, increase Android's versionCode when selected, and use the guarded [release workflow](../.agents/skills/mobile-egress-release/SKILL.md). Existing tags and assets remain immutable.

## Remaining work

| Status | Gate | Required evidence |
|---|---|---|
| Pending | Physical performance and acceptance | Run the paired browser/bulk workload through the owner's personal-computer relay and Funnel with the real cellular Agent. Record latency, failures, resource use and the applicable two-node regression in the [physical acceptance record](templates/physical-acceptance-record.md). iOS physical throughput remains unverified without a device. |
| Blocked in the last run | Full native iOS validation | Resolve the Mac `testmanagerd` infrastructure failure and rerun the exact-commit Xcode gate. Passing Swift tests and unsigned builds do not close this item. |
| Not established for current source | Next release candidate | Prepare and verify an explicitly selected release scope through the guarded orchestrator; record its source, artifacts, signers and hashes. Older version-specific candidate instructions do not authorize a new release. |
| Pending | Publication and stable acceptance | Publish only the authorized verified candidate; stable promotion requires all acceptance rows applicable to that exact artifact set to pass. Published prereleases are not evidence of completed physical acceptance. |
| Deferred; preflight not rechecked | Mac distribution prerequisites | Earlier records lack Apple signing/profile/notary setup. Verify the current prerequisites before selecting a Mac-bearing release; source tests do not establish distribution readiness. |
| Pending | Mac native product and physical acceptance | Exercise signed Keychain continuity, Service Management, relay socket authorization, launchd restart, actual Tailscale/Installer behavior, upgrade, reboot recovery and logout fail-closed behavior. Retain the signed/notarized PKG and private verification record for the selected release. |

The [deployment runbook](deployment.md) contains detailed acceptance procedures and historical v1.1.2 commands. Those version-specific examples are historical; current metadata and release scope must be reconciled before execution.

## Required external acceptance

The repository cannot automatically prove real Tailscale browser/Funnel authorization, real AWS IAM/SSM behavior, Windows UAC/service ACLs on clean machines, Android or iOS radio behavior on physical hardware, carrier egress, browser-originated target bursts on a physical handset, iOS active-stream rotation and foreground recovery, iOS provisioning, TestFlight upload, or empty-route packet-tunnel acceptance. Android paired browser/soak measurements need an isolated relay and controlled public HTTPS fixture. iOS physical throughput remains `unverified (no device)`; release acceptance is separate from native source validation.

Mac production acceptance remains unrecorded for a Mac-bearing release: the Developer ID-signed/notarized exact-commit PKG and private verification record, quarantined install on the available Apple-Silicon Mac, Service Management approval/restart, signed [Keychain continuity](macos-keychain-integration.md), Tailscale install/login/Funnel, mobile Agent pairing, one real EC2 Client, HTTP/CONNECT and SOCKS proxy traffic, rotation/update/repair, reboot recovery, and logout fail-closed behavior remain pending/unrun.

Follow the [signed-release and physical-acceptance runbook](deployment.md), preserve the Android browser-burst and Windows/Android two-node regression, follow the [iOS real-device checklist](../ios/README.md#real-device-acceptance) when iOS is selected, and save a sanitized copy of the applicable [acceptance record](templates/physical-acceptance-record.md) before stable promotion. Complete the Mac one-node checks only for a later Mac-bearing release.

## Known limits

- One operator computer, one relay, and one active Android or iOS Agent are availability dependencies. A Mac additionally requires the controlling administrator to remain logged in with Keychain/per-user Tailscale available; logout fails closed.
- At most ten managed EC2 nodes; no fixed active-stream count ceiling. Socket availability and device memory remain finite.
- Windows 10/11 or Apple Silicon macOS 13+ controller; x86-64 Windows Server 2019 nodes in `us-east-1` only.
- Funnel is beta, requires browser approval, uses public `*.ts.net` names, and has non-configurable bandwidth limits. Personal-plan use must comply with Tailscale terms; commercial/bulk use needs a supported ingress arrangement.
- No automatic GitHub updater. The operator deliberately downloads a signed controller bundle/PKG; node update/repair uses release metadata embedded in that signed controller.
- No Intel/universal Mac build, Windows-to-Mac private-state migration, Mac headless Client, or ZFNF Mac App Store distribution. The first later Mac-bearing release is clean-install-only for a new Mac bridge; same-Mac signed upgrade/repair preserves identities and remains a physical acceptance requirement for that release.
- The Mac build host produces the Desktop PKG and supports iOS simulator/local proof work. TestFlight, App Store, or Ad Hoc distribution requires the applicable paid Apple enrollment.
- Guided IP rotation cannot guarantee carrier reassignment. Android and iOS both require manual Airplane Mode interaction; iOS guides Control Center without changing Airplane Mode or opening a private Settings URL.
- Endpoint migration preserves the CA and identities; it is not recovery from relay-state/CA compromise.
- The app does not create/terminate EC2, open inbound rules, guarantee a carrier IP change, or route all OS traffic.
