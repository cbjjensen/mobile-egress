# Mobile Egress iOS Agent — direct Clients

The iPhone opens one authenticated cellular WebSocket per enabled Client, for at most ten records including pending and disabled Clients. Default Inevitable Gateway mode carries the pinned end-to-end TLS connection through the workload's outbound attachment; no router forwarding or phone account is needed. Advanced direct requires reachable workload ingress. Applications use local HTTP/CONNECT or SOCKS proxies and exit through cellular. There is no personal relay, AWS management, Tailscale/Funnel dependency or automatic fallback.

## Foreground lifecycle

Keep this app active and the phone unlocked while sharing. Leaving the app, opening an interruption that makes every app scene inactive, locking the phone, or entering the background closes Client sessions and target sockets. Returning active reconnects enabled Clients only when the saved Start intent remains enabled. Stop clears that intent. A reboot does not provide unattended service: the user must unlock and open the app.

**Keep screen awake while sharing** defaults on and is persisted. The app disables its idle timer exactly while this preference, Start intent, and active scene state are all true, including temporary connection retries. It restores normal auto-lock on Stop, preference off, inactive/background state, or a terminal sharing failure. It never changes brightness or system auto-lock settings. The dashboard shows both sharing and auto-lock state. This is foreground availability, not a background-execution mechanism.

Android uses a foreground service; iOS serves only in the active foreground. This is the owner-approved availability exception. Pairing, per-Client control, cellular-only egress, protocol, endpoint updates, rotation, and safe diagnostics remain shared product behavior.

## Pairing and maintenance

Create an invitation in the workload Client app and scan or paste it into the phone. Invitations have a ten-minute initial redemption window. The phone saves its per-Client Secure Enclave key and CSR before sending enrollment, then saves the issued identity before acknowledgement. Interrupted enrollment and lost acknowledgement remain visible as pending; **Retry pairing** reuses the same key. Pending and disabled records occupy one of the ten slots.

Updated builds accept the Client's smaller compact QRs and older plain setup codes, restoring identical bytes before normal trust validation. Older iOS builds need the complete text setup code instead. Existing pairings remain unchanged. See the [compact QR repair](../docs/superpowers/plans/2026-10-05-compact-pairing-qr.md); native parser/build checks do not replace signed physical iPhone acceptance.

An expired invitation releases its local slot and key when the durable record proves enrollment was never attempted, or the pinned enrollment endpoint definitively reports that the same invitation is invalid or expired. The phone records an attempt before sending; an uncertain, malformed or unrelated rejection keeps that same key available for recovery. Confirmed rejection removes the pending record durably before deleting its unissued key, and cannot remove a replaced invitation or issued identity. Generate a fresh invitation on the workload Client after confirmed rejection. Remove explicitly cancels a local pending record. Issued identities and pending acknowledgements never expire as invitation reservations.

Each association has separate trust and credentials in the existing shared Keychain access group. The direct registry is one atomically updated Keychain document. Keys use AfterFirstUnlockThisDeviceOnly, so they are unavailable before the first unlock after reboot and do not migrate to another device through backup. The retired extension never reads direct records.

The phone receives signed mode/endpoint updates through the authenticated session and polls configuration on connection and every 30 seconds while active. It verifies and saves before reconnecting. Updates bind Client ID, pairing ID, generation, mode and endpoint; skipped generations work and stale/conflicting updates reject. Older records default direct; neither mode can substitute trust or keys. QR/file import recovers offline peers. Pending acknowledgement stays visible. Renewal preserves the existing key; expired credentials require fresh pairing. Mobile Egress submits no traffic-usage accounting to Inevitable.

Disable pauses a Client without deleting credentials. Remove closes its sessions and deletes its local association. Revoke the pairing in the workload Client before pairing another phone; local deletion does not pretend to perform a remote administrative revocation. Status copies contain counts and finite states, never capabilities, keys, certificates, addresses, or raw network errors.

