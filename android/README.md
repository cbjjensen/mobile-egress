# Android cellular Agent

Related documents: [security model](../docs/security-model.md), [operations](../docs/operations.md), [deployment and release](../docs/deployment.md), and [current status](../docs/status.md).

This directory is a standalone Kotlin/Jetpack Compose Android application for a signed, sideloaded APK. It requires Android 10 or newer (`minSdk 29`), compiles and targets SDK 35, and uses the JDK 17 toolchain. The Gradle wrapper is pinned to Gradle 8.11.1 and verifies the downloaded distribution SHA-256.

## Permissions and user actions

| Manifest permission | Purpose and request boundary |
| --- | --- |
| `INTERNET` | Opens enrollment HTTPS, the authenticated relay WebSocket, and outbound target TCP connections. The app exposes no inbound listener. |
| `ACCESS_NETWORK_STATE` and `CHANGE_NETWORK_STATE` | Requests and observes the cellular `Network` used by the Agent rather than silently falling back to another transport. |
| `CAMERA` | Scans an Agent QR. Runtime camera permission is requested only after the user taps visible **Scan QR**; camera hardware is optional at install time, but the shipped app has no invitation paste flow. |
| `FOREGROUND_SERVICE` and `FOREGROUND_SERVICE_SPECIAL_USE` | Runs the user-started cellular Agent as the declared `specialUse` foreground service. |
| `POST_NOTIFICATIONS` | On Android 13+, the request is initiated only when the user taps visible **Start**, before starting the Agent's status-and-Stop notification. |

## Security boundaries

- Pairing is QR-only. The decoded invitation is never rendered as text, copied to the clipboard, accepted from a paste field, or persisted. It exists only transiently in process memory while the accepted scan is parsed and enrollment completes; only the resulting Agent identity is stored.
- The invitation must be a strict, unpadded base64url version 1 Agent bundle with an HTTPS relay origin, one valid CA certificate, a nonempty one-use capability, and an unexpired RFC 3339 expiry. Pairing generates a P-256 key in AndroidKeyStore and sends a CSR only after invitation-pinned TLS succeeds.
- The private device key is non-exportable. Issued certificate and relay identity material is AES-GCM encrypted with a separate AndroidKeyStore key. A returned CA, certificate/key, serial, or role mismatch is rejected.
- UI, notifications, and **Copy status** expose only aggregate state, counts, byte totals, and finite error classes—not QR values, capabilities, credentials, certificates, keys, destinations, or payloads.

Treat a displayed Agent QR as a live bearer secret until it is consumed or expires. Generating another QR does not revoke an earlier unexpired one; follow the canonical [QR and invitation handling rules](../docs/security-model.md#qr-and-invitation-handling).

## Foreground lifecycle

- The shipped app starts the Agent only after the user taps visible **Start**. It does not start from pairing, app launch, task creation, or a background trigger.
- The service returns `START_NOT_STICKY` and the manifest has no boot receiver. Android does not automatically restart the Agent after a phone reboot or service termination; the user must return to the visible app and tap **Start** again.
- The service declares `android:stopWithTask="false"`. Removing the activity task does not stop an already running Agent; use **Stop** in the app or the notification action when available.
- **Stop** in the app or notification closes the cellular runtime and active session/streams, removes the foreground notification, and stops the service. Destroying the service also tears down the cellular runtime.

## Cellular routing scope

The Agent requests an Android cellular `Network`. Enrollment TLS, relay DNS lookup and TLS/WebSocket sockets, and every target TCP socket are created through that selected network. Loss of the selected cellular network closes the relay session and target streams; Wi-Fi is not selected as a fallback.

This per-socket binding is **not a VPN**. Mobile Egress does not install an Android VPN, alter the phone's default route, or control unrelated phone traffic. Only sockets created by this Agent are cellular-bound; only Windows applications explicitly configured for the local SOCKS proxy send target traffic through them. See the [security model](../docs/security-model.md#android-state-and-routing) for the complete trust boundary.

## Local build

Install JDK 17 plus Android SDK Platform 35 and Build-Tools 35. The repository PowerShell tooling resolves the SDK in this order, using the first nonempty source:

1. `ANDROID_HOME`
2. `ANDROID_SDK_ROOT`
3. `sdk.dir` in ignored `android/local.properties`

Set `JAVA_HOME` to JDK 17. From `android`, run local JVM validation and build a debug APK:

```powershell
.\gradlew.bat testDebugUnitTest
.\gradlew.bat assembleDebug
```

The JVM tests exercise pairing parsing, public-address policy, cellular-only and foreground state transitions, protocol frames, queue behavior, and the eight-stream Agent limit. They do not exercise a physical radio, Android permission UI, task lifecycle, foreground notification behavior, or an installed signed APK.

## Release signing and sideloading

Generate and back up a dedicated release keystore outside the repository. Copy `android/keystore.properties.example` to the ignored `android/keystore.properties` and fill its four required properties locally; never commit either the keystore or its passwords.

From the repository root, [the guarded release script](../scripts/release-android.ps1) is the canonical release path:

```powershell
& .\scripts\release-android.ps1 -ValidateOnly
& .\scripts\release-android.ps1
```

`-ValidateOnly` checks that the required signing inputs exist and that `android/keystore.properties` is untracked and ignored, without printing values. The full `release-android` path additionally runs Android preflight, performs a clean release assembly, and verifies `android/app/build/outputs/apk/release/app-release.apk` with Build-Tools 35 `apksigner`. Direct `gradlew.bat assembleRelease` is not the supported release procedure because it omits those repository guards and final signature verification.

The script does not publish or install the APK. Transfer it through an owner-controlled channel, verify the recorded artifact hash on receipt, and follow the [artifact release](../docs/deployment.md#windows-and-android-releases) and [cross-device acceptance](../docs/deployment.md#pairing-and-cross-device-acceptance) runbooks. Losing the signing key prevents seamless upgrades; exposing it permits a malicious replacement APK.

## Physical-device smoke checklist

These remain device-only or maintained-harness acceptance—not automated verification:

- [ ] Install the recorded signed APK and exercise the visible **Scan QR** camera permission and **Start** notification permission paths.
- [ ] Confirm carrier egress while the phone's default route and unrelated traffic remain unchanged; then remove cellular with Wi-Fi still present and confirm fail-closed behavior with no Wi-Fi fallback.
- [ ] Exercise task removal, explicit app/notification **Stop**, reboot or service termination, and user-initiated restart; confirm the documented foreground lifecycle.
- [ ] Exercise concurrent TCP streams and stream fairness without claiming that the single shipped Windows Client can exceed its four-local-stream limit.

Perform and record these checks through the canonical [required physical-device checklist](../docs/deployment.md#required-physical-device-checklist-still-required-not-executed-by-automated-verification), which combines relay, Windows, Android, signed-install, routing, lifecycle, and stream acceptance. Do not duplicate that checklist in a component release record or claim a device result from unit tests, lint, assembly, or signature verification.
