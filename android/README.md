# Android cellular Agent

This directory is a standalone Kotlin/Jetpack Compose Android application for a signed, sideloaded APK. It requires Android 10 or newer (`minSdk 29`), compiles and targets SDK 35, and uses the JDK 17 toolchain. The Gradle wrapper is pinned to Gradle 8.11.1 and verifies the downloaded distribution SHA-256.

## Security boundaries

- Pair only with the exact, immutable base64url Agent bundle supplied by an enrolled owner. The bundle must contain version 1, an HTTPS relay origin, one CA certificate, the `agent` role, a nonempty one-use capability, and an unexpired RFC 3339 expiry.
- Pairing generates a P-256 key in AndroidKeyStore and sends a signed CSR only after TLS verifies against the bundle CA. A returned CA mismatch, certificate/key mismatch, serial mismatch, or non-Agent identity is rejected.
- Pairing CAs must explicitly permit certificate signing. Re-pair commits the new encrypted identity before best-effort removal of the old key; failed old-key cleanup can leave an unreferenced orphan and cannot roll back or delete the new key.
- The private device key is non-exportable. The issued certificate material and relay identity are AES-GCM encrypted with a separate AndroidKeyStore key. The one-use bundle and capability are not persisted.
- The foreground service can start only from the visible app Start action (or its visible notification Stop action), returns `START_NOT_STICKY`, and has no boot receiver. Its `specialUse` declaration documents the personal sideloaded relay use case.
- Relay TLS and every target TCP socket use the selected cellular `Network`. Cellular loss closes the WebSocket and all target streams; an available Wi-Fi path is never selected as fallback.
- Opaque stream data and protocol control frames use separate bounded queues. Immediate controls are serviced first; a normal target EOF reserves its required `close` but sends it only after that stream's accepted data drains. The stream remains addressable until that terminal is emitted; a relay-forwarded Client close first cancels the still-pending data/close sequence, then releases and tombstones the stream without affecting others. Per-stream data bounds and fair scheduling keep a busy stream from consuming another stream's queue share. An overloaded stream discards only its own queued data and closes with finite `agent_unavailable`; if the dedicated control path saturates, the whole WebSocket closes so the relay performs session cleanup.
- UI, notifications, copied status, and application code expose only aggregate state, stream counts, byte totals, and finite error classes. The app does not log payloads, target values, certificates, capabilities, or keys.

## Local build

Install JDK 17 and Android SDK Platform 35/build-tools, then set `JAVA_HOME` and either `ANDROID_HOME` or create an ignored `local.properties` containing `sdk.dir=...`.

```powershell
cd C:\path\to\mobile-egress\android
.\gradlew.bat testDebugUnitTest
.\gradlew.bat assembleDebug
```

The JVM tests cover strict pairing bundle parsing, public-address policy, cellular-only state transitions, foreground-service transitions, protocol frames, and the eight-stream Agent limit.

## Release signing and sideloading

Generate a dedicated keystore outside this repository. `keytool` prompts for passwords so they do not enter shell history:

```powershell
keytool -genkeypair -v -keystore C:\secure\mobile-egress-release.jks -alias mobile-egress -keyalg EC -groupname secp256r1 -validity 3650
Copy-Item .\keystore.properties.example .\keystore.properties
```

Edit the ignored `keystore.properties` with the absolute keystore path and local passwords. Neither file may be committed. Then build and verify the signed APK:

```powershell
.\gradlew.bat clean assembleRelease
& "$env:ANDROID_HOME\build-tools\35.0.0\apksigner.bat" verify --verbose .\app\build\outputs\apk\release\app-release.apk
adb install -r .\app\build\outputs\apk\release\app-release.apk
```

Back up the keystore and passwords in an owner-controlled secret manager. Losing the signing key prevents seamless upgrades; exposing it permits malicious replacement APKs.

## Physical-device smoke checklist

- [ ] Use an Android 10+ physical phone with working cellular data. Keep Wi-Fi enabled during the no-fallback check.
- [ ] Install the verified release APK and grant notification permission when Start requests it.
- [ ] From the owner Windows app, issue a short-lived Agent bundle and transfer it through an authenticated confidential channel.
- [ ] Paste the bundle into the masked field and pair once. Confirm the field clears immediately and the app reports only `paired`.
- [ ] Confirm an expired, Client-role, HTTP-origin, modified-CA, and reused bundle are each rejected without displaying their contents.
- [ ] Tap Start. Confirm the persistent notification appears and the app reaches `cellular / connected`.
- [ ] Start the Windows loopback SOCKS proxy and route one selected test application. Confirm traffic uses the phone carrier address and aggregate bytes/stream count change.
- [ ] Exercise eight simultaneous TCP streams. Confirm they can operate independently and a ninth is rejected without stalling existing streams.
- [ ] Disable cellular data while leaving Wi-Fi connected. Confirm the Agent becomes cellular unavailable, all streams close, and new SOCKS requests fail rather than using Wi-Fi.
- [ ] Re-enable cellular and confirm the relay session reconnects on cellular before new streams succeed.
- [ ] Tap Stop and confirm the notification disappears, active streams become zero, and selected application requests fail closed.
- [ ] Review `adb logcat` while repeating the run. Confirm there are no destinations, payload bytes, pairing bundles, capabilities, certificates, or key material from this application.
