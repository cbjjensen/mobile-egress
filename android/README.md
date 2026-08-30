# Android Agent

The Android app is the one cellular egress Agent for an operator's local relay. It supports Android 10+ (API 29+) and uses a visible foreground service.

## Enrollment

In the Windows controller, choose **Phone → Generate Android QR**. In Android choose **Scan QR**. The strict version-1 payload pins the relay CA and carries a short-lived one-use Agent capability. The app creates a non-exportable P-256 key in Android Keystore, enrolls over a cellular-bound pinned TLS connection, and stores only the resulting identity in encrypted app storage.

Tap **Start** to request a cellular network and connect. Every relay and target socket is created through that cellular `Network`. If cellular disappears while Wi-Fi remains available, streams close and reconnection waits for cellular; there is no Wi-Fi fallback.

## Endpoint migration

If the Windows controller rotates to a new Funnel name, it displays an `agent-endpoint-migration` QR. Stop the Agent, choose **Scan QR**, and scan it. Android requires the same stored CA, authenticates to the new endpoint with its existing Agent certificate, consumes the one-use capability, and changes only the stored relay origin. The Android Keystore alias/private key and Agent certificate are retained.

Enrollment and migration payloads are distinct and strict. Unknown fields, insecure/non-origin URLs, invalid/expired capabilities, invalid CAs, different CAs, malformed base64url, and trailing data are rejected.

## Capacity and queueing

The Agent admits at most 32 active streams. Each stream has a bounded inbound queue; outbound control, aggregate data, and per-stream data queues are bounded. Data frames are scheduled round-robin across ready streams. Saturation closes the affected stream or session with a finite error instead of allocating without bound.

Public-destination policy is applied before opening a cellular target socket. Private, loopback, link-local, multicast, reserved, and otherwise disallowed addresses fail closed.

## Local build

Set JDK 17+ and Android SDK Platform/Build-Tools 35, then run:

```powershell
cd android
.\gradlew.bat testDebugUnitTest lintDebug assembleDebug
```

For a distributable APK, the publisher reuses the ignored `mobile-egress-release.jks` and `keystore.properties`, then runs `scripts\release-android.ps1`. The script also matches the APK signer to the tracked public certificate identity. Do not distribute a debug or unsigned APK as a production Agent, regenerate an established release key, or give signing files to friends. Future agents must follow [the Android signing skill](../.agents/skills/mobile-egress-android-signing/SKILL.md).
