# Security model

Mobile Egress trusts the workload installation owner and the owner of the paired phone. HTTP/CONNECT/SOCKS proxies stay on loopback. Default hosted mode carries the phone's TLS connection through an outbound workload attachment; only Advanced direct mode opens a public workload listener. Neither mode offers an unauthenticated public proxy or remote administration.

## Hosted access boundary

Inevitable browser activation uses expiring PKCE proofs and existing account sessions. A scoped hashed-at-rest device credential authorizes the workload's outer TLS attachment; the phone receives none of it. Pilot grants authorize product access, not a data quota. Mobile Egress submits no traffic bytes, destinations or accounting events. Existing proxy accounting paths remain unchanged.

The gateway terminates only the workload's outer attachment TLS and its private node bridge. Phone TLS, mTLS, pairing capabilities, proxy credentials and application payloads stay encrypted to the workload. Bounded SNI parsing selects a registered route; it cannot request an arbitrary destination. Signed, expiring configuration and current PostgreSQL ownership bind node/process/session generations. Unavailable or expired authorization fails closed; do not treat revocation as instantaneous before the configured cache refresh/freshness boundary. Gateway access revocation and local phone revocation are separate operations.

## Trust and enrollment

Each Client creates a dedicated CA and server certificate in service-protected storage. TLS 1.3 verifies the pinned CA and advertised hostname/IP SAN. The private authority never moves to the phone. Ten-minute invitations are private pairing capabilities; scanning one establishes the Client trust association. Only the exact first-redeeming CSR key may resume issuance. Unknown fields, unsupported bundle types/versions, invalid origins, expired/reused capabilities and conflicting identities reject.

The phone generates a distinct non-exportable P-256 key per pairing in Android Keystore or Secure Enclave/Keychain. It persists pending bootstrap state before sending a CSR, validates the returned certificate/role/key/CA, persists issued identity, then acknowledges via mTLS. Durable admission checks supplement certificate validity. Revoked or unrelated certificates do not gain access simply because they chain to the authority.

Unauthenticated enrollment is bounded by body size, deadline and concurrent admission. All other direct APIs require admitted Agent mTLS. Administration stays behind authenticated local OS IPC. Invitation and explicit proxy-copy operations return sensitive values only at the owner's request; routine status does not.

## Storage and revocation

Windows service secrets use LocalSystem DPAPI. Mac service secrets use a dedicated file-based System Keychain store and restricted native identity access. The Client GUI never reads that store. Upgrades preserve service/signing identities and owner SID/UID. Secure-storage errors fail closed; repair does not replace identities silently.

Revocation commits before reporting success and closes active streams; reconnection is denied. Removing a Client on the phone durably disables reconnection before destroying its association. A new phone needs explicit re-pairing. Renewal requires an admitted existing identity and the same public key; expired/unrecoverable trust requires local re-pairing.

Endpoint update signatures cover exact serialized payload bytes, including mode, with a domain separator. The pinned authority, Client ID, pairing ID and generation must match; skipped generations are allowed, stale/conflicting changes reject. Update payloads cannot change authority, keys or proxy credentials. An explicit capability allows one bounded live update per phone session; QR/file recovery remains available. Desired endpoints remain pending until acknowledged.

## Traffic boundaries

Both the phone tunnel and target sockets require cellular. Cellular loss closes traffic without Wi-Fi fallback. Workload DNS selection and Agent validation prohibit private, loopback, link-local and other non-public destination candidates. Redirects never bypass enrollment pinning. Application credentials are limited to the local proxies; no system-wide routing or UDP/QUIC service is provided.

Resource bounds include data/control admission, native in-flight ownership, handshake deadlines and per-peer isolation. A busy Client cannot acquire another Client's identity or stream namespace. Diagnostic output uses finite error classes and counts, not private keys, capabilities, certificate material, proxy passwords, target addresses or raw HTTP errors.

## Migration and platform availability

Version 2 retires all relay trust as active configuration and requires new pairing. Encrypted recovery material is retained as recovery data, never an automatic fallback. Unknown existing Windows service paths/accounts reject migration. Former controller machines are not automatically exposed as Clients, and unrelated Tailscale/VPN configuration is not removed.

Android background service and iOS foreground sharing have different availability guarantees, explicitly approved by the owner. iOS keep-awake prevents automatic idle locking only. No Network Extension or artificial keepalive is used to imply supported background proxy serving.

See [direct protocol](direct-protocol-v2.md) and [operations](operations.md) for exact recovery behavior. Successful automated tests do not establish signed device acceptance or defeat a compromised local administrator/phone OS.
