# Mobile Egress iOS Agent

The iOS Agent supports real iPhone and iPad devices running iOS/iPadOS 17 or later. It is not a simulator-only product: enrollment, packet-tunnel provisioning, cellular-path behavior, and TestFlight delivery require a real device and a signed Apple provisioning setup.

The Agent reuses the Android Agent's enrollment and endpoint-migration QRs, `POST /v1/enroll`, `POST /v1/endpoint-migrations/consume`, `GET /v1/session`, binary WebSocket protocol, relay CA pinning, mTLS role, public-target policy, one-active-Agent model, 256-stream per-Client limit, and aggregate 256-stream cap. It uses a non-exportable Secure Enclave P-256 key and a Keychain access group shared by the app and packet-tunnel extension; this is deliberately different from Android's Android Keystore implementation.

The host app presents the approved ZFNF branding in an accessible true-black OLED dashboard. Cellular path health and relay health are separate signals, alongside bounded stream/byte metrics, finite error copy, guided rotation state, and **Copy diagnostic status**. Copied status never includes public addresses, relay origins, certificates, capabilities, opaque network tokens, or raw errors. The tracked [mobile feature manifest](../docs/mobile-feature-manifest.json) is the cross-platform parity ledger for this behavior.

## Capacity and queueing

The asynchronous `NWConnection` runtime admits at most 256 active streams per authenticated Client identity and 256 active streams first-come across the Agent, so one Client may hold every slot. Senders prefer 16 KiB chunks and accept valid inbound data frames through 32 KiB. Relay-bound and target-bound data each allow 32 retained frames per stream and have separate 8,192-frame/64-MiB session budgets. Reservations remain charged while data is queued or in flight and are refunded only after emission/write completion or discard. The runtime also bounds outbound controls at 512 and closed-stream tombstones at 1,024, and schedules ready outbound streams round-robin. Per-stream, aggregate-frame, or aggregate-byte data saturation closes only the contributing stream with the existing finite failure behavior; required-control saturation or writer failure closes the affected session. These boundaries are unit-tested, but they have not been load-, soak-, memory-, authenticated-harness-, or physical-device-validated; that acceptance remains pending and prohibited during this change.

## Guided cellular-IP rotation

Rotation is available only while the Agent is enrolled and running, cellular is available, and no other attempt is active. If streams are active, the app requires native confirmation before it pauses the packet tunnel and on-demand reconnect intent. The normal attempt uses a 10-second cellular reset; an unchanged result offers a 30-second retry. Outcomes are **changed**, **unchanged**, or **unverified**, and copied/logged evidence must never contain the compared addresses.

iOS does not permit this app to toggle Airplane Mode. Follow the dashboard instructions to open Control Center manually, turn Airplane Mode on, wait for the cue/countdown, then turn it off. The app does not open a private Settings URL. The coordinator observes cellular loss/return, resumes when the host app becomes active, and attempts to restore the previous Agent/on-demand intent after completion, cancellation, timeout, or other recoverable failure. A bounded five-minute App Group checkpoint supports foreground recovery after suspension; if the app reports that restoration failed, start the Agent manually before another rotation.

## Prerequisites

