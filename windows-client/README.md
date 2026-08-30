# Windows client

Related documents: [security model](../docs/security-model.md), [operations](../docs/operations.md), [deployment and release](../docs/deployment.md), and [current status](../docs/status.md).

This Wails v2 application provides an authenticated SOCKS5 listener for applications explicitly configured to use it. It does not install a system proxy, alter Windows routing, or expose a SOCKS service on the relay.

## Pairing identities

The first Windows installation consumes one confidential Owner invitation and enrolls two distinct relay identities:

| Identity | Purpose | Traffic boundary |
| --- | --- | --- |
| **Owner** | Issues Agent or Client invitations and revokes a known certificate serial. | Never opens the tunnel session and never carries local SOCKS traffic. |
| **Client** | Opens the relay tunnel used by this installation's local SOCKS listener. | The Client only—not the Owner—carries traffic from explicitly proxy-configured applications. |

Both identities, their private material, and the generated SOCKS credentials are stored in Windows-current-user DPAPI-protected state. DPAPI does not protect them from malware in the same interactive user session or an administrator; see the [security model](../docs/security-model.md#windows-state-and-loopback-socks).

The app automatically uses its new Owner to issue and consume the first local Client invitation. It also creates short-lived Agent invitations as in-memory QR images for Android **Scan QR** pairing; the Windows UI does not display or copy the raw Agent invitation. Follow the [security model's QR rules](../docs/security-model.md#qr-and-invitation-handling) because replacing the displayed image is not a revocation operation.

The shipped app does not create or import a Client identity for another Windows computer. Additional-Windows enrollment and multi-Windows deployment are **not supported as an app-first workflow**; they require a maintainer-owned procedure rather than manual transfer of invitation or identity material.

## Local SOCKS behavior

- The listener is IPv4 loopback only: TCP `127.0.0.1` on the selected port (default `1080`). It never binds a LAN address.
- SOCKS5 username/password authentication is mandatory. The UI masks the generated credentials until the user explicitly copies the proxy line.
- `CONNECT` is the only supported SOCKS command. `BIND` and UDP association are rejected.
- One Windows Client admits at most **four local streams**. A fifth request fails without raising that limit.
- Starting the proxy requires the Client identity. The Owner identity cannot substitute when the Client is missing, revoked, or expired.

## Setup, recovery, and tray lifecycle

If initial Owner enrollment succeeds but automatic Client enrollment does not, **Setup** shows Owner ready / Client missing. Use **Retry Windows client setup**; it retains the Owner and requests a fresh local Client enrollment.

The **Owner** view exposes only the current local Client certificate serial. It does not reveal the Owner serial or an Android Agent serial. **Replace Client** is an Owner-authenticated action for an existing local Client: it issues and consumes a fresh Client invitation in memory, commits the replacement only after enrollment and protected-state storage succeed, then stops the previous proxy resources. Use the complete revoke-and-replace sequence in [operations](../docs/operations.md#local-windows-client-recovery); a failed replacement leaves the prior stored Client selected.

Closing the window hides Mobile Egress to the notification tray and leaves the running proxy and relay streams active. The tray can show the window or start/stop the proxy. Only tray **Quit** stops the proxy, closes active relay streams, and exits the application.

## Development

Prerequisites are Windows 10/11, Go 1.26+, Node.js 22+, and the [WebView2 Evergreen Runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/). A global Wails installation is not required.

From the repository root, start development mode:

```powershell
& .\windows-client\scripts\dev.ps1
```

Build the React bundle and packaged Wails executable:

```powershell
& .\windows-client\scripts\build.ps1
```

The executable is created at `windows-client\build\bin\mobile-egress-windows.exe`. The scripts fetch the pinned Wails v2.14.0 CLI with `go run`; the first invocation therefore needs network access. The UI has no external analytics.

## Device acceptance

Automated Go and frontend checks do not prove the Wails runtime, tray behavior, Client-only tunnel identity, loopback authentication, or four-stream behavior on Windows. Exercise those component checks as part of the canonical [required physical-device checklist](../docs/deployment.md#required-physical-device-checklist-still-required-not-executed-by-automated-verification); do not record them as automated verification.
