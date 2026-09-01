# Protocol

All public relay traffic is TLS 1.3 authenticated under the relay-local CA. Funnel raw TCP forwarding does not terminate Mobile Egress TLS. Roles are encoded in client certificates and enforced per endpoint/message.

## Relay commands

- `bootstrap-owner --state-dir ... --public-name ... --public-url ... --owner-csr-file ...` initializes empty state and signs the supplied Owner CSR. Output contains only the Owner certificate chain, CA certificate, serial, and role.
- `rotate-endpoint --state-dir ... --public-name ... --public-url ...` rotates the relay leaf key/certificate under the existing CA and updates the stored origin.
- `serve --state-dir ... --listen 127.0.0.1:8443` runs foreground or through the separate Windows SCM path.
- `--version` prints the release version.

The bundled macOS LaunchDaemon uses the private `daemon` mode with no flags or extra arguments. Its state directory, admin socket, group, and loopback listener are fixed by the signed application; they are not production command-line settings and `daemon` is intentionally omitted from public usage text.

## macOS relay-admin IPC

This is local privileged administration only. It is carried on `/var/run/com.cbjjensen.mobile-egress.relay.sock`, owned `root:admin` with mode `0660`, and is never exposed through Funnel or public relay TLS.

Version 1 uses one strict JSON request and one strict JSON response per bounded frame:

```text
AdminRequest  = version + requestId + operation + optional typed setup/rotate payload
AdminResponse = version + requestId + ok + allowlisted errorCode + optional typed result/status
```

Only `status`, `setup`, `rotate`, and `repair` are accepted. Frames are at most 512 KiB; request IDs carry 128 bits of entropy and the response ID must exactly match; each operation has a five-minute deadline. Unknown versions, operations or fields, malformed/oversized payloads, mismatched IDs, conflicting request-ID reuse, and unauthorized peers fail closed. An exact completed retry receives the cached response and does not repeat the operation.

The daemon authenticates the kernel-reported peer UID. First setup requires a nonzero member of macOS `admin` and records that UID. Later management accepts only the recorded UID or root; root becomes recovery authority only after binding. Responses contain typed public status/results and finite error codes. Owner private keys, AWS credentials, node metadata, relay CA private keys, raw native/daemon errors, proxy secrets, destinations, and traffic payloads are prohibited.

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

Plaintext contains version, a monotonically increasing configuration generation, relay URL, Client role/serial/certificate chain/CA, and SOCKS credentials/port. The node persists the highest accepted generation and a bounded window of accepted-envelope fingerprints for that generation. It rejects replays in that window plus all stale, skipped, or reordered generations. Any valid current-generation envelope outside that window is an idempotent no-op only when its authenticated plaintext exactly matches the persisted configuration; this keeps ambiguous SSM/service-restart retries recoverable without permitting content changes or unbounded state growth. Non-canonical encoding, wrong keys, GCM failure, changed identity material, and invalid certificates fail closed. Endpoint-only updates advance the generation and change only the relay URL while retaining identity and credentials.

## Tunnel session

Binary WebSocket envelopes have finite types: `ping`, `pong`, `open`, `opened`, `data`, and `close`. Clients request public destinations; the relay resolves and rejects private, loopback, link-local, multicast, reserved, and otherwise disallowed addresses before forwarding `open` to the Agent. The Agent independently validates the resolved target.

Every Client has at most four streams. The single Agent/relay aggregate has at most 32. Session maps, queues, frame sizes, tombstones, open timeouts, and idle timeouts are bounded. Agent outbound data is scheduled fairly across ready streams. Close processing is idempotent and backpressure fails the affected stream closed.
