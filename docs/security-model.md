# Security model

Related documents: [architecture](architecture.md), [protocol](protocol.md), [deployment](deployment.md), [operations](operations.md), and [current status](status.md).

## Scope and trust assumptions

Mobile Egress protects relay bootstrap, control, and tunnel traffic with TLS 1.3 and a private relay CA. Shipped clients pin that CA instead of relying on the public Web PKI. The authorization model then limits enrolled identities to their persisted role and active certificate serial.

The design assumes that the relay host and the participating Windows and Android user accounts remain under the administrator's control. It does not claim to contain malware already running as the enrolled Windows user, an administrator with endpoint or relay-state access, or an attacker who has copied relay signing material. It is selective TCP egress, not an anonymity service or a device-compromise recovery system.

## Identity and authorization boundaries

The Windows application holds two separate relay identities. They are not interchangeable even though one application manages both:

| Identity | Permitted authority | Explicit boundary |
| --- | --- | --- |
| **Owner** | Issue one-use Agent or Client enrollment capabilities and revoke a known identity by certificate serial | Cannot open a tunnel session and never authenticates the local SOCKS tunnel |
| **Client** | Open a Client WebSocket session and request streams for the local Windows SOCKS proxy | Cannot issue enrollment capabilities or revoke identities |
| **Agent** | Open the single Agent WebSocket session and supply cellular-bound target connections | Has no Owner control authority and cannot act as a Client |
| **Relay administrator** | Access the relay host and persisted state | An operational role, not a certificate role; it remains distinct even when the same person is also the Owner |

The relay TLS listener requests a client certificate but permits a connection without one so that bootstrap and health can work. This is only a TLS transport boundary:

- `POST /v1/enroll` is certificate-free but requires a valid capability whose stored role matches the requested identity role.
- `GET /healthz` is certificate-free and read-only. It returns aggregate readiness, connection, stream, byte, and finite error counters.
- Owner control endpoints require a verified certificate whose serial is present, active, and persisted as Owner.
- The tunnel endpoint requires an active Client or Agent identity; Owner is forbidden. Session admission repeats the active-role check, and the relay rechecks identity status before processing every inbound session message.

Consequently, completing TLS verification with an enrolled certificate does not by itself grant control or tunnel authority. The application-level serial, revocation state, and role checks remain mandatory.

## Enrollment and CA pinning

Relay initialization creates the private CA, relay certificate, SQLite state, and one initial Owner capability. An active Owner can later issue Agent or Client capabilities. Each capability is generated from 32 random bytes, is bound to exactly one role, currently expires after ten minutes, and can be redeemed once. SQLite stores its SHA-256 hash, role, creation time, expiry, and consumption state rather than the raw capability. Consumption and identity creation occur in one database transaction.

An invitation contains the exact HTTPS relay origin, relay CA certificate, requested role, expiry, and raw bearer capability. Before sending the capability or CSR, the shipped Windows and Android clients establish TLS using only the invitation CA. They also reject an enrollment response that returns a different CA or a certificate that does not match the locally generated key, requested role, and response serial. The invitation is therefore both the bootstrap trust input and secret enrollment authority; it is not a short numeric PIN.

The `relay init` command intentionally writes the initial encoded Owner invitation to stdout once. This is the sole intended secret-output exception: capture it only in an administrator-controlled terminal and do not place it in shared logs, shell transcripts, screenshots, tickets, or documentation. Relay state retains only the capability hash.

## QR and invitation handling

The Windows UI renders an Agent invitation as an in-memory QR image and its expiry; it does not expose invitation text. The visible QR still contains the complete bearer invitation. Anyone who captures it can attempt Agent enrollment until the capability is redeemed or expires.

Do not screen-share, stream, record, or screenshot a valid Agent QR, and keep untrusted cameras out of view. Generating or displaying another QR issues another capability; it does **not** revoke an earlier unexpired QR. Routine Agent re-pairing also does not revoke the previously enrolled Agent identity.

## Stored secrets and local adversaries

### Relay state

The relay state directory includes the CA private key, relay private key, certificates, and SQLite database. The database contains capability hashes plus identity, revocation, last-seen, and aggregate metric state. These files are not application-encrypted at rest. Host filesystem permissions, administrator access control, disk protection, and backup custody are therefore part of the security boundary. Backups and restored copies require protection equivalent to the live state directory.

### Windows state and loopback SOCKS

Windows persists Owner and Client identities and generated SOCKS credentials with DPAPI for the current Windows user. This protects copied state from a different ordinary user context; it is not a guarantee against malware running as the same user, a compromised interactive session, or an administrator. Owner and Client private material must still be treated as secret.

