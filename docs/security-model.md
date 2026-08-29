# Security model

## Enrollment

Relay initialization creates a private CA and a one-time owner pairing bundle containing the exact public relay origin, CA certificate trust anchor, owner role, expiry, and high-entropy capability. The private CA remains only in protected relay state; it is not part of a pairing bundle. An enrolled owner Windows app issues the same kind of short-lived, one-use bundle for one Android agent or additional Windows clients. A device verifies enrollment TLS against the bundle CA before transmitting the capability or CSR and rejects a response with a different CA. Pairing capabilities are never six-digit PINs.

Device credentials are certificate-backed. The relay verifies active certificate serials for every persistent session and rejects revoked or role-incompatible identities. Revocation immediately prevents new streams and closes any active session for that identity.

## Network exposure

- The relay exposes an encrypted tunnel endpoint only; it never accepts SOCKS5 directly.
- The Windows SOCKS listener binds only to `127.0.0.1` and requires its generated local username and password.
- The Android agent only initiates outbound connections to the relay and approved targets.
- The agent binds both relay and target sockets to a cellular `Network`; it treats loss of cellular as unavailable, not permission to use Wi-Fi.

## Destination policy

The relay resolves requested hostnames then rejects any candidate address that is loopback, unspecified, private, carrier-grade NAT, link-local, multicast, documentation-only, benchmark, or otherwise non-global-unicast. The Android agent validates the supplied IP again before connecting. SOCKS5 supports `CONNECT` only.

## Logging and privacy

Logs contain connection state transitions, counts, byte totals, status, and redacted error codes. They must not contain DNS names, IPs, URLs, SOCKS authentication, certificates, pairing codes, payload bytes, or HTTP headers.
