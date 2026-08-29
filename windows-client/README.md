# Windows client

This Wails v2 application provides an authenticated SOCKS5 listener on `127.0.0.1` for applications explicitly configured to use it. It does not bind a LAN address, install a system proxy, or alter Windows routing.

## Pairing roles

- **Client** identities can establish the relay tunnel and start the local SOCKS proxy.
- **Owner** identities can issue short-lived pairing capabilities for an Android agent or another Windows client and revoke a device by certificate serial. Relay v1 intentionally does not expose an identity-list endpoint.

The first enrollment pins the private relay CA returned by the enrollment endpoint after validating both the returned client certificate and the observed relay server certificate against it. Later health, control, and WebSocket traffic use that pinned CA and the DPAPI-protected client identity.

## Development

Prerequisites are Windows 10/11, Go 1.26+, Node.js 22+, and the [WebView2 Evergreen Runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/). A global Wails installation is not required.

From the repository root:

```powershell
& .\windows-client\scripts\dev.ps1
```

Build the React bundle and packaged Wails executable:

```powershell
& .\windows-client\scripts\build.ps1
```

The executable is created at `windows-client\build\bin\mobile-egress-windows.exe`. The scripts fetch the pinned Wails v2.14.0 CLI with `go run`; the first invocation therefore needs network access.

The UI has no external analytics. Closing its window hides it to the notification tray. The tray's explicit **Quit** action stops the SOCKS listener, closes active relay streams, and exits.
