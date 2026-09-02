# Android Agent

The Android app is the one cellular egress Agent for an operator's local relay. It supports Android 10+ (API 29+) and uses a visible foreground service.

## Enrollment

In the Windows or macOS desktop controller, choose **Agent → Generate Agent QR**. In Android choose **Scan QR**. The strict version-1 payload pins the relay CA and carries a short-lived one-use Agent capability. The app creates a non-exportable P-256 key in Android Keystore, enrolls over a cellular-bound pinned TLS connection, and stores only the resulting identity in encrypted app storage.

Tap **Start** to request a cellular network and connect. The relay uses that cellular `Network`, and every still-unconnected target channel is bound to it before connect. If cellular disappears while Wi-Fi remains available, streams close and reconnection waits for cellular; there is no Wi-Fi fallback.

## Rotate the cellular IP

On a paired, running Agent with cellular available, choose **Rotate cellular IP**. If proxy streams are active, confirm that they may be disconnected. The Agent checks the current public IPv4 and IPv6 through the selected cellular network, closes the relay session, and opens Android's Airplane Mode settings.

1. Turn Airplane Mode **on**. Android requires this manual system-setting action; ZFNF Mobile Egress cannot toggle it for you.
2. Keep it on until the Agent notification's ten-second countdown finishes.
3. Turn Airplane Mode **off**. You may then return to ZFNF Mobile Egress.
4. The Agent waits for cellular, checks the address again, reconnects the relay, and shows **Changed**, **Unchanged**, or **Unverified** with transient before/after values.

If the carrier reused the address, choose **Retry with 30-second reset**. Rotation is best effort: a carrier may return the same public or CGNAT address repeatedly. **Cancel IP rotation** restores the current relay where cellular is still available. If cellular does not disconnect within two minutes, the Agent cancels automatically; if it does not return within three minutes, the Agent continues waiting normally.

Address checks use the public HTTPS endpoints `api.ipify.org` and `api6.ipify.org`. Those requests and their DNS lookups are cellular-bound. Exact addresses are kept only in the live screen; ZFNF Mobile Egress does not persist, log, or copy them into diagnostic status. ipify necessarily receives the request's public address. No root, ADB, accessibility service, VPN, extra Android permission, APN edit, or automatic Airplane Mode control is used.

## Endpoint migration

If the desktop controller rotates to a new Funnel name, it displays an `agent-endpoint-migration` QR. Stop the Agent, choose **Scan QR**, and scan it. Android requires the same stored CA, authenticates to the new endpoint with its existing Agent certificate, consumes the one-use capability, and changes only the stored relay origin. The Android Keystore alias/private key and Agent certificate are retained.

Enrollment and migration payloads are distinct and strict. Unknown fields, insecure/non-origin URLs, invalid/expired capabilities, invalid CAs, different CAs, malformed base64url, and trailing data are rejected.

## Capacity and queueing

The Agent admits at most 256 active streams across all Client identities, and one authenticated Client identity may hold all 256. Admission is first-come across the shared Agent limit. One nonblocking selector reactor binds every still-unconnected target channel to the selected cellular `Network` before connect and handles all partial target I/O, deadlines, cancellation, and closure without per-stream I/O threads.

Senders prefer 16 KiB data frames and accept valid frames up to 32 KiB. Relay-bound and target-bound data each allow 32 retained frames per stream and have separate 8,192-frame/64-MiB session budgets. Reservations remain charged while data is queued or in flight and are refunded only after emission/write completion or discard. The single ordered reactor command queue holds 9,216 commands: data commands may occupy at most 8,192 entries, leaving 1,024 entries available for control, and each selector cycle processes at most 512 commands before returning to target I/O. The outbound control queue holds 512 entries, and closed-stream tombstones are capped at 1,024. Data frames are scheduled round-robin across ready streams. Per-stream, aggregate-frame, or aggregate-byte data saturation closes only the contributing stream; required-control saturation or writer failure closes the affected session instead of allocating without bound.

These larger limits are covered by deterministic admission, mailbox, and fake-reactor unit tests plus ordinary Android build checks. They have not been load-, soak-, memory-, authenticated-harness-, or physical-device-validated; acceptance execution remains pending and was prohibited for this change.

Public-destination policy is applied before opening a cellular target socket. Private, loopback, link-local, multicast, reserved, and otherwise disallowed addresses fail closed.

## Local build

Set JDK 17+ and Android SDK Platform/Build-Tools 35, then run:

```powershell
cd android
.\gradlew.bat testDebugUnitTest lintDebug assembleDebug
```

For a distributable APK, the publisher reuses the ignored `mobile-egress-release.jks` and `keystore.properties`, then runs `scripts\release-android.ps1`. The signed output is named `zfnf-mobile-egress-android-<version>.apk`, and the script also matches the APK signer to the tracked public certificate identity. Do not distribute a debug or unsigned APK as a production Agent, regenerate an established release key, or give signing files to friends. Future agents must follow [the Android signing skill](../.agents/skills/mobile-egress-android-signing/SKILL.md).