Per-Client recovery distinguishes rejected/expired credentials, clock errors, inaccessible phone storage, unavailable Client storage and transient network/TLS failures. A saved Client needing local attention remains visible with **Retry**; other Clients continue. Unlock the phone or correct its clocks before retrying. Re-pair when credentials or trust are unusable, not merely because a network attempt timed out. The app never deletes trust automatically. See [operations and recovery](../docs/operations.md).

Removal immediately suppresses that Client before credential storage is changed. An atomic app-container journal containing only Client IDs preserves this distrust if Keychain deletion fails: ordinary refresh, Start and relaunch cannot reconnect it. The pending row offers Remove again to finish cleanup; other Clients remain available. If even the journal cannot be saved, the Client stays blocked in the current process and the app explicitly warns that removal could not be preserved across relaunch. Keep the app open and retry after freeing storage. A successful cleanup or new explicit pairing is required to clear removal intent.

After credentials and their registry record are durably deleted, removal is complete even if erasing the obsolete journal marker fails. Markers for absent records are pruned during reconstruction and before another removal, so housekeeping failures cannot consume the ten-Client limit or create an unavailable retry action.

The exact wire contract is [direct protocol v2](../docs/direct-protocol-v2.md).

## Legacy upgrade cleanup

The first direct major release retains the existing app/extension bundle IDs, App Group, Keychain access group, and Network Extension provisioning scaffold. The extension is cleanup compatibility only: every start fails closed and it creates no network connection or tunnel settings.

Before direct Start, the app loads VPN preferences, selects only configurations whose provider bundle identifier exactly matches its own retired extension, disables on-demand and the profile, stops the session, waits for a disconnected/invalid state, removes it, and reloads preferences to confirm absence. Other VPNs are untouched. If any operation fails or removal cannot be confirmed, direct Start is blocked and the app directs the user to Settings → General → VPN & Device Management to remove the old Mobile Egress profile.

The old relay identity remains protected as recovery data but is never used by the new entrypoint. Fresh direct pairing is required. Removal of the compatibility extension/entitlements is deferred until signed-upgrade acceptance establishes a safe migration.

## Capacity and rotation

The phone shares separate inbound and outbound 8,192-frame / 64-MiB data budgets across all Client sessions and replacement generations. Queued data and native transport debt retain budget leases until their owners release them. Each stream retains its 32-frame limit; control work is also bounded. Idle Clients reserve no data quota, and a single Client can use otherwise idle aggregate capacity. Each session sends one frame at a time, with ready streams scheduled round-robin. Aggregate saturation retains stream-local overload failure; it is not a new end-to-end flow-control protocol.

Across Clients, already-readable target chunks take FIFO peer turns, one chunk before returning a busy peer to the tail. The encoded chunk owns its shared budget before waiting, and cancellation removes its turn. A turn never spans an idle native receive or waits for a network send. Capacity admission remains first-come under total saturation; fair routing cannot create capacity when all retained frames are still in flight.

Target downloads pause before the next native receive when that stream reaches its outbound allowance. Waiting retains no payload; cancellation unblocks it. Existing exact-byte and EOF behavior is preserved.

Rotation is phone-wide. It confirms disruption of active streams, pauses all Client sessions, preserves enabled peers and Start intent, and uses the existing cellular observer, public-address checks, and durable bounded checkpoint. Follow the Airplane Mode instructions; the app does not change Airplane Mode itself. Live rotation work suspends with the foreground app and resumes from its checkpoint on activation. After process relaunch, sharing remains gated until checkpoint recovery reconstructs any active pause. Explicit Stop must not be undone by rotation completion, and disabled Clients stay disabled. The dashboard starts a ten-second attempt, or the required 30-second retry after an unchanged address.

For reliable guided rotation, turn Airplane Mode on in Control Center, return to Mobile Egress while it remains on, and follow the countdown. Then turn Airplane Mode off and return to the app. Cellular transitions delivered while inactive are retained for recovery, but iOS may suspend the app before delivering them. Missing observations are never treated as proof that a rotation occurred; retry the guided sequence when the app could not observe the disconnect. Sharing and live probes remain paused while inactive.

