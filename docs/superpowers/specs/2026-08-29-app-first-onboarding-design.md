# App-First Onboarding Design

## Goal

Make ordinary Mobile Egress use graphical after a trusted administrator has installed the relay on EC2 once. A single Windows installation owns the relay, pairs one Android agent with a QR code, and provides a loopback SOCKS5 endpoint to selected Windows applications.

## Scope

- The relay remains a manually installed, owner-operated EC2 service.
- The relay administrator transfers its one-use Owner invitation as text through a confidential channel.
- The Windows app pastes that invitation, securely keeps an Owner identity and a separate Client identity, and automatically enrolls the Client identity for itself.
- The Windows app creates short-lived Agent invitations and displays them only as QR codes.
- The Android app scans an Agent QR code, pairs, and continues to bind relay and target sockets to cellular.
- Windows and Android application installation remains a trusted manual process using the existing Windows installer and signed Android APK.

## Non-goals

- No AWS credential handling or EC2 provisioning in the desktop application.
- No public relay SOCKS listener or remote-client proxy endpoint.
- No automatic application update channel.
- No mobile IP rotation promise or control. A normal Android application can reconnect its own cellular-bound session but cannot force carrier mobile-data resets or guarantee a new carrier IP.

## Design

### Identities and first run

The Windows secure store keeps independent Owner and Client identities in one DPAPI-protected generation. The first-run action accepts only an Owner invitation. It enrolls the Owner identity, saves it, issues a Client invitation through the authenticated Owner control API, enrolls that Client identity, and saves both identities.

If Client enrollment fails after the Owner enrollment succeeds, the Owner identity remains usable and the UI offers a retry action that issues a fresh Client invitation. The consumed invitation itself is never retained. Existing single-identity state migrates on load: existing Client data becomes the Client identity; existing Owner data becomes the Owner identity and requires Client setup.

### Desktop experience

The setup screen accepts an Owner invitation by paste and reports only finite, redacted error classes. Once the Owner identity exists, the Phone screen issues one Agent invitation, encodes it as an in-memory QR image, and hides it after its expiry or successful replacement. The raw Agent invitation is neither displayed nor copied.

The proxy screen remains a local, authenticated SOCKS5 listener at `127.0.0.1`. It starts only when the Client identity is available and uses that identity for the relay session. Owner controls remain available on the same desktop installation for Agent invitation issuance and revocation.

### Android experience

The Android pairing screen offers Scan QR. The scanner requests camera permission only after the user taps it. Decoded text is passed directly to the strict existing pairing parser and cleared from UI state before asynchronous enrollment. The app does not log, render, copy, or persist a raw invitation. Invalid, expired, wrong-role, or unreadable codes yield a generic rejection state.

### Security invariants

- Owner and Client private keys remain encrypted with DPAPI; Android private keys remain non-exportable in Android Keystore.
- Pairing invitations remain one-use, expiry-bound, and unpersisted.
- The relay remains TLS-pinned and never exposes a public proxy endpoint.
- QR images contain secrets and therefore have a bounded expiry, no text fallback in the desktop UI, and no disk-backed rendering.
- The Android agent retains fail-closed cellular-only routing; Wi-Fi never becomes fallback transport.

## Acceptance criteria

1. A relay administrator can provide one Owner invitation; the Windows UI becomes both Owner-ready and Client-ready without a second Windows profile or manual Client invitation transfer.
2. The Windows UI can display an expiring Agent QR code; the Android app pairs from it without a paste field.
3. The Windows app starts the local SOCKS proxy with its Client identity while retaining Owner controls.
4. Existing encrypted single-role identity state migrates without exposing keys or SOCKS credentials.
5. Android scanner failure, permission denial, invalid QR, expired QR, and enrollment failure expose no raw secret.
6. Full automated checks pass, and a physical-device smoke test verifies phone-cellular egress and no Wi-Fi fallback.