The SOCKS5 listener binds only to IPv4 loopback (`127.0.0.1`), requires the generated username and password, accepts `CONNECT` only, and does not change the Windows default route. Loopback binding prevents network exposure from other hosts; it is not a security boundary against same-user malware or an administrator on the Windows machine.

### Android state and routing

Android generates the Agent signing key in AndroidKeyStore and persists certificate and relay identity material encrypted with a separate AndroidKeyStore key. The invitation capability is not persisted after pairing.

The Agent requests a cellular Android `Network`. Enrollment TLS, relay DNS and TLS/WebSocket connections, and target TCP sockets are individually created through that selected network. This is per-socket cellular binding, **not a VPN**: the application does not install an Android VPN, alter the phone's default route, or control unrelated phone traffic. Loss of the selected cellular network closes the Agent session and streams rather than authorizing Wi-Fi fallback.

## Network exposure and destination policy

The public relay listener serves TLS-protected enrollment, read-only health, Owner control, and authenticated Client/Agent WebSocket sessions. It never accepts SOCKS5 and is not a public proxy endpoint. The Android Agent initiates outbound relay and target connections; it exposes no inbound proxy listener.

The relay resolves Client-requested hostnames and rejects the request if any returned address violates its public-TCP policy. Rejected categories include loopback, unspecified, private, carrier-grade NAT, link-local, multicast, documentation-only, benchmark, and other non-global-unicast ranges. IPv6 literals must fall within the allowed public range after special and tunneling ranges are excluded. The Agent independently validates the relay-supplied literal IP before connecting.

## Revocation scope and compromise boundary

Owner revocation marks one known certificate serial as revoked, rejects its future authenticated requests, and closes its active session and streams if connected. It does not invalidate other identities or unredeemed enrollment capabilities. Creating a replacement identity or QR does not implicitly revoke the prior identity or capability.

Relay v1 has no identity-list endpoint, and Android does not display its certificate serial, so targeted lost-phone revocation is not a shipped app-first workflow. See [current status](status.md#known-limitations) for the explicit limitation; do not infer a recovery command that is not documented.

Certificate revocation cannot remediate exposure of the relay CA/private key or compromise of the relay state directory or its backups. Those events cross the trust boundary and require an administrator-led recovery decision; this document intentionally does not invent a recovery procedure that the current product does not implement.

## Logging and privacy

Application logs, diagnostics, examples, screenshots, and support artifacts must not contain invitation payloads, capability values, SOCKS authentication, private keys, certificates, payload bytes, DNS names, target IPs, URLs, or HTTP headers. The one documented exception is the initial Owner invitation written to stdout by successful `relay init`; treat that output as a secret, not as ordinary logging.

The relay health response and persisted metrics are aggregate only: readiness, connection and stream counts, total streams, byte count, and finite redacted error-code counts. Tunnel payloads remain opaque to the relay routing layer and must never be added to operational logging.

## Implementation anchors

- Relay initialization and capability hashing: [`relay/internal/service/init.go`](../relay/internal/service/init.go) and [`relay/internal/service/store.go`](../relay/internal/service/store.go)
- Enrollment, Owner authorization, and revocation: [`relay/internal/service/enrollment.go`](../relay/internal/service/enrollment.go) and [`relay/internal/service/session.go`](../relay/internal/service/session.go)
- TLS listener and read-only health: [`relay/internal/service/service.go`](../relay/internal/service/service.go)
- Intentional initialization output: [`relay/cmd/relay/main.go`](../relay/cmd/relay/main.go)
- Windows identity separation, DPAPI, and loopback SOCKS: [`windows-client/internal/client/core.go`](../windows-client/internal/client/core.go), [`windows-client/internal/securestore/dpapi_windows.go`](../windows-client/internal/securestore/dpapi_windows.go), and [`windows-client/internal/socks/server.go`](../windows-client/internal/socks/server.go)
- Android CA pinning and per-socket cellular binding: [`android/app/src/main/java/com/mobileegress/agent/security/PinnedTls.kt`](../android/app/src/main/java/com/mobileegress/agent/security/PinnedTls.kt), [`android/app/src/main/java/com/mobileegress/agent/service/AgentForegroundService.kt`](../android/app/src/main/java/com/mobileegress/agent/service/AgentForegroundService.kt), and [`android/app/src/main/java/com/mobileegress/agent/session/AgentSession.kt`](../android/app/src/main/java/com/mobileegress/agent/session/AgentSession.kt)
