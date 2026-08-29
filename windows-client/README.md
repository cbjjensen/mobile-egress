# Windows client

This Wails v2 application provides an authenticated SOCKS5 listener on `127.0.0.1` for applications explicitly configured to use it. It does not bind a LAN address, install a system proxy, or alter Windows routing.

## Pairing identities

- The first Windows installation pastes one confidential Owner invitation. It enrolls and retains both identities in its DPAPI-protected state: an **Owner** identity for issuing and revoking invitations, and a separate **Client** identity for its local SOCKS relay tunnel. These roles are not mutually exclusive on that installation.
- The Windows app creates short-lived Agent invitations only as in-memory QR codes for Android **Scan QR** pairing; it never displays or copies the raw Agent invitation. A distinct Client invitation is needed only for an additional Windows computer. Relay v1 intentionally does not expose an identity-list endpoint.

The first enrollment requires an owner-provided invitation containing the HTTPS relay origin, relay CA certificate, role, expiry, and one-time capability. TLS is verified against that supplied CA before the capability or CSR is sent, and a response carrying a different CA is rejected. Later health and control traffic use the pinned CA and the DPAPI-protected Owner identity; the local SOCKS WebSocket uses the separate DPAPI-protected Client identity.

For local Client recovery, open **Owner**, record the displayed current Client certificate serial, revoke that serial, choose **Replace Client**, then restart and verify the proxy. The app issues and consumes the replacement Client invitation internally; it does not display or copy the invitation. Replacement preserves the Owner identity, and a failed enrollment or protected-state write leaves the previous local Client selected.

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
