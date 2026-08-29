# Mobile Egress

Mobile Egress is a personal, self-hosted system that lets selected applications on a paired Windows computer use a paired Android phone's cellular connection through a local SOCKS5 proxy.

It deliberately has no public SOCKS listener. The public relay accepts only encrypted, enrolled agent connections; the Windows app exposes the proxy solely on `127.0.0.1`.

## Repository map

- `docs/` — architecture, security, protocol, operations, analysis, and implementation plan. Read these before changing behavior.
- `relay/` — Go relay service and its tests.
- `windows-client/` — Wails desktop and tray application plus local SOCKS5 listener.
- `android/` — Kotlin/Compose cellular egress agent.
- `deploy/` — portable Docker deployment assets.
- `scripts/` — local build and release checks.

## Development prerequisites

- Go 1.26+
- Docker Engine for relay deployment
- Node.js 22+ and WebView2 for the Windows Wails application
- JDK 17+ and Android SDK Platform 35+ for the Android app

Run the read-only prerequisite detector before building. It does not install software or inspect signing values:

```powershell
& .\scripts\preflight.ps1
& .\scripts\preflight.ps1 -Components Android
```

`MISSING:` means a required tool is not installed or configured; `INVALID:` means a discovered tool failed its validation. The full local gate is explicit and never creates a relay image, Wails executable, installer, or release APK:

```powershell
& .\scripts\test-all.ps1
```

It runs Go test/vet/build, frontend typecheck/build, and Compose configuration validation. Android test, lint, and debug assembly run only after JDK 17+ and Android SDK Platform 35/Build-Tools 35 validate. If those Android prerequisites are absent, the command exits nonzero with remediation rather than reporting partial success.

## Windows client bootstrap

The desktop client supports Windows 10/11 and requires the [Microsoft Edge WebView2 Evergreen Runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/). Startup checks for WebView2 before Wails opens; if it is missing, the app displays a prerequisite dialog instead of failing silently.

The scripts pin Wails v2.14.0 through `go run`, so a global Wails CLI installation is optional:

```powershell
cd C:\path\to\mobile-egress
& .\windows-client\scripts\dev.ps1
& .\windows-client\scripts\build.ps1
```

For a standalone frontend check:

```powershell
cd .\windows-client\frontend
npm install
npm run check
npm run build
```

The production executable is written to `windows-client\build\bin`. Device identity, relay trust, local settings, and generated SOCKS credentials are encrypted for the current Windows user with DPAPI. Closing the window hides it to the notification tray; choosing **Quit** stops the loopback proxy and closes every local stream. The app never configures the Windows system proxy or changes the default route.

## First-time setup and ordinary use

One relay administrator manually installs and initializes the EC2 relay once, then confidentially transfers its one-use Owner invitation to the first Windows installation. The Windows app pastes that invitation, enrolls its protected Owner identity, and automatically enrolls a separate protected Client identity for its local loopback SOCKS service. No AWS credential is entered into either application.

The Windows app then shows a short-lived Agent QR code. Install the signed Android APK manually, scan that code in the Android app, and start the cellular Agent. The code is the only pairing path for Android: do not transfer or paste an Agent invitation. In normal use, start the Android Agent, start the Windows loopback proxy, and put the generated SOCKS line only into the selected Windows application. No scripts are required for that flow.

There are no automatic application updates, public SOCKS endpoint, or promised IP rotation. The Android app can reconnect a cellular-bound session after a network change, but cannot force a carrier IP change or guarantee a new carrier IP.

If the local Windows Client must be revoked and recovered, use the app's Owner screen: record its displayed certificate serial, revoke it, choose **Replace Client**, then restart and verify the proxy. No Client invitation is exposed during this local recovery flow.

## Explicit package and release commands

These commands are intentionally separate from `test-all`:

```powershell
# Build the local relay image only when requested.
& .\scripts\build-relay-image.ps1

# Build the Wails executable, or explicitly request an NSIS installer.
& .\scripts\build-windows.ps1
& .\scripts\build-windows.ps1 -Installer

# Validate ignored, untracked Android signing inputs, then build and verify a signed APK.
& .\scripts\release-android.ps1 -ValidateOnly
& .\scripts\release-android.ps1
```

The Android release command requires `android\keystore.properties` to be ignored and untracked, and never prints its values. Keep the keystore outside the repository. Generated executables and package artifacts are ignored by Git; source files remain visible to Git.

## Safety boundary

This project is for devices and servers you administer. It allows TCP connections only to public Internet addresses. It rejects loopback, private, link-local, multicast, unspecified, and reserved destinations, and must not be repurposed as a public proxy service.

See [architecture](docs/architecture.md), [security model](docs/security-model.md), and [operations](docs/operations.md) before deploying it.

For secure relay initialization, release handling, rollback, and the required physical-device checks, follow [deployment and release](docs/deployment.md) together with [operations](docs/operations.md).
