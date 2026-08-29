# Deployment and release

Mobile Egress is a personal, self-hosted path for selected Windows applications. This guide covers only systems operated by the owner; it does not describe publishing a proxy endpoint or a customer rollout.

## Secure relay initialization

1. Run the read-only prerequisite check: `& .\scripts\preflight.ps1 -Components Docker`.
2. Copy `deploy/.env.example` to the ignored `deploy/.env`. Set the exact externally reachable `RELAY_PUBLIC_NAME` and `RELAY_PUBLIC_URL`; do not place credentials, pairing bundles, certificates, or relay state in that file.
3. Initialize once with `docker compose -f deploy/docker-compose.yml --profile init run --rm relay-init`.
4. Capture the single Owner pairing bundle directly into an owner-controlled password manager. It contains the CA certificate trust anchor and a high-entropy one-use capability, so do not paste it into issue trackers, shell history, chat logs, screenshots, or source control. The private CA remains only in protected relay state.
5. Transfer the bundle only over an authenticated confidential channel to the first owner Windows client. Confirm its exact relay origin and expiry before pairing. The client verifies the bundle CA before sending its capability or CSR and rejects a returned CA mismatch.
6. Start the relay with `docker compose -f deploy/docker-compose.yml up -d relay`. Keep `deploy/data` private, backed up, and outside sync folders. Port 8443 is the encrypted relay endpoint, not a SOCKS service.

The first Windows app uses that Owner invitation to enroll both its Owner identity and its separate local Client identity. It does not require a second Windows profile or a manually transferred Client invitation. For an additional Windows computer, create and confidentially transfer a distinct Client invitation. Treat every transferred invitation as a secret until consumed, then delete the transferred copy.

## Pairing and normal use

Install the signed Android APK manually through an owner-controlled channel. In the Windows app's Phone screen, create the short-lived Agent QR code. On Android, choose **Scan QR**, grant camera permission when prompted, and scan the code. The Android app pairs only by scanning the QR; it has no Agent invitation paste flow. Start the foreground service using **Start**. The notification and UI must say cellular/connected before proxy traffic is attempted. The app binds relay and destination sockets to cellular; it must not use Wi-Fi when cellular disappears.

On Windows, the same app retains Owner controls and starts the loopback SOCKS proxy with its Client identity. Copy its generated `socks5://username:password@127.0.0.1:port` line only into the browser, HTTP client, or other software that should use mobile egress. Do not set a Windows system proxy or alter the default route. Software without that proxy line keeps its ordinary network path. These ordinary app flows use no scripts and no AWS credentials.

Do not promise or expect IP rotation: the Android Agent may reconnect its cellular-bound session, but cannot force the carrier to reset mobile data or assign a different IP.

## Windows and Android releases

Use the package commands only when an artifact is intended:

```powershell
& .\scripts\build-relay-image.ps1
& .\scripts\build-windows.ps1 -Installer
```

The Windows installer is an NSIS artifact produced by Wails in `windows-client\build\bin`; distribute it only through an owner-controlled channel after exercising it on a non-primary Windows profile. WebView2 remains a runtime prerequisite.

For Android, generate and back up a dedicated keystore outside this checkout. Copy `android\keystore.properties.example` to the ignored `android\keystore.properties`, fill it locally, and confirm the file is untracked. First run `& .\scripts\release-android.ps1 -ValidateOnly`, then run `& .\scripts\release-android.ps1`. The script assembles and verifies the APK without echoing `storeFile`, passwords, or aliases. Preserve the keystore and its recovery material in an owner-controlled secret manager; a lost key prevents seamless updates and an exposed key permits a malicious replacement APK.

## Rollback and revocation

Before changing relay images, create a protected backup of `deploy/data`. For a non-compromise operational rollback, deploy the previous image while reusing the same state directory so enrolled identities remain valid. Do not use that procedure when relay state or its CA private key may have been exposed.

For a suspected state/CA compromise, stop the relay and Windows/Android clients, preserve the existing `deploy/data` as a restricted forensic copy outside normal deployment use, and do not restart from it. Initialize an empty replacement state directory to create a fresh CA, bootstrap with a newly generated Owner bundle, then re-pair every Windows and Android identity. Revocation under the old state does not restore trust because the old CA private key may have been copied. Revocation still rejects new sessions and closes active sessions for the active state while an incident is being contained.

## Required physical-device checklist (still required; not executed by automated verification)

Use an Android 10+ phone with a real cellular plan and an owner-controlled Windows machine. Record only aggregate outcomes and redacted error classes.

- [ ] Install the verified signed APK, grant notification permission, use the Windows app to show one freshly issued Agent QR, scan it from Android, and confirm the foreground notification reports cellular/connected.
- [ ] Keep Wi-Fi connected, configure the SOCKS line in exactly one test application, and compare an external IP check: only that proxy-configured software must report the phone carrier IP. An unconfigured application must retain its normal path.
- [ ] With Wi-Fi still present, abruptly disable cellular data or otherwise lose the cellular network. Active proxy streams must close and new proxy requests must fail; no request may fall back to Wi-Fi. Restore cellular and verify a new session reconnects before new traffic succeeds.
- [ ] Confirm the one-phone rule: pair or start a second phone and verify the relay keeps only one active Agent; take the active phone offline and verify the Windows client reports an offline/fail-closed state.
- [ ] Attempt private, loopback, link-local, multicast, and reserved destinations through SOCKS. Each must be denied without making a target connection.
- [ ] Revoke the Windows Client and the Android Agent separately from Owner mode. Verify each loses active access immediately and cannot reconnect. Re-enroll the Android Agent by scanning a freshly generated Agent QR; re-enroll the Windows Client with a newly issued Client invitation.
- [ ] Use a controlled public TCP test destination that returns a response and then EOF. Confirm the complete response arrives before the stream closes.
- [ ] Trigger a local client close and a relay/Agent close at nearly the same time. Confirm the close is idempotent, no spurious protocol violation remains, and unrelated streams continue.
- [ ] Run eight active streams with one continuously producing data. Confirm a quieter stream still progresses; overload one stream and confirm only that stream is rejected or closed without starving the others.
- [ ] Review Windows, Android (`adb logcat`), relay, and container logs. Verify they contain only state, counts, byte totals, and finite redacted errors—never destinations, payloads, proxy credentials, pairing bundles, capabilities, certificates, or keys.