The dashboard reports cellular availability separately from sharing intent and each Client's connection state. A cellular connection does not prove the workload endpoint is reachable, and a saved Start intent does not mean traffic is currently connected.

## Build and signing

The app bundles `MobileEgressAgent/PrivacyInfo.xcprivacy` for the required-reason APIs it uses. `AgentViewModel` keeps the explicit Stop latch in the app's `UserDefaults` (`CA92.1`) and gives `UserDefaultsNotificationFirstUseStore` the existing App Group defaults for the rotation notification prompt (`1C8F.1`). `NetworkTargetConnection` uses `ProcessInfo.systemUptime` only for elapsed connection-attempt deadlines (`35F9.1`); it does not send the device's boot time. These reasons follow [Apple's required API categories](https://developer.apple.com/documentation/bundleresources/app-privacy-configuration/nsprivacyaccessedapitypes/nsprivacyaccessedapitype) and [approved reasons](https://developer.apple.com/documentation/bundleresources/app-privacy-configuration/nsprivacyaccessedapitypes/nsprivacyaccessedapitypereasons).

The manifest declares these API uses only; it does not substitute for App Store Connect privacy or encryption answers. Release binary inspection confirmed that the retired extension retains `UserDefaultsNotificationFirstUseStore` and `NetworkTargetConnection` from the linked core library, including the preference and `systemUptime` calls. Both app and extension bundles therefore receive the same manifest. The extension's entrypoint still fails closed and does not serve traffic. The project-structure test checks both resource memberships; distribution validation must also inspect the Release archive's embedded products and privacy report.

The deployment target remains iOS/iPadOS 17+, with Swift 6 strict concurrency. Both targets use the local MobileEgressCore package. Identifiers stay:

- App: com.mobileegress.agent
- Retired extension: com.mobileegress.agent.tunnel
- App Group: group.com.mobileegress.agent
- Keychain suffix: com.mobileegress.agent.shared, expanded with the provisioned AppIdentifierPrefix

Use the Mac build server for Swift, Xcode, simulator, and device work. Follow [the build-server runbook](../docs/ios-build-server.md). The maintained exact-commit gate is:

    .\scripts\test-ios.ps1 -UseMacBuildServer

Native component commands from ios/:

    swift test
    swift test -Xswiftc -warnings-as-errors
    xcodebuild test -workspace . -scheme MobileEgressCore -destination "platform=macOS"
    xcodebuild -project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build
    xcodebuild -project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -sdk iphonesimulator -destination "generic/platform=iOS Simulator" CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build

Do not commit signing keys, profiles, Apple accounts, expanded team identifiers, or local SSH configuration. Installing profiles, changing developer-account state, device installation, Archive, TestFlight, and publication require separate authorization.

## Acceptance and evidence limits

Unit tests and unsigned builds do not establish signed-device behavior. Before release, a signed physical iPhone must demonstrate:

- Ten direct Clients concurrently, an eleventh rejected, cellular-only peer and target sockets with Wi-Fi present, and HTTP/CONNECT/SOCKS exact traffic.
- Default-on keep-awake in the real app, preference off, Stop, retries, inactive/background/lock transitions, app switching, relaunch, thermal/battery behavior, and no brightness change.
- Upgrading the old signed app with an active on-demand profile: only the owned profile is stopped/disabled/removed, failed cleanup blocks Start, and the compatibility extension never resumes old serving.
- Entitled Keychain/Secure Enclave availability, first-unlock behavior, independent keys, interrupted pairing, lost ACK, rotation, removal/revocation, certificate renewal, and signed endpoint updates after several missed generations.
- Slow Client/target pressure, fair progress across peers, bounded memory including old native callbacks, repeated cancel/reconnect, and exact final bytes/EOF.

Apple documents the limits of [ordinary iOS background execution](https://developer.apple.com/forums/thread/685525) and excludes proxy hosting from [packet-tunnel use cases](https://developer.apple.com/documentation/technotes/tn3120-expected-use-cases-for-network-extension-packet-tunnel-providers).
