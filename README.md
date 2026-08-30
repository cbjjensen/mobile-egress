# Mobile Egress

Mobile Egress is a personal, self-hosted system that lets selected applications on a paired Windows computer use a paired Android phone's cellular connection through a local SOCKS5 proxy.

It deliberately has no public SOCKS listener. Authenticated Agent and Client sessions use TLS with enrolled identities; enrollment and read-only health are bootstrap and observability surfaces, not public proxy access. The Windows app exposes the proxy solely on `127.0.0.1`.

## Terms

- **Owner** — the privileged relay certificate role held by the Windows owner application.
- **Client** — the certificate role used by the local Windows SOCKS tunnel, distinct from the Windows application as a whole.
- **Agent** — the Android certificate role and application that supplies cellular-bound egress.
- **Relay administrator** — the person with relay-host and relay-state access. This can be the Owner, but it has separate operational authority.
- **Agent QR code** — a displayed, short-lived Agent invitation for Android scanning. Treat it as secret material while valid; the Android application has no text fallback.
- **Local SOCKS5 proxy** — the authenticated Windows loopback listener, not an Internet-facing relay service.

## Start with the task you need

- [Deploy the relay](docs/deployment.md)
- [Operate or recover](docs/operations.md)
- [Understand security](docs/security-model.md)
- [Implement the protocol](docs/protocol.md)
- [Develop Windows](windows-client/README.md)
- [Develop Android](android/README.md)
- [Contribute](CONTRIBUTING.md)
- [Review current validation and known limitations](docs/status.md)

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

## Safety boundary

This project is for devices and servers you administer. It allows TCP connections only to public Internet addresses. It rejects loopback, private, link-local, multicast, unspecified, and reserved destinations, and must not be repurposed as a public proxy service.

See [architecture](docs/architecture.md) for components and data flow, [security model](docs/security-model.md) for trust boundaries, and [status](docs/status.md) for the current validation and unsupported workflows. Follow the deployment and operations runbooks for procedures; do not infer an unsupported workflow from this overview.