- A Mac running a macOS release supported by the current Xcode release, with the current Xcode command-line tools selected.
- Xcode 16 or later with Swift 6 support. The project sets `SWIFT_VERSION = 6.0` and the deployment target to iOS 17.0.
- An Apple Developer Program team that can enable the Packet Tunnel Network Extension capability for the two App IDs described below. Capability availability and approval are controlled through Apple Developer account provisioning; see [Apple's supported-capabilities reference](https://developer.apple.com/help/account/reference/supported-capabilities-ios/).
- A physical iPhone or iPad with iOS/iPadOS 17+ for signing, installation, acceptance, Archive validation, and TestFlight. Do not use a simulator to claim cellular or Network Extension acceptance.

Apple's platform references for this implementation are [Secure Enclave key protection](https://developer.apple.com/documentation/security/protecting-keys-with-the-secure-enclave), [packet-tunnel providers](https://developer.apple.com/documentation/networkextension/nepackettunnelprovider), [VPN routing](https://developer.apple.com/documentation/networkextension/routing-your-vpn-network-traffic), [tunnel-provider management](https://developer.apple.com/documentation/networkextension/netunnelprovidermanager), and [VisionKit scanning](https://developer.apple.com/documentation/visionkit/datascannerviewcontroller).

## Project and identifiers

Open `MobileEgressAgent.xcodeproj`. It deliberately has exactly two product targets:

- `MobileEgressAgent`, the SwiftUI host app with VisionKit QR scanning and the `NETunnelProviderManager` configuration.
- `MobileEgressTunnelExtension`, the packet-tunnel extension that owns the bounded Agent runtime.

Both targets consume the local `MobileEgressCore` Swift package. The package also provides portable unit tests under `Tests/MobileEgressCoreTests`.

The checked-in defaults are development placeholders, not a committed signing identity:

| Setting | Checked-in value | Expansion and release requirement |
| --- | --- | --- |
| Host app bundle ID | `com.mobileegress.agent` | Set `PRODUCT_BUNDLE_IDENTIFIER` to the registered host-app App ID if the product namespace changes. |
| `MOBILE_EGRESS_PROVIDER_BUNDLE_IDENTIFIER` | `com.mobileegress.agent.tunnel` | Expands to the extension's `PRODUCT_BUNDLE_IDENTIFIER`, `NSExtension` provider identity, and host configuration. Register it as the packet-tunnel extension App ID under the same team. |
| `MOBILE_EGRESS_APP_GROUP_IDENTIFIER` | `group.com.mobileegress.agent` | Expands unchanged into both targets' App Group entitlements and must be an App Group enabled for both App IDs. |
| `MOBILE_EGRESS_KEYCHAIN_GROUP_SUFFIX` | `com.mobileegress.agent.shared` | Combines with Xcode's `$(AppIdentifierPrefix)` into `$(AppIdentifierPrefix)com.mobileegress.agent.shared`; Xcode substitutes the selected team's application-identifier prefix. Enable the matching Keychain access group for both App IDs. |
| `$(AppIdentifierPrefix)` | supplied by Xcode provisioning | Do not replace this with a hard-coded Team ID or commit its expanded value. It must resolve identically for the host and extension in the signed archive. |

Before a signed build, register the host and extension App IDs under the same Apple Developer team, enable the same App Group and Keychain access group on both, enable the packet-tunnel Network Extension entitlement for the extension, regenerate/download matching provisioning profiles, and select that team in Xcode or local, ignored build settings. Do not add `DEVELOPMENT_TEAM`, provisioning profiles, Apple credentials, private keys, or signing certificates to this repository.

## On-demand is not Apple Always-On VPN

The host app saves a `NETunnelProviderManager` configuration with `NEOnDemandRuleConnect`, `isOnDemandEnabled`, and `disconnectOnSleep = false`. This is app-managed on-demand persistence intended to let the configured packet tunnel relaunch according to the OS's Network Extension behavior. If the manual start submission fails after that intent is saved, the app makes a best-effort compensating save with on-demand disabled so a reported start failure does not leave an unexpected background activation request behind.

It is **not** Apple's true Always-On VPN. True Always-On VPN requires supervised devices and MDM configuration; a TestFlight-installed app cannot honestly claim that guarantee. Treat sleep/relaunch behavior as real-device acceptance, not as a replacement for supervised-device policy. The tunnel settings intentionally have no included IPv4 or IPv6 routes, so this extension does not turn the device into a general VPN; the Agent's relay and target sockets remain the Mobile Egress workload.

## Development verification

Use the Mac for every iOS compile, simulator, device, signing, Archive, TestFlight, and App Store operation. Windows runs only the portable Swift checks unless it explicitly orchestrates the configured Mac build server.

From the repository root on Windows, run the portable suite and use its explicit unsupported-Xcode result:

```powershell
& .\scripts\test-ios.ps1
```

After committing a clean exact tree, use the maintained Mac build-server workflow:

```powershell
& .\scripts\test-ios.ps1 -UseMacBuildServer
```

`-UseMacBuildServer` defaults to the documented local Mac host and user. `-MacHost`, `-MacUser`, and `-SshKeyPath` are available when the network changes. Every successful run starts from a clean committed tree, records its exact `HEAD`, runs both portable suites, verifies that the tree and `HEAD` did not change, then runs every Mac phase against a detached disposable checkout of that commit. Transfer and execution require the configured identity and an existing matching `known_hosts` entry. The workflow does not update an existing Mac branch/ref or copy an ad hoc source directory, and it has no continuation controls that can combine separately recorded phase results. See [the Mac build-server runbook](../docs/ios-build-server.md).

The Mac suite runs, in order:

1. `swift test` for `MobileEgressCore`.
2. `swift test -Xswiftc -warnings-as-errors`.
3. `xcodebuild -list -project ios/MobileEgressAgent.xcodeproj`.
4. An unsigned `iphoneos` build of the host app and embedded extension with `CODE_SIGNING_ALLOWED=NO`, `CODE_SIGNING_REQUIRED=NO`, and an empty `CODE_SIGN_IDENTITY`.
5. `xcodebuild test -workspace . -scheme MobileEgressCore -destination platform=macOS` for the standalone package workspace, without assuming a simulator model. A known `com.apple.testmanagerd.control` invalidation is retried once only; the retry must pass or the Mac environment remains failed.

On Windows without `-UseMacBuildServer`, the script runs the two portable Swift test commands through Docker's `swift:6.0` image. When they pass, it emits exactly `IOS_PORTABLE_TEST_STATUS=PASSED`, then `IOS_XCODE_STATUS=UNSUPPORTED_HOST`, and exits `20`. Exit `20` means Xcode validation was not available on that host, not that iOS verification succeeded. With `-UseMacBuildServer`, successful portable and Mac suites emit `IOS_XCODE_STATUS=PASSED` and exit `0`. If Docker is absent or either portable suite fails, the script fails normally and does not return the unsupported-host result. On unsupported non-macOS/non-Windows hosts, it emits `IOS_PORTABLE_TEST_STATUS=NOT_RUN` and `IOS_XCODE_STATUS=UNSUPPORTED_HOST`, then exits `20` without running a build.

The unsigned commands verify source/project integration only. They do not prove entitlements, provisioning, device installation, Archive validity, TestFlight processing, or carrier behavior.

## Signing, Archive, and TestFlight

1. On a Mac, select the approved Apple Developer team and matching provisioning for both targets. Confirm the host, extension, App Group, Keychain access group, and packet-tunnel entitlement resolve under the same team prefix.
2. Run `scripts/test-ios.ps1` successfully on macOS before creating a release archive.
3. In Xcode choose the `MobileEgressAgent` scheme and a generic iOS device, then use **Product > Archive**. Resolve any entitlement or provisioning errors; do not work around them by changing checked-in identifiers or committing a Team ID.
4. In Organizer, validate the archive, distribute to App Store Connect, and upload the build. Wait for Apple processing, complete required App Store Connect metadata/compliance, then assign internal or external TestFlight testers.
5. Install the processed TestFlight build on a real iPhone/iPad and execute the acceptance checklist below. Keep the sanitized evidence with the release record.

The asset catalog includes an opaque 1024×1024 Mobile Egress AppIcon and the verification suite checks its catalog reference, dimensions, and PNG color format. Signed asset-archive validation and provisioning remain release work until they are performed against the selected Apple team; the checked-in icon and unsigned project build are not proof of an accepted signed archive.

## Real-device acceptance

Do not mark any item passed based on simulator, package, unsigned-build, or TestFlight upload evidence. Record only sanitized outcomes; never record QR values, capabilities, private keys, certificates, relay URLs, target addresses, traffic contents, or egress IP addresses.

1. Install the signed/TestFlight build on iOS/iPadOS 17+, keep Wi-Fi available and cellular data enabled, scan a fresh enrollment QR, and start the Agent. Require strict enrollment, pinned relay trust, mTLS connection, and a finite in-app error if any requirement fails.
2. With Wi-Fi still available, verify relayed traffic and target sockets use cellular egress. Confirm a public egress result differs from the EC2 node's ordinary direct route without recording either address.
3. With at least one active proxy stream, choose **Rotate cellular IP**. Decline the native confirmation once and verify the stream stays open. Start again, confirm, and verify every active stream closes before rotation proceeds.
4. Follow the iOS Control Center guidance for a normal 10-second attempt. Verify the app observes cellular loss and return, reconnects the relay, and presents only changed, unchanged, or unverified. Inspect copied status and available sanitized logs to confirm neither compared address appears. The app must not toggle Airplane Mode or open a private Settings URL.
5. If the result is unchanged, complete the offered 30-second retry and record only its categorical outcome. If changed or unverified, record that result without an address and do not force the retry path.
6. Exercise cancellation during the waiting/holding path, then separately exercise the two-minute loss timeout and three-minute return timeout. Verify each terminal path attempts to restore the previous Agent/on-demand intent; if restoration fails, require the finite failure and manual Agent start before another rotation.
7. During a separate rotation, background the app while using Control Center and return it to the foreground. Verify the active attempt resumes from observed state or the bounded checkpoint recovery completes, and that completion/cancellation/recoverable failure leaves the Agent and on-demand intent restored.
8. Disable cellular data while leaving Wi-Fi usable. Existing Agent streams must close and new relayed requests must fail with finite errors; no Wi-Fi fallback is allowed. Restore cellular and verify the Agent can reconnect.
9. Trigger controller endpoint migration. Scan the one-use migration QR and require an exact byte-for-byte CA match. Verify only the relay origin changes and the retained Secure Enclave identity/certificate continues to authenticate. A changed CA must fail closed.
10. Stop/start the tunnel, relaunch the app, and restart the device. Verify the expected persisted enrollment and identity behavior. Test the app-managed on-demand/sleep behavior with `NEOnDemandRuleConnect`, `isOnDemandEnabled`, and `disconnectOnSleep = false`; record the actual device result and do not call it Always-On VPN.
11. In a future separately authorized run, have one authenticated Client identity open, verify, and hold all 256 streams. While they remain held, a second authenticated identity's first and only stream attempt must probe aggregate stream 257 and fail with `agent_stream_limit`; after one held stream closes, require the holder to open and verify one replacement. Separately exercise the 32-frame per-stream bound and each directional 8,192-frame/64-MiB budget, including queued-plus-in-flight reservations, refunds after completion or discard, contributing-stream-only data saturation, and session-fatal required-control saturation or writer failure. This two-identity topology and its queue/overload cases are pending and prohibited during this change: do not execute them, record `unverified—no device`, and defer TestFlight promotion. Confirm ordinary direct EC2 traffic remains independent when the authorized physical run eventually occurs.
12. Verify invalid QR input, expired capability, non-public target rejection, unavailable identity, tunnel-settings failure, and runtime failure report finite user-facing errors rather than raw secrets or unbounded diagnostic text.
13. Validate the packet tunnel's intentional empty included-route configuration on the real device. This acceptance remains unverified until it succeeds on signed hardware; neither the unsigned build nor the package tests prove that Apple accepts the empty-route configuration.

Repository and Mac-build-server evidence covers portable Swift suites, warnings-as-errors, project/workspace integration, an unsigned `iphoneos` app-plus-extension build, and Xcode-hosted package tests only when all commands pass at one exact clean commit. It does not cover signing, Archive, TestFlight upload, simulator/device runtime, physical rotation acceptance, or empty-route acceptance.
