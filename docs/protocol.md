# Protocol

All public relay traffic is TLS 1.3 authenticated under the relay-local CA. Funnel raw TCP forwarding does not terminate Mobile Egress TLS. Roles are encoded in client certificates and enforced per endpoint/message.

## Relay commands

- `bootstrap-owner --state-dir ... --public-name ... --public-url ... --owner-csr-file ...` initializes empty state and signs the supplied Owner CSR. Output contains only the Owner certificate chain, CA certificate, serial, and role.
- `rotate-endpoint --state-dir ... --public-name ... --public-url ...` rotates the relay leaf key/certificate under the existing CA and updates the stored origin.
- `serve --state-dir ... --listen 127.0.0.1:8443` runs foreground or through the separate Windows SCM path.
- `--version` prints the release version.

## Control APIs

| Method/path | Authentication | Purpose |
|---|---|---|
| `GET /healthz` | none | Aggregate readiness/session/stream counters only |
| `POST /v1/enroll` | one-use capability | Agent enrollment and retained compatibility paths |
| `POST /v1/clients` | Owner mTLS | Sign a validated Client CSR; no Client private key is accepted or returned |
| `POST /v1/pairing-codes` | Owner mTLS | Issue short-lived Agent enrollment capability |
| `POST /v1/revoke` | Owner mTLS | Revoke a known certificate serial |
| `POST /v1/endpoint-migrations` | Owner mTLS | Issue one-use migration payload for the current endpoint |
| `POST /v1/endpoint-migrations/consume` | Agent mTLS plus capability | Consume migration and confirm the new relay URL |
| `GET /v1/session` | Client or Agent mTLS | Binary WebSocket tunnel session |

Control JSON is strict, bounded, and rejects unknown/trailing fields. Capabilities are high entropy, stored only as hashes, expire after ten minutes, and are atomically one-use.

## QR formats

QR values are unpadded base64url of strict JSON.

Agent enrollment is version 1 and includes `relayUrl`, `caCertificatePem`, `capability`, `role: "agent"`, and `expiresAt`.

Endpoint migration is intentionally distinct:

```json
{
  "version": 1,
  "type": "agent-endpoint-migration",
  "relayUrl": "https://name.ts.net:8443",
  "caCertificatePem": "...",
  "capability": "...",
  "expiresAt": "..."
}
```

Android requires the QR CA to byte-match its stored CA, then uses its existing mTLS identity over a cellular-bound connection to consume the capability. Only the stored endpoint changes.

## Sealed node configuration

The node's durable X25519 public key is returned at bootstrap. The controller creates an ephemeral X25519 key, derives a 32-byte key with HKDF-SHA256 using protocol context, and encrypts strict configuration JSON with AES-256-GCM and a fresh 96-bit nonce.

```json
{
  "version": 1,
  "ephemeralPublicKey": "<base64url X25519>",
  "nonce": "<base64url 12 bytes>",
  "ciphertext": "<base64url AES-GCM ciphertext+tag>"
}
```

Plaintext contains version, relay URL, Client role/serial/certificate chain/CA, and SOCKS credentials/port. The node rejects unsupported versions, non-canonical sizes/encoding, wrong keys, GCM failure, replayed envelope fingerprints, changed identity material, and invalid certificates. Endpoint-only updates may change only the relay URL while retaining identity and credentials.

## Tunnel session

Binary WebSocket envelopes have finite types: `ping`, `pong`, `open`, `opened`, `data`, and `close`. Clients request public destinations; the relay resolves and rejects private, loopback, link-local, multicast, reserved, and otherwise disallowed addresses before forwarding `open` to the Agent. The Agent independently validates the resolved target.

Every Client has at most four streams. The single Agent/relay aggregate has at most 32. Session maps, queues, frame sizes, tombstones, open timeouts, and idle timeouts are bounded. Agent outbound data is scheduled fairly across ready streams. Close processing is idempotent and backpressure fails the affected stream closed.
