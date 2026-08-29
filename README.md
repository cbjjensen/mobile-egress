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

The current machine has Go and Node available, but requires JDK 17+ and Android SDK tooling before the Android build can run.

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

## Safety boundary

This project is for devices and servers you administer. It allows TCP connections only to public Internet addresses. It rejects loopback, private, link-local, multicast, unspecified, and reserved destinations, and must not be repurposed as a public proxy service.

See [architecture](docs/architecture.md), [security model](docs/security-model.md), and [operations](docs/operations.md) before deploying it.
