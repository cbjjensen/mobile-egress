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

## Safety boundary

This project is for devices and servers you administer. It allows TCP connections only to public Internet addresses. It rejects loopback, private, link-local, multicast, unspecified, and reserved destinations, and must not be repurposed as a public proxy service.

See [architecture](docs/architecture.md), [security model](docs/security-model.md), and [operations](docs/operations.md) before deploying it.
