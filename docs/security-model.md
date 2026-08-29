# Security model

## Enrollment

Relay initialization creates a private CA and a one-time owner enrollment code. An enrolled owner Windows app can issue short-lived, one-use pairing codes for one Android agent or additional Windows clients. Pairing codes are high-entropy capabilities, never six-digit PINs.

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
