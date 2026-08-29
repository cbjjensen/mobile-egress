# Mobile Egress Design

## Goal

Provide a self-hosted, selective SOCKS5 route from paired Windows applications through one paired Android phone's cellular connection, without exposing either the phone or a public proxy endpoint.

## Required behavior

- Windows proxy access is loopback-only and authenticated locally.
- Only SOCKS5 `CONNECT` is supported.
- Relay and Android accept public Internet TCP destinations only.
- Android must never fall back to Wi-Fi when the configured path requires cellular.
- Pairing and revocation are owner-controlled and certificate-backed.
- Status includes only aggregate health/counters, never destination or payload data.

## Architecture

The relay is an independently deployable Go container with persistent SQLite and CA state. Windows and Android maintain authenticated WebSocket-over-TLS sessions. The relay validates an outbound request, asks the Android agent to create the cellular socket, and multiplexes bytes between the paired sessions.

## Scope boundary

The v1 target is a sideloaded Android APK, one active Android agent, and Windows 10/11 desktop clients. It does not support production customer traffic, alternate desktop platforms, UDP, arbitrary inbound forwarding, Wi-Fi fallback, a web administration dashboard, or destination-level audit history.
